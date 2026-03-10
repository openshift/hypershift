package aws

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeploymentTrustBundle(t *testing.T) {
	testCases := []struct {
		name                string
		additionalTrust     *corev1.LocalObjectReference
		expectVolume        bool
		expectEnvVar        bool
		expectedEnvVarValue string
	}{
		{
			name:            "no additional trust bundle",
			additionalTrust: nil,
			expectVolume:    false,
			expectEnvVar:    false,
		},
		{
			name:                "with additional trust bundle",
			additionalTrust:     &corev1.LocalObjectReference{Name: "user-ca-bundle"},
			expectVolume:        true,
			expectEnvVar:        true,
			expectedEnvVarValue: "/etc/pki/ca-trust/extracted/hypershift/user-ca-bundle.pem",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters-test-cluster",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
					AdditionalTrustBundle: tc.additionalTrust,
				},
			}

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			container := deployment.Spec.Template.Spec.Containers[0]
			volumes := deployment.Spec.Template.Spec.Volumes

			hasVolume := false
			for _, v := range volumes {
				if v.Name == "aws-ca-bundle" {
					hasVolume = true
					break
				}
			}
			g.Expect(hasVolume).To(Equal(tc.expectVolume))

			hasMount := false
			for _, vm := range container.VolumeMounts {
				if vm.Name == "aws-ca-bundle" {
					hasMount = true
					g.Expect(vm.MountPath).To(Equal("/etc/pki/ca-trust/extracted/hypershift"))
					g.Expect(vm.ReadOnly).To(BeTrue())
					break
				}
			}
			g.Expect(hasMount).To(Equal(tc.expectVolume))

			hasEnvVar := false
			for _, env := range container.Env {
				if env.Name == "AWS_CA_BUNDLE" {
					hasEnvVar = true
					g.Expect(env.Value).To(Equal(tc.expectedEnvVarValue))
					break
				}
			}
			g.Expect(hasEnvVar).To(Equal(tc.expectEnvVar))
		})
	}
}
