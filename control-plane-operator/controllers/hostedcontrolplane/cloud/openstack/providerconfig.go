package openstack

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
)

const (
	CloudConfigDir      = "/etc/openstack/config"
	CloudCredentialsDir = "/etc/openstack/credentials"
	CredentialsFile     = "clouds.conf"
	CaDir               = "/etc/pki/ca-trust/extracted/pem"
	CaKey               = "ca.pem"
	Provider            = "openstack"
	cloudsSecretKey     = "clouds.yaml"
	caSecretKey         = "cacert"
)

// ReconcileCloudConfig reconciles as expected by Nodes Kubelet.
func ReconcileCloudConfig(secret *corev1.Secret, hcp *hyperv1.HostedControlPlane, credentialsSecret *corev1.Secret, hasCACert bool) error {
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	config := string(credentialsSecret.Data[CredentialsFile]) // TODO(dulek): Missing key handling

	config += `
[Global]
use-clouds=true
clouds-file=` + CloudCredentialsDir + "/" + CredentialsFile + "\n"

	config += "\ncloud=" + hcp.Spec.Platform.OpenStack.IdentityRef.CloudName

	// FIXME(dulek): This is specific to CCM, we might want to have 2 versions.
	// FIXME(dulek): Is it really a good idea to have it here?
	// FIXME(dulek): How do we make it configurable?
	if hasCACert {
		config += "\nca-file =" + CaDir + "/" + CaKey + "\n"
	}

	config += "\n[LoadBalancer]\nmax-shared-lb = 1\nmanage-security-groups = true\n"

	secret.Data[CredentialsFile] = []byte(config)
	return nil
}

// ReconcileTrustedCA reconciles as expected by Nodes Kubelet.
func ReconcileTrustedCA(cm *corev1.ConfigMap, hcp *hyperv1.HostedControlPlane, caCertData []byte) error {
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[CaKey] = string(caCertData) // TODO(dulek): Missing key handling
	return nil
}

// GetCloudConfigFromCredentialsSecret returns the CA cert from the credentials secret.
func GetCACertFromCredentialsSecret(secret *corev1.Secret) []byte {
	caCert, ok := secret.Data[caSecretKey]
	if !ok {
		return nil
	}
	return caCert
}
