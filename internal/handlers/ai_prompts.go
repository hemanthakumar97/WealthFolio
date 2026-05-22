package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AIPromptsHandler struct {
	pool *pgxpool.Pool
}

func NewAIPromptsHandler(pool *pgxpool.Pool) *AIPromptsHandler {
	return &AIPromptsHandler{pool: pool}
}

type aiPromptItem struct {
	Key       string    `json:"key"`
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (h *AIPromptsHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT key, content, updated_at FROM ai_prompts ORDER BY key`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	var items []aiPromptItem
	for rows.Next() {
		var item aiPromptItem
		if err := rows.Scan(&item.Key, &item.Content, &item.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []aiPromptItem{}
	}
	writeJSON(w, http.StatusOK, items)
}

type aiPromptUpdateRequest struct {
	Content string `json:"content"`
}

func (h *AIPromptsHandler) Update(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}

	var req aiPromptUpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeError(w, http.StatusBadRequest, "content must not be empty")
		return
	}

	var item aiPromptItem
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO ai_prompts (key, content, updated_at)
		     VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE
		        SET content    = EXCLUDED.content,
		            updated_at = NOW()
		  RETURNING key, content, updated_at
	`, key, strings.TrimSpace(req.Content)).Scan(&item.Key, &item.Content, &item.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}
