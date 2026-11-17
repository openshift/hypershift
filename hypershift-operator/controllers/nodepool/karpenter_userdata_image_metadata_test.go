package nodepool

import (
	"maps"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/releaseinfo/testutils"

	imageapi "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/coreos/stream-metadata-go/stream"
	"github.com/coreos/stream-metadata-go/stream/rhcos"
	"github.com/go-logr/logr/testr"
)

func TestSetKarpenterAMILabels(t *testing.T) {
	testCases := []struct {
		name           string
		userDataSecret *corev1.Secret
		releaseImage   *releaseinfo.ReleaseImage
		region         string
		rhelStream     string
		expectedError  string
		expectedLabels map[string]string
	}{
		{
			name:   "When the user data secret is created for supported platform and architecture, it should set the expected labels",
			region: "us-east-1",
			userDataSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-data-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "true",
					},
				},
			},
			expectedLabels: map[string]string{
				karpenterutil.ArchToImageMetadataKey(hyperv1.ArchitectureAMD64, hyperkarpenterv1.UserDataAMILabel): "us-east-1-x86_64-image",
				karpenterutil.ArchToImageMetadataKey(hyperv1.ArchitectureARM64, hyperkarpenterv1.UserDataAMILabel): "us-east-1-aarch64-image",
			},
		},
		{
			name:   "When the AMI is unavailable for all architectures, it should return an error",
			region: "us-west-2",
			userDataSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-data-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "true",
					},
				},
			},
			expectedError: "no supported architectures found",
		},
		{
			name:   "When one architecture AMI is unavailable, it should set the label only for the available one",
			region: "us-east-1",
			userDataSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-data-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "true",
					},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{Name: "test-release"},
				},
				StreamMetadata: testAWSStream("x86_64", "us-east-1", "ami-amd64-only"),
			},
			expectedLabels: map[string]string{
				karpenterutil.ArchToImageMetadataKey(hyperv1.ArchitectureAMD64, hyperkarpenterv1.UserDataAMILabel): "ami-amd64-only",
			},
		},
		{
			name:       "When rhelStream is rhel-9 with single-stream payload, it should set AMI labels from StreamMetadata fallback",
			region:     "us-east-1",
			rhelStream: "rhel-9",
			userDataSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-data-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "true",
					},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{Name: "4.17.0"},
				},
				StreamMetadata: &stream.Stream{
					Architectures: map[string]stream.Arch{
						"x86_64": {
							Images: stream.Images{
								Aws: &stream.AwsImage{
									Regions: map[string]stream.SingleImage{
										"us-east-1": {Image: "ami-rhel9-fallback-amd64"},
									},
								},
							},
						},
						"aarch64": {
							Images: stream.Images{
								Aws: &stream.AwsImage{
									Regions: map[string]stream.SingleImage{
										"us-east-1": {Image: "ami-rhel9-fallback-arm64"},
									},
								},
							},
						},
					},
				},
				OSStreams: nil,
			},
			expectedLabels: map[string]string{
				karpenterutil.ArchToImageMetadataKey(hyperv1.ArchitectureAMD64, hyperkarpenterv1.UserDataAMILabel): "ami-rhel9-fallback-amd64",
				karpenterutil.ArchToImageMetadataKey(hyperv1.ArchitectureARM64, hyperkarpenterv1.UserDataAMILabel): "ami-rhel9-fallback-arm64",
			},
		},
		{
			name:       "When rhelStream is rhel-9 with multi-stream payload, it should use OSStreams rhel-9 AMI",
			region:     "us-east-1",
			rhelStream: "rhel-9",
			userDataSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-data-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "true",
					},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"},
				},
				StreamMetadata: testAWSStream("x86_64", "us-east-1", "ami-default-amd64"),
				OSStreams: map[string]*stream.Stream{
					"rhel-9": testAWSStream("x86_64", "us-east-1", "ami-rhel9-osstream-amd64"),
				},
			},
			expectedLabels: map[string]string{
				karpenterutil.ArchToImageMetadataKey(hyperv1.ArchitectureAMD64, hyperkarpenterv1.UserDataAMILabel): "ami-rhel9-osstream-amd64",
			},
		},
		{
			name:       "When rhelStream is rhel-10 with multi-stream payload, it should use OSStreams rhel-10 AMI",
			region:     "us-east-1",
			rhelStream: "rhel-10",
			userDataSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-data-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "true",
					},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"},
				},
				StreamMetadata: testAWSStream("x86_64", "us-east-1", "ami-default-amd64"),
				OSStreams: map[string]*stream.Stream{
					"rhel-10": testAWSStream("x86_64", "us-east-1", "ami-rhel10-osstream-amd64"),
				},
			},
			expectedLabels: map[string]string{
				karpenterutil.ArchToImageMetadataKey(hyperv1.ArchitectureAMD64, hyperkarpenterv1.UserDataAMILabel): "ami-rhel10-osstream-amd64",
			},
		},
	}
	log := testr.New(t)
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ri := tc.releaseImage
			if ri == nil {
				ri = testutils.InitReleaseImageOrDie("test-release")
			}
			err := setKarpenterAMILabels(log, tc.userDataSecret, tc.region, ri, tc.rhelStream)
			if tc.expectedError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(tc.expectedError))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			for labelKey, expectedAMI := range tc.expectedLabels {
				g.Expect(tc.userDataSecret.Labels).To(HaveKeyWithValue(labelKey, expectedAMI))
			}
			amdKey := karpenterutil.ArchToImageMetadataKey(hyperv1.ArchitectureAMD64, hyperkarpenterv1.UserDataAMILabel)
			armKey := karpenterutil.ArchToImageMetadataKey(hyperv1.ArchitectureARM64, hyperkarpenterv1.UserDataAMILabel)
			if _, ok := tc.expectedLabels[amdKey]; !ok {
				g.Expect(tc.userDataSecret.Labels).NotTo(HaveKey(amdKey))
			}
			if _, ok := tc.expectedLabels[armKey]; !ok {
				g.Expect(tc.userDataSecret.Labels).NotTo(HaveKey(armKey))
			}
		})
	}
}

func TestSetKarpenterAzureMarketplaceAnnotations(t *testing.T) {
	amd64Image := rhcos.AzureMarketplaceImage{
		Publisher: "azureopenshift",
		Offer:     "aro4",
		SKU:       "420-v2",
		Version:   "420.9.20250701",
	}
	arm64Image := rhcos.AzureMarketplaceImage{
		Publisher: "azureopenshift",
		Offer:     "aro4",
		SKU:       "420-arm",
		Version:   "420.9.20250702",
	}
	newArchitecture := func(image rhcos.AzureMarketplaceImage) stream.Arch {
		return stream.Arch{
			RHELCoreOSExtensions: &rhcos.Extensions{
				Marketplace: &rhcos.Marketplace{
					Azure: &rhcos.AzureMarketplace{
						NoPurchasePlan: &rhcos.AzureMarketplaceImages{
							Gen1: &rhcos.AzureMarketplaceImage{
								Publisher: "azureopenshift",
								Offer:     "aro4",
								SKU:       "aro_420",
								Version:   "420.9.20250601",
							},
							Gen2: &image,
						},
					},
				},
			},
		}
	}
	newReleaseImage := func() *releaseinfo.ReleaseImage {
		return &releaseinfo.ReleaseImage{
			ImageStream: &imageapi.ImageStream{
				ObjectMeta: metav1.ObjectMeta{Name: "4.20.0"},
			},
			StreamMetadata: &stream.Stream{
				Architectures: map[string]stream.Arch{
					"x86_64":  newArchitecture(amd64Image),
					"aarch64": newArchitecture(arm64Image),
				},
			},
		}
	}
	amd64Annotations := map[string]string{
		"hypershift.openshift.io/azure-marketplace-publisher": "azureopenshift",
		"hypershift.openshift.io/azure-marketplace-offer":     "aro4",
		"hypershift.openshift.io/azure-marketplace-sku":       "420-v2",
		"hypershift.openshift.io/azure-marketplace-version":   "420.9.20250701",
	}
	arm64Annotations := map[string]string{
		"hypershift.openshift.io/azure-marketplace-publisher-arm64": "azureopenshift",
		"hypershift.openshift.io/azure-marketplace-offer-arm64":     "aro4",
		"hypershift.openshift.io/azure-marketplace-sku-arm64":       "420-arm",
		"hypershift.openshift.io/azure-marketplace-version-arm64":   "420.9.20250702",
	}
	bothAnnotations := maps.Clone(amd64Annotations)
	maps.Copy(bothAnnotations, arm64Annotations)
	namedStreamAnnotations := maps.Clone(amd64Annotations)
	namedStreamAnnotations["hypershift.openshift.io/azure-marketplace-version"] = "420.9.20250801"
	staleAnnotations := maps.Clone(bothAnnotations)
	for key := range staleAnnotations {
		staleAnnotations[key] = "stale"
	}
	staleAnnotations["example.com/owner"] = "preserved"
	updatedAnnotations := maps.Clone(staleAnnotations)
	maps.Copy(updatedAnnotations, amd64Annotations)

	testCases := map[string]struct {
		prepareRelease      func(*releaseinfo.ReleaseImage)
		rhelStream          string
		initialAnnotations  map[string]string
		expectedAnnotations map[string]string
		expectedError       string
	}{
		"When both architectures and image generations are available, it should publish only Gen2 images with architecture-specific keys": {
			expectedAnnotations: bothAnnotations,
		},
		"When only amd64 is available, it should publish only unsuffixed annotations": {
			prepareRelease: func(release *releaseinfo.ReleaseImage) {
				delete(release.StreamMetadata.Architectures, "aarch64")
			},
			expectedAnnotations: amd64Annotations,
		},
		"When only arm64 is available, it should publish only arm64-suffixed annotations": {
			prepareRelease: func(release *releaseinfo.ReleaseImage) {
				delete(release.StreamMetadata.Architectures, "x86_64")
			},
			expectedAnnotations: arm64Annotations,
		},
		"When neither architecture is available, it should return an error without modifying the Secret": {
			prepareRelease: func(release *releaseinfo.ReleaseImage) {
				release.StreamMetadata.Architectures = nil
			},
			expectedError: "no Azure Marketplace Gen2 images found",
		},
		"When only Gen1 images are available, it should return an error without modifying existing annotations": {
			prepareRelease: func(release *releaseinfo.ReleaseImage) {
				for _, arch := range release.StreamMetadata.Architectures {
					arch.RHELCoreOSExtensions.Marketplace.Azure.NoPurchasePlan.Gen2 = nil
				}
			},
			initialAnnotations: staleAnnotations,
			expectedError:      "no Azure Marketplace Gen2 images found",
		},
		"When arm64 has no RHCOS extensions, it should publish only the complete amd64 image": {
			prepareRelease: func(release *releaseinfo.ReleaseImage) {
				release.StreamMetadata.Architectures["aarch64"] = stream.Arch{}
			},
			expectedAnnotations: amd64Annotations,
		},
		"When default stream metadata is missing, it should return the stream error without modifying the Secret": {
			prepareRelease: func(release *releaseinfo.ReleaseImage) {
				release.StreamMetadata = nil
			},
			initialAnnotations: staleAnnotations,
			expectedError:      "no default stream metadata available",
		},
		"When a stream name is supplied for a legacy payload, it should use the single-stream fallback": {
			rhelStream:          "rhel-9",
			expectedAnnotations: bothAnnotations,
		},
		"When a named stream is selected, it should publish that stream's image instead of the default image": {
			prepareRelease: func(release *releaseinfo.ReleaseImage) {
				image := amd64Image
				image.Version = "420.9.20250801"
				release.OSStreams = map[string]*stream.Stream{
					"rhel-9": {
						Architectures: map[string]stream.Arch{
							"x86_64": newArchitecture(image),
						},
					},
				}
			},
			rhelStream:          "rhel-9",
			expectedAnnotations: namedStreamAnnotations,
		},
		"When the named stream is missing from a multi-stream payload, it should return an error without falling back": {
			prepareRelease: func(release *releaseinfo.ReleaseImage) {
				release.OSStreams = map[string]*stream.Stream{"rhel-9": release.StreamMetadata}
			},
			rhelStream:         "rhel-10",
			initialAnnotations: staleAnnotations,
			expectedError:      `stream "rhel-10" not found`,
		},
		"When arm64 becomes unavailable, it should refresh amd64 annotations and preserve arm64 and unrelated metadata": {
			prepareRelease: func(release *releaseinfo.ReleaseImage) {
				delete(release.StreamMetadata.Architectures, "aarch64")
			},
			initialAnnotations:  staleAnnotations,
			expectedAnnotations: updatedAnnotations,
		},
		"When the arm64 image has no publisher, it should omit the entire arm64 annotation group": {
			prepareRelease: func(release *releaseinfo.ReleaseImage) {
				release.StreamMetadata.Architectures["aarch64"].RHELCoreOSExtensions.Marketplace.Azure.NoPurchasePlan.Gen2.Publisher = ""
			},
			expectedAnnotations: amd64Annotations,
		},
		"When the arm64 image has no offer, it should omit the entire arm64 annotation group": {
			prepareRelease: func(release *releaseinfo.ReleaseImage) {
				release.StreamMetadata.Architectures["aarch64"].RHELCoreOSExtensions.Marketplace.Azure.NoPurchasePlan.Gen2.Offer = ""
			},
			expectedAnnotations: amd64Annotations,
		},
		"When the arm64 image has no SKU, it should omit the entire arm64 annotation group": {
			prepareRelease: func(release *releaseinfo.ReleaseImage) {
				release.StreamMetadata.Architectures["aarch64"].RHELCoreOSExtensions.Marketplace.Azure.NoPurchasePlan.Gen2.SKU = ""
			},
			expectedAnnotations: amd64Annotations,
		},
		"When the arm64 image has no version, it should preserve the entire existing arm64 annotation group": {
			prepareRelease: func(release *releaseinfo.ReleaseImage) {
				release.StreamMetadata.Architectures["aarch64"].RHELCoreOSExtensions.Marketplace.Azure.NoPurchasePlan.Gen2.Version = ""
			},
			initialAnnotations:  staleAnnotations,
			expectedAnnotations: updatedAnnotations,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			release := newReleaseImage()
			if tc.prepareRelease != nil {
				tc.prepareRelease(release)
			}
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "user-data-azure-karpenter",
					Namespace:   "clusters-azure",
					Annotations: maps.Clone(tc.initialAnnotations),
					Labels:      map[string]string{karpenterutil.ManagedByKarpenterLabel: "true"},
				},
				Data: map[string][]byte{"value": []byte(`{"ignition":{"version":"3.2.0"}}`)},
			}
			original := secret.DeepCopy()

			err := setKarpenterAzureMarketplaceAnnotations(testr.New(t), secret, release, tc.rhelStream)
			if tc.expectedError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedError))
				g.Expect(secret).To(Equal(original))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			expected := original.DeepCopy()
			expected.Annotations = tc.expectedAnnotations
			g.Expect(secret).To(Equal(expected))
		})
	}
}
