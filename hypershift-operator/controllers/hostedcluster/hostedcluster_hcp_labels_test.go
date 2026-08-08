package hostedcluster

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileHostedControlPlane_WhenActionableLabelsAreRemovedItShouldClearStaleHostedControlPlaneLabels(t *testing.T) {
	t.Parallel()

	hcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "example",
			Namespace: "clusters",
			Labels:    map[string]string{},
		},
	}

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "example",
			Namespace: "clusters-example",
			Labels: map[string]string{
				"api.openshift.com/id": "stale",
				"other":                "keep",
			},
		},
	}

	if err := reconcileHostedControlPlane(hcp, hcluster, false, false, func() (map[string]string, error) {
		return map[string]string{}, nil
	}); err != nil {
		t.Fatalf("reconcileHostedControlPlane returned error: %v", err)
	}

	if _, exists := hcp.Labels["api.openshift.com/id"]; exists {
		t.Fatal("expected stale api.openshift.com label to be removed from hosted control plane")
	}
	if value := hcp.Labels["other"]; value != "keep" {
		t.Fatalf("expected unrelated label to be preserved, got %q", value)
	}
}
