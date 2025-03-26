package scheduler

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Ensure certain deployment fields do not get set
func TestReconcileSchedulerDeploymentNoChanges(t *testing.T) {

	// Setup expected values that are universal
	imageName := "schedulerImage"

	// Setup hypershift hosted control plane.
	targetNamespace := "test"
	schedulerDeployment := manifests.SchedulerDeployment(targetNamespace)
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: targetNamespace,
		},
	}
	hcp.Name = "name"
	hcp.Namespace = "namespace"
	schedulerConfig := manifests.SchedulerConfig(hcp.Namespace)
	ownerRef := config.OwnerRefFrom(hcp)
	testCases := []struct {
		deploymentConfig config.DeploymentConfig
		params           KubeSchedulerParams
		featureGates     []string
		ciphers          []string
	}{
		// empty deployment config
		{
			deploymentConfig: config.DeploymentConfig{},
			params: KubeSchedulerParams{
				DisableProfiling:        false,
				AvailabilityProberImage: "availability-prober",
			},
		},
	}
	for _, tc := range testCases {
		g := NewGomegaWithT(t)
		schedulerDeployment.Spec.MinReadySeconds = 60
		expectedMinReadySeconds := schedulerDeployment.Spec.MinReadySeconds
		err := ReconcileDeployment(schedulerDeployment, ownerRef, tc.deploymentConfig, imageName,
			tc.params.FeatureGates(), tc.params.SchedulerPolicy(), tc.params.AvailabilityProberImage,
			tc.params.CipherSuites(), tc.params.MinTLSVersion(), tc.params.DisableProfiling, schedulerConfig, hyperv1.IBMCloudPlatform)
		g.Expect(err).To(BeNil())
		g.Expect(expectedMinReadySeconds).To(Equal(schedulerDeployment.Spec.MinReadySeconds))
	}
}
