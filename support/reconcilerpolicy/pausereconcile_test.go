package reconcilerpolicy

import (
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
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
			name:             "When pausedUntil is absent, it should report reconciliation as active",
			inputPausedField: nil,
			expectedPaused:   false,
			expectedDuration: time.Duration(0),
		},
		{
			name:             "When pausedUntil is in the future, it should report reconciliation as paused for the remaining duration",
			inputPausedField: ptr.To(now.Add(4 * time.Hour).Format(time.RFC3339Nano)),
			expectedPaused:   true,
			expectedDuration: 4 * time.Hour,
		},
		{
			name:             "When pausedUntil is in the past, it should report reconciliation as active",
			inputPausedField: ptr.To(now.Add(-4 * time.Hour).Format(time.RFC3339Nano)),
			expectedPaused:   false,
			expectedDuration: -(4 * time.Hour),
		},
		{
			name:             "When pausedUntil is true, it should report reconciliation as paused without a duration",
			inputPausedField: ptr.To("true"),
			expectedPaused:   true,
			expectedDuration: time.Duration(0),
		},
		{
			name:             "When pausedUntil is invalid, it should report reconciliation as active with an error",
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

func TestGenerateReconciliationActiveCondition(t *testing.T) {
	fakeInputGeneration := int64(5)
	fakeFutureDate := ptr.To(time.Now().Add(4 * time.Hour).Format(time.RFC3339))
	fakePastDate := ptr.To(time.Now().Add(-4 * time.Hour).Format(time.RFC3339))
	testsCases := []struct {
		name              string
		inputPausedField  *string
		expectedCondition metav1.Condition
	}{
		{
			name:             "When pausedUntil is absent, it should generate an active condition",
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
			name:             "When pausedUntil is in the future, it should generate a paused condition",
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
			name:             "When pausedUntil is in the past, it should generate an active condition",
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
			name:             "When pausedUntil is true, it should generate a paused condition without an end time",
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
			name:             "When pausedUntil is invalid, it should generate an active condition with the invalid-value reason",
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
