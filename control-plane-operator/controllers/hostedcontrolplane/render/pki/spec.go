package pki

import (
	"net"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/render/pki/util"
)

type caSpec struct {
	name               string
	commonName         string
	organizationalUnit string
}

type certSpec struct {
	name         string
	ca           string
	commonName   string
	organization string
	hostNames    []string
	ips          []string
}

type kubeconfigSpec struct {
	certSpec
	serverAddress string
}

func generateCAs(caSpecs []caSpec) (map[string]*util.CA, error) {
	result := make(map[string]*util.CA)
	for _, caSpec := range caSpecs {
		log.Infof("Generating CA %s (cn=%s,ou=%s)", caSpec.name, caSpec.commonName, caSpec.organizationalUnit)
		ca, err := util.GenerateCA(caSpec.commonName, caSpec.organizationalUnit)
		if err != nil {
			return nil, err
		}
		result[caSpec.name] = ca
	}
	return result, nil
}

func generateKubeconfigs(kubeconfigSpecs []kubeconfigSpec, cas map[string]*util.CA) (map[string]*util.Kubeconfig, error) {
	result := make(map[string]*util.Kubeconfig)
	for _, spec := range kubeconfigSpecs {
		log.Infof("Generating kubeconfig %s (cn=%s,o=%s)", spec.name, spec.commonName, spec.organization)
		ca := cas[spec.ca]
		if ca == nil {
			return nil, errors.Errorf("CA %s for kubeconfig %s not found", spec.ca, spec.name)
		}
		kubeconfig, err := util.GenerateKubeconfig(spec.serverAddress, spec.commonName, spec.organization, cas["root-ca"], ca)
		if err != nil {
			return nil, err
		}
		result[spec.name] = kubeconfig
	}
	return result, nil
}

func generateCerts(certSpecs []certSpec, cas map[string]*util.CA) (map[string]*util.Cert, error) {
	result := make(map[string]*util.Cert)
	for _, spec := range certSpecs {
		log.Infof("Generating certificate %s (cn=%s,o=%s)", spec.name, spec.commonName, spec.organization)
		ca := cas[spec.ca]
		if ca == nil {
			return nil, errors.Errorf("CA %s for certificate %s not found", spec.ca, spec.name)
		}
		cert, err := util.GenerateCert(spec.commonName, spec.organization, spec.hostNames, spec.ips, ca)
		if err != nil {
			return nil, err
		}
		result[spec.name] = cert
	}
	return result, nil
}

func ca(name, commonName, organizationalUnit string) caSpec {
	return caSpec{
		name:               name,
		commonName:         commonName,
		organizationalUnit: organizationalUnit,
	}
}

func cert(name, ca, commonName, organization string, hostNames, ips []string) certSpec {
	return certSpec{
		name:         name,
		ca:           ca,
		commonName:   commonName,
		organization: organization,
		hostNames:    hostNames,
		ips:          ips,
	}
}

func kubeconfig(name, serverAddress, ca, commonName, organization string) kubeconfigSpec {
	return kubeconfigSpec{
		certSpec: certSpec{
			name:         name,
			ca:           ca,
			commonName:   commonName,
			organization: organization,
		},
		serverAddress: serverAddress,
	}
}

func serializeCerts(certMap map[string]*util.Cert, output map[string][]byte) {
	for k, v := range certMap {
		certBytes, keyBytes := v.Serialize()
		output[k+".crt"] = certBytes
		output[k+".key"] = keyBytes
	}
}

func serializeKubeconfigs(kubeconfigMap map[string]*util.Kubeconfig, output map[string][]byte) error {
	for k, v := range kubeconfigMap {
		kubeconfigBytes, err := v.Serialize()
		if err != nil {
			return err
		}
		output[k+".kubeconfig"] = kubeconfigBytes
	}
	return nil
}

func serializeCAs(caMap map[string]*util.CA, output map[string][]byte) {
	for k, v := range caMap {
		certBytes, keyBytes := v.Serialize()
		output[k+".crt"] = certBytes
		output[k+".key"] = keyBytes
	}
}

func serializeCombinedCA(cas []string, caMap map[string]*util.CA, fileName string, output map[string][]byte) error {
	var caList util.CAList
	for _, c := range cas {
		ca := caMap[c]
		if ca == nil {
			return errors.Errorf("failed to write combined CA. CA not found: %s", c)
		}
		caList = append(caList, ca)
	}
	output[fileName] = caList.Serialize()
	return nil
}

func nextIP(ip net.IP) net.IP {
	nextIP := net.IP(make([]byte, len(ip)))
	copy(nextIP, ip)
	for j := len(nextIP) - 1; j >= 0; j-- {
		nextIP[j]++
		if nextIP[j] > 0 {
			break
		}
	}
	return nextIP
}

func firstIP(network *net.IPNet) net.IP {
	return nextIP(network.IP)
}
