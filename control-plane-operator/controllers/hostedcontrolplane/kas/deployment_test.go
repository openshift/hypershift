package kas

import (
	"golang.org/x/net/context"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Ensure certain deployment fields do not get set
func TestReconcileKubeAPIServerDeploymentNoChanges(t *testing.T) {

	// Setup expected values that are universal

	// Setup hypershift hosted control plane.
	targetNamespace := "test"
	kubeAPIDeployment := manifests.KASDeployment(targetNamespace)

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: targetNamespace,
		},
	}
	hcp.Name = "name"
	hcp.Namespace = "namespace"
	ownerRef := config.OwnerRefFrom(hcp)

	testCases := []struct {
		config           *corev1.ConfigMap
		auditConfig      *corev1.ConfigMap
		authConfig       *corev1.ConfigMap
		deploymentConfig config.DeploymentConfig
		params           KubeAPIServerParams
		activeKey        []byte
		backupKey        []byte
	}{
		// empty deployment config
		{
			config:           manifests.OpenShiftAPIServerConfig(targetNamespace),
			auditConfig:      manifests.OpenShiftAPIServerAuditConfig(targetNamespace),
			authConfig:       manifests.AuthConfig(targetNamespace),
			deploymentConfig: config.DeploymentConfig{},
			params: KubeAPIServerParams{
				CloudProvider: "test-cloud-provider",
			},
		},
	}
	for _, tc := range testCases {
		g := NewGomegaWithT(t)
		kubeAPIDeployment.Spec.MinReadySeconds = 60
		expectedMinReadySeconds := kubeAPIDeployment.Spec.MinReadySeconds
		tc.config.Data = map[string]string{"config.json": "test-json"}
		tc.auditConfig.Data = map[string]string{"policy.yaml": "test-data"}
		tc.authConfig.Data = map[string]string{"auth.json": "test-data"}
		err := ReconcileKubeAPIServerDeployment(context.TODO(), nil, kubeAPIDeployment, hcp, ownerRef, tc.deploymentConfig, tc.params.NamedCertificates(), tc.params.CloudProvider,
			tc.params.CloudProviderConfig, tc.params.CloudProviderCreds, tc.params.Images, tc.config, tc.auditConfig, tc.authConfig, tc.params.AuditWebhookRef, tc.activeKey, tc.backupKey, 6443, "test-payload-version", tc.params.FeatureGate, nil, tc.params.CipherSuites())
		g.Expect(err).To(BeNil())
		g.Expect(expectedMinReadySeconds).To(Equal(kubeAPIDeployment.Spec.MinReadySeconds))
	}
}
