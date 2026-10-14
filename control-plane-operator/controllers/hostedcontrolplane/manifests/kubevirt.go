package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
)

const KubevirtCSIDriverTenantNamespaceStr = "openshift-cluster-csi-drivers"

func KubevirtCSIDriverInfraSA(ns string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubevirt-csi",
			Namespace: ns,
		},
	}
}

func KubevirtCSIDriverTenantControllerSA(ns string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubevirt-csi-controller-sa",
			Namespace: ns,
		},
	}
}

func KubevirtCSIDriverTenantControllerClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kubevirt-csi-controller-cr",
		},
	}
}

func KubevirtCSIDriverTenantControllerClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kubevirt-csi-controller-binding",
		},
	}
}

func KubevirtCSIDriverTenantNodeSA(ns string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubevirt-csi-node-sa",
			Namespace: ns,
		},
	}
}

func KubevirtCSIDriverTenantNodeClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kubevirt-csi-node-cr",
		},
	}
}

func KubevirtCSIDriverTenantNodeClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kubevirt-csi-node-binding",
		},
	}
}

func KubevirtCSIDriverDefaultTenantStorageClass() *storagev1.StorageClass {
	return &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kubevirt-csi-infra-default",
		},
	}
}

func KubevirtCSIDriverVolumeSnapshotClass() *snapshotv1.VolumeSnapshotClass {
	return &snapshotv1.VolumeSnapshotClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kubevirt-csi-snapshot",
		},
	}
}

func KubevirtCSIDriverResource() *storagev1.CSIDriver {
	return &storagev1.CSIDriver{
		ObjectMeta: metav1.ObjectMeta{
			Name: "csi.kubevirt.io",
		},
	}
}

func KubevirtCSIDriverDaemonSet(ns string) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubevirt-csi-node",
			Namespace: ns,
		},
	}
}

func KubevirtCSIDriverTenantNamespace(ns string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}
}
