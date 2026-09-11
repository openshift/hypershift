package cpo

import "testing"

func TestOAuthServerConfig(t *testing.T) {
	cm := OAuthServerConfig("test-ns")
	if cm.Name != "oauth-openshift" {
		t.Errorf("expected name %q, got %q", "oauth-openshift", cm.Name)
	}
	if cm.Namespace != "test-ns" {
		t.Errorf("expected namespace %q, got %q", "test-ns", cm.Namespace)
	}
}
