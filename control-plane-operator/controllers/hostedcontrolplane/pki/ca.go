package pki

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/certs"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

func (p *PKIParams) reconcileSelfSignedCA(secret *corev1.Secret, cn, ou string) error {
	util.EnsureOwnerRef(secret, p.OwnerReference)
	secret.Type = corev1.SecretTypeOpaque
	if hasKeys(secret, CASignerKeyMapKey, CASignerKeyMapKey) {
		return nil
	}
	cfg := &certs.CertCfg{
		Subject:   pkix.Name{CommonName: cn, OrganizationalUnit: []string{ou}},
		KeyUsages: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		Validity:  certs.ValidityTenYears,
		IsCA:      true,
	}
	key, crt, err := certs.GenerateSelfSignedCertificate(cfg)
	if err != nil {
		return fmt.Errorf("failed to generate CA (cn=%s,ou=%s): %w", cn, ou, err)
	}
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[CASignerCertMapKey] = certs.CertToPem(crt)
	secret.Data[CASignerKeyMapKey] = certs.PrivateKeyToPem(key)
	return nil
}

func (p *PKIParams) reconcileAggregateCA(configMap *corev1.ConfigMap, sources ...*corev1.Secret) error {
	util.EnsureOwnerRef(configMap, p.OwnerReference)
	combined := &bytes.Buffer{}
	for _, src := range sources {
		ca_bytes := src.Data[CASignerCertMapKey]
		fmt.Fprintf(combined, "%s", string(ca_bytes))
	}
	if configMap.Data == nil {
		configMap.Data = map[string]string{}
	}
	configMap.Data[CASignerCertMapKey] = combined.String()
	return nil
}

func (p *PKIParams) ReconcileRootCA(secret *corev1.Secret) error {
	return p.reconcileSelfSignedCA(secret, "root-ca", "openshift")
}

func (p *PKIParams) ReconcileClusterSignerCA(secret *corev1.Secret) error {
	return p.reconcileSelfSignedCA(secret, "cluster-signer", "openshift")
}

func (p *PKIParams) ReconcileCombinedCA(cm *corev1.ConfigMap, rootCA, signerCA *corev1.Secret) error {
	return p.reconcileAggregateCA(cm, rootCA, signerCA)
}
