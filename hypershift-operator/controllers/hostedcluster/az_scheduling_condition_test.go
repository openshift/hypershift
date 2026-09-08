package hostedcluster

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func zonalNode(name, zone string) corev1.Node {
	return corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{
		hyperv1.ControlPlaneNodeRoleLabel: hyperv1.ControlPlaneNodeRoleZonal,
		corev1.LabelTopologyZone:          zone,
	}}}
}

func overflowNode(name string) corev1.Node {
	return corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{
		hyperv1.ControlPlaneNodeRoleLabel: hyperv1.ControlPlaneNodeRoleOverflow,
	}}}
}

func TestControlPlaneAvailabilityZoneSchedulingCondition(t *testing.T) {
	fullContract := []corev1.Node{
		zonalNode("z1", "zone-a"), zonalNode("z2", "zone-b"), zonalNode("z3", "zone-c"),
		overflowNode("o1"),
	}

	tests := []struct {
		name        string
		annotations map[string]string
		nodes       []corev1.Node
		wantStatus  metav1.ConditionStatus
		wantReason  string
	}{
		{
			name:       "node contract satisfied",
			nodes:      fullContract,
			wantStatus: metav1.ConditionTrue,
			wantReason: hyperv1.ControlPlaneAvailabilityZoneSchedulingAppliedReason,
		},
		{
			name:        "dedicated request-serving topology takes precedence",
			annotations: map[string]string{hyperv1.TopologyAnnotation: hyperv1.DedicatedRequestServingComponentsTopology},
			nodes:       fullContract,
			wantStatus:  metav1.ConditionFalse,
			wantReason:  hyperv1.ControlPlaneAvailabilityZoneSchedulingConflictsWithRequestServingReason,
		},
		{
			name:       "fewer than three zonal zones",
			nodes:      []corev1.Node{zonalNode("z1", "zone-a"), zonalNode("z2", "zone-b"), overflowNode("o1")},
			wantStatus: metav1.ConditionFalse,
			wantReason: hyperv1.ControlPlaneAvailabilityZoneSchedulingNodeContractUnsatisfiedReason,
		},
		{
			name:       "three zonal nodes but only two distinct zones",
			nodes:      []corev1.Node{zonalNode("z1", "zone-a"), zonalNode("z2", "zone-a"), zonalNode("z3", "zone-b"), overflowNode("o1")},
			wantStatus: metav1.ConditionFalse,
			wantReason: hyperv1.ControlPlaneAvailabilityZoneSchedulingNodeContractUnsatisfiedReason,
		},
		{
			name:       "no overflow node",
			nodes:      []corev1.Node{zonalNode("z1", "zone-a"), zonalNode("z2", "zone-b"), zonalNode("z3", "zone-c")},
			wantStatus: metav1.ConditionFalse,
			wantReason: hyperv1.ControlPlaneAvailabilityZoneSchedulingNodeContractUnsatisfiedReason,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			hc := &hyperv1.HostedCluster{}
			hc.Annotations = test.annotations
			cond := controlPlaneAvailabilityZoneSchedulingCondition(hc, test.nodes)
			g.Expect(cond.Type).To(Equal(string(hyperv1.ControlPlaneAvailabilityZoneSchedulingAvailable)))
			g.Expect(cond.Status).To(Equal(test.wantStatus))
			g.Expect(cond.Reason).To(Equal(test.wantReason))
		})
	}
}
