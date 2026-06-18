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

	"github.com/coreos/stream-metadata-go/stream"
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
			name: "basic additional port",
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
		streamMeta    *stream.Stream
		expectedURL   string
		expectedHash  string
		expectedError bool
	}{
		{
			name:          "nil stream metadata",
			streamMeta:    nil,
			expectedError: true,
		},
		{
			name: "valid metadata",
			streamMeta: &stream.Stream{
				Architectures: map[string]stream.Arch{
					"x86_64": {
						Artifacts: map[string]stream.PlatformArtifacts{
							"openstack": {
								Formats: map[string]stream.ImageFormat{
									"qcow2.gz": {
										Disk: &stream.Artifact{
											Location: "https://example.com/image.qcow2.gz",
											Sha256:   "abcdef1234567890",
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
			streamMeta:    &stream.Stream{Architectures: map[string]stream.Arch{}},
			expectedError: true,
		},
		{
			name: "missing openstack artifact",
			streamMeta: &stream.Stream{
				Architectures: map[string]stream.Arch{
					"x86_64": {Artifacts: map[string]stream.PlatformArtifacts{}},
				},
			},
			expectedError: true,
		},
		{
			name: "missing qcow2.gz format",
			streamMeta: &stream.Stream{
				Architectures: map[string]stream.Arch{
					"x86_64": {
						Artifacts: map[string]stream.PlatformArtifacts{
							"openstack": {Formats: map[string]stream.ImageFormat{}},
						},
					},
				},
			},
			expectedError: true,
		},
		{
			name: "missing disk artifact",
			streamMeta: &stream.Stream{
				Architectures: map[string]stream.Arch{
					"x86_64": {
						Artifacts: map[string]stream.PlatformArtifacts{
							"openstack": {
								Formats: map[string]stream.ImageFormat{
									"qcow2.gz": {},
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
			url, hash, err := OpenstackDefaultImage(tc.streamMeta)
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
		streamMeta     *stream.Stream
		expectedResult string
		expectedError  bool
	}{
		{
			name:          "nil stream metadata",
			streamMeta:    nil,
			expectedError: true,
		},
		{
			name: "valid metadata",
			streamMeta: &stream.Stream{
				Architectures: map[string]stream.Arch{
					"x86_64": {
						Artifacts: map[string]stream.PlatformArtifacts{
							"openstack": {
								Release: "4.9.0",
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
			streamMeta:    &stream.Stream{Architectures: map[string]stream.Arch{}},
			expectedError: true,
		},
		{
			name: "missing openstack artifact",
			streamMeta: &stream.Stream{
				Architectures: map[string]stream.Arch{
					"x86_64": {Artifacts: map[string]stream.PlatformArtifacts{}},
				},
			},
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := OpenStackReleaseImage(tc.streamMeta)
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
			name: "valid configuration",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster",
				},
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
				StreamMetadata: &stream.Stream{
					Architectures: map[string]stream.Arch{
						"x86_64": {
							Artifacts: map[string]stream.PlatformArtifacts{
								"openstack": {
									Release: "4.9.0",
									Formats: map[string]stream.ImageFormat{
										"qcow2.gz": {
											Disk: &stream.Artifact{
												Location: "https://example.com/image.qcow2.gz",
												Sha256:   "abcdef1234567890",
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
					Name: "test-cluster-rhcos-4.9.0",
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
			},
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
						},
					},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &stream.Stream{
					Architectures: map[string]stream.Arch{},
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

func TestClusterImageName(t *testing.T) {
	testCases := []struct {
		name           string
		hostedCluster  *hyperv1.HostedCluster
		streamMeta     *stream.Stream
		expectedResult string
		expectedError  bool
	}{
		{
			name: "nil stream metadata",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
			},
			streamMeta:    nil,
			expectedError: true,
		},
		{
			name: "valid release image",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
			},
			streamMeta: &stream.Stream{
				Architectures: map[string]stream.Arch{
					"x86_64": {
						Artifacts: map[string]stream.PlatformArtifacts{
							"openstack": {
								Release: "4.19.0",
							},
						},
					},
				},
			},
			expectedResult: "test-cluster-rhcos-4.19.0",
			expectedError:  false,
		},
		{
			name: "missing architecture",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
			},
			streamMeta: &stream.Stream{
				Architectures: map[string]stream.Arch{},
			},
			expectedError: true,
		},
		{
			name: "missing openstack artifact",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
			},
			streamMeta: &stream.Stream{
				Architectures: map[string]stream.Arch{
					"x86_64": {
						Artifacts: map[string]stream.PlatformArtifacts{},
					},
				},
			},
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := PrefixedClusterImageName(tc.hostedCluster, tc.streamMeta)
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
