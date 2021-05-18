package pki

import (
	"fmt"
	"net"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
)

const (
	// Service signer secret keys
	ServiceSignerPrivateKey = "service-account.key"
	ServiceSignerPublicKey  = "service-account.pub"
)

func (p *PKIParams) ReconcileKASServerCertSecret(secret, ca *corev1.Secret) error {
	svc := manifests.KubeAPIServerService(p.Namespace)
	serviceCIDR := p.Network.Spec.ServiceNetwork[0]
	_, serviceIPNet, err := net.ParseCIDR(serviceCIDR)
	if err != nil {
		return fmt.Errorf("cannot parse service CIDR: %w", err)
	}
	serviceIP := firstIP(serviceIPNet)
	dnsNames := []string{
		"localhost",
		"kubernetes",
		"kubernetes.default.svc",
		"kubernetes.default.svc.cluster.local",
		svc.Name,
		fmt.Sprintf("%s.%s.svc", svc.Name, svc.Namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", svc.Name, svc.Namespace),
	}
	apiServerIPs := []string{
		"127.0.0.1",
		serviceIP.String(),
	}
	if isNumericIP(p.ExternalAPIAddress) {
		apiServerIPs = append(apiServerIPs, p.ExternalAPIAddress)
	} else {
		dnsNames = append(dnsNames, p.ExternalAPIAddress)
	}
	return p.reconcileSignedCertWithAddresses(secret, ca, "kubernetes", "kubernetes", X509DefaultUsage, X509UsageServerAuth, dnsNames, apiServerIPs)
}

func (p *PKIParams) ReconcileKASKubeletClientCertSecret(secret, ca *corev1.Secret) error {
	return p.reconcileSignedCert(secret, ca, "system:kube-apiserver", "kubernetes", X509DefaultUsage, X509UsageClientAuth)
}

func (p *PKIParams) ReconcileKASMachineBootstrapClientCertSecret(secret, ca *corev1.Secret) error {
	return p.reconcileSignedCert(secret, ca, "system:bootstrapper", "system:bootstrappers", X509DefaultUsage, X509UsageClientAuth)
}

func (p *PKIParams) ReconcileKASAggregatorCertSecret(secret, ca *corev1.Secret) error {
	return p.reconcileSignedCert(secret, ca, "system:openshift-aggregator", "kubernetes", X509DefaultUsage, X509UsageClientServerAuth)
}

func (p *PKIParams) ReconcileKASAdminClientCertSecret(secret, ca *corev1.Secret) error {
	return p.reconcileSignedCert(secret, ca, "system:admin", "system:masters", X509DefaultUsage, X509UsageClientServerAuth)
}

func nextIP(ip net.IP) net.IP {
	nextIP := net.IP(make([]byte, len(ip)))
	copy(nextIP, ip)
	for j := len(nextIP) - 1; j >= 0; j-- {
		nextIP[j]++
		if nextIP[j] > 0 {
			break
		}
	}
	return nextIP
}

func firstIP(network *net.IPNet) net.IP {
	return nextIP(network.IP)
}

func isNumericIP(s string) bool {
	return net.ParseIP(s) != nil
}
