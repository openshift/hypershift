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
	"k8s.io/utils/ptr"
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
		name              string
		imageOverride     string
		version           string
		hcpAnnotations    map[string]string
		tlsProfile        *configv1.TLSSecurityProfile
		expectedArgs      []string
		unexpectedArgs    []string
		expectedProxyArgs []string
		expectedImage     string
		expectedAnnotKey  string
	}{
		{
			name:    "When version is 4.14.0, it should serve metrics through the TLS sidecar without the feature gate or skip CRD migration flags",
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
			name:              "When version is 4.22.0, it should not add TLS args to the manager but should add them to the metrics sidecar",
			version:           "4.22.0",
			tlsProfile:        &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
			expectedArgs:      []string{"--feature-gates=MachineSetPreflightChecks=false"},
			unexpectedArgs:    []string{"--tls-min-version=VersionTLS12"},
			expectedProxyArgs: []string{"--tls-min-version=VersionTLS12"},
			expectedImage:     "cluster-capi-controllers",
		},
		{
			name:              "When the TLS profile is Modern, it should configure the metrics sidecar for TLS 1.3",
			version:           "5.0.0",
			tlsProfile:        &configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType, Modern: &configv1.ModernTLSProfile{}},
			expectedArgs:      []string{"--tls-min-version=VersionTLS13"},
			expectedProxyArgs: []string{"--tls-min-version=VersionTLS13"},
			expectedImage:     "cluster-capi-controllers",
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

			// The manager serves its diagnostics over plain HTTP on the loopback interface only, so
			// they are not reachable from outside the pod.
			g.Expect(countArg(managerContainer.Args, "--diagnostics-address=127.0.0.1:8080")).To(Equal(1))
			g.Expect(countArg(managerContainer.Args, "--insecure-diagnostics")).To(Equal(1))
			g.Expect(managerContainer.Args).ToNot(ContainElement("--diagnostics-address=:8080"))
			g.Expect(managerContainer.Ports).To(BeEmpty())

			// The kube-rbac-proxy sidecar is the only metrics endpoint: it serves TLS with the
			// HyperShift-issued serving certificate, requires the metrics-client certificate and
			// only proxies /metrics to the loopback diagnostics endpoint.
			proxyContainer := podspec.FindContainer("kube-rbac-proxy", deployment.Spec.Template.Spec.Containers)
			g.Expect(proxyContainer).ToNot(BeNil())
			g.Expect(proxyContainer.Image).To(Equal("kube-rbac-proxy"))
			g.Expect(proxyContainer.Args).To(ContainElements(
				"--secure-listen-address=:8443",
				"--upstream=http://127.0.0.1:8080/",
				"--tls-cert-file=/etc/tls/private/tls.crt",
				"--tls-private-key-file=/etc/tls/private/tls.key",
				"--client-ca-file=/etc/tls/client/ca.crt",
				"--config-file=/etc/kube-rbac-proxy/config.yaml",
				"--allow-paths=/metrics",
			))
			g.Expect(proxyContainer.Args).To(ContainElement(HavePrefix("--tls-min-version=")))
			g.Expect(proxyContainer.Args).ToNot(ContainElement(HavePrefix("--ignore-paths")))
			for _, expectedArg := range tc.expectedProxyArgs {
				g.Expect(countArg(proxyContainer.Args, expectedArg)).To(Equal(1))
			}
			g.Expect(proxyContainer.Ports).To(ConsistOf(corev1.ContainerPort{
				Name:          "metrics",
				ContainerPort: 8443,
				Protocol:      corev1.ProtocolTCP,
			}))
			g.Expect(proxyContainer.VolumeMounts).To(ConsistOf(
				corev1.VolumeMount{Name: "metrics-tls", MountPath: "/etc/tls/private", ReadOnly: true},
				corev1.VolumeMount{Name: "metrics-client-ca", MountPath: "/etc/tls/client", ReadOnly: true},
				corev1.VolumeMount{Name: "kube-rbac-proxy-config", MountPath: "/etc/kube-rbac-proxy", ReadOnly: true},
			))

			// A missing metrics certificate must never keep the manager container from starting, and
			// only the CA is projected from the metrics-client secret, not its private key.
			volumes := map[string]corev1.Volume{}
			for _, v := range deployment.Spec.Template.Spec.Volumes {
				volumes[v.Name] = v
			}
			g.Expect(volumes["metrics-tls"].Secret).ToNot(BeNil())
			g.Expect(volumes["metrics-tls"].Secret.SecretName).To(Equal("cluster-api-metrics-tls"))
			g.Expect(volumes["metrics-tls"].Secret.Optional).To(Equal(ptr.To(true)))
			g.Expect(volumes["metrics-client-ca"].Secret).ToNot(BeNil())
			g.Expect(volumes["metrics-client-ca"].Secret.SecretName).To(Equal("metrics-client"))
			g.Expect(volumes["metrics-client-ca"].Secret.Items).To(ConsistOf(corev1.KeyToPath{Key: "ca.crt", Path: "ca.crt"}))
			g.Expect(volumes["metrics-client-ca"].Secret.Optional).To(Equal(ptr.To(true)))
			g.Expect(volumes["kube-rbac-proxy-config"].ConfigMap).ToNot(BeNil())
			g.Expect(volumes["kube-rbac-proxy-config"].ConfigMap.Name).To(Equal("cluster-api-kube-rbac-proxy"))

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
