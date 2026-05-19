package conditions

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	metav1applyconfigurations "k8s.io/client-go/applyconfigurations/meta/v1"
)

func TestConditionChanged(t *testing.T) {
	existing := []metav1.Condition{
		{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "AllGood",
			Message:            "everything is fine",
			ObservedGeneration: 5,
		},
	}

	t.Run("When condition is identical to existing, it should return false", func(t *testing.T) {
		g := NewWithT(t)
		updated := metav1applyconfigurations.Condition().
			WithType("Ready").
			WithStatus(metav1.ConditionTrue).
			WithReason("AllGood").
			WithMessage("everything is fine").
			WithObservedGeneration(5)
		g.Expect(ConditionChanged(existing, updated)).To(BeFalse())
	})

	t.Run("When status differs, it should return true", func(t *testing.T) {
		g := NewWithT(t)
		updated := metav1applyconfigurations.Condition().
			WithType("Ready").
			WithStatus(metav1.ConditionFalse).
			WithReason("AllGood").
			WithMessage("everything is fine")
		g.Expect(ConditionChanged(existing, updated)).To(BeTrue())
	})

	t.Run("When reason differs, it should return true", func(t *testing.T) {
		g := NewWithT(t)
		updated := metav1applyconfigurations.Condition().
			WithType("Ready").
			WithStatus(metav1.ConditionTrue).
			WithReason("DifferentReason").
			WithMessage("everything is fine")
		g.Expect(ConditionChanged(existing, updated)).To(BeTrue())
	})

	t.Run("When message differs, it should return true", func(t *testing.T) {
		g := NewWithT(t)
		updated := metav1applyconfigurations.Condition().
			WithType("Ready").
			WithStatus(metav1.ConditionTrue).
			WithReason("AllGood").
			WithMessage("different message")
		g.Expect(ConditionChanged(existing, updated)).To(BeTrue())
	})

	t.Run("When observedGeneration differs, it should return true", func(t *testing.T) {
		g := NewWithT(t)
		updated := metav1applyconfigurations.Condition().
			WithType("Ready").
			WithStatus(metav1.ConditionTrue).
			WithReason("AllGood").
			WithMessage("everything is fine").
			WithObservedGeneration(6)
		g.Expect(ConditionChanged(existing, updated)).To(BeTrue())
	})

	t.Run("When condition type does not exist yet, it should return true", func(t *testing.T) {
		g := NewWithT(t)
		updated := metav1applyconfigurations.Condition().
			WithType("NewCondition").
			WithStatus(metav1.ConditionTrue).
			WithReason("New")
		g.Expect(ConditionChanged(existing, updated)).To(BeTrue())
	})

	t.Run("When existing is empty, it should return true", func(t *testing.T) {
		g := NewWithT(t)
		updated := metav1applyconfigurations.Condition().
			WithType("Ready").
			WithStatus(metav1.ConditionTrue).
			WithReason("AllGood")
		g.Expect(ConditionChanged(nil, updated)).To(BeTrue())
	})

	t.Run("When only some fields are set on updated and they match, it should return false", func(t *testing.T) {
		g := NewWithT(t)
		updated := metav1applyconfigurations.Condition().
			WithType("Ready").
			WithStatus(metav1.ConditionTrue).
			WithReason("AllGood")
		g.Expect(ConditionChanged(existing, updated)).To(BeFalse())
	})
}

func TestSSAConditions(t *testing.T) {
	oldTime := metav1.NewTime(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))

	t.Run("When an updated condition has the same status as existing, it should preserve the existing LastTransitionTime", func(t *testing.T) {
		g := NewWithT(t)

		existing := []metav1.Condition{
			{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				LastTransitionTime: oldTime,
				Reason:             "OldReason",
				Message:            "old message",
			},
		}
		managed := sets.New[string]("Ready")
		updated := metav1applyconfigurations.Condition().
			WithType("Ready").
			WithStatus(metav1.ConditionTrue).
			WithReason("NewReason").
			WithMessage("new message")

		result := SSAConditions(existing, managed, updated)

		g.Expect(result).To(HaveLen(1))
		g.Expect(*result[0].LastTransitionTime).To(Equal(oldTime))
		g.Expect(*result[0].Reason).To(Equal("NewReason"))
	})

	t.Run("When an updated condition has a different status from existing, it should set LastTransitionTime to now", func(t *testing.T) {
		g := NewWithT(t)

		existing := []metav1.Condition{
			{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				LastTransitionTime: oldTime,
				Reason:             "OldReason",
			},
		}
		managed := sets.New[string]("Ready")
		updated := metav1applyconfigurations.Condition().
			WithType("Ready").
			WithStatus(metav1.ConditionTrue).
			WithReason("NowReady")

		before := time.Now()
		result := SSAConditions(existing, managed, updated)
		after := time.Now()

		g.Expect(result).To(HaveLen(1))
		g.Expect(result[0].LastTransitionTime).NotTo(BeNil())
		g.Expect(result[0].LastTransitionTime.Time).To(BeTemporally(">=", before))
		g.Expect(result[0].LastTransitionTime.Time).To(BeTemporally("<=", after))
	})

	t.Run("When a condition is brand new with no existing match, it should set LastTransitionTime to now", func(t *testing.T) {
		g := NewWithT(t)

		managed := sets.New[string]("Ready")
		updated := metav1applyconfigurations.Condition().
			WithType("Ready").
			WithStatus(metav1.ConditionTrue).
			WithReason("FirstTime")

		before := time.Now()
		result := SSAConditions(nil, managed, updated)
		after := time.Now()

		g.Expect(result).To(HaveLen(1))
		g.Expect(result[0].LastTransitionTime).NotTo(BeNil())
		g.Expect(result[0].LastTransitionTime.Time).To(BeTemporally(">=", before))
		g.Expect(result[0].LastTransitionTime.Time).To(BeTemporally("<=", after))
	})

	t.Run("When the caller explicitly sets LastTransitionTime, it should not be overridden", func(t *testing.T) {
		g := NewWithT(t)

		callerTime := metav1.NewTime(time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC))

		existing := []metav1.Condition{
			{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				LastTransitionTime: oldTime,
				Reason:             "OldReason",
			},
		}
		managed := sets.New[string]("Ready")
		updated := metav1applyconfigurations.Condition().
			WithType("Ready").
			WithStatus(metav1.ConditionFalse).
			WithReason("NowFailing").
			WithLastTransitionTime(callerTime)

		result := SSAConditions(existing, managed, updated)

		g.Expect(result).To(HaveLen(1))
		g.Expect(*result[0].LastTransitionTime).To(Equal(callerTime))
	})

	t.Run("When the caller sets LastTransitionTime and status is unchanged, it should still use caller's value", func(t *testing.T) {
		g := NewWithT(t)

		callerTime := metav1.NewTime(time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC))

		existing := []metav1.Condition{
			{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				LastTransitionTime: oldTime,
				Reason:             "OldReason",
			},
		}
		managed := sets.New[string]("Ready")
		updated := metav1applyconfigurations.Condition().
			WithType("Ready").
			WithStatus(metav1.ConditionTrue).
			WithReason("StillReady").
			WithLastTransitionTime(callerTime)

		result := SSAConditions(existing, managed, updated)

		g.Expect(result).To(HaveLen(1))
		g.Expect(*result[0].LastTransitionTime).To(Equal(callerTime))
	})

	t.Run("When there are existing managed conditions not in updated, it should carry them forward with their LastTransitionTime", func(t *testing.T) {
		g := NewWithT(t)

		existing := []metav1.Condition{
			{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				LastTransitionTime: oldTime,
				Reason:             "AllGood",
				Message:            "everything is fine",
			},
			{
				Type:               "Degraded",
				Status:             metav1.ConditionFalse,
				LastTransitionTime: oldTime,
				Reason:             "NotDegraded",
				Message:            "not degraded",
			},
		}
		managed := sets.New[string]("Ready", "Degraded")
		updated := metav1applyconfigurations.Condition().
			WithType("Ready").
			WithStatus(metav1.ConditionTrue).
			WithReason("StillGood")

		result := SSAConditions(existing, managed, updated)

		g.Expect(result).To(HaveLen(2))
		// Sorted by type: Degraded, Ready
		g.Expect(*result[0].Type).To(Equal("Degraded"))
		g.Expect(*result[0].LastTransitionTime).To(Equal(oldTime))
		g.Expect(*result[1].Type).To(Equal("Ready"))
		g.Expect(*result[1].LastTransitionTime).To(Equal(oldTime))
	})

	t.Run("When results contain multiple conditions, it should sort them by type", func(t *testing.T) {
		g := NewWithT(t)

		managed := sets.New[string]("Zebra", "Alpha", "Middle")
		z := metav1applyconfigurations.Condition().WithType("Zebra").WithStatus(metav1.ConditionTrue).WithReason("R")
		a := metav1applyconfigurations.Condition().WithType("Alpha").WithStatus(metav1.ConditionTrue).WithReason("R")
		m := metav1applyconfigurations.Condition().WithType("Middle").WithStatus(metav1.ConditionTrue).WithReason("R")

		result := SSAConditions(nil, managed, z, a, m)

		g.Expect(result).To(HaveLen(3))
		g.Expect(*result[0].Type).To(Equal("Alpha"))
		g.Expect(*result[1].Type).To(Equal("Middle"))
		g.Expect(*result[2].Type).To(Equal("Zebra"))
	})

	t.Run("When an updated condition has no Type, it should panic", func(t *testing.T) {
		g := NewWithT(t)

		managed := sets.New[string]("Ready")
		noType := metav1applyconfigurations.Condition().WithStatus(metav1.ConditionTrue)

		g.Expect(func() {
			SSAConditions(nil, managed, noType)
		}).To(PanicWith(MatchError(ContainSubstring("must set a type"))))
	})

	t.Run("When an updated condition has a type not in managedTypes, it should panic", func(t *testing.T) {
		g := NewWithT(t)

		managed := sets.New[string]("Ready")
		unmanaged := metav1applyconfigurations.Condition().
			WithType("SomethingElse").
			WithStatus(metav1.ConditionTrue)

		g.Expect(func() {
			SSAConditions(nil, managed, unmanaged)
		}).To(PanicWith(MatchError(ContainSubstring("unmanaged condition type"))))
	})

	t.Run("When existing has unmanaged conditions, it should not include them in result", func(t *testing.T) {
		g := NewWithT(t)

		existing := []metav1.Condition{
			{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				LastTransitionTime: oldTime,
				Reason:             "OK",
			},
			{
				Type:               "OtherControllerCondition",
				Status:             metav1.ConditionTrue,
				LastTransitionTime: oldTime,
				Reason:             "SomeoneElse",
			},
		}
		managed := sets.New[string]("Ready")
		updated := metav1applyconfigurations.Condition().
			WithType("Ready").
			WithStatus(metav1.ConditionFalse).
			WithReason("Failing")

		result := SSAConditions(existing, managed, updated)

		g.Expect(result).To(HaveLen(1))
		g.Expect(*result[0].Type).To(Equal("Ready"))
	})

	t.Run("When no updated conditions are provided, it should carry forward all existing managed conditions", func(t *testing.T) {
		g := NewWithT(t)

		existing := []metav1.Condition{
			{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				LastTransitionTime: oldTime,
				Reason:             "OK",
				Message:            "ready",
			},
			{
				Type:               "Degraded",
				Status:             metav1.ConditionFalse,
				LastTransitionTime: oldTime,
				Reason:             "NotDegraded",
				Message:            "not degraded",
			},
		}
		managed := sets.New[string]("Ready", "Degraded")

		result := SSAConditions(existing, managed)

		g.Expect(result).To(HaveLen(2))
		// Carried forward with their original LastTransitionTime
		for _, cond := range result {
			g.Expect(*cond.LastTransitionTime).To(Equal(oldTime))
		}
	})
}
