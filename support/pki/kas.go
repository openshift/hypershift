package pki

import (
	"crypto/x509"
	"fmt"
	"net"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/util"
)

func GetKASServerCertificatesSANs(externalAPIAddress, internalAPIAddress string, serviceCIDRs []string, nodeInternalAPIServerIP string, namespace string) ([]string, []string, error) {
	svcAddresses := make([]string, 0)
	svc := manifests.KubeAPIServerService(namespace)

	for _, serviceCIDR := range serviceCIDRs {
		serviceIP, err := util.FirstUsableIP(serviceCIDR)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot get the first usable IP from CIDR %s: %w", serviceIP, err)
		}
		svcAddresses = append(svcAddresses, serviceIP)
	}

	dnsNames := []string{
		"localhost",
		"kubernetes",
		"kubernetes.default",
		"kubernetes.default.svc",
		"kubernetes.default.svc.cluster.local",
		svc.Name,
		// This is needed to configure Openshift Auth Provider that talks to openshift.default.svc
		"openshift",
		"openshift.default",
		"openshift.default.svc",
		"openshift.default.svc.cluster.local",
	}
	apiServerIPs := []string{
		"127.0.0.1",
		"0:0:0:0:0:0:0:1",
	}
	apiServerIPs = append(apiServerIPs, svcAddresses...)
	apiServerIPs = append(apiServerIPs, nodeInternalAPIServerIP)

	if IsNumericIP(externalAPIAddress) {
		apiServerIPs = append(apiServerIPs, externalAPIAddress)
	} else {
		dnsNames = append(dnsNames, externalAPIAddress)
	}
	if IsNumericIP(internalAPIAddress) {
		apiServerIPs = append(apiServerIPs, internalAPIAddress)
	} else {
		dnsNames = append(dnsNames, internalAPIAddress)
	}

	return dnsNames, apiServerIPs, nil
}

func IsNumericIP(s string) bool {
	return net.ParseIP(s) != nil
}

// GetSANsFromCertificate returns the SANs from a certificate as separate DNS names and IP addresses
func GetSANsFromCertificate(cert []byte) ([]string, []string, error) {
	parsedCert, err := x509.ParseCertificate(cert)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	dnsNames := make([]string, len(parsedCert.DNSNames))
	copy(dnsNames, parsedCert.DNSNames)

	ipAddresses := make([]string, 0, len(parsedCert.IPAddresses))
	for _, ip := range parsedCert.IPAddresses {
		ipAddresses = append(ipAddresses, ip.String())
	}

	return dnsNames, ipAddresses, nil
}
