package consoleoperator

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

	// Verify HTTP_PROXY is set correctly
	container := deployment.Spec.Template.Spec.Containers[0]
	var httpProxyFound, httpsProxyFound, noProxyFound, releaseVersionFound bool
	expectedSocks5URL := "socks5://127.0.0.1:8090"

	for _, env := range container.Env {
		switch env.Name {
		case "HTTP_PROXY":
			httpProxyFound = true
			g.Expect(env.Value).To(Equal(expectedSocks5URL))
		case "HTTPS_PROXY":
			httpsProxyFound = true
			g.Expect(env.Value).To(Equal(expectedSocks5URL))
		case "NO_PROXY":
			noProxyFound = true
			g.Expect(env.Value).To(ContainSubstring("kube-apiserver"))
			g.Expect(env.Value).To(ContainSubstring(".svc"))
			g.Expect(env.Value).To(ContainSubstring("localhost"))
		case "RELEASE_VERSION":
			releaseVersionFound = true
			g.Expect(env.Value).To(Equal("4.17.0"))
		}
	}

	g.Expect(httpProxyFound).To(BeTrue(), "HTTP_PROXY should be set")
	g.Expect(httpsProxyFound).To(BeTrue(), "HTTPS_PROXY should be set")
	g.Expect(noProxyFound).To(BeTrue(), "NO_PROXY should be set")
	g.Expect(releaseVersionFound).To(BeTrue(), "RELEASE_VERSION should be set")
}
