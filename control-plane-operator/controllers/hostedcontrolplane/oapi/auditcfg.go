package oapi

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"

	oauthv1 "github.com/openshift/api/oauth/v1"

	"github.com/openshift/hypershift/support/config"
)

const (
	auditPolicyConfigMapKey = "policy.yaml"
)

func ReconcileAuditConfig(cm *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(cm)
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	policy := defaultAuditPolicy()
	policyBytes, err := config.SerializeAuditPolicy(policy)
	if err != nil {
		return err
	}
	cm.Data[auditPolicyConfigMapKey] = string(policyBytes)
	return nil
}

func defaultAuditPolicy() *auditv1.Policy {
	return &auditv1.Policy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Policy",
			APIVersion: auditv1.SchemeGroupVersion.String(),
		},
		OmitStages: []auditv1.Stage{
			auditv1.StageRequestReceived,
		},
		Rules: []auditv1.PolicyRule{
			{
				Level: auditv1.LevelNone,
				Resources: []auditv1.GroupResources{
					{
						Group: corev1.SchemeGroupVersion.Group,
						Resources: []string{
							"events",
						},
					},
				},
			},
			{
				Level: auditv1.LevelNone,
				Resources: []auditv1.GroupResources{
					{
						Group: oauthv1.SchemeGroupVersion.Group,
						Resources: []string{
							"oauthaccesstokens",
							"oauthauthorizetokens",
						},
					},
				},
			},
			{
				Level: auditv1.LevelNone,
				NonResourceURLs: []string{
					"/api*",
					"/version",
					"/healthz",
				},
				UserGroups: []string{
					"system:authenticated",
					"system:unauthenticated",
				},
			},
			{
				Level: auditv1.LevelMetadata,
				OmitStages: []auditv1.Stage{
					auditv1.StageRequestReceived,
				},
			},
		},
	}
}
