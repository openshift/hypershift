package pki

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

func (p *PKIParams) ReconcileOpenShiftAPIServerCertSecret(secret, ca *corev1.Secret) error {
	dnsNames := []string{
		"openshift-apiserver",
		fmt.Sprintf("openshift-apiserver.%s.svc", p.Namespace),
		fmt.Sprintf("openshift-apiserver.%s.svc.cluster.local", p.Namespace),
		"openshift-apiserver.default.svc",
		"openshift-apiserver.default.svc.cluster.local",
	}
	return p.reconcileSignedCertWithAddresses(secret, ca, "openshift-apiserver", "openshift", X509SignerUsage, X509UsageClientServerAuth, dnsNames, nil)
}

func (p *PKIParams) ReconcileOpenShiftOAuthAPIServerCertSecret(secret, ca *corev1.Secret) error {
	dnsNames := []string{
		"openshift-oauth-apiserver",
		fmt.Sprintf("openshift-oauth-apiserver.%s.svc", p.Namespace),
		fmt.Sprintf("openshift-oauth-apiserver.%s.svc.cluster.local", p.Namespace),
		"openshift-oauth-apiserver.default.svc",
		"openshift-oauth-apiserver.default.svc.cluster.local",
	}
	return p.reconcileSignedCertWithAddresses(secret, ca, "openshift-oauth-apiserver", "openshift", X509SignerUsage, X509UsageClientServerAuth, dnsNames, nil)
}

func (p *PKIParams) ReconcileOpenShiftControllerManagerCertSecret(secret, ca *corev1.Secret) error {
	dnsNames := []string{
		"openshift-controller-manager",
		fmt.Sprintf("openshift-controller-manager.%s.svc", p.Namespace),
		fmt.Sprintf("openshift-controller-manager.%s.svc.cluster.local", p.Namespace),
	}
	return p.reconcileSignedCertWithAddresses(secret, ca, "openshift-controller-manager", "openshift", X509SignerUsage, X509UsageClientServerAuth, dnsNames, nil)
}

func (p *PKIParams) ReconcileOLMPackageServerCertSecret(secret, ca *corev1.Secret) error {
	dnsNames := []string{
		"packageserver",
		fmt.Sprintf("packageserver.%s.svc", p.Namespace),
		fmt.Sprintf("packageserver.%s.svc.cluster.local", p.Namespace),
		"packageserver.default.svc",
		"packageserver.default.svc.cluster.local",
	}
	return p.reconcileSignedCertWithAddresses(secret, ca, "packageserver", "openshift", X509SignerUsage, X509UsageClientServerAuth, dnsNames, nil)
}

func (p *PKIParams) ReconcileKonnectivityServerCertSecret(secret, ca *corev1.Secret) error {
	dnsNames := []string{
		"konnectivity-server",
		fmt.Sprintf("konnectivity-server.%s.svc", p.Namespace),
		fmt.Sprintf("konnectivity-server.%s.svc.cluster.local", p.Namespace),
	}
	return p.reconcileSignedCertWithAddresses(secret, ca, "konnectivity-server", "openshift", X509SignerUsage, X509UsageClientServerAuth, dnsNames, nil)
}

func (p *PKIParams) ReconcileKonnectivityClusterCertSecret(secret, ca *corev1.Secret) error {
	dnsNames := []string{}
	ips := []string{}
	if isNumericIP(p.ExternalKconnectivityAddress) {
		ips = append(ips, p.ExternalKconnectivityAddress)
	} else {
		dnsNames = append(dnsNames, p.ExternalKconnectivityAddress)
	}
	return p.reconcileSignedCertWithAddresses(secret, ca, "konnectivity-server", "openshift", X509SignerUsage, X509UsageClientServerAuth, dnsNames, ips)
}
