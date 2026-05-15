package etcdbackupgcs

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	rbacv1 "k8s.io/api/rbac/v1"
)

func adaptRole(cpContext component.WorkloadContext, role *rbacv1.Role) error {
	hcp := cpContext.HCP
	if hcp.Spec.SecretEncryption == nil || hcp.Spec.SecretEncryption.Type != hyperv1.AESCBC || hcp.Spec.SecretEncryption.AESCBC == nil {
		return nil
	}

	secretNames := []string{hcp.Spec.SecretEncryption.AESCBC.ActiveKey.Name}
	if hcp.Spec.SecretEncryption.AESCBC.BackupKey != nil {
		secretNames = append(secretNames, hcp.Spec.SecretEncryption.AESCBC.BackupKey.Name)
	}

	for i, rule := range role.Rules {
		if len(rule.Resources) == 1 && rule.Resources[0] == "secrets" {
			role.Rules[i].ResourceNames = append(role.Rules[i].ResourceNames, secretNames...)
			break
		}
	}

	return nil
}
