package vpn

import (
	"bytes"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

const (
	KubeAPIServerConfigKey = "client.conf"
	clientConfigKey        = "client.conf"
)

// TODO: Build this config with file path parameters
const kubeAPIServerClientConfig = `client
verb 3
nobind
dev tun
remote-cert-tls server
remote openvpn-server 1194 tcp
ca secret/ca.crt
cert secret/tls.crt
key secret/tls.key
`

func (p *VPNParams) ReconcileKubeAPIServerClientConfig(config *corev1.ConfigMap) error {
	util.EnsureOwnerRef(config, p.OwnerReference)
	if config.Data == nil {
		config.Data = map[string]string{}
	}
	config.Data[KubeAPIServerConfigKey] = kubeAPIServerClientConfig
	return nil
}

// TODO: Build this config with file path parameters
const baseWorkerClientConfig = `client
verb 3
nobind
dev tun
remote-cert-tls server
ca ca.crt
cert tls.crt
key tls.key
`

func (p *VPNParams) generateClientConfig() (string, error) {
	result := &bytes.Buffer{}
	fmt.Fprintf(result, "%s", baseWorkerClientConfig)
	fmt.Fprintf(result, "remote %s %d tcp\n", p.ExternalAddress, p.ExternalPort)
	return result.String(), nil
}

func (p *VPNParams) reconcileClientConfig(config *corev1.ConfigMap) error {
	if config.Data == nil {
		config.Data = map[string]string{}
	}
	configData, err := p.generateClientConfig()
	if err != nil {
		return err
	}
	config.Data[clientConfigKey] = configData
	return nil
}

func (p *VPNParams) ReconcileWorkerClientConfig(config *corev1.ConfigMap) error {
	util.EnsureOwnerRef(config, p.OwnerReference)
	clientConfig := manifests.VPNClientConfig()
	if err := p.reconcileClientConfig(clientConfig); err != nil {
		return err
	}
	return util.ReconcileWorkerManifest(config, clientConfig)
}
