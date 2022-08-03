package pki

import (
	"fmt"
	"net"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/clientcmd"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const (
	// Service signer secret keys
	ServiceSignerPrivateKey = "service-account.key"
	ServiceSignerPublicKey  = "service-account.pub"
)

func ReconcileKASServerCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef, externalAPIAddress, internalAPIAddress, serviceCIDR string) error {
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
		// TODO: add private KAS endpoint name
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
	if isNumericIP(internalAPIAddress) {
		apiServerIPs = append(apiServerIPs, internalAPIAddress)
	} else {
		dnsNames = append(dnsNames, internalAPIAddress)
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "kubernetes", []string{"kubernetes"}, X509UsageServerAuth, dnsNames, apiServerIPs)
}

func ReconcileKASKubeletClientCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "system:kube-apiserver", []string{"kubernetes"}, X509UsageClientAuth)
}

func ReconcileKASMachineBootstrapClientCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "system:serviceaccount:openshift-machine-config-operator:node-bootstrapper", []string{"system:serviceaccounts:openshift-machine-config-operator", "system:serviceaccounts"}, X509UsageClientAuth)
}

func ReconcileKASAggregatorCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "system:openshift-aggregator", []string{"kubernetes"}, X509UsageClientServerAuth)
}

func ReconcileKASAdminClientCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "system:admin", []string{"system:masters"}, X509UsageClientServerAuth)
}

func ReconcileServiceAccountKubeconfig(secret, ca *corev1.Secret, hcp *hyperv1.HostedControlPlane, serviceAccountNamespace, serviceAccountName string) error {
	cn := "system:serviceaccount:" + serviceAccountNamespace + ":" + serviceAccountName
	if err := reconcileSignedCert(secret, ca, config.OwnerRef{}, cn, []string{"system:serviceaccounts"}, X509UsageClientServerAuth); err != nil {
		return fmt.Errorf("failed to reconcile serviceaccount client cert: %w", err)
	}
	svcURL := inClusterKASURL(hcp.Namespace, util.APIPortWithDefault(hcp, config.DefaultAPIServerPort))

	return ReconcileKubeConfig(secret, secret, ca, svcURL, "", manifests.KubeconfigScopeLocal, config.OwnerRef{})
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

func ReconcileKubeConfig(secret, cert, ca *corev1.Secret, url string, key string, scope manifests.KubeconfigScope, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(secret)
	caBytes := ca.Data[certs.CASignerCertMapKey]
	crtBytes, keyBytes := cert.Data[corev1.TLSCertKey], cert.Data[corev1.TLSPrivateKeyKey]
	kubeCfgBytes, err := generateKubeConfig(url, crtBytes, keyBytes, caBytes)
	if err != nil {
		return fmt.Errorf("failed to generate kubeconfig: %w", err)
	}
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	if key == "" {
		key = util.KubeconfigKey
	}
	if secret.Labels == nil {
		secret.Labels = map[string]string{}
	}
	secret.Labels[manifests.KubeconfigScopeLabel] = string(scope)
	secret.Data[key] = kubeCfgBytes
	return nil
}

func generateKubeConfig(url string, crtBytes, keyBytes, caBytes []byte) ([]byte, error) {
	kubeCfg := clientcmdapi.Config{
		Kind:       "Config",
		APIVersion: "v1",
	}
	kubeCfg.Clusters = map[string]*clientcmdapi.Cluster{
		"cluster": {
			Server:                   url,
			CertificateAuthorityData: caBytes,
		},
	}
	kubeCfg.AuthInfos = map[string]*clientcmdapi.AuthInfo{
		"admin": {
			ClientCertificateData: crtBytes,
			ClientKeyData:         keyBytes,
		},
	}
	kubeCfg.Contexts = map[string]*clientcmdapi.Context{
		"admin": {
			Cluster:   "cluster",
			AuthInfo:  "admin",
			Namespace: "default",
		},
	}
	kubeCfg.CurrentContext = "admin"
	return clientcmd.Write(kubeCfg)
}

func inClusterKASURL(namespace string, apiServerPort int32) string {
	return fmt.Sprintf("https://%s:%d", manifests.KubeAPIServerServiceName, apiServerPort)
}
