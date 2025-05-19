package capimanager

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"

	"github.com/openshift/hypershift/support/certs"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
)

func adaptWebhookTLSSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	if secret.Annotations == nil {
		secret.Annotations = make(map[string]string)
	}
	secret.Annotations[util.HostedClusterAnnotation] = cpContext.HCP.Annotations[util.HostedClusterAnnotation]

	existingPrivateKeyKey := secret.Data[corev1.TLSPrivateKeyKey]
	existingTLSCertKey := secret.Data[corev1.TLSCertKey]
	// Keep existing certificate if present.
	if len(existingPrivateKeyKey) > 0 && len(existingTLSCertKey) > 0 {
		return nil
	}

	if cpContext.SkipCertificateSigning {
		return nil
	}

	// We currently don't expose CAPI webhooks but still they run as part of the manager
	// and it breaks without a cert https://github.com/kubernetes-sigs/cluster-api/pull/4709.
	cn := "capi-webhooks"
	ou := "openshift"
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
	secret.Data[corev1.TLSCertKey] = certs.CertToPem(crt)
	secret.Data[corev1.TLSPrivateKeyKey] = certs.PrivateKeyToPem(key)
	return nil
}
