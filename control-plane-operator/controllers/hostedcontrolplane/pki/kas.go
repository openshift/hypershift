package pki

import (
	"fmt"
	"net"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiserver/pkg/authentication/serviceaccount"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	utilsnet "k8s.io/utils/net"
)

const (
	// Service signer secret keys
	ServiceSignerPrivateKey = "service-account.key"
	ServiceSignerPublicKey  = "service-account.pub"
)

func ReconcileKASServerCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef, externalAPIAddress, internalAPIAddress string, serviceCIDRs []string, nodeInternalAPIServerIP string) error {
	svcAddresses := make([]string, 0)

	for _, serviceCIDR := range serviceCIDRs {
		serviceIP, err := util.FirstUsableIP(serviceCIDR)
		if err != nil {
			return fmt.Errorf("cannot get the first usable IP from CIDR %s: %w", serviceIP, err)
		}
		svcAddresses = append(svcAddresses, serviceIP)
	}

	dnsNames := []string{
		"localhost",
		"kubernetes",
		"kubernetes.default",
		"kubernetes.default.svc",
		"kubernetes.default.svc.cluster.local",
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

func ReconcileKASServerPrivateCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	svc := manifests.KubeAPIServerService(secret.Namespace)
	dnsNames := []string{
		svc.Name,
		fmt.Sprintf("%s.%s.svc", svc.Name, svc.Namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", svc.Name, svc.Namespace),
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "kubernetes-private", []string{"kubernetes"}, X509UsageServerAuth, dnsNames, nil)
}

func ReconcileKASKubeletClientCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "system:kube-apiserver", []string{"kubernetes"}, X509UsageClientAuth)
}

func ReconcileKASMachineBootstrapClientCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "system:serviceaccount:openshift-machine-config-operator:node-bootstrapper", []string{"system:serviceaccounts:openshift-machine-config-operator", "system:serviceaccounts"}, X509UsageClientAuth)
}

func ReconcileKASAggregatorCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "system:openshift-aggregator", []string{"kubernetes"}, X509UsageClientAuth)
}

func ReconcileKubeSchedulerClientCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "system:kube-scheduler", []string{"kubernetes"}, X509UsageClientAuth)
}

func ReconcileKubeControllerManagerClientCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "system:kube-controller-manager", []string{"kubernetes"}, X509UsageClientAuth)
}

func ReconcileSystemAdminClientCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "system:admin", []string{"system:masters"}, X509UsageClientAuth)
}

func ReconcileHCCOClientCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, fmt.Sprintf("system:%s", config.HCCOUser), []string{"kubernetes", "system:serviceaccounts:openshift"}, X509UsageClientAuth)
}

func ReconcileServiceAccountKubeconfig(secret, csrSigner *corev1.Secret, ca *corev1.ConfigMap, hcp *hyperv1.HostedControlPlane, serviceAccountNamespace, serviceAccountName string) error {
	cn := serviceaccount.MakeUsername(serviceAccountNamespace, serviceAccountName)
	if err := reconcileSignedCert(secret, csrSigner, config.OwnerRef{}, cn, serviceaccount.MakeGroupNames(serviceAccountNamespace), X509UsageClientAuth); err != nil {
		return fmt.Errorf("failed to reconcile serviceaccount client cert: %w", err)
	}
	svcURL := inClusterKASURL(hcp.Spec.Platform.Type)
	return ReconcileKubeConfig(secret, secret, ca, svcURL, "", manifests.KubeconfigScopeLocal, config.OwnerRef{})
}

func isNumericIP(s string) bool {
	return net.ParseIP(s) != nil
}

func ReconcileKubeConfig(secret, cert *corev1.Secret, ca *corev1.ConfigMap, url string, key string, scope manifests.KubeconfigScope, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(secret)
	caPEM := ca.Data[certs.CASignerCertMapKey]
	crtBytes, keyBytes := cert.Data[corev1.TLSCertKey], cert.Data[corev1.TLSPrivateKeyKey]
	kubeCfgBytes, err := generateKubeConfig(url, crtBytes, keyBytes, []byte(caPEM))
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
			Server:                   AddBracketsIfIPv6(url),
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

func inClusterKASURL(platformType hyperv1.PlatformType) string {
	if platformType == hyperv1.IBMCloudPlatform {
		return fmt.Sprintf("https://%s:%d", manifests.KubeAPIServerServiceName, config.KASSVCIBMCloudPort)
	}
	return fmt.Sprintf("https://%s:%d", manifests.KubeAPIServerServiceName, config.KASSVCPort)
}

// AddBracketsIfIPv6 function is needed to build the serverAPI url for every kubeconfig created.
// The function returns a string in 3 ways.
// - Without brackets if it's an URL or an IPv4
// - With brackets if it's a valid IPv6
func AddBracketsIfIPv6(apiAddress string) string {

	if utilsnet.IsIPv6String(apiAddress) {
		return fmt.Sprintf("[%s]", apiAddress)
	}

	return apiAddress
}
