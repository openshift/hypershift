package nodepool

import (
	"context"
	"sync"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/tracing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// testSpanExporter records exported spans for test assertions.
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

func findLastSpan(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for i := len(spans) - 1; i >= 0; i-- {
		if spans[i].Name() == name {
			return spans[i]
		}
	}
	return nil
}

func spanAttrStr(span sdktrace.ReadOnlySpan, key string) (string, bool) {
	for _, attr := range span.Attributes() {
		if string(attr.Key) == key {
			return attr.Value.AsString(), true
		}
	}
	return "", false
}

func setupTestTracing(t *testing.T) (*testSpanExporter, *sdktrace.TracerProvider) {
	t.Helper()
	exporter := &testSpanExporter{}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	originalTP := otel.GetTracerProvider()
	originalPropagator := otel.GetTextMapPropagator()
	originalTracer := nodePoolTracer
	otel.SetTracerProvider(tp)
	nodePoolTracer = otel.Tracer("hypershift/nodepool")
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Errorf("failed to shutdown TracerProvider: %v", err)
		}
		otel.SetTracerProvider(originalTP)
		otel.SetTextMapPropagator(originalPropagator)
		nodePoolTracer = originalTracer
	})
	return exporter, tp
}

func TestNodePoolReconcileTracingSpanAttributes(t *testing.T) {
	g := NewWithT(t)
	exporter, tp := setupTestTracing(t)

	hcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "test-ns",
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform:   hyperv1.PlatformSpec{Type: hyperv1.NonePlatform},
			Release:    hyperv1.Release{Image: "quay.io/ocp:4.15.0"},
			Etcd:       hyperv1.EtcdSpec{ManagementType: hyperv1.Managed},
			PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
			InfraID:    "infra-123",
		},
	}

	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-nodepool",
			Namespace: "test-ns",
		},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: "my-cluster",
			Release:     hyperv1.Release{Image: "quay.io/ocp:4.15.0"},
			Platform:    hyperv1.NodePoolPlatform{Type: hyperv1.NonePlatform},
			Replicas:    func() *int32 { v := int32(1); return &v }(),
			Management:  hyperv1.NodePoolManagement{UpgradeType: hyperv1.UpgradeTypeReplace},
		},
	}

	client := fake.NewClientBuilder().WithScheme(api.Scheme).
		WithObjects(hcluster, nodePool).WithStatusSubresource(nodePool).Build()

	r := &NodePoolReconciler{
		Client: client,
	}

	// Reconcile — we don't care about the result, just that the span has
	// the right attributes.
	_, _ = r.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: crclient.ObjectKeyFromObject(nodePool),
	})

	if err := tp.ForceFlush(t.Context()); err != nil {
		t.Errorf("ForceFlush failed: %v", err)
	}
	spans := exporter.getSpans()

	span := findSpan(spans, tracing.SpanNodePoolReconcile)
	g.Expect(span).ToNot(BeNil(),
		"When Reconcile is called, it should produce a 'NodePool.Reconcile' span")

	val, ok := spanAttrStr(span, string(tracing.AttrNodePoolName))
	g.Expect(ok).To(BeTrue(), "it should set nodepool.name")
	g.Expect(val).To(Equal("my-nodepool"))

	val, ok = spanAttrStr(span, string(tracing.AttrNodePoolNamespace))
	g.Expect(ok).To(BeTrue(), "it should set nodepool.namespace")
	g.Expect(val).To(Equal("test-ns"))

	val, ok = spanAttrStr(span, string(tracing.AttrNodePoolClusterName))
	g.Expect(ok).To(BeTrue(), "it should set nodepool.clusterName")
	g.Expect(val).To(Equal("my-cluster"))

	tracing.SetCorrelationAttrs("test.cluster.id")
	t.Cleanup(func() { tracing.SetCorrelationAttrs("") })

	// Re-reconcile with correlation attrs configured.
	_, _ = r.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: crclient.ObjectKeyFromObject(nodePool),
	})
	if err := tp.ForceFlush(t.Context()); err != nil {
		t.Errorf("ForceFlush failed: %v", err)
	}
	spans = exporter.getSpans()
	span = findLastSpan(spans, tracing.SpanNodePoolReconcile)
	val, ok = spanAttrStr(span, "test.cluster.id")
	g.Expect(ok).To(BeTrue(), "it should set the configured cluster ID attribute")
	g.Expect(val).To(Equal("infra-123"))
}

func TestNodePoolReconcileTracingNotFoundSkipsSpan(t *testing.T) {
	g := NewWithT(t)
	exporter, tp := setupTestTracing(t)

	client := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
	r := &NodePoolReconciler{Client: client}

	_, err := r.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: crclient.ObjectKey{Namespace: "missing", Name: "missing"},
	})
	g.Expect(err).ToNot(HaveOccurred())

	if err := tp.ForceFlush(t.Context()); err != nil {
		t.Errorf("ForceFlush failed: %v", err)
	}
	spans := exporter.getSpans()

	span := findSpan(spans, tracing.SpanNodePoolReconcile)
	g.Expect(span).To(BeNil(),
		"When the NodePool is not found, it should not create a reconcile span")
}

func TestNodePoolReconcileTracingRecordsError(t *testing.T) {
	g := NewWithT(t)
	exporter, tp := setupTestTracing(t)

	// Create a NodePool without a matching HostedCluster so reconcile fails.
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orphan-nodepool",
			Namespace: "test-ns",
		},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: "nonexistent-cluster",
			Release:     hyperv1.Release{Image: "quay.io/ocp:4.15.0"},
			Platform:    hyperv1.NodePoolPlatform{Type: hyperv1.NonePlatform},
			Replicas:    func() *int32 { v := int32(1); return &v }(),
			Management:  hyperv1.NodePoolManagement{UpgradeType: hyperv1.UpgradeTypeReplace},
		},
	}

	client := fake.NewClientBuilder().WithScheme(api.Scheme).
		WithObjects(nodePool).WithStatusSubresource(nodePool).Build()

	r := &NodePoolReconciler{Client: client}

	_, err := r.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: crclient.ObjectKeyFromObject(nodePool),
	})
	g.Expect(err).To(HaveOccurred(),
		"When the HostedCluster is missing, reconcile should fail")

	if err := tp.ForceFlush(t.Context()); err != nil {
		t.Errorf("ForceFlush failed: %v", err)
	}
	spans := exporter.getSpans()

	span := findSpan(spans, tracing.SpanNodePoolReconcile)
	g.Expect(span).ToNot(BeNil(),
		"it should create a span even when reconcile fails")

	// The error is recorded on the span before the early return in the
	// reconciler's error handling path.
	g.Expect(span.Status().Code.String()).To(Equal("Error"),
		"When reconcile returns an error, the span status should be Error")

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
}

func TestNodePoolReconcileTracingDeletingNodePool(t *testing.T) {
	g := NewWithT(t)
	exporter, tp := setupTestTracing(t)

	now := metav1.Now()
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "deleting-nodepool",
			Namespace:         "test-ns",
			DeletionTimestamp: &now,
			Finalizers:        []string{finalizer},
		},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: "my-cluster",
			Release:     hyperv1.Release{Image: "quay.io/ocp:4.15.0"},
			Platform:    hyperv1.NodePoolPlatform{Type: hyperv1.NonePlatform},
			Replicas:    func() *int32 { v := int32(1); return &v }(),
			Management:  hyperv1.NodePoolManagement{UpgradeType: hyperv1.UpgradeTypeReplace},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).
		WithObjects(nodePool).WithStatusSubresource(nodePool).Build()

	r := &NodePoolReconciler{
		Client:               fakeClient,
		KubevirtInfraClients: newKVInfraMapMock([]crclient.Object{nodePool}),
	}

	// Reconcile will process the deletion path.
	_, err := r.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: crclient.ObjectKeyFromObject(nodePool),
	})
	g.Expect(err).ToNot(HaveOccurred(), "deletion reconcile should succeed")

	if err := tp.ForceFlush(t.Context()); err != nil {
		t.Errorf("ForceFlush failed: %v", err)
	}
	spans := exporter.getSpans()

	span := findSpan(spans, tracing.SpanNodePoolReconcile)
	g.Expect(span).ToNot(BeNil(),
		"When reconciling a deleting NodePool, it should still create a span")

	found := false
	for _, attr := range span.Attributes() {
		if attr.Key == tracing.AttrNodePoolDeleting && attr.Value.AsBool() {
			found = true
		}
	}
	g.Expect(found).To(BeTrue(),
		"When the NodePool has a deletion timestamp, it should set nodepool.deleting=true")
}

func TestNodePoolReconcileTracingSpanLinkFromTraceparent(t *testing.T) {
	g := NewWithT(t)
	exporter, tp := setupTestTracing(t)

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
	))

	hcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "test-ns",
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform:   hyperv1.PlatformSpec{Type: hyperv1.NonePlatform},
			Release:    hyperv1.Release{Image: "quay.io/ocp:4.15.0"},
			Etcd:       hyperv1.EtcdSpec{ManagementType: hyperv1.Managed},
			PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
			InfraID:    "infra-123",
		},
	}

	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "linked-nodepool",
			Namespace: "test-ns",
			Annotations: map[string]string{
				"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
			},
		},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: "my-cluster",
			Release:     hyperv1.Release{Image: "quay.io/ocp:4.15.0"},
			Platform:    hyperv1.NodePoolPlatform{Type: hyperv1.NonePlatform},
			Replicas:    func() *int32 { v := int32(1); return &v }(),
			Management:  hyperv1.NodePoolManagement{UpgradeType: hyperv1.UpgradeTypeReplace},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).
		WithObjects(hcluster, nodePool).WithStatusSubresource(nodePool).Build()

	r := &NodePoolReconciler{Client: fakeClient}

	_, _ = r.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: crclient.ObjectKeyFromObject(nodePool),
	})

	if err := tp.ForceFlush(t.Context()); err != nil {
		t.Errorf("ForceFlush failed: %v", err)
	}
	spans := exporter.getSpans()

	var reconcileSpan sdktrace.ReadOnlySpan
	for _, s := range spans {
		if s.Name() == tracing.SpanNodePoolReconcile {
			reconcileSpan = s
			break
		}
	}
	g.Expect(reconcileSpan).ToNot(BeNil())

	links := reconcileSpan.Links()
	g.Expect(links).To(HaveLen(1),
		"When the NodePool has a traceparent annotation, the span should have exactly one link")
	g.Expect(links[0].SpanContext.TraceID().String()).To(Equal("0af7651916cd43dd8448eb211c80319c"),
		"the link should reference the CS trace ID from the traceparent annotation")
}
