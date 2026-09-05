package tracing

import (
	"context"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestInitProvider_NoopWhenUnset(t *testing.T) {
	shutdown, err := InitProvider(context.Background(), "test-service", Config{})
	if err != nil {
		t.Fatalf("InitProvider returned error: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	tp := otel.GetTracerProvider()
	if _, ok := tp.(noop.TracerProvider); !ok {
		t.Errorf("When Endpoint is empty, it should use noop.TracerProvider, got %T", tp)
	}
}

func TestInitProvider_SDKProviderWhenSet(t *testing.T) {
	shutdown, err := InitProvider(context.Background(), "test-service", Config{
		Endpoint: "localhost:4317",
	})
	if err != nil {
		t.Fatalf("InitProvider returned error: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	tp := otel.GetTracerProvider()
	if _, ok := tp.(*sdktrace.TracerProvider); !ok {
		t.Errorf("When Endpoint is set, it should use *sdktrace.TracerProvider, got %T", tp)
	}
}

func TestInitProvider_ShutdownIsIdempotent(t *testing.T) {
	shutdown, err := InitProvider(context.Background(), "test-service", Config{})
	if err != nil {
		t.Fatalf("InitProvider returned error: %v", err)
	}

	if err := shutdown(context.Background()); err != nil {
		t.Errorf("first shutdown returned error: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("second shutdown returned error: %v", err)
	}
}

func TestTracer_ReturnsNamedTracer(t *testing.T) {
	tracer := Tracer("mycontroller")
	if tracer == nil {
		t.Fatal("Tracer returned nil")
	}
}

// spanCapturingExporter is a minimal SpanExporter that records exported spans
// for test assertions, without requiring the tracetest sub-package.
type spanCapturingExporter struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (e *spanCapturingExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, spans...)
	return nil
}

func (e *spanCapturingExporter) Shutdown(context.Context) error { return nil }

func (e *spanCapturingExporter) getSpans() []sdktrace.ReadOnlySpan {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]sdktrace.ReadOnlySpan, len(e.spans))
	copy(out, e.spans)
	return out
}

func TestTracer_CreatesSpansWithCorrectName(t *testing.T) {
	exporter := &spanCapturingExporter{}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	tracer := Tracer("testcontroller")
	_, span := tracer.Start(context.Background(), "TestOp")
	span.End()

	tp.ForceFlush(context.Background())

	spans := exporter.getSpans()
	if len(spans) != 1 {
		t.Fatalf("When a span is started and ended, it should produce exactly 1 span, got %d", len(spans))
	}
	if spans[0].Name() != "TestOp" {
		t.Errorf("it should have span name \"TestOp\", got %q", spans[0].Name())
	}

	scope := spans[0].InstrumentationScope()
	if scope.Name != "hypershift/testcontroller" {
		t.Errorf("it should have instrumentation scope \"hypershift/testcontroller\", got %q", scope.Name)
	}
}

func TestTracer_SpanRecordsErrorStatus(t *testing.T) {
	exporter := &spanCapturingExporter{}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	tracer := Tracer("errortest")
	_, span := tracer.Start(context.Background(), "FailingOp")
	span.RecordError(context.DeadlineExceeded)
	span.End()

	tp.ForceFlush(context.Background())

	spans := exporter.getSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	events := spans[0].Events()
	if len(events) == 0 {
		t.Fatal("When RecordError is called, the span should have at least one event")
	}

	foundException := false
	for _, ev := range events {
		if ev.Name == "exception" {
			foundException = true
		}
	}
	if !foundException {
		t.Error("it should have an 'exception' event from RecordError")
	}
}

func TestSpanLinkFromAnnotations_NoAnnotation(t *testing.T) {
	link := SpanLinkFromAnnotations(map[string]string{"foo": "bar"})
	if link.SpanContext.IsValid() {
		t.Error("When there is no traceparent annotation, the link should not be valid")
	}
}

func TestSpanLinkFromAnnotations_NilAnnotations(t *testing.T) {
	link := SpanLinkFromAnnotations(nil)
	if link.SpanContext.IsValid() {
		t.Error("When annotations are nil, the link should not be valid")
	}
}

func TestSpanLinkFromAnnotations_ValidTraceparent(t *testing.T) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
	))

	annotations := map[string]string{
		"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
	}

	link := SpanLinkFromAnnotations(annotations)
	if !link.SpanContext.IsValid() {
		t.Fatal("When a valid traceparent annotation exists, the link should be valid")
	}
	if link.SpanContext.TraceID().String() != "0af7651916cd43dd8448eb211c80319c" {
		t.Errorf("it should extract the correct trace ID, got %s", link.SpanContext.TraceID())
	}
	if link.SpanContext.SpanID().String() != "b7ad6b7169203331" {
		t.Errorf("it should extract the correct span ID, got %s", link.SpanContext.SpanID())
	}
	if !link.SpanContext.TraceFlags().IsSampled() {
		t.Error("it should extract the sampled flag")
	}
}

func TestSpanLinkFromAnnotations_InvalidTraceparent(t *testing.T) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
	))

	annotations := map[string]string{
		"traceparent": "garbage-not-a-traceparent",
	}

	link := SpanLinkFromAnnotations(annotations)
	if link.SpanContext.IsValid() {
		t.Error("When the traceparent annotation is malformed, the link should not be valid")
	}
}

func TestParseSampler_DefaultIsParentBasedAlwaysOn(t *testing.T) {
	s := parseSampler("", "")
	desc := s.Description()
	if !contains(desc, "ParentBased") || !contains(desc, "AlwaysOn") {
		t.Errorf("When no sampler is specified, default should be ParentBased(AlwaysOn), got %s", desc)
	}
}

func TestParseSampler_AlwaysOff(t *testing.T) {
	s := parseSampler("always_off", "")
	if s.Description() != "AlwaysOffSampler" {
		t.Errorf("When sampler=always_off, should be AlwaysOffSampler, got %s", s.Description())
	}
}

func TestParseSampler_TraceIDRatio(t *testing.T) {
	s := parseSampler("traceidratio", "0.5")
	if s.Description() != "TraceIDRatioBased{0.5}" {
		t.Errorf("When sampler=traceidratio with arg 0.5, got %s", s.Description())
	}
}

func TestParseSampler_ParentBasedTraceIDRatio(t *testing.T) {
	s := parseSampler("parentbased_traceidratio", "0.1")
	desc := s.Description()
	if desc == "" {
		t.Fatal("sampler description should not be empty")
	}
	if !contains(desc, "0.1") {
		t.Errorf("When parentbased_traceidratio with arg 0.1, description should contain '0.1', got %s", desc)
	}
}

func TestParseSampler_InvalidArgDefaultsToOne(t *testing.T) {
	s := parseSampler("traceidratio", "not-a-number")
	if s.Description() != "TraceIDRatioBased{1}" {
		t.Errorf("When arg is invalid, should default to ratio 1.0, got %s", s.Description())
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
