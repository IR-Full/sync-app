package tracing

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
)

func TestInitWithNoExporterInstallsANoop(t *testing.T) {
	// Unset is the default in every deployment that has not opted in, so it must
	// cost nothing and never fail.
	t.Setenv("SYNCAPP_OTLP_ENDPOINT", "")
	t.Setenv("SYNCAPP_OTLP_ENDPOINT", "")
	t.Setenv("SYNCAPP_TRACE", "")
	t.Setenv("SYNCAPP_TRACE", "")

	shutdown, err := Init(context.Background())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Init returned no shutdown function")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

// Init always installs the W3C propagator, exporter or not: that is what carries
// a trace across the event bus, and a no-op exporter must not disable it for a
// service whose peer IS exporting.
func TestInitAlwaysInstallsTheW3CPropagator(t *testing.T) {
	t.Setenv("SYNCAPP_TRACE", "")
	t.Setenv("SYNCAPP_TRACE", "")
	if _, err := Init(context.Background()); err != nil {
		t.Fatal(err)
	}

	prop := otel.GetTextMapPropagator()
	if prop == nil {
		t.Fatal("no propagator installed")
	}
	fields := prop.Fields()
	found := false
	for _, f := range fields {
		if f == "traceparent" {
			found = true
		}
	}
	if !found {
		t.Fatalf("propagator does not carry traceparent: %v", fields)
	}
}

// The regression this guards: the exporter was selected by SYNCAPP_TRACE while
// the README, ARCHITECTURE.md and this package's own doc comment all documented
// SYNCAPP_TRACE — so configuring it as documented produced silent no-op tracing.
func TestStdoutExporterRespondsToTheDocumentedVariable(t *testing.T) {
	t.Setenv("SYNCAPP_OTLP_ENDPOINT", "")
	t.Setenv("SYNCAPP_OTLP_ENDPOINT", "")
	t.Setenv("SYNCAPP_TRACE", "")
	t.Setenv("SYNCAPP_TRACE", "stdout")

	exp, err := chooseExporter(context.Background())
	if err != nil {
		t.Fatalf("chooseExporter: %v", err)
	}
	if exp == nil {
		t.Fatal("SYNCAPP_TRACE=stdout selected no exporter — tracing would silently do nothing")
	}
	_ = exp.Shutdown(context.Background())
}

func TestStdoutExporterStillAcceptsTheLegacyVariable(t *testing.T) {
	t.Setenv("SYNCAPP_OTLP_ENDPOINT", "")
	t.Setenv("SYNCAPP_OTLP_ENDPOINT", "")
	t.Setenv("SYNCAPP_TRACE", "")
	t.Setenv("SYNCAPP_TRACE", "stdout")

	exp, err := chooseExporter(context.Background())
	if err != nil {
		t.Fatalf("chooseExporter: %v", err)
	}
	if exp == nil {
		t.Fatal("a deployment configured before the rename lost its exporter")
	}
	_ = exp.Shutdown(context.Background())
}

func TestNoExporterSelectedWhenUnset(t *testing.T) {
	t.Setenv("SYNCAPP_OTLP_ENDPOINT", "")
	t.Setenv("SYNCAPP_OTLP_ENDPOINT", "")
	t.Setenv("SYNCAPP_TRACE", "")
	t.Setenv("SYNCAPP_TRACE", "")

	exp, err := chooseExporter(context.Background())
	if err != nil {
		t.Fatalf("chooseExporter: %v", err)
	}
	if exp != nil {
		t.Fatal("an exporter was built with nothing configured")
	}
}

// OTLP takes precedence: a deployment shipping to a collector should not also
// print every span to stdout.
func TestOTLPTakesPrecedenceOverStdout(t *testing.T) {
	t.Setenv("SYNCAPP_OTLP_ENDPOINT", "localhost:4318")
	t.Setenv("SYNCAPP_TRACE", "stdout")

	exp, err := chooseExporter(context.Background())
	if err != nil {
		t.Fatalf("chooseExporter: %v", err)
	}
	if exp == nil {
		t.Fatal("OTLP endpoint selected no exporter")
	}
	// The OTLP exporter does not connect until it ships a span, so building it
	// against an address nothing is listening on is safe here.
	_ = exp.Shutdown(context.Background())
}

func TestStartCreatesASpanAndDoesNotPanicWithoutAProvider(t *testing.T) {
	t.Setenv("SYNCAPP_TRACE", "")
	t.Setenv("SYNCAPP_TRACE", "")
	if _, err := Init(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, span := Start(context.Background(), "test.operation")
	if span == nil {
		t.Fatal("Start returned no span")
	}
	if ctx == nil {
		t.Fatal("Start returned no context")
	}
	span.End() // must be safe under the no-op provider
}

// Inject is what carries the trace across the event bus. With no active span it
// must produce something harmless rather than panic — the write path calls it on
// every publish.
func TestInjectWithoutASpanIsHarmless(t *testing.T) {
	t.Setenv("SYNCAPP_TRACE", "")
	if _, err := Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	headers := Inject(context.Background())
	if headers == nil {
		return // an empty map or nil are both fine; neither carries a trace
	}
	if _, ok := headers["traceparent"]; ok && len(headers["traceparent"]) == 0 {
		t.Fatal("emitted an empty traceparent header")
	}
}

func TestInjectExtractRoundTrip(t *testing.T) {
	t.Setenv("SYNCAPP_TRACE", "stdout")
	shutdown, err := Init(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	ctx, span := Start(context.Background(), "producer")
	headers := Inject(ctx)
	span.End()

	if headers["traceparent"] == "" {
		t.Fatal("no traceparent injected from an active span — the trace stops at the bus")
	}

	// The consumer side rebuilds the context from the headers.
	restored := Extract(context.Background(), headers)
	if restored == nil {
		t.Fatal("Extract returned no context")
	}
}
