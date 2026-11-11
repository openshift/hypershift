package capimanager

import (
	"slices"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/backwardcompat"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/testutil"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/blang/semver"
)

// staticManagerArgs are the manager container args defined in the cluster-api deployment asset.
var staticManagerArgs = []string{
	"--namespace",
	"$(MY_NAMESPACE)",
	"--v=4",
	"--leader-elect=true",
	"--leader-elect-lease-duration=137s",
	"--leader-elect-retry-period=26s",
	"--leader-elect-renew-deadline=107s",
	"--diagnostics-address=:8080",
	"--insecure-diagnostics",
}

// expectedClientRateLimitArgs are the client rate limit args the manager container must carry, in order.
var expectedClientRateLimitArgs = []string{
	"--kube-api-qps=100",
	"--kube-api-burst=200",
	"--clustercache-client-qps=100",
	"--clustercache-client-burst=200",
}

// rateLimitArgsIn returns the args that set any of the client rate limit flags, in order.
func rateLimitArgsIn(args []string) []string {
	var found []string
	for _, arg := range args {
		name, _, _ := strings.Cut(arg, "=")
		switch name {
		case "--kube-api-qps", "--kube-api-burst", "--clustercache-client-qps", "--clustercache-client-burst":
			found = append(found, arg)
		}
	}
	return found
}

// wantRateLimitArgs returns the client rate limit args a manager container must carry: all four exactly once, or none.
func wantRateLimitArgs(expected bool) []string {
	if expected {
		return expectedClientRateLimitArgs
	}
	return nil
}

// backwardCompatibleCAPIImage returns the image the HyperShift Operator pins the CAPI manager to for a payload version.
func backwardCompatibleCAPIImage(version string) string {
	return backwardcompat.GetBackwardCompatibleCAPIImage(semver.MustParse(version))
}

// countArg returns how many times arg appears in args.
func countArg(args []string, arg string) int {
	count := 0
	for _, a := range args {
		if a == arg {
			count++
		}
	}
	return count
}

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		imageOverride    string
		version          string
		hcpAnnotations   map[string]string
		tlsProfile       *configv1.TLSSecurityProfile
		expectedArgs     []string
		unexpectedArgs   []string
		expectedImage    string
		expectedAnnotKey string
		// expectClientRateLimitArgs is whether the client rate limit flags must be set exactly once.
		expectClientRateLimitArgs bool
		// expectedFullArgs, when set, is the exact expected manager container args list.
		expectedFullArgs []string
	}{
		{
			name:    "When version is 4.14.0, it should serve metrics over HTTP without the feature gate or skip CRD migration flags",
			version: "4.14.0",
			unexpectedArgs: []string{
				"--feature-gates=MachineSetPreflightChecks=false",
				"--skip-crd-migration-phases=StorageVersionMigration",
				"--skip-crd-migration-phases=CleanupManagedFields",
			},
			expectedImage: "cluster-capi-controllers",
		},
		{
			name:    "When version is 4.18.0, it should not add feature gate or skip CRD migration flags",
			version: "4.18.0",
			unexpectedArgs: []string{
				"--feature-gates=MachineSetPreflightChecks=false",
				"--skip-crd-migration-phases=StorageVersionMigration",
				"--skip-crd-migration-phases=CleanupManagedFields",
			},
			expectedImage:             "cluster-capi-controllers",
			expectClientRateLimitArgs: false,
			expectedFullArgs:          staticManagerArgs,
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
			expectedImage:             "cluster-capi-controllers",
			expectClientRateLimitArgs: true,
		},
		{
			name:    "When version is 4.20.0, it should add feature gate and skip CRD migration flags",
			version: "4.20.0",
			expectedArgs: []string{
				"--feature-gates=MachineSetPreflightChecks=false",
				"--skip-crd-migration-phases=StorageVersionMigration",
				"--skip-crd-migration-phases=CleanupManagedFields",
			},
			expectedImage:             "cluster-capi-controllers",
			expectClientRateLimitArgs: true,
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
			expectedImage:             "quay.io/custom/capi:v1.0.0",
			expectClientRateLimitArgs: true,
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
			expectedImage:             "cluster-capi-controllers",
			expectedAnnotKey:          k8sutil.HostedClusterAnnotation,
			expectClientRateLimitArgs: true,
		},
		{
			name:                      "When version is 4.22.0, it should not add TLS args",
			version:                   "4.22.0",
			tlsProfile:                &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
			expectedArgs:              []string{"--feature-gates=MachineSetPreflightChecks=false"},
			unexpectedArgs:            []string{"--tls-min-version=VersionTLS12"},
			expectedImage:             "cluster-capi-controllers",
			expectClientRateLimitArgs: true,
		},
		{
			name:                      "When version is 4.23.0, it should add TLS args",
			version:                   "4.23.0",
			tlsProfile:                &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
			expectedArgs:              []string{"--feature-gates=MachineSetPreflightChecks=false", "--tls-min-version=VersionTLS12"},
			expectedImage:             "cluster-capi-controllers",
			expectClientRateLimitArgs: true,
		},
		{
			name:                      "When version is 5.0.0, it should add TLS args",
			version:                   "5.0.0",
			tlsProfile:                &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
			expectedArgs:              []string{"--feature-gates=MachineSetPreflightChecks=false", "--tls-min-version=VersionTLS12"},
			expectedImage:             "cluster-capi-controllers",
			expectClientRateLimitArgs: true,
		},
		{
			name:                      "When version is 4.14.0 and the backward-compatible CAPI image is used, it should add the client rate limit args",
			version:                   "4.14.0",
			imageOverride:             backwardCompatibleCAPIImage("4.14.0"),
			expectedImage:             backwardCompatibleCAPIImage("4.14.0"),
			expectClientRateLimitArgs: true,
			expectedFullArgs:          slices.Concat(staticManagerArgs, expectedClientRateLimitArgs),
		},
		{
			name:          "When version is 4.18.0 and the backward-compatible CAPI image is used, it should add the client rate limit args",
			version:       "4.18.0",
			imageOverride: backwardCompatibleCAPIImage("4.18.0"),
			unexpectedArgs: []string{
				"--feature-gates=MachineSetPreflightChecks=false",
				"--skip-crd-migration-phases=StorageVersionMigration",
			},
			expectedImage:             backwardCompatibleCAPIImage("4.18.0"),
			expectClientRateLimitArgs: true,
		},
		{
			name:                      "When version is 4.20.0 and the backward-compatible CAPI image is used, it should keep existing args and append the client rate limit args",
			version:                   "4.20.0",
			imageOverride:             backwardCompatibleCAPIImage("4.20.0"),
			expectedImage:             backwardCompatibleCAPIImage("4.20.0"),
			expectClientRateLimitArgs: true,
			expectedFullArgs: slices.Concat(staticManagerArgs, []string{
				"--feature-gates=MachineSetPreflightChecks=false",
				"--skip-crd-migration-phases=StorageVersionMigration",
				"--skip-crd-migration-phases=CleanupManagedFields",
			}, expectedClientRateLimitArgs),
		},
		{
			name:                      "When version is 4.19.0-rc.2 and the backward-compatible CAPI image is used, it should add the client rate limit args",
			version:                   "4.19.0-rc.2",
			imageOverride:             backwardCompatibleCAPIImage("4.19.0-rc.2"),
			expectedImage:             backwardCompatibleCAPIImage("4.19.0-rc.2"),
			expectClientRateLimitArgs: true,
		},
		{
			name:                      "When version is 4.21.0 and the payload CAPI image is used, it should add the client rate limit args",
			version:                   "4.21.0",
			expectedArgs:              []string{"--feature-gates=MachineSetPreflightChecks=false"},
			expectedImage:             "cluster-capi-controllers",
			expectClientRateLimitArgs: true,
		},
		{
			name:                      "When version is 5.1.0 and the payload CAPI image is used, it should keep TLS args and append the client rate limit args",
			version:                   "5.1.0",
			tlsProfile:                &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
			expectedArgs:              []string{"--feature-gates=MachineSetPreflightChecks=false", "--tls-min-version=VersionTLS12"},
			expectedImage:             "cluster-capi-controllers",
			expectClientRateLimitArgs: true,
		},
		{
			name:                      "When version is 4.18.0 and a user-provided image override is set, it should not add the client rate limit args",
			version:                   "4.18.0",
			imageOverride:             "quay.io/custom/capi:v1.0.0",
			expectedImage:             "quay.io/custom/capi:v1.0.0",
			expectClientRateLimitArgs: false,
			expectedFullArgs:          staticManagerArgs,
		},
		{
			name:                      "When version is 4.19.0-rc.2 and a user-provided image override is set, it should not add the client rate limit args",
			version:                   "4.19.0-rc.2",
			imageOverride:             "quay.io/custom/capi:v1.0.0",
			expectedImage:             "quay.io/custom/capi:v1.0.0",
			expectClientRateLimitArgs: false,
		},
		{
			name:                      "When version is 4.19.0 and a user-provided image override is set, it should add the client rate limit args",
			version:                   "4.19.0",
			imageOverride:             "quay.io/custom/capi:v1.0.0",
			expectedArgs:              []string{"--feature-gates=MachineSetPreflightChecks=false"},
			expectedImage:             "quay.io/custom/capi:v1.0.0",
			expectClientRateLimitArgs: true,
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
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: tc.tlsProfile,
						},
					},
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

			// Check the client rate limit args are set exactly once with the expected values, or not at all
			g.Expect(rateLimitArgsIn(managerContainer.Args)).To(Equal(wantRateLimitArgs(tc.expectClientRateLimitArgs)))

			// Check the complete args list when the case pins it
			if tc.expectedFullArgs != nil {
				g.Expect(managerContainer.Args).To(Equal(tc.expectedFullArgs))
			}

			// Check image
			g.Expect(managerContainer.Image).To(Equal(tc.expectedImage))

			// Every version serves metrics over plain HTTP on the named metrics port, so the cluster-api
			// PodMonitor can scrape it without TokenReview/SubjectAccessReview permissions.
			g.Expect(countArg(managerContainer.Args, "--diagnostics-address=:8080")).To(Equal(1))
			g.Expect(countArg(managerContainer.Args, "--insecure-diagnostics")).To(Equal(1))
			g.Expect(managerContainer.Ports).To(ConsistOf(corev1.ContainerPort{
				Name:          "metrics",
				ContainerPort: 8080,
				Protocol:      corev1.ProtocolTCP,
			}))

			// The health probe server and the webhook server are unchanged.
			g.Expect(managerContainer.Args).ToNot(ContainElement(HavePrefix("--health-addr")))
			g.Expect(managerContainer.Args).ToNot(ContainElement(HavePrefix("--webhook-port")))
			g.Expect(managerContainer.Args).ToNot(ContainElement(HavePrefix("--metrics-bind-addr")))
			g.Expect(managerContainer.LivenessProbe).ToNot(BeNil())
			g.Expect(managerContainer.LivenessProbe.HTTPGet).ToNot(BeNil())
			g.Expect(managerContainer.LivenessProbe.HTTPGet.Path).To(Equal("/healthz"))
			g.Expect(managerContainer.LivenessProbe.HTTPGet.Port).To(Equal(intstr.FromInt32(9440)))
			g.Expect(managerContainer.LivenessProbe.HTTPGet.Scheme).To(Equal(corev1.URISchemeHTTP))
			g.Expect(managerContainer.ReadinessProbe).ToNot(BeNil())
			g.Expect(managerContainer.ReadinessProbe.HTTPGet).ToNot(BeNil())
			g.Expect(managerContainer.ReadinessProbe.HTTPGet.Path).To(Equal("/readyz"))
			g.Expect(managerContainer.ReadinessProbe.HTTPGet.Port).To(Equal(intstr.FromInt32(9440)))
			g.Expect(managerContainer.ReadinessProbe.HTTPGet.Scheme).To(Equal(corev1.URISchemeHTTP))
			g.Expect(managerContainer.VolumeMounts).To(ContainElement(corev1.VolumeMount{
				Name:      "capi-webhooks-tls",
				MountPath: "/tmp/k8s-webhook-server/serving-certs",
				ReadOnly:  true,
			}))

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
