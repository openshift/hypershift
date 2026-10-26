//go:build e2ev2

package tests

import (
	"testing"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestComponentSupportsTLSConfig(t *testing.T) {
	t.Parallel()

	components := []struct {
		component      controlPlaneTLSComponent
		deploymentName string
		containerName  string
		flag           string
		unrelatedFlag  string
	}{
		{pkiOperatorTLSComponent, "control-plane-pki-operator", "control-plane-pki-operator", "--config=/etc/kubernetes/config/config.yaml", "--tls-min-version=VersionTLS13"},
		{awsPodIdentityWebhookTLSComponent, "kube-apiserver", "aws-pod-identity-webhook", "--tls-min-version=VersionTLS13", "--config=/etc/kubernetes/config/config.yaml"},
		{konnectivityServerTLSComponent, "kube-apiserver", "konnectivity-server", "--tls-min-version=VersionTLS12", "--config=/etc/kubernetes/config/config.yaml"},
	}
	for _, component := range components {
		t.Run(component.component.name, func(t *testing.T) {
			t.Parallel()
			testCases := []struct {
				name              string
				command           []string
				args              []string
				missingDeployment bool
				missingContainer  bool
				supported         bool
				errorSubstring    string
			}{
				{
					name:      "When the TLS flag is in Command, it should support profile configuration",
					command:   []string{component.containerName, component.flag},
					supported: true,
				},
				{
					name:      "When the TLS flag is in Args, it should support profile configuration without a ConfigMap",
					command:   []string{component.containerName},
					args:      []string{component.flag},
					supported: true,
				},
				{
					name: "When the TLS flag is absent, it should not require a profile-driven restart",
				},
				{
					name:    "When only unrelated flags are present, it should not require a profile-driven restart",
					command: []string{component.unrelatedFlag},
					args:    []string{"--other-flag=value"},
				},
				{
					name:              "When the deployment is missing, it should report the lookup error",
					missingDeployment: true,
					errorSubstring:    "failed to get " + component.deploymentName + " deployment",
				},
				{
					name:             "When the target container is missing, it should report an error instead of hiding a regression",
					missingContainer: true,
					errorSubstring:   "container " + component.containerName + " not found",
				},
			}
			for _, testCase := range testCases {
				t.Run(testCase.name, func(t *testing.T) {
					t.Parallel()
					assertions := NewWithT(t)
					scheme := runtime.NewScheme()
					assertions.Expect(appsv1.AddToScheme(scheme)).To(Succeed())
					deployment := &appsv1.Deployment{
						ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-tls", Name: component.deploymentName},
						Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{Name: "other-sidecar", Command: []string{component.flag}, Args: []string{component.flag}},
							},
						}}},
					}
					if !testCase.missingContainer {
						deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, corev1.Container{
							Name: component.containerName, Command: testCase.command, Args: testCase.args,
						})
					}
					builder := fake.NewClientBuilder().WithScheme(scheme)
					if !testCase.missingDeployment {
						builder.WithObjects(deployment)
					}
					supported, err := componentSupportsTLSConfig(t.Context(), builder.Build(), "clusters-tls", component.component)
					if testCase.errorSubstring != "" {
						assertions.Expect(err).To(MatchError(ContainSubstring(testCase.errorSubstring)))
						if testCase.missingDeployment {
							assertions.Expect(apierrors.IsNotFound(err)).To(BeTrue())
						}
					} else {
						assertions.Expect(err).NotTo(HaveOccurred())
					}
					assertions.Expect(supported).To(Equal(testCase.supported))
				})
			}
		})
	}
}
