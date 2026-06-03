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
	// Additional alert types
	MoverAlertEnabled bool    `json:"mover_alert_enabled"`
	MoverThreshold    float64 `json:"mover_threshold"`    // single-day % drop, default 3
	ATHAlertEnabled   bool    `json:"ath_alert_enabled"`
	LTCGAlertEnabled  bool    `json:"ltcg_alert_enabled"`
	LTCGThresholdPct  float64 `json:"ltcg_threshold_pct"` // % of ₹1.25L to alert at, default 80
	MoodAlertEnabled  bool    `json:"mood_alert_enabled"`
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
		  WHERE key LIKE 'discord_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cfg := &DiscordSettings{
		DrawdownThreshold: 10.0,
		MoverThreshold:    3.0,
		LTCGThresholdPct:  80.0,
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		pf := func(dst *float64) {
			var f float64
			if n, _ := fmt.Sscanf(v, "%f", &f); n == 1 && f > 0 {
				*dst = f
			}
		}
		switch k {
		case "discord_webhook_url":
			cfg.WebhookURL = v
		case "discord_alert_enabled":
			cfg.Enabled = v == "true"
		case "discord_drawdown_threshold":
			pf(&cfg.DrawdownThreshold)
		case "discord_mover_alert_enabled":
			cfg.MoverAlertEnabled = v == "true"
		case "discord_mover_threshold":
			pf(&cfg.MoverThreshold)
		case "discord_ath_alert_enabled":
			cfg.ATHAlertEnabled = v == "true"
		case "discord_ltcg_alert_enabled":
			cfg.LTCGAlertEnabled = v == "true"
		case "discord_ltcg_threshold_pct":
			pf(&cfg.LTCGThresholdPct)
		case "discord_mood_alert_enabled":
			cfg.MoodAlertEnabled = v == "true"
		}
	}
	return cfg, rows.Err()
}

// SaveSettings persists discord settings to app_settings.
func (s *DiscordService) SaveSettings(ctx context.Context, cfg *DiscordSettings) error {
	b := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	f := func(v float64) string { return fmt.Sprintf("%.2f", v) }

	_, err := s.pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at) VALUES
			('discord_alert_enabled',       $1, NOW()),
			('discord_drawdown_threshold',  $2, NOW()),
			('discord_mover_alert_enabled', $3, NOW()),
			('discord_mover_threshold',     $4, NOW()),
			('discord_ath_alert_enabled',   $5, NOW()),
			('discord_ltcg_alert_enabled',  $6, NOW()),
			('discord_ltcg_threshold_pct',  $7, NOW()),
			('discord_mood_alert_enabled',  $8, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
	`,
		b(cfg.Enabled), f(cfg.DrawdownThreshold),
		b(cfg.MoverAlertEnabled), f(cfg.MoverThreshold),
		b(cfg.ATHAlertEnabled),
		b(cfg.LTCGAlertEnabled), f(cfg.LTCGThresholdPct),
		b(cfg.MoodAlertEnabled),
	)
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

// ─── Additional alert checks (mover, ATH, LTCG, mood) ────────────────────────

// CheckAllAlerts runs all enabled alert types in sequence.
func (s *DiscordService) CheckAllAlerts(ctx context.Context) error {
	cfg, err := s.GetSettings(ctx)
	if err != nil {
		return err
	}
	if !cfg.Enabled || cfg.WebhookURL == "" {
		return nil
	}

	// Drawdown (already separate, called by scheduler)
	// These are the additional checks.

	if cfg.MoverAlertEnabled {
		if err := s.checkBigMovers(ctx, cfg); err != nil {
			slog.Error("big mover alert", "err", err)
		}
	}
	if cfg.ATHAlertEnabled {
		if err := s.checkATH(ctx, cfg); err != nil {
			slog.Error("ATH alert", "err", err)
		}
	}
	if cfg.LTCGAlertEnabled {
		if err := s.checkLTCG(ctx, cfg); err != nil {
			slog.Error("LTCG alert", "err", err)
		}
	}
	if cfg.MoodAlertEnabled {
		if err := s.checkMarketMood(ctx, cfg); err != nil {
			slog.Error("market mood alert", "err", err)
		}
	}
	return nil
}

// checkBigMovers alerts when any active holding drops or rises > MoverThreshold% in one day.
func (s *DiscordService) checkBigMovers(ctx context.Context, cfg *DiscordSettings) error {
	rows, err := s.pool.Query(ctx, `
		WITH today AS (
			SELECT DISTINCT ON (instrument_id) instrument_id, nav_price, price_date
			FROM prices ORDER BY instrument_id, price_date DESC
		),
		yesterday AS (
			SELECT DISTINCT ON (instrument_id) instrument_id, nav_price
			FROM prices
			WHERE price_date < (SELECT MAX(price_date) FROM prices)
			ORDER BY instrument_id, price_date DESC
		),
		active AS (
			SELECT instrument_id
			FROM transactions
			GROUP BY instrument_id
			HAVING SUM(CASE WHEN transaction_type IN ('BUY','SWITCH_IN','BONUS') THEN quantity
			               WHEN transaction_type IN ('SELL','SWITCH_OUT') THEN -quantity
			               ELSE 0 END) > 0.001
		)
		SELECT i.name, t.nav_price, y.nav_price,
		       (t.nav_price - y.nav_price) / NULLIF(y.nav_price,0) * 100 AS change_pct
		FROM today t
		JOIN yesterday y ON y.instrument_id = t.instrument_id
		JOIN instruments i ON i.id = t.instrument_id
		JOIN active a ON a.instrument_id = t.instrument_id
		WHERE ABS((t.nav_price - y.nav_price) / NULLIF(y.nav_price,0) * 100) >= $1
		ORDER BY ABS((t.nav_price - y.nav_price) / NULLIF(y.nav_price,0) * 100) DESC
		LIMIT 10
	`, cfg.MoverThreshold)
	if err != nil {
		return err
	}
	defer rows.Close()

	footer := &struct {
		Text string `json:"text"`
	}{Text: "WealthFolio Alert"}

	var embeds []discordEmbed
	for rows.Next() {
		var name string
		var todayP, yestP, changePct float64
		if err := rows.Scan(&name, &todayP, &yestP, &changePct); err != nil {
			continue
		}
		arrow := "📈"
		color := 3066993 // green
		if changePct < 0 {
			arrow = "📉"
			color = 15548997 // red
		}
		embeds = append(embeds, discordEmbed{
			Title: fmt.Sprintf("%s %s — %.1f%% in one day", arrow, name, changePct),
			Description: fmt.Sprintf(
				"**%s** moved **%.2f%%** today.\nYesterday: ₹%.2f → Today: ₹%.2f",
				name, changePct, yestP, todayP,
			),
			Color:     color,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Footer:    footer,
		})
	}
	if len(embeds) == 0 {
		return nil
	}
	slog.Info("big mover alerts", "count", len(embeds))
	return s.sendWebhook(cfg.WebhookURL, discordPayload{Embeds: embeds})
}

// checkATH alerts once when the portfolio sets a new all-time high today.
func (s *DiscordService) checkATH(ctx context.Context, cfg *DiscordSettings) error {
	var todayValue, historicMax float64
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT total_value FROM portfolio_snapshots ORDER BY snapshot_date DESC LIMIT 1),
			(SELECT MAX(total_value) FROM portfolio_snapshots WHERE snapshot_date < CURRENT_DATE)
	`).Scan(&todayValue, &historicMax)
	if err != nil || todayValue == 0 {
		return nil
	}

	if todayValue <= historicMax {
		return nil // not a new ATH
	}

	// Check cooldown: only alert once per day for ATH.
	state, _ := s.loadAlertStates(ctx)
	today := time.Now().In(ist()).Format("2006-01-02")
	if state["ath_date"].LastDate == today {
		return nil
	}
	state["ath_date"] = alertState{LastDate: today}
	_ = s.saveAlertStates(ctx, state)

	footer := &struct {
		Text string `json:"text"`
	}{Text: "WealthFolio Alert"}
	return s.sendWebhook(cfg.WebhookURL, discordPayload{
		Embeds: []discordEmbed{{
			Title: "🏆 Portfolio All-Time High!",
			Description: fmt.Sprintf(
				"Your portfolio just set a **new all-time high** of ₹%.2f (previous: ₹%.2f). 🎉",
				todayValue, historicMax,
			),
			Color:     3066993,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Footer:    footer,
		}},
	})
}

// checkLTCG alerts when estimated unrealized LTCG exceeds LTCGThresholdPct of ₹1.25L.
// Uses a simplified approach: profit on lots held > 1 year with positive gain.
func (s *DiscordService) checkLTCG(ctx context.Context, cfg *DiscordSettings) error {
	oneYearAgo := time.Now().AddDate(-1, 0, 0).Format("2006-01-02")
	// Approximate LTCG: sum of (current_value - invested) for instruments where
	// the earliest buy was > 1 year ago and current_value > invested.
	var ltcgEstimate float64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(s.value - s.invested), 0)
		FROM (
			SELECT DISTINCT ON (instrument_id) instrument_id, value, invested
			FROM instrument_snapshots
			ORDER BY instrument_id, snapshot_date DESC
		) s
		JOIN (
			SELECT instrument_id, MIN(transaction_date) AS first_buy
			FROM transactions
			WHERE transaction_type IN ('BUY','SWITCH_IN')
			GROUP BY instrument_id
			HAVING MIN(transaction_date) <= $1
		) t ON t.instrument_id = s.instrument_id
		WHERE s.value > s.invested
	`, oneYearAgo).Scan(&ltcgEstimate)
	if err != nil || ltcgEstimate <= 0 {
		return nil
	}

	limit := 125000.0 // ₹1.25 lakh LTCG exemption
	pct := ltcgEstimate / limit * 100
	if pct < cfg.LTCGThresholdPct {
		return nil
	}

	// Cooldown: once per week.
	state, _ := s.loadAlertStates(ctx)
	today := time.Now().In(ist()).Format("2006-01-02")
	if last, ok := state["ltcg"]; ok {
		if last.LastDate == today {
			return nil
		}
		// Only re-alert if it's been 7+ days or LTCG increased by 10K+.
		if last.LastDD > 0 && ltcgEstimate < last.LastDD+10000 {
			return nil
		}
	}
	state["ltcg"] = alertState{LastDD: ltcgEstimate, LastDate: today}
	_ = s.saveAlertStates(ctx, state)

	footer := &struct {
		Text string `json:"text"`
	}{Text: "WealthFolio Alert"}
	return s.sendWebhook(cfg.WebhookURL, discordPayload{
		Embeds: []discordEmbed{{
			Title: "🏛️ LTCG Tax Milestone Alert",
			Description: fmt.Sprintf(
				"Estimated unrealized LTCG: **₹%.0f** (%.0f%% of the ₹1.25L exemption limit).\n"+
					"Consider reviewing positions for tax harvesting before Mar 31.",
				ltcgEstimate, pct,
			),
			Color:     15105570, // orange
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Footer:    footer,
		}},
	})
}

// checkMarketMood alerts on Extreme Fear or Extreme Greed from the MMI.
func (s *DiscordService) checkMarketMood(ctx context.Context, cfg *DiscordSettings) error {
	var mmiValue float64
	var mmiMood string
	err := s.pool.QueryRow(ctx, `
		SELECT (cache_value->>'value')::float, cache_value->>'mood'
		FROM market_cache WHERE cache_key = 'mmi_now'
	`).Scan(&mmiValue, &mmiMood)
	if err != nil || (mmiMood != "Extreme Fear" && mmiMood != "Extreme Greed") {
		return nil
	}

	state, _ := s.loadAlertStates(ctx)
	today := time.Now().In(ist()).Format("2006-01-02")
	key := "mood_" + mmiMood
	if state[key].LastDate == today {
		return nil // already alerted today
	}
	state[key] = alertState{LastDate: today}
	_ = s.saveAlertStates(ctx, state)

	title, desc, color := moodEmbed(mmiMood, mmiValue)
	footer := &struct {
		Text string `json:"text"`
	}{Text: "WealthFolio Alert — Market Mood Index"}
	return s.sendWebhook(cfg.WebhookURL, discordPayload{
		Embeds: []discordEmbed{{
			Title:     title,
			Description: desc,
			Color:     color,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Footer:    footer,
		}},
	})
}

func moodEmbed(mood string, val float64) (title, desc string, color int) {
	switch mood {
	case "Extreme Fear":
		return "😱 Market Mood: Extreme Fear",
			fmt.Sprintf("MMI is at **%.0f** (Extreme Fear).\nHistorically, extreme fear is a **buying opportunity** — markets tend to be oversold.", val),
			3066993 // green — buy opportunity
	case "Extreme Greed":
		return "🤑 Market Mood: Extreme Greed",
			fmt.Sprintf("MMI is at **%.0f** (Extreme Greed).\nMarkets may be overheated. Consider **booking partial profits** or reviewing stop-losses.", val),
			15548997 // red — caution
	default:
		return mood, fmt.Sprintf("MMI: %.0f", val), 10070709
	}
}
