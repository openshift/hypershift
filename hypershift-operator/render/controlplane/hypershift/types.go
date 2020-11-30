package hypershift

import "github.com/google/uuid"

// NewClusterParams returns a new default cluster params struct
func NewClusterParams() *ClusterParams {
	p := &ClusterParams{}
	p.DefaultFeatureGates = []string{
		"SupportPodPidsLimit=true",
		"LocalStorageCapacityIsolation=false",
		"RotateKubeletServerCertificate=true",
	}
	p.ImageRegistryHTTPSecret = uuid.New().String()
	return p
}

type ClusterParams struct {
	Namespace                           string                 `json:"namespace"`
	ExternalAPIDNSName                  string                 `json:"externalAPIDNSName"`
	ExternalAPIAddress                  string                 `json:"externalAPIAddress"`
	ExternalAPIPort                     uint                   `json:"externalAPIPort"`
	ExternalOpenVPNAddress              string                 `json:"externalVPNAddress"`
	ExternalOpenVPNPort                 uint                   `json:"externalVPNPort"`
	ExternalOauthDNSName                string                 `json:"externalOauthDNSName"`
	ExternalOauthPort                   uint                   `json:"externalOauthPort"`
	IdentityProviders                   string                 `json:"identityProviders"`
	ServiceCIDR                         string                 `json:"serviceCIDR"`
	NamedCerts                          []NamedCert            `json:"namedCerts,omitempty"`
	PodCIDR                             string                 `json:"podCIDR"`
	ReleaseImage                        string                 `json:"releaseImage"`
	IngressSubdomain                    string                 `json:"ingressSubdomain"`
	OpenShiftAPIClusterIP               string                 `json:"openshiftAPIClusterIP"`
	ImageRegistryHTTPSecret             string                 `json:"imageRegistryHTTPSecret"`
	RouterNodePortHTTP                  string                 `json:"routerNodePortHTTP"`
	RouterNodePortHTTPS                 string                 `json:"routerNodePortHTTPS"`
	BaseDomain                          string                 `json:"baseDomain"`
	NetworkType                         string                 `json:"networkType"`
	Replicas                            string                 `json:"replicas"`
	EtcdClientName                      string                 `json:"etcdClientName"`
	OriginReleasePrefix                 string                 `json:"originReleasePrefix"`
	OpenshiftAPIServerCABundle          string                 `json:"openshiftAPIServerCABundle"`
	CloudProvider                       string                 `json:"cloudProvider"`
	CVOSetupImage                       string                 `json:"cvoSetupImage"`
	InternalAPIPort                     uint                   `json:"internalAPIPort"`
	RouterServiceType                   string                 `json:"routerServiceType"`
	KubeAPIServerResources              []ResourceRequirements `json:"kubeAPIServerResources"`
	OpenshiftControllerManagerResources []ResourceRequirements `json:"openshiftControllerManagerResources"`
	ClusterVersionOperatorResources     []ResourceRequirements `json:"clusterVersionOperatorResources"`
	KubeControllerManagerResources      []ResourceRequirements `json:"kubeControllerManagerResources"`
	OpenshiftAPIServerResources         []ResourceRequirements `json:"openshiftAPIServerResources"`
	KubeSchedulerResources              []ResourceRequirements `json:"kubeSchedulerResources"`
	ControlPlaneOperatorResources       []ResourceRequirements `json:"controlPlaneOperatorResources"`
	OAuthServerResources                []ResourceRequirements `json:"oAuthServerResources"`
	ClusterPolicyControllerResources    []ResourceRequirements `json:"clusterPolicyControllerResources"`
	AutoApproverResources               []ResourceRequirements `json:"autoApproverResources"`
	OpenVPNClientResources              []ResourceRequirements `json:"openVPNClientResources"`
	OpenVPNServerResources              []ResourceRequirements `json:"openVPNServerResources"`
	APIServerAuditEnabled               bool                   `json:"apiServerAuditEnabled"`
	RestartDate                         string                 `json:"restartDate"`
	ControlPlaneOperatorImage           string                 `json:"controlPlaneOperatorImage"`
	ControlPlaneOperatorControllers     []string               `json:"controlPlaneOperatorControllers"`
	ROKSMetricsImage                    string                 `json:"roksMetricsImage"`
	ExtraFeatureGates                   []string               `json:"extraFeatureGates"`
	ControlPlaneOperatorSecurity        string                 `json:"controlPlaneOperatorSecurity"`
	ApiserverLivenessPath               string                 `json:"apiserverLivenessPath"`
	PlatformType                        string                 `json:"platformType"`
	HypershiftOperatorImage             string                 `json:"hypershiftOperatorImage"`
	HypershiftOperatorResources         []ResourceRequirements `json:"hypershiftOperatorResourceRequirements"`
	HypershiftOperatorControllers       []string               `json:"hypershiftOperatorControllers"`
	MachineConfigServerAddress          string                 `json:"machineConfigServerAddress"`
	SSHKey                              string                 `json:"sshKey"`
	DefaultFeatureGates                 []string

	// Fields below are are taken from the ROKs type
	EndpointPublishingStrategyScope string                 `json:"endpointPublishingStrategyScope"`
	ClusterID                       string                 `json:"clusterID"`
	MasterPriorityClass             string                 `json:"masterPriorityClass"`
	KMSServerResources              []ResourceRequirements `json:"kmsServerResources"`
	KMSImage                        string                 `json:"kmsImage"`
	KPInfo                          string                 `json:"kpInfo"`
	KPRegion                        string                 `json:"kpRegion"`
	KPAPIKey                        string                 `json:"kpAPIKey"`
	APINodePort                     uint                   `json:"apiNodePort"`
}

type NamedCert struct {
	NamedCertPrefix string `json:"namedCertPrefix"`
	NamedCertDomain string `json:"namedCertDomain"`
}

type ResourceRequirements struct {
	ResourceLimit   []ResourceLimit   `json:"resourceLimit"`
	ResourceRequest []ResourceRequest `json:"resourceRequest"`
}

type ResourceLimit struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

type ResourceRequest struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}
