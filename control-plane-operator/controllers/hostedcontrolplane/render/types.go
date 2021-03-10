package render

import "github.com/google/uuid"

// NewClusterParams returns a new default cluster params struct
func NewClusterParams() *ClusterParams {
	p := &ClusterParams{}
	p.DefaultFeatureGates = []string{
		"SupportPodPidsLimit=true",
		"LocalStorageCapacityIsolation=false",
		"RotateKubeletServerCertificate=true",
		"LegacyNodeRoleBehavior=false",
	}
	p.ImageRegistryHTTPSecret = uuid.New().String()
	return p
}

type PKIParams struct {
	// API Server
	ExternalAPIAddress      string // An externally accessible DNS name or IP for the API server. Currently obtained from the load balancer DNS name.
	NodeInternalAPIServerIP string // A fixed IP that pods on worker nodes will use to communicate with the API server - 172.20.0.1
	ExternalAPIPort         uint   // External API server port - fixed at 6443. This is used for kubeconfig generation.
	InternalAPIPort         uint   // Internal API server network (on service network of host) - fixed at 6443. Used for kubeconfig generation.
	ServiceCIDR             string // Used to determine the internal IP address of the Kube service and generate an IP for it.

	// OAuth Server address
	ExternalOauthAddress string // An externally accessible DNS name or IP for the Oauth server. Currently obtained from Oauth load balancer DNS name.

	// Ingress
	IngressSubdomain string // Subdomain for cluster ingress. Used to generate the wildcard certificate for ingress.

	// MCO/MCS
	MachineConfigServerAddress string // An externally accessible DNS name or IP for the Machine Config Server. Currently generated using a route hostname.

	// VPN Server
	ExternalOpenVPNAddress string // An externally accessible DNS name or IP for the VPN Server. Currently obtained from VPN load balancer DNS name.

	// Common
	Namespace string // Used to generate internal DNS names for services.
}

type ClusterParams struct {
	Namespace               string      `json:"namespace"`
	ExternalAPIDNSName      string      `json:"externalAPIDNSName"`
	ExternalAPIAddress      string      `json:"externalAPIAddress"`
	ExternalAPIPort         uint        `json:"externalAPIPort"`
	ExternalOpenVPNAddress  string      `json:"externalVPNAddress"`
	ExternalOpenVPNPort     uint        `json:"externalVPNPort"`
	ExternalOauthDNSName    string      `json:"externalOauthDNSName"`
	ExternalOauthPort       uint        `json:"externalOauthPort"`
	IdentityProviders       string      `json:"identityProviders"`
	ServiceCIDR             string      `json:"serviceCIDR"`
	MachineCIDR             string      `json:"machineCIDR"`
	NamedCerts              []NamedCert `json:"namedCerts,omitempty"`
	PodCIDR                 string      `json:"podCIDR"`
	ReleaseImage            string      `json:"releaseImage"`
	IngressSubdomain        string      `json:"ingressSubdomain"`
	OpenShiftAPIClusterIP   string      `json:"openshiftAPIClusterIP"`
	OauthAPIClusterIP       string      `json:"oauthAPIClusterIP"`
	ImageRegistryHTTPSecret string      `json:"imageRegistryHTTPSecret"`
	RouterNodePortHTTP      string      `json:"routerNodePortHTTP"`
	RouterNodePortHTTPS     string      `json:"routerNodePortHTTPS"`
	BaseDomain              string      `json:"baseDomain"`
	NetworkType             string      `json:"networkType"`
	// APIAvailabilityPolicy defines the availability of components that support end-user facing API requests
	APIAvailabilityPolicy AvailabilityPolicy `json:"apiAvailabilityPolicy"`
	// ControllerAvailabilityPolicy defines the availability of controller components for the cluster
	ControllerAvailabilityPolicy           AvailabilityPolicy     `json:"controllerAvailabilityPolicy"`
	EtcdClientName                         string                 `json:"etcdClientName"`
	OriginReleasePrefix                    string                 `json:"originReleasePrefix"`
	OpenshiftAPIServerCABundle             string                 `json:"openshiftAPIServerCABundle"`
	OauthAPIServerCABundle                 string                 `json:"oauthAPIServerCABundle"`
	CloudProvider                          string                 `json:"cloudProvider"`
	CVOSetupImage                          string                 `json:"cvoSetupImage"`
	InternalAPIPort                        uint                   `json:"internalAPIPort"`
	RouterServiceType                      string                 `json:"routerServiceType"`
	KubeAPIServerResources                 []ResourceRequirements `json:"kubeAPIServerResources"`
	OpenshiftControllerManagerResources    []ResourceRequirements `json:"openshiftControllerManagerResources"`
	ClusterVersionOperatorResources        []ResourceRequirements `json:"clusterVersionOperatorResources"`
	KubeControllerManagerResources         []ResourceRequirements `json:"kubeControllerManagerResources"`
	OpenshiftAPIServerResources            []ResourceRequirements `json:"openshiftAPIServerResources"`
	KubeSchedulerResources                 []ResourceRequirements `json:"kubeSchedulerResources"`
	HostedClusterConfigOperatorResources   []ResourceRequirements `json:"hostedClusterConfigOperatorResources"`
	OAuthServerResources                   []ResourceRequirements `json:"oAuthServerResources"`
	ClusterPolicyControllerResources       []ResourceRequirements `json:"clusterPolicyControllerResources"`
	AutoApproverResources                  []ResourceRequirements `json:"autoApproverResources"`
	OpenVPNClientResources                 []ResourceRequirements `json:"openVPNClientResources"`
	OpenVPNServerResources                 []ResourceRequirements `json:"openVPNServerResources"`
	APIServerAuditEnabled                  bool                   `json:"apiServerAuditEnabled"`
	RestartDate                            string                 `json:"restartDate"`
	HostedClusterConfigOperatorControllers []string               `json:"hostedClusterConfigOperatorControllers"`
	ROKSMetricsImage                       string                 `json:"roksMetricsImage"`
	ExtraFeatureGates                      []string               `json:"extraFeatureGates"`
	HostedClusterConfigOperatorSecurity    string                 `json:"hostedClusterConfigOperatorSecurity"`
	ApiserverLivenessPath                  string                 `json:"apiserverLivenessPath"`
	PlatformType                           string                 `json:"platformType"`
	HypershiftOperatorImage                string                 `json:"hypershiftOperatorImage"`
	HypershiftOperatorResources            []ResourceRequirements `json:"hypershiftOperatorResourceRequirements"`
	HypershiftOperatorControllers          []string               `json:"hypershiftOperatorControllers"`
	MachineConfigServerAddress             string                 `json:"machineConfigServerAddress"`
	SSHKey                                 string                 `json:"sshKey"`
	InfraID                                string                 `json:"infraID"`
	ProviderCredsSecretName                string                 `json:"providerCredsSecretName"`
	CloudCredentials                       string                 `json:"cloudCredentials"`
	DefaultFeatureGates                    []string

	// AWS params
	AWSZone     string `json:"awsZone"`
	AWSVPCID    string `json:"awsVPCID"`
	AWSRegion   string `json:"awsRegion"`
	AWSSubnetID string `json:"awsSubnetID"`

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

type AvailabilityPolicy string

const (
	HighlyAvailable AvailabilityPolicy = "HighlyAvailable"
	SingleReplica   AvailabilityPolicy = "SingleReplica"
)
