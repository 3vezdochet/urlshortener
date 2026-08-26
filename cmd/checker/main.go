// Command checker periodically checks the availability of every link's
// target URL and records the result, so GET /v1/links/{code}/health can
// report it.
//
// Runs as its own process, independent of cmd/api. Availability checking
// is I/O-bound work against arbitrary external servers with unpredictable
// latency — running it inside the API process would let a burst of slow
// targets compete with the API for goroutines and complicate its shutdown;
// a separate process scales, deploys, and fails independently instead.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"urlshortener/internal/config"
	"urlshortener/internal/repository/postgres"
	"urlshortener/internal/usecase/checker"
	"urlshortener/internal/usecase/checker/httpprobe"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	svc := checker.New(
		postgres.NewHealthRepo(pool),
		httpprobe.New(),
		checker.WithLogger(logger),
	)

	ticker := time.NewTicker(cfg.CheckerInterval)
	defer ticker.Stop()

	logger.Info("checker starting", "interval", cfg.CheckerInterval)

	for {
		select {
		case <-ctx.Done():
			logger.Info("shutdown signal received")
			return nil
		case <-ticker.C:
			n, err := svc.RunOnce(ctx)
			if err != nil {
				logger.Error("run check batch", "error", err)
				continue
			}
			if n > 0 {
				logger.Info("checked batch", "count", n)
			}
		}
	}
}
