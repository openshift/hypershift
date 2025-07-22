package tracing

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/go-logr/logr"
	errors "github.com/zgalor/weberr"
)

const (
	CorrelationIDAnnotation = "aro.correlation_id"
)

// Without a specific configuration, a noop tracer is used by default.
// At least two environment variables must be configured to enable trace export:
//   - name: OTEL_EXPORTER_OTLP_ENDPOINT
//     value: http(s)://<service>.<namespace>:4318
//   - name: OTEL_TRACES_EXPORTER
//     value: otlp
func ConfigureOpenTelemetryTracer(ctx context.Context, log logr.Logger, resourceAttrs ...attribute.KeyValue) (func(context.Context) error, error) {
	log.Info("initializing OpenTelemetry tracer")

	exp, err := autoexport.NewSpanExporter(ctx, autoexport.WithFallbackSpanExporter(newNoopFactory))
	if err != nil {
		return nil, errors.Errorf("failed to create OTEL exporter: %s", err)
	}

	opts := []resource.Option{resource.WithHost()}
	if len(resourceAttrs) > 0 {
		opts = append(opts, resource.WithAttributes(resourceAttrs...))
	}

	resources, err := resource.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to initialise trace resources: %w", err)
	}

	tp := tracesdk.NewTracerProvider(
		tracesdk.WithBatcher(exp),
		tracesdk.WithResource(resources),
	)
	otel.SetTracerProvider(tp)

	shutdown := func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return tp.Shutdown(ctx)
	}

	propagator := propagation.NewCompositeTextMapPropagator(propagation.Baggage{}, propagation.TraceContext{})
	otel.SetTextMapPropagator(propagator)

	otel.SetErrorHandler(otelErrorHandlerFunc(func(err error) {
		log.Error(err, "OpenTelemetry.ErrorHandler")
	}))

	return shutdown, nil
}

// TracingEnabled returns true if the environment variable OTEL_TRACES_EXPORTER
// to configure the OpenTelemetry Exporter is defined.
func TracingEnabled() bool {
	_, ok := os.LookupEnv("OTEL_TRACES_EXPORTER")
	return ok
}

type otelErrorHandlerFunc func(error)

// Handle implements otel.ErrorHandler
func (f otelErrorHandlerFunc) Handle(err error) {
	f(err)
}

func newNoopFactory(_ context.Context) (tracesdk.SpanExporter, error) {
	return &noopSpanExporter{}, nil
}

var _ tracesdk.SpanExporter = noopSpanExporter{}

// noopSpanExporter is an implementation of trace.SpanExporter that performs no operations.
type noopSpanExporter struct{}

// ExportSpans is part of trace.SpanExporter interface.
func (e noopSpanExporter) ExportSpans(ctx context.Context, spans []tracesdk.ReadOnlySpan) error {
	return nil
}

// Shutdown is part of trace.SpanExporter interface.
func (e noopSpanExporter) Shutdown(ctx context.Context) error {
	return nil
}

// StartRootSpan initiates a new parent trace.
func StartRootSpan(ctx context.Context, tracerName, spanName string) (context.Context, trace.Span) {
	return otel.GetTracerProvider().
		Tracer(tracerName).
		Start(
			ctx,
			spanName,
			trace.WithNewRoot(),
			trace.WithSpanKind(trace.SpanKindInternal),
		)
}

// StartChildSpan creates a new span linked to the parent span from the current context.
func StartChildSpan(ctx context.Context, tracerName, spanName string) (context.Context, trace.Span) {
	return trace.SpanFromContext(ctx).
		TracerProvider().
		Tracer(tracerName).
		Start(ctx, spanName)
}
