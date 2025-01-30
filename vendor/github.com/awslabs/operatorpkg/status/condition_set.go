// Inspired by https://github.com/knative/pkg/tree/97c7258e3a98b81459936bc7a29dc6a9540fa357/apis,
// but we chose to diverge due to the unacceptably large dependency closure of knative/pkg.
package status

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConditionTypes is an abstract collection of the possible ConditionType values
// that a particular resource might expose.  It also holds the "root condition"
// for that resource, which we define to be one of Ready or Succeeded depending
// on whether it is a Living or Batch process respectively.
type ConditionTypes struct {
	root       string
	dependents []string
}

// NewReadyConditions returns a ConditionTypes to hold the conditions for the
// resource. ConditionReady is used as the root condition.
// The set of condition types provided are those of the terminal subconditions.
func NewReadyConditions(d ...string) ConditionTypes {
	return newConditionTypes(ConditionReady, d...)
}

// NewSucceededConditions returns a ConditionTypes to hold the conditions for the
// batch resource. ConditionSucceeded is used as the root condition.
// The set of condition types provided are those of the terminal subconditions.
func NewSucceededConditions(d ...string) ConditionTypes {
	return newConditionTypes(ConditionSucceeded, d...)
}

func newConditionTypes(root string, dependents ...string) ConditionTypes {
	return ConditionTypes{
		root:       root,
		dependents: lo.Reject(lo.Uniq(dependents), func(c string, _ int) bool { return c == root }),
	}
}

// ConditionSet provides methods for evaluating Conditions.
// +k8s:deepcopy-gen=false
type ConditionSet struct {
	ConditionTypes
	object Object
}

// For creates a ConditionSet from an object using the original
// ConditionTypes as a reference. Status must be a pointer to a struct.
func (r ConditionTypes) For(object Object) ConditionSet {
	cs := ConditionSet{object: object, ConditionTypes: r}
	// Set known conditions Unknown if not set.
	for _, t := range append(r.dependents, r.root) {
		if cs.Get(t) == nil {
			cs.SetUnknown(t)
		}
	}
	return cs
}

// Root returns the root Condition, typically "Ready" or "Succeeded"
func (c ConditionSet) Root() *Condition {
	if c.object == nil {
		return nil
	}
	return c.Get(c.root)
}

func (c ConditionSet) List() []Condition {
	if c.object == nil {
		return nil
	}
	return c.object.GetConditions()
}

// Get finds and returns the Condition that matches the ConditionType
// previously set on Conditions.
func (c ConditionSet) Get(t string) *Condition {
	if c.object == nil {
		return nil
	}
	if condition, found := lo.Find(c.object.GetConditions(), func(c Condition) bool { return c.Type == t }); found {
		return &condition
	}
	return nil
}

// IsTrue returns true if all condition types are true.
func (c ConditionSet) IsTrue(conditionTypes ...string) bool {
	for _, conditionType := range conditionTypes {
		if !c.Get(conditionType).IsTrue() {
			return false
		}
	}
	return true
}

// Set sets or updates the Condition on Conditions for Condition.Type.
// If there is an update, Conditions are stored back sorted.
func (c ConditionSet) Set(condition Condition) (modified bool) {
	conditionType := condition.Type
	var conditions []Condition
	for _, c := range c.object.GetConditions() {
		if c.Type != conditionType {
			conditions = append(conditions, c)
		} else {
			// If we'd only update the LastTransitionTime, then return.
			condition.LastTransitionTime = c.LastTransitionTime
			if reflect.DeepEqual(condition, c) {
				return false
			}
		}
	}
	condition.LastTransitionTime = metav1.Now()
	conditions = append(conditions, condition)
	// Sorted for convenience of the consumer, i.e. kubectl.
	sort.Slice(conditions, func(i, j int) bool { return conditions[i].Type < conditions[j].Type })
	c.object.SetConditions(conditions)

	// Recompute the root condition after setting any other condition
	c.recomputeRootCondition(conditionType)
	return true
}

// Clear removes the abnormal condition that matches the ConditionType
// Not implemented for normal conditions
func (c ConditionSet) Clear(t string) error {
	var conditions []Condition

	if c.object == nil {
		return nil
	}
	// Normal conditions are not handled as they can't be nil
	if t == c.root || lo.Contains(c.dependents, t) {
		return fmt.Errorf("clearing normal conditions not implemented")
	}
	cond := c.Get(t)
	if cond == nil {
		return nil
	}
	for _, c := range c.object.GetConditions() {
		if c.Type != t {
			conditions = append(conditions, c)
		}
	}

	// Sorted for convenience of the consumer, i.e. kubectl.
	sort.Slice(conditions, func(i, j int) bool { return conditions[i].Type < conditions[j].Type })
	c.object.SetConditions(conditions)

	return nil
}

// SetTrue sets the status of conditionType to true with the reason, and then marks the root condition to
// true if all other dependents are also true.
func (c ConditionSet) SetTrue(conditionType string) (modified bool) {
	return c.SetTrueWithReason(conditionType, conditionType, "")
}

// SetTrueWithReason sets the status of conditionType to true with the reason, and then marks the root condition to
// true if all other dependents are also true.
func (c ConditionSet) SetTrueWithReason(conditionType string, reason, message string) (modified bool) {
	return c.Set(Condition{
		Type:    conditionType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// SetUnknown sets the status of conditionType to Unknown and also sets the root condition
// to Unknown if no other dependent condition is in an error state.
func (c ConditionSet) SetUnknown(conditionType string) (modified bool) {
	return c.SetUnknownWithReason(conditionType, "AwaitingReconciliation", "object is awaiting reconciliation")
}

// SetUnknownWithReason sets the status of conditionType to Unknown with the reason, and also sets the root condition
// to Unknown if no other dependent condition is in an error state.
func (c ConditionSet) SetUnknownWithReason(conditionType string, reason, message string) (modified bool) {
	return c.Set(Condition{
		Type:    conditionType,
		Status:  metav1.ConditionUnknown,
		Reason:  reason,
		Message: message,
	})
}

// SetFalse sets the status of conditionType and the root condition to False.
func (c ConditionSet) SetFalse(conditionType string, reason, message string) (modified bool) {
	return c.Set(Condition{
		Type:    conditionType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// recomputeRootCondition marks the root condition to true if all other dependents are also true.
func (c ConditionSet) recomputeRootCondition(conditionType string) {
	if conditionType == c.root {
		return
	}
	if conditions := c.findUnhealthyDependents(); len(conditions) == 0 {
		c.SetTrue(c.root)
	} else {
		c.Set(Condition{
			Type: c.root,
			// The root condition is no longer unknown as soon as any are false
			Status: lo.Ternary(
				lo.ContainsBy(conditions, func(condition Condition) bool { return condition.IsFalse() }),
				metav1.ConditionFalse,
				metav1.ConditionUnknown,
			),
			Reason: "UnhealthyDependents",
			Message: strings.Join(lo.Map(conditions, func(condition Condition, _ int) string {
				return fmt.Sprintf("%s=%s", condition.Type, condition.Status)
			}), ", "),
		})
	}
}

func (c ConditionSet) findUnhealthyDependents() []Condition {
	if len(c.dependents) == 0 {
		return nil
	}
	// Get dependent conditions
	conditions := c.object.GetConditions()
	conditions = lo.Filter(conditions, func(condition Condition, _ int) bool {
		return lo.Contains(c.dependents, condition.Type)
	})
	conditions = lo.Filter(conditions, func(condition Condition, _ int) bool {
		return condition.IsFalse() || condition.IsUnknown()
	})

	// Sort set conditions by time.
	sort.Slice(conditions, func(i, j int) bool {
		return conditions[i].LastTransitionTime.After(conditions[j].LastTransitionTime.Time)
	})
	return conditions
}
