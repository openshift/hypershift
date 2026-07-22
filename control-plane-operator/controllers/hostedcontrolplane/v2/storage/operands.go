package storage

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func reconcileOperandTolerations(cpContext component.ControlPlaneContext) error {
	var operandDeploymentNames []string
	switch cpContext.HCP.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		operandDeploymentNames = []string{
			"aws-ebs-csi-driver-operator",
			"aws-ebs-csi-driver-controller",
		}
	case hyperv1.AzurePlatform:
		operandDeploymentNames = []string{
			"azure-disk-csi-driver-operator",
			"azure-disk-csi-driver-controller",
			"azure-file-csi-driver-operator",
			"azure-file-csi-driver-controller",
		}
	default:
		return nil
	}

	desiredTolerations := component.ControlPlaneTolerations(cpContext.HCP)

	for _, name := range operandDeploymentNames {
		deployment := &appsv1.Deployment{}
		if err := cpContext.Client.Get(cpContext, client.ObjectKey{
			Namespace: cpContext.HCP.Namespace,
			Name:      name,
		}, deployment); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("failed to get operand deployment %s: %w", name, err)
		}

		if equality.Semantic.DeepEqual(deployment.Spec.Template.Spec.Tolerations, desiredTolerations) {
			continue
		}

		oldDeployment := deployment.DeepCopy()
		deployment.Spec.Template.Spec.Tolerations = desiredTolerations
		if err := cpContext.Client.Patch(cpContext, deployment, client.MergeFrom(oldDeployment)); err != nil {
			return fmt.Errorf("failed to patch tolerations for operand deployment %s: %w", name, err)
		}
	}

	return nil
}
