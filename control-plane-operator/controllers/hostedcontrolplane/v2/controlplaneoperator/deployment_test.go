package controlplaneoperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestControlPlaneOperatorOptions_AdaptDeployment(t *testing.T) {
	newHCPContext := func(t *testing.T, hcAnnotations map[string]string, enableGMP bool) (controlplanecomponent.WorkloadContext, *ControlPlaneOperatorOptions) {
		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test-cluster",
				Namespace:   "clusters",
				Annotations: hcAnnotations,
			},
		}
		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "clusters-test-cluster",
			},
			Spec: hyperv1.HostedControlPlaneSpec{InfraID: "test-infra-id"},
		}
		cpo := &ControlPlaneOperatorOptions{
			HostedCluster:  hc,
			Image:          "test-image:latest",
			UtilitiesImage: "utilities:latest",
		}
		cpContext := controlplanecomponent.WorkloadContext{
			Context:             t.Context(),
			HCP:                 hcp,
			EnableGMPMonitoring: enableGMP,
		}
		return cpContext, cpo
	}

	t.Run("imageOverrides", func(t *testing.T) {
		testCases := []struct {
			name                      string
			hostedClusterAnnotations  map[string]string
			expectedImageOverridesArg string
			enableGMPMonitoring       bool
			expectGMPArg              bool
		}{
			{
				name: "When ImageOverridesAnnotation is set on HostedCluster it should pass the value in the flag",
				hostedClusterAnnotations: map[string]string{
					hyperv1.ImageOverridesAnnotation: "cluster-version-operator=quay.io/test@sha256:1234",
				},
				expectedImageOverridesArg: "--image-overrides=cluster-version-operator=quay.io/test@sha256:1234",
			},
			{
				name:                      "When ImageOverridesAnnotation is not set on HostedCluster it should pass a no-op default value",
				hostedClusterAnnotations:  map[string]string{},
				expectedImageOverridesArg: "--image-overrides==",
			},
			{
				name:                      "When GMP monitoring is enabled, it should pass the flag",
				hostedClusterAnnotations:  map[string]string{},
				expectedImageOverridesArg: "--image-overrides==",
				enableGMPMonitoring:       true,
				expectGMPArg:              true,
			},
			{
				name:                      "When GMP monitoring is disabled, it should not pass the flag",
				hostedClusterAnnotations:  map[string]string{},
				expectedImageOverridesArg: "--image-overrides==",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				g := NewGomegaWithT(t)
				cpContext, cpo := newHCPContext(t, tc.hostedClusterAnnotations, tc.enableGMPMonitoring)

				deployment, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cpo.adaptDeployment(cpContext, deployment)).To(Succeed())

				observedArgs := deployment.Spec.Template.Spec.Containers[0].Args
				g.Expect(observedArgs).To(ContainElement(tc.expectedImageOverridesArg))
				if tc.expectGMPArg {
					g.Expect(observedArgs).To(ContainElement("--enable-gmp-monitoring"))
				} else {
					g.Expect(observedArgs).ToNot(ContainElement("--enable-gmp-monitoring"))
				}
			})
		}
	})

	t.Run("userReleaseRegistryOverrides", func(t *testing.T) {
		testCases := []struct {
			name                                   string
			userReleaseRegistryOverrideCommandLine string
			expectArg                              bool
		}{
			{
				name:                                   "When UserReleaseRegistryOverrideCommandLine is set, flag should appear in args",
				userReleaseRegistryOverrideCommandLine: "quay.io/openshift-release-dev/ocp-v5.0-art-dev=235494791733.dkr.ecr.us-east-1.amazonaws.com/openshift-release-dev/ocp-v5.0-art-dev",
				expectArg:                              true,
			},
			{
				name:      "When UserReleaseRegistryOverrideCommandLine is empty, flag should not appear in args",
				expectArg: false,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				g := NewGomegaWithT(t)
				cpContext, cpo := newHCPContext(t, map[string]string{}, false)
				cpo.UserReleaseRegistryOverrideCommandLine = tc.userReleaseRegistryOverrideCommandLine

				deployment, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cpo.adaptDeployment(cpContext, deployment)).To(Succeed())

				observedArgs := deployment.Spec.Template.Spec.Containers[0].Args
				if tc.expectArg {
					g.Expect(observedArgs).To(ContainElement("--user-release-registry-overrides=" + tc.userReleaseRegistryOverrideCommandLine))
				} else {
					g.Expect(observedArgs).ToNot(ContainElement(HavePrefix("--user-release-registry-overrides=")))
				}
			})
		}
	})

	t.Run("watchListClientEnv", func(t *testing.T) {
		testCases := []struct {
			name          string
			envValue      string
			expectPresent bool
		}{
			{
				name:          "When KUBE_FEATURE_WatchListClient is set to false it should propagate the value",
				envValue:      "false",
				expectPresent: true,
			},
			{
				name:          "When KUBE_FEATURE_WatchListClient is set to true it should propagate the value",
				envValue:      "true",
				expectPresent: true,
			},
			{
				name:          "When KUBE_FEATURE_WatchListClient is unset it should not add the env var",
				envValue:      "",
				expectPresent: false,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				g := NewGomegaWithT(t)
				if tc.envValue != "" {
					t.Setenv("KUBE_FEATURE_WatchListClient", tc.envValue)
				}
				cpContext, cpo := newHCPContext(t, map[string]string{}, false)
				cpo.HasUtilities = true

				deployment, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cpo.adaptDeployment(cpContext, deployment)).To(Succeed())

				envVars := deployment.Spec.Template.Spec.Containers[0].Env
				if tc.expectPresent {
					g.Expect(envVars).To(ContainElement(corev1.EnvVar{Name: "KUBE_FEATURE_WatchListClient", Value: tc.envValue}))
				} else {
					g.Expect(envVars).ToNot(ContainElement(HaveField("Name", "KUBE_FEATURE_WatchListClient")))
				}
			})
		}
	})
}
