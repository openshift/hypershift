package kas

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	"k8s.io/utils/ptr"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/support/config"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TODO (cewong): Add tests for other params
func TestNewAPIServerParamsAPIAdvertiseAddressAndPort(t *testing.T) {
	tests := []struct {
		apiServiceMapping  hyperv1.ServicePublishingStrategyMapping
		name               string
		advertiseAddress   string
		serviceNetworkCIDR string
		port               *int32
		expectedAddress    string
		expectedPort       int32
	}{
		{
			name:               "not specified",
			expectedAddress:    config.DefaultAdvertiseIPv4Address,
			serviceNetworkCIDR: "10.0.0.0/24",
			expectedPort:       config.KASPodDefaultPort,
		},
		{
			name:               "address specified",
			advertiseAddress:   "1.2.3.4",
			serviceNetworkCIDR: "10.0.0.0/24",
			expectedAddress:    "1.2.3.4",
			expectedPort:       config.KASPodDefaultPort,
		},
		{
			name:               "port set for default service publishing strategies",
			port:               ptr.To[int32](6789),
			serviceNetworkCIDR: "10.0.0.0/24",
			expectedAddress:    config.DefaultAdvertiseIPv4Address,
			expectedPort:       6789,
		},
		{
			name: "port set for NodePort service Publishing Strategy",
			apiServiceMapping: hyperv1.ServicePublishingStrategyMapping{
				Service: hyperv1.APIServer,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					Type: hyperv1.NodePort,
				},
			},
			port:               ptr.To[int32](6789),
			serviceNetworkCIDR: "10.0.0.0/24",
			expectedAddress:    config.DefaultAdvertiseIPv4Address,
			expectedPort:       6789,
		},
	}

	imageProvider := imageprovider.NewFromImages(map[string]string{})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			hcp := &hyperv1.HostedControlPlane{}
			hcp.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{test.apiServiceMapping}
			hcp.Spec.Networking.ServiceNetwork = append(hcp.Spec.Networking.ServiceNetwork, hyperv1.ServiceNetworkEntry{CIDR: *ipnet.MustParseCIDR(test.serviceNetworkCIDR)})
			hcp.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{Port: test.port, AdvertiseAddress: ptr.To(test.advertiseAddress)}
			p := NewKubeAPIServerParams(context.Background(), hcp, imageProvider, "", 0, "", 0, false)
			if len(test.advertiseAddress) > 0 {
				g.Expect(test.advertiseAddress).To(Equal(test.expectedAddress))
			}
			g.Expect(p.KASPodPort).To(Equal(test.expectedPort))
		})
	}
}

func TestNewAPIServerParamsGoAwayChance(t *testing.T) {
	tests := []struct {
		name                 string
		hcp                  *hyperv1.HostedControlPlane
		expectedGoAwayChance string
	}{
		{
			name: "highly-available",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					ControllerAvailabilityPolicy: hyperv1.HighlyAvailable,
				},
			},
			expectedGoAwayChance: "0.001",
		},
		{
			name: "single-replica",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					ControllerAvailabilityPolicy: hyperv1.SingleReplica,
				},
			},
			expectedGoAwayChance: "0",
		},
		{
			name: "highly-available with annotation",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.KubeAPIServerGoAwayChance: "0.002",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ControllerAvailabilityPolicy: hyperv1.HighlyAvailable,
				},
			},
			expectedGoAwayChance: "0.002",
		},
		{
			name: "single-replica with annotation",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.KubeAPIServerGoAwayChance: "0.002",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ControllerAvailabilityPolicy: hyperv1.SingleReplica,
				},
			},
			expectedGoAwayChance: "0.002",
		},
	}

	imageProvider := imageprovider.NewFromImages(map[string]string{})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			p := NewKubeAPIServerParams(context.Background(), test.hcp, imageProvider, "", 0, "", 0, false)
			g.Expect(p.GoAwayChance).To(Equal(test.expectedGoAwayChance))
		})
	}
}
