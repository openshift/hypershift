package konnectivity

import (
	"slices"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
)

func TestReconcileAgentDaemonSet(t *testing.T) {
	testCases := []struct {
		name string
	}{
		{
			name: "When ReconcileAgentDaemonSet is called, it should include --sync-forever in the agent args",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			daemonset := &appsv1.DaemonSet{}
			params := &KonnectivityParams{
				Image:           "konnectivity-agent-image",
				ExternalAddress: "konnectivity-server",
				ExternalPort:    8091,
			}

			ReconcileAgentDaemonSet(daemonset, params, hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}, configv1.ProxyStatus{})

			args := daemonset.Spec.Template.Spec.Containers[0].Args
			if !slices.Contains(args, "--sync-forever") {
				t.Errorf("expected konnectivity-agent container args to contain --sync-forever, got: %v", args)
			}
		})
	}
}
