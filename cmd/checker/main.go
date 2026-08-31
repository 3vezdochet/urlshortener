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
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"urlshortener/internal/config"
	"urlshortener/internal/observability"
	"urlshortener/internal/observability/tracing"
	"urlshortener/internal/repository/postgres"
	"urlshortener/internal/usecase/checker"
	"urlshortener/internal/usecase/checker/checkermetrics"
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

	if cfg.OTLPEndpoint != "" {
		shutdown, err := tracing.Setup(ctx, "urlshortener-checker", cfg.OTLPEndpoint)
		if err != nil {
			return err
		}
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdown(shutdownCtx); err != nil {
				logger.Warn("tracer shutdown failed", "error", err)
			}
		}()
		logger.Info("tracing enabled", "otlp_endpoint", cfg.OTLPEndpoint)
	} else {
		logger.Info("tracing disabled (OTEL_EXPORTER_OTLP_ENDPOINT not set)")
	}

	reg := observability.NewRegistry()
	metricsSrv := startMetricsServer(cfg.MetricsAddr, reg, logger)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("metrics server shutdown failed", "error", err)
		}
	}()

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	var prober checker.Prober = httpprobe.New()
	if cfg.OTLPEndpoint != "" {
		prober = &tracingProber{next: prober, tracer: otel.Tracer("urlshortener-checker")}
	}

	svc := checker.New(
		postgres.NewHealthRepo(pool),
		prober,
		checker.WithLogger(logger),
		checker.WithMetrics(checkermetrics.New(reg)),
	)

	ticker := time.NewTicker(cfg.CheckerInterval)
	defer ticker.Stop()

	logger.Info("checker starting", "interval", cfg.CheckerInterval, "metrics_addr", cfg.MetricsAddr)

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

// startMetricsServer serves GET /metrics and /healthz on addr in the
// background. cmd/checker has no other HTTP server (unlike cmd/api, which
// serves metrics on its main port) — this exists solely so Prometheus has
// something to scrape.
func startMetricsServer(addr string, reg *prometheus.Registry, logger *slog.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics server failed", "error", err)
		}
	}()
	return srv
}

// tracingProber wraps a checker.Prober, creating one span per probe —
// composed here at cmd/checker, not inside the checker package itself,
// which stays free of the OpenTelemetry dependency (same reasoning as
// httpmetrics/checkermetrics living outside their core packages).
type tracingProber struct {
	next   checker.Prober
	tracer trace.Tracer
}

func (p *tracingProber) Probe(ctx context.Context, url string) (int, error) {
	ctx, span := p.tracer.Start(ctx, "checker.Probe", trace.WithAttributes(attribute.String("url", url)))
	defer span.End()

	statusCode, err := p.next.Probe(ctx, url)

	span.SetAttributes(attribute.Int("http.status_code", statusCode))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return statusCode, err
}
