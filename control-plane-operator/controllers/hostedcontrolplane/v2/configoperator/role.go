package configoperator

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

func adaptRole(cpContext component.ControlPlaneContext, role *rbacv1.Role) error {
	switch cpContext.HCP.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		// By isolating these rules behind the KubevirtPlatform switch case,
		// we know we can add/remove from this list in the future without
		// impacting other platforms.
		role.Rules = append(role.Rules, []rbacv1.PolicyRule{
			// These are needed by the KubeVirt platform in order to
			// use a subdomain route for the guest cluster's default
			// ingress
			{
				APIGroups: []string{"route.openshift.io"},
				Resources: []string{"routes"},
				Verbs: []string{
					"create",
					"get",
					"patch",
					"update",
					"list",
					"watch",
				},
			},
			{
				APIGroups: []string{"route.openshift.io"},
				Resources: []string{"routes/custom-host"},
				Verbs: []string{
					"create",
				},
			},
			{
				APIGroups: []string{discoveryv1.SchemeGroupVersion.Group},
				Resources: []string{
					"endpointslices",
					"endpointslices/restricted",
				},
				Verbs: []string{
					"delete",
					"create",
					"get",
					"patch",
					"update",
					"list",
					"watch",
				},
			},
			{
				APIGroups: []string{
					"kubevirt.io",
				},
				Resources: []string{"virtualmachines"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{
					"kubevirt.io",
				},
				Resources: []string{"virtualmachines/finalizers"},
				Verbs:     []string{"update"},
			},
			{
				APIGroups: []string{corev1.SchemeGroupVersion.Group},
				Resources: []string{
					"services",
				},
				Verbs: []string{
					"create",
					"get",
					"patch",
					"update",
					"list",
					"watch",
				},
			},
		}...)
	}
	// TODO (jparrill): Add RBAC specific needs for Agent platform
	return nil
}
