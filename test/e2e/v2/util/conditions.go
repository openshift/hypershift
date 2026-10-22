//go:build e2ev2

package util

import (
	"fmt"
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Condition is a common representation of Kubernetes status conditions.
type Condition struct {
	Type    string
	Status  metav1.ConditionStatus
	Reason  string
	Message string
}

// String formats the condition, including its reason and message when present.
func (condition Condition) String() string {
	message := fmt.Sprintf("%s=%s", condition.Type, condition.Status)
	if condition.Reason != "" {
		message += ": " + condition.Reason
	}
	if condition.Message != "" {
		message += "(" + condition.Message + ")"
	}
	return message
}

// Matches compares nonempty expectation fields against an observed condition.
func (expected Condition) Matches(actual Condition) bool {
	return (expected.Type == "" || expected.Type == actual.Type) &&
		(expected.Status == "" || expected.Status == actual.Status) &&
		(expected.Reason == "" || expected.Reason == actual.Reason) &&
		(expected.Message == "" || expected.Message == actual.Message)
}

// Conditions extracts status.conditions from a Kubernetes object. It returns an
// error for nil objects or malformed conditions and an empty slice when absent.
func Conditions(object crclient.Object) ([]Condition, error) {
	if object == nil || (reflect.ValueOf(object).Kind() == reflect.Pointer && reflect.ValueOf(object).IsNil()) {
		return nil, fmt.Errorf("cannot read conditions from a nil object")
	}
	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(object)
	if err != nil {
		return nil, fmt.Errorf("converting %T to read conditions: %w", object, err)
	}
	entries, _, err := unstructured.NestedSlice(content, "status", "conditions")
	if err != nil {
		return nil, fmt.Errorf("reading conditions from %T: %w", object, err)
	}
	conditions := make([]Condition, 0, len(entries))
	for index, entry := range entries {
		fields, ok := entry.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("condition %d on %T is not an object", index, object)
		}
		values := make(map[string]string, 4)
		for _, field := range []string{"type", "status", "reason", "message"} {
			value, _, err := unstructured.NestedString(fields, field)
			if err != nil {
				return nil, fmt.Errorf("reading condition %d field %s on %T: %w", index, field, object, err)
			}
			values[field] = value
		}
		conditions = append(conditions, Condition{
			Type: values["type"], Status: metav1.ConditionStatus(values["status"]),
			Reason: values["reason"], Message: values["message"],
		})
	}
	return conditions, nil
}
