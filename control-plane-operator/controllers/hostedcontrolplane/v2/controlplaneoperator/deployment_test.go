package controlplaneoperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/rhobsmonitoring"

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

func TestAdaptDeploymentWatchListClientEnv(t *testing.T) {
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

			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
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
				Name:  "KUBE_FEATURE_WatchListClient",
				Value: tc.envValue,
			}

			if tc.expectPresent {
				g.Expect(envVars).To(ContainElement(expectedEnvVar))
			} else {
				g.Expect(envVars).ToNot(ContainElement(HaveField("Name", "KUBE_FEATURE_WatchListClient")))
			}
		})
	}
}

func TestAdaptDeployment(t *testing.T) {
	for _, tc := range []struct {
		name, group, rhobs string
		wantGroup          bool
		wantErr            bool
	}{
		{name: "When GMP is selected, it should propagate the monitoring API group", group: string(metrics.MonitoringAPIGroupGMP), wantGroup: true},
		{name: "When monitoring selection is unset, it should preserve the default environment"},
		{name: "When CoreOS is explicit, it should preserve the default environment", group: string(metrics.MonitoringAPIGroupCoreOS)},
		{name: "When RHOBS uses CoreOS selection, it should preserve the RHOBS environment", group: string(metrics.MonitoringAPIGroupCoreOS), rhobs: "1"},
		{name: "When GMP conflicts with RHOBS, it should fail before mutating the deployment", group: string(metrics.MonitoringAPIGroupGMP), rhobs: "1", wantErr: true},
		{name: "When the monitoring API group is unsupported, it should fail before mutating the deployment", group: "unsupported", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			t.Setenv(metrics.MonitoringAPIGroupEnvVar, tc.group)
			t.Setenv(rhobsmonitoring.EnvironmentVariable, tc.rhobs)
			cpo := &ControlPlaneOperatorOptions{HostedCluster: &hyperv1.HostedCluster{}, Image: "quay.io/openshift-release-dev/ocp-v4.0-art-dev:control-plane-operator"}
			cpContext := controlplanecomponent.WorkloadContext{Context: t.Context(), HCP: &hyperv1.HostedControlPlane{}}
			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).NotTo(HaveOccurred())
			if tc.wantErr {
				g.Expect(cpo.adaptDeployment(cpContext, deployment)).NotTo(Succeed())
				// The invalid selection must fail fast without stamping the group onto the deployment.
				g.Expect(deployment.Spec.Template.Spec.Containers[0].Env).NotTo(ContainElement(HaveField("Name", metrics.MonitoringAPIGroupEnvVar)))
				return
			}
			g.Expect(cpo.adaptDeployment(cpContext, deployment)).To(Succeed())
			env := deployment.Spec.Template.Spec.Containers[0].Env
			if tc.wantGroup {
				g.Expect(env).To(ContainElement(corev1.EnvVar{Name: metrics.MonitoringAPIGroupEnvVar, Value: tc.group}))
			} else {
				g.Expect(env).NotTo(ContainElement(HaveField("Name", metrics.MonitoringAPIGroupEnvVar)))
			}
			if tc.rhobs == "1" {
				g.Expect(env).To(ContainElement(corev1.EnvVar{Name: rhobsmonitoring.EnvironmentVariable, Value: "1"}))
			}
		})
	}
}
