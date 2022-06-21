package kubevirt

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/config"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ccmServiceAccount(ns string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubevirt-cloud-controller-manager",
			Namespace: ns,
		},
	}
}

func ccmRole(ns string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubevirt-cloud-controller-manager",
			Namespace: ns,
		},
	}
}

func ccmRoleBinding(ns string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubevirt-cloud-controller-manager",
			Namespace: ns,
		},
	}
}

// func ccmClusterRole() *rbacv1.ClusterRole {
// 	return &rbacv1.ClusterRole{
// 		ObjectMeta: metav1.ObjectMeta{
// 			Name: "kubevirt-cloud-controller-manager-clusterrole",
// 		},
// 	}
// }
//
// func ccmClusterRoleBinding() *rbacv1.ClusterRoleBinding {
// 	return &rbacv1.ClusterRoleBinding{
// 		ObjectMeta: metav1.ObjectMeta{
// 			Name: "kubevirt-cloud-controller-manager-clusterrole",
// 		},
// 	}
// }

func ReconcileCloudConfig(cm *corev1.ConfigMap, hcp *hyperv1.HostedControlPlane, kubeconfig []byte) error {
	cfg := cloudConfig(kubeconfig)
	serializedCfg, err := cfg.String()
	if err != nil {
		return fmt.Errorf("failed to serialize cloudconfig: %w", err)
	}

	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[CloudConfigKey] = string(serializedCfg)

	return nil
}

func reconcileCCMServiceAccount(sa *corev1.ServiceAccount, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(sa)
	return nil
}

func reconcileCCMRole(role *rbacv1.Role, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(role)
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"kubevirt.io"},
			Resources: []string{"virtualmachines"},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{"kubevirt.io"},
			Resources: []string{"virtualmachineinstances"},
			Verbs: []string{
				"get",
				"list",
				"watch",
				"update",
			},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"pods"},
			Verbs:     []string{rbacv1.VerbAll},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"services"},
			Verbs:     []string{rbacv1.VerbAll},
		},
	}
	return nil
}

func reconcileCCMRoleBinding(roleBinding *rbacv1.RoleBinding, ownerRef config.OwnerRef, sa *corev1.ServiceAccount, role *rbacv1.Role) error {
	ownerRef.ApplyTo(roleBinding)
	roleBinding.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.GroupName,
		Kind:     "Role",
		Name:     role.Name,
	}
	roleBinding.Subjects = []rbacv1.Subject{
		{
			Namespace: sa.Namespace,
			Kind:      rbacv1.ServiceAccountKind,
			Name:      sa.Name,
		},
	}
	return nil
}

// func reconcileCCMClusterRole(clusterRole *rbacv1.ClusterRole) error {
// 	clusterRole.Rules = []rbacv1.PolicyRule{
// 		{
// 			APIGroups: []string{""},
// 			Resources: []string{"nodes"},
// 			Verbs:     []string{"get"},
// 		},
// 	}
// 	return nil
// }
//
// func reconcileCCMClusterRoleBinding(clusterRoleBinding *rbacv1.ClusterRoleBinding, sa *corev1.ServiceAccount, clusterRole *rbacv1.ClusterRole) error {
// 	clusterRoleBinding.RoleRef = rbacv1.RoleRef{
// 		APIGroup: rbacv1.GroupName,
// 		Kind:     "ClusterRole",
// 		Name:     clusterRole.Name,
// 	}
// 	clusterRoleBinding.Subjects = []rbacv1.Subject{
// 		{
// 			Namespace: sa.Namespace,
// 			Kind:      rbacv1.ServiceAccountKind,
// 			Name:      sa.Name,
// 		},
// 	}
// 	return nil
// }
