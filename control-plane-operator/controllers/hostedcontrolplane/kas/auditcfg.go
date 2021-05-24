package kas

import (
	"bytes"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
	oauthv1 "github.com/openshift/api/oauth/v1"
	routev1 "github.com/openshift/api/route/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

func defaultAuditPolicy() *auditv1.Policy {
	return &auditv1.Policy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Policy",
			APIVersion: auditv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "Default",
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
					"/readyz",
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

func writeRequestBodiesAuditPolicy() *auditv1.Policy {
	return &auditv1.Policy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Policy",
			APIVersion: auditv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "WriteRequestBodies",
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
					"/readyz",
				},
				UserGroups: []string{
					"system:authenticated",
					"system:unauthenticated",
				},
			},
			{
				Level: auditv1.LevelMetadata,
				Resources: []auditv1.GroupResources{
					{
						Group: routev1.SchemeGroupVersion.Group,
						Resources: []string{
							"routes",
						},
					},
					{
						Group: corev1.SchemeGroupVersion.Group,
						Resources: []string{
							"secrets",
						},
					},
				},
			},
			{
				Level: auditv1.LevelMetadata,
				Resources: []auditv1.GroupResources{
					{
						Group: oauthv1.SchemeGroupVersion.Group,
						Resources: []string{
							"oauthclients",
						},
					},
				},
			},
			{
				Level: auditv1.LevelRequestResponse,
				Verbs: []string{
					"update",
					"patch",
					"create",
					"delete",
					"deletecollection",
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

func allRequestBodiesAuditPolicy() *auditv1.Policy {
	return &auditv1.Policy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Policy",
			APIVersion: auditv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "AllRequestBodies",
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
					"/readyz",
				},
				UserGroups: []string{
					"system:authenticated",
					"system:unauthenticated",
				},
			},
			{
				Level: auditv1.LevelMetadata,
				Resources: []auditv1.GroupResources{
					{
						Group: routev1.SchemeGroupVersion.Group,
						Resources: []string{
							"routes",
						},
					},
					{
						Group: corev1.SchemeGroupVersion.Group,
						Resources: []string{
							"secrets",
						},
					},
				},
			},
			{
				Level: auditv1.LevelMetadata,
				Resources: []auditv1.GroupResources{
					{
						Group: oauthv1.SchemeGroupVersion.Group,
						Resources: []string{
							"oauthclients",
						},
					},
				},
			},
			{
				Level: auditv1.LevelRequestResponse,
			},
		},
	}

}

const (
	AuditPolicyConfigMapKey = "policy.yaml"
)

var (
	auditScheme     = runtime.NewScheme()
	auditSerializer = json.NewSerializerWithOptions(
		json.DefaultMetaFactory, auditScheme, auditScheme,
		json.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)
	auditPolicies = map[configv1.AuditProfileType]*auditv1.Policy{
		configv1.AuditProfileDefaultType:            defaultAuditPolicy(),
		configv1.WriteRequestBodiesAuditProfileType: writeRequestBodiesAuditPolicy(),
		configv1.AllRequestBodiesAuditProfileType:   allRequestBodiesAuditPolicy(),
	}
)

func init() {
	auditv1.AddToScheme(auditScheme)
}

func serializeAuditPolicy(policy *auditv1.Policy) ([]byte, error) {
	out := &bytes.Buffer{}
	if err := auditSerializer.Encode(policy, out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func ReconcileAuditConfig(auditCfgMap *corev1.ConfigMap, ownerRef config.OwnerRef, auditProfile configv1.AuditProfileType) error {
	ownerRef.ApplyTo(auditCfgMap)
	if auditCfgMap.Data == nil {
		auditCfgMap.Data = map[string]string{}
	}
	if auditProfile == "" {
		auditProfile = configv1.AuditProfileDefaultType
	}
	policy, ok := auditPolicies[auditProfile]
	if !ok {
		return fmt.Errorf("Invalid audit policy profile: %s", auditProfile)
	}
	policyBytes, err := serializeAuditPolicy(policy)
	if err != nil {
		return err
	}
	auditCfgMap.Data[AuditPolicyConfigMapKey] = string(policyBytes)
	return nil
}
