package catalogs

import (
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCatalogsPredicate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                     string
		olmCatalogPlacement      hyperv1.OLMCatalogPlacement
		disableAllDefaultSources bool
		expectedResult           bool
	}{
		{
			name:                     "When OLMCatalogPlacement is Management and default sources are not disabled, it should return true",
			olmCatalogPlacement:      hyperv1.ManagementOLMCatalogPlacement,
			disableAllDefaultSources: false,
			expectedResult:           true,
		},
		{
			name:                     "When OLMCatalogPlacement is Guest, it should return false",
			olmCatalogPlacement:      hyperv1.GuestOLMCatalogPlacement,
			disableAllDefaultSources: false,
			expectedResult:           false,
		},
		{
			name:                     "When default sources are disabled, it should return false regardless of placement",
			olmCatalogPlacement:      hyperv1.ManagementOLMCatalogPlacement,
			disableAllDefaultSources: true,
			expectedResult:           false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					OLMCatalogPlacement: tc.olmCatalogPlacement,
				},
			}

			if tc.disableAllDefaultSources {
				hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
					OperatorHub: &configv1.OperatorHubSpec{
						DisableAllDefaultSources: true,
					},
				}
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			result, err := catalogsPredicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expectedResult))
		})
	}
}

func TestImageStreamPredicate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                  string
		capabilityImageStream bool
		annotationPresent     bool
		expectedResult        bool
	}{
		{
			name:                  "When capabilityImageStream is false, it should return false",
			capabilityImageStream: false,
			annotationPresent:     false,
			expectedResult:        false,
		},
		{
			name:                  "When annotation is present, it should return false",
			capabilityImageStream: true,
			annotationPresent:     true,
			expectedResult:        false,
		},
		{
			name:                  "When capabilityImageStream is true and no annotation, it should return true",
			capabilityImageStream: true,
			annotationPresent:     false,
			expectedResult:        true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
			}

			if tc.annotationPresent {
				hcp.Annotations = map[string]string{
					hyperv1.RedHatOperatorsCatalogImageAnnotation: "some-image",
				}
			}

			catalogOpts := &catalogOptions{
				capabilityImageStream: tc.capabilityImageStream,
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			result := catalogOpts.imageStreamPredicate(cpContext)
			g.Expect(result).To(Equal(tc.expectedResult))
		})
	}
}

func TestNewCatalogComponents(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                  string
		capabilityImageStream bool
		expectedCount         int
	}{
		{
			name:                  "When creating catalog components, it should return 3 components",
			capabilityImageStream: false,
			expectedCount:         3,
		},
		{
			name:                  "When creating catalog components with image stream capability, it should return 3 components",
			capabilityImageStream: true,
			expectedCount:         3,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			components := NewCatalogComponents(tc.capabilityImageStream)

			g.Expect(components).To(HaveLen(tc.expectedCount))
			for _, comp := range components {
				g.Expect(comp).ToNot(BeNil())
			}
		})
	}

	for _, catalog := range NewCatalogComponents(false) {
		t.Run(fmt.Sprintf("When reconciling %s, it should not hide the image-provided cache of the extract-content init container", catalog.Name()), func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: "hcp-namespace",
					Annotations: map[string]string{
						hyperv1.RedHatOperatorsCatalogImageAnnotation:    "quay.io/test/redhat-operators@sha256:1111111111111111111111111111111111111111111111111111111111111111",
						hyperv1.CommunityOperatorsCatalogImageAnnotation: "quay.io/test/community-operators@sha256:2222222222222222222222222222222222222222222222222222222222222222",
						hyperv1.CertifiedOperatorsCatalogImageAnnotation: "quay.io/test/certified-operators@sha256:3333333333333333333333333333333333333333333333333333333333333333",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					OLMCatalogPlacement: hyperv1.ManagementOLMCatalogPlacement,
				},
			}
			imageProvider := testutil.FakeImageProvider()
			var dependencies []client.Object
			for _, name := range []string{"kube-apiserver", oapiv2.ComponentName} {
				dependencies = append(dependencies, &hyperv1.ControlPlaneComponent{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: hcp.Namespace},
					Status: hyperv1.ControlPlaneComponentStatus{
						Version: imageProvider.Version(),
						Conditions: []metav1.Condition{
							{Type: string(hyperv1.ControlPlaneComponentAvailable), Status: metav1.ConditionTrue},
							{Type: string(hyperv1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionTrue},
						},
					},
				})
			}
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(dependencies...).Build()

			g.Expect(catalog.Reconcile(component.ControlPlaneContext{
				Context:                  t.Context(),
				Client:                   fakeClient,
				HCP:                      hcp,
				ApplyProvider:            upsert.NewApplyProvider(false),
				ReleaseImageProvider:     imageProvider,
				UserReleaseImageProvider: imageProvider,
			})).To(Succeed(), "failed to reconcile %s", catalog.Name())

			deployment := &appsv1.Deployment{}
			g.Expect(fakeClient.Get(t.Context(), client.ObjectKey{Namespace: hcp.Namespace, Name: catalog.Name()}, deployment)).
				To(Succeed(), "failed to get the %s deployment", catalog.Name())

			podSpec := deployment.Spec.Template.Spec
			extractContent := podspec.FindContainer(extractContentContainerName, podSpec.InitContainers)
			g.Expect(extractContent).NotTo(BeNil(), "the %s deployment should have an %s init container", catalog.Name(), extractContentContainerName)
			g.Expect(extractContent.SecurityContext).NotTo(BeNil(), "init container %q should have a security context", extractContent.Name)
			g.Expect(extractContent.SecurityContext.ReadOnlyRootFilesystem).To(HaveValue(BeTrue()),
				"init container %q should keep a read-only root filesystem", extractContent.Name)

			var cacheFrom string
			for _, arg := range extractContent.Args {
				if value, found := strings.CutPrefix(arg, "--cache.from="); found {
					cacheFrom = value
				}
			}
			g.Expect(cacheFrom).NotTo(BeEmpty(), "init container %q should read the image-provided cache via --cache.from", extractContent.Name)
			for _, mount := range extractContent.VolumeMounts {
				mountPath := strings.TrimSuffix(mount.MountPath, "/")
				g.Expect(cacheFrom == mountPath || strings.HasPrefix(cacheFrom, mountPath+"/")).To(BeFalse(),
					"volume %q mounted at %q in init container %q would hide the image-provided cache at %q",
					mount.Name, mount.MountPath, extractContent.Name, cacheFrom)
			}

			registry := podspec.FindContainer("registry", podSpec.Containers)
			g.Expect(registry).NotTo(BeNil(), "the %s deployment should have a registry container", catalog.Name())
			g.Expect(registry.VolumeMounts).To(ContainElement(HaveField("MountPath", podspec.PodTmpDirMountPath)),
				"container %q should still get the writable /tmp mount", registry.Name)
		})
	}
}
