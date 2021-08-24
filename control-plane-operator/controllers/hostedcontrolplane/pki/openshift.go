package pki

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

func ReconcileOpenShiftAPIServerCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"openshift-apiserver",
		fmt.Sprintf("openshift-apiserver.%s.svc", secret.Namespace),
		fmt.Sprintf("openshift-apiserver.%s.svc.cluster.local", secret.Namespace),
		"openshift-apiserver.default.svc",
		"openshift-apiserver.default.svc.cluster.local",
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "openshift-apiserver", "openshift", X509SignerUsage, X509UsageClientServerAuth, dnsNames, nil)
}

func ReconcileOpenShiftOAuthAPIServerCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"openshift-oauth-apiserver",
		fmt.Sprintf("openshift-oauth-apiserver.%s.svc", secret.Namespace),
		fmt.Sprintf("openshift-oauth-apiserver.%s.svc.cluster.local", secret.Namespace),
		"openshift-oauth-apiserver.default.svc",
		"openshift-oauth-apiserver.default.svc.cluster.local",
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "openshift-oauth-apiserver", "openshift", X509SignerUsage, X509UsageClientServerAuth, dnsNames, nil)
}

func ReconcileOpenShiftControllerManagerCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"openshift-controller-manager",
		fmt.Sprintf("openshift-controller-manager.%s.svc", secret.Namespace),
		fmt.Sprintf("openshift-controller-manager.%s.svc.cluster.local", secret.Namespace),
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "openshift-controller-manager", "openshift", X509SignerUsage, X509UsageClientServerAuth, dnsNames, nil)
}

func ReconcileClusterPolicyControllerCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"cluster-policy-controller",
		fmt.Sprintf("openshift-controller-manager.%s.svc", secret.Namespace),
		fmt.Sprintf("openshift-controller-manager.%s.svc.cluster.local", secret.Namespace),
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "cluster-policy-controller", "openshift", X509SignerUsage, X509UsageClientServerAuth, dnsNames, nil)
}

func ReconcileOLMPackageServerCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"packageserver",
		fmt.Sprintf("packageserver.%s.svc", secret.Namespace),
		fmt.Sprintf("packageserver.%s.svc.cluster.local", secret.Namespace),
		"packageserver.default.svc",
		"packageserver.default.svc.cluster.local",
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "packageserver", "openshift", X509SignerUsage, X509UsageClientServerAuth, dnsNames, nil)
}
