package util

import (
	"fmt"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

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
			inputPausedField: pointer.StringPtr(now.Add(4 * time.Hour).Format(time.RFC3339Nano)),
			expectedPaused:   true,
			expectedDuration: 4 * time.Hour,
		},
		{
			name:             "if pausedUntil field is before time.Now then reconciliation is not paused",
			inputPausedField: pointer.StringPtr(now.Add(-4 * time.Hour).Format(time.RFC3339Nano)),
			expectedPaused:   false,
			expectedDuration: -(4 * time.Hour),
		},
		{
			name:             "if pausedUntil field is true then reconciliation is paused",
			inputPausedField: pointer.StringPtr("true"),
			expectedPaused:   true,
			expectedDuration: time.Duration(0),
		},
		{
			name:             "if pausedUntil field has an improper value then reconciliation is not paused",
			inputPausedField: pointer.StringPtr("badValue"),
			expectedPaused:   false,
			expectedDuration: time.Duration(0),
			expectedError:    true,
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			actualIsPaused, actualDuration, err := processPausedUntilField(tc.inputPausedField, now)
			g.Expect(actualIsPaused).To(Equal(tc.expectedPaused))
			g.Expect(actualDuration).To(Equal(tc.expectedDuration))
			g.Expect(err != nil).To(Equal(tc.expectedError))
		})
	}
}

func TestGenerateReconciliationPausedCondition(t *testing.T) {
	fakeInputGeneration := int64(5)
	fakeFutureDate := pointer.StringPtr(time.Now().Add(4 * time.Hour).Format(time.RFC3339))
	fakePastDate := pointer.StringPtr(time.Now().Add(-4 * time.Hour).Format(time.RFC3339))
	testsCases := []struct {
		name              string
		inputPausedField  *string
		expectedCondition metav1.Condition
	}{
		{
			name:             "if the pausedUntil field does not exist then ReconciliationPaused condition is false",
			inputPausedField: nil,
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.ReconciliationPaused),
				Status:             metav1.ConditionFalse,
				Reason:             reconciliationActiveConditionReason,
				Message:            "Reconciliation active on resource",
				ObservedGeneration: fakeInputGeneration,
			},
		},
		{
			name:             "if pausedUntil field is later than time.Now ReconciliationPaused condition is true",
			inputPausedField: fakeFutureDate,
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.ReconciliationPaused),
				Status:             metav1.ConditionTrue,
				Reason:             reconciliationPausedConditionReason,
				Message:            fmt.Sprintf("Reconciliation paused until: %s", *fakeFutureDate),
				ObservedGeneration: fakeInputGeneration,
			},
		},
		{
			name:             "if pausedUntil field is before time.Now then ReconciliationPaused condition is false",
			inputPausedField: fakePastDate,
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.ReconciliationPaused),
				Status:             metav1.ConditionFalse,
				Reason:             reconciliationActiveConditionReason,
				Message:            "Reconciliation active on resource",
				ObservedGeneration: fakeInputGeneration,
			},
		},
		{
			name:             "if pausedUntil field is true then ReconciliationPaused condition is true",
			inputPausedField: pointer.StringPtr("true"),
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.ReconciliationPaused),
				Status:             metav1.ConditionTrue,
				Reason:             reconciliationPausedConditionReason,
				Message:            "Reconciliation paused until field removed",
				ObservedGeneration: fakeInputGeneration,
			},
		},
		{
			name:             "if pausedUntil field has an improper value then ReconciliationPaused condition is false with a reason indicating invalid value provided",
			inputPausedField: pointer.StringPtr("badValue"),
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.ReconciliationPaused),
				Status:             metav1.ConditionFalse,
				Reason:             reconciliationInvalidPausedUntilConditionReason,
				Message:            "Invalid value provided for PausedUntil field",
				ObservedGeneration: fakeInputGeneration,
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			actualReconciliationPausedCondition := GenerateReconciliationPausedCondition(tc.inputPausedField, fakeInputGeneration)
			g.Expect(actualReconciliationPausedCondition).To(BeEquivalentTo(tc.expectedCondition))
		})
	}
}
