package hostedcluster

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/metrics"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
