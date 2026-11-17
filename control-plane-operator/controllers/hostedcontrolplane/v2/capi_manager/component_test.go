package capimanager

import (
	"context"
	"fmt"
	"slices"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestNewComponent(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		version       string
		imageOverride string
		expectedImage string
		expectedArgs  []string
	}{
		{
			name:          "When version is 4.18.0 and the backward-compatible CAPI image is used, it should render the client rate limit args once",
			version:       "4.18.0",
			imageOverride: backwardCompatibleCAPIImage("4.18.0"),
			expectedImage: backwardCompatibleCAPIImage("4.18.0"),
			expectedArgs:  slices.Concat(staticManagerArgs, expectedClientRateLimitArgs),
		},
		{
			name:          "When version is 4.22.0 and the payload CAPI image is used, it should render the client rate limit args once",
			version:       "4.22.0",
			expectedImage: "cluster-capi-controllers",
			expectedArgs: slices.Concat(staticManagerArgs, []string{
				"--feature-gates=MachineSetPreflightChecks=false",
				"--skip-crd-migration-phases=StorageVersionMigration",
				"--skip-crd-migration-phases=CleanupManagedFields",
			}, expectedClientRateLimitArgs),
		},
		{
			name:          "When version is 4.18.0 and a user-provided image override is set, it should render only the existing args",
			version:       "4.18.0",
			imageOverride: "quay.io/custom/capi:v1.0.0",
			expectedImage: "quay.io/custom/capi:v1.0.0",
			expectedArgs:  staticManagerArgs,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: "hcp-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS:  &hyperv1.AWSPlatformSpec{},
					},
					ReleaseImage: fmt.Sprintf("quay.io/openshift-release-dev/ocp-release:%s-x86_64", tc.version),
				},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c crclient.WithWatch, key crclient.ObjectKey, obj crclient.Object, opts ...crclient.GetOption) error {
					// Match the real client's preservation of a typed object's GVK on Get.
					gvk := obj.GetObjectKind().GroupVersionKind()
					defer obj.GetObjectKind().SetGroupVersionKind(gvk)
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build()
			cpContext := component.ControlPlaneContext{
				Context:                t.Context(),
				Client:                 fakeClient,
				ReleaseImageProvider:   testutil.FakeImageProvider(testutil.WithVersion(tc.version)),
				HCP:                    hcp,
				ApplyProvider:          upsert.NewApplyProvider(true),
				OmitOwnerReference:     true,
				SkipPredicate:          true,
				SkipCertificateSigning: true,
			}

			capi := NewComponent(tc.imageOverride)
			// Reconcile twice: the second reconcile must neither duplicate args nor update any object.
			for range 2 {
				g.Expect(capi.Reconcile(cpContext)).To(Succeed())
			}
			g.Expect(cpContext.ApplyProvider.ValidateUpdateEvents(0)).To(Succeed())

			deployment := &appsv1.Deployment{}
			err := fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: hcp.Namespace, Name: ComponentName}, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			managerContainer := podspec.FindContainer("manager", deployment.Spec.Template.Spec.Containers)
			g.Expect(managerContainer).ToNot(BeNil())
			g.Expect(managerContainer.Args).To(Equal(tc.expectedArgs))
			g.Expect(managerContainer.Image).To(Equal(tc.expectedImage))
		})
	}
}

func TestPredicate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		hcpAnnotations map[string]string
		expected       bool
	}{
		{
			name:     "When HCP has no annotations, it should return true",
			expected: true,
		},
		{
			name: "When HCP has DisableMachineManagement annotation, it should return false",
			hcpAnnotations: map[string]string{
				hyperv1.DisableMachineManagement: "true",
			},
			expected: false,
		},
		{
			name: "When HCP has other annotations but not DisableMachineManagement, it should return true",
			hcpAnnotations: map[string]string{
				"some.other/annotation": "value",
			},
			expected: true,
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
					Annotations: tc.hcpAnnotations,
				},
			}

			cpContext := component.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			result, err := predicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestCAPIManagerOptions_IsRequestServing(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	capi := &CAPIManagerOptions{}
	g.Expect(capi.IsRequestServing()).To(BeFalse())
}

func TestCAPIManagerOptions_MultiZoneSpread(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	capi := &CAPIManagerOptions{}
	g.Expect(capi.MultiZoneSpread()).To(BeFalse())
}

func TestCAPIManagerOptions_NeedsManagementKASAccess(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	capi := &CAPIManagerOptions{}
	g.Expect(capi.NeedsManagementKASAccess()).To(BeTrue())
}
