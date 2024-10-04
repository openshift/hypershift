package util

import (
	"fmt"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	. "github.com/onsi/gomega"
)

func TestProcessPausedUntilField(t *testing.T) {
	now := time.Now()
	testsCases := []struct {
		name             string
		inputPausedField *string
		expectedPaused   bool
		expectedDuration time.Duration
		expectedError    bool
	}{
		{
			name:             "if the pausedUntil field does not exist then reconciliation is not paused",
			inputPausedField: nil,
			expectedPaused:   false,
			expectedDuration: time.Duration(0),
		},
		{
			name:             "if pausedUntil field is later than time.Now then reconciliation is paused",
			inputPausedField: ptr.To(now.Add(4 * time.Hour).Format(time.RFC3339Nano)),
			expectedPaused:   true,
			expectedDuration: 4 * time.Hour,
		},
		{
			name:             "if pausedUntil field is before time.Now then reconciliation is not paused",
			inputPausedField: ptr.To(now.Add(-4 * time.Hour).Format(time.RFC3339Nano)),
			expectedPaused:   false,
			expectedDuration: -(4 * time.Hour),
		},
		{
			name:             "if pausedUntil field is true then reconciliation is paused",
			inputPausedField: ptr.To("true"),
			expectedPaused:   true,
			expectedDuration: time.Duration(0),
		},
		{
			name:             "if pausedUntil field has an improper value then reconciliation is not paused",
			inputPausedField: ptr.To("badValue"),
			expectedPaused:   false,
			expectedDuration: time.Duration(0),
			expectedError:    true,
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			actualIsPaused, actualDuration, err := ProcessPausedUntilField(tc.inputPausedField, now)
			g.Expect(actualIsPaused).To(Equal(tc.expectedPaused))
			g.Expect(actualDuration).To(Equal(tc.expectedDuration))
			g.Expect(err != nil).To(Equal(tc.expectedError))
		})
	}
}

func TestGenerateReconciliationPausedCondition(t *testing.T) {
	fakeInputGeneration := int64(5)
	fakeFutureDate := ptr.To(time.Now().Add(4 * time.Hour).Format(time.RFC3339))
	fakePastDate := ptr.To(time.Now().Add(-4 * time.Hour).Format(time.RFC3339))
	testsCases := []struct {
		name              string
		inputPausedField  *string
		expectedCondition metav1.Condition
	}{
		{
			name:             "if the pausedUntil field does not exist then ReconciliationActive condition is true",
			inputPausedField: nil,
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.ReconciliationActive),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1.AsExpectedReason,
				Message:            "Reconciliation active on resource",
				ObservedGeneration: fakeInputGeneration,
			},
		},
		{
			name:             "if pausedUntil field is later than time.Now ReconciliationActive condition is false",
			inputPausedField: fakeFutureDate,
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.ReconciliationActive),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.ReconciliationPausedConditionReason,
				Message:            fmt.Sprintf("Reconciliation paused until: %s", *fakeFutureDate),
				ObservedGeneration: fakeInputGeneration,
			},
		},
		{
			name:             "if pausedUntil field is before time.Now then ReconciliationActive condition is true",
			inputPausedField: fakePastDate,
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.ReconciliationActive),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1.AsExpectedReason,
				Message:            "Reconciliation active on resource",
				ObservedGeneration: fakeInputGeneration,
			},
		},
		{
			name:             "if pausedUntil field is true then ReconciliationActive condition is false",
			inputPausedField: ptr.To("true"),
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.ReconciliationActive),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.ReconciliationPausedConditionReason,
				Message:            "Reconciliation paused until field removed",
				ObservedGeneration: fakeInputGeneration,
			},
		},
		{
			name:             "if pausedUntil field has an improper value then ReconciliationActive condition is true with a reason indicating invalid value provided",
			inputPausedField: ptr.To("badValue"),
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.ReconciliationActive),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1.ReconciliationInvalidPausedUntilConditionReason,
				Message:            "Invalid value provided for PausedUntil field",
				ObservedGeneration: fakeInputGeneration,
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			actualReconciliationActiveCondition := GenerateReconciliationActiveCondition(tc.inputPausedField, fakeInputGeneration)
			g.Expect(actualReconciliationActiveCondition).To(BeEquivalentTo(tc.expectedCondition))
		})
	}
}
