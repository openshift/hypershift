package hcco

import "testing"

func TestKubeadminPasswordSecret(t *testing.T) {
	s := KubeadminPasswordSecret("test-ns")
	if s.Name != "kubeadmin-password" {
		t.Errorf("expected name %q, got %q", "kubeadmin-password", s.Name)
	}
	if s.Namespace != "test-ns" {
		t.Errorf("expected namespace %q, got %q", "test-ns", s.Namespace)
	}
}

func TestOAuthDeployment(t *testing.T) {
	d := OAuthDeployment("test-ns")
	if d.Name != "oauth-openshift" {
		t.Errorf("expected name %q, got %q", "oauth-openshift", d.Name)
	}
	if d.Namespace != "test-ns" {
		t.Errorf("expected namespace %q, got %q", "test-ns", d.Namespace)
	}
}
