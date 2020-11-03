package util

import (
	"os"
	"text/template"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
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

func (k *Kubeconfig) WriteTo(fileName string) error {
	if KubeconfigExists(fileName) {
		log.Infof("Skipping kubeconfig %s because it already exists", fileName)
		return nil
	}
	f, err := os.Create(fileName + ".kubeconfig")
	if err != nil {
		return errors.Wrapf(err, "failed to create kubeconfig file %s", fileName+".kubeconfig")
	}
	defer f.Close()
	caBytes := CertToPem(k.RootCA.Cert)
	certBytes := CertToPem(k.Cert.Cert)
	keyBytes := PrivateKeyToPem(k.Cert.Key)
	params := map[string]string{
		"ServerAddress": k.ServerAddress,
		"CACert":        Base64(caBytes),
		"ClientCert":    Base64(certBytes),
		"ClientKey":     Base64(keyBytes),
	}
	if err := kubeConfigTemplate.Execute(f, params); err != nil {
		return errors.Wrapf(err, "failed to execute kubeconfig template for file %s", fileName+".kubeconfig")
	}
	return nil
}
