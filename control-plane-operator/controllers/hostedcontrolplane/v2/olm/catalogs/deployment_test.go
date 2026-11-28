package catalogs

import (
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func findContainer(name string, containers []corev1.Container) *corev1.Container {
	for i := range containers {
		if containers[i].Name == name {
			return &containers[i]
		}
	}
	return nil
}

func TestCheckCatalogImageOverrides(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		images           map[string]string
		expectedOverride bool
		expectedError    error
	}{
		{
			name: "When all images are empty, it should return false with no error",
			images: map[string]string{
				"redhat-operators":    "",
				"community-operators": "",
				"certified-operators": "",
			},
			expectedOverride: false,
			expectedError:    nil,
		},
		{
			name: "When all images are provided with sha256, it should return true with no error",
			images: map[string]string{
				"redhat-operators":    "registry.io/repo@sha256:abc123",
				"community-operators": "registry.io/repo@sha256:ghi789",
				"certified-operators": "registry.io/repo@sha256:jkl012",
			},
			expectedOverride: true,
			expectedError:    nil,
		},
		{
			name: "When image is provided without sha256, it should return error",
			images: map[string]string{
				"redhat-operators":    "registry.io/repo:latest",
				"community-operators": "",
				"certified-operators": "",
			},
			expectedOverride: false,
			expectedError:    errors.New("images for OLM catalogs should be referenced only by digest"),
		},
		{
			name: "When some images are missing, it should return error",
			images: map[string]string{
				"redhat-operators":    "registry.io/repo@sha256:abc123",
				"community-operators": "",
				"certified-operators": "",
			},
			expectedOverride: false,
			expectedError:    errors.New("if OLM catalog images are overridden, all the values for the 3 default catalogs should be provided"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			override, err := checkCatalogImageOverides(tc.images)

			g.Expect(override).To(Equal(tc.expectedOverride))
			if tc.expectedError != nil {
				g.Expect(err).To(MatchError(tc.expectedError.Error()))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestGetCatalogImagesOverrides(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                  string
		annotations           map[string]string
		capabilityImageStream bool
		validate              func(g Gomega, result map[string]string, err error)
	}{
		{
			name: "When all catalog annotations are set with sha256, it should return overrides",
			annotations: map[string]string{
				hyperv1.RedHatOperatorsCatalogImageAnnotation:    "registry.io/redhat@sha256:abc",
				hyperv1.CommunityOperatorsCatalogImageAnnotation: "registry.io/community@sha256:ghi",
				hyperv1.CertifiedOperatorsCatalogImageAnnotation: "registry.io/certified@sha256:jkl",
			},
			capabilityImageStream: false,
			validate: func(g Gomega, result map[string]string, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(result).To(HaveKeyWithValue("redhat-operators", "registry.io/redhat@sha256:abc"))
				g.Expect(result).To(HaveKeyWithValue("community-operators", "registry.io/community@sha256:ghi"))
				g.Expect(result).To(HaveKeyWithValue("certified-operators", "registry.io/certified@sha256:jkl"))
			},
		},
		{
			name: "When annotations are incomplete, it should return error",
			annotations: map[string]string{
				hyperv1.RedHatOperatorsCatalogImageAnnotation: "registry.io/redhat@sha256:abc",
			},
			capabilityImageStream: false,
			validate: func(g Gomega, result map[string]string, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("if OLM catalog images are overridden"))
			},
		},
		{
			name: "When annotations use tags instead of digests, it should return error",
			annotations: map[string]string{
				hyperv1.RedHatOperatorsCatalogImageAnnotation: "registry.io/redhat:latest",
			},
			capabilityImageStream: false,
			validate: func(g Gomega, result map[string]string, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("should be referenced only by digest"))
			},
		},
		{
			name:                  "When no annotations are set and capabilityImageStream is true, it should return nil",
			annotations:           map[string]string{},
			capabilityImageStream: true,
			validate: func(g Gomega, result map[string]string, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(result).To(BeNil())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-hcp",
					Namespace:   "test-namespace",
					Annotations: tc.annotations,
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			result, err := getCatalogImagesOverrides(cpContext, tc.capabilityImageStream)
			tc.validate(g, result, err)
		})
	}
}

// TestCatalogDeploymentManifests asserts that every catalog deployment asset rebuilds the opm
// cache from the extracted /configs instead of copying the (possibly stale) cache shipped inside
// the catalog image. Filtered catalog images produced by oc-mirror v2 ship an opm cache that no
// longer matches their filtered /configs (OCPBUGS-109376); copying that cache makes
// `opm serve --cache-dir` fail its integrity check with "cache requires rebuild".
func TestCatalogDeploymentManifests(t *testing.T) {
	t.Parallel()

	componentNames := []string{
		redhatOperatorsCatalogComponentName,
		communityOperatorsCatalogComponentName,
		certifiedOperatorsCatalogComponentName,
	}

	for _, componentName := range componentNames {
		t.Run(componentName, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			deployment, err := assets.LoadDeploymentManifest(componentName)
			g.Expect(err).ToNot(HaveOccurred())

			initContainers := deployment.Spec.Template.Spec.InitContainers

			// When extracting the catalog content, it should copy only /configs and never the
			// cache shipped in the image.
			extractContent := findContainer("extract-content", initContainers)
			g.Expect(extractContent).ToNot(BeNil(), "extract-content init container should exist")
			g.Expect(extractContent.Args).To(ContainElements("--catalog.from=/configs", "--catalog.to=/extracted-catalog/catalog"))
			g.Expect(extractContent.Args).ToNot(ContainElement(ContainSubstring("--cache.from")))
			g.Expect(extractContent.Args).ToNot(ContainElement(ContainSubstring("--cache.to")))

			// When the catalog content has been extracted, a rebuild-cache init container should
			// regenerate the opm cache from the extracted /configs.
			rebuildCache := findContainer("rebuild-cache", initContainers)
			g.Expect(rebuildCache).ToNot(BeNil(), "rebuild-cache init container should exist")
			g.Expect(rebuildCache.Command).To(Equal([]string{"/bin/opm"}))
			g.Expect(rebuildCache.Args).To(Equal([]string{
				"serve",
				"/extracted-catalog/catalog",
				"--cache-dir=/extracted-catalog/cache",
				"--cache-only",
			}))
			g.Expect(rebuildCache.VolumeMounts).To(ContainElement(corev1.VolumeMount{
				Name:      "catalog-content",
				MountPath: "/extracted-catalog",
			}))

			// The registry container should serve the rebuilt cache.
			registry := findContainer("registry", deployment.Spec.Template.Spec.Containers)
			g.Expect(registry).ToNot(BeNil(), "registry container should exist")
			g.Expect(registry.Args).To(ContainElement("--cache-dir=/extracted-catalog/cache"))

			// The image triggers annotation should keep the rebuild-cache container's image in sync.
			g.Expect(deployment.Annotations["image.openshift.io/triggers"]).To(ContainSubstring(`@.name==\"rebuild-cache\"`))
		})
	}
}

// TestAdaptCatalogDeployment verifies that adaptCatalogDeployment resolves the catalog image and
// applies it to every container that runs opm, including the new rebuild-cache init container, and
// that the imagestream trigger annotation is handled correctly for both the override and the normal
// ImageStream flows.
func TestAdaptCatalogDeployment(t *testing.T) {
	t.Parallel()

	const overrideImage = "quay.io/openshift-release-dev/ocp-release@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const resolvedImage = "registry.example.com/redhat-operator-index@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	testCases := []struct {
		name string
		// annotations are set on the HCP; the override catalog image annotations drive the override path.
		annotations map[string]string
		// existingRegistryImage, when set, seeds an already-created catalog Deployment whose registry
		// container carries the ImageStream-resolved image, exercising the existing-Deployment branch.
		existingRegistryImage string
		// expectedImage is the image every opm container should end up with.
		expectedImage string
		// expectTriggerAnnotation is whether the imagestream trigger annotation should survive.
		expectTriggerAnnotation bool
	}{
		{
			name: "When catalog images are overridden via annotations, it should apply the override to every opm container and drop the imagestream trigger",
			annotations: map[string]string{
				hyperv1.RedHatOperatorsCatalogImageAnnotation:    overrideImage,
				hyperv1.CommunityOperatorsCatalogImageAnnotation: overrideImage,
				hyperv1.CertifiedOperatorsCatalogImageAnnotation: overrideImage,
			},
			expectedImage:           overrideImage,
			expectTriggerAnnotation: false,
		},
		{
			name:                    "When no override annotations are set, it should carry the existing ImageStream-resolved image into every opm container and preserve the imagestream trigger",
			existingRegistryImage:   resolvedImage,
			expectedImage:           resolvedImage,
			expectTriggerAnnotation: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hcp",
					Namespace:   "hcp-namespace",
					Annotations: tc.annotations,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					OLMCatalogPlacement: hyperv1.ManagementOLMCatalogPlacement,
				},
			}

			deployment, err := assets.LoadDeploymentManifest(redhatOperatorsCatalogComponentName)
			g.Expect(err).ToNot(HaveOccurred())
			deployment.Namespace = hcp.Namespace

			cpContext := component.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			// Seed an existing catalog Deployment whose registry container already carries the
			// ImageStream-resolved image, mirroring what the imagestream trigger produces in-cluster.
			if tc.existingRegistryImage != "" {
				existingDeployment := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      deployment.Name,
						Namespace: deployment.Namespace,
					},
					Spec: appsv1.DeploymentSpec{
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{Name: "registry", Image: tc.existingRegistryImage},
								},
							},
						},
					},
				}
				cpContext.Client = fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(existingDeployment).Build()
			}

			c := &catalogOptions{capabilityImageStream: true}

			err = c.adaptCatalogDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			// The resolved image should be applied to the registry, extract-content and
			// rebuild-cache containers so they all run the same catalog image.
			registry := findContainer("registry", deployment.Spec.Template.Spec.Containers)
			g.Expect(registry).ToNot(BeNil())
			g.Expect(registry.Image).To(Equal(tc.expectedImage))

			extractContent := findContainer("extract-content", deployment.Spec.Template.Spec.InitContainers)
			g.Expect(extractContent).ToNot(BeNil())
			g.Expect(extractContent.Image).To(Equal(tc.expectedImage))

			rebuildCache := findContainer("rebuild-cache", deployment.Spec.Template.Spec.InitContainers)
			g.Expect(rebuildCache).ToNot(BeNil())
			g.Expect(rebuildCache.Image).To(Equal(tc.expectedImage))

			// An override image must drop the imagestream trigger annotation so it is not
			// overwritten by the imagestream controller, while the existing-Deployment branch must
			// preserve it to keep the containers in sync on future image updates.
			if tc.expectTriggerAnnotation {
				g.Expect(deployment.Annotations).To(HaveKey("image.openshift.io/triggers"))
			} else {
				g.Expect(deployment.Annotations).ToNot(HaveKey("image.openshift.io/triggers"))
			}
		})
	}
}
