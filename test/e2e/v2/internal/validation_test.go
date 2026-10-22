//go:build e2ev2

package internal

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/onsi/gomega"
)

func TestValidateConditions(t *testing.T) {
	for _, test := range []struct {
		name   string
		object crclient.Object
		fail   bool
	}{
		{name: "When the expected condition matches, it should accept it", object: &hyperv1.NodePool{Status: hyperv1.NodePoolStatus{Conditions: []hyperv1.NodePoolCondition{{Type: "Ready", Status: corev1.ConditionTrue}}}}},
		{name: "When the condition is missing, it should fail", object: &hyperv1.NodePool{}, fail: true},
		{name: "When condition polarity differs, it should fail", object: &hyperv1.NodePool{Status: hyperv1.NodePoolStatus{Conditions: []hyperv1.NodePoolCondition{{Type: "Ready", Status: corev1.ConditionFalse}}}}, fail: true},
		{name: "When conditions are malformed, it should fail", object: &unstructured.Unstructured{Object: map[string]interface{}{"status": map[string]interface{}{"conditions": "invalid"}}}, fail: true},
		{name: "When the object is nil, it should report the error without dereferencing it", fail: true},
		{name: "When the object is typed nil, it should report the error without dereferencing it", object: (*hyperv1.NodePool)(nil), fail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			failures := 0
			assertions := gomega.NewGomega(func(string, ...int) { failures++ })
			ValidateConditions(assertions, test.object, []v2util.Condition{{Type: "Ready", Status: "True"}})
			if (failures > 0) != test.fail {
				t.Fatalf("unexpected assertion failure count: %d", failures)
			}
		})
	}
}
