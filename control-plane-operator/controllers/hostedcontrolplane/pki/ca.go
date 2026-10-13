package pki

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

func reconcileSelfSignedCA(secret *corev1.Secret, ownerRef config.OwnerRef, cn, ou string) error {
	ownerRef.ApplyTo(secret)
	secret.Type = corev1.SecretTypeOpaque
	return certs.ReconcileSelfSignedCA(secret, cn, ou)
}

func reconcileAggregateCA(configMap *corev1.ConfigMap, ownerRef config.OwnerRef, sources ...*corev1.Secret) error {
	ownerRef.ApplyTo(configMap)
	combined := &bytes.Buffer{}
	for _, src := range sources {
		caBytes := src.Data[certs.CASignerCertMapKey]
		_, err := fmt.Fprintf(combined, "%s", string(caBytes))
		if err != nil {
			return err
		}
	}
	if configMap.Data == nil {
		configMap.Data = map[string]string{}
	}
	configMap.Data[certs.CASignerCertMapKey] = combined.String()
	return nil
}

func ReconcileAggregatorClientSigner(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "aggregator-signer", "openshift")
}

func ReconcileKubeControlPlaneSigner(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "kube-control-plane-signer", "openshift")
}

func ReconcileKASToKubeletSigner(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "kube-apiserver-to-kubelet-signer", "openshift")
}

func ReconcileAdminKubeconfigSigner(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "admin-kubeconfig-signer", "openshift")
}

func ReconcileHCCOSigner(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "hcco-signer", "openshift")
}

func ReconcileKASBootstrapContainerSigner(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "kas-bootstrap-container-signer", "openshift")
}

func ReconcileKubeCSRSigner(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "kube-csr-signer", "openshift")
}

func ReconcileAggregatorClientCA(cm *corev1.ConfigMap, ownerRef config.OwnerRef, signer *corev1.Secret) error {
	return reconcileAggregateCA(cm, ownerRef, signer)
}

func ReconcileTotalClientCA(cm *corev1.ConfigMap, ownerRef config.OwnerRef, additional []*corev1.ConfigMap, signers ...*corev1.Secret) error {
	if err := reconcileAggregateCA(cm, ownerRef, signers...); err != nil {
		return err
	}
	combined := &bytes.Buffer{}
	if _, err := combined.WriteString(cm.Data[certs.CASignerCertMapKey]); err != nil {
		return err
	}
	for _, add := range additional {
		if _, err := combined.WriteString(add.Data[certs.OCPCASignerCertMapKey]); err != nil {
			return err
		}
	}
	cm.Data[certs.CASignerCertMapKey] = combined.String()
	return nil
}

func ReconcileKubeletClientCA(cm *corev1.ConfigMap, ownerRef config.OwnerRef, signers ...*corev1.Secret) error {
	return reconcileAggregateCA(cm, ownerRef, signers...)
}

func ReconcileRootCA(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "root-ca", "openshift")
}

func ReconcileEtcdSignerSecret(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "etcd-signer", "openshift")
}

func ReconcileEtcdSignerConfigMap(cm *corev1.ConfigMap, ownerRef config.OwnerRef, etcdSigner *corev1.Secret) error {
	return reconcileAggregateCA(cm, ownerRef, etcdSigner)
}

func ReconcileEtcdMetricsSignerSecret(secret *corev1.Secret, ownerref config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerref, "etcd-metrics-signer", "openshift")
}

func ReconcileEtcdMetricsSignerConfigMap(cm *corev1.ConfigMap, ownerRef config.OwnerRef, etcdMetricsSigner *corev1.Secret) error {
	return reconcileAggregateCA(cm, ownerRef, etcdMetricsSigner)
}

func ReconcileRootCAConfigMap(cm *corev1.ConfigMap, ownerRef config.OwnerRef, rootCA *corev1.Secret, observedDefaultIngressCert *corev1.ConfigMap, namedCertCAs ...*corev1.Secret) error {
	sources := []*corev1.Secret{rootCA}
	if observedDefaultIngressCert != nil {
		sources = append(sources, &corev1.Secret{
			Data: map[string][]byte{
				certs.CASignerCertMapKey: []byte(observedDefaultIngressCert.Data[certs.CASignerCertMapKey]),
			},
		})
	}
	sources = append(sources, namedCertCAs...)
	return reconcileAggregateCA(cm, ownerRef, sources...)
}

// FilterCACertificates parses a PEM bundle and returns only the certificates
// that are valid CAs (IsCA=true). Malformed or non-CA certificates are
// skipped.
func FilterCACertificates(certPEM []byte) []byte {
	var caCerts []byte
	rest := certPEM
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		if !cert.IsCA {
			continue
		}
		caCerts = append(caCerts, pem.EncodeToMemory(block)...)
	}
	return caCerts
}

// ExtractCACertificatesFromChain parses a PEM-encoded certificate chain and
// returns a PEM bundle containing only the non-leaf CA certificates.
// The first CERTIFICATE block (the leaf/server cert) is always skipped.
// Subsequent certificates that fail to parse or are not CAs are also skipped.
// If the chain contains only a single certificate, nil is returned.
func ExtractCACertificatesFromChain(certPEM []byte) []byte {
	// Skip the first CERTIFICATE PEM block (leaf), then filter the rest.
	rest := certPEM
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil
		}
		if block.Type == "CERTIFICATE" {
			break
		}
	}
	return FilterCACertificates(rest)
}

func ReconcileKonnectivityConfigMap(cm *corev1.ConfigMap, ownerRef config.OwnerRef, signers ...*corev1.Secret) error {
	return reconcileAggregateCA(cm, ownerRef, signers...)
}
