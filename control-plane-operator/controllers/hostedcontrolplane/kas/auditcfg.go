package kas

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/operator/apiserver/audit"

	corev1 "k8s.io/api/core/v1"
)

const (
	AuditPolicyConfigMapKey  = "policy.yaml"
	AuditPolicyProfileMapKey = "profile"
)

func ReconcileAuditConfig(auditCfgMap *corev1.ConfigMap, ownerRef config.OwnerRef, auditConfig configv1.Audit) error {
	ownerRef.ApplyTo(auditCfgMap)
	if auditCfgMap.Data == nil {
		auditCfgMap.Data = map[string]string{}
	}
	policy, err := audit.GetAuditPolicy(auditConfig)
	if err != nil {
		return fmt.Errorf("failed to get audit policy: %w", err)
	}
	policyBytes, err := config.SerializeAuditPolicy(policy)
	if err != nil {
		return err
	}
	auditCfgMap.Data[AuditPolicyConfigMapKey] = string(policyBytes)
	auditCfgMap.Data[AuditPolicyProfileMapKey] = string(auditConfig.Profile)
	return nil
}
