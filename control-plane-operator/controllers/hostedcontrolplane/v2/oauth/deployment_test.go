package oauth

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	const testNamespace = "clusters-test"

	tests := []struct {
		name         string
		platformType hyperv1.PlatformType
		wantNoProxy  []string
	}{
		{
			name:         "When platform is AWS, it should set NO_PROXY with kube-apiserver, audit-webhook, and oauth in-cluster DNS",
			platformType: hyperv1.AWSPlatform,
			wantNoProxy: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
				getOAuthServiceDNS(testNamespace),
			},
		},
		{
			name:         "When platform is Azure, it should set NO_PROXY with kube-apiserver, audit-webhook, and oauth in-cluster DNS",
			platformType: hyperv1.AzurePlatform,
			wantNoProxy: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
				getOAuthServiceDNS(testNamespace),
			},
		},
		{
			name:         "When platform is None, it should set NO_PROXY with kube-apiserver, audit-webhook, and oauth in-cluster DNS",
			platformType: hyperv1.NonePlatform,
			wantNoProxy: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
				getOAuthServiceDNS(testNamespace),
			},
		},
		{
			name:         "When platform is IBMCloud, it should also include IAM endpoints in NO_PROXY",
			platformType: hyperv1.IBMCloudPlatform,
			wantNoProxy: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
				getOAuthServiceDNS(testNamespace),
				"iam.cloud.ibm.com",
				"iam.test.cloud.ibm.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: tt.platformType,
					},
				},
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			cpContext := component.WorkloadContext{
				Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
				HCP:    hcp,
			}

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(container).ToNot(BeNil())

			noProxyEnv := podspec.FindEnvVar("NO_PROXY", container.Env)
			g.Expect(noProxyEnv).ToNot(BeNil())

			noProxyEntries := strings.Split(noProxyEnv.Value, ",")
			for _, entry := range tt.wantNoProxy {
				g.Expect(noProxyEntries).To(ContainElement(entry), "expected NO_PROXY to contain %q", entry)
			}

			// Ensure no unexpected IBM entries for non-IBM platforms
			if tt.platformType != hyperv1.IBMCloudPlatform {
				g.Expect(noProxyEnv.Value).ToNot(ContainSubstring("iam.cloud.ibm.com"))
				g.Expect(noProxyEnv.Value).ToNot(ContainSubstring("iam.test.cloud.ibm.com"))
			}
		})
	}
}
