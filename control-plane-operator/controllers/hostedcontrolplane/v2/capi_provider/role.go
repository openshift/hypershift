package capiprovider

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	rbacv1 "k8s.io/api/rbac/v1"
)

func (capi *CAPIProviderOptions) adaptRole(cpContext component.WorkloadContext, role *rbacv1.Role) error {
	if capi.platformPolicyRules != nil {
		role.Rules = append(role.Rules, capi.platformPolicyRules...)
	}

	if role.Annotations == nil {
		role.Annotations = make(map[string]string)
	}
	role.Annotations[util.HostedClusterAnnotation] = cpContext.HCP.Annotations[util.HostedClusterAnnotation]
	return nil
}
