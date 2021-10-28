package pki

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

func ReconcileOLMPackageServerCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"packageserver",
		fmt.Sprintf("packageserver.%s.svc", secret.Namespace),
		fmt.Sprintf("packageserver.%s.svc.cluster.local", secret.Namespace),
		"packageserver.default.svc",
		"packageserver.default.svc.cluster.local",
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "packageserver", []string{"openshift"}, X509SignerUsage, X509UsageClientServerAuth, dnsNames, nil)
}

func ReconcileOLMProfileCollectorCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "olm-pprof", []string{"openshift"}, X509SignerUsage, X509UsageClientAuth)
}

func ReconcileOLMCatalogOperatorServingCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"catalog-operator-metrics",
		fmt.Sprintf("catalog-operator-metrics.%s.svc", secret.Namespace),
		fmt.Sprintf("catalog-operator-metrics.%s.svc.cluster.local", secret.Namespace),
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "catalog-operator-metrics", []string{"openshift"}, X509SignerUsage, X509UsageClientServerAuth, dnsNames, nil)
}

func ReconcileOLMOperatorServingCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"olm-operator-metrics",
		fmt.Sprintf("olm-operator-metrics.%s.svc", secret.Namespace),
		fmt.Sprintf("olm-operator-metrics.%s.svc.cluster.local", secret.Namespace),
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "olm-operator-metrics", []string{"openshift"}, X509SignerUsage, X509UsageClientServerAuth, dnsNames, nil)
}
