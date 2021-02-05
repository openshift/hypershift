package util

import (
	"bytes"
	"text/template"

	"github.com/pkg/errors"
)

func GenerateKubeconfig(serverAddress, commonName, organization string, rootCA, signingCA *CA) (*Kubeconfig, error) {
	cert, err := GenerateCert(commonName, organization, nil, nil, signingCA)
	if err != nil {
		return nil, err
	}
	return &Kubeconfig{
		Cert:          cert,
		ServerAddress: serverAddress,
		RootCA:        rootCA,
	}, nil
}

type Kubeconfig struct {
	RootCA *CA
	*Cert
	ServerAddress string
}

var kubeConfigTemplate = template.Must(template.New("kubeconfig").Parse(`
apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: {{ .CACert }}
    server: {{ .ServerAddress }}
  name: default
contexts:
- context:
    cluster: default
    user: admin
  name: default
current-context: default
kind: Config
preferences: {}
users:
- name: admin
  user:
    client-certificate-data: {{ .ClientCert }}
    client-key-data: {{ .ClientKey }}
`))

func (k *Kubeconfig) Serialize() ([]byte, error) {
	caBytes := CertToPem(k.RootCA.Cert)
	certBytes := CertToPem(k.Cert.Cert)
	keyBytes := PrivateKeyToPem(k.Cert.Key)
	params := map[string]string{
		"ServerAddress": k.ServerAddress,
		"CACert":        Base64(caBytes),
		"ClientCert":    Base64(certBytes),
		"ClientKey":     Base64(keyBytes),
	}
	result := &bytes.Buffer{}
	if err := kubeConfigTemplate.Execute(result, params); err != nil {
		return nil, errors.Wrapf(err, "failed to execute kubeconfig template")
	}
	return result.Bytes(), nil
}
