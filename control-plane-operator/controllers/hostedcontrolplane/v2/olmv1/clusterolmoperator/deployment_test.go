package clusterolmoperator

import (
	"testing"

	. "github.com/onsi/gomega"

	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	g := NewWithT(t)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ComponentName,
			Namespace: "test-namespace",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  ComponentName,
							Image: "test-image",
						},
					},
				},
			},
		},
	}

	cpContext := component.WorkloadContext{
		UserReleaseImageProvider: &testutil.FakeReleaseImageProvider{Version: "4.17.0"},
	}

	err := adaptDeployment(cpContext, deployment)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify dual-API environment variables are set
	container := deployment.Spec.Template.Spec.Containers[0]
	var hostedKubeconfigFound, hypershiftModeFound, releaseVersionFound bool

	for _, env := range container.Env {
		switch env.Name {
		case "HOSTED_KUBECONFIG":
			hostedKubeconfigFound = true
			g.Expect(env.Value).To(Equal("/etc/openshift/kubeconfig/kubeconfig"))
		case "HYPERSHIFT_MODE":
			hypershiftModeFound = true
			g.Expect(env.Value).To(Equal("true"))
		case "RELEASE_VERSION":
			releaseVersionFound = true
			g.Expect(env.Value).To(Equal("4.17.0"))
		}
	}

	g.Expect(hostedKubeconfigFound).To(BeTrue(), "HOSTED_KUBECONFIG should be set")
	g.Expect(hypershiftModeFound).To(BeTrue(), "HYPERSHIFT_MODE should be set")
	g.Expect(releaseVersionFound).To(BeTrue(), "RELEASE_VERSION should be set")

	// Verify kubeconfig volume mount exists (added by olm.InjectHostedClusterKubeconfig)
	var kubeconfigVolumeFound bool
	for _, vol := range deployment.Spec.Template.Spec.Volumes {
		if vol.Name == "kubeconfig" {
			kubeconfigVolumeFound = true
			g.Expect(vol.Secret).ToNot(BeNil())
			g.Expect(vol.Secret.SecretName).To(Equal("admin-kubeconfig"))
		}
	}
	g.Expect(kubeconfigVolumeFound).To(BeTrue(), "kubeconfig volume should be mounted")
}
