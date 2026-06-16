package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cc-poc/internal/config"
	"cc-poc/internal/db"
	"cc-poc/internal/engine"
	"cc-poc/internal/feed"
	"cc-poc/internal/health"
	"cc-poc/internal/reporter"
	"cc-poc/internal/strategy"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "/config/strategies.yaml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		slog.Error("DATABASE_URL not set")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// DB connection with startup retry (postgres may still be initialising)
	database, err := connectWithRetry(ctx, dbURL)
	if err != nil {
		slog.Error("connect to db", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	// Build strategies from config
	strategies, strategyIDs, err := buildStrategies(ctx, cfg, database)
	if err != nil {
		slog.Error("build strategies", "error", err)
		os.Exit(1)
	}

	executor := engine.NewExecutor(database, cfg.SlippageRate, cfg.FeeRate)
	if err := executor.LoadPending(ctx, strategyIDs); err != nil {
		slog.Error("load pending orders", "error", err)
		os.Exit(1)
	}

	rep := reporter.NewConsole(database, 30*time.Second)
	tracker := health.NewTracker()

	// Start health HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/health", tracker.Handler())
	srv := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		slog.Info("health server listening", "addr", ":8080")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("health server", "error", err)
		}
	}()

	// Start price feed
	tickerCh := make(chan feed.Ticker, 10)
	priceFeed := feed.NewPriceFeed(cfg.PollInterval, tickerCh)
	go priceFeed.Run(ctx)

	slog.Info("simulator started",
		"strategies", len(strategies),
		"poll_interval", cfg.PollInterval,
		"slippage_rate", cfg.SlippageRate,
		"fee_rate", cfg.FeeRate)

	// Main event loop
	const pair = "btc_jpy"
	for {
		select {
		case <-ctx.Done():
			slog.Info("shutting down")
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = srv.Shutdown(shutdownCtx)
			shutdownCancel()
			return

		case t := <-tickerCh:
			tracker.RecordFetch()

			// Persist price tick
			if err := database.SavePrice(ctx, db.PriceRecord{
				Pair:       pair,
				Last:       t.Last,
				Bid:        t.Bid,
				Ask:        t.Ask,
				Volume:     t.Volume,
				RecordedAt: t.RecordedAt,
			}); err != nil {
				slog.Warn("save price failed", "error", err)
			}

			event := engine.PriceEvent{
				Pair:       pair,
				Last:       t.Last,
				Bid:        t.Bid,
				Ask:        t.Ask,
				Volume:     t.Volume,
				RecordedAt: t.RecordedAt,
			}

			// Check pending limit orders
			executor.CheckPending(ctx, event)

			// Run each strategy
			for _, s := range strategies {
				req := s.OnPrice(event)
				if req != nil {
					executor.Execute(ctx, s.ID(), req, event)
				}
			}

			rep.MaybeReport(ctx, t.Last)
		}
	}
}

func connectWithRetry(ctx context.Context, dsn string) (*db.DB, error) {
	backoff := time.Second
	for {
		d, err := db.New(ctx, dsn)
		if err == nil {
			return d, nil
		}
		slog.Warn("db connection failed, retrying", "error", err, "backoff", backoff)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func buildStrategies(ctx context.Context, cfg *config.Config, database *db.DB) ([]strategy.Strategy, []int64, error) {
	var strategies []strategy.Strategy
	var ids []int64

	for _, sc := range cfg.Strategies {
		id, err := database.CreateOrLoadStrategy(ctx, sc.Name, sc.InitialCash, sc.Params)
		if err != nil {
			return nil, nil, fmt.Errorf("strategy %q: %w", sc.Name, err)
		}

		portfolio, err := database.GetPortfolio(ctx, id)
		if err != nil {
			return nil, nil, fmt.Errorf("portfolio %q: %w", sc.Name, err)
		}

		var s strategy.Strategy
		switch sc.Type {
		case "ma_cross":
			short := config.GetInt(sc.Params, "short_period", 5)
			long := config.GetInt(sc.Params, "long_period", 20)
			amount := config.GetFloat(sc.Params, "trade_amount", 0.001)
			ms := strategy.NewMACross(id, sc.Name, short, long, amount)
			ms.SetPosition(portfolio.BTCAmount)
			s = ms
		case "rsi":
			period := config.GetInt(sc.Params, "period", 14)
			oversold := config.GetFloat(sc.Params, "oversold", 30)
			overbought := config.GetFloat(sc.Params, "overbought", 70)
			amount := config.GetFloat(sc.Params, "trade_amount", 0.001)
			rs := strategy.NewRSI(id, sc.Name, period, oversold, overbought, amount)
			rs.SetPosition(portfolio.BTCAmount)
			s = rs
		default:
			return nil, nil, fmt.Errorf("unknown strategy type %q", sc.Type)
		}

		// Warm up with recent price history
		prices, err := database.GetRecentPrices(ctx, "btc_jpy", cfg.WarmupBars)
		if err != nil {
			slog.Warn("warmup fetch failed", "strategy", sc.Name, "error", err)
		} else {
			events := make([]engine.PriceEvent, len(prices))
			for i, p := range prices {
				events[i] = engine.PriceEvent{
					Pair: p.Pair, Last: p.Last, Bid: p.Bid,
					Ask: p.Ask, Volume: p.Volume, RecordedAt: p.RecordedAt,
				}
			}
			s.Warmup(events)
			slog.Info("strategy warmed up", "name", sc.Name, "bars", len(events))
		}

		strategies = append(strategies, s)
		ids = append(ids, id)
		slog.Info("strategy loaded", "name", sc.Name, "type", sc.Type, "id", id,
			"cash_jpy", portfolio.CashJPY, "btc", portfolio.BTCAmount)
	}
	return strategies, ids, nil
}
