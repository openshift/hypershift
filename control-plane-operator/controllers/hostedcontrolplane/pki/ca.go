package pki

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"reflect"
	"sort"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/library-go/pkg/crypto"

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
	previousBundle := cm.Data[certs.CASignerCertMapKey]

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

	// Preserve non-expired CAs from the previous bundle that are absent
	// from the new bundle. This prevents client certificate breakage when
	// a CA (e.g. kube-csr-signer) rotates: clients still holding certs
	// signed by the old CA remain trusted until that CA expires.
	allCerts := parsePEMCertificates(combined.Bytes())
	for _, prev := range parsePEMCertificates([]byte(previousBundle)) {
		allCerts = append(allCerts, prev)
	}

	allCerts = crypto.FilterExpiredCerts(allCerts...)
	allCerts = deduplicateCerts(allCerts)

	// Sort by raw bytes for stable output, avoiding spurious ConfigMap
	// updates when cert ordering shifts between reconciliation loops.
	sort.SliceStable(allCerts, func(i, j int) bool {
		return bytes.Compare(allCerts[i].Raw, allCerts[j].Raw) < 0
	})

	caBytes, err := crypto.EncodeCertificates(allCerts...)
	if err != nil {
		return fmt.Errorf("failed to encode CA bundle: %w", err)
	}
	cm.Data[certs.CASignerCertMapKey] = string(caBytes)
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

func ReconcileRootCAConfigMap(cm *corev1.ConfigMap, ownerRef config.OwnerRef, rootCA *corev1.Secret, observedDefaultIngressCert *corev1.ConfigMap) error {
	sources := []*corev1.Secret{rootCA}
	if observedDefaultIngressCert != nil {
		sources = append(sources, &corev1.Secret{
			Data: map[string][]byte{
				certs.CASignerCertMapKey: []byte(observedDefaultIngressCert.Data[certs.CASignerCertMapKey]),
			},
		})
	}
	return reconcileAggregateCA(cm, ownerRef, sources...)
}

func ReconcileKonnectivityConfigMap(cm *corev1.ConfigMap, ownerRef config.OwnerRef, konnectivityCA *corev1.Secret) error {
	return reconcileAggregateCA(cm, ownerRef, konnectivityCA)
}

func parsePEMCertificates(data []byte) []*x509.Certificate {
	var result []*x509.Certificate
	for len(data) > 0 {
		var block *pem.Block
		block, data = pem.Decode(data)
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
		result = append(result, cert)
	}
	return result
}

// deduplicateCerts removes duplicate certificates using reflect.DeepEqual on
// the raw DER bytes, matching the approach used in library-go's cabundle.go.
func deduplicateCerts(in []*x509.Certificate) []*x509.Certificate {
	var out []*x509.Certificate
	for i := range in {
		found := false
		for j := range out {
			if reflect.DeepEqual(in[i].Raw, out[j].Raw) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, in[i])
		}
	}
	return out
}
