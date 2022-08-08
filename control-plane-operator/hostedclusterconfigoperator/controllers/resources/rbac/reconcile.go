package rbac

import (
	hccomanifests "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	rbacv1 "k8s.io/api/rbac/v1"
)

func ReconcileCSRApproverClusterRole(r *rbacv1.ClusterRole) error {
	r.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"certificates.k8s.io"},
			Resources: []string{"certificatesigningrequests"},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{"certificates.k8s.io"},
			Resources: []string{"certificatesigningrequests/approval"},
			Verbs: []string{
				"update",
			},
		},
		{
			APIGroups:     []string{"certificates.k8s.io"},
			Resources:     []string{"signers"},
			ResourceNames: []string{"kubernetes.io/kube-apiserver-client"},
			Verbs: []string{
				"approve",
			},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"events"},
			Verbs: []string{
				"create",
				"patch",
				"update",
			},
		},
	}
	return nil
}

func ReconcileCSRApproverClusterRoleBinding(r *rbacv1.ClusterRoleBinding) error {
	if r.Annotations == nil {
		r.Annotations = map[string]string{}
	}
	r.Annotations["rbac.authorization.kubernetes.io/autoupdate"] = "true"

	r.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "ClusterRole",
		Name:     hccomanifests.CSRApproverClusterRoleBinding().Name,
	}
	r.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      "cluster-csr-approver-controller",
			Namespace: "openshift-infra",
		},
	}
	return nil
}

func ReconcileIngressToRouteControllerClusterRole(r *rbacv1.ClusterRole) error {
	r.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"secrets", "services"},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{"networking.k8s.io"},
			Resources: []string{"ingress"},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{"networking.k8s.io"},
			Resources: []string{"ingresses/status"},
			Verbs: []string{
				"update",
			},
		},
		{
			APIGroups: []string{"route.openshift.io"},
			Resources: []string{"routes"},
			Verbs: []string{
				"create",
				"delete",
				"patch",
				"update",
				"get",
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{"route.openshift.io"},
			Resources: []string{"routes/custom-host"},
			Verbs: []string{
				"create",
				"update",
			},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"events"},
			Verbs: []string{
				"create",
				"patch",
				"update",
			},
		},
	}
	return nil
}

func ReconcileReconcileIngressToRouteControllerRole(r *rbacv1.Role) error {
	r.Rules = []rbacv1.PolicyRule{
		{
			APIGroups:     []string{"coordination.k8s.io"},
			Resources:     []string{"leases"},
			ResourceNames: []string{"openshift-route-controllers"},
			Verbs:         []string{"get", "update"},
		},
		{
			APIGroups: []string{"coordination.k8s.io"},
			Resources: []string{"leases"},
			Verbs:     []string{"create"},
		},
	}
	return nil
}

func ReconcileIngressToRouteControllerClusterRoleBinding(r *rbacv1.ClusterRoleBinding) error {
	r.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "ClusterRole",
		Name:     hccomanifests.IngressToRouteControllerClusterRole().Name,
	}
	r.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      "ingress-to-route-controller",
			Namespace: "openshift-infra",
		},
	}
	return nil
}

func ReconcileIngressToRouteControllerRoleBinding(r *rbacv1.RoleBinding) error {
	r.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "Role",
		Name:     hccomanifests.IngressToRouteControllerRole().Name,
	}
	r.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      "ingress-to-route-controller",
			Namespace: "openshift-infra",
		},
	}
	return nil
}

func ReconcileNamespaceSecurityAllocationControllerClusterRole(r *rbacv1.ClusterRole) error {
	r.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{
				"security.openshift.io",
				"security.internal.openshift.io",
			},
			Resources: []string{"rangeallocations"},
			Verbs: []string{
				"create",
				"get",
				"update",
			},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"namespaces"},
			Verbs: []string{
				"get",
				"list",
				"watch",
				"update",
				"patch",
			},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"events"},
			Verbs: []string{
				"create",
				"patch",
				"update",
			},
		},
	}
	return nil
}

func ReconcileNamespaceSecurityAllocationControllerClusterRoleBinding(r *rbacv1.ClusterRoleBinding) error {
	r.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "ClusterRole",
		Name:     hccomanifests.NamespaceSecurityAllocationControllerClusterRole().Name,
	}
	r.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      "namespace-security-allocation-controller",
			Namespace: "openshift-infra",
		},
	}
	return nil
}

func ReconcileNodeBootstrapperClusterRoleBinding(r *rbacv1.ClusterRoleBinding) error {
	r.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "ClusterRole",
		Name:     "system:node-bootstrapper",
	}
	r.Subjects = []rbacv1.Subject{
		{
			APIGroup: rbacv1.SchemeGroupVersion.Group,
			Kind:     "User",
			Name:     "system:serviceaccount:openshift-machine-config-operator:node-bootstrapper",
		},
	}
	return nil
}

func ReconcileCSRRenewalClusterRoleBinding(r *rbacv1.ClusterRoleBinding) error {
	r.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "ClusterRole",
		Name:     "system:certificates.k8s.io:certificatesigningrequests:selfnodeclient",
	}
	r.Subjects = []rbacv1.Subject{
		{
			APIGroup: rbacv1.SchemeGroupVersion.Group,
			Kind:     "Group",
			Name:     "system:nodes",
		},
	}
	return nil
}

func ReconcileGenericMetricsClusterRoleBinding(cn string) func(*rbacv1.ClusterRoleBinding) error {
	return func(r *rbacv1.ClusterRoleBinding) error {
		r.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.SchemeGroupVersion.Group,
			Kind:     "ClusterRole",
			Name:     "system:monitoring",
		}
		r.Subjects = []rbacv1.Subject{
			{
				APIGroup: rbacv1.SchemeGroupVersion.Group,
				Kind:     "User",
				Name:     cn,
			},
		}
		return nil
	}
}
