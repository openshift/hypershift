package karpenteroperator

import (
	"fmt"

	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
)

func adaptCredentialsSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	hcp := cpContext.HCP

	awsCredentialsTemplate := `[default]
	role_arn = %s
	web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token
	sts_regional_endpoints = regional
`
	arn := hcp.Spec.AutoNode.Provisioner.Karpenter.AWS.RoleARN

	credentials := fmt.Sprintf(awsCredentialsTemplate, arn)
	secret.Data = map[string][]byte{"credentials": []byte(credentials)}
	secret.Type = corev1.SecretTypeOpaque
	return nil
}
