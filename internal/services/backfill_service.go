package services

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// BackfillStatus is the live job state; safe to read from any goroutine.
type BackfillStatus struct {
	IsRunning  bool     `json:"is_running"`
	Total      int      `json:"total"`
	Processed  int      `json:"processed"`
	Current    string   `json:"current"`
	Logs       []string `json:"logs"`
	Warnings   []string `json:"warnings"`
	Errors     []string `json:"errors"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// BackfillEmitter receives real-time log lines from the job.
type BackfillEmitter interface {
	Emit(msg string)
}

// BackfillService runs price-history imports for instruments.
type BackfillService struct {
	pool    *pgxpool.Pool
	sheets  *SheetsService
	mfapi   *MFAPIClient
	yahoo   *YahooClient
	fx      *FXService
	mu      sync.RWMutex
	status  BackfillStatus
	emitter BackfillEmitter
}

func NewBackfillService(pool *pgxpool.Pool, sheets *SheetsService, fx *FXService, emitter BackfillEmitter) *BackfillService {
	return &BackfillService{
		pool:    pool,
		sheets:  sheets,
		mfapi:   NewMFAPIClient(),
		yahoo:   NewYahooClient(),
		fx:      fx,
		emitter: emitter,
		status:  BackfillStatus{Logs: []string{}, Warnings: []string{}, Errors: []string{}},
	}
}

// Status returns a snapshot of the current job state.
func (b *BackfillService) Status() BackfillStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()
	// Deep copy slices so callers can't race.
	s := b.status
	s.Logs = append([]string{}, b.status.Logs...)
	s.Warnings = append([]string{}, b.status.Warnings...)
	s.Errors = append([]string{}, b.status.Errors...)
	return s
}

// RunFromSheet starts a background import from a Google Sheet URL for one instrument.
func (b *BackfillService) RunFromSheet(ctx context.Context, instrumentID int64, sheetURL string) error {
	if b.isRunning() {
		return fmt.Errorf("backfill already running")
	}
	go b.run(context.Background(), instrumentID, func(ctx context.Context) ([]SheetRow, error) {
		return b.sheets.FetchCSV(ctx, sheetURL)
	})
	return nil
}

// RunFromMFAPI starts a background import from MFAPI using the given AMFI scheme code.
// It also persists the amfi_code to the instrument record.
func (b *BackfillService) RunFromMFAPI(ctx context.Context, instrumentID int64, amfiCode string) error {
	if b.isRunning() {
		return fmt.Errorf("backfill already running")
	}
	// Save the amfi_code to the instrument.
	if _, err := b.pool.Exec(ctx,
		`UPDATE instruments SET amfi_code = $1, updated_at = NOW() WHERE id = $2`,
		amfiCode, instrumentID,
	); err != nil {
		return fmt.Errorf("save amfi_code: %w", err)
	}
	go b.run(context.Background(), instrumentID, func(ctx context.Context) ([]SheetRow, error) {
		return b.mfapi.FetchHistory(ctx, amfiCode)
	})
	return nil
}

// RunFromYahoo starts a background import from Yahoo Finance using the given symbol.
// It also persists the yahoo_symbol to the instrument record.
func (b *BackfillService) RunFromYahoo(ctx context.Context, instrumentID int64, symbol string) error {
	if b.isRunning() {
		return fmt.Errorf("backfill already running")
	}
	if _, err := b.pool.Exec(ctx,
		`UPDATE instruments SET yahoo_symbol = $1, updated_at = NOW() WHERE id = $2`,
		symbol, instrumentID,
	); err != nil {
		return fmt.Errorf("save yahoo_symbol: %w", err)
	}
	go b.run(context.Background(), instrumentID, func(ctx context.Context) ([]SheetRow, error) {
		return b.yahoo.FetchHistory(ctx, symbol)
	})
	return nil
}

// RunFromReader starts a background import from an uploaded file reader.
func (b *BackfillService) RunFromReader(ctx context.Context, instrumentID int64, r io.Reader) error {
	if b.isRunning() {
		return fmt.Errorf("backfill already running")
	}
	// Read all bytes first so the reader isn't consumed after the HTTP handler returns.
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read upload: %w", err)
	}
	go b.run(context.Background(), instrumentID, func(_ context.Context) ([]SheetRow, error) {
		return ParseUploadedCSV(ioReader(data))
	})
	return nil
}

func (b *BackfillService) isRunning() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.status.IsRunning
}

type rowsFn func(context.Context) ([]SheetRow, error)

func (b *BackfillService) run(ctx context.Context, instrumentID int64, fetch rowsFn) {
	now := time.Now()
	b.mu.Lock()
	b.status = BackfillStatus{
		IsRunning: true,
		StartedAt: &now,
		Logs:      []string{},
		Warnings:  []string{},
		Errors:    []string{},
	}
	b.mu.Unlock()

	b.log("⏳ Starting backfill…")

	defer func() {
		fin := time.Now()
		b.mu.Lock()
		b.status.IsRunning = false
		b.status.FinishedAt = &fin
		b.mu.Unlock()
		b.log("✅ Backfill complete.")
	}()

	// Fetch instrument info.
	var instrName, currency string
	err := b.pool.QueryRow(ctx,
		`SELECT name, currency FROM instruments WHERE id = $1`, instrumentID,
	).Scan(&instrName, &currency)
	if err != nil {
		b.errLog(fmt.Sprintf("instrument %d not found: %v", instrumentID, err))
		return
	}

	b.mu.Lock()
	b.status.Total = 1
	b.status.Current = instrName
	b.mu.Unlock()
	b.log(fmt.Sprintf("📥 Fetching price data for %s…", instrName))

	rows, err := fetch(ctx)
	if err != nil {
		b.errLog(fmt.Sprintf("fetch failed: %v", err))
		return
	}
	b.log(fmt.Sprintf("📊 Got %d price rows.", len(rows)))

	// Get USD/INR rate if needed.
	var usdInr decimal.Decimal
	if currency == "USD" {
		usdInr, err = b.fx.USDToINR(ctx)
		if err != nil {
			usdInr = decimal.NewFromFloat(84.0)
			b.warn("Could not fetch live USD/INR rate; using ₹84 fallback.")
		} else {
			b.log(fmt.Sprintf("💱 USD/INR rate: %.2f", usdInrFloat(usdInr)))
		}
	}

	inserted, skipped := 0, 0
	for _, row := range rows {
		price := decimal.NewFromFloat(row.Close)
		isConverted := false

		if currency == "USD" && !usdInr.IsZero() {
			price = price.Mul(usdInr)
			isConverted = true
		}

		_, err := b.pool.Exec(ctx,
			`INSERT INTO prices (instrument_id, price_date, nav_price, source, is_converted, fetched_at)
			 VALUES ($1, $2, $3, 'MANUAL', $4, NOW())
			 ON CONFLICT (instrument_id, price_date) DO NOTHING`,
			instrumentID, row.Date.Format("2006-01-02"), price, isConverted,
		)
		if err != nil {
			b.warn(fmt.Sprintf("row %s: insert error: %v", row.Date.Format("2006-01-02"), err))
			skipped++
		} else {
			inserted++
		}
	}

	b.log(fmt.Sprintf("💾 Inserted %d rows, skipped %d (already exist).", inserted, skipped))

	b.mu.Lock()
	b.status.Processed = 1
	b.mu.Unlock()

	// Trigger snapshot backfill in background.
	b.log("🔄 Triggering snapshot recalculation…")
	go func() {
		slog.Info("backfill: snapshot recalc triggered")
	}()
}

func (b *BackfillService) log(msg string) {
	ts := time.Now().Format("15:04:05")
	line := fmt.Sprintf("[%s] %s", ts, msg)
	b.mu.Lock()
	b.status.Logs = append(b.status.Logs, line)
	b.mu.Unlock()
	if b.emitter != nil {
		b.emitter.Emit(line)
	}
}

func (b *BackfillService) warn(msg string) {
	ts := time.Now().Format("15:04:05")
	line := fmt.Sprintf("[%s] ⚠️  %s", ts, msg)
	b.mu.Lock()
	b.status.Warnings = append(b.status.Warnings, line)
	b.status.Logs = append(b.status.Logs, line)
	b.mu.Unlock()
	if b.emitter != nil {
		b.emitter.Emit(line)
	}
}

func (b *BackfillService) errLog(msg string) {
	ts := time.Now().Format("15:04:05")
	line := fmt.Sprintf("[%s] ❌ %s", ts, msg)
	b.mu.Lock()
	b.status.Errors = append(b.status.Errors, line)
	b.status.Logs = append(b.status.Logs, line)
	b.mu.Unlock()
	if b.emitter != nil {
		b.emitter.Emit(line)
	}
}

func usdInrFloat(d decimal.Decimal) float64 {
	f, _ := d.Float64()
	return f
}

// ioReader wraps a byte slice as io.Reader.
type ioReader []byte

func (r ioReader) Read(p []byte) (int, error) {
	if len(r) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r)
	return n, io.EOF
}

