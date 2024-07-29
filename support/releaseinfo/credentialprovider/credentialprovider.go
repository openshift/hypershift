package credentialprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

type DockerCredentialProvider struct {
	AWS ECRDockerCredentialProvider
}

type DockerAuth struct {
	AuthBase64 string `json:"auth"`
}

type DockerAuthMap struct {
	Auths map[string]DockerAuth `json:"auths"`
}

func AddCredentialProviderAuthToPullSecret(ctx context.Context, platformType hyperv1.PlatformType, pullSecret *corev1.Secret, openShiftImageRegistryOverrides map[string][]string) ([]byte, error) {
	log := ctrl.LoggerFrom(ctx)
	if len(openShiftImageRegistryOverrides) > 0 {
		log.Info("management cluster has ImageDigestMirrorSet or ImageContentSourcePolicy, checking for cloud credential provider auth and adding to pull secret")

		dockerCredentialProvider := DockerCredentialProvider{
			AWS: NewECRDockerCredentialProvider(),
		}

		newPullSecretBytes, err := addCredentialProviderAuthToPullSecret(ctx, dockerCredentialProvider, platformType, pullSecret.Data[corev1.DockerConfigJsonKey], openShiftImageRegistryOverrides)
		if err != nil {
			return nil, fmt.Errorf("cannot add credential provider auth to pull secret %s/%s: %w", pullSecret.Namespace, pullSecret.Name, err)

		}

		return newPullSecretBytes, nil
	}
	return pullSecret.Data[corev1.DockerConfigJsonKey], nil
}

func addCredentialProviderAuthToPullSecret(ctx context.Context, dockerCredentialProvider DockerCredentialProvider,
	platformType hyperv1.PlatformType, pullSecretBytes []byte, imageRegistryOverrides map[string][]string) ([]byte, error) {
	log := ctrl.LoggerFrom(ctx)

	decoder := json.NewDecoder(bytes.NewReader(pullSecretBytes))
	pullSecret := DockerAuthMap{}

	err := decoder.Decode(&pullSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to decode existing pull secret from JSON: %w", err)
	}

	for _, mirrors := range imageRegistryOverrides {
		for _, mirror := range mirrors {
			switch platformType {
			case hyperv1.AWSPlatform:
				ecrCredentialProvider := dockerCredentialProvider.AWS
				ecrRepo, err := ecrCredentialProvider.ParseECRRepoURL(mirror)
				if err != nil {
					log.Info(fmt.Sprintf("failed to parse ECR repo URL: %v", err))
					continue
					// mirrorCredentialsResponse.Auth.
				}

				credentials, err := ecrCredentialProvider.GetECRCredentials(ctx, ecrRepo)
				if err != nil {
					log.Info(fmt.Sprintf("failed to fetch from ECR repo %v: %v", ecrRepo, err))
					continue
				}

				log.Info(fmt.Sprintf("raw credentials: %v", credentials))
				// Use raw standard encoding (no padding)
				pullSecret.Auths[mirror] = DockerAuth{AuthBase64: credentials}
			}
		}
	}

	// TODO add instance metadata service URL?
	newPullSecretBytes := bytes.NewBuffer(nil)
	encoder := json.NewEncoder(newPullSecretBytes)
	err = encoder.Encode(pullSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to encode existing pull secret to JSON: %w", err)
	}

	return newPullSecretBytes.Bytes(), nil
}
