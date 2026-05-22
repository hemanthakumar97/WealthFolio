package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hemanthakumar97/wealthfolio/internal/auth"
)

type AuthHandler struct {
	pool        *pgxpool.Pool
	signer      *auth.Signer
	production  bool
	uploadDir   string
	profileMaxB int64
}

func NewAuthHandler(pool *pgxpool.Pool, signer *auth.Signer, production bool, uploadDir string) *AuthHandler {
	return &AuthHandler{
		pool:        pool,
		signer:      signer,
		production:  production,
		uploadDir:   uploadDir,
		profileMaxB: 5 << 20, // 5 MB
	}
}

type userResponse struct {
	ID             int64   `json:"id"`
	Email          string  `json:"email"`
	Username       *string `json:"username,omitempty"`
	ProfilePicture *string `json:"profile_picture,omitempty"`
}

type setupRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Username string `json:"username"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *AuthHandler) Status(w http.ResponseWriter, r *http.Request) {
	count, err := h.userCount(r.Context())
	if err != nil {
		slog.Error("auth status", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"needs_setup": count == 0,
		"message":     "ok",
	})
}

func (h *AuthHandler) Setup(w http.ResponseWriter, r *http.Request) {
	var req setupRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || len(req.Password) < 6 {
		writeError(w, http.StatusBadRequest, "email and a 6+ character password are required")
		return
	}

	count, err := h.userCount(r.Context())
	if err != nil {
		slog.Error("setup count", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if count > 0 {
		writeError(w, http.StatusConflict, "setup already completed")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not hash password")
		return
	}

	var (
		id        int64
		username  *string
		picture   *string
		createdAt time.Time
	)
	usernameArg := nullableString(req.Username)
	err = h.pool.QueryRow(r.Context(),
		`INSERT INTO users (email, username, password_hash)
		 VALUES ($1, $2, $3)
		 RETURNING id, username, profile_picture, created_at`,
		req.Email, usernameArg, hash,
	).Scan(&id, &username, &picture, &createdAt)
	if err != nil {
		slog.Error("setup insert", "err", err)
		writeError(w, http.StatusInternalServerError, "could not create user")
		return
	}

	if err := h.issueSession(w, id, req.Email); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sign token")
		return
	}
	writeJSON(w, http.StatusCreated, userResponse{
		ID: id, Email: req.Email, Username: username, ProfilePicture: picture,
	})
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password required")
		return
	}

	var (
		id       int64
		hash     string
		username *string
		picture  *string
	)
	err := h.pool.QueryRow(r.Context(),
		`SELECT id, password_hash, username, profile_picture FROM users WHERE email = $1`,
		req.Email,
	).Scan(&id, &hash, &username, &picture)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		slog.Error("login select", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if !auth.VerifyPassword(hash, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if err := h.issueSession(w, id, req.Email); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sign token")
		return
	}
	writeJSON(w, http.StatusOK, userResponse{
		ID: id, Email: req.Email, Username: username, ProfilePicture: picture,
	})
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	auth.ClearSessionCookie(w, h.production)
	writeJSON(w, http.StatusOK, map[string]string{"message": "logged out"})
}

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var (
		email    string
		username *string
		picture  *string
	)
	err := h.pool.QueryRow(r.Context(),
		`SELECT email, username, profile_picture FROM users WHERE id = $1`, id.UserID,
	).Scan(&email, &username, &picture)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, userResponse{
		ID: id.UserID, Email: email, Username: username, ProfilePicture: picture,
	})
}

// UpdateMe updates the current user's email and/or username.
type updateMeRequest struct {
	Email    string `json:"email"`
	Username string `json:"username"`
}

func (h *AuthHandler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req updateMeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	sets := []string{}
	args := []any{}
	add := func(col string, val any) {
		args = append(args, val)
		sets = append(sets, col+" = $"+itoa(len(args)))
	}
	if e := strings.TrimSpace(strings.ToLower(req.Email)); e != "" {
		add("email", e)
	}
	if u := strings.TrimSpace(req.Username); u != "" {
		add("username", u)
	}
	if len(sets) == 0 {
		writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}
	sets = append(sets, "updated_at = NOW()")
	args = append(args, id.UserID)

	sql := "UPDATE users SET " + strings.Join(sets, ", ") +
		" WHERE id = $" + itoa(len(args)) +
		" RETURNING id, email, username, profile_picture"

	var (
		uid     int64
		email   string
		uname   *string
		picture *string
	)
	if err := h.pool.QueryRow(r.Context(), sql, args...).Scan(&uid, &email, &uname, &picture); err != nil {
		if strings.Contains(err.Error(), "unique") {
			writeError(w, http.StatusConflict, "email already in use")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, userResponse{ID: uid, Email: email, Username: uname, ProfilePicture: picture})
}

// ChangePassword verifies the current password and sets a new one.
type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req changePasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(req.NewPassword) < 6 {
		writeError(w, http.StatusBadRequest, "new password must be at least 6 characters")
		return
	}

	var hash string
	if err := h.pool.QueryRow(r.Context(),
		`SELECT password_hash FROM users WHERE id = $1`, id.UserID,
	).Scan(&hash); err != nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}
	if !auth.VerifyPassword(hash, req.CurrentPassword) {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	_, err = h.pool.Exec(r.Context(),
		`UPDATE users SET password_hash = $1, updated_at = NOW() WHERE id = $2`, newHash, id.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "password updated"})
}

// UploadProfilePicture handles multipart profile picture uploads.
func (h *AuthHandler) UploadProfilePicture(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if err := r.ParseMultipartForm(h.profileMaxB); err != nil {
		writeError(w, http.StatusBadRequest, "file too large or invalid form")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file field required")
		return
	}
	defer file.Close()

	if header.Size > h.profileMaxB {
		writeError(w, http.StatusBadRequest, "file exceeds 5 MB limit")
		return
	}

	// Validate MIME by extension.
	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowed := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true}
	if !allowed[ext] {
		writeError(w, http.StatusBadRequest, "only JPEG, PNG, GIF, WebP allowed")
		return
	}

	// Save file.
	dir := filepath.Join(h.uploadDir, "profile_pictures")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create upload dir")
		return
	}
	filename := fmt.Sprintf("%s%s", uuid.New().String(), ext)
	dst, err := os.Create(filepath.Join(dir, filename))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save file")
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		writeError(w, http.StatusInternalServerError, "write error")
		return
	}

	picPath := "/uploads/profile_pictures/" + filename
	var (
		uid   int64
		email string
		uname *string
	)
	if err := h.pool.QueryRow(r.Context(),
		`UPDATE users SET profile_picture = $1, updated_at = NOW()
		 WHERE id = $2
		 RETURNING id, email, username`, picPath, id.UserID,
	).Scan(&uid, &email, &uname); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, userResponse{ID: uid, Email: email, Username: uname, ProfilePicture: &picPath})
}

func (h *AuthHandler) userCount(ctx context.Context) (int, error) {
	var n int
	err := h.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (h *AuthHandler) issueSession(w http.ResponseWriter, userID int64, email string) error {
	token, err := h.signer.Sign(userID, email)
	if err != nil {
		return err
	}
	auth.SetSessionCookie(w, token, h.production)
	return nil
}

