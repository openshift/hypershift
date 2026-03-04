package metricsproxy

import (
	"fmt"

	"github.com/openshift/hypershift/support/certs"
	component "github.com/openshift/hypershift/support/controlplane-component"

	routev1 "github.com/openshift/api/route/v1"

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
	return certs.ReconcileSelfSignedCA(secret, "metrics-proxy-ca", "openshift", func(o *certs.CAOpts) {
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
			Name:      "metrics-proxy-ca-cert",
		},
	}
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(caCertSecret), caCertSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get metrics-proxy ca-cert secret: %w", err)
	}

	metricsProxyRoute := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cpContext.HCP.Namespace,
			Name:      ComponentName,
		},
	}
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(metricsProxyRoute), metricsProxyRoute); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get metrics-proxy route: %w", err)
	}
	if len(metricsProxyRoute.Status.Ingress) == 0 || len(metricsProxyRoute.Status.Ingress[0].Host) == 0 {
		return nil
	}
	metricsProxyAddress := metricsProxyRoute.Status.Ingress[0].Host

	dnsNames := []string{
		metricsProxyAddress,
		"metrics-proxy",
		fmt.Sprintf("metrics-proxy.%s.svc", cpContext.HCP.Namespace),
		fmt.Sprintf("metrics-proxy.%s.svc.cluster.local", cpContext.HCP.Namespace),
	}

	secret.Type = corev1.SecretTypeTLS
	return certs.ReconcileSignedCert(
		secret,
		caCertSecret,
		"metrics-proxy",
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
