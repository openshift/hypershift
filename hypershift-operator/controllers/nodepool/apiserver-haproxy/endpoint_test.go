package haproxy

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestResolveSharedIngressWorkerAPIEndpoint(t *testing.T) {
	for name, tc := range map[string]struct {
		endpoint    hyperv1.SharedIngressWorkerAPIEndpoint
		missingHCP  bool
		wantAddress string
		wantPort    int32
		wantErr     string
	}{
		"When HCP is missing, it should return the lookup error": {
			missingHCP: true,
			wantErr:    "failed to get hosted control plane",
		},
		"When status is empty, it should wait for the port": {
			wantErr: "waiting on hosted control plane shared ingress worker API port",
		},
		"When only a port is present, it should wait for an address": {
			endpoint: hyperv1.SharedIngressWorkerAPIEndpoint{Port: 6443},
			wantErr:  "waiting on hosted control plane shared ingress worker API endpoint",
		},
		"When IP and hostname are present, it should prefer IP": {
			endpoint: hyperv1.SharedIngressWorkerAPIEndpoint{
				IP:       "10.0.0.100",
				Hostname: "router.example.com",
				Port:     6443,
			},
			wantAddress: "10.0.0.100",
			wantPort:    6443,
		},
		"When only a hostname is present, it should use the hostname and published port": {
			endpoint: hyperv1.SharedIngressWorkerAPIEndpoint{
				Hostname: "router.example.com",
				Port:     7443,
			},
			wantAddress: "router.example.com",
			wantPort:    7443,
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
			address, port, err := ResolveSharedIngressWorkerAPIEndpoint(t.Context(), builder.Build(), key)
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
