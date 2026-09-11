package hcco

import "testing"

func TestKonnectivityAgentDaemonSet(t *testing.T) {
	ds := KonnectivityAgentDaemonSet()
	if ds.Name != "konnectivity-agent" {
		t.Errorf("expected name %q, got %q", "konnectivity-agent", ds.Name)
	}
	if ds.Namespace != "kube-system" {
		t.Errorf("expected namespace %q, got %q", "kube-system", ds.Namespace)
	}
}
