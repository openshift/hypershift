package controlplaneoperator

import (
	"os"

	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"

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

	if azureutil.IsAroHCP() {
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
	}
	return nil
}
