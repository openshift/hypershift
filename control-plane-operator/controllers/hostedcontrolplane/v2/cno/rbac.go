package cno

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

func adaptRole(cpContext component.WorkloadContext, role *rbacv1.Role) error {
	if cpContext.HCP.Spec.Networking.NetworkType == hyperv1.OVNKubernetes {
		return nil
	}
	// The RBAC below is required when the networkType is not OVNKubernetes https://issues.redhat.com/browse/OCPBUGS-26977
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{corev1.SchemeGroupVersion.Group},
			Resources: []string{
				"configmaps",
			},
			ResourceNames: []string{
				"openshift-service-ca.crt",
				"root-ca",
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{corev1.SchemeGroupVersion.Group},
			Resources: []string{
				"configmaps",
			},
			ResourceNames: []string{
				"ovnkube-identity-cm",
			},
			Verbs: []string{
				"list",
				"get",
				"watch",
				"create",
				"patch",
				"update",
			},
		},
		{
			APIGroups: []string{appsv1.SchemeGroupVersion.Group},
			Resources: []string{"statefulsets", "deployments"},
			Verbs:     []string{"list", "watch"},
		},
		{
			APIGroups: []string{appsv1.SchemeGroupVersion.Group},
			Resources: []string{"deployments"},
			ResourceNames: []string{
				"multus-admission-controller",
				"network-node-identity",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{corev1.SchemeGroupVersion.Group},
			Resources: []string{"services"},
			ResourceNames: []string{
				"multus-admission-controller",
				"network-node-identity",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{hyperv1.GroupVersion.Group},
			Resources: []string{
				"hostedcontrolplanes",
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{hyperv1.GroupVersion.Group},
			Resources: []string{
				"hostedcontrolplanes/status",
			},
			Verbs: []string{"*"},
		},
	}

	return nil
}
