package openstack

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

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

	cm.Data[CredentialsFile] = getCloudConfig(cpContext.HCP.Spec, credentialsSecret)
	return nil
}

// getCloudConfig returns the cloud config.
func getCloudConfig(hcpSpec hyperv1.HostedControlPlaneSpec, credentialsSecret *corev1.Secret) string {
	caCertData := GetCACertFromCredentialsSecret(credentialsSecret)

	config := string(credentialsSecret.Data[CredentialsFile])
	config += "[Global]\n"
	config += "use-clouds = true\n"
	config += "clouds-file=" + CloudCredentialsDir + "/" + CloudsSecretKey + "\n"
	config += "cloud=" + hcpSpec.Platform.OpenStack.IdentityRef.CloudName + "\n"
	if caCertData != nil {
		config += "ca-file=" + CaDir + "/" + CABundleKey + "\n"
	}
	config += "\n[LoadBalancer]\nmax-shared-lb = 1\nmanage-security-groups = true\n"
	if hcpSpec.Platform.OpenStack.ExternalNetwork != nil {
		externalNetworkID := ptr.Deref(hcpSpec.Platform.OpenStack.ExternalNetwork.ID, "")
		if externalNetworkID != "" {
			config += "floating-network-id = " + externalNetworkID + "\n"
		}
	}
	config += "\n[Networking]\n"
	config += "address-sort-order = " + util.MachineNetworksToList(hcpSpec.Networking.MachineNetwork) + "\n"

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
