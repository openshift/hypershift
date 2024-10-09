package ocm

import (
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// Ensure certain deployment fields do not get set
func TestReconcileOpenshiftControllerManagerDeploymentNoChanges(t *testing.T) {

	// Setup expected values that are universal
	imageName := "ocmImage"

	// Setup hypershift hosted control plane.
	targetNamespace := "test"
	ocmDeployment := manifests.OpenShiftControllerManagerDeployment(targetNamespace)
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
		cm               corev1.ConfigMap
		deploymentConfig config.DeploymentConfig
	}{
		// empty deployment config
		{
			cm: corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ocm-config",
					Namespace: targetNamespace,
				},
				Data: map[string]string{"config.yaml": "test-data"},
			},
			deploymentConfig: config.DeploymentConfig{},
		},
	}
	for _, tc := range testCases {
		g := NewGomegaWithT(t)
		ocmDeployment.Spec.Template.Spec.TerminationGracePeriodSeconds = ptr.To[int64](60)
		expectedTermGraceSeconds := ocmDeployment.Spec.Template.Spec.TerminationGracePeriodSeconds
		ocmDeployment.Spec.MinReadySeconds = 60
		expectedMinReadySeconds := ocmDeployment.Spec.MinReadySeconds
		err := ReconcileDeployment(ocmDeployment, ownerRef, imageName, &tc.cm, tc.deploymentConfig)
		g.Expect(err).To(BeNil())
		g.Expect(expectedTermGraceSeconds).To(Equal(ocmDeployment.Spec.Template.Spec.TerminationGracePeriodSeconds))
		g.Expect(expectedMinReadySeconds).To(Equal(ocmDeployment.Spec.MinReadySeconds))
	}
}
