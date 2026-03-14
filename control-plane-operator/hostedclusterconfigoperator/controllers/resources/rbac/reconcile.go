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

func ReconcileAuthenticatedReaderForAuthenticatedUserRolebinding(r *rbacv1.RoleBinding) error {
	r.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "Role",
		Name:     "extension-apiserver-authentication-reader",
	}
	r.Subjects = []rbacv1.Subject{
		{
			APIGroup: rbacv1.SchemeGroupVersion.Group,
			Kind:     "Group",
			Name:     "system:authenticated",
		},
	}
	return nil
}

func ReconcileKCMLeaderElectionRole(r *rbacv1.Role) error {
	r.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"configmaps", "leases"},
			Verbs:     []string{"create"},
		},
	}

	return nil
}

func ReconcileKCMLeaderElectionRoleBinding(r *rbacv1.RoleBinding) error {
	r.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "Role",
		Name:     "system:openshift:leader-election-lock-kube-controller-manager",
	}
	r.Subjects = []rbacv1.Subject{
		{
			APIGroup: rbacv1.SchemeGroupVersion.Group,
			Kind:     "User",
			Name:     "system:kube-controller-manager",
		},
		{
			Kind:      "ServiceAccount",
			Name:      "namespace-security-allocation-controller",
			Namespace: "openshift-infra",
		},
	}

	return nil
}

func ReconcilePodSecurityAdmissionLabelSyncerControllerClusterRole(r *rbacv1.ClusterRole) error {
	if r.Annotations == nil {
		r.Annotations = map[string]string{}
	}
	r.Annotations["rbac.authorization.kubernetes.io/autoupdate"] = "true"
	r.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"namespaces"},
			Verbs: []string{
				"get",
				"list",
				"update",
				"watch",
				"patch",
			},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"events"},
			Verbs: []string{
				"create",
				"update",
				"patch",
			},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"serviceaccounts"},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{"security.openshift.io"},
			Resources: []string{"securitycontextconstraints"},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{"rbac.authorization.k8s.io"},
			Resources: []string{
				"clusterroles",
				"clusterrolebindings",
				"roles",
				"rolebindings",
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		},
	}
	return nil
}

func ReconcilePodSecurityAdmissionLabelSyncerControllerRoleBinding(r *rbacv1.ClusterRoleBinding) error {
	if r.Annotations == nil {
		r.Annotations = map[string]string{}
	}
	r.Annotations["rbac.authorization.kubernetes.io/autoupdate"] = "true"
	r.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "ClusterRole",
		Name:     "system:openshift:controller:podsecurity-admission-label-syncer-controller",
	}
	r.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      "podsecurity-admission-label-syncer-controller",
			Namespace: "openshift-infra",
		},
	}
	return nil
}

func ReconcilePriviligedNamespacesPSALabelSyncerClusterRole(r *rbacv1.ClusterRole) error {
	if r.Annotations == nil {
		r.Annotations = map[string]string{}
	}
	r.Annotations["rbac.authorization.kubernetes.io/autoupdate"] = "true"
	r.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"namespaces"},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		},
		{
			APIGroups:     []string{""},
			Resources:     []string{"namespaces"},
			ResourceNames: []string{"default", "kube-system", "kube-public"},
			Verbs: []string{
				"patch",
			},
		},
	}
	return nil
}

func ReconcilePriviligedNamespacesPSALabelSyncerClusterRoleBinding(r *rbacv1.ClusterRoleBinding) error {
	if r.Annotations == nil {
		r.Annotations = map[string]string{}
	}
	r.Annotations["rbac.authorization.kubernetes.io/autoupdate"] = "true"
	r.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "ClusterRole",
		Name:     "system:openshift:controller:privileged-namespaces-psa-label-syncer",
	}
	r.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      "privileged-namespaces-psa-label-syncer",
			Namespace: "openshift-infra",
		},
	}
	return nil
}

func ReconcileImageTriggerControllerClusterRole(r *rbacv1.ClusterRole) error {
	r.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{
				"apps.openshift.io",
			},
			Resources: []string{"deploymentconfigs"},
			Verbs: []string{
				"get",
				"list",
				"watch",
				"update",
			},
		},
		{
			APIGroups: []string{
				"build.openshift.io",
			},
			Resources: []string{"buildconfigs"},
			Verbs: []string{
				"get",
				"list",
				"watch",
				"update",
			},
		},
		{
			APIGroups: []string{
				"apps",
			},
			Resources: []string{
				"deployments",
				"daemonsets",
				"statefulsets",
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
				"update",
			},
		},
		{
			APIGroups: []string{
				"batch",
			},
			Resources: []string{
				"cronjobs",
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
				"update",
			},
		},
	}
	return nil
}

func ReconcileImageTriggerControllerClusterRoleBinding(r *rbacv1.ClusterRoleBinding) error {
	r.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "ClusterRole",
		Name:     "system:openshift:openshift-controller-manager:image-trigger-controller",
	}
	r.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Namespace: "openshift-infra",
			Name:      "image-trigger-controller",
		},
	}
	return nil
}

func ReconcileDeployerClusterRole(r *rbacv1.ClusterRole) error {
	if r.Annotations == nil {
		r.Annotations = map[string]string{}
	}
	r.Annotations["rbac.authorization.kubernetes.io/autoupdate"] = "true"
	r.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"replicationcontrollers"},
			Verbs:     []string{"get", "list", "watch", "update", "patch", "delete"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"replicationcontrollers/scale"},
			Verbs:     []string{"get", "update"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"pods"},
			Verbs:     []string{"create", "get", "list", "watch"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"pods/log"},
			Verbs:     []string{"get"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"events"},
			Verbs:     []string{"create", "list"},
		},
		{
			// apiGroups needs to include "" because the conformance test
			// does an explicit SubjectAccessReview against the wrong apiGroup
			// https://github.com/openshift/origin/pull/27689
			APIGroups: []string{"", "image.openshift.io"},
			Resources: []string{"imagestreamtags", "imagetags"},
			Verbs:     []string{"create", "update"},
		},
	}
	return nil
}

func ReconcileDeployerClusterRoleBinding(r *rbacv1.ClusterRoleBinding) error {
	if r.Annotations == nil {
		r.Annotations = map[string]string{}
	}
	r.Annotations["rbac.authorization.kubernetes.io/autoupdate"] = "true"
	r.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "ClusterRole",
		Name:     "system:deployer",
	}
	r.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      "default-rolebindings-controller",
			Namespace: "openshift-infra",
		},
	}
	return nil
}

func ReconcileUserOAuthClusterRoleBinding(r *rbacv1.ClusterRoleBinding) error {
	if r.Annotations == nil {
		r.Annotations = map[string]string{}
	}
	r.Annotations["rbac.authorization.kubernetes.io/autoupdate"] = "true"
	r.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "ClusterRole",
		Name:     "system:openshift:useroauthaccesstoken-manager",
	}
	r.Subjects = []rbacv1.Subject{
		{
			Kind:     "Group",
			APIGroup: rbacv1.SchemeGroupVersion.Group,
			Name:     "system:authenticated:oauth",
		},
	}
	return nil
}

func ReconcileUserOAuthClusterRole(r *rbacv1.ClusterRole) error {
	if r.Annotations == nil {
		r.Annotations = map[string]string{}
	}
	r.Annotations["rbac.authorization.kubernetes.io/autoupdate"] = "true"
	r.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"oauth.openshift.io"},
			Resources: []string{"useroauthaccesstokens"},
			Verbs:     []string{"get", "list", "watch", "delete"},
		},
	}
	return nil
}

func ReconcileAzureDiskCSIDriverNodeServiceAccountClusterRole(r *rbacv1.ClusterRole) error {
	if r.Annotations == nil {
		r.Annotations = map[string]string{}
	}
	r.Annotations["rbac.authorization.kubernetes.io/autoupdate"] = "true"
	r.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"secrets"},
			Verbs:     []string{"get"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"nodes"},
			Verbs: []string{
				"get",
				"list",
				"patch",
				"update",
				"watch",
			},
		},
		{
			APIGroups: []string{"storage.k8s.io"},
			Resources: []string{"csinodes"},
			Verbs:     []string{"get"},
		},
	}
	return nil
}

func ReconcileAzureDiskCSIDriverNodeServiceAccountClusterRoleBinding(r *rbacv1.ClusterRoleBinding) error {
	if r.Annotations == nil {
		r.Annotations = map[string]string{}
	}
	r.Annotations["rbac.authorization.kubernetes.io/autoupdate"] = "true"
	r.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "ClusterRole",
		Name:     "system:openshift:openshift-cluster-csi-drivers:azure-disk-csi-driver-node-sa",
	}
	r.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      "azure-disk-csi-driver-node-sa",
			Namespace: "openshift-cluster-csi-drivers",
		},
	}
	return nil
}

func ReconcileAzureFileCSIDriverNodeServiceAccountClusterRole(r *rbacv1.ClusterRole) error {
	if r.Annotations == nil {
		r.Annotations = map[string]string{}
	}
	r.Annotations["rbac.authorization.kubernetes.io/autoupdate"] = "true"
	r.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"secrets"},
			Verbs:     []string{"get"},
		},
	}
	return nil
}

func ReconcileAzureFileCSIDriverNodeServiceAccountClusterRoleBinding(r *rbacv1.ClusterRoleBinding) error {
	if r.Annotations == nil {
		r.Annotations = map[string]string{}
	}
	r.Annotations["rbac.authorization.kubernetes.io/autoupdate"] = "true"
	r.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "ClusterRole",
		Name:     "system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-node-sa",
	}
	r.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      "azure-file-csi-driver-node-sa",
			Namespace: "openshift-cluster-csi-drivers",
		},
	}
	return nil
}

func ReconcileCloudNetworkConfigControllerServiceAccountClusterRole(r *rbacv1.ClusterRole) error {
	if r.Annotations == nil {
		r.Annotations = map[string]string{}
	}
	r.Annotations["rbac.authorization.kubernetes.io/autoupdate"] = "true"
	r.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"events"},
			Verbs: []string{
				"get",
				"create",
			},
		},
	}
	return nil
}

func ReconcileCloudNetworkConfigControllerServiceAccountClusterRoleBinding(r *rbacv1.ClusterRoleBinding) error {
	if r.Annotations == nil {
		r.Annotations = map[string]string{}
	}
	r.Annotations["rbac.authorization.kubernetes.io/autoupdate"] = "true"
	r.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "ClusterRole",
		Name:     "system:serviceaccount:openshift-cloud-network-config-controller:cloud-network-config-controller",
	}
	r.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      "cloud-network-config-controller",
			Namespace: "openshift-cloud-network-config-controller",
		},
	}
	return nil
}
