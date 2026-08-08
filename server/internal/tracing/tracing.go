// Package tracing sets up OpenTelemetry distributed tracing (Section 13). Spans
// propagate through context.Context (already threaded everywhere), so a request
// can be followed across the gateway, services, and — once split — across the
// network. The exporter is chosen by env: SYNAPSE_TRACE=stdout prints spans
// (dev); unset installs a no-op provider (zero overhead). An OTLP exporter to a
// collector (Tempo/Jaeger) is a drop-in swap for production.
package tracing

import (
	"context"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Init installs the global tracer provider and returns a shutdown function.
// Exporter selection by env:
//   - SYNAPSE_OTLP_ENDPOINT=host:4318 → OTLP/HTTP to a collector (Tempo/Jaeger);
//   - else SYNAPSE_TRACE=stdout       → print spans (dev);
//   - else                            → no-op (zero overhead).
func Init(ctx context.Context) (func(context.Context) error, error) {
	// W3C trace-context propagation so spans continue across the event bus.
	otel.SetTextMapPropagator(propagation.TraceContext{})

	exp, err := chooseExporter(ctx)
	if err != nil {
		return nil, err
	}
	if exp == nil {
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
		return func(context.Context) error { return nil }, nil
	}
	res, _ := resource.New(ctx, resource.WithAttributes(semconv.ServiceName(serviceName)))
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

func chooseExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	if ep := os.Getenv("SYNAPSE_OTLP_ENDPOINT"); ep != "" {
		return otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(ep),
			otlptracehttp.WithInsecure(), // TLS terminated by the collector/mesh in prod
		)
	}
	if os.Getenv("SYNAPSE_TRACE") == "stdout" {
		return stdouttrace.New(stdouttrace.WithoutTimestamps())
	}
	return nil, nil
}

// Tracer returns the named tracer for creating spans.
func Tracer() trace.Tracer { return otel.Tracer(serviceName) }

// Start begins a span; the returned context carries it to children.
func Start(ctx context.Context, name string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name)
}

// Inject writes the current trace context into a header map for the event bus.
func Inject(ctx context.Context) map[string]string {
	h := map[string]string{}
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(h))
	return h
}

// Extract rebuilds a context carrying the trace context from event headers, so a
// consumer's span links to the producer's trace.
func Extract(ctx context.Context, headers map[string]string) context.Context {
	if headers == nil {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(headers))
}
