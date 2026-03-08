package endpointresolver

import (
	"fmt"

	"github.com/openshift/hypershift/support/certs"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func adaptCACertSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	if cpContext.SkipCertificateSigning {
		return nil
	}

	secret.Type = corev1.SecretTypeTLS
	return certs.ReconcileSelfSignedCA(secret, "endpoint-resolver-ca", "openshift", func(o *certs.CAOpts) {
		o.CASignerCertMapKey = corev1.TLSCertKey
		o.CASignerKeyMapKey = corev1.TLSPrivateKeyKey
	})
}

func adaptServingCertSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	if cpContext.SkipCertificateSigning {
		return nil
	}

	caCertSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cpContext.HCP.Namespace,
			Name:      "endpoint-resolver-ca",
		},
	}
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(caCertSecret), caCertSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get endpoint-resolver ca secret: %w", err)
	}

	dnsNames := []string{
		"endpoint-resolver",
		fmt.Sprintf("endpoint-resolver.%s.svc", cpContext.HCP.Namespace),
		fmt.Sprintf("endpoint-resolver.%s.svc.cluster.local", cpContext.HCP.Namespace),
	}

	secret.Type = corev1.SecretTypeTLS
	return certs.ReconcileSignedCert(
		secret,
		caCertSecret,
		"endpoint-resolver",
		[]string{"openshift"},
		nil,
		corev1.TLSCertKey,
		corev1.TLSPrivateKeyKey,
		"",
		dnsNames,
		nil,
		func(o *certs.CAOpts) {
			o.CASignerCertMapKey = corev1.TLSCertKey
			o.CASignerKeyMapKey = corev1.TLSPrivateKeyKey
		},
	)
}
