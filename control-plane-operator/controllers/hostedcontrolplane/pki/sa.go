package pki

import (
	"crypto/rsa"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/certs"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

func ReconcileServiceAccountSigningKeySecret(secret, signingKey *corev1.Secret, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(secret)
	secret.Type = corev1.SecretTypeOpaque
	expectedKeys := []string{ServiceSignerPrivateKey, ServiceSignerPublicKey}
	if !SecretUpToDate(secret, expectedKeys) {
		var key *rsa.PrivateKey
		var err error
		if signingKey != nil {
			signingKeySecretData, hasSigningKeySecretData := signingKey.Data["key"]
			if !hasSigningKeySecretData {
				return fmt.Errorf("signing key secret %s is missing the key key", signingKey.Name)
			}
			key, err = certs.PemToPrivateKey(signingKeySecretData)
			if err != nil {
				return fmt.Errorf("failed to PEM decode private key %s: %w", signingKey.Name, err)
			}
		} else {
			key, err = certs.PrivateKey()
			if err != nil {
				return fmt.Errorf("failed generating a private key: %w", err)
			}
		}
		keyBytes := certs.PrivateKeyToPem(key)
		publicKeyBytes, err := certs.PublicKeyToPem(&key.PublicKey)
		if err != nil {
			return fmt.Errorf("failed to generate public key from private key: %w", err)
		}
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data[ServiceSignerPrivateKey] = keyBytes
		secret.Data[ServiceSignerPublicKey] = publicKeyBytes
	}
	return nil
}
