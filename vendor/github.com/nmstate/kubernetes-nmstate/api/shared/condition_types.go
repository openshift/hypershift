/*
Copyright The Kubernetes NMState Authors.


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package shared

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ConditionList []Condition

type Condition struct {
	Type               ConditionType          `json:"type"`
	Status             corev1.ConditionStatus `json:"status"`
	Reason             ConditionReason        `json:"reason,omitempty"`
	Message            string                 `json:"message,omitempty"`
	MessageEncoded     string                 `json:"messageEncoded,omitempty"`
	LastHeartbeatTime  metav1.Time            `json:"lastHeartbeatTime,omitempty"`
	LastTransitionTime metav1.Time            `json:"lastTransitionTime,omitempty"`
}

type ConditionType string

type ConditionReason string

func NewCondition(conditionType ConditionType, status corev1.ConditionStatus, reason ConditionReason, message string) Condition {
	now := metav1.Time{Time: time.Now()}
	condition := Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastHeartbeatTime:  now,
		LastTransitionTime: now,
	}
	return condition
}

func (conditions *ConditionList) Set(conditionType ConditionType, status corev1.ConditionStatus, reason ConditionReason, message string) {
	condition := conditions.Find(conditionType)

	// If there isn't condition we want to change, add new one
	if condition == nil {
		condition := NewCondition(conditionType, status, reason, message)
		*conditions = append(*conditions, condition)
		return
	}

	now := metav1.Time{Time: time.Now()}

	// If there is different status, reason or message update it
	if condition.Status != status || condition.Reason != reason || condition.Message != message {
		if condition.Status != status {
			condition.LastTransitionTime = now
		}
		condition.Status = status
		condition.Reason = reason
		condition.Message = message
	}
	condition.LastHeartbeatTime = now
}

func (conditions ConditionList) Find(conditionType ConditionType) *Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
