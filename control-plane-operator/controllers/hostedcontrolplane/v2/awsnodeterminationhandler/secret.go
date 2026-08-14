package awsnodeterminationhandler

import (
	"fmt"

	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
)

func adaptCredentialsSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	hcp := cpContext.HCP

	// Get the NodePoolManagementARN from the HCP spec.
	// The predicate ensures this is set before the component is reconciled.
	roleARN := hcp.Spec.Platform.AWS.RolesRef.NodePoolManagementARN

	// Create AWS credentials file content using web identity token
	// This follows the same pattern as karpenter operator
	awsCredentialsTemplate := `[default]
role_arn = %s
web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token
sts_regional_endpoints = regional
`
	credentials := fmt.Sprintf(awsCredentialsTemplate, roleARN)

	// Set the credentials in the secret
	secret.Data = map[string][]byte{"credentials": []byte(credentials)}
	secret.Type = corev1.SecretTypeOpaque

	return nil
}
