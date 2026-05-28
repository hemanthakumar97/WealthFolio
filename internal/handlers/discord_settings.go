package handlers

import (
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hemanthakumar97/wealthfolio/internal/services"
)

// DiscordSettingsHandler manages GET/PUT for Discord webhook + alert settings,
// and a POST /test endpoint to verify the webhook is working.
type DiscordSettingsHandler struct {
	pool *pgxpool.Pool
	svc  *services.DiscordService
}

func NewDiscordSettingsHandler(pool *pgxpool.Pool) *DiscordSettingsHandler {
	return &DiscordSettingsHandler{pool: pool, svc: services.NewDiscordService(pool)}
}

type discordSettingsResponse struct {
	MaskedURL         string  `json:"masked_url"`
	Enabled           bool    `json:"enabled"`
	DrawdownThreshold float64 `json:"drawdown_threshold"`
	Configured        bool    `json:"configured"`
	// Additional alert types
	MoverAlertEnabled bool    `json:"mover_alert_enabled"`
	MoverThreshold    float64 `json:"mover_threshold"`
	ATHAlertEnabled   bool    `json:"ath_alert_enabled"`
	LTCGAlertEnabled  bool    `json:"ltcg_alert_enabled"`
	LTCGThresholdPct  float64 `json:"ltcg_threshold_pct"`
	MoodAlertEnabled  bool    `json:"mood_alert_enabled"`
}

type discordSettingsPutRequest struct {
	WebhookURL        string  `json:"webhook_url"`
	Enabled           bool    `json:"enabled"`
	DrawdownThreshold float64 `json:"drawdown_threshold"`
	// Additional alert types
	MoverAlertEnabled bool    `json:"mover_alert_enabled"`
	MoverThreshold    float64 `json:"mover_threshold"`
	ATHAlertEnabled   bool    `json:"ath_alert_enabled"`
	LTCGAlertEnabled  bool    `json:"ltcg_alert_enabled"`
	LTCGThresholdPct  float64 `json:"ltcg_threshold_pct"`
	MoodAlertEnabled  bool    `json:"mood_alert_enabled"`
}

func (h *DiscordSettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.svc.GetSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toDiscordResponse(cfg))
}

func (h *DiscordSettingsHandler) Put(w http.ResponseWriter, r *http.Request) {
	var req discordSettingsPutRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.WebhookURL = strings.TrimSpace(req.WebhookURL)
	if req.DrawdownThreshold <= 0 || req.DrawdownThreshold > 100 {
		req.DrawdownThreshold = 10.0
	}

	cfg := &services.DiscordSettings{
		WebhookURL:        req.WebhookURL,
		Enabled:           req.Enabled,
		DrawdownThreshold: req.DrawdownThreshold,
		MoverAlertEnabled: req.MoverAlertEnabled,
		MoverThreshold:    req.MoverThreshold,
		ATHAlertEnabled:   req.ATHAlertEnabled,
		LTCGAlertEnabled:  req.LTCGAlertEnabled,
		LTCGThresholdPct:  req.LTCGThresholdPct,
		MoodAlertEnabled:  req.MoodAlertEnabled,
	}
	if err := h.svc.SaveSettings(r.Context(), cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Re-read to get any stored URL back for masking.
	saved, err := h.svc.GetSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toDiscordResponse(saved))
}

func (h *DiscordSettingsHandler) Test(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.SendTestMessage(r.Context()); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "test message sent"})
}

func toDiscordResponse(cfg *services.DiscordSettings) discordSettingsResponse {
	masked := ""
	if cfg.WebhookURL != "" {
		u := cfg.WebhookURL
		if len(u) > 20 {
			masked = u[:20] + strings.Repeat("•", 12) + u[len(u)-8:]
		} else {
			masked = strings.Repeat("•", len(u))
		}
	}
	mt := cfg.MoverThreshold
	if mt == 0 {
		mt = 3.0
	}
	lp := cfg.LTCGThresholdPct
	if lp == 0 {
		lp = 80.0
	}
	return discordSettingsResponse{
		MaskedURL:         masked,
		Enabled:           cfg.Enabled,
		DrawdownThreshold: cfg.DrawdownThreshold,
		Configured:        cfg.WebhookURL != "",
		MoverAlertEnabled: cfg.MoverAlertEnabled,
		MoverThreshold:    mt,
		ATHAlertEnabled:   cfg.ATHAlertEnabled,
		LTCGAlertEnabled:  cfg.LTCGAlertEnabled,
		LTCGThresholdPct:  lp,
		MoodAlertEnabled:  cfg.MoodAlertEnabled,
	}
}
