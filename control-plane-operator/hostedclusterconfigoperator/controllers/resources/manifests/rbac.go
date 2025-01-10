package manifests

import (
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CSRApproverClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:controller:cluster-csr-approver-controller",
		},
	}
}

func CSRApproverClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:controller:cluster-csr-approver-controller",
		},
	}
}

func IngressToRouteControllerClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:openshift-controller-manager:ingress-to-route-controller",
		},
	}
}

func IngressToRouteControllerRole() *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-route-controller-manager",
			Name:      "openshift-route-controllers",
		},
	}
}

func IngressToRouteControllerClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:openshift-controller-manager:ingress-to-route-controller",
		},
	}
}

func IngressToRouteControllerRoleBinding() *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-route-controller-manager",
			Name:      "openshift-route-controllers",
		},
	}
}

func NamespaceSecurityAllocationControllerClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:controller:namespace-security-allocation-controller",
		},
	}
}

func NamespaceSecurityAllocationControllerClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:controller:namespace-security-allocation-controller",
		},
	}
}

func PodSecurityAdmissionLabelSyncerControllerClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:controller:podsecurity-admission-label-syncer-controller",
		},
	}
}

func PodSecurityAdmissionLabelSyncerControllerRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:controller:podsecurity-admission-label-syncer-controller",
		},
	}
}

func PriviligedNamespacesPSALabelSyncerClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:controller:privileged-namespaces-psa-label-syncer",
		},
	}
}

func PriviligedNamespacesPSALabelSyncerClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:controller:privileged-namespaces-psa-label-syncer",
		},
	}
}

func NodeBootstrapperClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "create-csrs-for-bootstrapping",
		},
	}
}

func CSRRenewalClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system-bootstrap-node-renewal",
		},
	}
}

func MetricsClientClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "hypershift-metrics-client",
		},
	}
}

func AuthenticatedReaderForAuthenticatedUserRolebinding() *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "authentication-reader-for-authenticated-users",
			Namespace: "kube-system",
		}}
}

func KCMLeaderElectionRole() *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "system:openshift:leader-election-lock-kube-controller-manager",
			Namespace: "kube-system",
		},
	}
}

func KCMLeaderElectionRoleBinding() *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "system:openshift:leader-election-lock-kube-controller-manager",
			Namespace: "kube-system",
		},
	}
}

func DeployerClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:deployer",
		},
	}
}

func DeployerClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:deployer",
		},
	}
}

func ImageTriggerControllerClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:openshift-controller-manager:image-trigger-controller",
		},
	}
}

func ImageTriggerControllerClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:openshift-controller-manager:image-trigger-controller",
		},
	}
}

func UserOAuthClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:useroauthaccesstoken-manager",
		},
	}
}

func UserOAuthClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:useroauthaccesstoken-manager",
		},
	}
}

func NodePublicInfoViewerClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:node-public-info-viewer",
		},
	}
}

func NodePublicInfoViewerClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:node-public-info-viewer",
		},
	}
}
