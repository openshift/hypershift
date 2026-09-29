package hostedcluster

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGCPFirewallRulesReadyCondition(t *testing.T) {
	t.Run("When the HCP condition's ObservedGeneration matches the HCP's current generation, it should pass it through unchanged", func(t *testing.T) {
		g := NewWithT(t)
		hcp := &hyperv1.HostedControlPlane{}
		hcp.Generation = 3
		hcpCondition := &metav1.Condition{
			Type:               string(hyperv1.GCPFirewallRulesReady),
			Status:             metav1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			ObservedGeneration: 3,
		}

		result := gcpFirewallRulesReadyCondition(hcp, hcpCondition)
		g.Expect(result).To(BeIdenticalTo(hcpCondition))
	})

	t.Run("When the HCP condition trails the HCP's current generation, it should report Unknown instead of the stale value", func(t *testing.T) {
		g := NewWithT(t)
		hcp := &hyperv1.HostedControlPlane{}
		hcp.Generation = 4
		hcpCondition := &metav1.Condition{
			Type:               string(hyperv1.GCPFirewallRulesReady),
			Status:             metav1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			ObservedGeneration: 3,
		}

		result := gcpFirewallRulesReadyCondition(hcp, hcpCondition)
		g.Expect(result.Type).To(Equal(string(hyperv1.GCPFirewallRulesReady)))
		g.Expect(result.Status).To(Equal(metav1.ConditionUnknown))
		g.Expect(result.Reason).To(Equal(hyperv1.StatusUnknownReason))
	})

	t.Run("When the HCP is nil, it should pass the condition through unchanged", func(t *testing.T) {
		g := NewWithT(t)
		hcpCondition := &metav1.Condition{Type: string(hyperv1.GCPFirewallRulesReady), Status: metav1.ConditionTrue}

		result := gcpFirewallRulesReadyCondition(nil, hcpCondition)
		g.Expect(result).To(BeIdenticalTo(hcpCondition))
	})

	t.Run("When the HCP condition is nil, it should return nil", func(t *testing.T) {
		g := NewWithT(t)
		hcp := &hyperv1.HostedControlPlane{}

		result := gcpFirewallRulesReadyCondition(hcp, nil)
		g.Expect(result).To(BeNil())
	})
}
