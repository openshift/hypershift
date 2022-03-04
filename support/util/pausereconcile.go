package util

import (
	"fmt"
	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strconv"
	"time"
)

const (
	reconciliationActiveConditionReason             = "ReconciliationActive"
	reconciliationPausedConditionReason             = "ReconciliationPaused"
	reconciliationInvalidPausedUntilConditionReason = "InvalidPausedUntilValue"
)

// IsReconciliationPaused checks the pauseUntil field to see if reconciliation on the resource should be
// paused.
func IsReconciliationPaused(logr logr.Logger, pausedUntilField *string) bool {
	isReconciliationPaused, _ := processPausedUntilField(logr, pausedUntilField)
	return isReconciliationPaused
}

// processPausedUntilField checks the pauseUntil field to see if reconciliation on the resource should be
// paused. It can either be a date in RFC3339 format, or a boolean. If it is a date: reconciliation will be paused until
// that date. If it is a true boolean value: it will be paused until the annotation is removed. If an improper value
// is specified: it will be treated as not paused. The second return boolean signifies if the user passed valid
// input to the pausedUntil field.
func processPausedUntilField(logr logr.Logger, pausedUntilField *string) (bool, bool) {
	if pausedUntilField == nil {
		return false, true
	}
	pausedUntilVal := *pausedUntilField
	isPausedBoolean, boolParseErr := strconv.ParseBool(pausedUntilVal)
	t, timeParseErr := time.Parse(time.RFC3339, pausedUntilVal)
	if boolParseErr == nil {
		return isPausedBoolean, true
	}
	if timeParseErr == nil {
		return time.Now().Before(t), true
	}
	logr.Info("Improper value specified in pausedUntil field. Considering the resource as not paused.", "value", pausedUntilVal)
	return false, false
}

// GenerateReconciliationPausedCondition will generate the resource condition that reflects the state of reconciliation
// on the resource.
func GenerateReconciliationPausedCondition(pausedUntilField *string, objectGeneration int64) metav1.Condition {
	isPaused, isValidValue := processPausedUntilField(logr.Discard(), pausedUntilField)
	var msgString string
	if isPaused {
		if _, err := strconv.ParseBool(*pausedUntilField); err == nil {
			msgString = "Reconciliation paused until field removed"
		} else {
			msgString = fmt.Sprintf("Reconciliation paused until: %s", *pausedUntilField)
		}
		return metav1.Condition{
			Type:               string(hyperv1.ReconciliationPaused),
			Status:             metav1.ConditionTrue,
			Reason:             reconciliationPausedConditionReason,
			Message:            msgString,
			ObservedGeneration: objectGeneration,
		}
	}
	msgString = "Reconciliation active on resource"
	reasonString := reconciliationActiveConditionReason
	if !isValidValue {
		reasonString = reconciliationInvalidPausedUntilConditionReason
		msgString = "Invalid value provided for PausedUntil field"
	}
	return metav1.Condition{
		Type:               string(hyperv1.ReconciliationPaused),
		Status:             metav1.ConditionFalse,
		Reason:             reasonString,
		Message:            msgString,
		ObservedGeneration: objectGeneration,
	}
}
