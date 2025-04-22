package util

import (
	batchv1 "k8s.io/api/batch/v1"
)

func FindJobCondition(object *batchv1.Job, conditionType batchv1.JobConditionType) *batchv1.JobCondition {
	for i := range object.Status.Conditions {
		if object.Status.Conditions[i].Type == conditionType {
			return &object.Status.Conditions[i]
		}
	}
	return nil
}
