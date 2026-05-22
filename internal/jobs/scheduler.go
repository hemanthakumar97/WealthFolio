// Package jobs runs recurring background tasks using gocron.
package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hemanthhku/wealthfolio-v2/internal/services"
	"github.com/hemanthhku/wealthfolio-v2/internal/sse"
)

// Scheduler wraps gocron and holds references to the services it calls.
type Scheduler struct {
	s            gocron.Scheduler
	pool         *pgxpool.Pool
	broker       *sse.Broker
	gmailWatcher *services.GmailWatcher // nil when Gmail is disabled
}

func New(pool *pgxpool.Pool, broker *sse.Broker, gmailWatcher *services.GmailWatcher) (*Scheduler, error) {
	ist, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		ist = time.FixedZone("IST", 5*3600+30*60)
	}
	s, err := gocron.NewScheduler(gocron.WithLocation(ist))
	if err != nil {
		return nil, err
	}
	return &Scheduler{s: s, pool: pool, broker: broker, gmailWatcher: gmailWatcher}, nil
}

// Register wires up all cron jobs. Call Start() afterwards.
func (sched *Scheduler) Register(snapshotHour, marketMoodHour int, gmailWatcherHour int) error {
	pool := sched.pool
	broker := sched.broker

	// Fetch prices every 15 minutes.
	if _, err := sched.s.NewJob(
		gocron.CronJob("*/15 * * * *", false),
		gocron.NewTask(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			fx := services.NewFXService(pool)
			pf := services.NewPriceFetcher(pool, fx)
			result, err := pf.FetchAll(ctx)
			if err != nil {
				slog.Error("price fetch job", "err", err)
				return
			}
			slog.Info("prices fetched", "fetched", result.Fetched, "failed", result.Failed)
			if broker != nil {
				broker.Publish("prices", map[string]int{"fetched": result.Fetched, "failed": result.Failed})
			}
		}),
	); err != nil {
		return err
	}

	// Daily snapshot at configurable hour.
	cronExpr := formatCron(snapshotHour)
	if _, err := sched.s.NewJob(
		gocron.CronJob(cronExpr, false),
		gocron.NewTask(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			fx := services.NewFXService(pool)
			calc := services.NewPortfolioCalculator(pool, fx)
			snapshotSvc := services.NewSnapshotService(pool, calc, fx)
			snap, err := snapshotSvc.CreateDailySnapshot(ctx)
			if err != nil {
				slog.Error("daily snapshot job", "err", err)
				return
			}
			slog.Info("daily snapshot created", "date", snap.Date, "value", snap.Value)
			if broker != nil {
				broker.Publish("snapshot", snap)
			}
		}),
	); err != nil {
		return err
	}

	// Market mood sync — runs daily at configurable hour (default 19:00 IST).
	moodCron := formatCron(marketMoodHour)
	if _, err := sched.s.NewJob(
		gocron.CronJob(moodCron, false),
		gocron.NewTask(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			moodSvc := services.NewMarketMoodService(pool)
			if err := moodSvc.SyncMarketData(ctx); err != nil {
				slog.Error("market mood sync job", "err", err)
			}
		}),
	); err != nil {
		return err
	}

	// Gmail email watcher (if enabled).
	if sched.gmailWatcher != nil {
		gmailCron := formatCron(gmailWatcherHour)
		if _, err := sched.s.NewJob(
			gocron.CronJob(gmailCron, false),
			gocron.NewTask(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				defer cancel()
				if err := sched.gmailWatcher.Run(ctx); err != nil {
					slog.Error("gmail watcher job", "err", err)
				}
			}),
		); err != nil {
			return err
		}
	}

	return nil
}

func (sched *Scheduler) Start() {
	sched.s.Start()
}

func (sched *Scheduler) Stop() error {
	return sched.s.Shutdown()
}

func formatCron(hour int) string {
	if hour < 0 || hour > 23 {
		hour = 23
	}
	return "0 " + itoa(hour) + " * * *"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
