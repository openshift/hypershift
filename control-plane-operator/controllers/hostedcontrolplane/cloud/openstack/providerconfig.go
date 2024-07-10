package openstack

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
)

const (
	CloudConfigDir      = "/etc/openstack/config"
	CloudCredentialsDir = "/etc/openstack/secret"
	CredentialsFile     = "cloud.conf"
	CaDir               = "/etc/pki/ca-trust/extracted/pem"
	CABundleKey         = "ca-bundle.pem"
	Provider            = "openstack"
	CloudsSecretKey     = "clouds.yaml"
	CASecretKey         = "cacert"
)

// ReconcileCloudConfig reconciles the cloud config secret.
// For some controllers (e.g. Manila CSI, CNCC, etc), the cloud config needs to be stored in a secret.
// In the hosted cluster config operator, we create the secrets needed by these controllers.
func ReconcileCloudConfigSecret(secret *corev1.Secret, cloudName string, credentialsSecret *corev1.Secret, caCertData []byte) error {
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	config := getCloudConfig(cloudName, credentialsSecret, caCertData)
	if caCertData != nil {
		secret.Data[CABundleKey] = caCertData
	}
	secret.Data[CredentialsFile] = []byte(config)

	return nil
}

// ReconcileCloudConfigConfigMap reconciles the cloud config configmap.
// In some cases (e.g. CCM, kube cloud config, etc), the cloud config needs to be stored in a configmap.
func ReconcileCloudConfigConfigMap(cm *corev1.ConfigMap, cloudName string, credentialsSecret *corev1.Secret, caCertData []byte) error {
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	config := getCloudConfig(cloudName, credentialsSecret, caCertData)
	if caCertData != nil {
		cm.Data[CABundleKey] = string(caCertData)
	}
	cm.Data[CredentialsFile] = config

	return nil
}

// getCloudConfig returns the cloud config.
func getCloudConfig(cloudName string, credentialsSecret *corev1.Secret, caCertData []byte) string {
	config := string(credentialsSecret.Data[CredentialsFile])
	config += "[Global]\n"
	config += "use-clouds = true\n"
	config += "clouds-file=" + CloudCredentialsDir + "/" + CloudsSecretKey + "\n"
	config += "cloud=" + cloudName + "\n"
	if caCertData != nil {
		config += "ca-file=" + CaDir + "/" + CABundleKey + "\n"
	}
	config += "\n[LoadBalancer]\nmax-shared-lb = 1\nmanage-security-groups = true\n"

	return config
}

// ReconcileTrustedCA reconciles as expected by Nodes Kubelet.
func ReconcileTrustedCA(cm *corev1.ConfigMap, hcp *hyperv1.HostedControlPlane, caCertData []byte) error {
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[CABundleKey] = string(caCertData)
	return nil
}

// GetCloudConfigFromCredentialsSecret returns the CA cert from the credentials secret.
func GetCACertFromCredentialsSecret(secret *corev1.Secret) []byte {
	caCert, ok := secret.Data[CASecretKey]
	if !ok {
		return nil
	}
	return caCert
}
