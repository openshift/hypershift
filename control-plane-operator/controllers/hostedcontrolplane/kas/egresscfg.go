package kas

import (
	"bytes"
	"fmt"

	"github.com/openshift/hypershift/api"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/konnectivity"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kasv1beta1 "k8s.io/apiserver/pkg/apis/apiserver/v1beta1"
)

func egressSelectorConfiguration() *kasv1beta1.EgressSelectorConfiguration {
	return &kasv1beta1.EgressSelectorConfiguration{
		TypeMeta: metav1.TypeMeta{
			Kind:       "EgressSelectorConfiguration",
			APIVersion: kasv1beta1.SchemeGroupVersion.String(),
		},
		EgressSelections: []kasv1beta1.EgressSelection{
			{
				Name: "controlplane",
				Connection: kasv1beta1.Connection{
					ProxyProtocol: kasv1beta1.ProtocolDirect,
				},
			},
			{
				Name: "etcd",
				Connection: kasv1beta1.Connection{
					ProxyProtocol: kasv1beta1.ProtocolDirect,
				},
			},
			{
				Name: "cluster",
				Connection: kasv1beta1.Connection{
					ProxyProtocol: kasv1beta1.ProtocolGRPC,
					Transport: &kasv1beta1.Transport{
						TCP: &kasv1beta1.TCPTransport{
							URL:       fmt.Sprintf("https://kconnectivity-server-local:%d", konnectivity.KonnectivityServerPort),
							TLSConfig: &kasv1beta1.TLSConfig{},
						},
					},
				},
			},
		},
	}
}

const (
	EgressSelectorConfigMapKey = "config.yaml"
)

func serializeEgressSelectorConfig(config *kasv1beta1.EgressSelectorConfiguration) ([]byte, error) {
	out := &bytes.Buffer{}
	if err := api.YamlSerializer.Encode(config, out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (p *KubeAPIServerParams) ReconcileEgressSelectorConfig(egressCfgMap *corev1.ConfigMap) error {
	if egressCfgMap.Data == nil {
		egressCfgMap.Data = map[string]string{}
	}
	configBytes, err := serializeEgressSelectorConfig(egressSelectorConfiguration())
	if err != nil {
		return err
	}
	egressCfgMap.Data[EgressSelectorConfigMapKey] = string(configBytes)
	return nil
}
