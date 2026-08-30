// Package tracing configures OpenTelemetry distributed tracing, exporting
// spans via OTLP/HTTP to a collector (Jaeger, Tempo, the OTel Collector —
// anything that speaks OTLP). See the repo root README's "Observability"
// section for how this fits together and why it's optional.
package tracing

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Setup configures the global OpenTelemetry tracer provider and
// W3C trace-context propagator, exporting spans for serviceName to
// endpoint (host:port, no scheme — OTLP/HTTP's usual :4318) via OTLP/HTTP.
//
// The connection is unencrypted (WithInsecure) — the same trusted-internal-
// network assumption already made for the Postgres, Redis, and rate
// limiter connections in this project; put it behind mTLS or a service
// mesh before crossing a trust boundary.
//
// Returns a shutdown func the caller must call (typically deferred in
// main) to flush any pending spans before the process exits.
func Setup(ctx context.Context, serviceName, endpoint string) (shutdown func(context.Context) error, err error) {
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("create OTLP exporter: %w", err)
	}

	res, err := resource.New(ctx, resource.WithAttributes(semconv.ServiceName(serviceName)))
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}
