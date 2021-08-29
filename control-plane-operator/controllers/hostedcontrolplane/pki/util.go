package pki

import (
	"crypto/md5"
	"crypto/rsa"
	"crypto/x509"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/support/certs"
)

const (
	CASignerCertMapKey = "ca.crt"
	CASignerKeyMapKey  = "ca.key"
	CAHashAnnotation   = "hypershiftlite.openshift.io/ca-hash"
)

func AnnotateWithCA(secret, ca *corev1.Secret) {
	if secret.Annotations == nil {
		secret.Annotations = map[string]string{}
	}
	secret.Annotations[CAHashAnnotation] = computeCAHash(ca)
}

func ValidCA(secret *corev1.Secret) bool {
	return hasKeys(secret, CASignerCertMapKey, CASignerKeyMapKey)
}

func SecretUpToDate(secret *corev1.Secret, keys []string) bool {
	return hasKeys(secret, keys...)
}

func SignedSecretUpToDate(secret, ca *corev1.Secret, keys []string) bool {
	return SecretUpToDate(secret, keys) && hasCAHash(secret, ca)
}

func SignCertificate(cfg *certs.CertCfg, ca *corev1.Secret) (crtBytes []byte, keyBytes []byte, caBytes []byte, err error) {
	caCert, caKey, err := decodeCA(ca)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to decode CA secret: %w", err)
	}
	key, crt, err := certs.GenerateSignedCertificate(caKey, caCert, cfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate etcd client secret: %w", err)
	}
	return certs.CertToPem(crt), certs.PrivateKeyToPem(key), certs.CertToPem(caCert), nil
}

func hasCAHash(secret *corev1.Secret, ca *corev1.Secret) bool {
	if secret.Annotations == nil {
		return false
	}
	actualHash, hasHash := secret.Annotations[CAHashAnnotation]
	if !hasHash {
		return false
	}
	desiredHash := computeCAHash(ca)
	return desiredHash == actualHash
}

func computeCAHash(ca *corev1.Secret) string {
	return fmt.Sprintf("%x", md5.Sum(append(ca.Data[CASignerCertMapKey], ca.Data[CASignerKeyMapKey]...)))
}

func decodeCA(ca *corev1.Secret) (*x509.Certificate, *rsa.PrivateKey, error) {
	crt, err := certs.PemToCertificate(ca.Data[CASignerCertMapKey])
	if err != nil {
		return nil, nil, err
	}
	key, err := certs.PemToPrivateKey(ca.Data[CASignerKeyMapKey])
	if err != nil {
		return nil, nil, err
	}
	return crt, key, nil
}

func hasKeys(secret *corev1.Secret, keys ...string) bool {
	for _, key := range keys {
		if _, hasKey := secret.Data[key]; !hasKey {
			return false
		}
	}
	return true
}
