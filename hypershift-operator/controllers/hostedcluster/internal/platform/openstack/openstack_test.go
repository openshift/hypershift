package openstack

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"k8s.io/utils/ptr"

	"github.com/openshift/hypershift/api/util/ipnet"
	capo "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

func TestReconcileOpenStackCluster(t *testing.T) {
	const externalNetworkID = "a42211a2-4d2c-426f-9413-830e4b4abbbc"
	const networkID = "803084c1-70a2-44d3-a484-3b9c08dedee0"
	const subnetID = "e08dd45e-1bce-42c7-a5a9-3f7e1e55640e"
	apiEndpoint := hyperv1.APIEndpoint{
		Host: "api-endpoint",
		Port: 6443,
	}
	testCases := []struct {
		name                         string
		hostedCluster                *hyperv1.HostedCluster
		expectedOpenStackClusterSpec capo.OpenStackClusterSpec
		wantErr                      bool
	}{
		{
			name: "CAPO provisioned network and subnet",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "cluster-123",
					Networking: hyperv1.ClusterNetworking{
						MachineNetwork: []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("10.0.0.0/24")}},
					},
					Platform: hyperv1.PlatformSpec{
						OpenStack: &hyperv1.OpenStackPlatformSpec{
							IdentityRef: hyperv1.OpenStackIdentityReference{
								Name:      "openstack-credentials",
								CloudName: "openstack",
							},
							ManagedSubnets: []hyperv1.SubnetSpec{{
								DNSNameservers: []string{"1.1.1.1"},
								AllocationPools: []hyperv1.AllocationPool{{
									Start: "10.0.0.1",
									End:   "10.0.0.10",
								}}}},
							NetworkMTU: ptr.To(1500),
						}},
				},
			},
			expectedOpenStackClusterSpec: capo.OpenStackClusterSpec{
				IdentityRef: capo.OpenStackIdentityReference{
					Name:      "openstack-credentials",
					CloudName: "openstack",
				},
				ManagedSubnets: []capo.SubnetSpec{{
					CIDR:           "10.0.0.0/24",
					DNSNameservers: []string{"1.1.1.1"},
					AllocationPools: []capo.AllocationPool{{
						Start: "10.0.0.1",
						End:   "10.0.0.10",
					}}},
				},
				NetworkMTU: ptr.To(1500),
				ControlPlaneEndpoint: &capiv1.APIEndpoint{
					Host: "api-endpoint",
					Port: 6443,
				},
				DisableAPIServerFloatingIP: ptr.To(true),
				ManagedSecurityGroups: &capo.ManagedSecurityGroups{
					AllNodesSecurityGroupRules: defaultWorkerSecurityGroupRules([]string{"10.0.0.0/24"}),
				},
				Tags: []string{"openshiftClusterID=cluster-123"},
			},
			wantErr: false,
		},
		{
			name: "User provided network and subnet by ID on hosted cluster",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "cluster-123",
					Networking: hyperv1.ClusterNetworking{
						MachineNetwork: []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
					},
					Platform: hyperv1.PlatformSpec{
						OpenStack: &hyperv1.OpenStackPlatformSpec{
							IdentityRef: hyperv1.OpenStackIdentityReference{
								Name:      "openstack-credentials",
								CloudName: "openstack",
							},
							ExternalNetwork: &hyperv1.NetworkParam{
								ID: ptr.To(externalNetworkID),
							},
							Network: &hyperv1.NetworkParam{
								ID: ptr.To(networkID),
							},
							Subnets: []hyperv1.SubnetParam{
								{ID: ptr.To(subnetID)},
							},
						}}}},
			expectedOpenStackClusterSpec: capo.OpenStackClusterSpec{
				IdentityRef: capo.OpenStackIdentityReference{
					Name:      "openstack-credentials",
					CloudName: "openstack",
				},
				ExternalNetwork: &capo.NetworkParam{
					ID: ptr.To(externalNetworkID),
				},
				Subnets: []capo.SubnetParam{{ID: ptr.To(subnetID)}},
				Network: &capo.NetworkParam{ID: ptr.To(networkID)},
				ControlPlaneEndpoint: &capiv1.APIEndpoint{
					Host: "api-endpoint",
					Port: 6443,
				},
				DisableAPIServerFloatingIP: ptr.To(true),
				ManagedSecurityGroups: &capo.ManagedSecurityGroups{
					AllNodesSecurityGroupRules: defaultWorkerSecurityGroupRules([]string{"192.168.1.0/24"}),
				},
				Tags: []string{"openshiftClusterID=cluster-123"},
			},
			wantErr: false,
		},
		{
			name: "User provided network and subnet by tag on hosted cluster",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "cluster-123",
					Networking: hyperv1.ClusterNetworking{
						MachineNetwork: []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
					},
					Platform: hyperv1.PlatformSpec{
						OpenStack: &hyperv1.OpenStackPlatformSpec{
							IdentityRef: hyperv1.OpenStackIdentityReference{
								Name:      "openstack-credentials",
								CloudName: "openstack",
							},
							Network: &hyperv1.NetworkParam{
								Filter: &hyperv1.NetworkFilter{
									FilterByNeutronTags: hyperv1.FilterByNeutronTags{
										Tags: []hyperv1.NeutronTag{"test"},
									}},
							},
							Subnets: []hyperv1.SubnetParam{
								{Filter: &hyperv1.SubnetFilter{
									FilterByNeutronTags: hyperv1.FilterByNeutronTags{
										Tags: []hyperv1.NeutronTag{"test"},
									},
								}},
							},
							Tags: []string{"hcp-id=123"},
						}}}},
			expectedOpenStackClusterSpec: capo.OpenStackClusterSpec{
				IdentityRef: capo.OpenStackIdentityReference{
					Name:      "openstack-credentials",
					CloudName: "openstack",
				},
				Subnets: []capo.SubnetParam{{
					Filter: &capo.SubnetFilter{
						FilterByNeutronTags: capo.FilterByNeutronTags{
							Tags: []capo.NeutronTag{"test"},
						},
					},
				}},
				Network: &capo.NetworkParam{
					Filter: &capo.NetworkFilter{
						FilterByNeutronTags: capo.FilterByNeutronTags{
							Tags: []capo.NeutronTag{"test"},
						}},
				},
				ControlPlaneEndpoint: &capiv1.APIEndpoint{
					Host: "api-endpoint",
					Port: 6443,
				},
				DisableAPIServerFloatingIP: ptr.To(true),
				ManagedSecurityGroups: &capo.ManagedSecurityGroups{
					AllNodesSecurityGroupRules: defaultWorkerSecurityGroupRules([]string{"192.168.1.0/24"}),
				},
				Tags: []string{"openshiftClusterID=cluster-123", "hcp-id=123"},
			},
			wantErr: false,
		},
		{
			name: "Missing machine networks",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						OpenStack: &hyperv1.OpenStackPlatformSpec{
							IdentityRef: hyperv1.OpenStackIdentityReference{
								Name:      "openstack-credentials",
								CloudName: "openstack",
							},
							Network: &hyperv1.NetworkParam{
								Filter: &hyperv1.NetworkFilter{
									FilterByNeutronTags: hyperv1.FilterByNeutronTags{
										Tags: []hyperv1.NeutronTag{"test"},
									}},
							},
							Subnets: []hyperv1.SubnetParam{
								{Filter: &hyperv1.SubnetFilter{
									FilterByNeutronTags: hyperv1.FilterByNeutronTags{
										Tags: []hyperv1.NeutronTag{"test"},
									},
								}},
							},
						}}}},
			expectedOpenStackClusterSpec: capo.OpenStackClusterSpec{
				IdentityRef: capo.OpenStackIdentityReference{
					Name:      "openstack-credentials",
					CloudName: "openstack",
				},
			},
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			initialOpenStackClusterSpec := capo.OpenStackClusterSpec{
				IdentityRef: capo.OpenStackIdentityReference{
					Name:      "openstack-credentials",
					CloudName: "openstack",
				},
			}
			err := reconcileOpenStackClusterSpec(tc.hostedCluster, &initialOpenStackClusterSpec, apiEndpoint)
			if (err != nil) != tc.wantErr {
				t.Fatalf("reconcileOpenStackClusterSpec() error = %v, wantErr %v", err, tc.wantErr)
			}
			if diff := cmp.Diff(initialOpenStackClusterSpec, tc.expectedOpenStackClusterSpec); diff != "" {
				t.Errorf("reconciled OpenStack cluster spec differs from expected OpenStack cluster spec: %s", diff)
			}
		})
	}
}
