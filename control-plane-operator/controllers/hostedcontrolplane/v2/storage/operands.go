package storage

import (
	"fmt"

	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func reconcileOperandTolerations(cpContext component.ControlPlaneContext) error {
	operands := storageOperands(cpContext.HCP.Spec.Platform.Type)
	if len(operands) == 0 {
		return nil
	}

	desiredTolerations := component.ControlPlaneTolerations(cpContext.HCP)

	for _, op := range operands {
		deployment := &appsv1.Deployment{}
		if err := cpContext.Client.Get(cpContext, client.ObjectKey{
			Namespace: cpContext.HCP.Namespace,
			Name:      op.DeploymentName,
		}, deployment); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("failed to get operand deployment %s: %w", op.DeploymentName, err)
		}

		if equality.Semantic.DeepEqual(deployment.Spec.Template.Spec.Tolerations, desiredTolerations) {
			continue
		}

		oldDeployment := deployment.DeepCopy()
		deployment.Spec.Template.Spec.Tolerations = desiredTolerations
		if err := cpContext.Client.Patch(cpContext, deployment, client.MergeFrom(oldDeployment)); err != nil {
			return fmt.Errorf("failed to patch tolerations for operand deployment %s: %w", op.DeploymentName, err)
		}
	}

	return nil
}
