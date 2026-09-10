package controlplanecomponent

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

type replicasTestOptions struct {
	requestServing bool
}

func (o replicasTestOptions) IsRequestServing() bool {
	return o.requestServing
}

func (o replicasTestOptions) MultiZoneSpread() bool {
	return false
}

func (o replicasTestOptions) NeedsManagementKASAccess() bool {
	return false
}

func TestDefaultReplicas(t *testing.T) {
	tests := []struct {
		name          string
		componentName string
		hcp           hyperv1.HostedControlPlane
		want          int32
	}{
		{
			name:          "When router override is two replicas, it should use two replicas",
			componentName: "router",
			hcp: hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{
				ControllerAvailabilityPolicy: hyperv1.HighlyAvailable,
				ControlPlaneComponentConfiguration: hyperv1.ControlPlaneComponentConfiguration{
					Router: hyperv1.ControlPlaneWorkloadConfiguration{Replicas: 2},
				},
			}},
			want: 2,
		},
		{
			name:          "When router override is unset, it should retain the highly available default",
			componentName: "router",
			hcp: hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{
				ControllerAvailabilityPolicy: hyperv1.HighlyAvailable,
			}},
			want: 3,
		},
		{
			name:          "When a non-router API critical component has a router override, it should retain three replicas",
			componentName: "kube-apiserver",
			hcp: hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{
				ControllerAvailabilityPolicy: hyperv1.HighlyAvailable,
				ControlPlaneComponentConfiguration: hyperv1.ControlPlaneComponentConfiguration{
					Router: hyperv1.ControlPlaneWorkloadConfiguration{Replicas: 2},
				},
			}},
			want: 3,
		},
		{
			name:          "When the control plane is single replica, it should take precedence over a router override",
			componentName: "router",
			hcp: hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{
				ControllerAvailabilityPolicy: hyperv1.SingleReplica,
				ControlPlaneComponentConfiguration: hyperv1.ControlPlaneComponentConfiguration{
					Router: hyperv1.ControlPlaneWorkloadConfiguration{Replicas: 2},
				},
			}},
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(DefaultReplicas(&tt.hcp, replicasTestOptions{}, tt.componentName)).To(Equal(tt.want))
		})
	}
}
