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

func adaptAuditConfig(cpContext component.ControlPlaneContext, auditCfgMap *corev1.ConfigMap) error {
	auditConfig := auditPolicyConfig(cpContext.HCP)
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

func auditPolicyConfig(hcp *hyperv1.HostedControlPlane) configv1.Audit {
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.APIServer != nil {
		return hcp.Spec.Configuration.APIServer.Audit
	} else {
		return configv1.Audit{
			Profile: configv1.DefaultAuditProfileType,
		}
	}
}
