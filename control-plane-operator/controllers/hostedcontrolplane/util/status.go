package util

import (
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

func SetConditionByType(conditions *[]hyperv1.HostedControlPlaneCondition, conditionType hyperv1.ConditionType, status hyperv1.ConditionStatus, reason, message string) {
	existingCondition := GetConditionByType(*conditions, conditionType)
	if existingCondition == nil {
		newCondition := hyperv1.HostedControlPlaneCondition{
			Type:               conditionType,
			Status:             status,
			Reason:             reason,
			Message:            message,
			LastTransitionTime: metav1.Now(),
		}
		*conditions = append(*conditions, newCondition)
	} else {
		if existingCondition.Status != status {
			existingCondition.LastTransitionTime = metav1.Now()
		}
		existingCondition.Status = status
		existingCondition.Reason = reason
		existingCondition.Message = message
	}
}

func GetConditionByType(conditions []hyperv1.HostedControlPlaneCondition, conditionType hyperv1.ConditionType) *hyperv1.HostedControlPlaneCondition {
	for k, v := range conditions {
		if v.Type == conditionType {
			return &conditions[k]
		}
	}
	return nil
}

func DeploymentConditionByType(deployment *appsv1.Deployment, conditionType appsv1.DeploymentConditionType) *appsv1.DeploymentCondition {
	for i, c := range deployment.Status.Conditions {
		if c.Type == conditionType {
			return &deployment.Status.Conditions[i]
		}
	}
	return nil
}
