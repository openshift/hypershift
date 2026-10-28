package hostedcluster

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestReconcilePrivateAPIServerCleanup(t *testing.T) {
	t.Parallel()
	const endpoint = "private=false,server=https://api.public.example.com:6443"
	tests := []struct {
		name            string
		access          hyperv1.AWSEndpointAccessType
		workerEndpoint  string
		noWorkers       bool
		missingSecret   bool
		missingService  bool
		endpointService bool
		failEndpointGet bool
		privateConfig   bool
		failList        bool
		karpenter       bool
		unmanaged       bool
		wantCleanup     bool
	}{
		{name: "When the workers have not reported their endpoint, it should retain the private endpoint", access: hyperv1.Public},
		{name: "When a worker still uses the private endpoint, it should retain the private endpoint", access: hyperv1.Public, workerEndpoint: "private=true"},
		{name: "When all workers have completed public cutover, it should authorize cleanup", access: hyperv1.Public, workerEndpoint: endpoint, wantCleanup: true},
		{name: "When the cluster has no NodePools, it should authorize cleanup", access: hyperv1.Public, noWorkers: true, wantCleanup: true},
		{name: "When the cluster returns to private access, it should revoke previous cleanup authorization", access: hyperv1.Private, workerEndpoint: endpoint},
		{name: "When public and private access is desired, it should retain the private endpoint", access: hyperv1.PublicAndPrivate, workerEndpoint: endpoint},
		{name: "When the kubeconfig is still private, it should wait for public endpoint configuration", access: hyperv1.Public, noWorkers: true, privateConfig: true},
		{name: "When the kubeconfig is not yet available, it should wait without blocking HCP reconciliation", access: hyperv1.Public, missingSecret: true},
		{name: "When NodePools cannot be listed, it should fail without authorizing cleanup", access: hyperv1.Public, failList: true},
		{name: "When Karpenter manages the workers, it should not infer cutover from an empty NodePool list", access: hyperv1.Public, noWorkers: true, karpenter: true},
		{name: "When worker management is disabled, it should not infer cutover from an empty NodePool list", access: hyperv1.Public, noWorkers: true, unmanaged: true},
		{name: "When the Service is absent but PrivateLink remains after worker cutover, it should authorize the remaining cleanup", access: hyperv1.Public, workerEndpoint: endpoint, missingService: true, endpointService: true, wantCleanup: true},
		{name: "When the Service is absent but workers still use PrivateLink, it should retain the endpoint service", access: hyperv1.Public, workerEndpoint: "private=true", missingService: true, endpointService: true},
		{name: "When both private resources are absent, it should revoke obsolete cleanup authorization", access: hyperv1.Public, workerEndpoint: endpoint, missingService: true},
		{name: "When the remaining endpoint service cannot be read, it should report the error without authorizing cleanup", access: hyperv1.Public, missingService: true, endpointService: true, failEndpointGet: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "clusters"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: tt.access}},
					Services: []hyperv1.ServicePublishingStrategyMapping{{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}}},
				},
				Status: hyperv1.HostedClusterStatus{KubeConfig: &corev1.LocalObjectReference{Name: "admin-kubeconfig"}},
			}
			hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Name: hc.Name, Namespace: "clusters-hc", Annotations: map[string]string{config.PrivateAPIServerCleanupAnnotation: "previous-authorization"}}}
			if tt.karpenter {
				hc.Spec.AutoNode = autoNode
			}
			if tt.unmanaged {
				hc.Annotations = map[string]string{hyperv1.DisableMachineManagement: "true"}
			}
			server := "https://api.public.example.com:6443"
			if tt.privateConfig {
				server = "https://api.hc.hypershift.local:6443"
			}
			var objects []client.Object
			privateService := cpomanifests.KubeAPIServerPrivateService(hcp.Namespace)
			if !tt.missingService {
				objects = append(objects, privateService)
			}
			if tt.endpointService {
				objects = append(objects, &hyperv1.AWSEndpointService{ObjectMeta: metav1.ObjectMeta{Name: privateService.Name, Namespace: hcp.Namespace}})
			}
			if !tt.missingSecret {
				objects = append(objects, &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "admin-kubeconfig", Namespace: hc.Namespace},
					Data:       map[string][]byte{"kubeconfig": []byte("apiVersion: v1\nkind: Config\nclusters:\n- name: cluster\n  cluster:\n    server: " + server + "\ncontexts:\n- name: default\n  context:\n    cluster: cluster\ncurrent-context: default\n")},
				})
			}
			if !tt.noWorkers {
				objects = append(objects, &hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{Name: "workers", Namespace: hc.Namespace, Annotations: map[string]string{config.NodePoolAPIEndpointAnnotation: tt.workerEndpoint}},
					Spec:       hyperv1.NodePoolSpec{ClusterName: hc.Name},
				})
			}
			// NodePools belonging to other HostedClusters must not block cleanup.
			objects = append(objects, &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: hc.Namespace}, Spec: hyperv1.NodePoolSpec{ClusterName: "other"}})
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*hyperv1.AWSEndpointService); ok && tt.failEndpointGet {
						return apierrors.NewServiceUnavailable("API unavailable")
					}
					return c.Get(ctx, key, obj, opts...)
				},
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if tt.failList {
						return apierrors.NewServiceUnavailable("API unavailable")
					}
					return c.List(ctx, list, opts...)
				},
			}).Build()
			r := &HostedClusterReconciler{Client: c}
			err := r.reconcilePrivateAPIServerCleanup(t.Context(), hcp, hc)
			if tt.failList {
				g.Expect(err).To(MatchError(ContainSubstring("failed to list NodePools")))
			} else if tt.failEndpointGet {
				g.Expect(err).To(MatchError(ContainSubstring("API unavailable")))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(r.reconcilePrivateAPIServerCleanup(t.Context(), hcp, hc)).To(Succeed())
			}
			if tt.wantCleanup {
				g.Expect(hcp.Annotations).To(HaveKeyWithValue(config.PrivateAPIServerCleanupAnnotation, endpoint))
			} else {
				g.Expect(hcp.Annotations).NotTo(HaveKey(config.PrivateAPIServerCleanupAnnotation))
			}
		})
	}
}
