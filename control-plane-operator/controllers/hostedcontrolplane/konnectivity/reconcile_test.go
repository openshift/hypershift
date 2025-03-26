package konnectivity

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Ensure certain deployment fields do not get set
func TestReconcileKonnectivityAgentDeploymentNoChanges(t *testing.T) {

	imageName := "konnectivity-agent-image"
	// Setup expected values that are universal

	// Setup hypershift hosted control plane.
	targetNamespace := "test"
	konnectivityAgentDeployment := manifests.KonnectivityAgentDeployment(targetNamespace)
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
		deploymentConfig config.DeploymentConfig
		ips              []string
	}{
		// empty deployment config
		{
			deploymentConfig: config.DeploymentConfig{},
			ips:              []string{"1.2.3.4"},
		},
	}
	for _, tc := range testCases {
		g := NewGomegaWithT(t)
		konnectivityAgentDeployment.Spec.MinReadySeconds = 60
		expectedMinReadySeconds := konnectivityAgentDeployment.Spec.MinReadySeconds
		err := ReconcileAgentDeployment(konnectivityAgentDeployment, ownerRef, tc.deploymentConfig, imageName, tc.ips)
		g.Expect(err).To(BeNil())
		g.Expect(expectedMinReadySeconds).To(Equal(konnectivityAgentDeployment.Spec.MinReadySeconds))
	}
}
