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
		platformSpec      *hyperv1.OpenStackPlatformSpec
		credentialsSecret *corev1.Secret
		machineNetwork    []hyperv1.MachineNetworkEntry
		expectedConfig    string
	}{
		{
			name: "basic config",
			platformSpec: &hyperv1.OpenStackPlatformSpec{
				IdentityRef: hyperv1.OpenStackIdentityReference{
					CloudName: "test-cloud",
				},
			},
			credentialsSecret: &corev1.Secret{
				Data: map[string][]byte{
					CloudConfigKey: []byte(""),
				},
			},
			machineNetwork: []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.0.0/24")}},
			expectedConfig: `[Global]
use-clouds = true
clouds-file = /etc/openstack/secret/clouds.yaml
cloud = test-cloud

[LoadBalancer]
max-shared-lb = 1
manage-security-groups = true

[Networking]
address-sort-order = 192.168.0.0/24
`,
		},
		{
			name: "with external network",
			platformSpec: &hyperv1.OpenStackPlatformSpec{
				IdentityRef: hyperv1.OpenStackIdentityReference{
					CloudName: "test-cloud",
				},
				ExternalNetwork: &hyperv1.NetworkParam{
					ID: ptr.To("external-network-id"),
				},
			},
			credentialsSecret: &corev1.Secret{
				Data: map[string][]byte{
					CloudConfigKey: []byte(""),
				},
			},
			machineNetwork: []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.0.0/24")}},
			expectedConfig: `[Global]
use-clouds = true
clouds-file = /etc/openstack/secret/clouds.yaml
cloud = test-cloud

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
			config := getCloudConfig(tt.platformSpec, tt.credentialsSecret, nil, tt.machineNetwork)
			if config != tt.expectedConfig {
				t.Errorf("expected %q, got %q", tt.expectedConfig, config)
			}
		})
	}
}
