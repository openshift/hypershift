package pki

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"sort"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"

	"github.com/openshift/library-go/pkg/crypto"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

const totalClientCADirectCertsAnnotation = "hypershift.openshift.io/total-client-ca-direct-certs"

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
	// nil map read is safe; reconcileAggregateCA initializes cm.Data below
	previousBundle := cm.Data[certs.CASignerCertMapKey]
	previousDirectIdentities, hasPreviousDirectIdentities := "", false
	if cm.Annotations != nil {
		previousDirectIdentities, hasPreviousDirectIdentities = cm.Annotations[totalClientCADirectCertsAnnotation]
	}

	if err := reconcileAggregateCA(cm, ownerRef, signers...); err != nil {
		return err
	}
	combined := &bytes.Buffer{}
	if _, err := combined.WriteString(cm.Data[certs.CASignerCertMapKey]); err != nil {
		return err
	}
	additionalCerts := make([]*x509.Certificate, 0)
	for _, add := range additional {
		caData := add.Data[certs.OCPCASignerCertMapKey]
		if len(caData) == 0 {
			klog.Warningf("additional ConfigMap %s/%s has empty %s key", add.Namespace, add.Name, certs.OCPCASignerCertMapKey)
		}
		if _, err := combined.WriteString(caData); err != nil {
			return err
		}
		additionalCerts = append(additionalCerts, parsePEMCertificates([]byte(caData))...)
	}

	directCerts := parsePEMCertificates([]byte(cm.Data[certs.CASignerCertMapKey]))
	previousDirectCerts := previousDirectCertificates(
		parsePEMCertificates([]byte(previousBundle)),
		directCerts,
		additionalCerts,
		previousDirectIdentities,
		hasPreviousDirectIdentities,
	)

	// Preserve non-expired CAs from the previous bundle only when their
	// provenance identifies them as direct CPO signer certificates. This
	// prevents client certificate breakage when a direct CA rotates while
	// allowing the authoritative additional source to revoke its own CAs.
	allCerts := parsePEMCertificates(combined.Bytes())
	allCerts = append(allCerts, previousDirectCerts...)

	allCerts = crypto.FilterExpiredCerts(allCerts...)
	allCerts = deduplicateCerts(allCerts)

	if len(allCerts) == 0 {
		return fmt.Errorf("refusing to write empty CA bundle: all certificates expired or invalid")
	}

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

	directHistory := append([]*x509.Certificate{}, directCerts...)
	directHistory = append(directHistory, previousDirectCerts...)
	directHistory = crypto.FilterExpiredCerts(directHistory...)
	directHistory = deduplicateCerts(directHistory)
	if cm.Annotations == nil {
		cm.Annotations = map[string]string{}
	}
	if len(directHistory) == 0 {
		delete(cm.Annotations, totalClientCADirectCertsAnnotation)
	} else {
		cm.Annotations[totalClientCADirectCertsAnnotation] = encodeCertificateIdentities(directHistory)
	}
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
			klog.Warningf("skipping corrupted CERTIFICATE PEM block: %v", err)
			continue
		}
		result = append(result, cert)
	}
	return result
}

func previousDirectCertificates(previous, currentDirect, currentAdditional []*x509.Certificate, encodedIdentities string, hasIdentities bool) []*x509.Certificate {
	if !hasIdentities {
		currentDirectSubjects := make(map[string]struct{}, len(currentDirect))
		for _, cert := range currentDirect {
			currentDirectSubjects[string(cert.RawSubject)] = struct{}{}
		}
		currentAdditionalSubjects := make(map[string]struct{}, len(currentAdditional))
		for _, cert := range currentAdditional {
			currentAdditionalSubjects[string(cert.RawSubject)] = struct{}{}
		}

		previousIdentitiesBySubject := make(map[string]map[string]struct{})
		for _, cert := range previous {
			subject := string(cert.RawSubject)
			if _, ok := currentDirectSubjects[subject]; !ok {
				continue
			}
			if previousIdentitiesBySubject[subject] == nil {
				previousIdentitiesBySubject[subject] = make(map[string]struct{})
			}
			previousIdentitiesBySubject[subject][certificateIdentity(cert)] = struct{}{}
		}

		retained := make([]*x509.Certificate, 0, len(previous))
		for _, cert := range previous {
			subject := string(cert.RawSubject)
			if _, ok := currentDirectSubjects[subject]; !ok {
				continue
			}
			if _, ok := currentAdditionalSubjects[subject]; ok {
				continue
			}
			if len(previousIdentitiesBySubject[subject]) != 1 {
				continue
			}
			retained = append(retained, cert)
		}
		return retained
	}

	identities := parseCertificateIdentities(encodedIdentities)
	retained := make([]*x509.Certificate, 0, len(previous))
	for _, cert := range previous {
		if _, ok := identities[certificateIdentity(cert)]; ok {
			retained = append(retained, cert)
		}
	}
	return retained
}

func certificateIdentity(cert *x509.Certificate) string {
	digest := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(digest[:])
}

func parseCertificateIdentities(encoded string) map[string]struct{} {
	identities := make(map[string]struct{})
	for _, identity := range bytes.Split([]byte(encoded), []byte(",")) {
		if len(identity) == 0 {
			continue
		}
		if len(identity) != sha256.Size*2 {
			klog.Warningf("ignoring malformed direct CA provenance identity %q", string(identity))
			continue
		}
		if _, err := hex.DecodeString(string(identity)); err != nil {
			klog.Warningf("ignoring malformed direct CA provenance identity %q: %v", string(identity), err)
			continue
		}
		identities[string(identity)] = struct{}{}
	}
	return identities
}

func encodeCertificateIdentities(certs []*x509.Certificate) string {
	identities := make([]string, 0, len(certs))
	seen := make(map[string]struct{}, len(certs))
	for _, cert := range certs {
		identity := certificateIdentity(cert)
		if _, ok := seen[identity]; ok {
			continue
		}
		seen[identity] = struct{}{}
		identities = append(identities, identity)
	}
	sort.Strings(identities)

	var encoded bytes.Buffer
	for i, identity := range identities {
		if i > 0 {
			encoded.WriteByte(',')
		}
		encoded.WriteString(identity)
	}
	return encoded.String()
}

// deduplicateCerts removes duplicate certificates by comparing raw DER bytes,
// matching the approach used in library-go's cabundle.go.
func deduplicateCerts(in []*x509.Certificate) []*x509.Certificate {
	var out []*x509.Certificate
	for i := range in {
		found := false
		for j := range out {
			if bytes.Equal(in[i].Raw, out[j].Raw) {
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
