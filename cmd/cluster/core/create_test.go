package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiversion "k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakekubeclient "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestValidateMgmtClusterAndNodePoolCPUArchitectures(t *testing.T) {
	ctx := t.Context()

	fakeKubeClient := fakekubeclient.NewClientset()
	fakeDiscovery, ok := fakeKubeClient.Discovery().(*fakediscovery.FakeDiscovery)
	if !ok {
		t.Fatalf("failed to convert FakeDiscovery")
	}

	fakeMetadataProvider := &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
		Result:   &dockerv1client.DockerImageConfig{},
		Manifest: fakeimagemetadataprovider.FakeManifest{},
	}

	// if you want to fake a specific version
	fakeDiscovery.FakedServerVersion = &apiversion.Info{
		Platform: "linux/amd64",
	}

	tests := []struct {
		name        string
		opts        *RawCreateOptions
		expected    bool
		expectError bool
	}{
		{
			name: "When a multi-arch release is passed, the function should return no errors",
			opts: &RawCreateOptions{
				ReleaseImage:   "quay.io/openshift-release-dev/ocp-release:4.16.13-multi",
				PullSecretFile: "../../../hack/dev/fakePullSecret.json",
				ReleaseStream:  "4-stable",
				Arch:           "amd64",
			},
			expectError: false,
		},
		{
			name: "When no release image was passed and a valid multi-arch stream is passed, the function should return no errors",
			opts: &RawCreateOptions{
				ReleaseImage:   "",
				PullSecretFile: "../../../hack/dev/fakePullSecret.json",
				ReleaseStream:  "4-stable-multi",
				Arch:           "amd64",
			},
			expectError: false,
		},
		{
			name: "When a single arch release is passed and the NodePool arch matches the arch of the release, the function should return no errors",
			opts: &RawCreateOptions{
				ReleaseImage:   "quay.io/openshift-release-dev/ocp-release:4.16.13-x86_64",
				PullSecretFile: "../../../hack/dev/fakePullSecret.json",
				ReleaseStream:  "4-stable",
				Arch:           "amd64",
			},
			expectError: false,
		},
		{
			name: "When a single arch release is passed and the NodePool arch doesn't match the arch of the release, the function should return an error",
			opts: &RawCreateOptions{
				ReleaseImage:   "quay.io/openshift-release-dev/ocp-release:4.16.13-x86_64",
				PullSecretFile: "../../../hack/dev/fakePullSecret.json",
				ReleaseStream:  "4-stable",
				Arch:           "arm64",
			},
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			err := validateMgmtClusterAndNodePoolCPUArchitectures(ctx, tc.opts, fakeKubeClient, fakeMetadataProvider)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

// This test will make sure the order of the objects is correct
// being the HC and NP the last ones and the first one is the namespace.
func TestAsObjects(t *testing.T) {
	tests := []struct {
		name         string
		resources    *resources
		expectedFail bool
	}{
		{
			name: "All resources are present",
			resources: &resources{
				Namespace:             &corev1.Namespace{},
				AdditionalTrustBundle: &corev1.ConfigMap{},
				PullSecret:            &corev1.Secret{},
				SSHKey:                &corev1.Secret{},
				Cluster:               &hyperv1.HostedCluster{},
				Resources: []crclient.Object{
					&corev1.ConfigMap{},
					&corev1.Secret{},
				},
				NodePools: []*hyperv1.NodePool{{}, {}},
			},
			expectedFail: false,
		},
		{
			name: "Namespace resource is nil",
			resources: &resources{
				Namespace:             nil,
				AdditionalTrustBundle: &corev1.ConfigMap{},
				PullSecret:            &corev1.Secret{},
				SSHKey:                &corev1.Secret{},
				Cluster:               &hyperv1.HostedCluster{},
				Resources: []crclient.Object{
					&corev1.ConfigMap{},
					&corev1.Secret{},
				},
				NodePools: []*hyperv1.NodePool{{}, {}},
			},
			expectedFail: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			objects := tc.resources.asObjects()
			if tc.expectedFail {
				g.Expect(objects[0]).To(Not(Equal(tc.resources.Namespace)))
				return
			}
			g.Expect(objects[0]).To(Equal(tc.resources.Namespace), "Namespace should be the first object in the slice")
			hcPosition := len(objects) - len(tc.resources.NodePools) - 1
			g.Expect(objects[hcPosition]).To(Equal(tc.resources.Cluster), "HostedCluster should be the secodn-to-last object in the slice")
			g.Expect(objects[len(objects)-1]).To(Equal(tc.resources.NodePools[len(tc.resources.NodePools)-1]), "NodePools should be the last object in the slice")
		})
	}
}

func TestPrototypeResources(t *testing.T) {
	g := NewWithT(t)
	opts := &CreateOptions{
		completedCreateOptions: &completedCreateOptions{
			ValidatedCreateOptions: &ValidatedCreateOptions{
				validatedCreateOptions: &validatedCreateOptions{
					RawCreateOptions: &RawCreateOptions{
						EnableClusterCapabilities:  []string{string(hyperv1.BaremetalCapability)},
						DisableClusterCapabilities: []string{string(hyperv1.ImageRegistryCapability)},
						KubeAPIServerDNSName:       "test-dns-name.example.com",
					},
				},
			},
		},
	}
	resources, err := prototypeResources(t.Context(), opts)
	g.Expect(err).To(BeNil())
	g.Expect(resources.Cluster.Spec.Capabilities.Disabled).
		To(Equal([]hyperv1.OptionalCapability{hyperv1.ImageRegistryCapability}))
	g.Expect(resources.Cluster.Spec.Capabilities.Enabled).
		To(Equal([]hyperv1.OptionalCapability{hyperv1.BaremetalCapability}))
	g.Expect(resources.Cluster.Spec.KubeAPIServerDNSName).To(Equal("test-dns-name.example.com"))
}

func TestValidate(t *testing.T) {
	g := NewWithT(t)
	ctx := t.Context()
	tempDir := t.TempDir()

	pullSecretFile := filepath.Join(tempDir, "pull-secret.json")

	if err := os.WriteFile(pullSecretFile, []byte(`fake`), 0600); err != nil {
		t.Fatalf("failed to write pullSecret: %v", err)
	}

	tests := []struct {
		name        string
		rawOpts     *RawCreateOptions
		expectedErr string
	}{
		{
			name: "fails with unsupported disabled capability",
			rawOpts: &RawCreateOptions{
				Name:                       "test-hc",
				Namespace:                  "test-hc",
				PullSecretFile:             pullSecretFile,
				Arch:                       "amd64",
				DisableClusterCapabilities: []string{"UnsupportedCapability"},
			},
			expectedErr: "unknown disabled capability: UnsupportedCapability, accepted values are:",
		},
		{
			name: "fails with unsupported enabled capability",
			rawOpts: &RawCreateOptions{
				Name:                      "test-hc",
				Namespace:                 "test-hc",
				PullSecretFile:            pullSecretFile,
				Arch:                      "amd64",
				EnableClusterCapabilities: []string{"UnsupportedCapability"},
			},
			expectedErr: "unknown enabled capability: UnsupportedCapability, accepted values are:",
		},
		{
			name: "passes with valid capabilities being enabled and disabled",
			rawOpts: &RawCreateOptions{
				Name:                       "test-hc",
				Namespace:                  "test-hc",
				PullSecretFile:             pullSecretFile,
				Arch:                       "amd64",
				EnableClusterCapabilities:  []string{"baremetal"},
				DisableClusterCapabilities: []string{"ImageRegistry"},
			},
			expectedErr: "",
		},
		{
			name: "passes with openshift-samples capability",
			rawOpts: &RawCreateOptions{
				Name:                       "test-hc",
				Namespace:                  "test-hc",
				PullSecretFile:             pullSecretFile,
				Arch:                       "amd64",
				DisableClusterCapabilities: []string{"openshift-samples"},
			},
			expectedErr: "",
		},
		{
			name: "passes with Insights capability",
			rawOpts: &RawCreateOptions{
				Name:                       "test-hc",
				Namespace:                  "test-hc",
				PullSecretFile:             pullSecretFile,
				Arch:                       "amd64",
				DisableClusterCapabilities: []string{"Insights"},
			},
			expectedErr: "",
		},
		{
			name: "passes with Console capability",
			rawOpts: &RawCreateOptions{
				Name:                       "test-hc",
				Namespace:                  "test-hc",
				PullSecretFile:             pullSecretFile,
				Arch:                       "amd64",
				DisableClusterCapabilities: []string{"Console"},
			},
			expectedErr: "",
		},
		{
			name: "passes with NodeTuning capability",
			rawOpts: &RawCreateOptions{
				Name:                       "test-hc",
				Namespace:                  "test-hc",
				PullSecretFile:             pullSecretFile,
				Arch:                       "amd64",
				DisableClusterCapabilities: []string{"NodeTuning"},
			},
			expectedErr: "",
		},
		{
			name: "fails with an invalid DNS name as KubeAPIServerDNSName",
			rawOpts: &RawCreateOptions{
				Name:                 "test-hc",
				Namespace:            "test-hc",
				PullSecretFile:       pullSecretFile,
				Arch:                 "amd64",
				KubeAPIServerDNSName: "INVALID-DNS-NAME.example.com",
			},
			expectedErr: "KubeAPIServerDNSName failed DNS validation: a lowercase RFC 1123 subdomain must consist of lower case alphanumeric characters",
		},
		{
			name: "passes with KubeAPIServerDNSName",
			rawOpts: &RawCreateOptions{
				Name:                 "test-hc",
				Namespace:            "test-hc",
				PullSecretFile:       pullSecretFile,
				Arch:                 "amd64",
				KubeAPIServerDNSName: "test-dns-name.example.com",
			},
			expectedErr: "",
		},
		{
			name: "fails when ingress is disabled but console is not disabled",
			rawOpts: &RawCreateOptions{
				Name:                       "test-hc",
				Namespace:                  "test-hc",
				PullSecretFile:             pullSecretFile,
				Arch:                       "amd64",
				DisableClusterCapabilities: []string{"Ingress"},
			},
			expectedErr: "ingress capability can only be disabled if Console capability is also disabled",
		},
		{
			name: "passes when both ingress and console are disabled",
			rawOpts: &RawCreateOptions{
				Name:                       "test-hc",
				Namespace:                  "test-hc",
				PullSecretFile:             pullSecretFile,
				Arch:                       "amd64",
				DisableClusterCapabilities: []string{"Ingress", "Console"},
			},
			expectedErr: "",
		},
		{
			name: "passes when only console is disabled without ingress",
			rawOpts: &RawCreateOptions{
				Name:                       "test-hc",
				Namespace:                  "test-hc",
				PullSecretFile:             pullSecretFile,
				Arch:                       "amd64",
				DisableClusterCapabilities: []string{"Console"},
			},
			expectedErr: "",
		},
		{
			name: "passes when ingress and console are disabled along with other capabilities",
			rawOpts: &RawCreateOptions{
				Name:                       "test-hc",
				Namespace:                  "test-hc",
				PullSecretFile:             pullSecretFile,
				Arch:                       "amd64",
				DisableClusterCapabilities: []string{"Ingress", "Console", "ImageRegistry"},
			},
			expectedErr: "",
		},
		{
			name: "fails when ingress is disabled with other capabilities but console is not disabled",
			rawOpts: &RawCreateOptions{
				Name:                       "test-hc",
				Namespace:                  "test-hc",
				PullSecretFile:             pullSecretFile,
				Arch:                       "amd64",
				DisableClusterCapabilities: []string{"Ingress", "ImageRegistry"},
			},
			expectedErr: "ingress capability can only be disabled if Console capability is also disabled",
		},
		{
			name: "passes when disable-multi-network is used with network-type=Other",
			rawOpts: &RawCreateOptions{
				Name:                "test-hc",
				Namespace:           "test-hc",
				PullSecretFile:      pullSecretFile,
				Arch:                "amd64",
				DisableMultiNetwork: true,
				NetworkType:         "Other",
			},
			expectedErr: "",
		},
		{
			name: "passes when disable-multi-network is false with any network-type",
			rawOpts: &RawCreateOptions{
				Name:                "test-hc",
				Namespace:           "test-hc",
				PullSecretFile:      pullSecretFile,
				Arch:                "amd64",
				DisableMultiNetwork: false,
				NetworkType:         "OVNKubernetes",
			},
			expectedErr: "",
		},
		{
			name: "fails when disable-multi-network is true with network-type=OVNKubernetes",
			rawOpts: &RawCreateOptions{
				Name:                "test-hc",
				Namespace:           "test-hc",
				PullSecretFile:      pullSecretFile,
				Arch:                "amd64",
				DisableMultiNetwork: true,
				NetworkType:         "OVNKubernetes",
			},
			expectedErr: "disableMultiNetwork is only allowed when networkType is 'Other' (got 'OVNKubernetes')",
		},
		{
			name: "fails when disable-multi-network is true with network-type=OpenShiftSDN",
			rawOpts: &RawCreateOptions{
				Name:                "test-hc",
				Namespace:           "test-hc",
				PullSecretFile:      pullSecretFile,
				Arch:                "amd64",
				DisableMultiNetwork: true,
				NetworkType:         "OpenShiftSDN",
			},
			expectedErr: "disableMultiNetwork is only allowed when networkType is 'Other' (got 'OpenShiftSDN')",
		},
		{
			name: "fails when disable-multi-network is true with network-type=Calico",
			rawOpts: &RawCreateOptions{
				Name:                "test-hc",
				Namespace:           "test-hc",
				PullSecretFile:      pullSecretFile,
				Arch:                "amd64",
				DisableMultiNetwork: true,
				NetworkType:         "Calico",
			},
			expectedErr: "disableMultiNetwork is only allowed when networkType is 'Other' (got 'Calico')",
		},
		{
			name: "When ovn-kubernetes-mtu is set with OVNKubernetes, it should pass validation",
			rawOpts: &RawCreateOptions{
				Name:             "test-hc",
				Namespace:        "test-hc",
				PullSecretFile:   pullSecretFile,
				Arch:             "amd64",
				OVNKubernetesMTU: 1400,
				NetworkType:      "OVNKubernetes",
			},
			expectedErr: "",
		},
		{
			name: "When ovn-kubernetes-mtu is set with non-OVN network type, it should fail validation",
			rawOpts: &RawCreateOptions{
				Name:             "test-hc",
				Namespace:        "test-hc",
				PullSecretFile:   pullSecretFile,
				Arch:             "amd64",
				OVNKubernetesMTU: 1400,
				NetworkType:      "OpenShiftSDN",
			},
			expectedErr: "--ovn-kubernetes-mtu is only valid when --network-type is OVNKubernetes (got 'OpenShiftSDN')",
		},
		{
			name: "When ovn-kubernetes-mtu is below minimum, it should fail validation",
			rawOpts: &RawCreateOptions{
				Name:             "test-hc",
				Namespace:        "test-hc",
				PullSecretFile:   pullSecretFile,
				Arch:             "amd64",
				OVNKubernetesMTU: 100,
				NetworkType:      "OVNKubernetes",
			},
			expectedErr: "--ovn-kubernetes-mtu must be between 576 and 9216 (got 100)",
		},
		{
			name: "When ovn-kubernetes-mtu is above maximum, it should fail validation",
			rawOpts: &RawCreateOptions{
				Name:             "test-hc",
				Namespace:        "test-hc",
				PullSecretFile:   pullSecretFile,
				Arch:             "amd64",
				OVNKubernetesMTU: 10000,
				NetworkType:      "OVNKubernetes",
			},
			expectedErr: "--ovn-kubernetes-mtu must be between 576 and 9216 (got 10000)",
		},
		{
			name: "When ovn-kubernetes-mtu is negative, it should fail validation",
			rawOpts: &RawCreateOptions{
				Name:             "test-hc",
				Namespace:        "test-hc",
				PullSecretFile:   pullSecretFile,
				Arch:             "amd64",
				OVNKubernetesMTU: -1,
				NetworkType:      "OVNKubernetes",
			},
			expectedErr: "--ovn-kubernetes-mtu must be between 576 and 9216 (got -1)",
		},
		{
			name: "passes when allocate-node-cidrs is used with network-type=Other",
			rawOpts: &RawCreateOptions{
				Name:              "test-hc",
				Namespace:         "test-hc",
				PullSecretFile:    pullSecretFile,
				Arch:              "amd64",
				AllocateNodeCIDRs: true,
				NetworkType:       "Other",
			},
			expectedErr: "",
		},
		{
			name: "passes when allocate-node-cidrs is false with any network-type",
			rawOpts: &RawCreateOptions{
				Name:              "test-hc",
				Namespace:         "test-hc",
				PullSecretFile:    pullSecretFile,
				Arch:              "amd64",
				AllocateNodeCIDRs: false,
				NetworkType:       "OVNKubernetes",
			},
			expectedErr: "",
		},
		{
			name: "fails when allocate-node-cidrs is true with network-type=OVNKubernetes",
			rawOpts: &RawCreateOptions{
				Name:              "test-hc",
				Namespace:         "test-hc",
				PullSecretFile:    pullSecretFile,
				Arch:              "amd64",
				AllocateNodeCIDRs: true,
				NetworkType:       "OVNKubernetes",
			},
			expectedErr: "allocateNodeCIDRs is only allowed when networkType is 'Other' (got 'OVNKubernetes')",
		},
		{
			name: "fails when allocate-node-cidrs is true with network-type=OpenShiftSDN",
			rawOpts: &RawCreateOptions{
				Name:              "test-hc",
				Namespace:         "test-hc",
				PullSecretFile:    pullSecretFile,
				Arch:              "amd64",
				AllocateNodeCIDRs: true,
				NetworkType:       "OpenShiftSDN",
			},
			expectedErr: "allocateNodeCIDRs is only allowed when networkType is 'Other' (got 'OpenShiftSDN')",
		},
		{
			name: "fails when allocate-node-cidrs is true with network-type=Calico",
			rawOpts: &RawCreateOptions{
				Name:              "test-hc",
				Namespace:         "test-hc",
				PullSecretFile:    pullSecretFile,
				Arch:              "amd64",
				AllocateNodeCIDRs: true,
				NetworkType:       "Calico",
			},
			expectedErr: "allocateNodeCIDRs is only allowed when networkType is 'Other' (got 'Calico')",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// avoid actual client calls in Validate
			test.rawOpts.Render = true
			_, err := test.rawOpts.Validate(ctx)
			if test.expectedErr == "" {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(test.expectedErr))
			}
		})
	}
}

func TestDisableMultiNetworkFlag(t *testing.T) {
	tests := []struct {
		name                        string
		disableMultiNetwork         bool
		expectedDisableMultiNetwork *bool
		description                 string
	}{
		{
			name:                        "disable multus flag set to true",
			disableMultiNetwork:         true,
			expectedDisableMultiNetwork: ptr.To(true),
			description:                 "When --disable-multi-network=true is set, DisableMultiNetwork should be true",
		},
		{
			name:                        "disable multus flag set to false",
			disableMultiNetwork:         false,
			expectedDisableMultiNetwork: ptr.To(false),
			description:                 "When --disable-multi-network=false is set, DisableMultiNetwork should be false",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Create options with the test value, following the pattern from TestPrototypeResources
			opts := &CreateOptions{
				completedCreateOptions: &completedCreateOptions{
					ValidatedCreateOptions: &ValidatedCreateOptions{
						validatedCreateOptions: &validatedCreateOptions{
							RawCreateOptions: &RawCreateOptions{
								DisableMultiNetwork: tt.disableMultiNetwork,
							},
						},
					},
				},
			}

			// Create prototype resources using the actual function
			resources, err := prototypeResources(context.Background(), opts)
			g.Expect(err).To(BeNil())

			// Verify the field is set correctly
			if tt.disableMultiNetwork {
				g.Expect(resources.Cluster.Spec.OperatorConfiguration).ToNot(BeNil())
				g.Expect(resources.Cluster.Spec.OperatorConfiguration.ClusterNetworkOperator).ToNot(BeNil())

				// Both should be non-nil pointers to bool
				g.Expect(resources.Cluster.Spec.OperatorConfiguration.ClusterNetworkOperator.DisableMultiNetwork).ToNot(BeNil())
				g.Expect(tt.expectedDisableMultiNetwork).ToNot(BeNil())
				g.Expect(*resources.Cluster.Spec.OperatorConfiguration.ClusterNetworkOperator.DisableMultiNetwork).To(Equal(*tt.expectedDisableMultiNetwork), tt.description)
			} else {
				if resources.Cluster.Spec.OperatorConfiguration != nil && resources.Cluster.Spec.OperatorConfiguration.ClusterNetworkOperator != nil {
					// Both should be non-nil pointers to bool
					g.Expect(resources.Cluster.Spec.OperatorConfiguration.ClusterNetworkOperator.DisableMultiNetwork).ToNot(BeNil())
					g.Expect(tt.expectedDisableMultiNetwork).ToNot(BeNil())
					g.Expect(*resources.Cluster.Spec.OperatorConfiguration.ClusterNetworkOperator.DisableMultiNetwork).To(Equal(*tt.expectedDisableMultiNetwork), tt.description)
				}
				// If OperatorConfiguration is nil, that's also valid since DisableMultiNetwork defaults to false
			}
		})
	}
}

func TestOVNKubernetesMTUFlag(t *testing.T) {
	t.Run("When ovn-kubernetes-mtu is set, it should propagate to OVNKubernetesConfig", func(t *testing.T) {
		g := NewWithT(t)
		resources, err := prototypeResources(t.Context(), &CreateOptions{
			completedCreateOptions: &completedCreateOptions{
				ValidatedCreateOptions: &ValidatedCreateOptions{
					validatedCreateOptions: &validatedCreateOptions{
						RawCreateOptions: &RawCreateOptions{
							OVNKubernetesMTU: 1400,
						},
					},
				},
			},
		})
		g.Expect(err).To(BeNil())
		g.Expect(resources.Cluster.Spec.OperatorConfiguration.ClusterNetworkOperator.OVNKubernetesConfig.MTU).To(Equal(int32(1400)))
	})

	t.Run("When ovn-kubernetes-mtu is not set, it should not create OVNKubernetesConfig", func(t *testing.T) {
		g := NewWithT(t)
		resources, err := prototypeResources(t.Context(), &CreateOptions{
			completedCreateOptions: &completedCreateOptions{
				ValidatedCreateOptions: &ValidatedCreateOptions{
					validatedCreateOptions: &validatedCreateOptions{
						RawCreateOptions: &RawCreateOptions{},
					},
				},
			},
		})
		g.Expect(err).To(BeNil())
		g.Expect(resources.Cluster.Spec.OperatorConfiguration).To(BeNil())
	})
}

func TestAllocateNodeCIDRsFlag(t *testing.T) {
	tests := []struct {
		name                      string
		allocateNodeCIDRs         bool
		expectedAllocateNodeCIDRs *hyperv1.AllocateNodeCIDRsMode
		description               string
	}{
		{
			name:                      "allocate-node-cidrs flag set to true",
			allocateNodeCIDRs:         true,
			expectedAllocateNodeCIDRs: ptr.To(hyperv1.AllocateNodeCIDRsEnabled),
			description:               "When --allocate-node-cidrs=true is set, AllocateNodeCIDRs should be Enabled",
		},
		{
			name:                      "allocate-node-cidrs flag set to false",
			allocateNodeCIDRs:         false,
			expectedAllocateNodeCIDRs: nil,
			description:               "When --allocate-node-cidrs=false is set or not provided, AllocateNodeCIDRs should be nil (defaults to Disabled in webhook).",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Create options with the test value, following the pattern from TestPrototypeResources
			opts := &CreateOptions{
				completedCreateOptions: &completedCreateOptions{
					ValidatedCreateOptions: &ValidatedCreateOptions{
						validatedCreateOptions: &validatedCreateOptions{
							RawCreateOptions: &RawCreateOptions{
								AllocateNodeCIDRs: tt.allocateNodeCIDRs,
							},
						},
					},
				},
			}

			// Create prototype resources using the actual function
			resources, err := prototypeResources(context.Background(), opts)
			g.Expect(err).To(BeNil())
			g.Expect(resources.Cluster.Spec.Networking).ToNot(BeNil())
			if tt.expectedAllocateNodeCIDRs == nil {
				g.Expect(resources.Cluster.Spec.Networking.AllocateNodeCIDRs).To(BeNil(), tt.description)
			} else {
				g.Expect(resources.Cluster.Spec.Networking.AllocateNodeCIDRs).ToNot(BeNil(), tt.description)
				g.Expect(*resources.Cluster.Spec.Networking.AllocateNodeCIDRs).To(Equal(*tt.expectedAllocateNodeCIDRs), tt.description)
			}

		})
	}
}

func TestCreateOptionsGetClient(t *testing.T) {
	t.Run("When ClientFn is set it should use the provided function", func(t *testing.T) {
		g := NewWithT(t)
		expectedClient := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()
		opts := &RawCreateOptions{
			ClientFn: func() (crclient.Client, error) {
				return expectedClient, nil
			},
		}
		c, err := opts.GetClient()
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(c).To(Equal(expectedClient))
	})

	t.Run("When ClientFn is set it should be accessible via completed options", func(t *testing.T) {
		g := NewWithT(t)
		expectedClient := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()
		opts := &CreateOptions{
			completedCreateOptions: &completedCreateOptions{
				ValidatedCreateOptions: &ValidatedCreateOptions{
					validatedCreateOptions: &validatedCreateOptions{
						RawCreateOptions: &RawCreateOptions{
							ClientFn: func() (crclient.Client, error) {
								return expectedClient, nil
							},
						},
					},
				},
			},
		}
		c, err := opts.GetClient()
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(c).To(Equal(expectedClient))
	})
}

func TestValidateWithInjectedClient(t *testing.T) {
	t.Run("When a HostedCluster already exists it should return an error", func(t *testing.T) {
		g := NewWithT(t)
		ctx := t.Context()
		tempDir := t.TempDir()

		pullSecretFile := filepath.Join(tempDir, "pull-secret.json")
		if err := os.WriteFile(pullSecretFile, []byte(`fake`), 0600); err != nil {
			t.Fatalf("failed to write pullSecret: %v", err)
		}

		existingCluster := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "existing-cluster",
				Namespace: "clusters",
			},
		}
		fakeClient := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(existingCluster).
			Build()

		rawOpts := &RawCreateOptions{
			Name:           "existing-cluster",
			Namespace:      "clusters",
			PullSecretFile: pullSecretFile,
			Arch:           "amd64",
			Render:         false,
			ClientFn: func() (crclient.Client, error) {
				return fakeClient, nil
			},
		}

		_, err := rawOpts.Validate(ctx)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("already exists"))
	})

	t.Run("When no HostedCluster exists and render is true it should pass validation", func(t *testing.T) {
		g := NewWithT(t)
		ctx := t.Context()
		tempDir := t.TempDir()

		pullSecretFile := filepath.Join(tempDir, "pull-secret.json")
		if err := os.WriteFile(pullSecretFile, []byte(`fake`), 0600); err != nil {
			t.Fatalf("failed to write pullSecret: %v", err)
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			Build()

		rawOpts := &RawCreateOptions{
			Name:           "new-cluster",
			Namespace:      "clusters",
			PullSecretFile: pullSecretFile,
			Arch:           "amd64",
			Render:         true,
			ClientFn: func() (crclient.Client, error) {
				return fakeClient, nil
			},
		}

		validated, err := rawOpts.Validate(ctx)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(validated).ToNot(BeNil())
	})
}

func TestValidateVersion(t *testing.T) {
	tests := []struct {
		name             string
		versionCLI       string
		operatorVersion  string
		wantError        bool
		errMessage       string
		mutateDeployment func(*appsv1.Deployment)
	}{
		{
			name:            "Commit SHAS match",
			versionCLI:      "abc123",
			operatorVersion: "abc123",
			wantError:       false,
		},
		{
			name:            "Mismatching SHAs",
			versionCLI:      "abc123",
			operatorVersion: "def456",
			wantError:       true,
			errMessage:      "version mismatch detected, CLI: abc123, Operator: def456",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()
			supportedVersions := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "supported-versions",
					Namespace: "hypershift",
				},
				Data: map[string]string{
					config.ConfigMapServerVersionKey: tc.operatorVersion,
					config.ConfigMapVersionsKey:      `{"versions":[]}`,
				},
			}
			client := fake.NewClientBuilder().WithObjects(supportedVersions).Build()
			err := validateVersion(ctx, tc.versionCLI, client)
			if tc.wantError {
				g.Expect(err).To(HaveOccurred(), "Expected error for test case: %s", tc.name)
			} else {
				g.Expect(err).To(BeNil(), "Did not expect error for test case: %s", tc.name)
			}
		})
	}
}

func TestGetServicePublishingStrategyMapping(t *testing.T) {
	t.Parallel()

	deprecatedServiceTypes := []hyperv1.ServiceType{
		hyperv1.OVNSbDb,
	}

	requiredServiceTypes := []hyperv1.ServiceType{
		hyperv1.APIServer,
		hyperv1.OAuthServer,
		hyperv1.Konnectivity,
		hyperv1.Ignition,
	}

	requiredServiceTypesForAPIServerAddress := []hyperv1.ServiceType{
		hyperv1.APIServer,
		hyperv1.OAuthServer,
		hyperv1.OIDC,
		hyperv1.Konnectivity,
		hyperv1.Ignition,
	}

	testAPIServerAddress := "192.168.1.1"

	tests := []struct {
		name             string
		services         []hyperv1.ServicePublishingStrategyMapping
		checkDeprecated  bool
		checkRequired    bool
		requiredTypes    []hyperv1.ServiceType
		checkStrategy    bool
		expectedStrategy hyperv1.PublishingStrategyType
		expectedAddress  string
	}{
		{
			name:            "When GetIngressServicePublishingStrategyMapping is called with OVNKubernetes, it should not include deprecated service types",
			services:        GetIngressServicePublishingStrategyMapping(hyperv1.OVNKubernetes, false),
			checkDeprecated: true,
		},
		{
			name:            "When GetIngressServicePublishingStrategyMapping is called with Other network type, it should not include deprecated service types",
			services:        GetIngressServicePublishingStrategyMapping(hyperv1.Other, false),
			checkDeprecated: true,
		},
		{
			name:            "When GetServicePublishingStrategyMappingByAPIServerAddress is called with OVNKubernetes, it should not include deprecated service types",
			services:        GetServicePublishingStrategyMappingByAPIServerAddress(testAPIServerAddress, hyperv1.OVNKubernetes),
			checkDeprecated: true,
		},
		{
			name:            "When GetServicePublishingStrategyMappingByAPIServerAddress is called with Other network type, it should not include deprecated service types",
			services:        GetServicePublishingStrategyMappingByAPIServerAddress(testAPIServerAddress, hyperv1.Other),
			checkDeprecated: true,
		},
		{
			name:          "When GetIngressServicePublishingStrategyMapping is called, it should include all required service types",
			services:      GetIngressServicePublishingStrategyMapping(hyperv1.Other, false),
			checkRequired: true,
			requiredTypes: requiredServiceTypes,
		},
		{
			name:          "When GetServicePublishingStrategyMappingByAPIServerAddress is called, it should include all required service types",
			services:      GetServicePublishingStrategyMappingByAPIServerAddress(testAPIServerAddress, hyperv1.Other),
			checkRequired: true,
			requiredTypes: requiredServiceTypesForAPIServerAddress,
		},
		{
			name:             "When GetServicePublishingStrategyMappingByAPIServerAddress is called, it should use NodePort strategy with the given address",
			services:         GetServicePublishingStrategyMappingByAPIServerAddress(testAPIServerAddress, hyperv1.OVNKubernetes),
			checkStrategy:    true,
			expectedStrategy: hyperv1.NodePort,
			expectedAddress:  testAPIServerAddress,
		},
		{
			name:             "When GetIngressServicePublishingStrategyMapping is called without external DNS, it should use LoadBalancer for APIServer",
			services:         GetIngressServicePublishingStrategyMapping(hyperv1.OVNKubernetes, false),
			checkStrategy:    true,
			expectedStrategy: hyperv1.LoadBalancer,
		},
		{
			name:             "When GetIngressServicePublishingStrategyMapping is called with external DNS, it should use Route for APIServer",
			services:         GetIngressServicePublishingStrategyMapping(hyperv1.OVNKubernetes, true),
			checkStrategy:    true,
			expectedStrategy: hyperv1.Route,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			if tc.checkDeprecated {
				for _, svc := range tc.services {
					for _, deprecated := range deprecatedServiceTypes {
						g.Expect(svc.Service).NotTo(Equal(deprecated),
							"service list should not contain deprecated service type %s", deprecated)
					}
				}
			}

			if tc.checkRequired {
				serviceTypes := make(map[hyperv1.ServiceType]bool)
				for _, svc := range tc.services {
					serviceTypes[svc.Service] = true
				}
				for _, required := range tc.requiredTypes {
					g.Expect(serviceTypes).To(HaveKey(required),
						"service list should include required service type %s", required)
				}
			}

			if tc.checkStrategy {
				g.Expect(tc.services).NotTo(BeEmpty(), "service list should not be empty")
				// Find APIServer entry to check its strategy
				foundAPIServer := false
				for _, svc := range tc.services {
					if svc.Service == hyperv1.APIServer {
						foundAPIServer = true
						g.Expect(svc.ServicePublishingStrategy.Type).To(Equal(tc.expectedStrategy),
							"APIServer should use %s strategy", tc.expectedStrategy)
						if tc.expectedAddress != "" {
							g.Expect(svc.ServicePublishingStrategy.NodePort).NotTo(BeNil(),
								"NodePort config should not be nil")
							g.Expect(svc.ServicePublishingStrategy.NodePort.Address).To(Equal(tc.expectedAddress),
								"NodePort address should match the provided API server address")
						}
					}
				}
				g.Expect(foundAPIServer).To(BeTrue(), "service list should include APIServer")
			}
		})
	}
}

func TestParseKeyValuePairs(t *testing.T) {
	tests := []struct {
		name        string
		items       []string
		kind        string
		expected    map[string]string
		expectError string
	}{
		{
			name:     "When valid key=value pairs are provided, it should return a map",
			items:    []string{"key1=value1", "key2=value2"},
			kind:     "annotation",
			expected: map[string]string{"key1": "value1", "key2": "value2"},
		},
		{
			name:     "When an empty list is provided, it should return an empty map",
			items:    []string{},
			kind:     "label",
			expected: map[string]string{},
		},
		{
			name:        "When a malformed pair without equals sign is provided, it should return an error",
			items:       []string{"badentry"},
			kind:        "annotation",
			expectError: "invalid annotation: badentry",
		},
		{
			name:     "When a value contains an equals sign, it should split only on the first equals",
			items:    []string{"key=val=extra"},
			kind:     "label",
			expected: map[string]string{"key": "val=extra"},
		},
		{
			name:     "When a value is empty after equals sign, it should accept it",
			items:    []string{"key="},
			kind:     "annotation",
			expected: map[string]string{"key": ""},
		},
		{
			name:        "When the key is empty, it should return an error",
			items:       []string{"=value"},
			kind:        "label",
			expectError: "key must not be empty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			result, err := parseKeyValuePairs(tc.items, tc.kind)
			if tc.expectError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectError))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(result).To(Equal(tc.expected))
			}
		})
	}
}

func TestApplyEtcdConfig(t *testing.T) {
	tests := []struct {
		name               string
		etcdStorageClass   string
		etcdStorageSize    string
		expectError        string
		expectStorageClass *string
		expectSizeString   string
	}{
		{
			name:             "When no etcd storage options are provided, it should leave defaults unchanged",
			etcdStorageClass: "",
			etcdStorageSize:  "",
		},
		{
			name:               "When etcd storage class is provided, it should set the storage class",
			etcdStorageClass:   "gp3-csi",
			expectStorageClass: ptr.To("gp3-csi"),
		},
		{
			name:             "When a valid etcd storage size is provided, it should set the storage size",
			etcdStorageSize:  "8Gi",
			expectSizeString: "8Gi",
		},
		{
			name:            "When an invalid etcd storage size is provided, it should return an error",
			etcdStorageSize: "notasize",
			expectError:     "failed parse ectd storage size",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			cluster := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Etcd: hyperv1.EtcdSpec{
						Managed: &hyperv1.ManagedEtcdSpec{
							Storage: hyperv1.ManagedEtcdStorageSpec{
								PersistentVolume: &hyperv1.PersistentVolumeEtcdStorageSpec{
									Size: &hyperv1.DefaultPersistentVolumeEtcdStorageSize,
								},
							},
						},
					},
				},
			}
			opts := &CreateOptions{
				completedCreateOptions: &completedCreateOptions{
					ValidatedCreateOptions: &ValidatedCreateOptions{
						validatedCreateOptions: &validatedCreateOptions{
							RawCreateOptions: &RawCreateOptions{
								EtcdStorageClass: tc.etcdStorageClass,
								EtcdStorageSize:  tc.etcdStorageSize,
							},
						},
					},
				},
			}

			err := applyEtcdConfig(cluster, opts)
			if tc.expectError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectError))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				if tc.expectStorageClass != nil {
					g.Expect(cluster.Spec.Etcd.Managed.Storage.PersistentVolume.StorageClassName).To(Equal(tc.expectStorageClass))
				}
				if tc.expectSizeString != "" {
					g.Expect(cluster.Spec.Etcd.Managed.Storage.PersistentVolume.Size.String()).To(Equal(tc.expectSizeString))
				}
				// When no storage options are provided, verify defaults remain unchanged
				if tc.etcdStorageClass == "" && tc.etcdStorageSize == "" {
					// StorageClassName should remain nil (default)
					g.Expect(cluster.Spec.Etcd.Managed.Storage.PersistentVolume.StorageClassName).To(BeNil(), "StorageClassName should remain nil when not specified")
					// Size should remain at the default value
					g.Expect(cluster.Spec.Etcd.Managed.Storage.PersistentVolume.Size).To(Equal(&hyperv1.DefaultPersistentVolumeEtcdStorageSize), "Size should remain at default when not specified")
				}
			}
		})
	}
}

func TestApplyPausedUntil(t *testing.T) {
	tests := []struct {
		name        string
		pausedUntil string
		expectError string
		expectSet   bool
	}{
		{
			name:        "When pausedUntil is empty, it should not set PausedUntil on the cluster",
			pausedUntil: "",
			expectSet:   false,
		},
		{
			name:        "When pausedUntil is 'true', it should not set PausedUntil on the cluster",
			pausedUntil: "true",
			expectSet:   false,
		},
		{
			name:        "When pausedUntil is a valid RFC3339 date, it should set PausedUntil on the cluster",
			pausedUntil: "2026-12-31T23:59:59Z",
			expectSet:   true,
		},
		{
			name:        "When pausedUntil is an invalid date format, it should return an error",
			pausedUntil: "not-a-date",
			expectError: "invalid pausedUntil value",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			cluster := &hyperv1.HostedCluster{}
			opts := &CreateOptions{
				completedCreateOptions: &completedCreateOptions{
					ValidatedCreateOptions: &ValidatedCreateOptions{
						validatedCreateOptions: &validatedCreateOptions{
							RawCreateOptions: &RawCreateOptions{
								PausedUntil: tc.pausedUntil,
							},
						},
					},
				},
			}

			err := applyPausedUntil(cluster, opts)
			if tc.expectError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectError))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				if tc.expectSet {
					g.Expect(cluster.Spec.PausedUntil).NotTo(BeNil())
					g.Expect(*cluster.Spec.PausedUntil).To(Equal(tc.pausedUntil))
				} else {
					g.Expect(cluster.Spec.PausedUntil).To(BeNil())
				}
			}
		})
	}
}

func TestApplyOLMConfig(t *testing.T) {
	tests := []struct {
		name                     string
		olmDisableDefaultSources bool
		olmCatalogPlacement      hyperv1.OLMCatalogPlacement
		expectOperatorHub        bool
		expectCatalogPlacement   hyperv1.OLMCatalogPlacement
	}{
		{
			name:                     "When OLM default sources are disabled, it should set DisableAllDefaultSources",
			olmDisableDefaultSources: true,
			olmCatalogPlacement:      "",
			expectOperatorHub:        true,
		},
		{
			name:                   "When OLM catalog placement is set to Guest, it should set OLMCatalogPlacement",
			olmCatalogPlacement:    hyperv1.GuestOLMCatalogPlacement,
			expectCatalogPlacement: hyperv1.GuestOLMCatalogPlacement,
		},
		{
			name:              "When no OLM options are set, it should not modify the cluster",
			expectOperatorHub: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			cluster := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Configuration: &hyperv1.ClusterConfiguration{},
				},
			}
			opts := &CreateOptions{
				completedCreateOptions: &completedCreateOptions{
					ValidatedCreateOptions: &ValidatedCreateOptions{
						validatedCreateOptions: &validatedCreateOptions{
							RawCreateOptions: &RawCreateOptions{
								OLMDisableDefaultSources: tc.olmDisableDefaultSources,
								OLMCatalogPlacement:      tc.olmCatalogPlacement,
							},
						},
					},
				},
			}

			applyOLMConfig(cluster, opts)
			if tc.expectOperatorHub {
				g.Expect(cluster.Spec.Configuration.OperatorHub).NotTo(BeNil())
				g.Expect(cluster.Spec.Configuration.OperatorHub.DisableAllDefaultSources).To(BeTrue())
			}
			if tc.expectCatalogPlacement != "" {
				g.Expect(cluster.Spec.OLMCatalogPlacement).To(Equal(tc.expectCatalogPlacement))
			}
			// When no OLM options are set, verify cluster remains unmodified
			if !tc.olmDisableDefaultSources && tc.olmCatalogPlacement == "" {
				g.Expect(cluster.Spec.Configuration.OperatorHub).To(BeNil(), "OperatorHub should remain nil when OLM options are not set")
				g.Expect(cluster.Spec.OLMCatalogPlacement).To(BeEmpty(), "OLMCatalogPlacement should remain empty when not specified")
			}
		})
	}
}

func TestApplyFeatureSet(t *testing.T) {
	tests := []struct {
		name               string
		featureSet         string
		expectFeatureGate  bool
		expectedFeatureSet configv1.FeatureSet
	}{
		{
			name:              "When feature set is Default, it should not set FeatureGate",
			featureSet:        string(configv1.Default),
			expectFeatureGate: false,
		},
		{
			name:               "When feature set is TechPreviewNoUpgrade, it should set the TechPreviewNoUpgrade feature gate",
			featureSet:         string(configv1.TechPreviewNoUpgrade),
			expectFeatureGate:  true,
			expectedFeatureSet: configv1.TechPreviewNoUpgrade,
		},
		{
			name:               "When feature set is DevPreviewNoUpgrade, it should set the DevPreviewNoUpgrade feature gate",
			featureSet:         string(configv1.DevPreviewNoUpgrade),
			expectFeatureGate:  true,
			expectedFeatureSet: configv1.DevPreviewNoUpgrade,
		},
		{
			name:              "When feature set is an unrecognized value, it should not set FeatureGate",
			featureSet:        "SomethingElse",
			expectFeatureGate: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			cluster := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Configuration: &hyperv1.ClusterConfiguration{},
				},
			}
			opts := &CreateOptions{
				completedCreateOptions: &completedCreateOptions{
					ValidatedCreateOptions: &ValidatedCreateOptions{
						validatedCreateOptions: &validatedCreateOptions{
							RawCreateOptions: &RawCreateOptions{
								FeatureSet: tc.featureSet,
							},
						},
					},
				},
			}

			applyFeatureSet(cluster, opts)
			if tc.expectFeatureGate {
				g.Expect(cluster.Spec.Configuration.FeatureGate).NotTo(BeNil())
				g.Expect(cluster.Spec.Configuration.FeatureGate.FeatureSet).To(Equal(tc.expectedFeatureSet))
			} else {
				g.Expect(cluster.Spec.Configuration.FeatureGate).To(BeNil())
			}
		})
	}
}

func TestApplyNetworkConfig(t *testing.T) {
	tests := []struct {
		name           string
		clusterCIDR    []string
		serviceCIDR    []string
		machineCIDR    []string
		expectError    string
		expectClusters int
		expectServices int
		expectMachines int
	}{
		{
			name:           "When valid CIDRs are provided, it should parse and append them",
			clusterCIDR:    []string{"10.128.0.0/14"},
			serviceCIDR:    []string{"172.30.0.0/16"},
			machineCIDR:    []string{"10.0.0.0/16"},
			expectClusters: 1,
			expectServices: 1,
			expectMachines: 1,
		},
		{
			name:           "When dual-stack CIDRs are provided, it should parse both",
			clusterCIDR:    []string{"10.128.0.0/14", "fd01::/48"},
			serviceCIDR:    []string{"172.30.0.0/16", "fd02::/112"},
			expectClusters: 2,
			expectServices: 2,
			expectMachines: 0,
		},
		{
			name:        "When an invalid cluster CIDR is provided, it should return an error",
			clusterCIDR: []string{"not-a-cidr"},
			expectError: "parsing ClusterCIDR",
		},
		{
			name:        "When an invalid service CIDR is provided, it should return an error",
			serviceCIDR: []string{"not-a-cidr"},
			expectError: "parsing ServiceCIDR",
		},
		{
			name:        "When an invalid machine CIDR is provided, it should return an error",
			machineCIDR: []string{"not-a-cidr"},
			expectError: "parsing MachineCIDR",
		},
		{
			name:           "When no CIDRs are provided, it should produce empty network lists",
			expectClusters: 0,
			expectServices: 0,
			expectMachines: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			cluster := &hyperv1.HostedCluster{}
			opts := &CreateOptions{
				completedCreateOptions: &completedCreateOptions{
					ValidatedCreateOptions: &ValidatedCreateOptions{
						validatedCreateOptions: &validatedCreateOptions{
							RawCreateOptions: &RawCreateOptions{
								ClusterCIDR: tc.clusterCIDR,
								ServiceCIDR: tc.serviceCIDR,
								MachineCIDR: tc.machineCIDR,
							},
						},
					},
				},
			}

			err := applyNetworkConfig(cluster, opts)
			if tc.expectError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectError))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cluster.Spec.Networking.ClusterNetwork).To(HaveLen(tc.expectClusters))
				g.Expect(cluster.Spec.Networking.ServiceNetwork).To(HaveLen(tc.expectServices))
				g.Expect(cluster.Spec.Networking.MachineNetwork).To(HaveLen(tc.expectMachines))
			}
		})
	}
}

func TestApplySchedulingConfig(t *testing.T) {
	tests := []struct {
		name               string
		nodeSelector       map[string]string
		podsLabels         map[string]string
		expectNodeSelector map[string]string
		expectLabels       map[string]string
	}{
		{
			name:               "When node selector is provided, it should set NodeSelector on the cluster",
			nodeSelector:       map[string]string{"role": "cp", "disk": "fast"},
			expectNodeSelector: map[string]string{"role": "cp", "disk": "fast"},
		},
		{
			name:         "When pods labels are provided, it should set Labels on the cluster",
			podsLabels:   map[string]string{"team": "hypershift"},
			expectLabels: map[string]string{"team": "hypershift"},
		},
		{
			name: "When neither is provided, it should not modify the cluster",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			cluster := &hyperv1.HostedCluster{}
			opts := &CreateOptions{
				completedCreateOptions: &completedCreateOptions{
					ValidatedCreateOptions: &ValidatedCreateOptions{
						validatedCreateOptions: &validatedCreateOptions{
							RawCreateOptions: &RawCreateOptions{
								NodeSelector: tc.nodeSelector,
								PodsLabels:   tc.podsLabels,
							},
						},
					},
				},
			}

			applySchedulingConfig(cluster, opts)
			if tc.expectNodeSelector != nil {
				g.Expect(cluster.Spec.NodeSelector).To(Equal(tc.expectNodeSelector))
			}
			if tc.expectLabels != nil {
				g.Expect(cluster.Spec.Labels).To(Equal(tc.expectLabels))
			}
			// When neither node selector nor labels are provided, verify cluster remains unmodified
			if tc.nodeSelector == nil && tc.podsLabels == nil {
				g.Expect(cluster.Spec.NodeSelector).To(BeNil(), "NodeSelector should remain nil when not specified")
				g.Expect(cluster.Spec.Labels).To(BeNil(), "Labels should remain nil when not specified")
			}
		})
	}
}

func TestParseTolerationString(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectError string
		expected    *corev1.Toleration
	}{
		{
			name:  "When a full toleration is specified, it should parse all fields",
			input: "key=node-role.kubernetes.io/master,operator=Exists,effect=NoSchedule",
			expected: &corev1.Toleration{
				Key:      "node-role.kubernetes.io/master",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			},
		},
		{
			name:  "When operator is Equal and a value is specified, it should parse correctly",
			input: "key=mykey,value=myvalue,operator=Equal,effect=NoExecute",
			expected: &corev1.Toleration{
				Key:      "mykey",
				Value:    "myvalue",
				Operator: corev1.TolerationOpEqual,
				Effect:   corev1.TaintEffectNoExecute,
			},
		},
		{
			name:  "When tolerationSeconds is specified, it should parse the integer value",
			input: "key=mykey,operator=Exists,effect=NoExecute,tolerationSeconds=300",
			expected: &corev1.Toleration{
				Key:               "mykey",
				Operator:          corev1.TolerationOpExists,
				Effect:            corev1.TaintEffectNoExecute,
				TolerationSeconds: ptr.To(int64(300)),
			},
		},
		{
			name:  "When effect is PreferNoSchedule, it should normalize the case",
			input: "key=mykey,effect=preferNoSchedule",
			expected: &corev1.Toleration{
				Key:    "mykey",
				Effect: corev1.TaintEffectPreferNoSchedule,
			},
		},
		{
			name:        "When an unknown operator type is provided, it should return an error",
			input:       "key=mykey,operator=Unknown",
			expectError: "unknown operator type",
		},
		{
			name:        "When an unknown effect type is provided, it should return an error",
			input:       "key=mykey,effect=Unknown",
			expectError: "unknown effect type",
		},
		{
			name:        "When a malformed key-value is provided, it should return an error",
			input:       "badformat",
			expectError: "invalid toleration cli argument",
		},
		{
			name:        "When an unknown field is provided, it should return an error",
			input:       "unknownfield=value",
			expectError: "unknown field",
		},
		{
			name:        "When tolerationSeconds is not an integer, it should return an error",
			input:       "key=mykey,tolerationSeconds=abc",
			expectError: "failed to parse tolerationSeconds",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			result, err := parseTolerationString(tc.input)
			if tc.expectError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectError))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(result).To(Equal(tc.expected))
			}
		})
	}
}

func TestPostProcess(t *testing.T) {
	t.Run("When secret encryption is nil, it should default to AESCBC", func(t *testing.T) {
		g := NewWithT(t)
		r := &resources{
			Cluster: &hyperv1.HostedCluster{},
		}
		opts := &CreateOptions{
			completedCreateOptions: &completedCreateOptions{
				ValidatedCreateOptions: &ValidatedCreateOptions{
					validatedCreateOptions: &validatedCreateOptions{
						RawCreateOptions: &RawCreateOptions{
							Name:      "test-cluster",
							Namespace: "clusters",
						},
					},
				},
			},
		}

		postProcess(r, opts)

		g.Expect(r.Cluster.Spec.SecretEncryption).NotTo(BeNil())
		g.Expect(r.Cluster.Spec.SecretEncryption.Type).To(Equal(hyperv1.AESCBC))
		g.Expect(r.Cluster.Spec.SecretEncryption.AESCBC).NotTo(BeNil())
		g.Expect(r.Cluster.Spec.SecretEncryption.AESCBC.ActiveKey.Name).To(Equal("test-cluster-etcd-encryption-key"))
		g.Expect(r.Resources).To(HaveLen(1))
	})

	t.Run("When secret encryption is already set, it should not override it", func(t *testing.T) {
		g := NewWithT(t)
		r := &resources{
			Cluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					SecretEncryption: &hyperv1.SecretEncryptionSpec{
						Type: hyperv1.KMS,
					},
				},
			},
		}
		opts := &CreateOptions{
			completedCreateOptions: &completedCreateOptions{
				ValidatedCreateOptions: &ValidatedCreateOptions{
					validatedCreateOptions: &validatedCreateOptions{
						RawCreateOptions: &RawCreateOptions{
							Name:      "test-cluster",
							Namespace: "clusters",
						},
					},
				},
			},
		}

		postProcess(r, opts)

		g.Expect(r.Cluster.Spec.SecretEncryption.Type).To(Equal(hyperv1.KMS))
		g.Expect(r.Resources).To(BeEmpty())
	})
}

func TestDefaultNodePool(t *testing.T) {
	tests := []struct {
		name         string
		clusterName  string
		suffix       string
		expectedName string
	}{
		{
			name:         "When no suffix is provided, it should use the cluster name",
			clusterName:  "my-cluster",
			suffix:       "",
			expectedName: "my-cluster",
		},
		{
			name:         "When a suffix is provided, it should append it to the cluster name",
			clusterName:  "my-cluster",
			suffix:       "us-east-1a",
			expectedName: "my-cluster-us-east-1a",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			replicas := int32(3)
			opts := &CreateOptions{
				completedCreateOptions: &completedCreateOptions{
					ValidatedCreateOptions: &ValidatedCreateOptions{
						validatedCreateOptions: &validatedCreateOptions{
							RawCreateOptions: &RawCreateOptions{
								Name:             tc.clusterName,
								Namespace:        "clusters",
								NodePoolReplicas: replicas,
								ReleaseImage:     "quay.io/openshift/ocp:4.16",
								AutoRepair:       true,
								Arch:             "amd64",
								NodeDrainTimeout: 5 * time.Minute,
							},
						},
					},
				},
			}

			constructor := defaultNodePool(opts)
			np := constructor(hyperv1.AWSPlatform, tc.suffix)

			g.Expect(np.Name).To(Equal(tc.expectedName))
			g.Expect(np.Namespace).To(Equal("clusters"))
			g.Expect(np.Spec.ClusterName).To(Equal(tc.clusterName))
			g.Expect(np.Spec.Platform.Type).To(Equal(hyperv1.AWSPlatform))
			g.Expect(*np.Spec.Replicas).To(Equal(replicas))
			g.Expect(np.Spec.Management.AutoRepair).To(BeTrue())
			g.Expect(np.Spec.Arch).To(Equal("amd64"))
			g.Expect(np.Spec.Release.Image).To(Equal("quay.io/openshift/ocp:4.16"))
		})
	}
}

func TestValidateArchAndFeatureSet(t *testing.T) {
	tests := []struct {
		name        string
		arch        string
		featureSet  string
		expectError string
	}{
		{
			name:       "When amd64 arch and Default feature set are provided, it should pass",
			arch:       "amd64",
			featureSet: string(configv1.Default),
		},
		{
			name:       "When arm64 arch is provided, it should pass",
			arch:       "arm64",
			featureSet: string(configv1.Default),
		},
		{
			name:       "When ppc64le arch is provided, it should pass",
			arch:       "ppc64le",
			featureSet: string(configv1.Default),
		},
		{
			name:       "When s390x arch is provided, it should pass",
			arch:       "s390x",
			featureSet: string(configv1.Default),
		},
		{
			name:        "When an unsupported arch is provided, it should return an error",
			arch:        "mips",
			featureSet:  string(configv1.Default),
			expectError: "specified arch",
		},
		{
			name:       "When TechPreviewNoUpgrade feature set is provided, it should pass",
			arch:       "amd64",
			featureSet: string(configv1.TechPreviewNoUpgrade),
		},
		{
			name:       "When DevPreviewNoUpgrade feature set is provided, it should pass",
			arch:       "amd64",
			featureSet: string(configv1.DevPreviewNoUpgrade),
		},
		{
			name:        "When CustomNoUpgrade feature set is provided, it should return an error",
			arch:        "amd64",
			featureSet:  string(configv1.CustomNoUpgrade),
			expectError: "only a predefined feature set is supported",
		},
		{
			name:        "When an unknown feature set is provided, it should return an error",
			arch:        "amd64",
			featureSet:  "SomethingRandom",
			expectError: "specified feature set",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			opts := &RawCreateOptions{
				Arch:       tc.arch,
				FeatureSet: tc.featureSet,
			}

			err := opts.validateArchAndFeatureSet()
			if tc.expectError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectError))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestApplyClusterCapabilities(t *testing.T) {
	tests := []struct {
		name           string
		enableCaps     []string
		disableCaps    []string
		expectEnabled  []hyperv1.OptionalCapability
		expectDisabled []hyperv1.OptionalCapability
	}{
		{
			name:           "When both enable and disable capabilities are provided, it should set both",
			enableCaps:     []string{"baremetal", "Console"},
			disableCaps:    []string{"ImageRegistry"},
			expectEnabled:  []hyperv1.OptionalCapability{"baremetal", "Console"},
			expectDisabled: []hyperv1.OptionalCapability{"ImageRegistry"},
		},
		{
			name:           "When only disable capabilities are provided, it should set disabled only",
			disableCaps:    []string{"Insights", "NodeTuning"},
			expectDisabled: []hyperv1.OptionalCapability{"Insights", "NodeTuning"},
		},
		{
			name: "When neither enable nor disable are provided, it should not set capabilities",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			cluster := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Capabilities: &hyperv1.Capabilities{},
				},
			}
			opts := &CreateOptions{
				completedCreateOptions: &completedCreateOptions{
					ValidatedCreateOptions: &ValidatedCreateOptions{
						validatedCreateOptions: &validatedCreateOptions{
							RawCreateOptions: &RawCreateOptions{
								EnableClusterCapabilities:  tc.enableCaps,
								DisableClusterCapabilities: tc.disableCaps,
							},
						},
					},
				},
			}

			applyClusterCapabilities(cluster, opts)

			if tc.expectEnabled != nil {
				g.Expect(cluster.Spec.Capabilities.Enabled).To(Equal(tc.expectEnabled))
			} else {
				g.Expect(cluster.Spec.Capabilities.Enabled).To(BeNil())
			}
			if tc.expectDisabled != nil {
				g.Expect(cluster.Spec.Capabilities.Disabled).To(Equal(tc.expectDisabled))
			} else {
				g.Expect(cluster.Spec.Capabilities.Disabled).To(BeNil())
			}
		})
	}
}

func TestEnsureClusterNetworkOperatorSpec(t *testing.T) {
	t.Run("When OperatorConfiguration is nil, it should initialize both OperatorConfiguration and ClusterNetworkOperator", func(t *testing.T) {
		g := NewWithT(t)
		cluster := &hyperv1.HostedCluster{}

		ensureClusterNetworkOperatorSpec(cluster)

		g.Expect(cluster.Spec.OperatorConfiguration).NotTo(BeNil())
		g.Expect(cluster.Spec.OperatorConfiguration.ClusterNetworkOperator).NotTo(BeNil())
	})

	t.Run("When OperatorConfiguration exists but ClusterNetworkOperator is nil, it should initialize ClusterNetworkOperator", func(t *testing.T) {
		g := NewWithT(t)
		cluster := &hyperv1.HostedCluster{
			Spec: hyperv1.HostedClusterSpec{
				OperatorConfiguration: &hyperv1.OperatorConfiguration{},
			},
		}

		ensureClusterNetworkOperatorSpec(cluster)

		g.Expect(cluster.Spec.OperatorConfiguration.ClusterNetworkOperator).NotTo(BeNil())
	})
}
