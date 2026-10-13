package util

import (
	"context"
	"fmt"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func EnsurePullSecret(serviceAccount *corev1.ServiceAccount, secretName string) {
	for _, secretRef := range serviceAccount.ImagePullSecrets {
		if secretRef.Name == secretName {
			// secret is already part of image pull secrets
			return
		}
	}
	serviceAccount.ImagePullSecrets = append(serviceAccount.ImagePullSecrets, corev1.LocalObjectReference{
		Name: secretName,
	})
}

func CreateTokenForServiceAccount(ctx context.Context, serviceAccount *corev1.ServiceAccount, client *kubernetes.Clientset) (string, error) {
	serviceAccount, err := client.CoreV1().ServiceAccounts(serviceAccount.Namespace).Get(ctx, serviceAccount.Name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return "", fmt.Errorf("failed to get serviceaccount: %w", err)
		}
		return "", fmt.Errorf("serviceaccount not found: %w", err)
	}

	treq := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			Audiences: []string{"openshift"},
		},
	}

	// Create the service account token
	token, err := client.CoreV1().ServiceAccounts(serviceAccount.Namespace).CreateToken(ctx, serviceAccount.Name, treq, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create token: %w", err)
	}

	return token.Status.Token, nil
}
