package haproxy

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/sharedingress"
	"github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestResolveSharedIngressWorkerAPIEndpoint(t *testing.T) {
	for name, tc := range map[string]struct {
		endpoint                          *hyperv1.APIEndpoint
		missingHCP                        bool
		allowSharedIngressServiceFallback bool
		withRouterService                 bool
		routerIngress                     []corev1.LoadBalancerIngress
		wantAddress                       string
		wantPort                          int32
		wantErr                           string
	}{
		"When HCP is missing, it should return the lookup error": {
			missingHCP: true,
			wantErr:    "failed to get hosted control plane",
		},
		"When status is empty and fallback is disabled, it should wait for the endpoint": {
			wantErr: "waiting on hosted control plane shared ingress worker API endpoint",
		},
		"When only a port is present and fallback is disabled, it should wait for an address": {
			endpoint: &hyperv1.APIEndpoint{Port: 6443},
			wantErr:  "waiting on hosted control plane shared ingress worker API endpoint",
		},
		"When only a host is present and fallback is disabled, it should wait for the port": {
			endpoint: &hyperv1.APIEndpoint{Host: "10.0.0.100"},
			wantErr:  "waiting on hosted control plane shared ingress worker API endpoint",
		},
		"When a hostname is present, it should use it with the published port": {
			endpoint: &hyperv1.APIEndpoint{
				Host: "router.example.com",
				Port: 7443,
			},
			wantAddress: "router.example.com",
			wantPort:    7443,
		},
		"When status is published, it should prefer it over the router Service": {
			endpoint: &hyperv1.APIEndpoint{
				Host: "10.0.0.100",
				Port: 6443,
			},
			allowSharedIngressServiceFallback: true,
			withRouterService:                 true,
			routerIngress:                     []corev1.LoadBalancerIngress{{IP: "10.0.0.201"}},
			wantAddress:                       "10.0.0.100",
			wantPort:                          6443,
		},
		"When status is empty and the router Service reports an IP, it should fall back to it": {
			allowSharedIngressServiceFallback: true,
			withRouterService:                 true,
			routerIngress:                     []corev1.LoadBalancerIngress{{IP: "10.0.0.200"}},
			wantAddress:                       "10.0.0.200",
			wantPort:                          sharedingress.KASSVCLBPort,
		},
		"When status is empty and the router Service reports a hostname, it should fall back to it": {
			allowSharedIngressServiceFallback: true,
			withRouterService:                 true,
			routerIngress:                     []corev1.LoadBalancerIngress{{Hostname: "router.example.com"}},
			wantAddress:                       "router.example.com",
			wantPort:                          sharedingress.KASSVCLBPort,
		},
		"When status is empty and the router Service has no ingress, it should wait": {
			allowSharedIngressServiceFallback: true,
			withRouterService:                 true,
			wantErr:                           "waiting on sharedIngress route service to report .Status.LoadBalancer.Ingress",
		},
		"When status is empty and the router Service is missing, it should return the lookup error": {
			allowSharedIngressServiceFallback: true,
			wantErr:                           "failed to get shared ingress router service",
		},
	} {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)

			key := client.ObjectKey{Namespace: "clusters-cluster", Name: "cluster"}
			builder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if !tc.missingHCP {
				builder = builder.WithObjects(&hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name},
					Status:     hyperv1.HostedControlPlaneStatus{SharedIngressWorkerAPIEndpoint: tc.endpoint},
				})
			}
			if tc.withRouterService {
				router := sharedingress.RouterPublicService()
				router.Status.LoadBalancer.Ingress = tc.routerIngress
				builder = builder.WithObjects(router)
			}
			address, port, err := ResolveSharedIngressWorkerAPIEndpoint(t.Context(), builder.Build(), key, tc.allowSharedIngressServiceFallback)
			if tc.wantErr != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.wantErr)))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(address).To(Equal(tc.wantAddress))
			g.Expect(port).To(Equal(tc.wantPort))
		})
	}
}
