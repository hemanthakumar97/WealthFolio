package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type AISettingsHandler struct {
	pool *pgxpool.Pool
}

func NewAISettingsHandler(pool *pgxpool.Pool) *AISettingsHandler {
	return &AISettingsHandler{pool: pool}
}

type aiSettingsResponse struct {
	Provider   string `json:"provider"`   // "anthropic" | "openai" | "antigravity" | ""
	Model      string `json:"model"`      // e.g. "claude-sonnet-4-6"
	MaskedKey  string `json:"masked_key"` // first 8 chars + "••••••••" — never the real key
	Configured bool   `json:"configured"`
}

type aiSettingsPutRequest struct {
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"`
	Model    string `json:"model"`
}

func (h *AISettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	provider, key, model, err := loadAIConfig(r.Context(), h.pool)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := aiSettingsResponse{
		Provider:   provider,
		Model:      model,
		Configured: provider != "" && key != "",
	}
	if len(key) > 8 {
		resp.MaskedKey = key[:8] + strings.Repeat("•", 8)
	} else if key != "" {
		resp.MaskedKey = strings.Repeat("•", len(key))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *AISettingsHandler) Put(w http.ResponseWriter, r *http.Request) {
	var req aiSettingsPutRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	provider := strings.TrimSpace(req.Provider)
	key := strings.TrimSpace(req.APIKey)
	model := strings.TrimSpace(req.Model)

	if provider != "" && provider != "anthropic" && provider != "openai" && provider != "antigravity" {
		writeError(w, http.StatusBadRequest, "provider must be anthropic, openai, or antigravity")
		return
	}

	if err := saveAIConfig(r.Context(), h.pool, provider, key, model); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := aiSettingsResponse{
		Provider:   provider,
		Model:      model,
		Configured: provider != "" && key != "",
	}
	if len(key) > 8 {
		resp.MaskedKey = key[:8] + strings.Repeat("•", 8)
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─── Shared DB helpers ────────────────────────────────────────────────────────

func loadAIConfig(ctx context.Context, pool *pgxpool.Pool) (provider, key, model string, err error) {
	rows, err := pool.Query(ctx,
		`SELECT key, value FROM app_settings WHERE key IN ('ai_provider', 'ai_api_key', 'ai_model')`)
	if err != nil {
		return "", "", "", err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return "", "", "", err
		}
		switch k {
		case "ai_provider":
			provider = v
		case "ai_api_key":
			key = v
		case "ai_model":
			model = v
		}
	}
	return provider, key, model, rows.Err()
}

func saveAIConfig(ctx context.Context, pool *pgxpool.Pool, provider, key, model string) error {
	// Always persist provider and model.
	_, err := pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at)
		     VALUES ('ai_provider', $1, NOW()), ('ai_model', $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
	`, provider, model)
	if err != nil {
		return err
	}
	// Only overwrite the API key when the caller supplies a non-empty one,
	// so a model-only update does not wipe the stored key.
	if key != "" {
		_, err = pool.Exec(ctx, `
			INSERT INTO app_settings (key, value, updated_at) VALUES ('ai_api_key', $1, NOW())
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
		`, key)
	}
	return err
}
