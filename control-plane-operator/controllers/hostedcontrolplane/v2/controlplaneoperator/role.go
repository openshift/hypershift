package controlplaneoperator

import (
	"os"

	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	rbacv1 "k8s.io/api/rbac/v1"
)

func adaptRole(cpContext component.WorkloadContext, role *rbacv1.Role) error {
	if os.Getenv(config.EnableCVOManagementClusterMetricsAccessEnvVar) == "1" {
		role.Rules = append(role.Rules, rbacv1.PolicyRule{
			APIGroups: []string{"metrics.k8s.io"},
			Resources: []string{"pods"},
			Verbs:     []string{"get"},
		})
	}

	// Add SecretProviderClass RBAC unconditionally.
	// The SecretProviderClass CRD may be installed by the Secrets Store CSI Driver operator
	// or other components, independent of HyperShift. While only Azure platforms create
	// SecretProviderClass resources, CPOv2's component framework processes all platform
	// components and can trigger informer creation. Without RBAC, controller-runtime's
	// cache fails to sync, blocking all reconciliation.
	role.Rules = append(role.Rules, rbacv1.PolicyRule{
		APIGroups: []string{"secrets-store.csi.x-k8s.io"},
		Resources: []string{"secretproviderclasses"},
		Verbs: []string{
			"get",
			"list",
			"create",
			"update",
			"watch",
		},
	})

	if role.Annotations == nil {
		role.Annotations = map[string]string{}
	}
	role.Annotations[util.HostedClusterAnnotation] = cpContext.HCP.Annotations[util.HostedClusterAnnotation]

	return nil
}
