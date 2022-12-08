package clusterpolicy

import (
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

func TestReconcileClusterPolicyDeployment(t *testing.T) {

	imageName := "clusterPolicyImage"
	// Setup expected values that are universal

	// Setup hypershift hosted control plane.
	targetNamespace := "test"
	clusterPolicyDeployment := manifests.ClusterPolicyControllerDeployment(targetNamespace)
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
	}{
		// empty deployment config
		{
			deploymentConfig: config.DeploymentConfig{},
		},
	}
	for _, tc := range testCases {
		g := NewGomegaWithT(t)
		clusterPolicyDeployment.Spec.Template.Spec.TerminationGracePeriodSeconds = pointer.Int64(60)
		expectedTermGraceSeconds := clusterPolicyDeployment.Spec.Template.Spec.TerminationGracePeriodSeconds
		clusterPolicyDeployment.Spec.MinReadySeconds = 60
		expectedMinReadySeconds := clusterPolicyDeployment.Spec.MinReadySeconds
		err := ReconcileDeployment(clusterPolicyDeployment, ownerRef, imageName, tc.deploymentConfig, util.AvailabilityProberImageName, pointer.Int32(1234))
		g.Expect(err).To(BeNil())
		g.Expect(expectedTermGraceSeconds).To(Equal(clusterPolicyDeployment.Spec.Template.Spec.TerminationGracePeriodSeconds))
		g.Expect(expectedMinReadySeconds).To(Equal(clusterPolicyDeployment.Spec.MinReadySeconds))
	}
}
