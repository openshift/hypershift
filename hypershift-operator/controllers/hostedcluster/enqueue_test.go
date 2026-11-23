package hostedcluster

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/metrics"

	operatorv1 "github.com/openshift/api/operator/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// TestEnqueueHostedClustersFunc covers both referenced-object indexes of
// enqueueHostedClustersFunc. It consolidates the former legacy
// TestEnqueueHostedClustersForReferencedSecret (delete+recreate of the
// ingress default certificate) with the auth proxy trusted CA case, so
// enqueueHostedClustersFunc keeps a single top-level test.
func TestEnqueueHostedClustersFunc(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		// hc is the HostedCluster whose spec references the object.
		hc *hyperv1.HostedCluster
		// index is the field index backing the watch for the referenced kind.
		index referencedObjectIndex
		// object is handed to the mapping function as the API server would
		// deliver it right after a create or a recreate: no
		// referenced-resource annotation can exist yet, so the only match is
		// the HostedCluster spec reference.
		object   client.Object
		expected []reconcile.Request
	}{
		{
			name: "When a missing referenced configmap is created without an annotation, it should enqueue the owning HostedCluster",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "clusters"},
				Spec: hyperv1.HostedClusterSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						Authentication: hyperv1.AuthenticationOperatorSpec{
							Proxy: operatorv1.AuthenticationProxyConfig{
								TrustedCA: operatorv1.AuthenticationConfigMapReference{Name: "my-ca"},
							},
						},
					},
				},
			},
			index: referencedObjectIndex{
				field: hostedClusterAuthProxyTrustedCAConfigMapIndex,
				fn:    indexHostedClusterByAuthProxyTrustedCAConfigMap,
			},
			object: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "my-ca", Namespace: "clusters"}},
			expected: []reconcile.Request{
				{NamespacedName: types.NamespacedName{Namespace: "clusters", Name: "hc"}},
			},
		},
		{
			name: "When a configmap is not referenced by any HostedCluster, it should enqueue nothing",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "clusters"},
				Spec: hyperv1.HostedClusterSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						Authentication: hyperv1.AuthenticationOperatorSpec{
							Proxy: operatorv1.AuthenticationProxyConfig{
								TrustedCA: operatorv1.AuthenticationConfigMapReference{Name: "my-ca"},
							},
						},
					},
				},
			},
			index: referencedObjectIndex{
				field: hostedClusterAuthProxyTrustedCAConfigMapIndex,
				fn:    indexHostedClusterByAuthProxyTrustedCAConfigMap,
			},
			object:   &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "clusters"}},
			expected: nil,
		},
		{
			name: "When a recreated referenced secret has no annotation, it should enqueue the owning HostedCluster",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "clusters"},
				Spec: hyperv1.HostedClusterSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						IngressOperator: &hyperv1.IngressOperatorSpec{
							DefaultCertificate: hyperv1.IngressDefaultCertificateReference{Name: "my-cert"},
						},
					},
				},
			},
			index: referencedObjectIndex{
				field: hostedClusterIngressDefaultCertSecretIndex,
				fn:    indexHostedClusterByIngressDefaultCertSecret,
			},
			object: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "my-cert", Namespace: "clusters"}},
			expected: []reconcile.Request{
				{NamespacedName: types.NamespacedName{Namespace: "clusters", Name: "hc"}},
			},
		},
		{
			name: "When a secret is not referenced by any HostedCluster, it should enqueue nothing",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "clusters"},
				Spec: hyperv1.HostedClusterSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						IngressOperator: &hyperv1.IngressOperatorSpec{
							DefaultCertificate: hyperv1.IngressDefaultCertificateReference{Name: "my-cert"},
						},
					},
				},
			},
			index: referencedObjectIndex{
				field: hostedClusterIngressDefaultCertSecretIndex,
				fn:    indexHostedClusterByIngressDefaultCertSecret,
			},
			object:   &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "clusters"}},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithIndex(&hyperv1.HostedCluster{}, tt.index.field, tt.index.fn).
				WithObjects(tt.hc).
				Build()
			mapFn := enqueueHostedClustersFunc(metrics.MetricsSetTelemetry, "hypershift", fakeClient)

			requests := mapFn(ctx, tt.object)

			if len(tt.expected) == 0 {
				g.Expect(requests).To(BeEmpty())
				return
			}
			g.Expect(requests).To(ConsistOf(tt.expected))
		})
	}
}

// referencedObjectIndex carries the field index that backs the watch for one
// referenced-object kind in the table above.
type referencedObjectIndex struct {
	field string
	fn    client.IndexerFunc
}
