package hostedcluster

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/pullsecret"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// TestEnqueueHostedClustersForReferencedSecret ensures a referenced Secret maps
// back to its owning HostedCluster even when the Secret carries no
// referenced-resource annotation, which is the delete+recreate case: the
// recreated Secret is a fresh object with no annotation, so it must be matched by
// the HostedCluster spec reference instead.
func TestEnqueueHostedClustersForReferencedSecret(t *testing.T) {
	ctx := context.Background()

	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "clusters"},
		Spec: hyperv1.HostedClusterSpec{
			OperatorConfiguration: &hyperv1.OperatorConfiguration{
				IngressOperator: &hyperv1.IngressOperatorSpec{
					DefaultCertificate: hyperv1.IngressDefaultCertificateReference{Name: "my-cert"},
				},
			},
		},
	}

	tests := []struct {
		name       string
		secretName string
		expected   []reconcile.Request
	}{
		{
			name:       "When a recreated referenced secret has no annotation, it should enqueue the owning HostedCluster",
			secretName: "my-cert",
			expected: []reconcile.Request{
				{NamespacedName: types.NamespacedName{Namespace: "clusters", Name: "hc"}},
			},
		},
		{
			name:       "When a secret is not referenced by any HostedCluster, it should enqueue nothing",
			secretName: "unrelated",
			expected:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithIndex(&hyperv1.HostedCluster{}, hostedClusterIngressDefaultCertSecretIndex, indexHostedClusterByIngressDefaultCertSecret).
				WithObjects(hc).
				Build()
			mapFn := enqueueHostedClustersFunc(metrics.MetricsSetTelemetry, "hypershift", fakeClient)

			// A freshly recreated secret carries no referenced-resource annotation.
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: tt.secretName, Namespace: "clusters"}}
			requests := mapFn(ctx, secret)

			if len(tt.expected) == 0 {
				g.Expect(requests).To(BeEmpty())
				return
			}
			g.Expect(requests).To(ConsistOf(tt.expected))
		})
	}
}

// TestEnqueueHostedClustersPinningControlPlaneRelease ensures a change to the
// operator-namespace control plane release pull secret enqueues every
// HostedCluster for which a control plane release is pinned, either by its spec
// or by a HostedControlPlane that still applies the pin, and only those.
func TestEnqueueHostedClustersPinningControlPlaneRelease(t *testing.T) {
	ctx := context.Background()

	pinnedImage := "registry.example.com/ocp-release:4.19.0"
	pinned := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "pinned", Namespace: "clusters"},
		Spec: hyperv1.HostedClusterSpec{
			ControlPlaneRelease: &hyperv1.Release{Image: pinnedImage},
		},
	}
	unpinned := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "unpinned", Namespace: "clusters"},
	}
	// The pin was cleared on the HostedCluster, but its HostedControlPlane has not
	// applied the unpin yet, for example because the HostedCluster is paused.
	unpinPending := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "unpin-pending", Namespace: "clusters"},
	}
	unpinPendingHCP := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "unpin-pending", Namespace: "clusters-unpin-pending"},
		Spec:       hyperv1.HostedControlPlaneSpec{ControlPlaneReleaseImage: &pinnedImage},
	}
	unpinApplied := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "unpin-applied", Namespace: "clusters"},
	}
	unpinAppliedHCP := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "unpin-applied", Namespace: "clusters-unpin-applied"},
	}

	g := NewGomegaWithT(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(pinned, unpinned, unpinPending, unpinPendingHCP, unpinApplied, unpinAppliedHCP).
		Build()
	mapFn := enqueueHostedClustersPinningControlPlaneRelease(fakeClient)

	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      pullsecret.ControlPlaneReleaseSecretName,
		Namespace: "hypershift",
	}}
	requests := mapFn(ctx, secret)
	g.Expect(requests).To(ConsistOf([]reconcile.Request{
		{NamespacedName: types.NamespacedName{Namespace: "clusters", Name: "pinned"}},
		{NamespacedName: types.NamespacedName{Namespace: "clusters", Name: "unpin-pending"}},
	}))
}

// TestControlPlaneReleasePullSecretPredicate ensures the dedicated watch only
// reacts to the provider Secret in the operator namespace, under its default
// name or the configured one.
func TestControlPlaneReleasePullSecretPredicate(t *testing.T) {
	const configuredName = "generated-by-another-system"

	tests := []struct {
		name           string
		configuredName string
		secretName     string
		namespace      string
		expected       bool
	}{
		{
			name:           "When a secret name is configured and that secret is in the operator namespace, it should match",
			configuredName: configuredName,
			secretName:     configuredName,
			namespace:      "hypershift",
			expected:       true,
		},
		{
			name:           "When a secret name is configured, it should not match the default name",
			configuredName: configuredName,
			secretName:     pullsecret.ControlPlaneReleaseSecretName,
			namespace:      "hypershift",
			expected:       false,
		},
		{
			name:           "When a secret with the configured name is in another namespace, it should not match",
			configuredName: configuredName,
			secretName:     configuredName,
			namespace:      "clusters",
			expected:       false,
		},
		{
			name:       "When no secret name is configured, it should not match another provider-named secret",
			secretName: configuredName,
			namespace:  "hypershift",
			expected:   false,
		},
		{
			name:       "When the control plane release pull secret is in the operator namespace, it should match",
			secretName: pullsecret.ControlPlaneReleaseSecretName,
			namespace:  "hypershift",
			expected:   true,
		},
		{
			name:       "When a secret with that name is in another namespace, it should not match",
			secretName: pullsecret.ControlPlaneReleaseSecretName,
			namespace:  "clusters",
			expected:   false,
		},
		{
			name:       "When another secret is in the operator namespace, it should not match",
			secretName: "some-other-secret",
			namespace:  "hypershift",
			expected:   false,
		},
		{
			name:       "When the synced copy is in a control plane namespace, it should not match",
			secretName: pullsecret.SyncedControlPlaneReleaseSecretName,
			namespace:  "clusters-example",
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			p := controlPlaneReleasePullSecretPredicate(pullsecret.SourceSecretKey("hypershift", tt.configuredName))
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: tt.secretName, Namespace: tt.namespace}}
			g.Expect(p.Create(event.CreateEvent{Object: secret})).To(Equal(tt.expected))
			g.Expect(p.Update(event.UpdateEvent{ObjectNew: secret})).To(Equal(tt.expected))
			g.Expect(p.Delete(event.DeleteEvent{Object: secret})).To(Equal(tt.expected))
		})
	}
}
