package k8sutil

import (
	"context"
	"fmt"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

type ServiceAccountTokenCreator interface {
	Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.ServiceAccount, error)
	CreateToken(ctx context.Context, name string, tokenRequest *authenticationv1.TokenRequest, opts metav1.CreateOptions) (*authenticationv1.TokenRequest, error)
}

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

func ServiceAccountClient(client kubernetes.Interface, namespace string) corev1client.ServiceAccountInterface {
	return client.CoreV1().ServiceAccounts(namespace)
}

func CreateTokenForServiceAccount(ctx context.Context, serviceAccount *corev1.ServiceAccount, saClient ServiceAccountTokenCreator) (string, error) {
	if serviceAccount == nil {
		return "", fmt.Errorf("serviceaccount is nil")
	}
	serviceAccount, err := saClient.Get(ctx, serviceAccount.Name, metav1.GetOptions{})
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

	token, err := saClient.CreateToken(ctx, serviceAccount.Name, treq, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create token: %w", err)
	}

	return token.Status.Token, nil
}
