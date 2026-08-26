// Command api runs the URL shortener HTTP API. Run cmd/migrate first —
// this binary does not migrate the database itself (see the postgres
// package doc comment for why).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"urlshortener/internal/config"
	"urlshortener/internal/delivery/httpapi"
	"urlshortener/internal/ratelimit"
	"urlshortener/internal/ratelimit/grpcclient"
	"urlshortener/internal/repository/postgres"
	"urlshortener/internal/repository/rediscache"
	"urlshortener/internal/usecase/shortener"
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

	if len(cfg.APIKeys) == 0 {
		logger.Warn("API_KEYS is empty — every POST/DELETE /v1/links request will be rejected until keys are issued")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Health checking always uses this same Postgres connection — unlike
	// Redis/the rate limiter, it isn't separate infrastructure to opt
	// into. cmd/checker is still a genuinely separate, optional process:
	// running cmd/api without it just means every link's health stays
	// "unknown" (never checked), not broken.
	healthRepo := postgres.NewHealthRepo(pool)

	svcOpts := []shortener.Option{
		shortener.WithDefaultTTL(cfg.DefaultLinkTTL),
		shortener.WithLogger(logger),
		shortener.WithHealthScheduler(healthRepo),
	}

	// Caching is opt-in: only wire it up (and only add its readiness
	// check) when REDIS_ADDR is actually configured. Without it, the
	// service runs exactly as it did before caching existed.
	pingers := []func(context.Context) error{pool.Ping}

	if cfg.RedisAddr != "" {
		redisClient, err := rediscache.NewClient(ctx, cfg.RedisAddr)
		if err != nil {
			return err
		}
		defer redisClient.Close()

		svcOpts = append(svcOpts, shortener.WithCache(rediscache.NewCache(redisClient)))
		if cfg.CacheTTL > 0 {
			svcOpts = append(svcOpts, shortener.WithCacheTTL(cfg.CacheTTL))
		}
		if cfg.NegativeCacheTTL > 0 {
			svcOpts = append(svcOpts, shortener.WithNegativeCacheTTL(cfg.NegativeCacheTTL))
		}

		pingers = append(pingers, func(ctx context.Context) error { return redisClient.Ping(ctx).Err() })
		logger.Info("cache enabled", "redis_addr", cfg.RedisAddr)
	} else {
		logger.Info("cache disabled (REDIS_ADDR not set)")
	}

	// Rate limiting is opt-in too, same reasoning: without RATE_LIMITER_ADDR
	// the service runs with no throttling rather than refusing to start.
	var limiter ratelimit.Limiter
	if cfg.RateLimiterAddr != "" {
		rlClient, err := grpcclient.Dial(cfg.RateLimiterAddr)
		if err != nil {
			return err
		}
		defer rlClient.Close()

		limiter = rlClient
		logger.Info("rate limiting enabled", "rate_limiter_addr", cfg.RateLimiterAddr)
	} else {
		logger.Info("rate limiting disabled (RATE_LIMITER_ADDR not set)")
	}

	svc := shortener.New(postgres.NewLinkRepo(pool), postgres.NewCodeGen(pool, "link_codes"), svcOpts...)

	router := httpapi.NewRouter(svc, cfg.APIKeys, limiter, healthRepo, multiPinger(pingers), logger)
	srv := httpapi.NewServer(cfg.HTTPAddr, router)

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server starting", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return srv.Shutdown(shutdownCtx)
}

// multiPinger satisfies handler.Pinger by checking every dependency in
// order, failing readiness on the first one that doesn't respond. Defined
// locally rather than in the handler package, which deliberately doesn't
// import any specific driver (postgres or redis).
type multiPinger []func(context.Context) error

func (m multiPinger) Ping(ctx context.Context) error {
	for _, ping := range m {
		if err := ping(ctx); err != nil {
			return err
		}
	}
	return nil
}
