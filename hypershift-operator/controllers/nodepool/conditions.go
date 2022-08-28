package nodepool

import (
	"fmt"
	"strconv"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	reconciliationActiveConditionReason             = "ReconciliationActive"
	reconciliationPausedConditionReason             = "ReconciliationPaused"
	reconciliationInvalidPausedUntilConditionReason = "InvalidPausedUntilValue"
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

// generateReconciliationActiveCondition will generate the resource condition that reflects the state of reconciliation
// on the resource.
// (copied from support/util/pausereconcile_test.go and adjusted to use NodePoolCondition)
func generateReconciliationActiveCondition(pausedUntilField *string, objectGeneration int64) hyperv1.NodePoolCondition {
	isPaused, _, err := util.ProcessPausedUntilField(pausedUntilField, time.Now())
	var msgString string
	if isPaused {
		if _, err := strconv.ParseBool(*pausedUntilField); err == nil {
			msgString = "Reconciliation paused until field removed"
		} else {
			msgString = fmt.Sprintf("Reconciliation paused until: %s", *pausedUntilField)
		}
		return hyperv1.NodePoolCondition{
			Type:               string(hyperv1.NodePoolReconciliationActiveConditionType),
			Status:             corev1.ConditionFalse,
			Reason:             reconciliationPausedConditionReason,
			Message:            msgString,
			ObservedGeneration: objectGeneration,
		}
	}
	msgString = "Reconciliation active on resource"
	reasonString := reconciliationActiveConditionReason
	if err != nil {
		reasonString = reconciliationInvalidPausedUntilConditionReason
		msgString = "Invalid value provided for PausedUntil field"
	}
	return hyperv1.NodePoolCondition{
		Type:               string(hyperv1.NodePoolReconciliationActiveConditionType),
		Status:             corev1.ConditionTrue,
		Reason:             reasonString,
		Message:            msgString,
		ObservedGeneration: objectGeneration,
	}
}
