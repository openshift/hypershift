package hostedcluster

import (
	"context"
	"fmt"
	"sync"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/tracing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// testExporter captures spans for assertions (reused from tracing_test.go pattern).
type reportTestExporter struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (e *reportTestExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, spans...)
	return nil
}

func (e *reportTestExporter) Shutdown(context.Context) error { return nil }

func (e *reportTestExporter) getSpans() []sdktrace.ReadOnlySpan {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]sdktrace.ReadOnlySpan, len(e.spans))
	copy(out, e.spans)
	return out
}

func setupReportTestTracing(t *testing.T) *reportTestExporter {
	t.Helper()
	exporter := &reportTestExporter{}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	originalTP := otel.GetTracerProvider()
	originalPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}))
	hostedClusterTracer = otel.Tracer("hypershift/hostedcluster")
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(originalTP)
		otel.SetTextMapPropagator(originalPropagator)
	})
	return exporter
}

func findSpanByName(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

func spanAttrString(span sdktrace.ReadOnlySpan, key string) (string, bool) {
	for _, attr := range span.Attributes() {
		if string(attr.Key) == key {
			return attr.Value.AsString(), true
		}
	}
	return "", false
}

func spanAttrBool(span sdktrace.ReadOnlySpan, key string) (bool, bool) {
	for _, attr := range span.Attributes() {
		if string(attr.Key) == key {
			return attr.Value.AsBool(), true
		}
	}
	return false, false
}

func TestReconcileReportExecuteCreatesSpan(t *testing.T) {
	g := NewWithT(t)
	exporter := setupReportTestTracing(t)

	report := &reconcileReport{ctx: t.Context()}
	report.execute("TestOperation", nonCritical, func() error {
		return nil
	})

	otel.GetTracerProvider().(*sdktrace.TracerProvider).ForceFlush(t.Context())
	spans := exporter.getSpans()

	span := findSpanByName(spans, tracing.ReconcileSubSpan("TestOperation"))
	g.Expect(span).ToNot(BeNil(),
		"When execute is called, it should create a span named 'HostedCluster.Reconcile.<operation>'")

	val, ok := spanAttrString(span, string(tracing.AttrReconcileOperation))
	g.Expect(ok).To(BeTrue())
	g.Expect(val).To(Equal("TestOperation"))
}

func TestReconcileReportExecuteRecordsError(t *testing.T) {
	g := NewWithT(t)
	exporter := setupReportTestTracing(t)

	report := &reconcileReport{ctx: t.Context()}
	report.execute("FailingOp", critical, func() error {
		return fmt.Errorf("something broke")
	})

	otel.GetTracerProvider().(*sdktrace.TracerProvider).ForceFlush(t.Context())
	spans := exporter.getSpans()

	span := findSpanByName(spans, tracing.ReconcileSubSpan("FailingOp"))
	g.Expect(span).ToNot(BeNil())

	g.Expect(span.Status().Code.String()).To(Equal("Error"),
		"When the operation returns an error, the span status should be Error")

	events := span.Events()
	hasException := false
	for _, ev := range events {
		if ev.Name == "exception" {
			hasException = true
		}
	}
	g.Expect(hasException).To(BeTrue(),
		"When the operation returns an error, the span should have an exception event")

	val, ok := spanAttrBool(span, string(tracing.AttrReconcileCritical))
	g.Expect(ok).To(BeTrue())
	g.Expect(val).To(BeTrue(),
		"it should mark critical operations with reconcile.critical=true")
}

func TestReconcileReportExecuteOrBlockCreatesSpan(t *testing.T) {
	g := NewWithT(t)
	exporter := setupReportTestTracing(t)

	report := &reconcileReport{ctx: t.Context()}
	report.executeOrBlock("NonBlockedOp", func() error {
		return nil
	})

	otel.GetTracerProvider().(*sdktrace.TracerProvider).ForceFlush(t.Context())
	spans := exporter.getSpans()

	span := findSpanByName(spans, tracing.ReconcileSubSpan("NonBlockedOp"))
	g.Expect(span).ToNot(BeNil(),
		"When executeOrBlock runs without prior critical failures, it should create a span")
}

func TestReconcileReportBlockedOperationMarksSpan(t *testing.T) {
	g := NewWithT(t)
	exporter := setupReportTestTracing(t)

	report := &reconcileReport{ctx: t.Context()}
	// First, a critical failure
	report.execute("CriticalOp", critical, func() error {
		return fmt.Errorf("critical failure")
	})
	// Then a dependent operation that should be blocked
	report.executeOrBlock("DependentOp", func() error {
		return nil
	})

	otel.GetTracerProvider().(*sdktrace.TracerProvider).ForceFlush(t.Context())
	spans := exporter.getSpans()

	span := findSpanByName(spans, tracing.ReconcileSubSpan("DependentOp"))
	g.Expect(span).ToNot(BeNil(),
		"When an operation is blocked, it should still create a span")

	val, ok := spanAttrBool(span, string(tracing.AttrReconcileBlocked))
	g.Expect(ok).To(BeTrue())
	g.Expect(val).To(BeTrue(),
		"When an operation is blocked by a prior critical failure, it should set reconcile.blocked=true")
}

func TestReconcileReportMultipleOperationsCreateMultipleSpans(t *testing.T) {
	g := NewWithT(t)
	exporter := setupReportTestTracing(t)

	report := &reconcileReport{ctx: t.Context()}
	report.execute("OpA", critical, func() error { return nil })
	report.execute("OpB", nonCritical, func() error { return nil })
	report.executeOrBlock("OpC", func() error { return nil })

	otel.GetTracerProvider().(*sdktrace.TracerProvider).ForceFlush(t.Context())
	spans := exporter.getSpans()

	g.Expect(findSpanByName(spans, tracing.ReconcileSubSpan("OpA"))).ToNot(BeNil())
	g.Expect(findSpanByName(spans, tracing.ReconcileSubSpan("OpB"))).ToNot(BeNil())
	g.Expect(findSpanByName(spans, tracing.ReconcileSubSpan("OpC"))).ToNot(BeNil())
}
