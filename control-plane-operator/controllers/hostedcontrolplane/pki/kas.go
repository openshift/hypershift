package pki

import (
	"fmt"
	"net"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
)

const (
	// Service signer secret keys
	ServiceSignerPrivateKey = "service-account.key"
	ServiceSignerPublicKey  = "service-account.pub"
)

func ReconcileKASServerCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef, externalAPIAddress, serviceCIDR string) error {
	svc := manifests.KubeAPIServerService(secret.Namespace)
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
	if isNumericIP(externalAPIAddress) {
		apiServerIPs = append(apiServerIPs, externalAPIAddress)
	} else {
		dnsNames = append(dnsNames, externalAPIAddress)
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "kubernetes", "kubernetes", X509DefaultUsage, X509UsageServerAuth, dnsNames, apiServerIPs)
}

func ReconcileKASKubeletClientCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "system:kube-apiserver", "kubernetes", X509DefaultUsage, X509UsageClientAuth)
}

func ReconcileKASMachineBootstrapClientCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "system:bootstrapper", "system:bootstrappers", X509DefaultUsage, X509UsageClientAuth)
}

func ReconcileKASAggregatorCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "system:openshift-aggregator", "kubernetes", X509DefaultUsage, X509UsageClientServerAuth)
}

func ReconcileKASAdminClientCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "system:admin", "system:masters", X509DefaultUsage, X509UsageClientServerAuth)
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
