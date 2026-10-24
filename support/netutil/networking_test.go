package netutil

import (
	"reflect"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"

	"k8s.io/utils/ptr"
)

const (
	DefaultAdvertiseIPv4Address = "172.20.0.1"
	DefaultAdvertiseIPv6Address = "fd00::1"
)

func TestGetAdvertiseAddress(t *testing.T) {
	tests := []struct {
		name string
		hcp  *hyperv1.HostedControlPlane
		want string
	}{
		{
			name: "When an AdvertiseAddress is set in the HCP, it should return it",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{
							AdvertiseAddress: ptr.To("192.168.1.1"),
						},
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{{
							CIDR: *ipnet.MustParseCIDR("2620:52:0:1306::1/64"),
						}},
					},
				},
			},
			want: "192.168.1.1",
		},
		{
			name: "When no AdvertiseAddress is set in the HCP, it should return IPv4 based default address",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{{
							CIDR: *ipnet.MustParseCIDR("192.168.1.0/24"),
						}},
					},
				},
			},
			want: DefaultAdvertiseIPv4Address,
		},
		{
			name: "When no AdvertiseAddress is set and ServiceNetwork is IPv6, it should return IPv6 based default address",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{{
							CIDR: *ipnet.MustParseCIDR("2620:52:0:1306::1/64"),
						}},
					},
				},
			},
			want: DefaultAdvertiseIPv6Address,
		},
		{
			name: "When no ServiceNetwork CIDR is set in the HCP, it should return IPv4 based default address",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{},
					},
				},
			},
			want: DefaultAdvertiseIPv4Address,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetAdvertiseAddress(tt.hcp, DefaultAdvertiseIPv4Address, DefaultAdvertiseIPv6Address); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetAdvertiseAddress() = %v, want %v", got, tt.want)
			}
		})
	}
}
func TestMachineNetworksToList(t *testing.T) {
	tests := []struct {
		name           string
		machineNetwork []hyperv1.MachineNetworkEntry
		want           string
	}{
		{
			name: "When a single CIDR is provided, it should return comma-separated list",
			machineNetwork: []hyperv1.MachineNetworkEntry{
				{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")},
			},
			want: "192.168.1.0/24",
		},
		{
			name: "When multiple CIDRs are provided, it should return comma-separated list",
			machineNetwork: []hyperv1.MachineNetworkEntry{
				{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")},
				{CIDR: *ipnet.MustParseCIDR("10.0.0.0/8")},
			},
			want: "192.168.1.0/24,10.0.0.0/8",
		},
		{
			name:           "When no CIDRs are provided, it should return empty string",
			machineNetwork: []hyperv1.MachineNetworkEntry{},
			want:           "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MachineNetworksToList(tt.machineNetwork); got != tt.want {
				t.Errorf("MachineNetworksToList() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHAProxyFrontendPort(t *testing.T) {
	tests := []struct {
		name string
		hcp  *hyperv1.HostedControlPlane
		want int32
	}{
		{
			name: "When apiServer.port is nil, it should return default 6443",
			hcp:  &hyperv1.HostedControlPlane{},
			want: 6443,
		},
		{
			name: "When apiServer.port is 443, it should return 443",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{
							Port: ptr.To[int32](443),
						},
					},
				},
			},
			want: 443,
		},
		{
			name: "When apiServer.port is 6443, it should return 6443",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{
							Port: ptr.To[int32](6443),
						},
					},
				},
			},
			want: 6443,
		},
		{
			name: "When apiServer.port is custom value, it should return that value",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{
							Port: ptr.To[int32](8443),
						},
					},
				},
			},
			want: 8443,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HAProxyFrontendPort(tt.hcp); got != tt.want {
				t.Errorf("HAProxyFrontendPort() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUsesNodeLocalKASProxy(t *testing.T) {
	tests := []struct {
		name         string
		platformType hyperv1.PlatformType
		want         bool
	}{
		{name: "When platform is Agent, it should return true", platformType: hyperv1.AgentPlatform, want: true},
		{name: "When platform is None, it should return true", platformType: hyperv1.NonePlatform, want: true},
		{name: "When platform is KubeVirt, it should return true", platformType: hyperv1.KubevirtPlatform, want: true},
		{name: "When platform is OpenStack, it should return true", platformType: hyperv1.OpenStackPlatform, want: true},
		{name: "When platform is AWS, it should return false", platformType: hyperv1.AWSPlatform, want: false},
		{name: "When platform is Azure, it should return false", platformType: hyperv1.AzurePlatform, want: false},
		{name: "When platform is GCP, it should return false", platformType: hyperv1.GCPPlatform, want: false},
		{name: "When platform is IBMCloud, it should return false", platformType: hyperv1.IBMCloudPlatform, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: tt.platformType},
				},
			}
			if got := UsesNodeLocalKASProxy(hcp); got != tt.want {
				t.Errorf("UsesNodeLocalKASProxy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsMultusDisabled(t *testing.T) {
	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected bool
	}{
		{
			name: "When DisableMultiNetwork is nil, it should default to false (multus enabled)",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{},
					},
				},
			},
			expected: false,
		},
		{
			name: "When DisableMultiNetwork is explicitly false, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{
							DisableMultiNetwork: func() *bool { b := false; return &b }(),
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "When DisableMultiNetwork is explicitly true, it should return true",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{
							DisableMultiNetwork: func() *bool { b := true; return &b }(),
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "When OperatorConfiguration is nil, it should default to false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{},
			},
			expected: false,
		},
		{
			name: "When ClusterNetworkOperator is nil, it should default to false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsDisableMultiNetwork(tt.hcp); got != tt.expected {
				t.Errorf("IsDisableMultiNetwork() = %v, want %v", got, tt.expected)
			}
		})
	}
}
