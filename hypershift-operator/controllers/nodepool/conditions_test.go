package nodepool

import (
	"fmt"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	. "github.com/onsi/gomega"
)

func TestGenerateReconciliationPausedCondition(t *testing.T) {
	fakeInputGeneration := int64(5)
	fakeFutureDate := ptr.To(time.Now().Add(4 * time.Hour).Format(time.RFC3339))
	fakePastDate := ptr.To(time.Now().Add(-4 * time.Hour).Format(time.RFC3339))
	testsCases := []struct {
		name              string
		inputPausedField  *string
		expectedCondition hyperv1.NodePoolCondition
	}{
		{
			name:             "if the pausedUntil field does not exist then ReconciliationActive condition is true",
			inputPausedField: nil,
			expectedCondition: hyperv1.NodePoolCondition{
				Type:               string(hyperv1.NodePoolReconciliationActiveConditionType),
				Status:             corev1.ConditionTrue,
				Reason:             reconciliationActiveConditionReason,
				Message:            "Reconciliation active on resource",
				ObservedGeneration: fakeInputGeneration,
			},
		},
		{
			name:             "if pausedUntil field is later than time.Now ReconciliationActive condition is false",
			inputPausedField: fakeFutureDate,
			expectedCondition: hyperv1.NodePoolCondition{
				Type:               string(hyperv1.NodePoolReconciliationActiveConditionType),
				Status:             corev1.ConditionFalse,
				Reason:             reconciliationPausedConditionReason,
				Message:            fmt.Sprintf("Reconciliation paused until: %s", *fakeFutureDate),
				ObservedGeneration: fakeInputGeneration,
			},
		},
		{
			name:             "if pausedUntil field is before time.Now then ReconciliationActive condition is true",
			inputPausedField: fakePastDate,
			expectedCondition: hyperv1.NodePoolCondition{
				Type:               string(hyperv1.NodePoolReconciliationActiveConditionType),
				Status:             corev1.ConditionTrue,
				Reason:             reconciliationActiveConditionReason,
				Message:            "Reconciliation active on resource",
				ObservedGeneration: fakeInputGeneration,
			},
		},
		{
			name:             "if pausedUntil field is true then ReconciliationActive condition is false",
			inputPausedField: ptr.To("true"),
			expectedCondition: hyperv1.NodePoolCondition{
				Type:               string(hyperv1.NodePoolReconciliationActiveConditionType),
				Status:             corev1.ConditionFalse,
				Reason:             reconciliationPausedConditionReason,
				Message:            "Reconciliation paused until field removed",
				ObservedGeneration: fakeInputGeneration,
			},
		},
		{
			name:             "if pausedUntil field has an improper value then ReconciliationActive condition is true with a reason indicating invalid value provided",
			inputPausedField: ptr.To("badValue"),
			expectedCondition: hyperv1.NodePoolCondition{
				Type:               string(hyperv1.NodePoolReconciliationActiveConditionType),
				Status:             corev1.ConditionTrue,
				Reason:             reconciliationInvalidPausedUntilConditionReason,
				Message:            "Invalid value provided for PausedUntil field",
				ObservedGeneration: fakeInputGeneration,
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			actualReconciliationActiveCondition := generateReconciliationActiveCondition(tc.inputPausedField, fakeInputGeneration)
			g.Expect(actualReconciliationActiveCondition).To(BeEquivalentTo(tc.expectedCondition))
		})
	}
}
