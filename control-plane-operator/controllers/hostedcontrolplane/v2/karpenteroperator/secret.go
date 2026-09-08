package karpenteroperator

import (
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
)

func adaptCredentialsSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	hcp := cpContext.HCP
	secret.Type = corev1.SecretTypeOpaque

	switch hcp.Spec.AutoNode.Provisioner.Karpenter.Platform {
	case hyperv1.AzurePlatform:
		if hcp.Spec.Platform.Azure == nil {
			return fmt.Errorf("azure platform spec is required for Karpenter credentials")
		}
		clientID := string(hcp.Spec.AutoNode.Provisioner.Karpenter.Azure.ClientID)
		if clientID == "" {
			return fmt.Errorf("AutoNode Karpenter Azure clientID is required")
		}
		secret.Data = map[string][]byte{
			"azure_client_id":            []byte(clientID),
			"azure_tenant_id":            []byte(hcp.Spec.Platform.Azure.TenantID),
			"azure_subscription_id":      []byte(hcp.Spec.Platform.Azure.SubscriptionID),
			"azure_federated_token_file": []byte(path.Join(config.CloudTokenMountPath, "token")),
		}
		return nil
	default:
		awsCredentialsTemplate := `[default]
	role_arn = %s
	web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token
	sts_regional_endpoints = regional
`
		arn := hcp.Spec.AutoNode.Provisioner.Karpenter.AWS.RoleARN
		credentials := fmt.Sprintf(awsCredentialsTemplate, arn)
		secret.Data = map[string][]byte{"credentials": []byte(credentials)}
		return nil
	}
}
