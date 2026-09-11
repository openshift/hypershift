package cpo

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestKASCustomKubeconfigSecret(t *testing.T) {
	tests := []struct {
		name         string
		namespace    string
		ref          *hyperv1.KubeconfigSecretRef
		expectedName string
	}{
		{
			name:         "When ref is nil, it should return Secret with default name",
			namespace:    "test-ns",
			ref:          nil,
			expectedName: "custom-admin-kubeconfig",
		},
		{
			name:         "When ref is provided, it should use ref.Name",
			namespace:    "test-ns",
			ref:          &hyperv1.KubeconfigSecretRef{Name: "my-kubeconfig"},
			expectedName: "my-kubeconfig",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := KASCustomKubeconfigSecret(tt.namespace, tt.ref)
			if s.Name != tt.expectedName {
				t.Errorf("expected name %q, got %q", tt.expectedName, s.Name)
			}
			if s.Namespace != tt.namespace {
				t.Errorf("expected namespace %q, got %q", tt.namespace, s.Namespace)
			}
		})
	}
}

func TestKubeAPIServerService(t *testing.T) {
	svc := KubeAPIServerService("test-ns")
	if svc.Name != "kube-apiserver" {
		t.Errorf("expected name %q, got %q", "kube-apiserver", svc.Name)
	}
	if svc.Namespace != "test-ns" {
		t.Errorf("expected namespace %q, got %q", "test-ns", svc.Namespace)
	}
}

func TestKubeAPIServerServiceAzureLB(t *testing.T) {
	svc := KubeAPIServerServiceAzureLB("test-ns")
	if svc.Name != "kube-apiserverlb" {
		t.Errorf("expected name %q, got %q", "kube-apiserverlb", svc.Name)
	}
	if svc.Namespace != "test-ns" {
		t.Errorf("expected namespace %q, got %q", "test-ns", svc.Namespace)
	}
}
