package controlplaneoperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/images"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestControlPlaneOperatorOptions_AdaptDeployment(t *testing.T) {
	t.Run("image overrides", func(t *testing.T) {
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

				cpo := &ControlPlaneOperatorOptions{
					HostedCluster: &hyperv1.HostedCluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:        "test-cluster",
							Namespace:   "clusters",
							Annotations: tc.hostedClusterAnnotations,
						},
					},
					Image:          "test-image:latest",
					UtilitiesImage: "utilities:latest",
				}

				deployment := adaptDeploymentForTest(t, cpo)

				observedArgs := deployment.Spec.Template.Spec.Containers[0].Args
				g.Expect(observedArgs).To(ContainElement(tc.expectedImageOverridesArg))
			})
		}
	})

	t.Run("propagated env", func(t *testing.T) {
		testCases := []struct {
			name          string
			envName       string
			envValue      string
			expectPresent bool
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
				name:          "When IMAGE_SHARED_INGRESS_HAPROXY is set it should propagate the value",
				envName:       images.SharedIngressHAProxyEnvVar,
				envValue:      "quay.io/test/haproxy@sha256:1234",
				expectPresent: true,
			},
			{
				name:          "When IMAGE_SHARED_INGRESS_HAPROXY is unset it should not add the env var",
				envName:       images.SharedIngressHAProxyEnvVar,
				envValue:      "",
				expectPresent: false,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				g := NewGomegaWithT(t)

				t.Setenv(tc.envName, tc.envValue)

				cpo := &ControlPlaneOperatorOptions{
					HostedCluster: &hyperv1.HostedCluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-cluster",
							Namespace: "clusters",
						},
					},
					Image:          "test-image:latest",
					UtilitiesImage: "utilities:latest",
					HasUtilities:   true,
				}

				deployment := adaptDeploymentForTest(t, cpo)

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
	})
}

func adaptDeploymentForTest(t *testing.T, cpo *ControlPlaneOperatorOptions) *appsv1.Deployment {
	t.Helper()
	g := NewWithT(t)

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "clusters-test-cluster",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra-id",
		},
	}

	cpContext := controlplanecomponent.WorkloadContext{
		Context: t.Context(),
		HCP:     hcp,
	}

	deployment, err := assets.LoadDeploymentManifest(ComponentName)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(cpo.adaptDeployment(cpContext, deployment)).To(Succeed())

	return deployment
}
