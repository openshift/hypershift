package manifests

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

const (
	EtcdDefragName = "etcd-defrag-controller"
)

func EtcdStatefulSet(ns string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd",
			Namespace: ns,
		},
	}
}

func EtcdDiscoveryService(ns string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-discovery",
			Namespace: ns,
		},
	}
}

func EtcdClientService(ns string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-client",
			Namespace: ns,
		},
	}
}

func EtcdServiceMonitor(ns string) *prometheusoperatorv1.ServiceMonitor {
	return &prometheusoperatorv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd",
			Namespace: ns,
		},
	}
}

func EtcdPodDisruptionBudget(ns string) *policyv1.PodDisruptionBudget {
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd",
			Namespace: ns,
		},
	}
}

func EtcdDefragControllerRole(ns string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      EtcdDefragName,
			Namespace: ns,
		},
	}
}

func EtcdDefragControllerRoleBinding(ns string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      EtcdDefragName,
			Namespace: ns,
		},
	}

}

func EtcdDefragControllerServiceAccount(ns string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      EtcdDefragName,
			Namespace: ns,
		},
	}
}

func EtcdBackupServiceAccount(hcpNamespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-backup-sa",
			Namespace: hcpNamespace,
		},
	}
}

func EtcdBackupCronJob(hcpNamespace string) *batchv1.CronJob {
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-backup",
			Namespace: hcpNamespace,
		},
		Spec: batchv1.CronJobSpec{
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": "etcd-backup",
							},
						},
					},
				},
			},
		},
	}
}

// EtcdBackupJob returns a Job manifest for running an etcd backup from the
// HyperShift Operator namespace. The controller populates the full PodSpec
// with init containers (fetch-etcd-certs, etcd-backup) and main container
// (etcd-upload).
func EtcdBackupJob(ns string, hcpName string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("etcd-backup-%s", hcpName),
			Namespace: ns,
			Labels: map[string]string{
				"app":                         "etcd-backup",
				"hypershift.openshift.io/hcp": hcpName,
			},
		},
	}
}

// EtcdBackupJobServiceAccount returns a ServiceAccount for etcd backup Jobs
// running in the given namespace. This SA is used by Jobs in the HO namespace.
func EtcdBackupJobServiceAccount(ns string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-backup-job",
			Namespace: ns,
		},
	}
}

// EtcdBackupJobRole returns a Role in the HCP namespace granting read access
// to etcd TLS secrets and CA configmaps needed by the fetch-etcd-certs init
// container.
func EtcdBackupJobRole(hcpNamespace string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-backup-job",
			Namespace: hcpNamespace,
		},
	}
}

// EtcdBackupJobRoleBinding binds the etcd-backup-job ServiceAccount (in the HO
// namespace) to the Role in the HCP namespace, enabling cross-namespace access
// to etcd TLS resources.
func EtcdBackupJobRoleBinding(hcpNamespace string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-backup-job",
			Namespace: hcpNamespace,
		},
	}
}

// EtcdBackupNetworkPolicy returns a NetworkPolicy in the HCP namespace that
// allows ingress from etcd backup Job pods (in the HO namespace) to etcd on
// port 2379. This policy is created before the Job and cleaned up after it
// completes.
func EtcdBackupNetworkPolicy(hcpNamespace string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "allow-etcd-backup",
			Namespace: hcpNamespace,
		},
	}
}
