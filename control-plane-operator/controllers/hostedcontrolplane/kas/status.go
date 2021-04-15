package kas

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	hcputil "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

func ReconcileKubeAPIServerDeploymentStatus(ctx context.Context, c client.Client, hcp *hyperv1.HostedControlPlane, deployment *appsv1.Deployment) error {
	log := ctrl.LoggerFrom(ctx)
	if deployment == nil {
		log.Info("Kube APIServer deployment doesn't exist yet")
		return nil
	}
	availableCondition := hcputil.DeploymentConditionByType(deployment, appsv1.DeploymentAvailable)
	if availableCondition != nil && availableCondition.Status == corev1.ConditionTrue &&
		deployment.Status.AvailableReplicas > 0 {
		hcputil.SetConditionByType(&hcp.Status.Conditions, hyperv1.KubeAPIServerAvailable, hyperv1.ConditionTrue, "Running", "Kube APIServer is running and available")
	} else {
		hcputil.SetConditionByType(&hcp.Status.Conditions, hyperv1.KubeAPIServerAvailable, hyperv1.ConditionFalse, "ScalingUp", "Kube APIServer is not yet ready")
	}
	if err := c.Status().Update(ctx, hcp); err != nil {
		return err
	}
	return nil
}
