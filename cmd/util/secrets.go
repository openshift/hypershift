package util

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetSecret(name string, namespace string) (*corev1.Secret, error) {
	return GetSecretWithClient(nil, name, namespace)
}

func GetSecretWithClient(client client.Client, name string, namespace string) (*corev1.Secret, error) {
	var err error
	if client == nil {
		client, err = GetClient()
		if err != nil {
			return nil, err
		}
	}

	secret := &corev1.Secret{}
	err = client.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace}, secret)
	return secret, err
}

// ExtractOptionsFromSecret
// Returns baseDomain, awsAccessKeyID & awsSecretAccessKey
// If len(baseDomain) > 0 we override the value found in the secret
func ExtractOptionsFromSecret(client client.Client, name string, namespace string, baseDomain string) (string, string, string, error) {
	secret, err := GetSecretWithClient(client, name, namespace)
	if err != nil {
		return baseDomain, "", "", err
	}

	if len(baseDomain) == 0 {
		fmt.Println("Using baseDomain from the secret-creds", "baseDomain", string(secret.Data["baseDomain"]))
		baseDomain = string(secret.Data["baseDomain"])
	} else {
		fmt.Println("Using baseDomain from the --base-domain flag")
	}
	awsAccessKeyID := string(secret.Data["aws_access_key_id"])
	awsSecretAccessKey := string(secret.Data["aws_secret_access_key"])

	if len(baseDomain) == 0 {
		return "", "", "", fmt.Errorf("the baseDomain key is invalid, {namespace: %s, secret: %s}", namespace, name)
	}
	if len(awsAccessKeyID) == 0 {
		return "", "", "", fmt.Errorf("the aws_access_key_id key is invalid, {namespace: %s, secret: %s}", namespace, name)
	}
	if len(awsSecretAccessKey) == 0 {
		return "", "", "", fmt.Errorf("the aws_secret_access_key key is invalid, {namespace: %s, secret: %s}", namespace, name)
	}
	return baseDomain, awsAccessKeyID, awsSecretAccessKey, nil
}

func GetDockerConfigJSON(name string, namespace string) ([]byte, error) {
	secret, err := GetSecret(name, namespace)
	if err != nil {
		return nil, err
	}
	dockerConfigJSON := secret.Data[".dockerconfigjson"]
	if len(dockerConfigJSON) == 0 {
		return nil, fmt.Errorf("the .dockerconfigjson key is invalid, {namespace: %s, secret: %s}", namespace, name)
	}
	return dockerConfigJSON, nil
}

func GetPullSecret(name string, namespace string) ([]byte, error) {
	secret, err := GetSecret(name, namespace)
	if err != nil {
		return nil, err
	}
	pullSecret := secret.Data["pullSecret"]
	if len(pullSecret) == 0 {
		return nil, fmt.Errorf("the pull secret is invalid, {namespace: %s, secret: %s}", namespace, name)
	}
	return []byte(pullSecret), nil
}
