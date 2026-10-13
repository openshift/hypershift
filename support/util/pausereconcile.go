package util

import (
	"fmt"
	"strconv"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-logr/logr"
)

// IsReconciliationPaused checks the pauseUntil field to see if reconciliation on the resource should be
// paused and for how long.
func IsReconciliationPaused(logr logr.Logger, pausedUntilField *string) (bool, time.Duration) {
	now := time.Now()
	isReconciliationPaused, duration, err := ProcessPausedUntilField(pausedUntilField, now)
	if err != nil {
		// We ignore error and consider an invalid input as not paused.
		logr.Error(err, "error processing .pausedUntil field")
	}
	return isReconciliationPaused, duration
}

// ProcessPausedUntilField checks the pauseUntil field to see if reconciliation on the resource should be
// paused. Input can either be a date in RFC3339 format, or a Boolean.
// It returns a Boolean isPaused, a duration and an error.
// If the input is a date, a duration!=0 is returned so consumers can choose to requeueAfter it.
func ProcessPausedUntilField(pausedUntilField *string, now time.Time) (isPaused bool, duration time.Duration, err error) {
	if pausedUntilField == nil {
		return false, time.Duration(0), nil
	}
	pausedUntilVal := *pausedUntilField

	isPaused, err = strconv.ParseBool(pausedUntilVal)
	if err == nil {
		return isPaused, time.Duration(0), nil
	}

	t, err := time.Parse(time.RFC3339, pausedUntilVal)
	if err == nil {
		return now.Before(t), t.Sub(now), nil
	}

	return false, time.Duration(0), fmt.Errorf("invalid value specified in pausedUntil field: %q. Considering the resource as not paused", pausedUntilVal)
}

// GenerateReconciliationActiveCondition will generate the resource condition that reflects the state of reconciliation
// on the resource.
func GenerateReconciliationActiveCondition(pausedUntilField *string, objectGeneration int64) metav1.Condition {
	isPaused, _, err := ProcessPausedUntilField(pausedUntilField, time.Now())
	var msgString string
	if isPaused {
		if _, err := strconv.ParseBool(*pausedUntilField); err == nil {
			msgString = "Reconciliation paused until field removed"
		} else {
			msgString = fmt.Sprintf("Reconciliation paused until: %s", *pausedUntilField)
		}
		return metav1.Condition{
			Type:               string(hyperv1.ReconciliationActive),
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.ReconciliationPausedConditionReason,
			Message:            msgString,
			ObservedGeneration: objectGeneration,
		}
	}
	msgString = "Reconciliation active on resource"
	reasonString := hyperv1.AsExpectedReason
	if err != nil {
		reasonString = hyperv1.ReconciliationInvalidPausedUntilConditionReason
		msgString = "Invalid value provided for PausedUntil field"
	}
	return metav1.Condition{
		Type:               string(hyperv1.ReconciliationActive),
		Status:             metav1.ConditionTrue,
		Reason:             reasonString,
		Message:            msgString,
		ObservedGeneration: objectGeneration,
	}
}
