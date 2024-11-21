//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/support/assets"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/integration"
	integrationframework "github.com/openshift/hypershift/test/integration/framework"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestOnCreateAPIUX(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	t.Run("HostedCluster creation", func(t *testing.T) {
		g := NewWithT(t)
		client, err := e2eutil.GetClient()
		g.Expect(err).NotTo(HaveOccurred(), "couldn't get client")

		testCases := []struct {
			name        string
			file        string
			validations []struct {
				name                   string
				mutateInput            func(*hyperv1.HostedCluster)
				expectedErrorSubstring string
			}
		}{
			{
				name: "when based domain is not valid it should fail",
				file: "hostedcluster-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.HostedCluster)
					expectedErrorSubstring string
				}{
					{
						name: "when baseDomain has invalid chars it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.DNS.BaseDomain = "@foo"
						},
						expectedErrorSubstring: "baseDomain must be a valid domain (e.g., example, example.com, sub.example.com)",
					},
					{
						name: "when baseDomain is a valid hierarchical domain with two levels it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.DNS.BaseDomain = "foo.bar"
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when baseDomain is a valid hierarchical domain it with 3 levels should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.DNS.BaseDomain = "123.foo.bar"
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when baseDomain is a single subdomain it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.DNS.BaseDomain = "foo"
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when baseDomain is empty it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.DNS.BaseDomain = ""
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when baseDomainPrefix has invalid chars it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.DNS.BaseDomainPrefix = ptr.To("@foo")
						},
						expectedErrorSubstring: "baseDomainPrefix must be a valid domain (e.g., example, example.com, sub.example.com)",
					},
					{
						name: "when baseDomainPrefix is a valid hierarchical domain with two levels it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.DNS.BaseDomainPrefix = ptr.To("foo.bar")
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when baseDomainPrefix is a valid hierarchical domain it with 3 levels should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.DNS.BaseDomainPrefix = ptr.To("123.foo.bar")
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when baseDomainPrefix is a single subdomain it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.DNS.BaseDomainPrefix = ptr.To("foo")
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when baseDomainPrefix is empty it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.DNS.BaseDomainPrefix = ptr.To("")
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when publicZoneID and privateZoneID are empty it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.DNS.PublicZoneID = ""
							hc.Spec.DNS.PrivateZoneID = ""
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when publicZoneID and privateZoneID are set it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.DNS.PublicZoneID = "123"
							hc.Spec.DNS.PrivateZoneID = "123"
						},
						expectedErrorSubstring: "",
					},
				},
			},
			{
				name: "when feature gated fields are used it should fail",
				file: "hostedcluster-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.HostedCluster)
					expectedErrorSubstring string
				}{
					{
						name: "When OpenStack value is set as platform type it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Platform.Type = hyperv1.OpenStackPlatform
						},
						expectedErrorSubstring: "Unsupported value: \"OpenStack\"",
					},
				},
			},
			{
				name: "when infraID or clusterID are not valid input it should fail",
				file: "hostedcluster-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.HostedCluster)
					expectedErrorSubstring string
				}{
					{
						name: "when clusterID is not RFC4122 UUID it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.ClusterID = "foo"
						},
						expectedErrorSubstring: "clusterID must be an RFC4122 UUID value (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx in hexadecimal digits)",
					},
					{
						name: "when infraID is not RFC4122 UUID it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.InfraID = "@"
						},
						expectedErrorSubstring: "infraID must consist of lowercase alphanumeric characters or '-', start and end with an alphanumeric character, and be between 1 and 253 characters",
					},
					{
						name: "when infraID and clusterID it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.ClusterID = "123e4567-e89b-12d3-a456-426614174000"
							hc.Spec.InfraID = "infra-id"
						},
						expectedErrorSubstring: "",
					},
				},
			},
			{
				name: "when updateService is not a valid url it should fail",
				file: "hostedcluster-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.HostedCluster)
					expectedErrorSubstring string
				}{
					{
						name: "when updateService is not a complete URL it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.UpdateService = "foo"
						},
						expectedErrorSubstring: "updateService must be a valid absolute URL",
					},
					{
						name: "when updateService is a valid URL it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.UpdateService = "https://custom-updateservice.com"
						},
						expectedErrorSubstring: "",
					},
				},
			},
			{
				name: "when availabilityPolicy is not HighlyAvailable or SingleReplica it should fail",
				file: "hostedcluster-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.HostedCluster)
					expectedErrorSubstring string
				}{
					{
						name: "when controllerAvailabilityPolicy is not HighlyAvailable or SingleReplica it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.ControllerAvailabilityPolicy = "foo"
						},
						expectedErrorSubstring: "Unsupported value: \"foo\": supported values: \"HighlyAvailable\", \"SingleReplica\"",
					},
					{
						name: "when infrastructureAvailabilityPolicy is not HighlyAvailable or SingleReplica it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.InfrastructureAvailabilityPolicy = "foo"
						},
						expectedErrorSubstring: "Unsupported value: \"foo\": supported values: \"HighlyAvailable\", \"SingleReplica\"",
					},
				},
			},
			{
				name: "when networking is not configured properly it should fail",
				file: "hostedcluster-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.HostedCluster)
					expectedErrorSubstring string
				}{
					{
						name: "when networkType is not one of OpenShiftSDN;Calico;OVNKubernetes;Other it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Networking = hyperv1.ClusterNetworking{
								NetworkType: "foo",
							}
						},
						expectedErrorSubstring: "Unsupported value: \"foo\": supported values: \"OpenShiftSDN\", \"Calico\", \"OVNKubernetes\", \"Other\"",
					},
					{
						name: "when the cidr is not valid it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Networking = hyperv1.ClusterNetworking{
								// We can't not use a yaml file to pass the bad CIDR as a string atm
								// because the ipnet.IPNet wrapper unmarshall will fail to un marshal here before we get to apply the resource.
								// Instead, we pass an IP without a mask in the unmarshalled resource here which results in ipnet.IPNet marshal returning a string as "<nil>".
								// So this validation ultimately uses "<nil>" as the marshaled resource input to test the CRD validation.
								ClusterNetwork: []hyperv1.ClusterNetworkEntry{
									{
										CIDR: ipnet.IPNet{
											IP: net.IPv4(10, 128, 0, 0),
										},
										HostPrefix: 0,
									},
								},
							}
						},
						expectedErrorSubstring: "cidr must be a valid IPv4 or IPv6 CIDR notation (e.g., 192.168.1.0/24 or 2001:db8::/64)",
					},
					{
						name: "when a cidr in clusterNetwork and serviceNetwork is duplicated it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Networking = hyperv1.ClusterNetworking{
								ClusterNetwork: []hyperv1.ClusterNetworkEntry{
									{
										CIDR: ipnet.IPNet{
											IP:   net.IPv4(10, 128, 0, 0),
											Mask: net.CIDRMask(32, 32),
										},
										HostPrefix: 0,
									},
								},
								ServiceNetwork: []hyperv1.ServiceNetworkEntry{
									{
										CIDR: ipnet.IPNet{
											IP:   net.IPv4(10, 128, 0, 0),
											Mask: net.CIDRMask(32, 32),
										},
									},
								},
							}
						},
						expectedErrorSubstring: "CIDR ranges in machineNetwork, clusterNetwork, and serviceNetwork must be unique and non-overlapping",
					},
					{
						name: "when a cidr in machineNetwork and serviceNetwork is duplicated it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Networking = hyperv1.ClusterNetworking{
								MachineNetwork: []hyperv1.MachineNetworkEntry{
									{
										CIDR: ipnet.IPNet{
											IP:   net.IPv4(10, 128, 0, 0),
											Mask: net.CIDRMask(32, 32),
										},
									},
								},
								ServiceNetwork: []hyperv1.ServiceNetworkEntry{
									{
										CIDR: ipnet.IPNet{
											IP:   net.IPv4(10, 128, 0, 0),
											Mask: net.CIDRMask(32, 32),
										},
									},
								},
							}
						},
						expectedErrorSubstring: "CIDR ranges in machineNetwork, clusterNetwork, and serviceNetwork must be unique and non-overlapping",
					},
					{
						name: "when a cidr in machineNetwork and ClusterNetwork is duplicated it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Networking = hyperv1.ClusterNetworking{
								MachineNetwork: []hyperv1.MachineNetworkEntry{
									{
										CIDR: ipnet.IPNet{
											IP:   net.IPv4(10, 128, 0, 0),
											Mask: net.CIDRMask(32, 32),
										},
									},
								},
								ClusterNetwork: []hyperv1.ClusterNetworkEntry{
									{
										CIDR: ipnet.IPNet{
											IP:   net.IPv4(10, 128, 0, 0),
											Mask: net.CIDRMask(32, 32),
										},
									},
								},
							}
						},
						expectedErrorSubstring: "CIDR ranges in machineNetwork, clusterNetwork, and serviceNetwork must be unique and non-overlapping",
					},
				},
			},
			{
				name: "when etcd is not configured properly it should fail",
				file: "hostedcluster-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.HostedCluster)
					expectedErrorSubstring string
				}{
					{
						name: "when managementType is managed with unmanaged configuration it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Etcd = hyperv1.EtcdSpec{
								ManagementType: hyperv1.Managed,
								Unmanaged: &hyperv1.UnmanagedEtcdSpec{
									Endpoint: "https://etcd.example.com:2379",
								},
							}
						},
						expectedErrorSubstring: "Only managed configuration must be set when managementType is Managed",
					},
					{
						name: "when managementType is unmanaged with managed configuration it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Etcd = hyperv1.EtcdSpec{
								ManagementType: hyperv1.Unmanaged,
								Managed: &hyperv1.ManagedEtcdSpec{
									Storage: hyperv1.ManagedEtcdStorageSpec{
										Type: hyperv1.PersistentVolumeEtcdStorage,
									},
								},
							}
						},
						expectedErrorSubstring: "Only unmanaged configuration must be set when managementType is Unmanaged",
					},
				},
			},
			{
				name: "when services is not configured properly it should fail",
				file: "hostedcluster-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.HostedCluster)
					expectedErrorSubstring string
				}{
					// {
					// 	name: "when serviceType is 'APIServer' and publishing strategy is 'Route' and hostname is not set it should fail",
					// 	mutateInput: func(hc *hyperv1.HostedCluster) {
					// 		hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
					// 			{
					// 				Service: hyperv1.APIServer,
					// 				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					// 					Type:  hyperv1.Route,
					// 					Route: &hyperv1.RoutePublishingStrategy{},
					// 				},
					// 			},
					// 			{
					// 				Service: hyperv1.Ignition,
					// 				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					// 					Type:  hyperv1.Route,
					// 					Route: &hyperv1.RoutePublishingStrategy{},
					// 				},
					// 			},
					// 			{
					// 				Service: hyperv1.Konnectivity,
					// 				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					// 					Type:  hyperv1.Route,
					// 					Route: &hyperv1.RoutePublishingStrategy{},
					// 				},
					// 			},
					// 			{
					// 				Service: hyperv1.OAuthServer,
					// 				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					// 					Type:  hyperv1.Route,
					// 					Route: &hyperv1.RoutePublishingStrategy{},
					// 				},
					// 			},
					// 		}
					// 	},
					// 	expectedErrorSubstring: "If serviceType is 'APIServer' and publishing strategy is 'Route', then hostname must be set",
					// },
					{
						name: "when less than 4 services are set it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
								{
									Service: hyperv1.Ignition,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type:  hyperv1.Route,
										Route: &hyperv1.RoutePublishingStrategy{},
									},
								},
								{
									Service: hyperv1.Konnectivity,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type:  hyperv1.Route,
										Route: &hyperv1.RoutePublishingStrategy{},
									},
								},
								{
									Service: hyperv1.OAuthServer,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type:  hyperv1.Route,
										Route: &hyperv1.RoutePublishingStrategy{},
									},
								},
							}
						},
						expectedErrorSubstring: "3: spec.services in body should have at least 4 items",
					},
					// {
					// 	name: "when any of the required services is missing it should fail",
					// 	mutateInput: func(hc *hyperv1.HostedCluster) {
					// 		hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
					// 			{
					// 				Service: hyperv1.Ignition,
					// 				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					// 					Type:  hyperv1.Route,
					// 					Route: &hyperv1.RoutePublishingStrategy{},
					// 				},
					// 			},
					// 			{
					// 				Service: hyperv1.Konnectivity,
					// 				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					// 					Type:  hyperv1.Route,
					// 					Route: &hyperv1.RoutePublishingStrategy{},
					// 				},
					// 			},
					// 			{
					// 				Service: hyperv1.OAuthServer,
					// 				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					// 					Type:  hyperv1.Route,
					// 					Route: &hyperv1.RoutePublishingStrategy{},
					// 				},
					// 			},
					// 			{
					// 				Service: hyperv1.OVNSbDb,
					// 				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					// 					Type:  hyperv1.Route,
					// 					Route: &hyperv1.RoutePublishingStrategy{},
					// 				},
					// 			},
					// 		}
					// 	},
					// 	expectedErrorSubstring: "Services list must contain at least 'APIServer', 'OAuthServer', 'Konnectivity', and 'Ignition' service types",
					// },
					// {
					// 	name: "when there is a duplicated hostname in routes it should fail",
					// 	mutateInput: func(hc *hyperv1.HostedCluster) {
					// 		hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
					// 			{
					// 				Service: hyperv1.APIServer,
					// 				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					// 					Type: hyperv1.Route,
					// 					Route: &hyperv1.RoutePublishingStrategy{
					// 						Hostname: "api.example.com",
					// 					},
					// 				},
					// 			},
					// 			{
					// 				Service: hyperv1.Ignition,
					// 				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					// 					Type: hyperv1.Route,
					// 					Route: &hyperv1.RoutePublishingStrategy{
					// 						Hostname: "api.example.com",
					// 					},
					// 				},
					// 			},
					// 			{
					// 				Service: hyperv1.Konnectivity,
					// 				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					// 					Type:  hyperv1.Route,
					// 					Route: &hyperv1.RoutePublishingStrategy{},
					// 				},
					// 			},
					// 			{
					// 				Service: hyperv1.OAuthServer,
					// 				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					// 					Type:  hyperv1.Route,
					// 					Route: &hyperv1.RoutePublishingStrategy{},
					// 				},
					// 			},
					// 		}
					// 	},
					// 	expectedErrorSubstring: "Each route publishingStrategy 'hostname' must be unique within the Services list",
					// },
					// {
					// 	name: "when there is a duplicated nodePort entries it should fail",
					// 	mutateInput: func(hc *hyperv1.HostedCluster) {
					// 		hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
					// 			{
					// 				Service: hyperv1.APIServer,
					// 				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					// 					Type: hyperv1.NodePort,
					// 					NodePort: &hyperv1.NodePortPublishingStrategy{
					// 						Address: "api.example.com",
					// 						Port:    3030,
					// 					},
					// 				},
					// 			},
					// 			{
					// 				Service: hyperv1.Ignition,
					// 				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					// 					Type: hyperv1.NodePort,
					// 					NodePort: &hyperv1.NodePortPublishingStrategy{
					// 						Address: "api.example.com",
					// 						Port:    3030,
					// 					},
					// 				},
					// 			},
					// 			{
					// 				Service: hyperv1.Konnectivity,
					// 				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					// 					Type:  hyperv1.Route,
					// 					Route: &hyperv1.RoutePublishingStrategy{},
					// 				},
					// 			},
					// 			{
					// 				Service: hyperv1.OAuthServer,
					// 				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					// 					Type:  hyperv1.Route,
					// 					Route: &hyperv1.RoutePublishingStrategy{},
					// 				},
					// 			},
					// 		}
					// 	},
					// 	expectedErrorSubstring: "Each nodePort publishingStrategy 'nodePort' and 'hostname' must be unique within the Services list",
					// },
					{
						name: "when a type Route set with the nodePort configuration it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
								{
									Service: hyperv1.APIServer,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type: hyperv1.Route,
										Route: &hyperv1.RoutePublishingStrategy{
											Hostname: "api.example.com",
										},
									},
								},
								{
									Service: hyperv1.Ignition,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type: hyperv1.Route,
										NodePort: &hyperv1.NodePortPublishingStrategy{
											Address: "ignition.example.com",
											Port:    3030,
										},
									},
								},
								{
									Service: hyperv1.Konnectivity,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type:  hyperv1.Route,
										Route: &hyperv1.RoutePublishingStrategy{},
									},
								},
								{
									Service: hyperv1.OAuthServer,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type:  hyperv1.Route,
										Route: &hyperv1.RoutePublishingStrategy{},
									},
								},
							}
						},
						expectedErrorSubstring: "nodePort is required when type is NodePort, and forbidden otherwise",
					},
					{
						name: "when a type NodePort set with the route configuration it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
								{
									Service: hyperv1.APIServer,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type: hyperv1.Route,
										Route: &hyperv1.RoutePublishingStrategy{
											Hostname: "api.example.com",
										},
									},
								},
								{
									Service: hyperv1.Ignition,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type: hyperv1.NodePort,
										Route: &hyperv1.RoutePublishingStrategy{
											Hostname: "ignition.example.com",
										},
									},
								},
								{
									Service: hyperv1.Konnectivity,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type:  hyperv1.Route,
										Route: &hyperv1.RoutePublishingStrategy{},
									},
								},
								{
									Service: hyperv1.OAuthServer,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type:  hyperv1.Route,
										Route: &hyperv1.RoutePublishingStrategy{},
									},
								},
							}
						},
						expectedErrorSubstring: "only route is allowed when type is Route, and forbidden otherwise",
					},
					{
						name: "when platform is Azure and not all services are route with hostname it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Platform.Type = hyperv1.AzurePlatform
							hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
								{
									Service: hyperv1.APIServer,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type: hyperv1.Route,
										Route: &hyperv1.RoutePublishingStrategy{
											Hostname: "api.example.com",
										},
									},
								},
								{
									Service: hyperv1.Ignition,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type: hyperv1.NodePort,
										Route: &hyperv1.RoutePublishingStrategy{
											Hostname: "ignition.example.com",
										},
									},
								},
								{
									Service: hyperv1.Konnectivity,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type: hyperv1.Route,
										Route: &hyperv1.RoutePublishingStrategy{
											Hostname: "konnectivity.example.com",
										},
									},
								},
								{
									Service: hyperv1.OAuthServer,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type:  hyperv1.Route,
										Route: &hyperv1.RoutePublishingStrategy{},
									},
								},
							}
						},
						expectedErrorSubstring: "Azure platform requires Ignition Route service with a hostname to be defined",
					},
				},
			},
			{
				name: "when serviceAccountSigningKey and issuerURL are not configured properly it should fail",
				file: "hostedcluster-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.HostedCluster)
					expectedErrorSubstring string
				}{
					{
						name: "when issuerURL is not a valid URL it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.IssuerURL = "foo"
						},
						expectedErrorSubstring: "issuerURL must be a valid absolute URL",
					},
				},
			},
		}

		for _, tc := range testCases {
			for _, v := range tc.validations {
				t.Logf("Running validation %q", v.name)
				hostedCluster := assets.ShouldHostedCluster(content.ReadFile, fmt.Sprintf("assets/%s", tc.file))
				defer client.Delete(ctx, hostedCluster)
				v.mutateInput(hostedCluster)

				err = client.Create(ctx, hostedCluster)
				if v.expectedErrorSubstring != "" {
					g.Expect(err).To(HaveOccurred())
					g.Expect(err.Error()).To(ContainSubstring(v.expectedErrorSubstring))
				} else {
					g.Expect(err).ToNot(HaveOccurred())
				}
				client.Delete(ctx, hostedCluster)
			}
		}

	})

	t.Run("NodePool creation", func(t *testing.T) {
		g := NewWithT(t)
		client, err := e2eutil.GetClient()
		g.Expect(err).NotTo(HaveOccurred(), "couldn't get client")

		testCases := []struct {
			name        string
			file        string
			validations []struct {
				name                   string
				mutateInput            func(*hyperv1.NodePool)
				expectedErrorSubstring string
			}
		}{
			{
				name: "When Taint key/value is not a qualified name with an optional subdomain prefix to upstream validation, it should fail",
				file: "nodepool-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.NodePool)
					expectedErrorSubstring string
				}{
					{
						name: "when key prefix is not a valid sudomain it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Taints = []hyperv1.Taint{{Key: "prefix@/suffix", Value: "value", Effect: "NoSchedule"}}
						},
						expectedErrorSubstring: "key must be a qualified name with an optional subdomain prefix e.g. example.com/MyName",
					},
					{
						name: "when key suffix is not a valid qualified name it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Taints = []hyperv1.Taint{{Key: "prefix/suffix@", Value: "value", Effect: "NoSchedule"}}
						},
						expectedErrorSubstring: "key must be a qualified name with an optional subdomain prefix e.g. example.com/MyName",
					},
					{
						name: "when key is empty it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Taints = []hyperv1.Taint{{Key: "", Value: "value", Effect: "NoSchedule"}}
						},
						expectedErrorSubstring: "spec.taints[0].key in body should be at least 1 chars long",
					},
					{
						name: "when key is a valid qualified name with no prefix it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Taints = []hyperv1.Taint{{Key: "valid-suffix", Value: "", Effect: "NoSchedule"}}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when key is a valid qualified name with a subdomain prefix it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Taints = []hyperv1.Taint{{Key: "valid-prefix.com/valid-suffix", Value: "", Effect: "NoSchedule"}}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when key is a valid qualified name with a subdomain prefix and value is a valid qualified name it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Taints = []hyperv1.Taint{{Key: "valid-prefix.com/valid-suffix", Value: "value", Effect: "NoSchedule"}}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when value contains strange chars it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Taints = []hyperv1.Taint{{Key: "valid-prefix.com/valid-suffix", Value: "@", Effect: "NoSchedule"}}
						},
						expectedErrorSubstring: "Value must start and end with alphanumeric characters and can only contain '-', '_', '.' in the middle",
					},
				},
			},
			{
				name: "when pausedUntil is not a date with RFC3339 format or a boolean as in 'true', 'false', 'True', 'False' it should fail",
				file: "nodepool-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.NodePool)
					expectedErrorSubstring string
				}{
					{
						name: "when pausedUntil is a random string it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.PausedUntil = ptr.To("fail")
						},
						expectedErrorSubstring: "PausedUntil must be a date in RFC3339 format or 'True', 'true', 'False' or 'false'",
					},
					{
						name: "when pausedUntil date is not RFC3339 format it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.PausedUntil = ptr.To("2022-01-01")
						},
						expectedErrorSubstring: "PausedUntil must be a date in RFC3339 format or 'True', 'true', 'False' or 'false'",
					},
					{
						name: "when pausedUntil is an allowed bool False it shoud pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.PausedUntil = ptr.To("False")
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when pausedUntil is an allowed bool false it shoud pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.PausedUntil = ptr.To("false")
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when pausedUntil is an allowed bool true it shoud pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.PausedUntil = ptr.To("true")
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when pausedUntil is an allowed bool True it shoud pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.PausedUntil = ptr.To("True")
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when pausedUntil date is RFC3339 it shoud pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.PausedUntil = ptr.To("2022-01-01T00:00:00Z")
						},
						expectedErrorSubstring: "",
					},
				},
			},
			{
				name: "when release does not have a valid image value it should fail",
				file: "nodepool-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.NodePool)
					expectedErrorSubstring string
				}{
					{
						name: "when image is bad format it shoud fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Release.Image = "@"
						},
						expectedErrorSubstring: "Image must start with a word character (letters, digits, or underscores) and contain no white spaces",
					},
					{
						name: "when image is empty it shoud fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Release.Image = "@"
						},
						expectedErrorSubstring: "Image must start with a word character (letters, digits, or underscores) and contain no white spaces",
					},
					{
						name: "when image is valid it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.17.0-rc.0-x86_64"
						},
						expectedErrorSubstring: "",
					},
				},
			},
			{
				name: "when management has invalid input it should fail",
				file: "nodepool-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.NodePool)
					expectedErrorSubstring string
				}{
					{
						name: "when replace upgrade type is set with inPlace configuration it shoud fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Management = hyperv1.NodePoolManagement{
								UpgradeType: hyperv1.UpgradeTypeReplace,
								InPlace: &hyperv1.InPlaceUpgrade{
									MaxUnavailable: ptr.To(intstr.FromInt32(1)),
								},
							}
						},
						expectedErrorSubstring: "The 'inPlace' field can only be set when 'upgradeType' is 'InPlace'",
					},
					{
						name: "when  strategy is onDelete with RollingUpdate configuration it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Management = hyperv1.NodePoolManagement{
								UpgradeType: hyperv1.UpgradeTypeReplace,
								Replace: &hyperv1.ReplaceUpgrade{
									Strategy: hyperv1.UpgradeStrategyOnDelete,
									RollingUpdate: &hyperv1.RollingUpdate{
										MaxUnavailable: ptr.To(intstr.FromInt32(1)),
									},
								},
							}
						},
						expectedErrorSubstring: "The 'rollingUpdate' field can only be set when 'strategy' is 'RollingUpdate'",
					},
				},
			},
		}

		for _, tc := range testCases {
			for _, v := range tc.validations {
				t.Logf("Running validation %q", v.name)
				nodePool := assets.ShouldNodePool(content.ReadFile, fmt.Sprintf("assets/%s", tc.file))
				defer client.Delete(ctx, nodePool)
				v.mutateInput(nodePool)

				err = client.Create(ctx, nodePool)
				if v.expectedErrorSubstring != "" {
					g.Expect(err).To(HaveOccurred())
					g.Expect(err.Error()).To(ContainSubstring(v.expectedErrorSubstring))
				} else {
					g.Expect(err).ToNot(HaveOccurred())
				}
				client.Delete(ctx, nodePool)
			}
		}
	})
}

// TestCreateCluster implements a test that creates a cluster with the code under test
// vs upgrading to the code under test as TestUpgradeControlPlane does.
func TestCreateCluster(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	zones := strings.Split(globalOpts.configurableClusterOptions.Zone.String(), ",")
	if len(zones) >= 3 {
		// CreateCluster also tests multi-zone workers work properly if a sufficient number of zones are configured
		t.Logf("Sufficient zones available for InfrastructureAvailabilityPolicy HighlyAvailable")
		clusterOpts.AWSPlatform.Zones = zones
		clusterOpts.InfrastructureAvailabilityPolicy = string(hyperv1.HighlyAvailable)
		clusterOpts.NodePoolReplicas = 1
	}
	if !e2eutil.IsLessThan(e2eutil.Version418) {
		clusterOpts.FeatureSet = string(configv1.TechPreviewNoUpgrade)
	}

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Sanity check the cluster by waiting for the nodes to report ready
		_ = e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

		t.Logf("fetching mgmt kubeconfig")
		mgmtCfg, err := e2eutil.GetConfig()
		g.Expect(err).NotTo(HaveOccurred(), "couldn't get mgmt kubeconfig")
		mgmtCfg.QPS = -1
		mgmtCfg.Burst = -1

		mgmtClients, err := integrationframework.NewClients(mgmtCfg)
		g.Expect(err).NotTo(HaveOccurred(), "couldn't create mgmt clients")

		guestKubeConfigSecretData := e2eutil.WaitForGuestKubeConfig(t, ctx, mgtClient, hostedCluster)

		guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
		g.Expect(err).NotTo(HaveOccurred(), "couldn't load guest kubeconfig")
		guestConfig.QPS = -1
		guestConfig.Burst = -1

		guestClients, err := integrationframework.NewClients(guestConfig)
		g.Expect(err).NotTo(HaveOccurred(), "couldn't create guest clients")

		integration.RunTestControlPlanePKIOperatorBreakGlassCredentials(t, testContext, hostedCluster, mgmtClients, guestClients)
		e2eutil.EnsureAPIUX(t, ctx, mgtClient, hostedCluster)
	}).
		Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

// TestCreateClusterV2 tests the new CPO implementation, which is currently hidden behind an annotation.
func TestCreateClusterV2(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	zones := strings.Split(globalOpts.configurableClusterOptions.Zone.String(), ",")
	if len(zones) >= 3 {
		// CreateCluster also tests multi-zone workers work properly if a sufficient number of zones are configured
		t.Logf("Sufficient zones available for InfrastructureAvailabilityPolicy HighlyAvailable")
		clusterOpts.AWSPlatform.Zones = zones
		clusterOpts.InfrastructureAvailabilityPolicy = string(hyperv1.HighlyAvailable)
		clusterOpts.NodePoolReplicas = 1
	}
	clusterOpts.BeforeApply = func(o crclient.Object) {
		switch obj := o.(type) {
		case *hyperv1.HostedCluster:
			if obj.Annotations == nil {
				obj.Annotations = make(map[string]string)
			}
			obj.Annotations[hyperv1.ControlPlaneOperatorV2Annotation] = "true"
		}
	}

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Sanity check the cluster by waiting for the nodes to report ready
		_ = e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

		t.Logf("fetching mgmt kubeconfig")
		mgmtCfg, err := e2eutil.GetConfig()
		g.Expect(err).NotTo(HaveOccurred(), "couldn't get mgmt kubeconfig")
		mgmtCfg.QPS = -1
		mgmtCfg.Burst = -1

		mgmtClients, err := integrationframework.NewClients(mgmtCfg)
		g.Expect(err).NotTo(HaveOccurred(), "couldn't create mgmt clients")

		guestKubeConfigSecretData := e2eutil.WaitForGuestKubeConfig(t, ctx, mgtClient, hostedCluster)

		guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
		g.Expect(err).NotTo(HaveOccurred(), "couldn't load guest kubeconfig")
		guestConfig.QPS = -1
		guestConfig.Burst = -1

		guestClients, err := integrationframework.NewClients(guestConfig)
		g.Expect(err).NotTo(HaveOccurred(), "couldn't create guest clients")

		integration.RunTestControlPlanePKIOperatorBreakGlassCredentials(t, testContext, hostedCluster, mgmtClients, guestClients)
		e2eutil.EnsureAPIUX(t, ctx, mgtClient, hostedCluster)
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

func TestCreateClusterRequestServingIsolation(t *testing.T) {
	if !globalOpts.RequestServingIsolation {
		t.Skip("Skipping request serving isolation test")
	}
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("Request serving isolation test requires the AWS platform")
	}
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	nodePools := e2eutil.SetupReqServingClusterNodePools(ctx, t, globalOpts.ManagementParentKubeconfig, globalOpts.ManagementClusterNamespace, globalOpts.ManagementClusterName)
	defer e2eutil.TearDownNodePools(ctx, t, globalOpts.ManagementParentKubeconfig, nodePools)

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	zones := strings.Split(globalOpts.configurableClusterOptions.Zone.String(), ",")
	if len(zones) >= 3 {
		// CreateCluster also tests multi-zone workers work properly if a sufficient number of zones are configured
		t.Logf("Sufficient zones available for InfrastructureAvailabilityPolicy HighlyAvailable")
		clusterOpts.AWSPlatform.Zones = zones
		clusterOpts.InfrastructureAvailabilityPolicy = string(hyperv1.HighlyAvailable)
		clusterOpts.NodePoolReplicas = 1
		clusterOpts.NodeSelector = map[string]string{"hypershift.openshift.io/control-plane": "true"}
	}

	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.HighlyAvailable)
	clusterOpts.Annotations = append(clusterOpts.Annotations, fmt.Sprintf("%s=%s", hyperv1.TopologyAnnotation, hyperv1.DedicatedRequestServingComponentsTopology))

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		guestClient := e2eutil.WaitForGuestClient(t, testContext, mgtClient, hostedCluster)
		e2eutil.EnsurePSANotPrivileged(t, ctx, guestClient)
		e2eutil.EnsureAllReqServingPodsLandOnReqServingNodes(t, ctx, guestClient)
		e2eutil.EnsureOnlyRequestServingPodsOnRequestServingNodes(t, ctx, guestClient)
		e2eutil.EnsureNoHCPPodsLandOnDefaultNode(t, ctx, guestClient, hostedCluster)
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

func TestCreateClusterCustomConfig(t *testing.T) {
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	// find kms key ARN using alias
	kmsKeyArn, err := e2eutil.GetKMSKeyArn(clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile, clusterOpts.AWSPlatform.Region, globalOpts.configurableClusterOptions.AWSKmsKeyAlias)
	if err != nil || kmsKeyArn == nil {
		t.Fatal("failed to retrieve kms key arn")
	}

	clusterOpts.AWSPlatform.EtcdKMSKeyARN = *kmsKeyArn

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {

		g.Expect(hostedCluster.Spec.SecretEncryption.KMS.AWS.ActiveKey.ARN).To(Equal(*kmsKeyArn))
		g.Expect(hostedCluster.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN).ToNot(BeEmpty())

		guestClient := e2eutil.WaitForGuestClient(t, testContext, mgtClient, hostedCluster)
		e2eutil.EnsureSecretEncryptedUsingKMSV2(t, ctx, hostedCluster, guestClient)
		// test oauth with identity provider
		e2eutil.EnsureOAuthWithIdentityProvider(t, ctx, mgtClient, hostedCluster)
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

func TestNoneCreateCluster(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Wait for the rollout to be reported complete
		// Since the None platform has no workers, CVO will not have expectations set,
		// which in turn means that the ClusterVersion object will never be populated.
		// Therefore only test if the control plane comes up (etc, apiserver, ...)
		e2eutil.WaitForConditionsOnHostedControlPlane(t, ctx, mgtClient, hostedCluster, globalOpts.LatestReleaseImage)

		// etcd restarts for me once always and apiserver two times before running stable
		// e2eutil.EnsureNoCrashingPods(t, ctx, client, hostedCluster)
	}).Execute(&clusterOpts, hyperv1.NonePlatform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

// TestCreateClusterProxy implements a test that creates a cluster behind a proxy with the code under test.
func TestCreateClusterProxy(t *testing.T) {
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}
	t.Parallel()
	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.AWSPlatform.EnableProxy = true
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)

	e2eutil.NewHypershiftTest(t, ctx, nil).
		Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

func TestCreateClusterPrivate(t *testing.T) {
	testCreateClusterPrivate(t, false)
}

func TestCreateClusterPrivateWithRouteKAS(t *testing.T) {
	testCreateClusterPrivate(t, true)
}

// testCreateClusterPrivate implements a smoke test that creates a private cluster.
// Validations requiring guest cluster client are dropped here since the kas is not accessible when private.
// In the future we might want to leverage https://issues.redhat.com/browse/HOSTEDCP-697 to access guest cluster.
func testCreateClusterPrivate(t *testing.T, enableExternalDNS bool) {
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)
	clusterOpts.AWSPlatform.EndpointAccess = string(hyperv1.Private)
	expectGuestKubeconfHostChange := false
	if !enableExternalDNS {
		clusterOpts.ExternalDNSDomain = ""
		expectGuestKubeconfHostChange = true
	}

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Private -> publicAndPrivate
		t.Run("SwitchFromPrivateToPublic", testSwitchFromPrivateToPublic(ctx, mgtClient, hostedCluster, &clusterOpts, expectGuestKubeconfHostChange))
		// publicAndPrivate -> Private
		t.Run("SwitchFromPublicToPrivate", testSwitchFromPublicToPrivate(ctx, mgtClient, hostedCluster, &clusterOpts))
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

func testSwitchFromPrivateToPublic(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, clusterOpts *e2eutil.PlatformAgnosticOptions, expectGuestKubeconfHostChange bool) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		if globalOpts.Platform != hyperv1.AWSPlatform {
			t.Skip("test only supported on platform AWS")
		}

		var (
			host string
			err  error
		)
		if expectGuestKubeconfHostChange {
			// Get guest kubeconfig host before switching endpoint access
			host, err = e2eutil.GetGuestKubeconfigHost(t, ctx, client, hostedCluster)
			g.Expect(err).ToNot(HaveOccurred(), "failed to get guest kubeconfig host")
			t.Logf("Found guest kubeconfig host before switching endpoint access: %s", host)
		}

		// Switch to PublicAndPrivate endpoint access
		err = e2eutil.UpdateObject(t, ctx, client, hostedCluster, func(obj *hyperv1.HostedCluster) {
			obj.Spec.Platform.AWS.EndpointAccess = hyperv1.PublicAndPrivate
		})
		g.Expect(err).ToNot(HaveOccurred(), "failed to update hostedcluster EndpointAccess")

		if expectGuestKubeconfHostChange {
			e2eutil.WaitForGuestKubeconfigHostUpdate(t, ctx, client, hostedCluster, host)
		}

		e2eutil.ValidatePublicCluster(t, ctx, client, hostedCluster, clusterOpts)
	}
}

func testSwitchFromPublicToPrivate(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, clusterOpts *e2eutil.PlatformAgnosticOptions) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		if globalOpts.Platform != hyperv1.AWSPlatform {
			t.Skip("test only supported on platform AWS")
		}

		err := e2eutil.UpdateObject(t, ctx, client, hostedCluster, func(obj *hyperv1.HostedCluster) {
			obj.Spec.Platform.AWS.EndpointAccess = hyperv1.Private
		})
		g.Expect(err).ToNot(HaveOccurred(), "failed to update hostedcluster EndpointAccess")

		e2eutil.ValidatePrivateCluster(t, ctx, client, hostedCluster, clusterOpts)
	}
}
