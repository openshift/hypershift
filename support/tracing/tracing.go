// Package tracing provides OpenTelemetry SDK tracing initialization for
// HyperShift components. It configures a TracerProvider that exports spans
// via OTLP when a collector endpoint is provided, and falls back to a no-op
// provider otherwise. This lets the same binary run with or without a
// collector sidecar.
package tracing

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Config holds tracing configuration passed from operator flags.
type Config struct {
	// Endpoint is the OTLP/gRPC collector endpoint. Empty disables tracing.
	Endpoint string
	// Sampler is the trace sampler type (e.g. parentbased_always_on, traceidratio).
	Sampler string
	// SamplerArg is the sampler argument (e.g. ratio 0.0-1.0).
	SamplerArg string
	// CorrelationAttrs is a comma-separated list of span attribute names for
	// cross-service correlation. Each configured key is set to the cluster's
	// infraID on every reconcile span. For example, ROSA sets "cs.cluster.id"
	// to correlate with Cluster Service traces. Empty disables correlation.
	CorrelationAttrs string
}

// InitProvider initializes the global TracerProvider. When cfg.Endpoint is
// non-empty it creates an OTLP/gRPC exporter targeting that endpoint;
// otherwise the provider is a no-op and the returned shutdown function does
// nothing.
//
// The caller must invoke the returned shutdown function on process exit
// to flush pending spans.
func InitProvider(ctx context.Context, serviceName string, cfg Config) (shutdown func(context.Context) error, err error) {
	SetCorrelationAttrs(cfg.CorrelationAttrs)

	if cfg.Endpoint == "" {
		otel.SetTracerProvider(noop.NewTracerProvider())
		return func(context.Context) error { return nil }, nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
		),
		resource.WithProcessRuntimeName(),
		resource.WithHost(),
	)
	if err != nil {
		return nil, fmt.Errorf("creating OTEL resource: %w", err)
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.Endpoint),
	)
	if err != nil {
		return nil, fmt.Errorf("creating OTLP trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(5*time.Second),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(parseSampler(cfg.Sampler, cfg.SamplerArg)),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// Tracer returns a named tracer from the global provider. Controllers
// should call this once during setup and reuse the returned Tracer.
func Tracer(name string) trace.Tracer {
	return otel.Tracer("hypershift/" + name)
}

// StartSpan is a convenience wrapper that starts a child span from the
// given context. It returns the updated context and span. The caller must
// call span.End() when the operation completes.
func StartSpan(ctx context.Context, t trace.Tracer, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return t.Start(ctx, name, opts...)
}

// parseSampler returns a trace sampler for the given name and argument.
// If name is empty, it defaults to parentbased_always_on (100% sampling).
func parseSampler(name, arg string) sdktrace.Sampler {
	parseRatio := func() float64 {
		if arg == "" {
			return 1.0
		}
		r, err := strconv.ParseFloat(arg, 64)
		if err != nil || r < 0 || r > 1 {
			return 1.0
		}
		return r
	}

	switch name {
	case "always_on":
		return sdktrace.AlwaysSample()
	case "always_off":
		return sdktrace.NeverSample()
	case "traceidratio":
		return sdktrace.TraceIDRatioBased(parseRatio())
	case "parentbased_always_on":
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	case "parentbased_always_off":
		return sdktrace.ParentBased(sdktrace.NeverSample())
	case "parentbased_traceidratio":
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(parseRatio()))
	default:
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
}

const (
	// TraceparentAnnotation is the W3C Trace Context annotation key used by
	// CS to inject trace context into ManifestWork payloads.
	TraceparentAnnotation = "traceparent"

	// TracestateAnnotation is the optional W3C tracestate companion.
	TracestateAnnotation = "tracestate"
)

// annotationCarrier adapts a map[string]string (Kubernetes annotations) to
// the propagation.TextMapCarrier interface so the OTEL propagator can
// extract trace context from resource annotations.
type annotationCarrier map[string]string

func (c annotationCarrier) Get(key string) string {
	return c[key]
}

func (c annotationCarrier) Set(string, string) {}
func (c annotationCarrier) Keys() []string     { return nil }

// SpanLinkFromAnnotations extracts W3C trace context from Kubernetes
// resource annotations and returns a trace.Link suitable for use with
// trace.WithLinks(). If no traceparent annotation is present, it returns
// an empty link with an invalid SpanContext.
func SpanLinkFromAnnotations(annotations map[string]string) trace.Link {
	if annotations == nil {
		return trace.Link{}
	}
	if _, ok := annotations[TraceparentAnnotation]; !ok {
		return trace.Link{}
	}

	ctx := otel.GetTextMapPropagator().Extract(context.Background(), annotationCarrier(annotations))
	sc := trace.SpanContextFromContext(ctx)
	return trace.Link{SpanContext: sc}
}
