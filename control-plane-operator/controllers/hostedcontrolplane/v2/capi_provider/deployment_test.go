package capiprovider

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeploymentAWSCABundle(t *testing.T) {
	testCases := []struct {
		name            string
		platformType    hyperv1.PlatformType
		additionalTrust *corev1.LocalObjectReference
		expectVolume    bool
	}{
		{
			name:            "AWS with additional trust bundle",
			platformType:    hyperv1.AWSPlatform,
			additionalTrust: &corev1.LocalObjectReference{Name: "user-ca-bundle"},
			expectVolume:    true,
		},
		{
			name:            "AWS without additional trust bundle",
			platformType:    hyperv1.AWSPlatform,
			additionalTrust: nil,
			expectVolume:    false,
		},
		{
			name:            "non-AWS platform with additional trust bundle",
			platformType:    hyperv1.KubevirtPlatform,
			additionalTrust: &corev1.LocalObjectReference{Name: "user-ca-bundle"},
			expectVolume:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters-test-cluster",
					Annotations: map[string]string{
						"hypershift.openshift.io/cluster": "clusters/test-cluster",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: tc.platformType,
					},
					AdditionalTrustBundle: tc.additionalTrust,
				},
			}

			deploymentSpec := &appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "manager",
								Image: "test-image",
							},
						},
					},
				},
			}

			capi := &CAPIProviderOptions{
				deploymentSpec: deploymentSpec,
			}

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ComponentName,
					Namespace: hcp.Namespace,
				},
			}

			err := capi.adaptDeployment(cpContext, deployment)
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
					g.Expect(env.Value).To(Equal("/etc/pki/ca-trust/extracted/hypershift/user-ca-bundle.pem"))
					break
				}
			}
			g.Expect(hasEnvVar).To(Equal(tc.expectVolume))
		})
	}
}
