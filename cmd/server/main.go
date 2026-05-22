package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hemanthakumar97/wealthfolio/internal/auth"
	"github.com/hemanthakumar97/wealthfolio/internal/config"
	"github.com/hemanthakumar97/wealthfolio/internal/db"
	"github.com/hemanthakumar97/wealthfolio/internal/handlers"
	"github.com/hemanthakumar97/wealthfolio/internal/jobs"
	"github.com/hemanthakumar97/wealthfolio/internal/services"
	"github.com/hemanthakumar97/wealthfolio/internal/sse"
	"github.com/hemanthakumar97/wealthfolio/internal/web"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}
	defer pool.Close()

	slog.Info("running migrations")
	if err := db.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	signer := auth.NewSigner(cfg.JWTSecret)
	sseBroker := sse.NewBroker()

	// Shared import service (used by HTTP upload handler and Gmail watcher).
	instrSvc := services.NewInstrumentService(pool)
	dupSvc := services.NewDuplicateDetector(pool)
	importSvc := services.NewImportService(pool, instrSvc, dupSvc)

	// Gmail watcher — always started; runs only if credentials are configured in DB.
	gmailWatcher := services.NewGmailWatcher(pool, importSvc, services.GmailConfig{
		LookbackDays:       cfg.GmailLookbackDays,
		ZerodhaPDFPassword: cfg.ZerodhaPDFPassword,
	})
	slog.Info("gmail watcher ready", "hour_ist", cfg.GmailWatcherHour, "lookback_days", cfg.GmailLookbackDays)

	// Start background scheduler.
	sched, err := jobs.New(pool, sseBroker, gmailWatcher)
	if err != nil {
		return fmt.Errorf("scheduler init: %w", err)
	}
	if err := sched.Register(cfg.SnapshotHour, cfg.MarketMoodHour, cfg.GmailWatcherHour); err != nil {
		return fmt.Errorf("scheduler register: %w", err)
	}
	sched.Start()
	defer func() {
		if err := sched.Stop(); err != nil {
			slog.Warn("scheduler stop", "err", err)
		}
	}()

	router := handlers.NewRouter(handlers.Deps{
		Pool:         pool,
		Signer:       signer,
		Production:   cfg.IsProduction(),
		SPA:          web.SPA(),
		UploadDir:    cfg.UploadDir,
		UploadMaxMB:  cfg.UploadMaxMB,
		SSEBroker:    sseBroker,
		GmailWatcher: gmailWatcher,
	})

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	go func() {
		slog.Info("server listening", "addr", srv.Addr, "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("listen", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
