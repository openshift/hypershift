package openstack

import (
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	capiopenstackv1beta1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"

	"github.com/google/go-cmp/cmp"
	orc "github.com/k-orc/openstack-resource-controller/api/v1alpha1"
)

const flavor = "m1.xlarge"
const imageName = "rhcos"

func TestOpenStackMachineTemplate(t *testing.T) {
	testCases := []struct {
		name                string
		nodePool            hyperv1.NodePoolSpec
		nodePoolAnnotations map[string]string
		expected            *capiopenstackv1beta1.OpenStackMachineTemplateSpec
		checkError          func(*testing.T, error)
	}{
		{
			name: "basic valid node pool",
			nodePool: hyperv1.NodePoolSpec{
				ClusterName: "",
				Replicas:    nil,
				Config:      nil,
				Management:  hyperv1.NodePoolManagement{},
				AutoScaling: nil,
				Platform: hyperv1.NodePoolPlatform{
					Type: hyperv1.OpenStackPlatform,
					OpenStack: &hyperv1.OpenStackNodePoolPlatform{
						Flavor:    flavor,
						ImageName: imageName,
					},
				},
				Release: hyperv1.Release{},
			},

			expected: &capiopenstackv1beta1.OpenStackMachineTemplateSpec{
				Template: capiopenstackv1beta1.OpenStackMachineTemplateResource{
					Spec: capiopenstackv1beta1.OpenStackMachineSpec{
						Flavor: ptr.To(flavor),
						Image: capiopenstackv1beta1.ImageParam{
							Filter: &capiopenstackv1beta1.ImageFilter{
								Name: ptr.To(imageName),
							},
						},
					},
				},
			},
		},
		{
			name: "additional port for SR-IOV",
			nodePool: hyperv1.NodePoolSpec{
				ClusterName: "",
				Replicas:    nil,
				Config:      nil,
				Management:  hyperv1.NodePoolManagement{},
				AutoScaling: nil,
				Platform: hyperv1.NodePoolPlatform{
					Type: hyperv1.OpenStackPlatform,
					OpenStack: &hyperv1.OpenStackNodePoolPlatform{
						Flavor:    flavor,
						ImageName: imageName,
						AdditionalPorts: []hyperv1.PortSpec{
							{
								Network: &hyperv1.NetworkParam{
									ID: ptr.To("123"),
								},
								VNICType:           "direct",
								PortSecurityPolicy: hyperv1.PortSecurityDisabled,
							},
						},
					},
				},
				Release: hyperv1.Release{},
			},

			expected: &capiopenstackv1beta1.OpenStackMachineTemplateSpec{
				Template: capiopenstackv1beta1.OpenStackMachineTemplateResource{
					Spec: capiopenstackv1beta1.OpenStackMachineSpec{
						Flavor: ptr.To(flavor),
						Image: capiopenstackv1beta1.ImageParam{
							Filter: &capiopenstackv1beta1.ImageFilter{
								Name: ptr.To(imageName),
							},
						},
						Ports: []capiopenstackv1beta1.PortOpts{
							{},
							{
								Description: ptr.To("Additional port for Hypershift node pool tests"),
								Network: &capiopenstackv1beta1.NetworkParam{
									ID: ptr.To("123"),
								},
								ResolvedPortSpecFields: capiopenstackv1beta1.ResolvedPortSpecFields{
									DisablePortSecurity: ptr.To(true),
									VNICType:            ptr.To("direct"),
								},
							},
						},
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.nodePool.Platform.OpenStack == nil {
				tc.nodePool.Platform.OpenStack = &hyperv1.OpenStackNodePoolPlatform{}
			}
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						OpenStack: &hyperv1.OpenStackPlatformSpec{},
					},
					InfraID: "123",
				},
			}
			result, err := MachineTemplateSpec(
				hc,
				&hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name: "tests",
					},
					Spec: tc.nodePool,
				}, &releaseinfo.ReleaseImage{})
			if tc.checkError != nil {
				tc.checkError(t, err)
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
			if tc.expected == nil {
				return
			}
			if !equality.Semantic.DeepEqual(tc.expected, result) {
				t.Error(cmp.Diff(tc.expected, result))
			}
		})
	}
}
func TestOpenstackDefaultImage(t *testing.T) {
	testCases := []struct {
		name          string
		releaseImage  *releaseinfo.ReleaseImage
		expectedURL   string
		expectedHash  string
		expectedError bool
	}{
		{
			name: "valid metadata",
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {
							Artifacts: map[string]releaseinfo.CoreOSArtifact{
								"openstack": {
									Formats: map[string]map[string]releaseinfo.CoreOSFormat{
										"qcow2.gz": {
											"disk": {
												Location: "https://example.com/image.qcow2.gz",
												SHA256:   "abcdef1234567890",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedURL:   "https://example.com/image.qcow2.gz",
			expectedHash:  "abcdef1234567890",
			expectedError: false,
		},
		{
			name:          "missing architecture",
			releaseImage:  &releaseinfo.ReleaseImage{StreamMetadata: &releaseinfo.CoreOSStreamMetadata{Architectures: map[string]releaseinfo.CoreOSArchitecture{}}},
			expectedError: true,
		},
		{
			name: "missing openstack artifact",
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {Artifacts: map[string]releaseinfo.CoreOSArtifact{}},
					},
				},
			},
			expectedError: true,
		},
		{
			name: "missing qcow2.gz format",
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {
							Artifacts: map[string]releaseinfo.CoreOSArtifact{
								"openstack": {Formats: map[string]map[string]releaseinfo.CoreOSFormat{}},
							},
						},
					},
				},
			},
			expectedError: true,
		},
		{
			name: "missing disk artifact",
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {
							Artifacts: map[string]releaseinfo.CoreOSArtifact{
								"openstack": {
									Formats: map[string]map[string]releaseinfo.CoreOSFormat{
										"qcow2.gz": {},
									},
								},
							},
						},
					},
				},
			},
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			url, hash, err := OpenstackDefaultImage(tc.releaseImage)
			if tc.expectedError {
				if err == nil {
					t.Error("expected error but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if url != tc.expectedURL {
					t.Errorf("expected URL %q but got %q", tc.expectedURL, url)
				}
				if hash != tc.expectedHash {
					t.Errorf("expected hash %q but got %q", tc.expectedHash, hash)
				}
			}
		})
	}
}

func TestOpenStackReleaseImage(t *testing.T) {
	testCases := []struct {
		name           string
		releaseImage   *releaseinfo.ReleaseImage
		expectedResult string
		expectedError  bool
	}{
		{
			name: "valid metadata",
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {
							Artifacts: map[string]releaseinfo.CoreOSArtifact{
								"openstack": {
									Release: "4.9.0",
								},
							},
						},
					},
				},
			},
			expectedResult: "4.9.0",
			expectedError:  false,
		},
		{
			name:          "missing architecture",
			releaseImage:  &releaseinfo.ReleaseImage{StreamMetadata: &releaseinfo.CoreOSStreamMetadata{Architectures: map[string]releaseinfo.CoreOSArchitecture{}}},
			expectedError: true,
		},
		{
			name: "missing openstack artifact",
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {Artifacts: map[string]releaseinfo.CoreOSArtifact{}},
					},
				},
			},
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := OpenStackReleaseImage(tc.releaseImage)
			if tc.expectedError {
				if err == nil {
					t.Error("expected error but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if result != tc.expectedResult {
					t.Errorf("expected result %q but got %q", tc.expectedResult, result)
				}
			}
		})
	}
}
func TestReconcileOpenStackImageSpec(t *testing.T) {
	testCases := []struct {
		name                   string
		hostedCluster          *hyperv1.HostedCluster
		releaseImage           *releaseinfo.ReleaseImage
		expectedImageSpec      *orc.ImageSpec
		expectedError          bool
		expectedErrorSubstring string
	}{
		{
			name: "valid configuration with no retention policy",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						OpenStack: &hyperv1.OpenStackPlatformSpec{
							IdentityRef: hyperv1.OpenStackIdentityReference{
								Name:      "test-secret",
								CloudName: "test-cloud",
							},
						},
					},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {
							Artifacts: map[string]releaseinfo.CoreOSArtifact{
								"openstack": {
									Release: "4.9.0",
									Formats: map[string]map[string]releaseinfo.CoreOSFormat{
										"qcow2.gz": {
											"disk": {
												Location: "https://example.com/image.qcow2.gz",
												SHA256:   "abcdef1234567890",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedImageSpec: &orc.ImageSpec{
				CloudCredentialsRef: orc.CloudCredentialsReference{
					SecretName: "test-secret",
					CloudName:  "test-cloud",
				},
				Resource: &orc.ImageResourceSpec{
					Name: "rhcos-4.9.0",
					Content: &orc.ImageContent{
						ContainerFormat: "bare",
						DiskFormat:      "qcow2",
						Download: &orc.ImageContentSourceDownload{
							URL:        "https://example.com/image.qcow2.gz",
							Decompress: ptr.To(orc.ImageCompressionGZ),
							Hash: &orc.ImageHash{
								Algorithm: "sha256",
								Value:     "abcdef1234567890",
							},
						},
					},
				},
				ManagedOptions: &orc.ManagedOptions{OnDelete: orc.OnDeleteDelete},
			},
		},
		{
			name: "valid configuration with orphan retention policy",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						OpenStack: &hyperv1.OpenStackPlatformSpec{
							IdentityRef: hyperv1.OpenStackIdentityReference{
								Name:      "test-secret",
								CloudName: "test-cloud",
							},
							ImageRetentionPolicy: hyperv1.OrphanRetentionPolicy,
						},
					},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {
							Artifacts: map[string]releaseinfo.CoreOSArtifact{
								"openstack": {
									Release: "4.9.0",
									Formats: map[string]map[string]releaseinfo.CoreOSFormat{
										"qcow2.gz": {
											"disk": {
												Location: "https://example.com/image.qcow2.gz",
												SHA256:   "abcdef1234567890",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedImageSpec: &orc.ImageSpec{
				CloudCredentialsRef: orc.CloudCredentialsReference{
					SecretName: "test-secret",
					CloudName:  "test-cloud",
				},
				Resource: &orc.ImageResourceSpec{
					Name: "rhcos-4.9.0",
					Content: &orc.ImageContent{
						ContainerFormat: "bare",
						DiskFormat:      "qcow2",
						Download: &orc.ImageContentSourceDownload{
							URL:        "https://example.com/image.qcow2.gz",
							Decompress: ptr.To(orc.ImageCompressionGZ),
							Hash: &orc.ImageHash{
								Algorithm: "sha256",
								Value:     "abcdef1234567890",
							},
						},
					},
				},
				ManagedOptions: &orc.ManagedOptions{OnDelete: orc.OnDeleteDetach},
			},
		},
		{
			name: "valid configuration with prune retention policy",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						OpenStack: &hyperv1.OpenStackPlatformSpec{
							IdentityRef: hyperv1.OpenStackIdentityReference{
								Name:      "test-secret",
								CloudName: "test-cloud",
							},
							ImageRetentionPolicy: hyperv1.PruneRetentionPolicy,
						},
					},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {
							Artifacts: map[string]releaseinfo.CoreOSArtifact{
								"openstack": {
									Release: "4.9.0",
									Formats: map[string]map[string]releaseinfo.CoreOSFormat{
										"qcow2.gz": {
											"disk": {
												Location: "https://example.com/image.qcow2.gz",
												SHA256:   "abcdef1234567890",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedImageSpec: &orc.ImageSpec{
				CloudCredentialsRef: orc.CloudCredentialsReference{
					SecretName: "test-secret",
					CloudName:  "test-cloud",
				},
				Resource: &orc.ImageResourceSpec{
					Name: "rhcos-4.9.0",
					Content: &orc.ImageContent{
						ContainerFormat: "bare",
						DiskFormat:      "qcow2",
						Download: &orc.ImageContentSourceDownload{
							URL:        "https://example.com/image.qcow2.gz",
							Decompress: ptr.To(orc.ImageCompressionGZ),
							Hash: &orc.ImageHash{
								Algorithm: "sha256",
								Value:     "abcdef1234567890",
							},
						},
					},
				},
				ManagedOptions: &orc.ManagedOptions{OnDelete: orc.OnDeleteDelete},
			},
		},
		{
			name: "invalid retention policy",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						OpenStack: &hyperv1.OpenStackPlatformSpec{
							IdentityRef: hyperv1.OpenStackIdentityReference{
								Name:      "test-secret",
								CloudName: "test-cloud",
							},
							ImageRetentionPolicy: "invalid",
						},
					},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {
							Artifacts: map[string]releaseinfo.CoreOSArtifact{
								"openstack": {
									Release: "4.9.0",
									Formats: map[string]map[string]releaseinfo.CoreOSFormat{
										"qcow2.gz": {
											"disk": {
												Location: "https://example.com/image.qcow2.gz",
												SHA256:   "abcdef1234567890",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedError:          true,
			expectedErrorSubstring: "unsupported image retention policy",
		},
		{
			name: "release image error",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						OpenStack: &hyperv1.OpenStackPlatformSpec{
							IdentityRef: hyperv1.OpenStackIdentityReference{
								Name:      "test-secret",
								CloudName: "test-cloud",
							},
							ImageRetentionPolicy: hyperv1.PruneRetentionPolicy,
						},
					},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						// Missing x86_64 architecture data will cause the OpenstackDefaultImage to fail
					},
				},
			},
			expectedError:          true,
			expectedErrorSubstring: "couldn't find OS metadata for architecture",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			imageSpec := &orc.ImageSpec{}
			err := ReconcileOpenStackImageSpec(tc.hostedCluster, imageSpec, tc.releaseImage)

			if tc.expectedError {
				if err == nil {
					t.Error("expected error but got nil")
				} else if tc.expectedErrorSubstring != "" && !strings.Contains(err.Error(), tc.expectedErrorSubstring) {
					t.Errorf("expected error containing %q but got %q", tc.expectedErrorSubstring, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if tc.expectedImageSpec != nil {
				if !equality.Semantic.DeepEqual(tc.expectedImageSpec, imageSpec) {
					t.Error(cmp.Diff(tc.expectedImageSpec, imageSpec))
				}
			}
		})
	}
}
