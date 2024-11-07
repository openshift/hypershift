package openstack

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	CloudCredentialsDir = "/etc/openstack/secret"
	CredentialsFile     = "cloud.conf"
	CloudsSecretKey     = "clouds.yaml"
	CABundleKey         = "ca-bundle.pem"
)

// TODO: is this configMap really needed?
// 'openstack-cloud-config' configMap already has the CA under the same key
func adaptTrustedCA(cpContext component.ControlPlaneContext, cm *corev1.ConfigMap) error {
	credentialsSecret, err := getCredentialsSecret(cpContext)
	if err != nil {
		return err
	}

	caCertData := GetCACertFromCredentialsSecret(credentialsSecret)
	if caCertData != nil {
		cm.Data[CABundleKey] = string(caCertData)
	}
	return nil
}

// In some cases (e.g. CCM, kube cloud config, etc), the cloud config needs to be stored in a configmap.
func adaptConfig(cpContext component.ControlPlaneContext, cm *corev1.ConfigMap) error {
	credentialsSecret, err := getCredentialsSecret(cpContext)
	if err != nil {
		return err
	}

	caCertData := GetCACertFromCredentialsSecret(credentialsSecret)
	if caCertData != nil {
		cm.Data[CABundleKey] = string(caCertData)
	}

	cm.Data[CredentialsFile] = getCloudConfig(cpContext.HCP.Spec.Platform.OpenStack, credentialsSecret)
	return nil
}

// For some controllers (e.g. Manila CSI, CNCC, etc), the cloud config needs to be stored in a secret.
// In the hosted cluster config operator, we create the secrets needed by these controllers.
func adaptConfigSecret(cpContext component.ControlPlaneContext, secret *corev1.Secret) error {
	credentialsSecret, err := getCredentialsSecret(cpContext)
	if err != nil {
		return err
	}

	caCertData := GetCACertFromCredentialsSecret(credentialsSecret)
	if caCertData != nil {
		secret.Data[CABundleKey] = caCertData
	}

	config := getCloudConfig(cpContext.HCP.Spec.Platform.OpenStack, credentialsSecret)
	secret.Data[CredentialsFile] = []byte(config)
	return nil
}

// getCloudConfig returns the cloud config.
func getCloudConfig(platformSpec *hyperv1.OpenStackPlatformSpec, credentialsSecret *corev1.Secret) string {
	caCertData := GetCACertFromCredentialsSecret(credentialsSecret)

	config := string(credentialsSecret.Data[CredentialsFile])
	config += "[Global]\n"
	config += "use-clouds = true\n"
	config += "clouds-file=" + CloudCredentialsDir + "/" + CloudsSecretKey + "\n"
	config += "cloud=" + platformSpec.IdentityRef.CloudName + "\n"
	// This takes priority over the 'cacert' value in 'clouds.yaml' and we therefore
	// unset then when creating the initial secret.
	if caCertData != nil {
		config += "ca-file=" + CaDir + "/" + CABundleKey + "\n"
	}
	config += "\n[LoadBalancer]\nmax-shared-lb = 1\nmanage-security-groups = true\n"
	if platformSpec.ExternalNetwork != nil {
		externalNetworkID := ptr.Deref(platformSpec.ExternalNetwork.ID, "")
		if externalNetworkID != "" {
			config += "floating-network-id = " + externalNetworkID + "\n"
		}
	}

	return config
}

func getCredentialsSecret(cpContext component.ControlPlaneContext) (*corev1.Secret, error) {
	if cpContext.HCP.Spec.Platform.OpenStack == nil {
		return nil, fmt.Errorf(".spec.platform.openStack is not defined")
	}

	credentialsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cpContext.HCP.Namespace,
			Name:      cpContext.HCP.Spec.Platform.OpenStack.IdentityRef.Name,
		},
	}
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(credentialsSecret), credentialsSecret); err != nil {
		return nil, fmt.Errorf("failed to get OpenStack credentials secret: %w", err)
	}
	return credentialsSecret, nil
}
