package cvo

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ResourcesToRemove(platformType hyperv1.PlatformType) []client.Object {
	switch platformType {
	case hyperv1.IBMCloudPlatform, hyperv1.PowerVSPlatform:
		return []client.Object{
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "network-operator", Namespace: "openshift-network-operator"}},
			&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "default-account-cluster-network-operator"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "cluster-node-tuning-operator", Namespace: "openshift-cluster-node-tuning-operator"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "cluster-image-registry-operator", Namespace: "openshift-image-registry"}},
		}
	default:
		return []client.Object{
			&apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "machineconfigs.machineconfiguration.openshift.io"}},
			&apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "machineconfigpools.machineconfiguration.openshift.io"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "network-operator", Namespace: "openshift-network-operator"}},
			&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "default-account-cluster-network-operator"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "cluster-node-tuning-operator", Namespace: "openshift-cluster-node-tuning-operator"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "cluster-image-registry-operator", Namespace: "openshift-image-registry"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "cluster-storage-operator", Namespace: "openshift-cluster-storage-operator"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "csi-snapshot-controller-operator", Namespace: "openshift-cluster-storage-operator"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "aws-ebs-csi-driver-operator", Namespace: "openshift-cluster-csi-drivers"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "aws-ebs-csi-driver-controller", Namespace: "openshift-cluster-csi-drivers"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "csi-snapshot-controller", Namespace: "openshift-cluster-storage-operator"}},
		}
	}
}
