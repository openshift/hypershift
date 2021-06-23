package pki

import (
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
	corev1 "k8s.io/api/core/v1"
)

func (p *PKIParams) ReconcileKonnectivityServerSecret(secret, ca *corev1.Secret) error {
	dnsNames := []string{
		"konnectivity-server-local",
		fmt.Sprintf("konnectivity-server-local.%s.svc", p.Namespace),
		fmt.Sprintf("konnectivity-server-local.%s.svc.cluster.local", p.Namespace),
	}
	return p.reconcileSignedCertWithAddresses(secret, ca, "konnectivity-server-local", "kubernetes", X509DefaultUsage, X509UsageServerAuth, dnsNames, nil)
}

func (p *PKIParams) ReconcileKonnectivityClusterSecret(secret, ca *corev1.Secret) error {
	dnsNames := []string{
		"konnectivity-server",
		fmt.Sprintf("konnectivity-server.%s.svc", p.Namespace),
		fmt.Sprintf("konnectivity-server.%s.svc.cluster.local", p.Namespace),
	}
	ips := []string{}
	if isNumericIP(p.ExternalKconnectivityAddress) {
		ips = append(ips, p.ExternalKconnectivityAddress)
	} else {
		dnsNames = append(dnsNames, p.ExternalKconnectivityAddress)
	}
	return p.reconcileSignedCertWithAddresses(secret, ca, "konnectivity-server", "kubernetes", X509DefaultUsage, X509UsageServerAuth, dnsNames, ips)
}

func (p *PKIParams) ReconcileKonnectivityClientSecret(secret, ca *corev1.Secret) error {
	return p.reconcileSignedCert(secret, ca, "konnectivity-client", "kubernetes", X509DefaultUsage, X509UsageClientAuth)
}

func (p *PKIParams) ReconcileKonnectivityAgentSecret(secret, ca *corev1.Secret) error {
	return p.reconcileSignedCert(secret, ca, "konnectivity-agent", "kubernetes", X509DefaultUsage, X509UsageClientAuth)
}

func (p *PKIParams) ReconcileKonnectivityWorkerAgentSecret(cm *corev1.ConfigMap, ca *corev1.Secret) error {
	util.EnsureOwnerRef(cm, p.OwnerReference)
	secret := manifests.KonnectivityAgentSecret("kube-system")
	if err := p.ReconcileKonnectivityAgentSecret(secret, ca); err != nil {
		return err
	}
	return util.ReconcileWorkerManifest(cm, secret)
}
