package machineapprover

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()
	t.Run("When a custom TLS profile is missing its configuration, it should report an error", func(t *testing.T) {
		g := NewWithT(t)
		deployment, err := assets.LoadDeploymentManifest(ComponentName)
		g.Expect(err).NotTo(HaveOccurred())
		cpContext := component.WorkloadContext{HCP: &hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{
			Configuration: &hyperv1.ClusterConfiguration{APIServer: &configv1.APIServerSpec{
				TLSSecurityProfile: &configv1.TLSSecurityProfile{Type: configv1.TLSProfileCustomType},
			}},
		}}}
		g.Expect(adaptDeployment(cpContext, deployment)).NotTo(Succeed())
	})

	tests := []struct {
		name              string
		namespace         string
		initialContainers []corev1.Container
		validate          func(*testing.T, *appsv1.Deployment)
	}{
		{
			name:      "When deployment has machine-approver container, it should add machine-namespace arg",
			namespace: "test-namespace",
			initialContainers: []corev1.Container{
				{
					Name: ComponentName,
					Args: []string{"--existing-arg=value"},
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment) {
				g := NewWithT(t)
				g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
				container := deployment.Spec.Template.Spec.Containers[0]
				g.Expect(container.Name).To(Equal(ComponentName))
				g.Expect(container.Command[:2]).To(Equal([]string{"/bin/bash", "-c"}))
				g.Expect(container.Args[:3]).To(Equal([]string{"2", "/usr/bin/machine-approver", "--help"}))
				g.Expect(container.Args).To(ContainElement(HavePrefix("--tls-curve-preferences=")))
				g.Expect(container.Args).To(ContainElement("--existing-arg=value"))
				g.Expect(container.Args).To(ContainElement("--machine-namespace=test-namespace"))
			},
		},
		{
			name:      "When deployment has machine-approver container with no args, it should add machine-namespace arg",
			namespace: "another-namespace",
			initialContainers: []corev1.Container{
				{
					Name: ComponentName,
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment) {
				g := NewWithT(t)
				g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
				container := deployment.Spec.Template.Spec.Containers[0]
				g.Expect(container.Name).To(Equal(ComponentName))
				g.Expect(container.Args).To(ContainElement("--machine-namespace=another-namespace"))
			},
		},
		{
			name:      "When deployment has multiple containers, it should only modify machine-approver container",
			namespace: "test-namespace",
			initialContainers: []corev1.Container{
				{
					Name: "other-container",
					Args: []string{"--other-arg=value"},
				},
				{
					Name: ComponentName,
					Args: []string{"--existing-arg=value"},
				},
				{
					Name: "another-container",
					Args: []string{"--another-arg=value"},
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment) {
				g := NewWithT(t)
				g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(3))

				// First container should be unchanged
				g.Expect(deployment.Spec.Template.Spec.Containers[0].Name).To(Equal("other-container"))
				g.Expect(deployment.Spec.Template.Spec.Containers[0].Args).To(Equal([]string{"--other-arg=value"}))

				// Machine approver container should have the new arg
				g.Expect(deployment.Spec.Template.Spec.Containers[1].Name).To(Equal(ComponentName))
				g.Expect(deployment.Spec.Template.Spec.Containers[1].Args).To(ContainElement("--existing-arg=value"))
				g.Expect(deployment.Spec.Template.Spec.Containers[1].Args).To(ContainElement("--machine-namespace=test-namespace"))

				// Third container should be unchanged
				g.Expect(deployment.Spec.Template.Spec.Containers[2].Name).To(Equal("another-container"))
				g.Expect(deployment.Spec.Template.Spec.Containers[2].Args).To(Equal([]string{"--another-arg=value"}))
			},
		},
		{
			name:      "When deployment has no machine-approver container, it should not modify any containers",
			namespace: "test-namespace",
			initialContainers: []corev1.Container{
				{
					Name: "other-container",
					Args: []string{"--other-arg=value"},
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment) {
				g := NewWithT(t)
				g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
				g.Expect(deployment.Spec.Template.Spec.Containers[0].Name).To(Equal("other-container"))
				g.Expect(deployment.Spec.Template.Spec.Containers[0].Args).To(Equal([]string{"--other-arg=value"}))
			},
		},
		{
			name:      "When deployment has empty namespace, it should add empty machine-namespace arg",
			namespace: "",
			initialContainers: []corev1.Container{
				{
					Name: ComponentName,
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment) {
				g := NewWithT(t)
				g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
				container := deployment.Spec.Template.Spec.Containers[0]
				g.Expect(container.Args).To(ContainElement("--machine-namespace="))
			},
		},
		{
			name:      "When namespace has special characters, it should correctly format the arg",
			namespace: "test-namespace-with-dashes_and_underscores",
			initialContainers: []corev1.Container{
				{
					Name: ComponentName,
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment) {
				g := NewWithT(t)
				g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
				container := deployment.Spec.Template.Spec.Containers[0]
				g.Expect(container.Args).To(ContainElement("--machine-namespace=test-namespace-with-dashes_and_underscores"))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: tc.namespace,
				},
			}

			containers := make([]corev1.Container, len(tc.initialContainers))
			for i := range tc.initialContainers {
				containers[i] = *tc.initialContainers[i].DeepCopy()
				if containers[i].Name == ComponentName {
					containers[i].Command = []string{"/usr/bin/machine-approver"}
				}
			}

			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-deployment",
					Namespace: tc.namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: containers,
						},
					},
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			err := adaptDeployment(cpContext, deployment)

			g.Expect(err).ToNot(HaveOccurred())
			tc.validate(t, deployment)
		})
	}
}
