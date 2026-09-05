package hostedcluster

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/tracing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// testSpanExporter is a minimal SpanExporter that records exported spans
// for test assertions, without requiring the tracetest sub-package.
type testSpanExporter struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (e *testSpanExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, spans...)
	return nil
}

func (e *testSpanExporter) Shutdown(context.Context) error { return nil }

func (e *testSpanExporter) getSpans() []sdktrace.ReadOnlySpan {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]sdktrace.ReadOnlySpan, len(e.spans))
	copy(out, e.spans)
	return out
}

func findSpan(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

func spanAttr(span sdktrace.ReadOnlySpan, key string) (attribute.Value, bool) {
	for _, attr := range span.Attributes() {
		if string(attr.Key) == key {
			return attr.Value, true
		}
	}
	return attribute.Value{}, false
}

// setupTestTracing installs a test TracerProvider and returns the exporter
// and a cleanup function. It also overrides the package-level
// hostedClusterTracer so spans are captured.
func setupTestTracing(t *testing.T) *testSpanExporter {
	t.Helper()
	exporter := &testSpanExporter{}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	originalTP := otel.GetTracerProvider()
	originalPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	hostedClusterTracer = otel.Tracer("hypershift/hostedcluster")
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(originalTP)
		otel.SetTextMapPropagator(originalPropagator)
	})
	return exporter
}

// noopReconciler returns a HostedClusterReconciler that uses overwriteReconcile
// to return immediately, so we only test the tracing wrapper around Reconcile()
// without needing the full controller infrastructure.
func noopReconciler(client crclient.Client) *HostedClusterReconciler {
	return &HostedClusterReconciler{
		Client: client,
		Clock:  clocktesting.NewFakeClock(time.Now()),
		now:    func() metav1.Time { return metav1.NewTime(time.Now()) },
		overwriteReconcile: func(ctx context.Context, req ctrl.Request, log logr.Logger, hcluster *hyperv1.HostedCluster) (ctrl.Result, error) {
			return ctrl.Result{}, nil
		},
	}
}

func TestReconcileTracingSpanAttributes(t *testing.T) {
	g := NewWithT(t)
	exporter := setupTestTracing(t)

	hcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-ns",
		},
		Spec: hyperv1.HostedClusterSpec{
			ClusterID:  "cid-1234",
			InfraID:    "infra-abc",
			Platform:   hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
			PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
			Release:    hyperv1.Release{Image: "quay.io/ocp:4.15.0"},
			Etcd:       hyperv1.EtcdSpec{ManagementType: hyperv1.Managed},
		},
	}

	client := fake.NewClientBuilder().WithScheme(api.Scheme).
		WithObjects(hcluster).WithStatusSubresource(hcluster).Build()

	r := noopReconciler(client)

	_, err := r.Reconcile(t.Context(), ctrl.Request{
		NamespacedName: crclient.ObjectKeyFromObject(hcluster),
	})
	g.Expect(err).ToNot(HaveOccurred())

	otel.GetTracerProvider().(*sdktrace.TracerProvider).ForceFlush(t.Context())
	spans := exporter.getSpans()

	span := findSpan(spans, tracing.SpanHostedClusterReconcile)
	g.Expect(span).ToNot(BeNil(), "When Reconcile succeeds, it should produce a 'HostedCluster.Reconcile' span")

	val, ok := spanAttr(span, string(tracing.AttrHostedClusterName))
	g.Expect(ok).To(BeTrue(), "it should set hostedcluster.name")
	g.Expect(val.AsString()).To(Equal("test-cluster"))

	val, ok = spanAttr(span, string(tracing.AttrHostedClusterNamespace))
	g.Expect(ok).To(BeTrue(), "it should set hostedcluster.namespace")
	g.Expect(val.AsString()).To(Equal("test-ns"))

	val, ok = spanAttr(span, string(tracing.AttrHostedClusterClusterID))
	g.Expect(ok).To(BeTrue(), "it should set hostedcluster.clusterID")
	g.Expect(val.AsString()).To(Equal("cid-1234"))

	val, ok = spanAttr(span, string(tracing.AttrHostedClusterInfraID))
	g.Expect(ok).To(BeTrue(), "it should set hostedcluster.infraID")
	g.Expect(val.AsString()).To(Equal("infra-abc"))

	val, ok = spanAttr(span, string(tracing.AttrHostedClusterPlatform))
	g.Expect(ok).To(BeTrue(), "it should set hostedcluster.platform")
	g.Expect(val.AsString()).To(Equal("AWS"))
}

func TestReconcileTracingNotFoundSkipsSpan(t *testing.T) {
	g := NewWithT(t)
	exporter := setupTestTracing(t)

	client := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
	r := noopReconciler(client)

	_, err := r.Reconcile(t.Context(), ctrl.Request{
		NamespacedName: crclient.ObjectKey{Namespace: "missing", Name: "missing"},
	})
	g.Expect(err).ToNot(HaveOccurred())

	otel.GetTracerProvider().(*sdktrace.TracerProvider).ForceFlush(t.Context())
	spans := exporter.getSpans()

	span := findSpan(spans, tracing.SpanHostedClusterReconcile)
	g.Expect(span).To(BeNil(),
		"When the HostedCluster is not found, it should not create a reconcile span")
}

func TestReconcileTracingDeletingCluster(t *testing.T) {
	g := NewWithT(t)
	exporter := setupTestTracing(t)

	now := metav1.Now()
	hcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "deleting-cluster",
			Namespace:         "test-ns",
			DeletionTimestamp: &now,
			Finalizers:        []string{HostedClusterFinalizer},
		},
		Spec: hyperv1.HostedClusterSpec{
			ClusterID:  "del-1234",
			InfraID:    "del-infra",
			Platform:   hyperv1.PlatformSpec{Type: hyperv1.NonePlatform},
			PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
			Release:    hyperv1.Release{Image: "quay.io/ocp:4.15.0"},
			Etcd:       hyperv1.EtcdSpec{ManagementType: hyperv1.Managed},
		},
	}

	client := fake.NewClientBuilder().WithScheme(api.Scheme).
		WithObjects(hcluster).WithStatusSubresource(hcluster).Build()

	r := noopReconciler(client)

	_, err := r.Reconcile(t.Context(), ctrl.Request{
		NamespacedName: crclient.ObjectKeyFromObject(hcluster),
	})
	g.Expect(err).ToNot(HaveOccurred())

	otel.GetTracerProvider().(*sdktrace.TracerProvider).ForceFlush(t.Context())
	spans := exporter.getSpans()

	span := findSpan(spans, tracing.SpanHostedClusterReconcile)
	g.Expect(span).ToNot(BeNil(),
		"When reconciling a deleting cluster, it should still create a span")

	val, ok := spanAttr(span, string(tracing.AttrHostedClusterDeleting))
	g.Expect(ok).To(BeTrue(), "it should set hostedcluster.deleting attribute")
	g.Expect(val.AsBool()).To(BeTrue(), "hostedcluster.deleting should be true")
}

func TestReconcileTracingRecordError(t *testing.T) {
	g := NewWithT(t)
	exporter := setupTestTracing(t)

	hcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "error-cluster",
			Namespace: "test-ns",
		},
		Spec: hyperv1.HostedClusterSpec{
			ClusterID:  "err-1234",
			Platform:   hyperv1.PlatformSpec{Type: hyperv1.NonePlatform},
			PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
			Release:    hyperv1.Release{Image: "quay.io/ocp:4.15.0"},
			Etcd:       hyperv1.EtcdSpec{ManagementType: hyperv1.Managed},
		},
	}

	client := fake.NewClientBuilder().WithScheme(api.Scheme).
		WithObjects(hcluster).WithStatusSubresource(hcluster).Build()

	reconcileErr := fmt.Errorf("simulated reconcile failure")
	r := &HostedClusterReconciler{
		Client: client,
		Clock:  clocktesting.NewFakeClock(time.Now()),
		now:    func() metav1.Time { return metav1.NewTime(time.Now()) },
		overwriteReconcile: func(ctx context.Context, req ctrl.Request, log logr.Logger, hcluster *hyperv1.HostedCluster) (reconcile.Result, error) {
			return ctrl.Result{}, reconcileErr
		},
	}

	_, err := r.Reconcile(t.Context(), ctrl.Request{
		NamespacedName: crclient.ObjectKeyFromObject(hcluster),
	})
	// The reconciler wraps the error in status update aggregation, so just
	// check that an error was returned.
	g.Expect(err).To(HaveOccurred())

	otel.GetTracerProvider().(*sdktrace.TracerProvider).ForceFlush(t.Context())
	spans := exporter.getSpans()

	span := findSpan(spans, tracing.SpanHostedClusterReconcile)
	g.Expect(span).ToNot(BeNil(), "it should create a span even when reconcile fails")

	// Verify the span recorded an error event.
	events := span.Events()
	foundException := false
	for _, ev := range events {
		if ev.Name == "exception" {
			foundException = true
			break
		}
	}
	g.Expect(foundException).To(BeTrue(),
		"When reconcile returns an error, the span should have an 'exception' event")

	// Verify the span status is Error.
	g.Expect(span.Status().Code.String()).To(Equal("Error"),
		"When reconcile returns an error, the span status should be Error")
}

func TestReconcileTracingNoClusterIDOmitsAttribute(t *testing.T) {
	g := NewWithT(t)
	exporter := setupTestTracing(t)

	hcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "no-id-cluster",
			Namespace: "test-ns",
		},
		Spec: hyperv1.HostedClusterSpec{
			// ClusterID and InfraID intentionally left empty.
			Platform:   hyperv1.PlatformSpec{Type: hyperv1.NonePlatform},
			PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
			Release:    hyperv1.Release{Image: "quay.io/ocp:4.15.0"},
			Etcd:       hyperv1.EtcdSpec{ManagementType: hyperv1.Managed},
		},
	}

	client := fake.NewClientBuilder().WithScheme(api.Scheme).
		WithObjects(hcluster).WithStatusSubresource(hcluster).Build()

	r := noopReconciler(client)

	_, err := r.Reconcile(t.Context(), ctrl.Request{
		NamespacedName: crclient.ObjectKeyFromObject(hcluster),
	})
	g.Expect(err).ToNot(HaveOccurred())

	otel.GetTracerProvider().(*sdktrace.TracerProvider).ForceFlush(t.Context())
	spans := exporter.getSpans()

	span := findSpan(spans, tracing.SpanHostedClusterReconcile)
	g.Expect(span).ToNot(BeNil())

	_, ok := spanAttr(span, string(tracing.AttrHostedClusterClusterID))
	g.Expect(ok).To(BeFalse(),
		"When ClusterID is empty, hostedcluster.clusterID attribute should not be set")

	_, ok = spanAttr(span, string(tracing.AttrHostedClusterInfraID))
	g.Expect(ok).To(BeFalse(),
		"When InfraID is empty, hostedcluster.infraID attribute should not be set")
}

func TestReconcileTracingSpanLinkFromTraceparent(t *testing.T) {
	g := NewWithT(t)
	exporter := setupTestTracing(t)

	// Set up propagator for trace context extraction.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
	))

	hcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "linked-cluster",
			Namespace: "test-ns",
			Annotations: map[string]string{
				"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
			},
		},
		Spec: hyperv1.HostedClusterSpec{
			ClusterID:  "link-1234",
			Platform:   hyperv1.PlatformSpec{Type: hyperv1.NonePlatform},
			PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
			Release:    hyperv1.Release{Image: "quay.io/ocp:4.15.0"},
			Etcd:       hyperv1.EtcdSpec{ManagementType: hyperv1.Managed},
		},
	}

	client := fake.NewClientBuilder().WithScheme(api.Scheme).
		WithObjects(hcluster).WithStatusSubresource(hcluster).Build()

	r := noopReconciler(client)

	_, err := r.Reconcile(t.Context(), ctrl.Request{
		NamespacedName: crclient.ObjectKeyFromObject(hcluster),
	})
	g.Expect(err).ToNot(HaveOccurred())

	otel.GetTracerProvider().(*sdktrace.TracerProvider).ForceFlush(t.Context())
	spans := exporter.getSpans()

	var reconcileSpan sdktrace.ReadOnlySpan
	for _, s := range spans {
		if s.Name() == tracing.SpanHostedClusterReconcile {
			reconcileSpan = s
			break
		}
	}
	g.Expect(reconcileSpan).ToNot(BeNil())

	links := reconcileSpan.Links()
	g.Expect(links).To(HaveLen(1),
		"When the HostedCluster has a traceparent annotation, the span should have exactly one link")
	g.Expect(links[0].SpanContext.TraceID().String()).To(Equal("0af7651916cd43dd8448eb211c80319c"),
		"the link should reference the CS trace ID from the traceparent annotation")
	g.Expect(links[0].SpanContext.SpanID().String()).To(Equal("b7ad6b7169203331"),
		"the link should reference the CS span ID from the traceparent annotation")
}

func TestReconcileTracingNoSpanLinkWithoutTraceparent(t *testing.T) {
	g := NewWithT(t)
	exporter := setupTestTracing(t)

	hcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unlinked-cluster",
			Namespace: "test-ns",
		},
		Spec: hyperv1.HostedClusterSpec{
			ClusterID:  "nolink-1234",
			Platform:   hyperv1.PlatformSpec{Type: hyperv1.NonePlatform},
			PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
			Release:    hyperv1.Release{Image: "quay.io/ocp:4.15.0"},
			Etcd:       hyperv1.EtcdSpec{ManagementType: hyperv1.Managed},
		},
	}

	client := fake.NewClientBuilder().WithScheme(api.Scheme).
		WithObjects(hcluster).WithStatusSubresource(hcluster).Build()

	r := noopReconciler(client)

	_, err := r.Reconcile(t.Context(), ctrl.Request{
		NamespacedName: crclient.ObjectKeyFromObject(hcluster),
	})
	g.Expect(err).ToNot(HaveOccurred())

	otel.GetTracerProvider().(*sdktrace.TracerProvider).ForceFlush(t.Context())
	spans := exporter.getSpans()

	var reconcileSpan sdktrace.ReadOnlySpan
	for _, s := range spans {
		if s.Name() == tracing.SpanHostedClusterReconcile {
			reconcileSpan = s
			break
		}
	}
	g.Expect(reconcileSpan).ToNot(BeNil())
	g.Expect(reconcileSpan.Links()).To(BeEmpty(),
		"When there is no traceparent annotation, the span should have no links")
}
