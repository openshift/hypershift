package hcco

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestGlobalPullSecret(t *testing.T) {
	s := GlobalPullSecret()
	if s.Name != "global-pull-secret" {
		t.Errorf("expected name %q, got %q", "global-pull-secret", s.Name)
	}
	if s.Namespace != GlobalPullSecretNamespace {
		t.Errorf("expected namespace %q, got %q", GlobalPullSecretNamespace, s.Namespace)
	}
	if s.Type != corev1.SecretTypeDockerConfigJson {
		t.Errorf("expected type %q, got %q", corev1.SecretTypeDockerConfigJson, s.Type)
	}
}

func TestGlobalPullSecretDaemonSet(t *testing.T) {
	ds := GlobalPullSecretDaemonSet()
	if ds.Name != "global-pull-secret-syncer" {
		t.Errorf("expected name %q, got %q", "global-pull-secret-syncer", ds.Name)
	}
	if ds.Namespace != GlobalPullSecretNamespace {
		t.Errorf("expected namespace %q, got %q", GlobalPullSecretNamespace, ds.Namespace)
	}
}

func TestOriginalPullSecret(t *testing.T) {
	s := OriginalPullSecret()
	if s.Name != "original-pull-secret" {
		t.Errorf("expected name %q, got %q", "original-pull-secret", s.Name)
	}
	if s.Namespace != GlobalPullSecretNamespace {
		t.Errorf("expected namespace %q, got %q", GlobalPullSecretNamespace, s.Namespace)
	}
}

func TestAdditionalPullSecret(t *testing.T) {
	s := AdditionalPullSecret()
	if s.Name != "additional-pull-secret" {
		t.Errorf("expected name %q, got %q", "additional-pull-secret", s.Name)
	}
	if s.Namespace != GlobalPullSecretNamespace {
		t.Errorf("expected namespace %q, got %q", GlobalPullSecretNamespace, s.Namespace)
	}
}
