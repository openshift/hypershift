package oapi

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/operator/apiserver/audit"

	corev1 "k8s.io/api/core/v1"
)

const (
	auditPolicyConfigMapKey  = "policy.yaml"
	auditPolicyProfileMapKey = "profile"
)

func ReconcileAuditConfig(cm *corev1.ConfigMap, ownerRef config.OwnerRef, auditConfig configv1.Audit) error {
	ownerRef.ApplyTo(cm)
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	policy, err := audit.GetAuditPolicy(auditConfig)
	if err != nil {
		return fmt.Errorf("failed to get audit policy: %w", err)
	}
	policyBytes, err := config.SerializeAuditPolicy(policy)
	if err != nil {
		return err
	}
	cm.Data[auditPolicyConfigMapKey] = string(policyBytes)
	cm.Data[auditPolicyProfileMapKey] = string(auditConfig.Profile)
	return nil
}
