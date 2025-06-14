package globalps

import (
	"encoding/json"
	"fmt"

	"github.com/openshift/hypershift/support/thirdparty/kubernetes/pkg/credentialprovider"
	corev1 "k8s.io/api/core/v1"
)

const (
	NodePullSecretPath = "/var/lib/kubelet/config.json"
)

func ValidateAdditionalPullSecret(pullSecret *corev1.Secret) ([]byte, error) {
	var dockerConfigJSON credentialprovider.DockerConfigJSON

	// Validate that the pull secret contains the dockerConfigJson key
	if _, ok := pullSecret.Data[corev1.DockerConfigJsonKey]; !ok {
		return nil, fmt.Errorf("pull secret data is not a valid docker config json")
	}

	// Validate that the pull secret is a valid Docker config JSON
	pullSecretBytes := pullSecret.Data[corev1.DockerConfigJsonKey]
	if err := json.Unmarshal(pullSecretBytes, &dockerConfigJSON); err != nil {
		return nil, fmt.Errorf("invalid docker config json format: %w", err)
	}

	// Validate that the pull secret contains at least one auth entry
	if len(dockerConfigJSON.Auths) == 0 {
		return nil, fmt.Errorf("docker config json must contain at least one auth entry")
	}

	// TODO (jparrill):
	// 	- Validate MachineConfig patches looking for changes over the original pull secret path.
	// 	- Validate the Kubelet flags does not contain a different path for the pull secret.
	// 	- Validate that the pull secret is not conflicting with the original pull secret.

	return pullSecretBytes, nil
}

func MergePullSecrets(originalPullSecret, additionalPullSecret []byte) ([]byte, error) {
	var (
		originalDockerConfigJSON   credentialprovider.DockerConfigJSON
		additionalDockerConfigJSON credentialprovider.DockerConfigJSON
		globalPullSecretBytes      []byte
		err                        error
	)

	if err = json.Unmarshal(originalPullSecret, &originalDockerConfigJSON); err != nil {
		return nil, fmt.Errorf("invalid original pull secret format: %w", err)
	}

	if err = json.Unmarshal(additionalPullSecret, &additionalDockerConfigJSON); err != nil {
		return nil, fmt.Errorf("invalid additional pull secret format: %w", err)
	}

	// Merge the additional with the original pull secret.
	// if an auth entry already exists, it will be overwritten.
	for k, v := range additionalDockerConfigJSON.Auths {
		originalDockerConfigJSON.Auths[k] = v
	}

	globalPullSecretBytes, err = json.Marshal(originalDockerConfigJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal merged pull secret: %w", err)
	}

	return globalPullSecretBytes, nil
}
