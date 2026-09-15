package oapi

import (
	"fmt"

	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// adaptCombinedPullSecret bootstraps combined-pull-secret from pull-secret when
// the secret has no data yet. This handles the upgrade-skew case where the old
// HyperShift Operator (pre-combined-pull-secret support) has not created the
// secret. HCCO takes ownership for ongoing updates (merging additional registry
// credentials); this only provides the initial seed.
func adaptCombinedPullSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	if len(secret.Data[corev1.DockerConfigJsonKey]) > 0 {
		return nil
	}
	pullSecret := &corev1.Secret{}
	pullSecretKey := client.ObjectKey{Namespace: cpContext.HCP.Namespace, Name: "pull-secret"}
	if err := cpContext.Client.Get(cpContext, pullSecretKey, pullSecret); err != nil {
		return fmt.Errorf("failed to get pull-secret for combined-pull-secret bootstrap: %w", err)
	}
	secret.Type = corev1.SecretTypeDockerConfigJson
	secret.Data = map[string][]byte{
		corev1.DockerConfigJsonKey: pullSecret.Data[corev1.DockerConfigJsonKey],
	}
	return nil
}
