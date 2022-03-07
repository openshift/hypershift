package nodepool

import (
	"time"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// These are copies pf metav1.Condition to accept hyperv1.NodePoolCondition

// setStatusCondition sets the corresponding condition in conditions to newCondition.
// conditions must be non-nil.
// 1. if the condition of the specified type already exists (all fields of the existing condition are updated to
//    newCondition, LastTransitionTime is set to now if the new status differs from the old status)
// 2. if a condition of the specified type does not exist (LastTransitionTime is set to now() if unset, and newCondition is appended)
func setStatusCondition(conditions *[]hyperv1.NodePoolCondition, newCondition hyperv1.NodePoolCondition) {
	if conditions == nil {
		return
	}
	existingCondition := findStatusCondition(*conditions, newCondition.Type)
	if existingCondition == nil {
		if newCondition.LastTransitionTime.IsZero() {
			newCondition.LastTransitionTime = metav1.NewTime(time.Now())
		}
		*conditions = append(*conditions, newCondition)
		return
	}

	if existingCondition.Status != newCondition.Status {
		existingCondition.Status = newCondition.Status
		if !newCondition.LastTransitionTime.IsZero() {
			existingCondition.LastTransitionTime = newCondition.LastTransitionTime
		} else {
			existingCondition.LastTransitionTime = metav1.NewTime(time.Now())
		}
	}

	existingCondition.Reason = newCondition.Reason
	existingCondition.Message = newCondition.Message
	existingCondition.ObservedGeneration = newCondition.ObservedGeneration
}

// removeStatusCondition removes the corresponding conditionType from conditions.
// conditions must be non-nil.
func removeStatusCondition(conditions *[]hyperv1.NodePoolCondition, conditionType string) {
	if conditions == nil || len(*conditions) == 0 {
		return
	}

	newConditions := make([]hyperv1.NodePoolCondition, 0, len(*conditions)-1)
	for _, condition := range *conditions {
		if condition.Type != conditionType {
			newConditions = append(newConditions, condition)
		}
	}

	*conditions = newConditions
}

// findStatusCondition finds the conditionType in conditions.
func findStatusCondition(conditions []hyperv1.NodePoolCondition, conditionType string) *hyperv1.NodePoolCondition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}

	return nil
}
