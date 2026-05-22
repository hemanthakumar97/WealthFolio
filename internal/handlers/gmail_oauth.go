package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	gmailv1 "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hemanthhku/wealthfolio-v2/internal/services"
)

const (
	settingGmailRefreshToken = "gmail_refresh_token"
	settingGmailEmail        = "gmail_email"
	gmailOAuthState          = "wealthfolio-gmail"
)

type GmailOAuthHandler struct {
	pool        *pgxpool.Pool
	frontendURL string
	watcher     *services.GmailWatcher
}

func NewGmailOAuthHandler(pool *pgxpool.Pool, frontendURL string, watcher *services.GmailWatcher) *GmailOAuthHandler {
	return &GmailOAuthHandler{
		pool:        pool,
		frontendURL: frontendURL,
		watcher:     watcher,
	}
}

// TestRun runs a dry-run of the Gmail watcher and returns what would be imported.
// POST /api/gmail/test
func (h *GmailOAuthHandler) TestRun(w http.ResponseWriter, r *http.Request) {
	if h.watcher == nil {
		writeError(w, http.StatusServiceUnavailable, "Gmail watcher is not configured")
		return
	}
	results, err := h.watcher.DryRun(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}

func (h *GmailOAuthHandler) oauthConfig(r *http.Request) *oauth2.Config {
	// Build redirect URI from the incoming request's host so it works on any port.
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	redirectURI := scheme + "://" + r.Host + "/api/gmail/callback"

	var clientID, clientSecret string
	_ = h.pool.QueryRow(r.Context(), `SELECT value FROM app_settings WHERE key = 'gmail_client_id'`).Scan(&clientID)
	_ = h.pool.QueryRow(r.Context(), `SELECT value FROM app_settings WHERE key = 'gmail_client_secret'`).Scan(&clientSecret)

	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       []string{gmailv1.GmailReadonlyScope},
		Endpoint:     google.Endpoint,
		RedirectURL:  redirectURI,
	}
}

// Status returns Gmail integration status.
// GET /api/settings/gmail
func (h *GmailOAuthHandler) Status(w http.ResponseWriter, r *http.Request) {
	var clientID, clientSecret string
	_ = h.pool.QueryRow(r.Context(), `SELECT value FROM app_settings WHERE key = 'gmail_client_id'`).Scan(&clientID)
	_ = h.pool.QueryRow(r.Context(), `SELECT value FROM app_settings WHERE key = 'gmail_client_secret'`).Scan(&clientSecret)

	configured := clientID != "" && clientSecret != ""

	token, _ := h.getSetting(r.Context(), settingGmailRefreshToken)
	email, _ := h.getSetting(r.Context(), settingGmailEmail)

	var maskedSecret string
	if len(clientSecret) > 8 {
		maskedSecret = clientSecret[:8] + strings.Repeat("•", 8)
	} else if clientSecret != "" {
		maskedSecret = strings.Repeat("•", len(clientSecret))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"configured":    configured,
		"connected":     token != "",
		"email":         email,
		"client_id":     clientID,
		"masked_secret": maskedSecret,
	})
}

type saveGmailCredentialsRequest struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// SaveCredentials updates Gmail client credentials in the DB.
// PUT /api/settings/gmail
func (h *GmailOAuthHandler) SaveCredentials(w http.ResponseWriter, r *http.Request) {
	var req saveGmailCredentialsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	clientID := strings.TrimSpace(req.ClientID)
	clientSecret := strings.TrimSpace(req.ClientSecret)

	if clientID == "" {
		writeError(w, http.StatusBadRequest, "client_id is required")
		return
	}

	if err := h.setSetting(r.Context(), "gmail_client_id", clientID); err != nil {
		writeError(w, http.StatusInternalServerError, "save client id: "+err.Error())
		return
	}

	// Only update secret if a non-empty, non-masked one is provided
	if clientSecret != "" && !strings.Contains(clientSecret, "•") {
		if err := h.setSetting(r.Context(), "gmail_client_secret", clientSecret); err != nil {
			writeError(w, http.StatusInternalServerError, "save client secret: "+err.Error())
			return
		}
	}

	h.Status(w, r)
}

// Connect initiates the OAuth2 flow by redirecting to Google.
// GET /api/gmail/connect
func (h *GmailOAuthHandler) Connect(w http.ResponseWriter, r *http.Request) {
	var clientID, clientSecret string
	_ = h.pool.QueryRow(r.Context(), `SELECT value FROM app_settings WHERE key = 'gmail_client_id'`).Scan(&clientID)
	_ = h.pool.QueryRow(r.Context(), `SELECT value FROM app_settings WHERE key = 'gmail_client_secret'`).Scan(&clientSecret)

	if clientID == "" || clientSecret == "" {
		writeError(w, http.StatusBadRequest, "Gmail Client ID and Client Secret must be configured in Settings first")
		return
	}
	cfg := h.oauthConfig(r)
	url := cfg.AuthCodeURL(gmailOAuthState, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	http.Redirect(w, r, url, http.StatusFound)
}

// Callback handles the OAuth2 redirect from Google, stores the refresh token.
// GET /api/gmail/callback
func (h *GmailOAuthHandler) Callback(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("state") != gmailOAuthState {
		writeError(w, http.StatusBadRequest, "invalid state")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "missing code")
		return
	}

	cfg := h.oauthConfig(r)
	tok, err := cfg.Exchange(r.Context(), code)
	if err != nil {
		slog.Error("gmail oauth exchange", "err", err)
		writeError(w, http.StatusInternalServerError, "token exchange failed: "+err.Error())
		return
	}

	if err := h.setSetting(r.Context(), settingGmailRefreshToken, tok.RefreshToken); err != nil {
		writeError(w, http.StatusInternalServerError, "save token: "+err.Error())
		return
	}

	// Fetch the user's Gmail address to display in the UI.
	ts := cfg.TokenSource(r.Context(), tok)
	svc, err := gmailv1.NewService(r.Context(), option.WithTokenSource(ts))
	if err == nil {
		if profile, err := svc.Users.GetProfile("me").Context(r.Context()).Do(); err == nil {
			_ = h.setSetting(r.Context(), settingGmailEmail, profile.EmailAddress)
		}
	}

	slog.Info("gmail oauth: connected successfully")

	// Redirect back to the frontend settings page.
	http.Redirect(w, r, h.frontendURL+"/settings?tab=integrations&gmail=connected", http.StatusFound)
}

// Disconnect removes the stored Gmail tokens.
// DELETE /api/settings/gmail
func (h *GmailOAuthHandler) Disconnect(w http.ResponseWriter, r *http.Request) {
	_ = h.deleteSetting(r.Context(), settingGmailRefreshToken)
	_ = h.deleteSetting(r.Context(), settingGmailEmail)
	writeJSON(w, http.StatusOK, map[string]bool{"disconnected": true})
}

// RefreshToken returns the stored refresh token (used by GmailWatcher at runtime).
func (h *GmailOAuthHandler) RefreshToken(ctx context.Context) string {
	tok, _ := h.getSetting(ctx, settingGmailRefreshToken)
	return tok
}

// --- DB helpers ---

func (h *GmailOAuthHandler) getSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := h.pool.QueryRow(ctx,
		`SELECT value FROM app_settings WHERE key = $1`, key,
	).Scan(&value)
	return value, err
}

func (h *GmailOAuthHandler) setSetting(ctx context.Context, key, value string) error {
	_, err := h.pool.Exec(ctx,
		`INSERT INTO app_settings (key, value, updated_at)
		 VALUES ($1, $2, NOW())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		key, value,
	)
	return err
}

func (h *GmailOAuthHandler) deleteSetting(ctx context.Context, key string) error {
	_, err := h.pool.Exec(ctx, `DELETE FROM app_settings WHERE key = $1`, key)
	return err
}
