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

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

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
		{
			name:           "When version is 4.22.0, it should not add TLS args",
			version:        "4.22.0",
			tlsProfile:     &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
			expectedArgs:   []string{"--feature-gates=MachineSetPreflightChecks=false"},
			unexpectedArgs: []string{"--tls-min-version=VersionTLS12"},
			expectedImage:  "cluster-capi-controllers",
		},
		{
			name:          "When version is 4.23.0, it should add TLS args",
			version:       "4.23.0",
			tlsProfile:    &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
			expectedArgs:  []string{"--feature-gates=MachineSetPreflightChecks=false", "--tls-min-version=VersionTLS12"},
			expectedImage: "cluster-capi-controllers",
		},
		{
			name:          "When version is 5.0.0, it should add TLS args",
			version:       "5.0.0",
			tlsProfile:    &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
			expectedArgs:  []string{"--feature-gates=MachineSetPreflightChecks=false", "--tls-min-version=VersionTLS12"},
			expectedImage: "cluster-capi-controllers",
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
