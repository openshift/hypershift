package kas

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/operator/apiserver/audit"

	corev1 "k8s.io/api/core/v1"
)

const (
	AuditPolicyConfigMapKey  = "policy.yaml"
	AuditPolicyProfileMapKey = "profile"
)

func AdaptAuditConfig(cpContext component.WorkloadContext, auditCfgMap *corev1.ConfigMap) error {
	auditConfig := cpContext.HCP.Spec.Configuration.GetAuditPolicyConfig()
	policy, err := audit.GetAuditPolicy(auditConfig)
	if err != nil {
		return fmt.Errorf("failed to get audit policy: %w", err)
	}
	policyBytes, err := config.SerializeAuditPolicy(policy)
	if err != nil {
		return err
	}

	if auditCfgMap.Data == nil {
		auditCfgMap.Data = map[string]string{}
	}
	auditCfgMap.Data[AuditPolicyConfigMapKey] = string(policyBytes)
	auditCfgMap.Data[AuditPolicyProfileMapKey] = string(auditConfig.Profile)
	return nil
}

func AuditEnabledWorkloadContext(cpContext component.WorkloadContext) bool {
	return AuditEnabled(*cpContext.HCP)
}

func AuditEnabled(hcp hyperv1.HostedControlPlane) bool {
	return hcp.Spec.Configuration.GetAuditPolicyConfig().Profile != configv1.NoneAuditProfileType
}
