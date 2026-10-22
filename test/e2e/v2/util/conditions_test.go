//go:build e2ev2

package util

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestConditions(t *testing.T) {
	tests := []struct {
		name      string
		object    crclient.Object
		count     int
		wantError bool
	}{
		{name: "When the object is nil, it should return an error", wantError: true},
		{name: "When the object is a typed nil, it should return an error", object: (*hyperv1.HostedCluster)(nil), wantError: true},
		{name: "When an unstructured object is a typed nil, it should return an error", object: (*unstructured.Unstructured)(nil), wantError: true},
		{name: "When conditions are absent, it should return an empty slice", object: &hyperv1.NodePool{}},
		{name: "When a pod has a condition, it should not prepend empty conditions", object: &corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}}, count: 1},
		{name: "When a HostedCluster has a condition, it should preserve its fields", object: &hyperv1.HostedCluster{Status: hyperv1.HostedClusterStatus{Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, Reason: "AsExpected", Message: "available"}}}}, count: 1},
		{name: "When conditions are not a slice, it should return an error", object: &unstructured.Unstructured{Object: map[string]interface{}{"status": map[string]interface{}{"conditions": "invalid"}}}, wantError: true},
		{name: "When a condition is not an object, it should return an error", object: &unstructured.Unstructured{Object: map[string]interface{}{"status": map[string]interface{}{"conditions": []interface{}{"invalid"}}}}, wantError: true},
		{name: "When a condition field is malformed, it should return an error", object: &unstructured.Unstructured{Object: map[string]interface{}{"status": map[string]interface{}{"conditions": []interface{}{map[string]interface{}{"type": int64(7)}}}}}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conditions, err := Conditions(test.object)
			if (err != nil) != test.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if test.wantError {
				return
			}
			if len(conditions) != test.count {
				t.Fatalf("expected %d conditions, got %d", test.count, len(conditions))
			}
			for _, condition := range conditions {
				if condition.Type != "Ready" || condition.Status != metav1.ConditionTrue {
					t.Fatalf("unexpected adapted condition: %v", condition)
				}
			}
			if _, ok := test.object.(*hyperv1.HostedCluster); ok && (conditions[0].Reason != "AsExpected" || conditions[0].Message != "available") {
				t.Fatal("condition diagnostics were not preserved")
			}
		})
	}
}

func TestConditionMatches(t *testing.T) {
	actual := Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "AsExpected", Message: "available"}
	tests := []struct {
		name     string
		expected Condition
		matches  bool
	}{
		{name: "When all fields match, it should match", expected: actual, matches: true},
		{name: "When expectation fields are empty, it should treat them as wildcards", matches: true},
		{name: "When the type differs, it should not match", expected: Condition{Type: "Available"}},
		{name: "When status differs, it should not match", expected: Condition{Status: metav1.ConditionFalse}},
		{name: "When reason differs, it should not match", expected: Condition{Reason: "Failed"}},
		{name: "When message differs, it should not match", expected: Condition{Message: "unavailable"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.expected.Matches(actual) != test.matches {
				t.Fatal("unexpected condition match")
			}
		})
	}
}

func TestConditionString(t *testing.T) {
	tests := []struct {
		name      string
		condition Condition
		want      string
	}{
		{name: "When diagnostics are empty, it should format type and status", condition: Condition{Type: "Ready", Status: metav1.ConditionTrue}, want: "Ready=True"},
		{name: "When diagnostics exist, it should include reason and message", condition: Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: "Failed", Message: "unavailable"}, want: "Ready=False: Failed(unavailable)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.condition.String(); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}
