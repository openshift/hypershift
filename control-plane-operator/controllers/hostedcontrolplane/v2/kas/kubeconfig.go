package kas

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/infra"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	KubeconfigKey = util.KubeconfigKey
)

func adaptServiceKubeconfigSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	svcURL := InClusterKASURL(cpContext.HCP.Spec.Platform.Type)
	kubeconfig, err := GenerateKubeConfig(cpContext, manifests.SystemAdminClientCertSecret(cpContext.HCP.Namespace), svcURL)
	if err != nil {
		return fmt.Errorf("failed to generate kubeconfig: %w", err)
	}

	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[KubeconfigKey] = kubeconfig
	return nil
}

func adaptCAPIKubeconfigSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	clusterName := cpContext.HCP.Spec.InfraID
	// The client used by CAPI machine controller expects the kubeconfig to follow this naming convention
	// https://github.com/kubernetes-sigs/cluster-api/blob/5c85a0a01ee44ecf7c8a3c3fdc867a88af87d73c/util/secret/secret.go#L29-L33
	secret.Name = fmt.Sprintf("%s-kubeconfig", clusterName)

	// The client used by CAPI machine controller expects the kubeconfig to have this key
	// https://github.com/kubernetes-sigs/cluster-api/blob/5c85a0a01ee44ecf7c8a3c3fdc867a88af87d73c/util/secret/secret.go#L29-L33
	// and to be labeled with cluster.x-k8s.io/cluster-name=<clusterName> so the secret can be cached by the client.
	// https://github.com/kubernetes-sigs/cluster-api/blob/8ba3f47b053da8bbf63cf407c930a2ee10bfd754/main.go#L304
	if secret.Labels == nil {
		secret.Labels = make(map[string]string)
	}
	secret.Labels[capiv1.ClusterNameLabel] = clusterName

	svcURL := InClusterKASURL(cpContext.HCP.Spec.Platform.Type)
	kubeconfig, err := GenerateKubeConfig(cpContext, manifests.SystemAdminClientCertSecret(cpContext.HCP.Namespace), svcURL)
	if err != nil {
		return fmt.Errorf("failed to generate kubeconfig: %w", err)
	}

	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data["value"] = kubeconfig
	return nil
}

func adaptHCCOKubeconfigSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	svcURL := InClusterKASURL(cpContext.HCP.Spec.Platform.Type)
	kubeconfig, err := GenerateKubeConfig(cpContext, manifests.HCCOClientCertSecret(cpContext.HCP.Namespace), svcURL)
	if err != nil {
		return fmt.Errorf("failed to generate kubeconfig: %w", err)
	}

	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[KubeconfigKey] = kubeconfig
	return nil
}

func adaptLocalhostKubeconfigSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	apiServerPort := util.KASPodPort(cpContext.HCP)
	localhostURL := fmt.Sprintf("https://localhost:%d", apiServerPort)
	kubeconfig, err := GenerateKubeConfig(cpContext, manifests.SystemAdminClientCertSecret(cpContext.HCP.Namespace), localhostURL)
	if err != nil {
		return fmt.Errorf("failed to generate kubeconfig: %w", err)
	}

	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[KubeconfigKey] = kubeconfig
	return nil
}

func adapExternalAdminKubeconfigSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	if cpContext.HCP.Spec.KubeConfig != nil {
		secret.Name = cpContext.HCP.Spec.KubeConfig.Name
	}

	url := externalURL(cpContext.InfraStatus)

	if !util.IsPublicHCP(cpContext.HCP) && !util.IsRouteKAS(cpContext.HCP) {
		url = internalURL(cpContext.InfraStatus, cpContext.HCP.Name)
	}
	kubeconfig, err := GenerateKubeConfig(cpContext, manifests.SystemAdminClientCertSecret(cpContext.HCP.Namespace), url)
	if err != nil {
		return fmt.Errorf("failed to generate kubeconfig: %w", err)
	}

	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[externalKubeconfigKey(cpContext.HCP)] = kubeconfig
	return nil
}

func adaptCustomAdminKubeconfigSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	hcp := cpContext.HCP
	apiServerPort := util.KASPodPort(hcp)
	url := customExternalURL(hcp.Spec.KubeAPIServerDNSName, apiServerPort)
	kubeconfig, err := GenerateKubeConfig(cpContext, manifests.SystemAdminClientCertSecret(hcp.Namespace), url)
	if err != nil {
		return fmt.Errorf("failed to generate kubeconfig: %w", err)
	}

	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[externalKubeconfigKey(hcp)] = kubeconfig

	return nil

}

func adaptBootstrapKubeconfigSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {

	url := externalURL(cpContext.InfraStatus)
	if util.IsPrivateHCP(cpContext.HCP) {
		url = internalURL(cpContext.InfraStatus, cpContext.HCP.Name)
	}
	kubeconfig, err := GenerateKubeConfig(cpContext, manifests.KASMachineBootstrapClientCertSecret(cpContext.HCP.Namespace), url)
	if err != nil {
		return fmt.Errorf("failed to generate kubeconfig: %w", err)
	}

	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[KubeconfigKey] = kubeconfig
	return nil
}

func adaptAWSPodIdentityWebhookKubeconfigSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	csrSigner := manifests.CSRSignerCASecret(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(csrSigner), csrSigner); err != nil {
		return fmt.Errorf("failed to get cluster-signer-ca secret: %v", err)
	}
	rootCA := manifests.RootCASecret(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return fmt.Errorf("failed to get root ca cert secret: %w", err)
	}
	rootCACM := &corev1.ConfigMap{
		Data: map[string]string{
			certs.CASignerCertMapKey: string(rootCA.Data[certs.CASignerCertMapKey]),
		},
	}

	if !cpContext.SkipCertificateSigning {
		return pki.ReconcileServiceAccountKubeconfig(secret, csrSigner, rootCACM, cpContext.HCP, "openshift-authentication", "aws-pod-identity-webhook")
	}
	return nil
}

func GenerateKubeConfig(cpContext component.WorkloadContext, cert *corev1.Secret, url string) ([]byte, error) {
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(cert), cert); err != nil {
		return nil, fmt.Errorf("failed to get cert secret %s: %w", cert.Name, err)
	}
	rootCA := manifests.RootCASecret(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return nil, fmt.Errorf("failed to get root ca cert secret: %w", err)
	}

	caPEM := rootCA.Data[certs.CASignerCertMapKey]
	crtBytes, keyBytes := cert.Data[corev1.TLSCertKey], cert.Data[corev1.TLSPrivateKeyKey]

	kubeCfg := clientcmdapi.Config{
		Kind:       "Config",
		APIVersion: "v1",
		Clusters: map[string]*clientcmdapi.Cluster{
			"cluster": {
				Server:                   pki.AddBracketsIfIPv6(url),
				CertificateAuthorityData: []byte(caPEM),
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"admin": {
				ClientCertificateData: crtBytes,
				ClientKeyData:         keyBytes,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"admin": {
				Cluster:   "cluster",
				AuthInfo:  "admin",
				Namespace: "default",
			},
		},
		CurrentContext: "admin",
	}

	return clientcmd.Write(kubeCfg)
}

func InClusterKASURL(platformType hyperv1.PlatformType) string {
	if platformType == hyperv1.IBMCloudPlatform {
		return fmt.Sprintf("https://%s:%d", manifests.KubeAPIServerServiceName, config.KASSVCIBMCloudPort)
	}
	return fmt.Sprintf("https://%s:%d", manifests.KubeAPIServerServiceName, config.KASSVCPort)
}

func customExternalURL(address string, port int32) string {
	return fmt.Sprintf("https://%s:%d", pki.AddBracketsIfIPv6(address), port)
}

func externalURL(infraStatus infra.InfrastructureStatus) string {
	return fmt.Sprintf("https://%s:%d", pki.AddBracketsIfIPv6(infraStatus.APIHost), infraStatus.APIPort)
}

func internalURL(infraStatus infra.InfrastructureStatus, hcpName string) string {
	internalAddress := fmt.Sprintf("api.%s.hypershift.local", hcpName)
	return fmt.Sprintf("https://%s:%d", internalAddress, infraStatus.APIPort)
}

func externalKubeconfigKey(hcp *hyperv1.HostedControlPlane) string {
	if hcp.Spec.KubeConfig == nil {
		return util.KubeconfigKey
	}
	return hcp.Spec.KubeConfig.Key
}
