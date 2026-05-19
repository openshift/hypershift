package conditions

import (
	"fmt"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	metav1applyconfigurations "k8s.io/client-go/applyconfigurations/meta/v1"
)

// ConditionChanged reports whether applying the given condition would change
// anything relative to the existing conditions. It mirrors the change-detection
// logic of meta.SetStatusCondition: a condition is "changed" if it's new or if
// its Status, Reason, Message, or ObservedGeneration differ from the existing one.
func ConditionChanged(existing []metav1.Condition, updated *metav1applyconfigurations.ConditionApplyConfiguration) bool {
	if updated.Type == nil {
		return true
	}
	for i := range existing {
		if existing[i].Type != *updated.Type {
			continue
		}
		if updated.Status != nil && existing[i].Status != *updated.Status {
			return true
		}
		if updated.Reason != nil && existing[i].Reason != *updated.Reason {
			return true
		}
		if updated.Message != nil && existing[i].Message != *updated.Message {
			return true
		}
		if updated.ObservedGeneration != nil && existing[i].ObservedGeneration != *updated.ObservedGeneration {
			return true
		}
		return false
	}
	return true
}

// SSAConditions builds the full list of condition apply configurations needed for
// a Server-Side Apply call. With SSA, a field manager that previously declared
// ownership of a condition type will lose that condition if it's omitted from a
// subsequent apply. This helper merges the caller's updated conditions with any
// existing managed conditions that aren't being updated, preventing accidental removal.
//
// Panics on programmer error (precedent: hostedclustersizing_controller.go, which
// this was extracted from): if an updated condition has no Type, or if it specifies
// a Type not in managedTypes.
func SSAConditions(
	existing []metav1.Condition,
	managedTypes sets.Set[string],
	updated ...*metav1applyconfigurations.ConditionApplyConfiguration,
) []*metav1applyconfigurations.ConditionApplyConfiguration {
	updatedTypes := sets.New[string]()
	for _, condition := range updated {
		if condition.Type == nil {
			panic(fmt.Errorf("programmer error: must set a type for condition: %#v", condition))
		}
		if !managedTypes.Has(*condition.Type) {
			panic(fmt.Errorf("programmer error: attempting to set unmanaged condition type %q", *condition.Type))
		}
		updatedTypes.Insert(*condition.Type)
	}
	result := append([]*metav1applyconfigurations.ConditionApplyConfiguration{}, updated...)
	for _, condition := range existing {
		if !updatedTypes.Has(condition.Type) && managedTypes.Has(condition.Type) {
			result = append(result, metav1applyconfigurations.Condition().
				WithType(condition.Type).
				WithStatus(condition.Status).
				WithObservedGeneration(condition.ObservedGeneration).
				WithLastTransitionTime(condition.LastTransitionTime).
				WithReason(condition.Reason).
				WithMessage(condition.Message),
			)
		}
	}
	// Ensure LastTransitionTime is set on every condition — it's a required field
	// on metav1.Condition and the API server rejects SSA applies that omit it.
	for _, cond := range result {
		if cond.LastTransitionTime != nil {
			continue
		}
		// Preserve existing transition time when status hasn't changed.
		for i := range existing {
			if existing[i].Type == *cond.Type && cond.Status != nil && existing[i].Status == *cond.Status {
				cond.LastTransitionTime = &existing[i].LastTransitionTime
				break
			}
		}
		if cond.LastTransitionTime == nil {
			now := metav1.NewTime(time.Now())
			cond.LastTransitionTime = &now
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return *result[i].Type < *result[j].Type
	})
	return result
}
