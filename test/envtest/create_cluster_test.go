package envtest

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	installassets "github.com/openshift/hypershift/cmd/install/assets"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/assets"

	configv1 "github.com/openshift/api/config/v1"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// assetReader returns an AssetReader that reads from the test/e2e/assets directory.
// Fetching the e2e assets ensures that the tests do not drift from each other.
func assetReader() assets.AssetReader {
	_, thisFile, _, _ := runtime.Caller(0)
	assetsDir := filepath.Join(filepath.Dir(thisFile), "..", "e2e")
	return func(name string) ([]byte, error) {
		return os.ReadFile(filepath.Join(assetsDir, name))
	}
}

// validGCPPlatformSpec returns a valid GCPPlatformSpec baseline for testing.
func validGCPPlatformSpec() *hyperv1.GCPPlatformSpec {
	return &hyperv1.GCPPlatformSpec{
		Project: "my-project-123",
		Region:  "us-central1",
		NetworkConfig: hyperv1.GCPNetworkConfig{
			Network:                     hyperv1.GCPResourceReference{Name: "my-network"},
			PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{Name: "my-psc-subnet"},
		},
		WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
			ProjectNumber: "123456789012",
			PoolID:        "my-wif-pool",
			ProviderID:    "my-wif-provider",
			ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
				NodePool:        "nodepool@my-project-123.iam.gserviceaccount.com",
				ControlPlane:    "controlplane@my-project-123.iam.gserviceaccount.com",
				CloudController: "cloudcontroller@my-project-123.iam.gserviceaccount.com",
				Storage:         "storage@my-project-123.iam.gserviceaccount.com",
				ImageRegistry:   "imageregistry@my-project-123.iam.gserviceaccount.com",
			},
		},
	}
}

// hypershiftCRDs returns the HyperShift CRDs for the given feature set,
// using the same annotation-based filtering as `hypershift install`.
func hypershiftCRDs(featureSet string) []*apiextensionsv1.CustomResourceDefinition {
	crdObjects := installassets.CustomResourceDefinitions(
		func(path string, crd *apiextensionsv1.CustomResourceDefinition) bool {
			if !strings.Contains(path, "hypershift-operator/") {
				return false
			}
			if annotationFS, ok := crd.Annotations["release.openshift.io/feature-set"]; ok {
				return annotationFS == featureSet
			}
			// CRDs without a feature-set annotation are always included.
			return true
		},
		nil,
	)

	crds := make([]*apiextensionsv1.CustomResourceDefinition, len(crdObjects))
	for i, obj := range crdObjects {
		crds[i] = obj.(*apiextensionsv1.CustomResourceDefinition)
	}
	return crds
}

func TestOnCreateAPIUX(t *testing.T) {
	featureSet := os.Getenv("FEATURE_SET")
	if featureSet == "" {
		featureSet = "TechPreviewNoUpgrade"
	}
	g := NewWithT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start envtest with HyperShift CRDs for the given feature set.
	testEnv := &envtest.Environment{
		CRDInstallOptions: envtest.CRDInstallOptions{
			CRDs: hypershiftCRDs(featureSet),
		},
	}
	cfg, err := testEnv.Start()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(cfg).NotTo(BeNil())

	defer func() {
		cancel()
		err := testEnv.Stop()
		g.Expect(err).NotTo(HaveOccurred())
	}()

	k8sClient, err := crclient.New(cfg, crclient.Options{Scheme: hyperapi.Scheme})
	g.Expect(err).NotTo(HaveOccurred())

	reader := assetReader()

	t.Run("HostedCluster creation", func(t *testing.T) {
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
						name: "when capabilities.disabled is set to a supported capability it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Capabilities = &hyperv1.Capabilities{
								Disabled: []hyperv1.OptionalCapability{
									hyperv1.ImageRegistryCapability,
								},
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when capabilities.disabled is set to openshift-samples it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Capabilities = &hyperv1.Capabilities{
								Disabled: []hyperv1.OptionalCapability{
									hyperv1.OpenShiftSamplesCapability,
								},
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when capabilities.disabled is set to an invalid capability it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Capabilities = &hyperv1.Capabilities{
								Disabled: []hyperv1.OptionalCapability{
									hyperv1.OptionalCapability("AnInvalidCapability"),
								},
							}
						},
						expectedErrorSubstring: "Unsupported value: \"AnInvalidCapability\": supported values: \"ImageRegistry\", " +
							"\"openshift-samples\", \"Insights\", \"baremetal\"",
					},
					{
						name: "when capabilities.disabled is set to an unsupported capability it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Capabilities = &hyperv1.Capabilities{
								Disabled: []hyperv1.OptionalCapability{
									hyperv1.OptionalCapability("Storage"),
								},
							}
						},
						expectedErrorSubstring: "Unsupported value: \"Storage\": supported values: \"ImageRegistry\", " +
							"\"openshift-samples\", \"Insights\", \"baremetal\"",
					},
					{
						name: "when capabilities.enabled is set to a supported capability it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Capabilities = &hyperv1.Capabilities{
								Enabled: []hyperv1.OptionalCapability{
									hyperv1.BaremetalCapability,
								},
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when capabilities.enabled is set to an invalid capability it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Capabilities = &hyperv1.Capabilities{
								Enabled: []hyperv1.OptionalCapability{
									hyperv1.OptionalCapability("AnInvalidCapability"),
								},
							}
						},
						expectedErrorSubstring: "Unsupported value: \"AnInvalidCapability\": supported values: \"ImageRegistry\", " +
							"\"openshift-samples\", \"Insights\", \"baremetal\"",
					},
					{
						name: "when capabilities.enabled is set to an unsupported capability it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Capabilities = &hyperv1.Capabilities{
								Enabled: []hyperv1.OptionalCapability{
									hyperv1.OptionalCapability("Storage"),
								},
							}
						},
						expectedErrorSubstring: "Unsupported value: \"Storage\": supported values: \"ImageRegistry\", " +
							"\"openshift-samples\", \"Insights\", \"baremetal\"",
					},
					{
						name: "when the same capability is added to both enabled and disabled, it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Capabilities = &hyperv1.Capabilities{
								Enabled: []hyperv1.OptionalCapability{
									hyperv1.OptionalCapability("Insights"),
								},
								Disabled: []hyperv1.OptionalCapability{
									hyperv1.OptionalCapability("Insights"),
								},
							}
						},
						expectedErrorSubstring: "Capabilities can not be both enabled and disabled at once.",
					},
					{
						name: "when Ingress capability is disabled but Console capability is enabled, it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Capabilities = &hyperv1.Capabilities{
								Enabled: []hyperv1.OptionalCapability{
									hyperv1.ConsoleCapability,
								},
								Disabled: []hyperv1.OptionalCapability{
									hyperv1.IngressCapability,
								},
							}
						},
						expectedErrorSubstring: "Ingress capability can only be disabled if Console capability is also disabled",
					},
					{
						name: "when both Ingress and Console capabilities are disabled, it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Capabilities = &hyperv1.Capabilities{
								Disabled: []hyperv1.OptionalCapability{
									hyperv1.IngressCapability,
									hyperv1.ConsoleCapability,
								},
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when neither Ingress nor Console capability is disabled, it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Capabilities = &hyperv1.Capabilities{
								Disabled: []hyperv1.OptionalCapability{
									hyperv1.ImageRegistryCapability,
								},
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when Ingress capability is enabled but Console capability is disabled, it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Capabilities = &hyperv1.Capabilities{
								Enabled: []hyperv1.OptionalCapability{
									hyperv1.IngressCapability,
								},
								Disabled: []hyperv1.OptionalCapability{
									hyperv1.ConsoleCapability,
								},
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when baseDomain has invalid chars it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.DNS.BaseDomain = "@foo"
						},
						expectedErrorSubstring: "baseDomain must be a valid domain name (e.g., example, example.com, sub.example.com)",
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
						expectedErrorSubstring: "baseDomainPrefix must be a valid domain name (e.g., example, example.com, sub.example.com)",
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
				name: "when GCP project/region validation is applied it should handle formats",
				file: "hostedcluster-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.HostedCluster)
					expectedErrorSubstring string
				}{
					{
						name: "when GCP project ID has invalid format it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Platform.Type = hyperv1.GCPPlatform
							spec := validGCPPlatformSpec()
							spec.Project = "My-Project"
							hc.Spec.Platform.GCP = spec
						},
						expectedErrorSubstring: "project in body should match",
					},
					{
						name: "when GCP region has invalid format it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Platform.Type = hyperv1.GCPPlatform
							spec := validGCPPlatformSpec()
							spec.Region = "us-central"
							hc.Spec.Platform.GCP = spec
						},
						expectedErrorSubstring: "region in body should match",
					},
					{
						name: "when GCP project and region are valid it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Platform.Type = hyperv1.GCPPlatform
							spec := validGCPPlatformSpec()
							spec.Region = "europe-west2"
							hc.Spec.Platform.GCP = spec
						},
						expectedErrorSubstring: "",
					},
				},
			},
			{
				name: "when GCP network configuration is not valid it should fail",
				file: "hostedcluster-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.HostedCluster)
					expectedErrorSubstring string
				}{
					{
						name: "when GCP network name has invalid format it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Platform.Type = hyperv1.GCPPlatform
							spec := validGCPPlatformSpec()
							spec.NetworkConfig.Network.Name = "Invalid-Network-Name"
							hc.Spec.Platform.GCP = spec
						},
						expectedErrorSubstring: "name in body should match",
					},
					{
						name: "when GCP privateServiceConnectSubnet name has invalid format it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Platform.Type = hyperv1.GCPPlatform
							spec := validGCPPlatformSpec()
							spec.NetworkConfig.PrivateServiceConnectSubnet.Name = "Invalid--Subnet"
							hc.Spec.Platform.GCP = spec
						},
						expectedErrorSubstring: "name in body should match",
					},
					{
						name: "when GCP network name is too long it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Platform.Type = hyperv1.GCPPlatform
							spec := validGCPPlatformSpec()
							spec.NetworkConfig.Network.Name = "this-network-name-is-way-way-too-long-and-exceeds-the-maximum-limit"
							hc.Spec.Platform.GCP = spec
						},
						expectedErrorSubstring: "may not be more than 63 bytes",
					},
					{
						name: "when GCP network name is empty it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Platform.Type = hyperv1.GCPPlatform
							spec := validGCPPlatformSpec()
							spec.NetworkConfig.Network.Name = ""
							hc.Spec.Platform.GCP = spec
						},
						expectedErrorSubstring: "name in body should be at least 1 chars long",
					},
					{
						name: "when GCP endpointAccess has invalid value it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Platform.Type = hyperv1.GCPPlatform
							spec := validGCPPlatformSpec()
							spec.EndpointAccess = "InvalidAccess"
							hc.Spec.Platform.GCP = spec
						},
						expectedErrorSubstring: "Unsupported value: \"InvalidAccess\": supported values: \"PublicAndPrivate\", \"Private\"",
					},
					{
						name: "when all GCP network configuration is valid it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Platform.Type = hyperv1.GCPPlatform
							spec := validGCPPlatformSpec()
							spec.EndpointAccess = hyperv1.GCPEndpointAccessPrivate
							hc.Spec.Platform.GCP = spec
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when GCP endpointAccess is PublicAndPrivate it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Platform.Type = hyperv1.GCPPlatform
							spec := validGCPPlatformSpec()
							spec.EndpointAccess = hyperv1.GCPEndpointAccessPublicAndPrivate
							hc.Spec.Platform.GCP = spec
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when GCP resource names contain hyphens correctly it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Platform.Type = hyperv1.GCPPlatform
							spec := validGCPPlatformSpec()
							spec.NetworkConfig.Network.Name = "my-vpc-network"
							spec.NetworkConfig.PrivateServiceConnectSubnet.Name = "my-psc-subnet-01"
							spec.EndpointAccess = hyperv1.GCPEndpointAccessPrivate
							hc.Spec.Platform.GCP = spec
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when GCP resource names are single characters it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Platform.Type = hyperv1.GCPPlatform
							spec := validGCPPlatformSpec()
							spec.NetworkConfig.Network.Name = "n"
							spec.NetworkConfig.PrivateServiceConnectSubnet.Name = "s"
							spec.EndpointAccess = hyperv1.GCPEndpointAccessPrivate
							hc.Spec.Platform.GCP = spec
						},
						expectedErrorSubstring: "",
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
				name: "when Labels contain invalid entries it should fail",
				file: "hostedcluster-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.HostedCluster)
					expectedErrorSubstring string
				}{
					{
						name: "when labels have more than 20 entries it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							labels := map[string]string{}
							for i := 0; i < 25; i++ {
								key := fmt.Sprintf("test%d", i)
								labels[key] = key
							}
							hc.Spec.Labels = labels
						},
						expectedErrorSubstring: "must have at most 20 items",
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
					{
						name: "when allocateNodeCIDRs is Disabled it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							disabled := hyperv1.AllocateNodeCIDRsDisabled
							hc.Spec.Networking = hyperv1.ClusterNetworking{
								AllocateNodeCIDRs: &disabled,
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when allocateNodeCIDRs is Enabled and networkType is Other it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							enabled := hyperv1.AllocateNodeCIDRsEnabled
							hc.Spec.Networking = hyperv1.ClusterNetworking{
								AllocateNodeCIDRs: &enabled,
								NetworkType:       hyperv1.Other,
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when allocateNodeCIDRs is Enabled and networkType is not Other it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							enabled := hyperv1.AllocateNodeCIDRsEnabled
							hc.Spec.Networking = hyperv1.ClusterNetworking{
								AllocateNodeCIDRs: &enabled,
								NetworkType:       hyperv1.OVNKubernetes,
							}
						},
						expectedErrorSubstring: "allocateNodeCIDRs can only be set to Enabled when networkType is 'Other'",
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
					{
						name: "when servicePublishingStrategy is loadBalancer for kas and the hostname clashes with one of configuration.apiServer.servingCerts.namedCertificates it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
								{
									Service: hyperv1.APIServer,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type: hyperv1.LoadBalancer,
										LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
											Hostname: "kas.duplicated.hostname.com",
										},
									},
								},
								{
									Service: hyperv1.Ignition,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type: hyperv1.NodePort,
										NodePort: &hyperv1.NodePortPublishingStrategy{
											Address: "127.0.0.1",
										},
									},
								},
								{
									Service: hyperv1.Konnectivity,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type: hyperv1.NodePort,
										NodePort: &hyperv1.NodePortPublishingStrategy{
											Address: "fd2e:6f44:5dd8:c956::14",
										},
									},
								},
								{
									Service: hyperv1.OAuthServer,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type: hyperv1.NodePort,
										NodePort: &hyperv1.NodePortPublishingStrategy{
											Address: "fd2e:6f44:5dd8:c956:0000:0000:0000:0014",
										},
									},
								},
							}
							hc.Spec.Configuration = &hyperv1.ClusterConfiguration{
								APIServer: &configv1.APIServerSpec{
									ServingCerts: configv1.APIServerServingCerts{
										NamedCertificates: []configv1.APIServerNamedServingCert{
											{
												Names: []string{
													"anything",
													"kas.duplicated.hostname.com",
												},
											},
										},
									},
								},
							}
						},
						expectedErrorSubstring: "loadBalancer hostname cannot be in ClusterConfiguration.apiserver.servingCerts.namedCertificates",
					},
					{
						name: "when servicePublishingStrategy is nodePort and addresses valid hostname, IPv4 and IPv6 it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
								{
									Service: hyperv1.APIServer,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type: hyperv1.NodePort,
										NodePort: &hyperv1.NodePortPublishingStrategy{
											Address: "kas.example.com",
										},
									},
								},
								{
									Service: hyperv1.Ignition,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type: hyperv1.NodePort,
										NodePort: &hyperv1.NodePortPublishingStrategy{
											Address: "127.0.0.1",
										},
									},
								},
								{
									Service: hyperv1.Konnectivity,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type: hyperv1.NodePort,
										NodePort: &hyperv1.NodePortPublishingStrategy{
											Address: "fd2e:6f44:5dd8:c956::14",
										},
									},
								},
								{
									Service: hyperv1.OAuthServer,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type: hyperv1.NodePort,
										NodePort: &hyperv1.NodePortPublishingStrategy{
											Address: "fd2e:6f44:5dd8:c956:0000:0000:0000:0014",
										},
									},
								},
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when servicePublishingStrategy is nodePort and addresses is invalid it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
								{
									Service: hyperv1.APIServer,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type: hyperv1.NodePort,
										NodePort: &hyperv1.NodePortPublishingStrategy{
											Address: "@foo",
										},
									},
								},
								{
									Service: hyperv1.Ignition,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type: hyperv1.NodePort,
										NodePort: &hyperv1.NodePortPublishingStrategy{
											Address: "127.0.0.1",
										},
									},
								},
								{
									Service: hyperv1.Konnectivity,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type: hyperv1.NodePort,
										NodePort: &hyperv1.NodePortPublishingStrategy{
											Address: "fd2e:6f44:5dd8:c956::14",
										},
									},
								},
								{
									Service: hyperv1.OAuthServer,
									ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
										Type: hyperv1.NodePort,
										NodePort: &hyperv1.NodePortPublishingStrategy{
											Address: "fd2e:6f44:5dd8:c956:0000:0000:0000:0014",
										},
									},
								},
							}
						},
						expectedErrorSubstring: "address must be a valid hostname, IPv4, or IPv6 address",
					},
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
						expectedErrorSubstring: "spec.services in body should have at least 4 items or 3 for IBMCloud",
					},
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
			{
				name: "when kubeAPIServerDNSName is not valid it should fail",
				file: "hostedcluster-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.HostedCluster)
					expectedErrorSubstring string
				}{
					{
						name: "when kubeAPIServerDNSName has invalid chars it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.KubeAPIServerDNSName = "@foo"
						},
						expectedErrorSubstring: "kubeAPIServerDNSName must be a valid URL name (e.g., api.example.com)",
					},
					{
						name: "when kubeAPIServerDNSName is a valid hierarchical domain with two levels it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.KubeAPIServerDNSName = "foo.bar"
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when kubeAPIServerDNSName is a valid hierarchical domain it with 3 levels should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.KubeAPIServerDNSName = "123.foo.bar"
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when kubeAPIServerDNSName is a single subdomain it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.KubeAPIServerDNSName = "foo"
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when kubeAPIServerDNSName is empty it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.KubeAPIServerDNSName = ""
						},
						expectedErrorSubstring: "",
					},
				},
			},
			{
				name: "when autoscaling scaleDown is not configured properly it should fail",
				file: "hostedcluster-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.HostedCluster)
					expectedErrorSubstring string
				}{
					{
						name: "when scaling is ScaleUpOnly and scaleDown is set it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Autoscaling = hyperv1.ClusterAutoscaling{
								Scaling: hyperv1.ScaleUpOnly,
								ScaleDown: &hyperv1.ScaleDownConfig{
									DelayAfterAddSeconds: ptr.To(int32(300)),
								},
							}
						},
						expectedErrorSubstring: "scaleDown can only be set when scaling is ScaleUpAndScaleDown",
					},
					{
						name: "when scaling is ScaleUpAndScaleDown and scaleDown is set it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Autoscaling = hyperv1.ClusterAutoscaling{
								Scaling: hyperv1.ScaleUpAndScaleDown,
								ScaleDown: &hyperv1.ScaleDownConfig{
									DelayAfterAddSeconds: ptr.To(int32(300)),
								},
							}
						},
						expectedErrorSubstring: "",
					},
				},
			},
			{
				name: "when balancingIgnoredLabels contains invalid label keys it should fail",
				file: "hostedcluster-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.HostedCluster)
					expectedErrorSubstring string
				}{
					{
						name: "when balancingIgnoredLabels contains invalid label key with special characters it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Autoscaling = hyperv1.ClusterAutoscaling{
								BalancingIgnoredLabels: []string{
									"invalid@label",
								},
							}
						},
						expectedErrorSubstring: "Each balancingIgnoredLabels item must be a valid label key",
					},
					{
						name: "when balancingIgnoredLabels contains invalid label key starting with dash it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Autoscaling = hyperv1.ClusterAutoscaling{
								BalancingIgnoredLabels: []string{
									"-invalid-label",
								},
							}
						},
						expectedErrorSubstring: "Each balancingIgnoredLabels item must be a valid label key",
					},
					{
						name: "when balancingIgnoredLabels contains invalid label key ending with dash it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Autoscaling = hyperv1.ClusterAutoscaling{
								BalancingIgnoredLabels: []string{
									"invalid-label-",
								},
							}
						},
						expectedErrorSubstring: "Each balancingIgnoredLabels item must be a valid label key",
					},
					{
						name: "when balancingIgnoredLabels contains valid label keys it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Autoscaling = hyperv1.ClusterAutoscaling{
								BalancingIgnoredLabels: []string{
									"valid-label",
									"valid.prefix.com/valid-suffix",
									"topology.ebs.csi.aws.com/zone",
								},
							}
						},
						expectedErrorSubstring: "",
					},
				},
			},
			{
				name: "when Azure authentication configuration is not properly configured it should fail",
				file: "hostedcluster-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.HostedCluster)
					expectedErrorSubstring string
				}{
					{
						name: "when azureAuthenticationConfigType is ManagedIdentities but managedIdentities field is missing it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Platform.Type = hyperv1.AzurePlatform
							hc.Spec.Platform.Azure = &hyperv1.AzurePlatformSpec{
								Location:          "eastus",
								ResourceGroupName: "test-rg",
								VnetID:            "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet",
								SubnetID:          "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
								SubscriptionID:    "12345678-1234-5678-9012-123456789012",
								SecurityGroupID:   "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg",
								TenantID:          "87654321-4321-8765-2109-876543210987",
								AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
									AzureAuthenticationConfigType: "ManagedIdentities",
								},
							}
						},
						expectedErrorSubstring: "managedIdentities is required when azureAuthenticationConfigType is ManagedIdentities, and forbidden otherwise",
					},
					{
						name: "when azureAuthenticationConfigType is WorkloadIdentities but workloadIdentities field is missing it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Platform.Type = hyperv1.AzurePlatform
							hc.Spec.Platform.Azure = &hyperv1.AzurePlatformSpec{
								Location:          "eastus",
								ResourceGroupName: "test-rg",
								VnetID:            "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet",
								SubnetID:          "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
								SubscriptionID:    "12345678-1234-5678-9012-123456789012",
								SecurityGroupID:   "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg",
								TenantID:          "87654321-4321-8765-2109-876543210987",
								AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
									AzureAuthenticationConfigType: "WorkloadIdentities",
								},
							}
						},
						expectedErrorSubstring: "workloadIdentities is required when azureAuthenticationConfigType is WorkloadIdentities, and forbidden otherwise",
					},
					{
						name: "when azureAuthenticationConfigType is ManagedIdentities but workloadIdentities field is present it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Platform.Type = hyperv1.AzurePlatform
							hc.Spec.Platform.Azure = &hyperv1.AzurePlatformSpec{
								Location:          "eastus",
								ResourceGroupName: "test-rg",
								VnetID:            "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet",
								SubnetID:          "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
								SubscriptionID:    "12345678-1234-5678-9012-123456789012",
								SecurityGroupID:   "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg",
								TenantID:          "87654321-4321-8765-2109-876543210987",
								AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
									AzureAuthenticationConfigType: "ManagedIdentities",
									WorkloadIdentities: &hyperv1.AzureWorkloadIdentities{
										ImageRegistry:      hyperv1.WorkloadIdentity{ClientID: "12345678-1234-5678-9012-123456789012"},
										Ingress:            hyperv1.WorkloadIdentity{ClientID: "12345678-1234-5678-9012-123456789012"},
										File:               hyperv1.WorkloadIdentity{ClientID: "12345678-1234-5678-9012-123456789012"},
										Disk:               hyperv1.WorkloadIdentity{ClientID: "12345678-1234-5678-9012-123456789012"},
										NodePoolManagement: hyperv1.WorkloadIdentity{ClientID: "12345678-1234-5678-9012-123456789012"},
										CloudProvider:      hyperv1.WorkloadIdentity{ClientID: "12345678-1234-5678-9012-123456789012"},
									},
								},
							}
						},
						expectedErrorSubstring: "managedIdentities is required when azureAuthenticationConfigType is ManagedIdentities, and forbidden otherwise",
					},
					{
						name: "when azureAuthenticationConfigType is WorkloadIdentities but managedIdentities field is present it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Platform.Type = hyperv1.AzurePlatform
							hc.Spec.Platform.Azure = &hyperv1.AzurePlatformSpec{
								Location:          "eastus",
								ResourceGroupName: "test-rg",
								VnetID:            "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet",
								SubnetID:          "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
								SubscriptionID:    "12345678-1234-5678-9012-123456789012",
								SecurityGroupID:   "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg",
								TenantID:          "87654321-4321-8765-2109-876543210987",
								AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
									AzureAuthenticationConfigType: "WorkloadIdentities",
									ManagedIdentities: &hyperv1.AzureResourceManagedIdentities{
										ControlPlane: hyperv1.ControlPlaneManagedIdentities{
											ManagedIdentitiesKeyVault: hyperv1.ManagedAzureKeyVault{
												Name:     "test-kv",
												TenantID: "87654321-4321-8765-2109-876543210987",
											},
											CloudProvider: hyperv1.ManagedIdentity{
												ClientID:              "12345678-1234-5678-9012-123456789012",
												ObjectEncoding:        "utf-8",
												CredentialsSecretName: "cp-secret",
											},
											NodePoolManagement: hyperv1.ManagedIdentity{
												ClientID:              "12345678-1234-5678-9012-123456789012",
												ObjectEncoding:        "utf-8",
												CredentialsSecretName: "npm-secret",
											},
											ControlPlaneOperator: hyperv1.ManagedIdentity{
												ClientID:              "12345678-1234-5678-9012-123456789012",
												ObjectEncoding:        "utf-8",
												CredentialsSecretName: "cpo-secret",
											},
											ImageRegistry: hyperv1.ManagedIdentity{
												ClientID:              "12345678-1234-5678-9012-123456789012",
												ObjectEncoding:        "utf-8",
												CredentialsSecretName: "ir-secret",
											},
											Ingress: hyperv1.ManagedIdentity{
												ClientID:              "12345678-1234-5678-9012-123456789012",
												ObjectEncoding:        "utf-8",
												CredentialsSecretName: "ingress-secret",
											},
											Network: hyperv1.ManagedIdentity{
												ClientID:              "12345678-1234-5678-9012-123456789012",
												ObjectEncoding:        "utf-8",
												CredentialsSecretName: "network-secret",
											},
											Disk: hyperv1.ManagedIdentity{
												ClientID:              "12345678-1234-5678-9012-123456789012",
												ObjectEncoding:        "utf-8",
												CredentialsSecretName: "disk-secret",
											},
											File: hyperv1.ManagedIdentity{
												ClientID:              "12345678-1234-5678-9012-123456789012",
												ObjectEncoding:        "utf-8",
												CredentialsSecretName: "file-secret",
											},
										},
										DataPlane: hyperv1.DataPlaneManagedIdentities{
											ImageRegistryMSIClientID: "12345678-1234-5678-9012-123456789012",
											DiskMSIClientID:          "12345678-1234-5678-9012-123456789012",
											FileMSIClientID:          "12345678-1234-5678-9012-123456789012",
										},
									},
								},
							}
						},
						expectedErrorSubstring: "workloadIdentities is required when azureAuthenticationConfigType is WorkloadIdentities, and forbidden otherwise",
					},
				},
			},
			{
				name: "when operator configuration is not valid it should fail",
				file: "hostedcluster-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.HostedCluster)
					expectedErrorSubstring string
				}{
					{
						name: "when disableMultiNetwork is set to false it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{
								ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{
									DisableMultiNetwork: ptr.To(false),
								},
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when disableMultiNetwork is true and networkType is Other it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Networking = hyperv1.ClusterNetworking{
								NetworkType: hyperv1.Other,
							}
							hc.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{
								ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{
									DisableMultiNetwork: ptr.To(true),
								},
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when disableMultiNetwork is true and networkType is not Other it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Networking = hyperv1.ClusterNetworking{
								NetworkType: hyperv1.OVNKubernetes,
							}
							hc.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{
								ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{
									DisableMultiNetwork: ptr.To(true),
								},
							}
						},
						expectedErrorSubstring: "disableMultiNetwork can only be set to true when networkType is 'Other'",
					},
					{
						name: "when disableMultiNetwork is false and networkType is OVNKubernetes it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Networking = hyperv1.ClusterNetworking{
								NetworkType: hyperv1.OVNKubernetes,
							}
							hc.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{
								ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{
									DisableMultiNetwork: ptr.To(false),
								},
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when ovnKubernetesConfig is set and networkType is not OVNKubernetes it should fail",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Networking = hyperv1.ClusterNetworking{
								NetworkType: hyperv1.OpenShiftSDN,
							}
							hc.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{
								ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{
									OVNKubernetesConfig: &hyperv1.OVNKubernetesConfig{
										IPv4: &hyperv1.OVNIPv4Config{
											InternalJoinSubnet: "10.10.0.0/16",
										},
									},
								},
							}
						},
						expectedErrorSubstring: "ovnKubernetesConfig is forbidden when networkType is not OVNKubernetes",
					},
					{
						name: "when ovnKubernetesConfig is set and networkType is OVNKubernetes it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Networking = hyperv1.ClusterNetworking{
								NetworkType: hyperv1.OVNKubernetes,
							}
							hc.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{
								ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{
									OVNKubernetesConfig: &hyperv1.OVNKubernetesConfig{
										IPv4: &hyperv1.OVNIPv4Config{
											InternalJoinSubnet:          "10.10.0.0/16",
											InternalTransitSwitchSubnet: "10.20.0.0/16",
										},
									},
								},
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when ovnKubernetesConfig is not set and networkType is not OVNKubernetes it should pass",
						mutateInput: func(hc *hyperv1.HostedCluster) {
							hc.Spec.Networking = hyperv1.ClusterNetworking{
								NetworkType: hyperv1.OpenShiftSDN,
							}
							hc.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{
								ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{
									DisableMultiNetwork: ptr.To(false),
								},
							}
						},
						expectedErrorSubstring: "",
					},
				},
			},
		}

		for _, tc := range testCases {
			for i, v := range tc.validations {
				t.Run(v.name, func(t *testing.T) {
					g := NewWithT(t)
					hostedCluster := assets.ShouldHostedCluster(reader, fmt.Sprintf("assets/%s", tc.file))
					hostedCluster.Name = fmt.Sprintf("test-%d-%d", time.Now().UnixNano(), i)
					defer func() {
						g.Expect(crclient.IgnoreNotFound(k8sClient.Delete(ctx, hostedCluster))).To(Succeed())
					}()
					v.mutateInput(hostedCluster)

					// GCP fields only exist in TechPreviewNoUpgrade CRDs.
					if hostedCluster.Spec.Platform.Type == hyperv1.GCPPlatform && featureSet != "TechPreviewNoUpgrade" {
						t.Skipf("Skipping GCP validation outside TechPreviewNoUpgrade: %s", v.name)
					}

					err := k8sClient.Create(ctx, hostedCluster)
					if v.expectedErrorSubstring != "" {
						g.Expect(err).To(HaveOccurred())
						g.Expect(err.Error()).To(ContainSubstring(v.expectedErrorSubstring))
					} else {
						g.Expect(err).ToNot(HaveOccurred())
					}
					g.Expect(crclient.IgnoreNotFound(k8sClient.Delete(ctx, hostedCluster))).To(Succeed())
				})
			}
		}
	})

	t.Run("NodePool creation", func(t *testing.T) {
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
						name: "when pausedUntil is an allowed bool False it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.PausedUntil = ptr.To("False")
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when pausedUntil is an allowed bool false it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.PausedUntil = ptr.To("false")
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when pausedUntil is an allowed bool true it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.PausedUntil = ptr.To("true")
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when pausedUntil is an allowed bool True it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.PausedUntil = ptr.To("True")
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when pausedUntil date is RFC3339 it should pass",
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
						name: "when image is bad format it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Release.Image = "@"
						},
						expectedErrorSubstring: "Image must start with a word character (letters, digits, or underscores) and contain no white spaces",
					},
					{
						name: "when image is empty it should fail",
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
						name: "when replace upgrade type is set with inPlace configuration it should fail",
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
			{
				name: "when AWS placement options have invalid configurations it should fail",
				file: "nodepool-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.NodePool)
					expectedErrorSubstring string
				}{
					{
						name: "when tenancy is 'host' and capacity reservation is specified it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
								Tenancy: "host",
								CapacityReservation: &hyperv1.CapacityReservationOptions{
									ID: ptr.To("cr-1234567890abcdef0"),
								},
							}
						},
						expectedErrorSubstring: "AWS Capacity Reservations cannot be used with Dedicated Hosts (tenancy 'host')",
					},
					{
						name: "when capacity reservation ID is specified with preference 'None' it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
								CapacityReservation: &hyperv1.CapacityReservationOptions{
									ID:         ptr.To("cr-1234567890abcdef0"),
									Preference: hyperv1.CapacityReservationPreferenceNone,
								},
							}
						},
						expectedErrorSubstring: "AWS Capacity Reservation preference 'None' or 'Open' is incompatible with specifying a Capacity Reservation ID",
					},
					{
						name: "when capacity reservation ID has invalid format it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
								CapacityReservation: &hyperv1.CapacityReservationOptions{
									ID: ptr.To("invalid-id"),
								},
							}
						},
						expectedErrorSubstring: "AWS Capacity Reservation ID must start with 'cr-' followed by 17 lowercase hexadecimal characters",
					},
					{
						name: "when capacity reservation ID is specified with preference 'Open' it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
								CapacityReservation: &hyperv1.CapacityReservationOptions{
									ID:         ptr.To("cr-1234567890abcdef0"),
									Preference: hyperv1.CapacityReservationPreferenceOpen,
								},
							}
						},
						expectedErrorSubstring: "AWS Capacity Reservation preference 'None' or 'Open' is incompatible with specifying a Capacity Reservation ID",
					},
					{
						name: "when marketType is 'CapacityBlocks' without capacity reservation ID it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
								CapacityReservation: &hyperv1.CapacityReservationOptions{
									MarketType: hyperv1.MarketTypeCapacityBlock,
									Preference: hyperv1.CapacityReservationPreferenceOpen,
								},
							}
						},
						expectedErrorSubstring: "AWS Capacity Reservation market type 'CapacityBlocks' requires a Capacity Reservation ID",
					},
					{
						name: "when tenancy is 'default' with capacity reservation it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
								Tenancy: "default",
								CapacityReservation: &hyperv1.CapacityReservationOptions{
									ID: ptr.To("cr-1234567890abcdef0"),
								},
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when capacity reservation has preference 'Open' without ID it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
								CapacityReservation: &hyperv1.CapacityReservationOptions{
									Preference: hyperv1.CapacityReservationPreferenceOpen,
									MarketType: hyperv1.MarketTypeOnDemand,
								},
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when capacity reservation ID is specified with preference 'CapacityReservationsOnly' it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
								CapacityReservation: &hyperv1.CapacityReservationOptions{
									ID:         ptr.To("cr-1234567890abcdef0"),
									Preference: hyperv1.CapacityReservationPreferenceOnly,
									MarketType: hyperv1.MarketTypeCapacityBlock,
								},
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "When spot market type is set without spot options it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeSpot,
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "When spot options are set without spot market type it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeOnDemand,
								Spot: hyperv1.SpotOptions{
									MaxPrice: "0.50",
								},
							}
						},
						expectedErrorSubstring: "spot options can only be specified when marketType is 'Spot'",
					},
					{
						name: "When spot market type is set with default tenancy it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeSpot,
								Tenancy:    "default",
								Spot: hyperv1.SpotOptions{
									MaxPrice: "0.50",
								},
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "When spot market type is set with dedicated tenancy it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeSpot,
								Tenancy:    "dedicated",
								Spot: hyperv1.SpotOptions{
									MaxPrice: "0.50",
								},
							}
						},
						expectedErrorSubstring: "Spot instances require default tenancy or unset",
					},
					{
						name: "When spot market type is set with host tenancy it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeSpot,
								Tenancy:    "host",
								Spot: hyperv1.SpotOptions{
									MaxPrice: "0.50",
								},
							}
						},
						expectedErrorSubstring: "Spot instances require default tenancy or unset",
					},
					{
						name: "When spot market type is set with capacity reservation it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeSpot,
								Spot: hyperv1.SpotOptions{
									MaxPrice: "0.50",
								},
								CapacityReservation: &hyperv1.CapacityReservationOptions{
									ID: ptr.To("cr-1234567890abcdef0"),
								},
							}
						},
						expectedErrorSubstring: "Spot instances cannot be combined with Capacity Reservations",
					},
					{
						name: "When deprecated capacity reservation market type is set to CapacityBlocks with ID it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
								CapacityReservation: &hyperv1.CapacityReservationOptions{
									ID:         ptr.To("cr-1234567890abcdef0"),
									MarketType: hyperv1.MarketTypeCapacityBlock,
								},
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "When both placement and deprecated market type are set it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeCapacityBlock,
								CapacityReservation: &hyperv1.CapacityReservationOptions{
									ID:         ptr.To("cr-1234567890abcdef0"),
									MarketType: hyperv1.MarketTypeOnDemand,
								},
							}
						},
						expectedErrorSubstring: "",
					},
				},
			},
			{
				name: "when arch is s390x and platform not kubevirt it should fail",
				file: "nodepool-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.NodePool)
					expectedErrorSubstring string
				}{
					{
						name: "should fail for s390x arch on AWS platform",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Arch = "s390x"
							np.Spec.Platform.Type = hyperv1.AWSPlatform
							np.Spec.Platform.Kubevirt = nil
							np.Spec.Platform.AWS = &hyperv1.AWSNodePoolPlatform{
								InstanceType: "m6i.large",
							}
						},
						expectedErrorSubstring: "s390x is only supported on KubeVirt platform",
					},
				},
			},
			{
				name: "when Azure VM image configuration has valid and invalid combinations",
				file: "nodepool-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.NodePool)
					expectedErrorSubstring string
				}{
					{
						name: "when marketplace is fully populated with imageGeneration set it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.Type = hyperv1.AzurePlatform
							np.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
								VMSize: "Standard_D4s_v5",
								Image: hyperv1.AzureVMImage{
									Type: hyperv1.AzureMarketplace,
									AzureMarketplace: &hyperv1.AzureMarketplaceImage{
										Publisher:       "azureopenshift",
										Offer:           "aro4",
										SKU:             "aro_417_rhel8_gen2",
										Version:         "417.94.20240701",
										ImageGeneration: ptr.To(hyperv1.Gen2),
									},
								},
								OSDisk: hyperv1.AzureNodePoolOSDisk{
									DiskStorageAccountType: hyperv1.DiskStorageAccountTypesPremiumLRS,
								},
								SubnetID: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when marketplace is fully populated without imageGeneration it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.Type = hyperv1.AzurePlatform
							np.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
								VMSize: "Standard_D4s_v5",
								Image: hyperv1.AzureVMImage{
									Type: hyperv1.AzureMarketplace,
									AzureMarketplace: &hyperv1.AzureMarketplaceImage{
										Publisher: "azureopenshift",
										Offer:     "aro4",
										SKU:       "aro_417_rhel8_gen2",
										Version:   "417.94.20240701",
									},
								},
								OSDisk: hyperv1.AzureNodePoolOSDisk{
									DiskStorageAccountType: hyperv1.DiskStorageAccountTypesPremiumLRS,
								},
								SubnetID: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when type is AzureMarketplace with empty marketplace struct and imageGeneration is set it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.Type = hyperv1.AzurePlatform
							np.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
								VMSize: "Standard_D4s_v5",
								Image: hyperv1.AzureVMImage{
									Type: hyperv1.AzureMarketplace,
									AzureMarketplace: &hyperv1.AzureMarketplaceImage{
										ImageGeneration: ptr.To(hyperv1.Gen2),
									},
								},
								OSDisk: hyperv1.AzureNodePoolOSDisk{
									DiskStorageAccountType: hyperv1.DiskStorageAccountTypesPremiumLRS,
								},
								SubnetID: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when type is AzureMarketplace with empty marketplace struct it should pass (allows defaulting)",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.Type = hyperv1.AzurePlatform
							np.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
								VMSize: "Standard_D4s_v5",
								Image: hyperv1.AzureVMImage{
									Type:             hyperv1.AzureMarketplace,
									AzureMarketplace: &hyperv1.AzureMarketplaceImage{},
								},
								OSDisk: hyperv1.AzureNodePoolOSDisk{
									DiskStorageAccountType: hyperv1.DiskStorageAccountTypesPremiumLRS,
								},
								SubnetID: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when marketplace has only publisher and offer but not sku and version it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.Type = hyperv1.AzurePlatform
							np.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
								VMSize: "Standard_D4s_v5",
								Image: hyperv1.AzureVMImage{
									Type: hyperv1.AzureMarketplace,
									AzureMarketplace: &hyperv1.AzureMarketplaceImage{
										Publisher: "azureopenshift",
										Offer:     "aro4",
									},
								},
								OSDisk: hyperv1.AzureNodePoolOSDisk{
									DiskStorageAccountType: hyperv1.DiskStorageAccountTypesPremiumLRS,
								},
								SubnetID: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
							}
						},
						expectedErrorSubstring: "publisher, offer, sku and version must either be all set, or all omitted",
					},
					{
						name: "when marketplace has only sku without publisher, offer, and version it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.Type = hyperv1.AzurePlatform
							np.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
								VMSize: "Standard_D4s_v5",
								Image: hyperv1.AzureVMImage{
									Type: hyperv1.AzureMarketplace,
									AzureMarketplace: &hyperv1.AzureMarketplaceImage{
										SKU: "aro_417_rhel8_gen2",
									},
								},
								OSDisk: hyperv1.AzureNodePoolOSDisk{
									DiskStorageAccountType: hyperv1.DiskStorageAccountTypesPremiumLRS,
								},
								SubnetID: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
							}
						},
						expectedErrorSubstring: "publisher, offer, sku and version must either be all set, or all omitted",
					},
					{
						name: "when marketplace has publisher, offer, sku but not version it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.Type = hyperv1.AzurePlatform
							np.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
								VMSize: "Standard_D4s_v5",
								Image: hyperv1.AzureVMImage{
									Type: hyperv1.AzureMarketplace,
									AzureMarketplace: &hyperv1.AzureMarketplaceImage{
										Publisher: "azureopenshift",
										Offer:     "aro4",
										SKU:       "aro_417_rhel8_gen2",
									},
								},
								OSDisk: hyperv1.AzureNodePoolOSDisk{
									DiskStorageAccountType: hyperv1.DiskStorageAccountTypesPremiumLRS,
								},
								SubnetID: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
							}
						},
						expectedErrorSubstring: "publisher, offer, sku and version must either be all set, or all omitted",
					},
				},
			},
			{
				name: "when AWS imageType and arch combinations are validated",
				file: "nodepool-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.NodePool)
					expectedErrorSubstring string
				}{
					{
						name: "should pass when imageType is not specified for arm64",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Arch = hyperv1.ArchitectureARM64
							np.Spec.Platform.Type = hyperv1.AWSPlatform
							if np.Spec.Platform.AWS == nil {
								np.Spec.Platform.AWS = &hyperv1.AWSNodePoolPlatform{}
							}
							np.Spec.Platform.AWS.InstanceType = "m6g.large"
						},
						expectedErrorSubstring: "",
					},
					{
						name: "should pass when imageType is not specified for amd64",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Arch = hyperv1.ArchitectureAMD64
							np.Spec.Platform.Type = hyperv1.AWSPlatform
							if np.Spec.Platform.AWS == nil {
								np.Spec.Platform.AWS = &hyperv1.AWSNodePoolPlatform{}
							}
							np.Spec.Platform.AWS.InstanceType = "m6i.large"
						},
						expectedErrorSubstring: "",
					},
					{
						name: "should fail when imageType is Windows with arm64 architecture",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Arch = hyperv1.ArchitectureARM64
							np.Spec.Platform.Type = hyperv1.AWSPlatform
							if np.Spec.Platform.AWS == nil {
								np.Spec.Platform.AWS = &hyperv1.AWSNodePoolPlatform{}
							}
							np.Spec.Platform.AWS.InstanceType = "m6g.large"
							np.Spec.Platform.AWS.ImageType = hyperv1.ImageTypeWindows
						},
						expectedErrorSubstring: "ImageType 'Windows' requires arch 'amd64' (AWS only)",
					},
					{
						name: "should pass when imageType is Windows with amd64 architecture",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Arch = hyperv1.ArchitectureAMD64
							np.Spec.Platform.Type = hyperv1.AWSPlatform
							if np.Spec.Platform.AWS == nil {
								np.Spec.Platform.AWS = &hyperv1.AWSNodePoolPlatform{}
							}
							np.Spec.Platform.AWS.InstanceType = "m6i.large"
							np.Spec.Platform.AWS.ImageType = hyperv1.ImageTypeWindows
						},
						expectedErrorSubstring: "",
					},
					{
						name: "should pass when imageType is Windows without arch (defaults to amd64)",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Platform.Type = hyperv1.AWSPlatform
							if np.Spec.Platform.AWS == nil {
								np.Spec.Platform.AWS = &hyperv1.AWSNodePoolPlatform{}
							}
							np.Spec.Platform.AWS.InstanceType = "m6i.large"
							np.Spec.Platform.AWS.ImageType = hyperv1.ImageTypeWindows
						},
						expectedErrorSubstring: "",
					},
					{
						name: "should pass when imageType is Linux with arm64 architecture",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Arch = hyperv1.ArchitectureARM64
							np.Spec.Platform.Type = hyperv1.AWSPlatform
							if np.Spec.Platform.AWS == nil {
								np.Spec.Platform.AWS = &hyperv1.AWSNodePoolPlatform{}
							}
							np.Spec.Platform.AWS.InstanceType = "m6g.large"
							np.Spec.Platform.AWS.ImageType = hyperv1.ImageTypeLinux
						},
						expectedErrorSubstring: "",
					},
					{
						name: "should pass when imageType is Linux with amd64 architecture",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Arch = hyperv1.ArchitectureAMD64
							np.Spec.Platform.Type = hyperv1.AWSPlatform
							if np.Spec.Platform.AWS == nil {
								np.Spec.Platform.AWS = &hyperv1.AWSNodePoolPlatform{}
							}
							np.Spec.Platform.AWS.InstanceType = "m6i.large"
							np.Spec.Platform.AWS.ImageType = hyperv1.ImageTypeLinux
						},
						expectedErrorSubstring: "",
					},
				},
			},
			{
				name: "when autoscaling min=0 (scale-from-zero) is configured it should only be allowed on AWS platform",
				file: "nodepool-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.NodePool)
					expectedErrorSubstring string
				}{
					{
						name: "when autoScaling min=0 on AWS platform it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Replicas = nil
							np.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
								Min: ptr.To[int32](0),
								Max: 3,
							}
							np.Spec.Platform.Type = hyperv1.AWSPlatform
							np.Spec.Platform.AWS = &hyperv1.AWSNodePoolPlatform{
								InstanceType:    "m6a.2xlarge",
								InstanceProfile: "a-profile",
								Subnet: hyperv1.AWSResourceReference{
									ID: ptr.To("subnet-01234567"),
								},
								RootVolume: &hyperv1.Volume{
									Type: "gp3",
									Size: 120,
								},
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when autoScaling min=0 on Azure platform it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Replicas = nil
							np.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
								Min: ptr.To[int32](0),
								Max: 3,
							}
							np.Spec.Platform.Type = hyperv1.AzurePlatform
							np.Spec.Platform.AWS = nil
							np.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
								VMSize: "Standard_D4s_v4",
								Image: hyperv1.AzureVMImage{
									Type:             hyperv1.AzureMarketplace,
									AzureMarketplace: &hyperv1.AzureMarketplaceImage{},
								},
								OSDisk: hyperv1.AzureNodePoolOSDisk{
									DiskStorageAccountType: hyperv1.DiskStorageAccountTypesPremiumLRS,
								},
								SubnetID: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
							}
						},
						expectedErrorSubstring: "Scale-from-zero (autoScaling.min=0) is currently only supported for AWS platform",
					},
					{
						name: "when autoScaling min=0 on Agent platform it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Replicas = nil
							np.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
								Min: ptr.To[int32](0),
								Max: 3,
							}
							np.Spec.Platform.Type = hyperv1.AgentPlatform
							np.Spec.Platform.AWS = nil
							np.Spec.Platform.Agent = &hyperv1.AgentNodePoolPlatform{}
						},
						expectedErrorSubstring: "Scale-from-zero (autoScaling.min=0) is currently only supported for AWS platform",
					},
					{
						name: "when autoScaling min=0 on KubeVirt platform it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Replicas = nil
							np.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
								Min: ptr.To[int32](0),
								Max: 3,
							}
							np.Spec.Platform.Type = hyperv1.KubevirtPlatform
							np.Spec.Platform.AWS = nil
							np.Spec.Platform.Kubevirt = &hyperv1.KubevirtNodePoolPlatform{
								RootVolume: &hyperv1.KubevirtRootVolume{
									KubevirtVolume: hyperv1.KubevirtVolume{
										Type: hyperv1.KubevirtVolumeTypePersistent,
										Persistent: &hyperv1.KubevirtPersistentVolume{
											Size: ptr.To(resource.MustParse("32Gi")),
										},
									},
								},
							}
						},
						expectedErrorSubstring: "Scale-from-zero (autoScaling.min=0) is currently only supported for AWS platform",
					},
					{
						name: "when autoScaling min=1 on Azure platform it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Replicas = nil
							np.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
								Min: ptr.To[int32](1),
								Max: 3,
							}
							np.Spec.Platform.Type = hyperv1.AzurePlatform
							np.Spec.Platform.AWS = nil
							np.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
								VMSize: "Standard_D4s_v4",
								Image: hyperv1.AzureVMImage{
									Type:             hyperv1.AzureMarketplace,
									AzureMarketplace: &hyperv1.AzureMarketplaceImage{},
								},
								OSDisk: hyperv1.AzureNodePoolOSDisk{
									DiskStorageAccountType: hyperv1.DiskStorageAccountTypesPremiumLRS,
								},
								SubnetID: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when autoScaling min=1 on AWS platform it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Replicas = nil
							np.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
								Min: ptr.To[int32](1),
								Max: 3,
							}
							np.Spec.Platform.Type = hyperv1.AWSPlatform
							np.Spec.Platform.AWS = &hyperv1.AWSNodePoolPlatform{
								InstanceType:    "m6a.2xlarge",
								InstanceProfile: "a-profile",
								Subnet: hyperv1.AWSResourceReference{
									ID: ptr.To("subnet-any"),
								},
								RootVolume: &hyperv1.Volume{
									Type: "gp3",
									Size: 120,
								},
							}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when autoScaling is not set it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.AutoScaling = nil
							np.Spec.Replicas = ptr.To[int32](1)
						},
						expectedErrorSubstring: "",
					},
				},
			},
		}

		for _, tc := range testCases {
			for _, v := range tc.validations {
				t.Run(v.name, func(t *testing.T) {
					g := NewWithT(t)
					nodePool := assets.ShouldNodePool(reader, fmt.Sprintf("assets/%s", tc.file))
					defer func() {
						g.Expect(crclient.IgnoreNotFound(k8sClient.Delete(ctx, nodePool))).To(Succeed())
					}()
					v.mutateInput(nodePool)

					err := k8sClient.Create(ctx, nodePool)
					if v.expectedErrorSubstring != "" {
						g.Expect(err).To(HaveOccurred())
						g.Expect(err.Error()).To(ContainSubstring(v.expectedErrorSubstring))
					} else {
						g.Expect(err).ToNot(HaveOccurred())
					}
					g.Expect(crclient.IgnoreNotFound(k8sClient.Delete(ctx, nodePool))).To(Succeed())
				})
			}
		}
	})
}
