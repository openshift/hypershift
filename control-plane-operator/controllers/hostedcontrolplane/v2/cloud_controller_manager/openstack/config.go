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
	CloudConfigKey      = "cloud.conf"
	CloudsSecretKey     = "clouds.yaml"
	CABundleKey         = "ca-bundle.pem"
)

// TODO: is this configMap really needed?
// 'openstack-cloud-config' configMap already has the CA under the same key
func adaptTrustedCA(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
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
func adaptConfig(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	credentialsSecret, err := getCredentialsSecret(cpContext)
	if err != nil {
		return err
	}

	caCertData := GetCACertFromCredentialsSecret(credentialsSecret)
	if caCertData != nil {
		// NOTE(stephenfin): While we (OpenStack) would prefer that this used
		// 'cacert' like everything else, CCCMO expects the CA cert to be found
		// at 'ca-bundle.pem' since it will combine this cert with an optional
		// cert bundle. This is done for more platforms that OpenStack so we
		// don't want to change that. See the below for more information.
		//
		// https://github.com/openshift/cluster-cloud-controller-manager-operator/blob/master/docs/dev/trusted_ca_bundle_sync.md
		cm.Data[CABundleKey] = string(caCertData)
	}

	cm.Data[CloudConfigKey] = getCloudConfig(cpContext.HCP.Spec, credentialsSecret)
	return nil
}

// getCloudConfig returns the cloud config.
func getCloudConfig(hcpSpec hyperv1.HostedControlPlaneSpec, credentialsSecret *corev1.Secret) string {
	caCertData := GetCACertFromCredentialsSecret(credentialsSecret)

	config := string(credentialsSecret.Data[CloudConfigKey])
	config += "[Global]\n"
	config += "use-clouds = true\n"
	config += "clouds-file = " + CloudCredentialsDir + "/" + CloudsSecretKey + "\n"
	config += "cloud = " + hcpSpec.Platform.OpenStack.IdentityRef.CloudName + "\n"
	// This takes priority over the 'cacert' value in 'clouds.yaml' and we therefore
	// unset that when creating the initial secret.
	if caCertData != nil {
		config += "ca-file = " + CADir + "/" + CABundleKey + "\n"
	}
	config += "\n"
	config += "[LoadBalancer]\n"
	config += "max-shared-lb = 1\n"
	config += "manage-security-groups = true\n"
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

func getCredentialsSecret(cpContext component.WorkloadContext) (*corev1.Secret, error) {
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
