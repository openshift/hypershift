package openstack

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

func TestGetCloudConfig(t *testing.T) {
	tests := []struct {
		name              string
		hcpSpec           *hyperv1.HostedControlPlaneSpec
		credentialsSecret *corev1.Secret
		expectedConfig    string
	}{
		{
			name: "basic config",
			hcpSpec: &hyperv1.HostedControlPlaneSpec{
				Networking: hyperv1.ClusterNetworking{
					MachineNetwork: []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.0.0/24")}},
				},
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.OpenStackPlatform,
					OpenStack: &hyperv1.OpenStackPlatformSpec{
						IdentityRef: hyperv1.OpenStackIdentityReference{
							CloudName: "test-cloud",
						},
					},
				},
			},
			credentialsSecret: &corev1.Secret{
				Data: map[string][]byte{
					CredentialsFile: []byte(""),
				},
			},
			expectedConfig: `[Global]
use-clouds = true
clouds-file=/etc/openstack/secret/clouds.yaml
cloud=test-cloud

[LoadBalancer]
max-shared-lb = 1
manage-security-groups = true

[Networking]
address-sort-order = 192.168.0.0/24
`,
		},
		{
			name: "with external network",
			hcpSpec: &hyperv1.HostedControlPlaneSpec{
				Networking: hyperv1.ClusterNetworking{
					MachineNetwork: []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.0.0/24")}},
				},
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.OpenStackPlatform,
					OpenStack: &hyperv1.OpenStackPlatformSpec{
						IdentityRef: hyperv1.OpenStackIdentityReference{
							CloudName: "test-cloud",
						},
						ExternalNetwork: &hyperv1.NetworkParam{
							ID: ptr.To("external-network-id"),
						},
					},
				},
			},
			credentialsSecret: &corev1.Secret{
				Data: map[string][]byte{
					CredentialsFile: []byte(""),
				},
			},
			expectedConfig: `[Global]
use-clouds = true
clouds-file=/etc/openstack/secret/clouds.yaml
cloud=test-cloud

[LoadBalancer]
max-shared-lb = 1
manage-security-groups = true
floating-network-id = external-network-id

[Networking]
address-sort-order = 192.168.0.0/24
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := getCloudConfig(*tt.hcpSpec, tt.credentialsSecret)
			if config != tt.expectedConfig {
				t.Errorf("expected %q, got %q", tt.expectedConfig, config)
			}
		})
	}
}
