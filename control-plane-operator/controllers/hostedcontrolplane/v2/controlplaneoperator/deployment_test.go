package controlplaneoperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/pdbdrain"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeploymentImageOverrides(t *testing.T) {
	testCases := []struct {
		name                      string
		hostedClusterAnnotations  map[string]string
		expectedImageOverridesArg string
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-cluster",
					Namespace:   "clusters",
					Annotations: tc.hostedClusterAnnotations,
				},
			}

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters-test-cluster",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "test-infra-id",
				},
			}

			cpo := &ControlPlaneOperatorOptions{
				HostedCluster:               hc,
				Image:                       "test-image:latest",
				UtilitiesImage:              "utilities:latest",
				HasUtilities:                false,
				RegistryOverrideCommandLine: "",
			}

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = cpo.adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			observedArgs := deployment.Spec.Template.Spec.Containers[0].Args
			g.Expect(observedArgs).To(ContainElement(tc.expectedImageOverridesArg))
		})
	}
}

func TestAdaptDeploymentEnvPropagation(t *testing.T) {
	testCases := []struct {
		name                string
		envName             string
		envValue            string
		drainReadinessFlag  string
		drainReadinessOptIn bool
		expectPresent       bool
	}{
		{
			name:          "When KUBE_FEATURE_WatchListClient is set to false it should propagate the value",
			envName:       "KUBE_FEATURE_WatchListClient",
			envValue:      "false",
			expectPresent: true,
		},
		{
			name:          "When KUBE_FEATURE_WatchListClient is set to true it should propagate the value",
			envName:       "KUBE_FEATURE_WatchListClient",
			envValue:      "true",
			expectPresent: true,
		},
		{
			name:          "When KUBE_FEATURE_WatchListClient is unset it should not add the env var",
			envName:       "KUBE_FEATURE_WatchListClient",
			envValue:      "",
			expectPresent: false,
		},
		{
			name:                "When NodePool drain readiness is enabled it should propagate the flag to CPO",
			envName:             "ENABLE_NODEPOOL_DRAIN_READINESS",
			envValue:            "1",
			drainReadinessOptIn: true,
			expectPresent:       true,
		},
		{
			name:          "When NodePool drain readiness is disabled it should omit the flag from CPO",
			envName:       "ENABLE_NODEPOOL_DRAIN_READINESS",
			envValue:      "0",
			expectPresent: false,
		},
		{
			name:                "When managed namespace prefixes are configured with drain readiness, it should propagate them to CPO",
			envName:             pdbdrain.AdditionalPlatformNamespacePrefixesEnv,
			envValue:            "redhat-,open-cluster-management",
			drainReadinessFlag:  "1",
			drainReadinessOptIn: true,
			expectPresent:       true,
		},
		{
			name:          "When drain readiness is disabled, it should omit managed namespace prefixes from CPO",
			envName:       pdbdrain.AdditionalPlatformNamespacePrefixesEnv,
			envValue:      "redhat-",
			expectPresent: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			t.Setenv("ENABLE_NODEPOOL_DRAIN_READINESS", tc.drainReadinessFlag)
			t.Setenv(tc.envName, tc.envValue)

			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
			}

			hcpAnnotations := map[string]string{}
			if tc.drainReadinessOptIn {
				hcpAnnotations[hyperv1.NodePoolDrainReadinessAnnotation] = "true"
			}

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-cluster",
					Namespace:   "clusters-test-cluster",
					Annotations: hcpAnnotations,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "test-infra-id",
				},
			}

			cpo := &ControlPlaneOperatorOptions{
				HostedCluster:  hc,
				Image:          "test-image:latest",
				UtilitiesImage: "utilities:latest",
				HasUtilities:   true,
			}

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = cpo.adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			envVars := deployment.Spec.Template.Spec.Containers[0].Env
			expectedEnvVar := corev1.EnvVar{
				Name:  tc.envName,
				Value: tc.envValue,
			}

			if tc.expectPresent {
				g.Expect(envVars).To(ContainElement(expectedEnvVar))
			} else {
				g.Expect(envVars).ToNot(ContainElement(HaveField("Name", tc.envName)))
			}
		})
	}
}
