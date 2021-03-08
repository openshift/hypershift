package pki

import (
	"fmt"
	"net"
	"strconv"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/render"
)

func GeneratePKI(params *render.PKIParams) (map[string][]byte, error) {
	log.Info("Generating PKI artifacts")

	cas := []caSpec{
		ca("root-ca", "root-ca", "openshift"),
		ca("cluster-signer", "cluster-signer", "openshift"),
		ca("openvpn-ca", "openvpn-ca", "openshift"),
	}

	externalAPIServerAddress := fmt.Sprintf("https://%s:%d", params.ExternalAPIAddress, params.ExternalAPIPort)
	internalAPIServerAddress := fmt.Sprintf("https://kube-apiserver:%d", params.InternalAPIPort)
	kubeconfigs := []kubeconfigSpec{
		kubeconfig("admin", externalAPIServerAddress, "root-ca", "system:admin", "system:masters"),
		kubeconfig("internal-admin", internalAPIServerAddress, "root-ca", "system:admin", "system:masters"),
		kubeconfig("localhost-admin", "https://localhost:"+strconv.FormatUint(uint64(params.InternalAPIPort), 10), "root-ca", "system:admin", "system:masters"),
		kubeconfig("kubelet-bootstrap", externalAPIServerAddress, "cluster-signer", "system:bootstrapper", "system:bootstrappers"),
	}

	_, serviceIPNet, err := net.ParseCIDR(params.ServiceCIDR)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse service CIDR: %q", params.ServiceCIDR)
	}
	kubeIP := firstIP(serviceIPNet)
	apiServerHostNames := []string{
		"localhost",
		"kubernetes",
		"kubernetes.default.svc",
		"kubernetes.default.svc.cluster.local",
		"kube-apiserver",
		fmt.Sprintf("kube-apiserver.%s.svc", params.Namespace),
		fmt.Sprintf("kube-apiserver.%s.svc.cluster.local", params.Namespace),
	}
	apiServerIPs := []string{
		"127.0.0.1",
		kubeIP.String(),
		params.NodeInternalAPIServerIP,
	}
	if isNumericIP(params.ExternalAPIAddress) {
		apiServerIPs = append(apiServerIPs, params.ExternalAPIAddress)
	} else {
		apiServerHostNames = append(apiServerHostNames, params.ExternalAPIAddress)
	}
	var ingressNumericIPs, ingressHostNames []string
	if isNumericIP(params.ExternalOauthAddress) {
		ingressNumericIPs = append(ingressNumericIPs, params.ExternalOauthAddress)
	} else {
		ingressHostNames = append(ingressHostNames, params.ExternalOauthAddress)
	}
	ingressHostNames = append(ingressHostNames, fmt.Sprintf("*.%s", params.IngressSubdomain))

	certs := []certSpec{
		// kube-apiserver
		cert("kube-apiserver-server", "root-ca", "kubernetes", "kubernetes", apiServerHostNames, apiServerIPs),
		cert("kube-apiserver-kubelet", "root-ca", "system:kube-apiserver", "kubernetes", nil, nil),
		cert("kube-apiserver-aggregator-proxy-client", "root-ca", "system:openshift-aggregator", "kubernetes", nil, nil),

		// etcd
		cert("etcd-client", "root-ca", "etcd-client", "kubernetes", nil, nil),
		cert("etcd-server", "root-ca", "etcd-server", "kubernetes",
			[]string{
				fmt.Sprintf("*.etcd.%s.svc", params.Namespace),
				fmt.Sprintf("etcd-client.%s.svc", params.Namespace),
				"etcd",
				"etcd-client",
				"localhost",
			}, nil),
		cert("etcd-peer", "root-ca", "etcd-peer", "kubernetes",
			[]string{
				fmt.Sprintf("*.etcd.%s.svc", params.Namespace),
				fmt.Sprintf("*.etcd.%s.svc.cluster.local", params.Namespace),
			}, nil),

		// openshift-apiserver
		cert("openshift-apiserver-server", "root-ca", "openshift-apiserver", "openshift",
			[]string{
				"openshift-apiserver",
				fmt.Sprintf("openshift-apiserver.%s.svc", params.Namespace),
				fmt.Sprintf("openshift-apiserver.%s.svc.cluster.local", params.Namespace),
				"openshift-apiserver.default.svc",
				"openshift-apiserver.default.svc.cluster.local",
			}, nil),

		// oauth-apiserver
		cert("oauth-apiserver-server", "root-ca", "openshift-oauth-apiserver", "openshift",
			[]string{
				"openshift-oauth-apiserver",
				fmt.Sprintf("openshift-oauth-apiserver.%s.svc", params.Namespace),
				fmt.Sprintf("openshift-oauth-apiserver.%s.svc.cluster.local", params.Namespace),
				"openshift-oauth-apiserver.default.svc",
				"openshift-oauth-apiserver.default.svc.cluster.local",
			}, nil),

		// openshift-controller-manager
		cert("openshift-controller-manager-server", "root-ca", "openshift-controller-manager", "openshift",
			[]string{
				"openshift-controller-manager",
				fmt.Sprintf("openshift-controller-manager.%s.svc", params.Namespace),
				fmt.Sprintf("openshift-controller-manager.%s.svc.cluster.local", params.Namespace),
			}, nil),

		cert("machine-config-server", "root-ca", "machine-config-server", "openshift",
			[]string{
				"machine-config-server",
				fmt.Sprintf("machine-config-server.%s.svc", params.Namespace),
				fmt.Sprintf("machine-config-server.%s.svc.cluster.local", params.Namespace),
				params.MachineConfigServerAddress,
			}, nil),

		// openvpn
		cert("openvpn-server", "openvpn-ca", "server", "kubernetes",
			[]string{
				"openvpn-server",
				fmt.Sprintf("openvpn-server.%s.svc", params.Namespace),
				params.ExternalOpenVPNAddress,
			}, nil),
		// oauth server
		cert("ingress-openshift", "root-ca", "openshift-ingress", "openshift", ingressHostNames, ingressNumericIPs),
		cert("openvpn-kube-apiserver-client", "openvpn-ca", "kube-apiserver", "kubernetes", nil, nil),
		cert("openvpn-router-proxy-client", "openvpn-ca", "router-proxy", "kubernetes", nil, nil),
		cert("openvpn-worker-client", "openvpn-ca", "worker", "kubernetes", nil, nil),
	}
	caMap, err := generateCAs(cas)
	if err != nil {
		return nil, err
	}
	kubeconfigMap, err := generateKubeconfigs(kubeconfigs, caMap)
	if err != nil {
		return nil, err
	}
	certMap, err := generateCerts(certs, caMap)
	if err != nil {
		return nil, err
	}

	result := map[string][]byte{}

	serializeCAs(caMap, result)
	if err := serializeKubeconfigs(kubeconfigMap, result); err != nil {
		return nil, err
	}
	serializeCerts(certMap, result)

	// Miscellaneous PKI artifacts
	if err := serializeCombinedCA([]string{"root-ca", "cluster-signer"}, caMap, "combined-ca.crt", result); err != nil {
		return nil, err
	}
	if err := serializeRSAKey("service-account", result); err != nil {
		return nil, err
	}
	return result, nil
}

func isNumericIP(s string) bool {
	return net.ParseIP(s) != nil
}
