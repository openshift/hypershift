package nodepool

import (
	"fmt"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"

	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	"github.com/stretchr/testify/require"
)

func makeReleaseImage(region, release string) *releaseinfo.ReleaseImage {
	return &releaseinfo.ReleaseImage{
		StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
			Architectures: map[string]releaseinfo.CoreOSArchitecture{
				"ppc64le": {
					Images: releaseinfo.CoreOSImages{
						PowerVS: releaseinfo.CoreOSPowerVSImages{
							Regions: map[string]releaseinfo.CoreOSPowerVSImage{
								getImageRegion(region): {
									Release: release,
									Bucket:  "test-bucket",
									Object:  "test-object",
								},
							},
						},
					},
				},
			},
		},
	}
}

func makeHostedCluster(region, svcID, subnetID string) *hyperv1.HostedCluster {
	return &hyperv1.HostedCluster{
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				PowerVS: &hyperv1.PowerVSPlatformSpec{
					Region:            region,
					ServiceInstanceID: svcID,
					Subnet:            &hyperv1.PowerVSResourceReference{ID: ptr.To(subnetID)},
				},
			},
		},
	}
}

func makeNodePool(sysType, procCap string, memGiB int32, procType hyperv1.PowerVSNodePoolProcType, image *hyperv1.PowerVSResourceReference) *hyperv1.NodePool {
	return &hyperv1.NodePool{
		Spec: hyperv1.NodePoolSpec{
			Platform: hyperv1.NodePoolPlatform{
				PowerVS: &hyperv1.PowerVSNodePoolPlatform{
					SystemType:    sysType,
					ProcessorType: procType,
					Processors:    intstr.FromString(procCap),
					MemoryGiB:     int32(memGiB),
					Image:         image,
					StorageType:   "tier1",
				},
			},
		},
	}
}

func TestGetImageRegion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"us-south", "us-south", "us-south"},
		{"dal", "dal", "us-south"},
		{"lon", "lon", "eu-gb"},
		{"unknown", "xyz", defaultCOSRegion},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getImageRegion(tt.input)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestGetPowerVSImage(t *testing.T) {
	releaseImg := makeReleaseImage("us-south", "rhcos-4.15")

	tests := []struct {
		name        string
		region      string
		image       *releaseinfo.ReleaseImage
		wantRegion  string
		wantRelease string
		wantErr     bool
	}{
		{
			name:        "valid region",
			region:      "us-south",
			image:       releaseImg,
			wantRegion:  "us-south",
			wantRelease: "rhcos-4-15",
			wantErr:     false,
		},
		{
			name:        "missing architecture",
			region:      "us-south",
			image:       &releaseinfo.ReleaseImage{StreamMetadata: &releaseinfo.CoreOSStreamMetadata{}},
			wantRegion:  "",
			wantRelease: "",
			wantErr:     true,
		},
		{
			name:        "missing region in image",
			region:      "eu-de",
			image:       releaseImg,
			wantRegion:  "",
			wantRelease: "",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img, region, err := getPowerVSImage(tt.region, tt.image)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantRegion, region)
			require.Equal(t, tt.wantRelease, img.Release)
		})
	}
}

func Test_ibmPowerVSMachineTemplateSpec(t *testing.T) {
	tests := []struct {
		name        string
		hcluster    *hyperv1.HostedCluster
		nodePool    *hyperv1.NodePool
		release     *releaseinfo.ReleaseImage
		expectErr   bool
		expectImage bool
		expectRef   bool
	}{
		{
			name:        "Image provided explicitly",
			hcluster:    makeHostedCluster("us-south", "svc-123", "subnet-id"),
			nodePool:    makeNodePool("s922", "1", 8, hyperv1.PowerVSNodePoolSharedProcType, &hyperv1.PowerVSResourceReference{ID: ptr.To("img-id"), Name: ptr.To("img-name")}),
			release:     makeReleaseImage("us-south", "rhcos-412"),
			expectImage: true,
			expectRef:   false,
		},
		{
			name:     "Fallback to ImageRef when Image not provided",
			hcluster: makeHostedCluster("us-south", "svc-123", "subnet-id"),
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						PowerVS: &hyperv1.PowerVSNodePoolPlatform{
							SystemType:    "e980",
							ProcessorType: hyperv1.PowerVSNodePoolSharedProcType,
							Processors:    intstr.FromInt(1),
							MemoryGiB:     16,
						},
					},
				},
			},
			release:     makeReleaseImage("us-south", "rhcos-413"),
			expectImage: false,
			expectRef:   true,
		},
		{
			name:     "Error due to missing region data",
			hcluster: makeHostedCluster("invalid-region", "svc-123", "subnet-id"),
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						PowerVS: &hyperv1.PowerVSNodePoolPlatform{},
					},
				},
			},
			// release only has us-east, so lookup for "invalid-region" should fail
			release:   makeReleaseImage("us-east", "rhcos-414"),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := ibmPowerVSMachineTemplateSpec(tt.hcluster, tt.nodePool, tt.release)

			if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.expectImage {
				if spec.Template.Spec.Image == nil {
					t.Errorf("expected Image to be set but got nil")
				}
				if spec.Template.Spec.ImageRef != nil {
					t.Errorf("expected ImageRef to be nil but got %+v", spec.Template.Spec.ImageRef)
				}
			} else if tt.expectRef {
				if spec.Template.Spec.ImageRef == nil {
					t.Errorf("expected ImageRef to be set but got nil")
				}
				if spec.Template.Spec.Image != nil {
					t.Errorf("expected Image to be nil but got %+v", spec.Template.Spec.Image)
				}
			} else {
				if spec.Template.Spec.Image != nil || spec.Template.Spec.ImageRef != nil {
					t.Errorf("expected both Image and ImageRef to be nil but got Image=%+v, ImageRef=%+v",
						spec.Template.Spec.Image, spec.Template.Spec.ImageRef)
				}
			}
		})
	}
}

func Test_ibmPowerVSMachineTemplate(t *testing.T) {
	testCases := []struct {
		name                  string
		hcluster              *hyperv1.HostedCluster
		nodePool              *hyperv1.NodePool
		release               *releaseinfo.ReleaseImage
		templateNameGenerator func(any) (string, error)
		expectErr             bool
	}{
		{
			name:     "success - valid inputs produce template",
			hcluster: makeHostedCluster("us-south", "svc-123", "subnet-id"),
			nodePool: makeNodePool("s922", "1", 8, hyperv1.PowerVSNodePoolSharedProcType, &hyperv1.PowerVSResourceReference{ID: ptr.To("img-id"), Name: ptr.To("img-name")}),
			release:  makeReleaseImage("us-south", "ppc64le"),
			templateNameGenerator: func(spec any) (string, error) {
				return "valid-template", nil
			},
		},
		{
			name:     "error - release image missing",
			hcluster: makeHostedCluster("us-south", "svc-123", "subnet-id"),
			nodePool: makeNodePool("s922", "1", 8, hyperv1.PowerVSNodePoolSharedProcType, &hyperv1.PowerVSResourceReference{ID: ptr.To("img-id"), Name: ptr.To("img-name")}),
			release: &releaseinfo.ReleaseImage{
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"ppc64le": {},
					},
				},
			},
			templateNameGenerator: func(spec any) (string, error) {
				return "unused", nil
			},
			expectErr: true,
		},
		{
			name:     "error - template name generator fails",
			hcluster: makeHostedCluster("us-south", "svc-123", "subnet-id"),
			nodePool: makeNodePool("s922", "1", 8, hyperv1.PowerVSNodePoolSharedProcType, &hyperv1.PowerVSResourceReference{ID: ptr.To("img-id"), Name: ptr.To("img-name")}),
			release:  makeReleaseImage("us-south", "ppc64le"),
			templateNameGenerator: func(spec any) (string, error) {
				return "", fmt.Errorf("name generation failed")
			},
			expectErr: true,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			c := &CAPI{
				Token: &Token{
					ConfigGenerator: &ConfigGenerator{
						nodePool:      tt.nodePool,
						hostedCluster: tt.hcluster,
						rolloutConfig: &rolloutConfig{
							releaseImage: tt.release,
						},
					},
				},
			}

			template, err := c.ibmPowerVSMachineTemplate(tt.templateNameGenerator)

			if tt.expectErr {
				require.Error(t, err)
				require.Nil(t, template)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, template)
			require.Equal(t, "valid-template", template.Name)
			require.Equal(t, "svc-123", template.Spec.Template.Spec.ServiceInstanceID)
		})
	}
}
