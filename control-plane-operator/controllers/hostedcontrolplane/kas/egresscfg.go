package kas

import (
	"bytes"
	"fmt"
	"path"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	hcpconfig "github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kasv1beta1 "k8s.io/apiserver/pkg/apis/apiserver/v1beta1"
)

func egressSelectorConfiguration() *kasv1beta1.EgressSelectorConfiguration {
	cpath := func(volume, file string) string {
		return path.Join(volumeMounts.Path(kasContainerMain().Name, volume), file)
	}
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
					ProxyProtocol: kasv1beta1.ProtocolHTTPConnect,
					Transport: &kasv1beta1.Transport{
						TCP: &kasv1beta1.TCPTransport{
							URL: fmt.Sprintf("https://%s:%d", manifests.KonnectivityServerLocalService("").Name, KonnectivityServerLocalPort),
							TLSConfig: &kasv1beta1.TLSConfig{
								CABundle:   cpath(kasVolumeKonnectivityCA().Name, certs.CASignerCertMapKey),
								ClientCert: cpath(kasVolumeKonnectivityClientCert().Name, corev1.TLSCertKey),
								ClientKey:  cpath(kasVolumeKonnectivityClientCert().Name, corev1.TLSPrivateKeyKey),
							},
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

func ReconcileEgressSelectorConfig(config *corev1.ConfigMap, ownerRef hcpconfig.OwnerRef) error {
	ownerRef.ApplyTo(config)
	if config.Data == nil {
		config.Data = map[string]string{}
	}
	configBytes, err := serializeEgressSelectorConfig(egressSelectorConfiguration())
	if err != nil {
		return err
	}
	config.Data[EgressSelectorConfigMapKey] = string(configBytes)
	return nil
}
