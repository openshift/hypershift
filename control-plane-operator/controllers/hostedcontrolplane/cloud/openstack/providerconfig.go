package openstack

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

const (
	CloudConfigDir      = "/etc/openstack/config"
	CloudCredentialsDir = "/etc/openstack/secret"
	CloudConfigKey      = "cloud.conf"
	CADir               = "/etc/pki/ca-trust/extracted/pem"
	CABundleKey         = "ca-bundle.pem"
	Provider            = "openstack"
	CloudsSecretKey     = "clouds.yaml"
	CASecretKey         = "cacert"
)

// ReconcileCloudConfigSecret reconciles the cloud config secret.
// For some controllers (e.g. Manila CSI, CNCC, etc), the cloud config needs to be stored in a secret.
// In the hosted cluster config operator, we create the secrets needed by these controllers.
func ReconcileCloudConfigSecret(platformSpec *hyperv1.OpenStackPlatformSpec, secret *corev1.Secret, credentialsSecret *corev1.Secret, caCertData []byte, machineNetwork []hyperv1.MachineNetworkEntry) error {
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	config := getCloudConfig(platformSpec, credentialsSecret, caCertData, machineNetwork)
	if caCertData != nil {
		secret.Data[CABundleKey] = caCertData
	}
	secret.Data[CloudConfigKey] = []byte(config)

	return nil
}

// ReconcileCloudConfigConfigMap reconciles the cloud config configmap.
// In some cases (e.g. CCM, kube cloud config, etc), the cloud config needs to be stored in a configmap.
func ReconcileCloudConfigConfigMap(platformSpec *hyperv1.OpenStackPlatformSpec, cm *corev1.ConfigMap, credentialsSecret *corev1.Secret, caCertData []byte, machineNetwork []hyperv1.MachineNetworkEntry) error {
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	config := getCloudConfig(platformSpec, credentialsSecret, caCertData, machineNetwork)
	if caCertData != nil {
		cm.Data[CABundleKey] = string(caCertData)
	}
	cm.Data[CloudConfigKey] = config

	return nil
}

// getCloudConfig returns the cloud config.
func getCloudConfig(platformSpec *hyperv1.OpenStackPlatformSpec, credentialsSecret *corev1.Secret, caCertData []byte, machineNetwork []hyperv1.MachineNetworkEntry) string {
	config := string(credentialsSecret.Data[CloudConfigKey])
	config += "[Global]\n"
	config += "use-clouds = true\n"
	config += "clouds-file = " + CloudCredentialsDir + "/" + CloudsSecretKey + "\n"
	config += "cloud = " + platformSpec.IdentityRef.CloudName + "\n"
	// This takes priority over the 'cacert' value in 'clouds.yaml' and we therefore
	// unset that when creating the initial secret.
	if caCertData != nil {
		config += "ca-file = " + CADir + "/" + CABundleKey + "\n"
	}
	config += "\n"
	config += "[LoadBalancer]\n"
	config += "max-shared-lb = 1\n"
	config += "manage-security-groups = true\n"
	if platformSpec.ExternalNetwork != nil {
		externalNetworkID := ptr.Deref(platformSpec.ExternalNetwork.ID, "")
		if externalNetworkID != "" {
			config += "floating-network-id = " + externalNetworkID + "\n"
		}
	}
	config += "\n[Networking]\n"
	config += "address-sort-order = " + util.MachineNetworksToList(machineNetwork) + "\n"

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
