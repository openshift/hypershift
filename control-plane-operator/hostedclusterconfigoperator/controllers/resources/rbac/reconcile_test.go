package rbac

import (
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
)

func TestReconcileMetricsResourcesClusterRole(t *testing.T) {
	t.Run("When reconciling it should set the correct policy rules", func(t *testing.T) {
		role := &rbacv1.ClusterRole{}
		if err := ReconcileMetricsResourcesClusterRole(role); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(role.Rules) != 1 {
			t.Fatalf("expected 1 rule, got %d", len(role.Rules))
		}
		rule := role.Rules[0]
		if len(rule.NonResourceURLs) != 1 || rule.NonResourceURLs[0] != "/metrics/resources" {
			t.Errorf("expected NonResourceURLs [\"/metrics/resources\"], got %v", rule.NonResourceURLs)
		}
		if len(rule.Verbs) != 1 || rule.Verbs[0] != "get" {
			t.Errorf("expected Verbs [\"get\"], got %v", rule.Verbs)
		}
	})
}

func TestReconcileMetricsResourcesClusterRoleBinding(t *testing.T) {
	t.Run("When reconciling it should set the correct role ref and subjects", func(t *testing.T) {
		binding := &rbacv1.ClusterRoleBinding{}
		if err := ReconcileMetricsResourcesClusterRoleBinding(binding); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if binding.RoleRef.Kind != "ClusterRole" {
			t.Errorf("expected RoleRef.Kind ClusterRole, got %s", binding.RoleRef.Kind)
		}
		if binding.RoleRef.Name != "hypershift-metrics-resources-reader" {
			t.Errorf("expected RoleRef.Name hypershift-metrics-resources-reader, got %s", binding.RoleRef.Name)
		}
		if len(binding.Subjects) != 1 {
			t.Fatalf("expected 1 subject, got %d", len(binding.Subjects))
		}
		subject := binding.Subjects[0]
		if subject.Kind != "User" {
			t.Errorf("expected subject Kind User, got %s", subject.Kind)
		}
		if subject.Name != "system:serviceaccount:hypershift:prometheus" {
			t.Errorf("expected subject Name system:serviceaccount:hypershift:prometheus, got %s", subject.Name)
		}
	})
}
