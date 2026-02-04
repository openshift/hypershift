package nodepool

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"

	imageapi "github.com/openshift/api/image/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	capigcp "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
)

func TestGcpMachineTemplateSpec(t *testing.T) {
	testCases := []struct {
		name        string
		nodePool    *hyperv1.NodePool
		hc          *hyperv1.HostedCluster
		expectedErr bool
		validator   func(*testing.T, *capigcp.GCPMachineSpec)
	}{
		{
			name: "When NodePool has basic GCP configuration, it should create valid machine template spec",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPNodePoolPlatform{
							MachineType: "n1-standard-2",
							Zone:        "us-central1-a",
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "test-infra-id",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "test-project",
							Region:  "us-central1",
							NetworkConfig: hyperv1.GCPNetworkConfig{
								PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{
									Name: "test-psc-subnet",
								},
							},
						},
					},
				},
			},
			expectedErr: false,
			validator: func(t *testing.T, spec *capigcp.GCPMachineSpec) {
				g := NewWithT(t)
				g.Expect(spec.InstanceType).To(Equal("n1-standard-2"))
				g.Expect(*spec.Subnet).To(Equal("test-psc-subnet"))
				g.Expect(spec.RootDeviceSize).To(Equal(int64(64))) // Default size
				g.Expect(*spec.RootDeviceType).To(Equal(capigcp.DiskType("pd-balanced")))
				g.Expect(spec.Preemptible).To(BeFalse())
				g.Expect(*spec.OnHostMaintenance).To(Equal(capigcp.HostMaintenancePolicyMigrate))
			},
		},
		{
			name: "When NodePool has custom disk configuration, it should apply disk settings correctly",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPNodePoolPlatform{
							MachineType: "n1-standard-2",
							Zone:        "us-central1-a",
							BootDisk: &hyperv1.GCPBootDisk{
								DiskSizeGB: ptr.To[int64](100),
								DiskType:   ptr.To("pd-ssd"),
							},
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "test-infra-id",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "test-project",
							Region:  "us-central1",
							NetworkConfig: hyperv1.GCPNetworkConfig{
								PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{
									Name: "test-psc-subnet",
								},
							},
						},
					},
				},
			},
			expectedErr: false,
			validator: func(t *testing.T, spec *capigcp.GCPMachineSpec) {
				g := NewWithT(t)
				g.Expect(spec.RootDeviceSize).To(Equal(int64(100)))
				g.Expect(*spec.RootDeviceType).To(Equal(capigcp.DiskType("pd-ssd")))
			},
		},
		{
			name: "When NodePool has preemptible configuration, it should set maintenance policy to terminate",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPNodePoolPlatform{
							MachineType:       "n1-standard-2",
							Zone:              "us-central1-a",
							ProvisioningModel: ptr.To(hyperv1.GCPProvisioningModelPreemptible),
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "test-infra-id",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "test-project",
							Region:  "us-central1",
							NetworkConfig: hyperv1.GCPNetworkConfig{
								PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{
									Name: "test-psc-subnet",
								},
							},
						},
					},
				},
			},
			expectedErr: false,
			validator: func(t *testing.T, spec *capigcp.GCPMachineSpec) {
				g := NewWithT(t)
				g.Expect(spec.Preemptible).To(BeTrue())
				g.Expect(*spec.OnHostMaintenance).To(Equal(capigcp.HostMaintenancePolicyTerminate))
				// Preemptible uses the boolean field, not ProvisioningModel
				g.Expect(spec.ProvisioningModel).To(BeNil())
			},
		},
		{
			name: "When NodePool has Spot configuration, it should set CAPG ProvisioningModel to Spot",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPNodePoolPlatform{
							MachineType:       "n1-standard-2",
							Zone:              "us-central1-a",
							ProvisioningModel: ptr.To(hyperv1.GCPProvisioningModelSpot),
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "test-infra-id",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "test-project",
							Region:  "us-central1",
							NetworkConfig: hyperv1.GCPNetworkConfig{
								PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{
									Name: "test-psc-subnet",
								},
							},
						},
					},
				},
			},
			expectedErr: false,
			validator: func(t *testing.T, spec *capigcp.GCPMachineSpec) {
				g := NewWithT(t)
				g.Expect(spec.Preemptible).To(BeFalse())
				g.Expect(*spec.OnHostMaintenance).To(Equal(capigcp.HostMaintenancePolicyTerminate))
				// Spot uses the ProvisioningModel field
				g.Expect(spec.ProvisioningModel).ToNot(BeNil())
				g.Expect(*spec.ProvisioningModel).To(Equal(capigcp.ProvisioningModelSpot))
			},
		},
		{
			name: "When NodePool has custom image, it should use specified image over release metadata",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPNodePoolPlatform{
							MachineType: "n1-standard-2",
							Zone:        "us-central1-a",
							Image:       ptr.To("projects/my-project/global/images/custom-rhcos-image"),
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "test-infra-id",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "test-project",
							Region:  "us-central1",
							NetworkConfig: hyperv1.GCPNetworkConfig{
								PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{
									Name: "test-psc-subnet",
								},
							},
						},
					},
				},
			},
			expectedErr: false,
			validator: func(t *testing.T, spec *capigcp.GCPMachineSpec) {
				g := NewWithT(t)
				g.Expect(*spec.Image).To(Equal("projects/my-project/global/images/custom-rhcos-image"))
			},
		},
		{
			name: "When NodePool has resource labels and network tags, it should apply them correctly",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPNodePoolPlatform{
							MachineType: "n1-standard-2",
							Zone:        "us-central1-a",
							ResourceLabels: []hyperv1.GCPResourceLabel{
								{Key: "env", Value: ptr.To("test")},
								{Key: "team", Value: ptr.To("platform")},
							},
							NetworkTags: []string{"allow-ssh", "allow-internal"},
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "test-infra-id",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "test-project",
							Region:  "us-central1",
							NetworkConfig: hyperv1.GCPNetworkConfig{
								PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{
									Name: "test-psc-subnet",
								},
							},
							ResourceLabels: []hyperv1.GCPResourceLabel{
								{Key: "cluster", Value: ptr.To("test-cluster")},
							},
						},
					},
				},
			},
			expectedErr: false,
			validator: func(t *testing.T, spec *capigcp.GCPMachineSpec) {
				g := NewWithT(t)
				// Should have both cluster and nodepool labels
				g.Expect(spec.AdditionalLabels).To(HaveKeyWithValue("cluster", "test-cluster"))
				g.Expect(spec.AdditionalLabels).To(HaveKeyWithValue("env", "test"))
				g.Expect(spec.AdditionalLabels).To(HaveKeyWithValue("team", "platform"))

				// Should have user tags plus infra tag
				g.Expect(spec.AdditionalNetworkTags).To(ContainElements("allow-ssh", "allow-internal", "test-infra-id-worker"))
			},
		},
		{
			name: "When NodePool has encryption key configuration, it should configure disk encryption",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPNodePoolPlatform{
							MachineType: "n1-standard-2",
							Zone:        "us-central1-a",
							BootDisk: &hyperv1.GCPBootDisk{
								DiskSizeGB: ptr.To[int64](64),
								DiskType:   ptr.To("pd-standard"),
								EncryptionKey: &hyperv1.GCPDiskEncryptionKey{
									KMSKeyName: "projects/test-project/locations/us-central1/keyRings/test-ring/cryptoKeys/test-key",
								},
							},
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "test-infra-id",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "test-project",
							Region:  "us-central1",
							NetworkConfig: hyperv1.GCPNetworkConfig{
								PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{
									Name: "test-psc-subnet",
								},
							},
						},
					},
				},
			},
			expectedErr: false,
			validator: func(t *testing.T, spec *capigcp.GCPMachineSpec) {
				g := NewWithT(t)
				g.Expect(spec.RootDiskEncryptionKey).ToNot(BeNil())
				g.Expect(spec.RootDiskEncryptionKey.KeyType).To(Equal(capigcp.CustomerManagedKey))
				g.Expect(spec.RootDiskEncryptionKey.ManagedKey).ToNot(BeNil())
				g.Expect(spec.RootDiskEncryptionKey.ManagedKey.KMSKeyName).To(Equal("projects/test-project/locations/us-central1/keyRings/test-ring/cryptoKeys/test-key"))
			},
		},
		{
			name: "When NodePool has no service account configuration, it should use GCP default compute service account",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPNodePoolPlatform{
							MachineType: "n1-standard-2",
							Zone:        "us-central1-a",
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "test-infra-id",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "test-project",
							Region:  "us-central1",
							NetworkConfig: hyperv1.GCPNetworkConfig{
								PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{
									Name: "test-psc-subnet",
								},
							},
						},
					},
				},
			},
			expectedErr: false,
			validator: func(t *testing.T, spec *capigcp.GCPMachineSpec) {
				g := NewWithT(t)
				// When no service account is configured, should be nil (GCP uses default compute SA)
				g.Expect(spec.ServiceAccount).To(BeNil())
			},
		},
		{
			name: "When NodePool has service account configuration, it should configure service account correctly",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPNodePoolPlatform{
							MachineType: "n1-standard-2",
							Zone:        "us-central1-a",
							ServiceAccount: &hyperv1.GCPNodeServiceAccount{
								Email: ptr.To("test-nodepool@test-project.iam.gserviceaccount.com"),
								Scopes: []string{
									"https://www.googleapis.com/auth/cloud-platform",
								},
							},
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "test-infra-id",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "test-project",
							Region:  "us-central1",
							NetworkConfig: hyperv1.GCPNetworkConfig{
								PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{
									Name: "test-psc-subnet",
								},
							},
						},
					},
				},
			},
			expectedErr: false,
			validator: func(t *testing.T, spec *capigcp.GCPMachineSpec) {
				g := NewWithT(t)
				g.Expect(spec.ServiceAccount).ToNot(BeNil())
				g.Expect(spec.ServiceAccount.Email).To(Equal("test-nodepool@test-project.iam.gserviceaccount.com"))
				g.Expect(spec.ServiceAccount.Scopes).To(ContainElement("https://www.googleapis.com/auth/cloud-platform"))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			// Create a fake release image with GCP metadata
			releaseImage := &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{Name: "4.18.0"},
				},
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {
							Images: releaseinfo.CoreOSImages{
								GCP: releaseinfo.CoreOSGCPImage{
									Image: "projects/rhcos-cloud/global/images/rhcos-x86-64-418",
								},
							},
						},
					},
				},
			}

			spec, err := gcpMachineTemplateSpec(
				tc.hc.Spec.InfraID,
				tc.hc,
				tc.nodePool,
				releaseImage,
			)

			if tc.expectedErr {
				g.Expect(err).To(HaveOccurred())
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(spec).ToNot(BeNil())

			if tc.validator != nil {
				tc.validator(t, spec)
			}
		})
	}
}

func TestDefaultNodePoolGCPImage(t *testing.T) {
	testCases := []struct {
		name           string
		arch           string
		releaseImage   *releaseinfo.ReleaseImage
		expectedImage  string
		expectedErr    bool
		expectedErrMsg string
	}{
		{
			name: "When architecture is x86_64 with valid release metadata, it should return correct image",
			arch: hyperv1.ArchitectureAMD64,
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {
							Images: releaseinfo.CoreOSImages{
								GCP: releaseinfo.CoreOSGCPImage{
									Image: "projects/rhcos-cloud/global/images/rhcos-x86-64-418",
								},
							},
						},
					},
				},
			},
			expectedImage: "projects/rhcos-cloud/global/images/rhcos-x86-64-418",
			expectedErr:   false,
		},
		{
			name: "When architecture is aarch64 with valid release metadata, it should return correct image",
			arch: hyperv1.ArchitectureARM64,
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"aarch64": {
							Images: releaseinfo.CoreOSImages{
								GCP: releaseinfo.CoreOSGCPImage{
									Image: "projects/rhcos-cloud/global/images/rhcos-aarch64-418",
								},
							},
						},
					},
				},
			},
			expectedImage: "projects/rhcos-cloud/global/images/rhcos-aarch64-418",
			expectedErr:   false,
		},
		{
			name: "When architecture is not found in release metadata, it should return error",
			arch: "unsupported-arch",
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {
							Images: releaseinfo.CoreOSImages{
								GCP: releaseinfo.CoreOSGCPImage{
									Image: "projects/rhcos-cloud/global/images/rhcos-x86-64-418",
								},
							},
						},
					},
				},
			},
			expectedErr:    true,
			expectedErrMsg: "couldn't find OS metadata for architecture \"unsupported-arch\"",
		},
		{
			name: "When GCP image is empty in release metadata, it should return error",
			arch: hyperv1.ArchitectureAMD64,
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {
							Images: releaseinfo.CoreOSImages{
								GCP: releaseinfo.CoreOSGCPImage{
									Image: "", // Empty image
								},
							},
						},
					},
				},
			},
			expectedErr:    true,
			expectedErrMsg: "release image metadata has no GCP image for architecture \"amd64\"",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			image, err := defaultNodePoolGCPImage(tc.arch, tc.releaseImage)

			if tc.expectedErr {
				g.Expect(err).To(HaveOccurred())
				if tc.expectedErrMsg != "" {
					g.Expect(err.Error()).To(ContainSubstring(tc.expectedErrMsg))
				}
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(image).To(Equal(tc.expectedImage))
		})
	}
}

func TestConfigureGCPMaintenanceBehavior(t *testing.T) {
	testCases := []struct {
		name              string
		userMaintenance   *string
		provisioningModel *hyperv1.GCPProvisioningModel
		expectedBehavior  capigcp.HostMaintenancePolicy
	}{
		{
			name:              "When user specifies TERMINATE maintenance, it should return terminate policy",
			userMaintenance:   ptr.To("TERMINATE"),
			provisioningModel: ptr.To(hyperv1.GCPProvisioningModelStandard),
			expectedBehavior:  capigcp.HostMaintenancePolicyTerminate,
		},
		{
			name:              "When user specifies MIGRATE maintenance, it should return migrate policy",
			userMaintenance:   ptr.To("MIGRATE"),
			provisioningModel: ptr.To(hyperv1.GCPProvisioningModelStandard),
			expectedBehavior:  capigcp.HostMaintenancePolicyMigrate,
		},
		{
			name:              "When instance is preemptible with no user setting, it should return terminate policy",
			userMaintenance:   ptr.To(""),
			provisioningModel: ptr.To(hyperv1.GCPProvisioningModelPreemptible),
			expectedBehavior:  capigcp.HostMaintenancePolicyTerminate,
		},
		{
			name:              "When instance is Spot with no user setting, it should return terminate policy",
			userMaintenance:   ptr.To(""),
			provisioningModel: ptr.To(hyperv1.GCPProvisioningModelSpot),
			expectedBehavior:  capigcp.HostMaintenancePolicyTerminate,
		},
		{
			name:              "When instance is not preemptible with no user setting, it should return migrate policy",
			userMaintenance:   ptr.To(""),
			provisioningModel: ptr.To(hyperv1.GCPProvisioningModelStandard),
			expectedBehavior:  capigcp.HostMaintenancePolicyMigrate,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			result := configureGCPMaintenanceBehavior(tc.userMaintenance, tc.provisioningModel)
			g.Expect(result).To(Equal(tc.expectedBehavior))
		})
	}
}

func TestConfigureGCPNetworkTags(t *testing.T) {
	testCases := []struct {
		name         string
		userTags     []string
		infraID      string
		expectedTags []string
	}{
		{
			name:     "When user tags and infraID provided, it should combine them with worker suffix",
			userTags: []string{"custom-tag"},
			infraID:  "my-cluster-infra",
			expectedTags: []string{
				"custom-tag",
				"my-cluster-infra-worker",
			},
		},
		{
			name:     "When multiple user tags provided, it should include all with infra tag",
			userTags: []string{"tag1", "tag2"},
			infraID:  "my-cluster-infra",
			expectedTags: []string{
				"tag1",
				"tag2",
				"my-cluster-infra-worker",
			},
		},
		{
			name:         "When no user tags provided, it should only add infra tag",
			userTags:     nil,
			infraID:      "test-infra",
			expectedTags: []string{"test-infra-worker"},
		},
		{
			name:         "When infraID is empty, it should only include user tags",
			userTags:     []string{"user-tag"},
			infraID:      "",
			expectedTags: []string{"user-tag"},
		},
		{
			name:         "When both are empty, it should return nil or empty slice",
			userTags:     nil,
			infraID:      "",
			expectedTags: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			result := configureGCPNetworkTags(tc.userTags, tc.infraID)

			// Handle nil vs empty slice comparison
			if tc.expectedTags == nil {
				g.Expect(result).To(BeEmpty())
			} else {
				g.Expect(result).To(Equal(tc.expectedTags))
			}
		})
	}
}
