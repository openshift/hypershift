package etcdrecovery

import "testing"

func TestEtcdRecoveryJob(t *testing.T) {
	job := EtcdRecoveryJob("test-ns")
	if job.Name != "etcd-recovery" {
		t.Errorf("expected name %q, got %q", "etcd-recovery", job.Name)
	}
	if job.Namespace != "test-ns" {
		t.Errorf("expected namespace %q, got %q", "test-ns", job.Namespace)
	}
	if job.Labels["app"] != "etcd-recovery" {
		t.Errorf("expected label app=%q, got %q", "etcd-recovery", job.Labels["app"])
	}
}
