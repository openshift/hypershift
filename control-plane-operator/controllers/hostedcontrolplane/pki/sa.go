package pki

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
)

func ReconcileServiceAccountSigningKeySecret(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	if secret != nil && hasKeys(secret, ServiceSignerPrivateKey, ServiceSignerPublicKey) {
		return nil
	}
	ownerRef.ApplyTo(secret)
	secret.Type = corev1.SecretTypeOpaque

	// The SA signer only supports RSA and ECDSA: https://github.com/kubernetes/kubernetes/blob/ab13c85316015cf9f115e29923ba9740bd1564fd/pkg/serviceaccount/jwt.go#L80
	// and AWS seems to refuse JWTs that are not signed with RSA as invalid (`The ID Token provided is not a valid JWT. (You may see this error if you sent an Access Token)`)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("failed generating a private key: %w", err)
	}
	keyBytes, err := certs.PrivateKeyToPem(key)
	if err != nil {
		return fmt.Errorf("failed to serialize private key to PEM: %w", err)
	}
	publicKeyBytes, err := certs.PublicKeyToPem(key.Public())
	if err != nil {
		return fmt.Errorf("failed to generate public key from private key: %w", err)
	}
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[ServiceSignerPrivateKey] = keyBytes
	secret.Data[ServiceSignerPublicKey] = publicKeyBytes
	return nil
}

func ReconcileMetricsSAClientCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "system:serviceaccount:hypershift:prometheus", []string{"kubernetes"}, X509UsageClientAuth)
}
