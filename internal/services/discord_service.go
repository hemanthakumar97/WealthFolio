package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DiscordSettings holds all Discord webhook + alert configuration.
type DiscordSettings struct {
	WebhookURL        string  `json:"webhook_url"`
	Enabled           bool    `json:"enabled"`
	DrawdownThreshold float64 `json:"drawdown_threshold"` // e.g. 10.0 means -10%
}

// alertState persists the last drawdown percentage at the time of alert per key.
type alertState struct {
	LastDD   float64 `json:"last_dd"`
	LastDate string  `json:"last_date"`
}

// DrawdownAlert describes a single instrument or portfolio drawdown breach.
type DrawdownAlert struct {
	Key            string  // "portfolio" or "instr_<id>"
	InstrumentName string
	PeakValue      float64
	CurrentValue   float64
	DrawdownPct    float64
}

// DiscordService manages Discord webhook settings and sends drawdown alerts.
type DiscordService struct {
	pool *pgxpool.Pool
}

func NewDiscordService(pool *pgxpool.Pool) *DiscordService {
	return &DiscordService{pool: pool}
}

// GetSettings reads discord settings from app_settings.
func (s *DiscordService) GetSettings(ctx context.Context) (*DiscordSettings, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT key, value FROM app_settings
		  WHERE key IN ('discord_webhook_url','discord_alert_enabled','discord_drawdown_threshold')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cfg := &DiscordSettings{DrawdownThreshold: 10.0}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		switch k {
		case "discord_webhook_url":
			cfg.WebhookURL = v
		case "discord_alert_enabled":
			cfg.Enabled = v == "true"
		case "discord_drawdown_threshold":
			var f float64
			if n, _ := fmt.Sscanf(v, "%f", &f); n == 1 && f > 0 {
				cfg.DrawdownThreshold = f
			}
		}
	}
	return cfg, rows.Err()
}

// SaveSettings persists discord settings to app_settings.
func (s *DiscordService) SaveSettings(ctx context.Context, cfg *DiscordSettings) error {
	enabled := "false"
	if cfg.Enabled {
		enabled = "true"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at) VALUES
			('discord_alert_enabled',        $1, NOW()),
			('discord_drawdown_threshold',   $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
	`, enabled, fmt.Sprintf("%.2f", cfg.DrawdownThreshold))
	if err != nil {
		return err
	}
	// Only overwrite the URL when caller supplies one (same pattern as AI key).
	if cfg.WebhookURL != "" {
		_, err = s.pool.Exec(ctx, `
			INSERT INTO app_settings (key, value, updated_at) VALUES ('discord_webhook_url', $1, NOW())
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
		`, cfg.WebhookURL)
	}
	return err
}

// ─── Discord webhook sending ──────────────────────────────────────────────────

type discordEmbed struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Color       int    `json:"color"`
	Timestamp   string `json:"timestamp,omitempty"`
	Footer      *struct {
		Text string `json:"text"`
	} `json:"footer,omitempty"`
}

type discordPayload struct {
	Embeds []discordEmbed `json:"embeds"`
}

func (s *DiscordService) sendWebhook(webhookURL string, payload discordPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// SendTestMessage posts a test message to verify the webhook is working.
func (s *DiscordService) SendTestMessage(ctx context.Context) error {
	cfg, err := s.GetSettings(ctx)
	if err != nil {
		return err
	}
	if cfg.WebhookURL == "" {
		return fmt.Errorf("no webhook URL configured")
	}
	footer := &struct {
		Text string `json:"text"`
	}{Text: "WealthFolio Alert"}
	return s.sendWebhook(cfg.WebhookURL, discordPayload{
		Embeds: []discordEmbed{{
			Title:       "✅ WealthFolio — Test Alert",
			Description: fmt.Sprintf("Discord alerts are configured. Drawdown threshold: **%.0f%%** from peak.", cfg.DrawdownThreshold),
			Color:       3066993, // green
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			Footer:      footer,
		}},
	})
}

// ─── Drawdown alert logic ─────────────────────────────────────────────────────

// CheckAndSendDrawdownAlerts computes per-instrument and portfolio drawdowns,
// then sends a Discord alert for any that have newly breached or worsened
// past the configured threshold.
func (s *DiscordService) CheckAndSendDrawdownAlerts(ctx context.Context) error {
	cfg, err := s.GetSettings(ctx)
	if err != nil {
		return err
	}
	if !cfg.Enabled || cfg.WebhookURL == "" {
		return nil
	}

	state, err := s.loadAlertStates(ctx)
	if err != nil {
		return err
	}

	today := time.Now().In(ist()).Format("2006-01-02")

	candidates, err := s.findBreaches(ctx, cfg.DrawdownThreshold)
	if err != nil {
		return err
	}

	// Determine which alerts to actually send (new breach or worsened by ≥1%).
	var toSend []DrawdownAlert
	for _, a := range candidates {
		prev, exists := state[a.Key]
		if exists && a.DrawdownPct < prev.LastDD+1.0 {
			continue // not significantly worse than last alert
		}
		toSend = append(toSend, a)
		state[a.Key] = alertState{LastDD: a.DrawdownPct, LastDate: today}
	}

	// Reset state for anything that has recovered above the threshold.
	recovered := s.findRecoveredKeys(state, candidates, cfg.DrawdownThreshold)
	for _, k := range recovered {
		delete(state, k)
	}

	if len(toSend) == 0 {
		_ = s.saveAlertStates(ctx, state) // persist any recoveries
		return nil
	}

	// Build embeds (Discord max 10 per message).
	footer := &struct {
		Text string `json:"text"`
	}{Text: "WealthFolio Alert"}

	var embeds []discordEmbed
	for i, a := range toSend {
		if i >= 10 {
			break
		}
		desc := fmt.Sprintf(
			"**%s** has drawn down **%.1f%%** from its all-time peak.\nPeak value: ₹%.2f → Current: ₹%.2f",
			a.InstrumentName, a.DrawdownPct, a.PeakValue, a.CurrentValue,
		)
		embeds = append(embeds, discordEmbed{
			Title:       fmt.Sprintf("⚠️ Drawdown Alert — %s", a.InstrumentName),
			Description: desc,
			Color:       15548997, // red
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			Footer:      footer,
		})
	}

	if err := s.sendWebhook(cfg.WebhookURL, discordPayload{Embeds: embeds}); err != nil {
		return fmt.Errorf("send discord alert: %w", err)
	}
	slog.Info("discord drawdown alerts sent", "count", len(embeds))

	return s.saveAlertStates(ctx, state)
}

// findBreaches returns all instruments (and portfolio) whose current drawdown from
// all-time peak is >= threshold.
func (s *DiscordService) findBreaches(ctx context.Context, threshold float64) ([]DrawdownAlert, error) {
	var result []DrawdownAlert

	// Per-instrument
	rows, err := s.pool.Query(ctx, `
		WITH peak AS (
			SELECT instrument_id, MAX(value) AS peak_value
			FROM instrument_snapshots
			GROUP BY instrument_id
		),
		latest AS (
			SELECT DISTINCT ON (instrument_id) instrument_id, value AS current_value
			FROM instrument_snapshots
			ORDER BY instrument_id, snapshot_date DESC
		)
		SELECT i.id, i.name, p.peak_value, l.current_value
		FROM peak p
		JOIN latest l ON l.instrument_id = p.instrument_id
		JOIN instruments i ON i.id = p.instrument_id
		WHERE p.peak_value > 0
		  AND l.current_value > 0
		  AND (p.peak_value - l.current_value) / p.peak_value * 100 >= $1
		ORDER BY (p.peak_value - l.current_value) / p.peak_value DESC
	`, threshold)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a DrawdownAlert
		var id int64
		if err := rows.Scan(&id, &a.InstrumentName, &a.PeakValue, &a.CurrentValue); err != nil {
			return nil, err
		}
		a.Key = fmt.Sprintf("instr_%d", id)
		a.DrawdownPct = (a.PeakValue - a.CurrentValue) / a.PeakValue * 100
		result = append(result, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Portfolio-level
	var peak, current float64
	err = s.pool.QueryRow(ctx, `
		SELECT MAX(total_value), (
			SELECT total_value FROM portfolio_snapshots ORDER BY snapshot_date DESC LIMIT 1
		)
		FROM portfolio_snapshots
	`).Scan(&peak, &current)
	if err == nil && peak > 0 && current > 0 {
		dd := (peak - current) / peak * 100
		if dd >= threshold {
			result = append(result, DrawdownAlert{
				Key:            "portfolio",
				InstrumentName: "Total Portfolio",
				PeakValue:      peak,
				CurrentValue:   current,
				DrawdownPct:    dd,
			})
		}
	}

	return result, nil
}

func (s *DiscordService) findRecoveredKeys(state map[string]alertState, active []DrawdownAlert, threshold float64) []string {
	activeKeys := make(map[string]bool, len(active))
	for _, a := range active {
		activeKeys[a.Key] = true
	}
	var recovered []string
	for k := range state {
		if !activeKeys[k] {
			recovered = append(recovered, k)
		}
	}
	return recovered
}

// ─── Alert state persistence ──────────────────────────────────────────────────

func (s *DiscordService) loadAlertStates(ctx context.Context) (map[string]alertState, error) {
	var val string
	err := s.pool.QueryRow(ctx,
		`SELECT value FROM app_settings WHERE key = 'discord_alert_state'`).Scan(&val)
	if err != nil {
		return make(map[string]alertState), nil
	}
	state := make(map[string]alertState)
	_ = json.Unmarshal([]byte(val), &state)
	return state, nil
}

func (s *DiscordService) saveAlertStates(ctx context.Context, state map[string]alertState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at) VALUES ('discord_alert_state', $1, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
	`, string(data))
	return err
}
