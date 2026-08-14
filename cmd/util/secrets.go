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

type CredentialsSecretData struct {
	AWSAccessKeyID     string
	AWSSecretAccessKey string
	AWSSessionToken    string

	BaseDomain string
}

// ExtractOptionsFromSecret
// Returns baseDomain, awsAccessKeyID & awsSecretAccessKey
// If len(baseDomain) > 0 we override the value found in the secret
func ExtractOptionsFromSecret(client client.Client, name string, namespace string, baseDomain string) (*CredentialsSecretData, error) {
	secret, err := GetSecretWithClient(client, name, namespace)
	if err != nil {
		return nil, err
	}

	if len(baseDomain) == 0 {
		fmt.Println("Using baseDomain from the secret-creds", "baseDomain", string(secret.Data["baseDomain"]))
		baseDomain = string(secret.Data["baseDomain"])
	} else {
		fmt.Println("Using baseDomain from the --base-domain flag")
	}
	awsAccessKeyID := string(secret.Data["aws_access_key_id"])
	awsSecretAccessKey := string(secret.Data["aws_secret_access_key"])
	awsSessionToken := string(secret.Data["aws_session_token"])

	if len(baseDomain) == 0 {
		return nil, fmt.Errorf("the baseDomain key is invalid, {namespace: %s, secret: %s}", namespace, name)
	}
	if len(awsAccessKeyID) == 0 {
		return nil, fmt.Errorf("the aws_access_key_id key is invalid, {namespace: %s, secret: %s}", namespace, name)
	}
	if len(awsSecretAccessKey) == 0 {
		return nil, fmt.Errorf("the aws_secret_access_key key is invalid, {namespace: %s, secret: %s}", namespace, name)
	}
	if len(awsSessionToken) == 0 {
		return nil, fmt.Errorf("the aws_session_token key is invalid, {namespace: %s, secret: %s}", namespace, name)
	}

	return &CredentialsSecretData{
		AWSAccessKeyID:     awsAccessKeyID,
		AWSSecretAccessKey: awsSecretAccessKey,
		AWSSessionToken:    awsSessionToken,
		BaseDomain:         baseDomain,
	}, nil
}
