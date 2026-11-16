package capimanager

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/certs"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/k8sutil"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// metricsServerName is the TLS server name that the cluster-api PodMonitor verifies
	// against the kube-rbac-proxy metrics serving certificate.
	metricsServerName = "cluster-api"
)

func adaptWebhookTLSSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	if secret.Annotations == nil {
		secret.Annotations = make(map[string]string)
	}
	secret.Annotations[k8sutil.HostedClusterAnnotation] = cpContext.HCP.Annotations[k8sutil.HostedClusterAnnotation]

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

// adaptMetricsTLSSecret issues the serving certificate for the kube-rbac-proxy sidecar that
// serves the CAPI manager metrics over TLS. The certificate is signed by the hosted control
// plane root CA, which the cluster-api PodMonitor trusts through the root-ca ConfigMap.
//
// The root CA is created by the control-plane-operator, which can start after the HyperShift
// Operator first reconciles the CAPI manager. Until the root CA exists the secret is left
// empty and the certificate is issued on a later reconcile.
func adaptMetricsTLSSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	if secret.Annotations == nil {
		secret.Annotations = make(map[string]string)
	}
	secret.Annotations[k8sutil.HostedClusterAnnotation] = cpContext.HCP.Annotations[k8sutil.HostedClusterAnnotation]

	if cpContext.SkipCertificateSigning {
		return nil
	}

	rootCA := manifests.RootCASecret(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get root CA secret: %w", err)
	}

	return certs.ReconcileSignedCert(
		secret,
		rootCA,
		"cluster-api-metrics",
		[]string{"openshift"},
		[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		corev1.TLSCertKey,
		corev1.TLSPrivateKeyKey,
		"",
		[]string{metricsServerName},
		nil,
	)
}
