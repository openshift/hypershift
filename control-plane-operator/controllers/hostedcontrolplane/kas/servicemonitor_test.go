package kas

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"

	"k8s.io/apimachinery/pkg/util/sets"
)

func TestReconcileRecordingRules(t *testing.T) {
	rules := manifests.ControlPlaneRecordingRules("test")
	ReconcileRecordingRules(rules, "fake-id")
	g := NewWithT(t)
	g.Expect(rules.Spec.Groups[0].Name).To(Equal("hypershift.rules"))
	ruleNames := sets.NewString()
	for _, rule := range rules.Spec.Groups[0].Rules {
		ruleNames.Insert(rule.Record)
	}
	g.Expect(ruleNames.Has("instance:etcd_object_counts:sum")).To(BeTrue())
	g.Expect(rules.Spec.Groups[0].Rules[0].Labels).To(HaveKeyWithValue("_id", "fake-id"))
}
