package pki

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

func ReconcileOLMPackageServerCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"packageserver",
		fmt.Sprintf("packageserver.%s.svc", secret.Namespace),
		fmt.Sprintf("packageserver.%s.svc.cluster.local", secret.Namespace),
		"packageserver.default.svc",
		"packageserver.default.svc.cluster.local",
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "packageserver", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}

func ReconcileOLMCatalogOperatorServingCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"catalog-operator-metrics",
		fmt.Sprintf("catalog-operator-metrics.%s.svc", secret.Namespace),
		fmt.Sprintf("catalog-operator-metrics.%s.svc.cluster.local", secret.Namespace),
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "catalog-operator-metrics", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}

func ReconcileOLMOperatorServingCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"olm-operator-metrics",
		fmt.Sprintf("olm-operator-metrics.%s.svc", secret.Namespace),
		fmt.Sprintf("olm-operator-metrics.%s.svc.cluster.local", secret.Namespace),
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "olm-operator-metrics", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}
