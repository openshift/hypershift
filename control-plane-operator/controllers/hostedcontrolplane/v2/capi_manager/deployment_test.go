package capimanager

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/testutil"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		imageOverride    string
		version          string
		hcpAnnotations   map[string]string
		expectedArgs     []string
		unexpectedArgs   []string
		expectedImage    string
		expectedAnnotKey string
	}{
		{
			name:    "When version is 4.18.0, it should not add feature gate or skip CRD migration flags",
			version: "4.18.0",
			unexpectedArgs: []string{
				"--feature-gates=MachineSetPreflightChecks=false",
				"--skip-crd-migration-phases=StorageVersionMigration",
				"--skip-crd-migration-phases=CleanupManagedFields",
			},
			expectedImage: "cluster-capi-controllers",
		},
		{
			name:    "When version is 4.19.0, it should add feature gate but not skip CRD migration phases",
			version: "4.19.0",
			expectedArgs: []string{
				"--feature-gates=MachineSetPreflightChecks=false",
			},
			unexpectedArgs: []string{
				"--skip-crd-migration-phases=StorageVersionMigration",
				"--skip-crd-migration-phases=CleanupManagedFields",
			},
			expectedImage: "cluster-capi-controllers",
		},
		{
			name:    "When version is 4.20.0, it should add feature gate and skip CRD migration flags",
			version: "4.20.0",
			expectedArgs: []string{
				"--feature-gates=MachineSetPreflightChecks=false",
				"--skip-crd-migration-phases=StorageVersionMigration",
				"--skip-crd-migration-phases=CleanupManagedFields",
			},
			expectedImage: "cluster-capi-controllers",
		},
		{
			name:          "When imageOverride is set, it should use the override image",
			version:       "4.20.0",
			imageOverride: "quay.io/custom/capi:v1.0.0",
			expectedArgs: []string{
				"--feature-gates=MachineSetPreflightChecks=false",
				"--skip-crd-migration-phases=StorageVersionMigration",
				"--skip-crd-migration-phases=CleanupManagedFields",
			},
			expectedImage: "quay.io/custom/capi:v1.0.0",
		},
		{
			name:    "When HCP has hosted cluster annotation, it should set deployment annotation",
			version: "4.19.0",
			hcpAnnotations: map[string]string{
				k8sutil.HostedClusterAnnotation: "test-namespace/test-cluster",
			},
			expectedArgs: []string{
				"--feature-gates=MachineSetPreflightChecks=false",
			},
			expectedImage:    "cluster-capi-controllers",
			expectedAnnotKey: k8sutil.HostedClusterAnnotation,
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

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			releaseProvider := testutil.FakeImageProvider(testutil.WithVersion(tc.version))

			capi := &CAPIManagerOptions{
				imageOverride: tc.imageOverride,
			}

			cpContext := component.WorkloadContext{
				Context:                t.Context(),
				HCP:                    hcp,
				ReleaseImageProvider:   releaseProvider,
				SkipCertificateSigning: false,
			}

			err = capi.adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			// Find the manager container
			managerContainer := podspec.FindContainer("manager", deployment.Spec.Template.Spec.Containers)
			g.Expect(managerContainer).ToNot(BeNil())

			// Check expected args
			for _, expectedArg := range tc.expectedArgs {
				g.Expect(managerContainer.Args).To(ContainElement(expectedArg))
			}

			// Check unexpected args are absent
			for _, unexpectedArg := range tc.unexpectedArgs {
				g.Expect(managerContainer.Args).ToNot(ContainElement(unexpectedArg))
			}

			// Check image
			g.Expect(managerContainer.Image).To(Equal(tc.expectedImage))

			// Check annotations
			if tc.expectedAnnotKey != "" {
				g.Expect(deployment.Annotations).To(HaveKey(tc.expectedAnnotKey))
				g.Expect(deployment.Annotations[tc.expectedAnnotKey]).To(Equal(hcp.Annotations[tc.expectedAnnotKey]))
			}
		})
	}
}

func TestAdaptDeployment_ParseVersionError(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
	}

	deployment, err := assets.LoadDeploymentManifest(ComponentName)
	g.Expect(err).ToNot(HaveOccurred())

	releaseProvider := testutil.FakeImageProvider(testutil.WithVersion("invalid-version"))

	capi := &CAPIManagerOptions{}

	cpContext := component.WorkloadContext{
		Context:              t.Context(),
		HCP:                  hcp,
		ReleaseImageProvider: releaseProvider,
	}

	err = capi.adaptDeployment(cpContext, deployment)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("failed to parse version"))
}
