package dnsoperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/testutil"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		validate func(*testing.T, component.WorkloadContext)
	}{
		{
			name: "When adapting deployment, it should set correct command",
			validate: func(t *testing.T, cpContext component.WorkloadContext) {
				g := NewWithT(t)
				deployment, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				err = adaptDeployment(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				dnsContainer := podspec.FindContainer("dns-operator", deployment.Spec.Template.Spec.Containers)

				g.Expect(dnsContainer).ToNot(BeNil())
				g.Expect(dnsContainer.Command).To(Equal([]string{"dns-operator"}))
			},
		},
		{
			name: "When adapting deployment, it should set ImagePullPolicy to IfNotPresent",
			validate: func(t *testing.T, cpContext component.WorkloadContext) {
				g := NewWithT(t)
				deployment, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				err = adaptDeployment(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				dnsContainer := podspec.FindContainer("dns-operator", deployment.Spec.Template.Spec.Containers)

				g.Expect(dnsContainer).ToNot(BeNil())
				g.Expect(dnsContainer.ImagePullPolicy).To(Equal(corev1.PullIfNotPresent))
			},
		},
		{
			name: "When adapting deployment, it should configure all required environment variables",
			validate: func(t *testing.T, cpContext component.WorkloadContext) {
				g := NewWithT(t)
				deployment, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				err = adaptDeployment(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				dnsContainer := podspec.FindContainer("dns-operator", deployment.Spec.Template.Spec.Containers)

				g.Expect(dnsContainer).ToNot(BeNil())
				g.Expect(dnsContainer.Env).To(HaveLen(5))

				envMap := make(map[string]string)
				for _, env := range dnsContainer.Env {
					envMap[env.Name] = env.Value
				}

				g.Expect(envMap).To(HaveKeyWithValue("RELEASE_VERSION", "4.18.0"))
				g.Expect(envMap).To(HaveKeyWithValue("IMAGE", "coredns"))
				g.Expect(envMap).To(HaveKeyWithValue("OPENSHIFT_CLI_IMAGE", "cli"))
				g.Expect(envMap).To(HaveKeyWithValue("KUBE_RBAC_PROXY_IMAGE", "kube-rbac-proxy"))
				g.Expect(envMap).To(HaveKeyWithValue("KUBECONFIG", "/etc/kubernetes/kubeconfig"))
			},
		},
		{
			name: "When adapting deployment, it should set correct resource requests",
			validate: func(t *testing.T, cpContext component.WorkloadContext) {
				g := NewWithT(t)
				deployment, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				err = adaptDeployment(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				dnsContainer := podspec.FindContainer("dns-operator", deployment.Spec.Template.Spec.Containers)

				g.Expect(dnsContainer).ToNot(BeNil())
				g.Expect(dnsContainer.Resources.Requests).To(HaveKeyWithValue(corev1.ResourceCPU, resource.MustParse("10m")))
				g.Expect(dnsContainer.Resources.Requests).To(HaveKeyWithValue(corev1.ResourceMemory, resource.MustParse("29Mi")))
			},
		},
		{
			name: "When adapting deployment, it should set TerminationMessagePolicy to FallbackToLogsOnError",
			validate: func(t *testing.T, cpContext component.WorkloadContext) {
				g := NewWithT(t)
				deployment, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				err = adaptDeployment(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				dnsContainer := podspec.FindContainer("dns-operator", deployment.Spec.Template.Spec.Containers)

				g.Expect(dnsContainer).ToNot(BeNil())
				g.Expect(dnsContainer.TerminationMessagePolicy).To(Equal(corev1.TerminationMessageFallbackToLogsOnError))
			},
		},
		{
			name: "When adapting deployment, it should set termination grace period to 2 seconds",
			validate: func(t *testing.T, cpContext component.WorkloadContext) {
				g := NewWithT(t)
				deployment, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				err = adaptDeployment(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				g.Expect(deployment.Spec.Template.Spec.TerminationGracePeriodSeconds).To(Equal(ptr.To[int64](2)))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			}

			cpContext := component.WorkloadContext{
				Context:                  t.Context(),
				HCP:                      hcp,
				UserReleaseImageProvider: testutil.FakeImageProvider(),
			}

			tc.validate(t, cpContext)
		})
	}
}
