package operatorcontroller

import (
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/olmv1/util"
	component "github.com/openshift/hypershift/support/controlplane-component"
	appsv1 "k8s.io/api/apps/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	// Inject kubeconfig for hosted cluster API access
	// This adds:
	// - admin-kubeconfig volume mount
	// - KUBECONFIG environment variable (ctrl.GetConfigOrDie() uses this automatically)
	if err := util.InjectHostedClusterKubeconfig(cpContext, deployment); err != nil {
		return err
	}

	// No additional operator-controller-specific configuration needed
	// Service discovery will find catalogd in same namespace
	// operator-controller installs operators into hosted cluster worker nodes via hosted API

	return nil
}
