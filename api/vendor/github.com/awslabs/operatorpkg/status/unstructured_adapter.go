package status

import (
	"time"

	"github.com/awslabs/operatorpkg/object"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// unstructuredAdapter is an adapter for the status.Object interface. unstructuredAdapter
// makes the assumption that status conditions are found on status.conditions path.
type unstructuredAdapter[T client.Object] struct {
	unstructured.Unstructured
}

func NewUnstructuredAdapter[T client.Object](obj client.Object) *unstructuredAdapter[T] {
	u := unstructured.Unstructured{Object: lo.Must(runtime.DefaultUnstructuredConverter.ToUnstructured(obj))}
	ua := &unstructuredAdapter[T]{Unstructured: u}
	ua.SetGroupVersionKind(object.GVK(obj))
	return ua
}

func (u *unstructuredAdapter[T]) GetObjectKind() schema.ObjectKind {
	return u
}
func (u *unstructuredAdapter[T]) SetGroupVersionKind(gvk schema.GroupVersionKind) {
	u.Unstructured.SetGroupVersionKind(gvk)
}
func (u *unstructuredAdapter[T]) GroupVersionKind() schema.GroupVersionKind {
	return object.GVK(object.New[T]())
}

func (u *unstructuredAdapter[T]) GetConditions() []Condition {
	conditions, _, _ := unstructured.NestedFieldNoCopy(u.Object, "status", "conditions")
	if conditions == nil {
		return nil
	}
	return lo.Map(conditions.([]interface{}), func(condition interface{}, _ int) Condition {
		var newCondition Condition
		cond := condition.(map[string]interface{})
		newCondition.Type, _, _ = unstructured.NestedString(cond, "type")
		newCondition.Reason, _, _ = unstructured.NestedString(cond, "reason")
		status, _, _ := unstructured.NestedString(cond, "status")
		if status != "" {
			newCondition.Status = metav1.ConditionStatus(status)
		}
		newCondition.Message, _, _ = unstructured.NestedString(cond, "message")
		transitionTime, _, _ := unstructured.NestedString(cond, "lastTransitionTime")
		if transitionTime != "" {
			newCondition.LastTransitionTime = metav1.Time{Time: lo.Must(time.Parse(time.RFC3339, transitionTime))}
		}
		newCondition.ObservedGeneration, _, _ = unstructured.NestedInt64(cond, "observedGeneration")
		return newCondition
	})
}
func (u *unstructuredAdapter[T]) SetConditions(conditions []Condition) {
	unstructured.SetNestedSlice(u.Object, lo.Map(conditions, func(condition Condition, _ int) interface{} {
		b := map[string]interface{}{}
		if condition.Type != "" {
			b["type"] = condition.Type
		}
		if condition.Reason != "" {
			b["reason"] = condition.Reason
		}
		if condition.Status != "" {
			b["status"] = string(condition.Status)
		}
		if condition.Message != "" {
			b["message"] = condition.Message
		}
		if !condition.LastTransitionTime.IsZero() {
			b["lastTransitionTime"] = condition.LastTransitionTime.Format(time.RFC3339)
		}
		if condition.ObservedGeneration != 0 {
			b["observedGeneration"] = condition.ObservedGeneration
		}
		return b
	}), "status", "conditions")
}

func (u *unstructuredAdapter[T]) StatusConditions() ConditionSet {
	conditionTypes := lo.Map(u.GetConditions(), func(condition Condition, _ int) string {
		return condition.Type
	})
	return NewReadyConditions(conditionTypes...).For(u)
}
