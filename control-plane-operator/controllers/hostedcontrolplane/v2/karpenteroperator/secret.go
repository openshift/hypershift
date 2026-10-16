package karpenteroperator

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
)

func adaptCredentialsSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	hcp := cpContext.HCP
	secret.Type = corev1.SecretTypeOpaque

	switch hcp.Spec.AutoNode.Provisioner.Karpenter.Platform {
	case hyperv1.AWSPlatform:
		awsCredentialsTemplate := `[default]
		role_arn = %s
		web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token
		sts_regional_endpoints = regional
	`
		arn := hcp.Spec.AutoNode.Provisioner.Karpenter.AWS.RoleARN
		credentials := fmt.Sprintf(awsCredentialsTemplate, arn)
		secret.Data = map[string][]byte{"credentials": []byte(credentials)}
		return nil
	default:
		return fmt.Errorf("unsupported platform: %s", hcp.Spec.AutoNode.Provisioner.Karpenter.Platform)
	}
}
