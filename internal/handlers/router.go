package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hemanthakumar97/wealthfolio/internal/auth"
	"github.com/hemanthakumar97/wealthfolio/internal/services"
	"github.com/hemanthakumar97/wealthfolio/internal/sse"
)

type Deps struct {
	Pool         *pgxpool.Pool
	Signer       *auth.Signer
	Production   bool
	SPA          http.Handler
	UploadDir    string
	UploadMaxMB  int
	SSEBroker    *sse.Broker
	FrontendURL  string
	GmailWatcher *services.GmailWatcher
}

func NewRouter(deps Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	instrSvc := services.NewInstrumentService(deps.Pool)
	dupSvc := services.NewDuplicateDetector(deps.Pool)
	importSvc := services.NewImportService(deps.Pool, instrSvc, dupSvc)
	fxSvc := services.NewFXService(deps.Pool)
	priceFetcher := services.NewPriceFetcher(deps.Pool, fxSvc)
	portfolioCalc := services.NewPortfolioCalculator(deps.Pool, fxSvc)
	snapshotSvc := services.NewSnapshotService(deps.Pool, portfolioCalc, fxSvc)

	frontendURL := deps.FrontendURL
	if frontendURL == "" {
		frontendURL = "http://localhost:5174"
	}
	gmailH := NewGmailOAuthHandler(deps.Pool, frontendURL, deps.GmailWatcher)

	authH := NewAuthHandler(deps.Pool, deps.Signer, deps.Production, deps.UploadDir)
	healthH := NewHealthHandler(deps.Pool)
	txH := NewTransactionsHandler(deps.Pool, instrSvc, dupSvc, importSvc, deps.UploadDir, deps.UploadMaxMB)
	instrH := NewInstrumentsHandler(deps.Pool, priceFetcher)
	portfolioH := NewPortfolioHandler(deps.Pool, portfolioCalc)
	trendsH := NewTrendsHandler(deps.Pool, snapshotSvc)
	categoriesH := NewCategoriesHandler(deps.Pool)
	allocationsH := NewAllocationsHandler(deps.Pool)

	moodSvc := services.NewMarketMoodService(deps.Pool)
	mmiSvc := services.NewTickertapeService(deps.Pool)
	metalsSvc := services.NewPreciousMetalsService(deps.Pool, fxSvc)
	marketH := NewMarketHandler(deps.Pool, moodSvc, mmiSvc, metalsSvc)
	backfillH := NewBackfillHandler(deps.Pool, deps.SSEBroker)
	signalH := NewSignalHandler(deps.Pool)
	aiSettingsH := NewAISettingsHandler(deps.Pool)
	aiPromptsH := NewAIPromptsHandler(deps.Pool)
	discordH := NewDiscordSettingsHandler(deps.Pool)
	analysisH := NewAnalysisHandler(deps.Pool)

	r.Route("/api", func(r chi.Router) {
		r.Get("/health", healthH.Health)
		if deps.SSEBroker != nil {
			sseHandler := deps.Signer.Required(deps.SSEBroker)
			r.Get("/events", sseHandler.ServeHTTP)
		}

		// Gmail OAuth2 — connect/callback public; test requires auth
		r.Get("/gmail/connect", gmailH.Connect)
		r.Get("/gmail/callback", gmailH.Callback)

		r.Route("/auth", func(r chi.Router) {
			r.Get("/status", authH.Status)
			r.Post("/setup", authH.Setup)
			r.Post("/login", authH.Login)
			r.Post("/logout", authH.Logout)
			r.Group(func(r chi.Router) {
				r.Use(deps.Signer.Required)
				r.Get("/me", authH.Me)
				r.Put("/me", authH.UpdateMe)
				r.Post("/change-password", authH.ChangePassword)
				r.Post("/profile-picture", authH.UploadProfilePicture)
			})
		})

		r.Group(func(r chi.Router) {
			r.Use(deps.Signer.Required)

			r.Route("/portfolio", func(r chi.Router) {
				r.Get("/summary", portfolioH.Summary)
				r.Get("/holdings", portfolioH.Holdings)
				r.Get("/closed-positions", portfolioH.ClosedPositions)
				r.Post("/snapshot", trendsH.CreateSnapshot)
			})

			r.Route("/trends", func(r chi.Router) {
				r.Get("/portfolio", trendsH.Portfolio)
				r.Get("/allocation-history", trendsH.AllocationHistory)
				r.Get("/benchmark", trendsH.Benchmark)
				r.Get("/monthly-returns", trendsH.MonthlyReturns)
				r.Post("/backfill", trendsH.Backfill)
				r.Get("/backfill/status", trendsH.BackfillStatus)
			})

			r.Route("/transactions", func(r chi.Router) {
				r.Post("/", txH.Upload)
				r.Post("/manual", txH.Manual)
				r.Get("/history", txH.History)
				r.Get("/history/{id}", txH.HistoryItem)
				r.Get("/formats", txH.Formats)
				r.Get("/{id}/logs", txH.Logs)
				r.Delete("/{id}", txH.Delete)
			})

			r.Route("/instruments", func(r chi.Router) {
				r.Get("/", instrH.List)
				r.Post("/", instrH.Create)
				r.Post("/fetch-prices", instrH.FetchPrices)
				r.Get("/{id}", instrH.Get)
				r.Put("/{id}", instrH.Update)
				r.Delete("/{id}", instrH.Delete)
				r.Get("/{id}/transactions", instrH.Transactions)
				r.Get("/{id}/prices", instrH.Prices)
			})

			r.Route("/categories", func(r chi.Router) {
				r.Get("/", categoriesH.List)
				r.Post("/", categoriesH.Create)
				r.Get("/{id}", categoriesH.Get)
				r.Put("/{id}", categoriesH.Update)
				r.Delete("/{id}", categoriesH.Delete)
				r.Get("/{id}/instruments", categoriesH.ListInstruments)
				r.Post("/{id}/instruments", categoriesH.AddInstrument)
				r.Delete("/{id}/instruments/{instr_id}", categoriesH.RemoveInstrument)
			})

			r.Route("/allocations", func(r chi.Router) {
				r.Get("/", allocationsH.Overview)
				r.Put("/instruments/{id}", allocationsH.UpdateInstrument)
				r.Put("/categories/{name}", allocationsH.UpdateCategory)
				r.Post("/calculate-distribution", allocationsH.CalculateDistribution)
			})

			// AI Signal
			r.Route("/ai/signal", func(r chi.Router) {
				r.Get("/holdings", signalH.GetHoldings)
				r.With(signalH.withLongTimeout).Post("/holdings", signalH.AnalyseHoldings)
				r.With(signalH.withLongTimeout).Post("/analyse", signalH.AnalyseStock)
				r.Get("/instrument-metrics/{instrument_id}", signalH.GetInstrumentMetrics)
				r.With(signalH.withLongTimeout).Post("/refresh-scores", signalH.RefreshScores)
				r.Get("/mf-metrics/{instrument_id}", signalH.GetMFMetrics) // backward compat
			})

			// AI Prompts
			r.Route("/ai/prompts", func(r chi.Router) {
				r.Get("/", aiPromptsH.List)
				r.Put("/{key}", aiPromptsH.Update)
			})

			r.Route("/backfill", func(r chi.Router) {
				r.Post("/start", backfillH.Start)
				r.Get("/status", backfillH.Status)
				r.Get("/auto-search", backfillH.AutoSearch)
				r.Post("/start-auto", backfillH.StartAuto)
				r.Get("/mfapi/search", backfillH.SearchMFAPI)
				r.Post("/mfapi/start", backfillH.StartFromMFAPI)
				r.Put("/instruments/{id}/amfi-code", backfillH.UpdateAMFICode)
			})

			emailRulesH := NewEmailRulesHandler(deps.Pool)
			r.Post("/gmail/test", gmailH.TestRun)

			r.Route("/settings", func(r chi.Router) {
				r.Get("/gmail", gmailH.Status)
				r.Put("/gmail", gmailH.SaveCredentials)
				r.Delete("/gmail", gmailH.Disconnect)
				r.Get("/email-rules", emailRulesH.List)
				r.Post("/email-rules", emailRulesH.Create)
				r.Put("/email-rules/{id}", emailRulesH.Update)
				r.Delete("/email-rules/{id}", emailRulesH.Delete)
				r.Get("/ai", aiSettingsH.Get)
				r.Put("/ai", aiSettingsH.Put)
				r.Get("/discord", discordH.Get)
				r.Put("/discord", discordH.Put)
				r.Post("/discord/test", discordH.Test)
			})

				r.Route("/analysis", func(r chi.Router) {
					r.Post("/run", analysisH.Run)
					r.Get("/watchlist", analysisH.Watchlist)
					r.Get("/{symbol}", analysisH.Get)
				})

			r.Route("/portfolio/market", func(r chi.Router) {
				r.Get("/config", marketH.Config)
				r.Post("/config/{index_name}/toggle", marketH.ToggleConfig)
				r.Get("/moods", marketH.Moods)
				r.Get("/history/{index_name}", marketH.IndexHistory)
				r.Get("/mmi", marketH.MMI)
				r.Get("/metals", marketH.Metals)
				r.Post("/refresh", marketH.Refresh)
				r.Post("/sync", marketH.Sync)
				r.Post("/ingest-pe", marketH.IngestPE)
				r.Get("/watchlist", marketH.ListWatchlist)
				r.Post("/watchlist", marketH.AddWatchlist)
				r.Delete("/watchlist/{symbol}", marketH.RemoveWatchlist)
			})
		})
	})

	// Serve uploaded files (profile pictures, etc.) from the upload directory.
	if deps.UploadDir != "" {
		fs := http.StripPrefix("/uploads/", http.FileServer(http.Dir(deps.UploadDir)))
		r.Handle("/uploads/*", fs)
	}

	if deps.SPA != nil {
		r.NotFound(deps.SPA.ServeHTTP)
	}

	return r
}
