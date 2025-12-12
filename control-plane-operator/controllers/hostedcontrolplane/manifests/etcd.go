package manifests

import (
	"fmt"

	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
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
	}
}

// resourceNameForShard generates resource names for etcd shards
// For backward compatibility, the default shard uses the base name without suffix
// Named shards use the pattern: baseName-shardName
func resourceNameForShard(baseName, shardName string) string {
	if shardName == "default" {
		return baseName
	}
	return fmt.Sprintf("%s-%s", baseName, shardName)
}

// EtcdStatefulSetForShard returns a StatefulSet manifest for a specific etcd shard
func EtcdStatefulSetForShard(ns, shardName string) *appsv1.StatefulSet {
	sts, err := assets.LoadStatefulSetManifest("etcd")
	if err != nil {
		panic(fmt.Sprintf("failed to load etcd statefulset asset: %v", err))
	}
	sts.Name = resourceNameForShard("etcd", shardName)
	sts.Namespace = ns
	return sts
}

// EtcdClientServiceForShard returns a client Service manifest for a specific etcd shard
func EtcdClientServiceForShard(ns, shardName string) *corev1.Service {
	svc := &corev1.Service{}
	if _, _, err := assets.LoadManifestInto("etcd", "service.yaml", svc); err != nil {
		panic(fmt.Sprintf("failed to load etcd service asset: %v", err))
	}
	svc.Name = resourceNameForShard("etcd-client", shardName)
	svc.Namespace = ns
	return svc
}

// EtcdDiscoveryServiceForShard returns a discovery Service manifest for a specific etcd shard
func EtcdDiscoveryServiceForShard(ns, shardName string) *corev1.Service {
	svc := &corev1.Service{}
	if _, _, err := assets.LoadManifestInto("etcd", "discovery-service.yaml", svc); err != nil {
		panic(fmt.Sprintf("failed to load etcd discovery service asset: %v", err))
	}
	svc.Name = resourceNameForShard("etcd-discovery", shardName)
	svc.Namespace = ns
	return svc
}

// EtcdServiceMonitorForShard returns a ServiceMonitor manifest for a specific etcd shard
func EtcdServiceMonitorForShard(ns, shardName string) *prometheusoperatorv1.ServiceMonitor {
	sm := &prometheusoperatorv1.ServiceMonitor{}
	if _, _, err := assets.LoadManifestInto("etcd", "servicemonitor.yaml", sm); err != nil {
		panic(fmt.Sprintf("failed to load etcd servicemonitor asset: %v", err))
	}
	sm.Name = resourceNameForShard("etcd", shardName)
	sm.Namespace = ns
	return sm
}

// EtcdPodDisruptionBudgetForShard returns a PodDisruptionBudget manifest for a specific etcd shard
func EtcdPodDisruptionBudgetForShard(ns, shardName string) *policyv1.PodDisruptionBudget {
	pdb := &policyv1.PodDisruptionBudget{}
	if _, _, err := assets.LoadManifestInto("etcd", "pdb.yaml", pdb); err != nil {
		panic(fmt.Sprintf("failed to load etcd pdb asset: %v", err))
	}
	pdb.Name = resourceNameForShard("etcd", shardName)
	pdb.Namespace = ns
	return pdb
}
