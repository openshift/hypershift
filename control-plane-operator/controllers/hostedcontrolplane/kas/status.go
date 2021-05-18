package kas

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	hcputil "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

func ReconcileKubeAPIServerDeploymentStatus(ctx context.Context, hcpStatus *hyperv1.HostedControlPlaneStatus, deployment *appsv1.Deployment) {
	log := ctrl.LoggerFrom(ctx)
	if deployment == nil {
		log.Info("Kube APIServer deployment doesn't exist yet")
		hcputil.SetConditionByType(&hcpStatus.Conditions, hyperv1.KubeAPIServerAvailable, hyperv1.ConditionFalse, "NotCreated", "Kube APIServer deployment is not yet created")
		return
	}
	availableCondition := hcputil.DeploymentConditionByType(deployment, appsv1.DeploymentAvailable)
	if availableCondition != nil && availableCondition.Status == corev1.ConditionTrue &&
		deployment.Status.AvailableReplicas > 0 {
		hcputil.SetConditionByType(&hcpStatus.Conditions, hyperv1.KubeAPIServerAvailable, hyperv1.ConditionTrue, "Running", "Kube APIServer is running and available")
	} else {
		hcputil.SetConditionByType(&hcpStatus.Conditions, hyperv1.KubeAPIServerAvailable, hyperv1.ConditionFalse, "ScalingUp", "Kube APIServer is not yet ready")
	}
}
