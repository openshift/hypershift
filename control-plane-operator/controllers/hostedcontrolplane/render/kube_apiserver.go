package render

import "text/template"

type KubeAPIServerParams struct {
	PodCIDR                 string
	ServiceCIDR             string
	ExternalAPIAddress      string
	APIServerAuditEnabled   bool
	CloudProvider           string
	EtcdClientName          string
	DefaultFeatureGates     []string
	ExtraFeatureGates       []string
	InfraID                 string
	IngressSubdomain        string
	IssuerURL               string
	InternalAPIPort         uint
	NamedCerts              []NamedCert
	PKI                     map[string][]byte
	APIAvailabilityPolicy   KubeAPIServerParamsAvailabilityPolicy
	ClusterID               string
	Images                  map[string]string
	ApiserverLivenessPath   string
	APINodePort             uint
	ExternalOauthPort       uint
	ExternalOauthDNSName    string
	ProviderCredsSecretName string
	AWSZone                 string
	AWSVPCID                string
	AWSRegion               string
	AWSSubnetID             string
}

type KubeAPIServerParamsAvailabilityPolicy string

const (
	KubeAPIServerParamsHighlyAvailable KubeAPIServerParamsAvailabilityPolicy = "HighlyAvailable"
	KubeAPIServerParamsSingleReplica   KubeAPIServerParamsAvailabilityPolicy = "SingleReplica"
)

type kubeAPIServerManifestContext struct {
	*renderContext
	userManifestFiles []string
	userManifests     map[string]string
}

func NewKubeAPIServerManifestContext(params *KubeAPIServerParams) *kubeAPIServerManifestContext {
	ctx := &kubeAPIServerManifestContext{
		renderContext: newRenderContext(params),
		userManifests: make(map[string]string),
	}
	ctx.setFuncs(template.FuncMap{
		"pki":         pkiFunc(params.PKI),
		"include_pki": includePKIFunc(params.PKI),
		"imageFor":    imageFunc(params.Images),
		"include":     includeFileFunc(params, ctx.renderContext),
	})
	ctx.addManifestFiles(
		"kube-apiserver/kube-apiserver-deployment.yaml",
		"kube-apiserver/kube-apiserver-service.yaml",
		"kube-apiserver/kube-apiserver-config-configmap.yaml",
		"kube-apiserver/kube-apiserver-oauth-metadata-configmap.yaml",
		"kube-apiserver/kube-apiserver-vpnclient-config.yaml",
		"kube-apiserver/kube-apiserver-secret.yaml",
		"kube-apiserver/kube-apiserver-configmap.yaml",
		"kube-apiserver/kube-apiserver-vpnclient-secret.yaml",
		"kube-apiserver/kube-apiserver-default-audit-policy.yaml",
		"kube-apiserver/kube-apiserver-localhost-kubeconfig-secret.yaml",
	)
	return ctx
}

func (c *kubeAPIServerManifestContext) Render() (map[string][]byte, error) {
	return c.renderManifests()
}
