package cpo

import "testing"

func TestEtcdStatefulSet(t *testing.T) {
	sts := EtcdStatefulSet("test-ns")
	if sts.Name != "etcd" {
		t.Errorf("expected name %q, got %q", "etcd", sts.Name)
	}
	if sts.Namespace != "test-ns" {
		t.Errorf("expected namespace %q, got %q", "test-ns", sts.Namespace)
	}
}
