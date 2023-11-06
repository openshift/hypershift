package kas

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/aws"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type KubeAPIServerImages struct {
	ClusterConfigOperator      string `json:"clusterConfigOperator"`
	CLI                        string `json:"cli"`
	HyperKube                  string `json:"hyperKube"`
	IBMCloudKMS                string `json:"ibmcloudKMS"`
	AWSKMS                     string `json:"awsKMS"`
	Portieris                  string `json:"portieris"`
	TokenMinterImage           string
	AWSPodIdentityWebhookImage string
	KonnectivityServer         string
}

type KubeAPIServerParams struct {
	APIServer           *configv1.APIServerSpec      `json:"apiServer"`
	FeatureGate         *configv1.FeatureGateSpec    `json:"featureGate"`
	Network             *configv1.NetworkSpec        `json:"network"`
	Image               *configv1.ImageSpec          `json:"image"`
	Scheduler           *configv1.SchedulerSpec      `json:"scheduler"`
	CloudProvider       string                       `json:"cloudProvider"`
	CloudProviderConfig *corev1.LocalObjectReference `json:"cloudProviderConfig"`
	CloudProviderCreds  *corev1.LocalObjectReference `json:"cloudProviderCreds"`

	ServiceAccountIssuer string   `json:"serviceAccountIssuer"`
	ServiceCIDRs         []string `json:"serviceCIDRs"`
	ClusterCIDRs         []string `json:"clusterCIDRs"`
	AdvertiseAddress     string   `json:"advertiseAddress"`
	ExternalAddress      string   `json:"externalAddress"`
	// ExternalPort is the port coming from the status of the SVC which is exposing the KAS, e.g. common router LB, dedicated private/public/ LB...
	// This is used to build kas urls for generated internal kubeconfigs for example.
	ExternalPort    int32  `json:"externalPort"`
	InternalAddress string `json:"internalAddress"`
	// InternalPort is the port that was used to expose the KAS SVC.
	// This is used to build kas urls for generated external kubeconfigs for example.
	InternalPort int32 `json:"internalPort"`
	// APIServerPort is port to expose the KAS Pod.
	APIServerPort        int32                        `json:"apiServerPort"`
	ExternalOAuthAddress string                       `json:"externalOAuthAddress"`
	ExternalOAuthPort    int32                        `json:"externalOAuthPort"`
	EtcdURL              string                       `json:"etcdAddress"`
	KubeConfigRef        *hyperv1.KubeconfigSecretRef `json:"kubeConfigRef"`
	AuditWebhookRef      *corev1.LocalObjectReference `json:"auditWebhookRef"`
	ConsolePublicURL     string                       `json:"consolePublicURL"`
	DisableProfiling     bool                         `json:"disableProfiling"`
	config.DeploymentConfig
	config.OwnerRef

	Images KubeAPIServerImages `json:"images"`

	Availability hyperv1.AvailabilityPolicy
}

type KubeAPIServerServiceParams struct {
	// APIServerPort is the port used for the SVC.
	APIServerPort int
	// APIServerListenPort is the port used for the TargetPort.
	APIServerListenPort int
	AllowedCIDRBlocks   []string
	OwnerReference      *metav1.OwnerReference
}

const (
	KonnectivityHealthPort      = 2041
	KonnectivityServerLocalPort = 8090
	KonnectivityServerPort      = 8091
)

func NewKubeAPIServerParams(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider *imageprovider.ReleaseImageProvider, externalAPIAddress string, externalAPIPort int32, externalOAuthAddress string, externalOAuthPort int32, setDefaultSecurityContext bool) *KubeAPIServerParams {
	dns := globalconfig.DNSConfig()
	globalconfig.ReconcileDNSConfig(dns, hcp)
	params := &KubeAPIServerParams{
		ExternalAddress:      externalAPIAddress,
		ExternalPort:         externalAPIPort,
		InternalAddress:      fmt.Sprintf("api.%s.hypershift.local", hcp.Name),
		InternalPort:         util.InternalAPIPortWithDefault(hcp, config.DefaultAPIServerPort),
		ExternalOAuthAddress: externalOAuthAddress,
		ExternalOAuthPort:    externalOAuthPort,
		ServiceAccountIssuer: hcp.Spec.IssuerURL,
		ServiceCIDRs:         util.ServiceCIDRs(hcp.Spec.Networking.ServiceNetwork),
		ClusterCIDRs:         util.ClusterCIDRs(hcp.Spec.Networking.ClusterNetwork),
		Availability:         hcp.Spec.ControllerAvailabilityPolicy,
		ConsolePublicURL:     fmt.Sprintf("https://console-openshift-console.%s", dns.Spec.BaseDomain),
		DisableProfiling:     util.StringListContains(hcp.Annotations[hyperv1.DisableProfilingAnnotation], manifests.KASDeployment("").Name),

		Images: KubeAPIServerImages{
			HyperKube:                  releaseImageProvider.GetImage("hyperkube"),
			CLI:                        releaseImageProvider.GetImage("cli"),
			ClusterConfigOperator:      releaseImageProvider.GetImage("cluster-config-operator"),
			TokenMinterImage:           releaseImageProvider.GetImage("token-minter"),
			AWSKMS:                     releaseImageProvider.GetImage("aws-kms-provider"),
			AWSPodIdentityWebhookImage: releaseImageProvider.GetImage("aws-pod-identity-webhook"),
			KonnectivityServer:         releaseImageProvider.GetImage("apiserver-network-proxy"),
		},
	}
	if hcp.Spec.Configuration != nil {
		params.APIServer = hcp.Spec.Configuration.APIServer
		params.FeatureGate = hcp.Spec.Configuration.FeatureGate
		params.Network = hcp.Spec.Configuration.Network
		params.Image = hcp.Spec.Configuration.Image
		params.Scheduler = hcp.Spec.Configuration.Scheduler
	}

	params.AdvertiseAddress = util.GetAdvertiseAddress(hcp, config.DefaultAdvertiseIPv4Address, config.DefaultAdvertiseIPv6Address)

	params.APIServerPort = util.BindAPIPortWithDefault(hcp, config.DefaultAPIServerPort)
	if _, ok := hcp.Annotations[hyperv1.PortierisImageAnnotation]; ok {
		params.Images.Portieris = hcp.Annotations[hyperv1.PortierisImageAnnotation]
	}

	switch hcp.Spec.Etcd.ManagementType {
	case hyperv1.Unmanaged:
		params.EtcdURL = hcp.Spec.Etcd.Unmanaged.Endpoint
	case hyperv1.Managed:
		params.EtcdURL = fmt.Sprintf("https://etcd-client.%s.svc:2379", hcp.Namespace)
	default:
		params.EtcdURL = config.DefaultEtcdURL
	}
	params.Scheduling = config.Scheduling{
		PriorityClass: config.APICriticalPriorityClass,
	}
	if hcp.Annotations[hyperv1.APICriticalPriorityClass] != "" {
		params.Scheduling.PriorityClass = hcp.Annotations[hyperv1.APICriticalPriorityClass]
	}
	baseLivenessProbeConfig := corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Scheme: corev1.URISchemeHTTPS,
				Port:   intstr.FromInt(int(params.APIServerPort)),
				Path:   "livez?exclude=etcd",
			},
		},
		InitialDelaySeconds: 300,
		PeriodSeconds:       180,
		TimeoutSeconds:      160,
		FailureThreshold:    6,
		SuccessThreshold:    1,
	}
	if hcp.Spec.SecretEncryption != nil {
		// Adjust KAS liveness probe to not have a hard depdendency on kms so problems isolated to kms don't
		// cause the entire kube-apiserver to restart and potentially enter CrashloopBackoff
		totalProviderInstances := 0
		switch hcp.Spec.SecretEncryption.Type {
		case hyperv1.KMS:
			if hcp.Spec.SecretEncryption.KMS != nil {
				switch hcp.Spec.SecretEncryption.KMS.Provider {
				case hyperv1.AWS:
					if hcp.Spec.SecretEncryption.KMS.AWS != nil {
						// Always will have an active key
						totalProviderInstances = 1
						if hcp.Spec.SecretEncryption.KMS.AWS.BackupKey != nil && len(hcp.Spec.SecretEncryption.KMS.AWS.BackupKey.ARN) > 0 {
							totalProviderInstances++
						}
					}
				case hyperv1.IBMCloud:
					if hcp.Spec.SecretEncryption.KMS.IBMCloud != nil {
						totalProviderInstances = len(hcp.Spec.SecretEncryption.KMS.IBMCloud.KeyList)
					}
				}
			}
		}
		for i := 0; i < totalProviderInstances; i++ {
			baseLivenessProbeConfig.HTTPGet.Path = baseLivenessProbeConfig.HTTPGet.Path + fmt.Sprintf("&exclude=kms-provider-%d", i)
		}
	}
	params.LivenessProbes = config.LivenessProbes{
		kasContainerMain().Name: baseLivenessProbeConfig,
		kasContainerIBMCloudKMS().Name: corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTP,
					Port:   intstr.FromInt(int(ibmCloudKMSHealthPort)),
					Path:   "healthz/liveness",
				},
			},
			InitialDelaySeconds: 120,
			PeriodSeconds:       300,
			TimeoutSeconds:      160,
			FailureThreshold:    3,
			SuccessThreshold:    1,
		},
		kasContainerAWSKMSBackup().Name: corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTP,
					Port:   intstr.FromInt(backupAWSKMSHealthPort),
					Path:   "healthz",
				},
			},
			InitialDelaySeconds: 120,
			PeriodSeconds:       300,
			TimeoutSeconds:      160,
			FailureThreshold:    3,
			SuccessThreshold:    1,
		},
		kasContainerAWSKMSActive().Name: corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTP,
					Port:   intstr.FromInt(activeAWSKMSHealthPort),
					Path:   "healthz",
				},
			},
			InitialDelaySeconds: 120,
			PeriodSeconds:       300,
			TimeoutSeconds:      160,
			FailureThreshold:    3,
			SuccessThreshold:    1,
		},
		kasContainerPortieries().Name: corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTPS,
					Port:   intstr.FromInt(portierisPort),
					Path:   "/health/liveness",
				},
			},
			InitialDelaySeconds: 120,
			PeriodSeconds:       300,
			TimeoutSeconds:      160,
			FailureThreshold:    3,
			SuccessThreshold:    1,
		},
		konnectivityServerContainer().Name: {
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTP,
					Port:   intstr.FromInt(int(KonnectivityHealthPort)),
					Path:   "healthz",
				},
			},
			InitialDelaySeconds: 120,
			TimeoutSeconds:      30,
			PeriodSeconds:       60,
			FailureThreshold:    3,
			SuccessThreshold:    1,
		},
	}
	params.ReadinessProbes = config.ReadinessProbes{
		kasContainerMain().Name: {
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTPS,
					Port:   intstr.FromInt(int(params.APIServerPort)),
					Path:   "readyz",
				},
			},
			InitialDelaySeconds: 15,
			PeriodSeconds:       30,
			TimeoutSeconds:      120,
			FailureThreshold:    6,
			SuccessThreshold:    1,
		},
		konnectivityServerContainer().Name: {
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTP,
					Port:   intstr.FromInt(int(KonnectivityHealthPort)),
					Path:   "healthz",
				},
			},
			InitialDelaySeconds: 15,
			PeriodSeconds:       60,
			SuccessThreshold:    1,
			FailureThreshold:    3,
			TimeoutSeconds:      5,
		},
	}
	params.Resources = map[string]corev1.ResourceRequirements{
		kasContainerBootstrap().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("30m"),
			},
		},
		kasContainerMain().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("2Gi"),
				corev1.ResourceCPU:    resource.MustParse("350m"),
			},
		},
		kasContainerAWSKMSActive().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("10Mi"),
				corev1.ResourceCPU:    resource.MustParse("10m"),
			},
		},
		kasContainerAWSKMSBackup().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("10Mi"),
				corev1.ResourceCPU:    resource.MustParse("10m"),
			},
		},
		kasContainerIBMCloudKMS().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("10Mi"),
				corev1.ResourceCPU:    resource.MustParse("10m"),
			},
		},
		kasContainerPortieries().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("20Mi"),
				corev1.ResourceCPU:    resource.MustParse("5m"),
			},
		},
		konnectivityServerContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("10m"),
			},
		},
	}

	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		params.CloudProvider = aws.Provider
	case hyperv1.AzurePlatform:
		params.CloudProvider = azure.Provider
		params.CloudProviderConfig = &corev1.LocalObjectReference{Name: manifests.AzureProviderConfigWithCredentials("").Name}
	}
	if hcp.Spec.AuditWebhook != nil && len(hcp.Spec.AuditWebhook.Name) > 0 {
		params.AuditWebhookRef = hcp.Spec.AuditWebhook
	}
	if _, ok := hcp.Annotations[hyperv1.AWSKMSProviderImage]; ok {
		params.Images.AWSKMS = hcp.Annotations[hyperv1.AWSKMSProviderImage]
	}
	if _, ok := hcp.Annotations[hyperv1.IBMCloudKMSProviderImage]; ok {
		params.Images.IBMCloudKMS = hcp.Annotations[hyperv1.IBMCloudKMSProviderImage]
	}
	if _, ok := hcp.Annotations[hyperv1.KonnectivityServerImageAnnotation]; ok {
		params.Images.KonnectivityServer = hcp.Annotations[hyperv1.KonnectivityServerImageAnnotation]
	}

	params.KubeConfigRef = hcp.Spec.KubeConfig
	params.OwnerRef = config.OwnerRefFrom(hcp)

	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.DeploymentConfig.SetRequestServingDefaults(hcp, kasLabels(), nil)
	params.DeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext

	return params
}

func (p *KubeAPIServerParams) NamedCertificates() []configv1.APIServerNamedServingCert {
	if p.APIServer != nil {
		return p.APIServer.ServingCerts.NamedCertificates
	} else {
		return []configv1.APIServerNamedServingCert{}
	}
}

func (p *KubeAPIServerParams) AuditPolicyConfig() configv1.Audit {
	if p.APIServer != nil {
		return p.APIServer.Audit
	} else {
		return configv1.Audit{
			Profile: configv1.DefaultAuditProfileType,
		}
	}
}

func (p *KubeAPIServerParams) ExternalURL() string {
	return fmt.Sprintf("https://%s:%d", p.ExternalAddress, p.ExternalPort)
}

// InternalURL is used by ReconcileBootstrapKubeconfigSecret.
func (p *KubeAPIServerParams) InternalURL() string {
	return fmt.Sprintf("https://%s:%d", p.InternalAddress, 443)
}

func (p *KubeAPIServerParams) ExternalKubeconfigKey() string {
	if p.KubeConfigRef == nil {
		return ""
	}
	return p.KubeConfigRef.Key
}

func (p *KubeAPIServerParams) ExternalIPConfig() *configv1.ExternalIPConfig {
	if p.Network != nil {
		return p.Network.ExternalIP
	} else {
		return nil
	}
}

func (p *KubeAPIServerParams) ClusterNetwork() []string {
	return p.ClusterCIDRs
}

func (p *KubeAPIServerParams) ServiceNetwork() []string {
	return p.ServiceCIDRs
}

func (p *KubeAPIServerParams) ConfigParams() KubeAPIServerConfigParams {
	return KubeAPIServerConfigParams{
		ExternalIPConfig:             p.ExternalIPConfig(),
		ClusterNetwork:               p.ClusterNetwork(),
		ServiceNetwork:               p.ServiceNetwork(),
		NamedCertificates:            p.NamedCertificates(),
		APIServerPort:                p.APIServerPort,
		TLSSecurityProfile:           p.TLSSecurityProfile(),
		AdditionalCORSAllowedOrigins: p.AdditionalCORSAllowedOrigins(),
		InternalRegistryHostName:     p.InternalRegistryHostName(),
		ExternalRegistryHostNames:    p.ExternalRegistryHostNames(),
		DefaultNodeSelector:          p.DefaultNodeSelector(),
		AdvertiseAddress:             p.AdvertiseAddress,
		ServiceAccountIssuerURL:      p.ServiceAccountIssuerURL(),
		CloudProvider:                p.CloudProvider,
		CloudProviderConfigRef:       p.CloudProviderConfig,
		EtcdURL:                      p.EtcdURL,
		FeatureGates:                 p.FeatureGates(),
		NodePortRange:                p.ServiceNodePortRange(),
		AuditWebhookEnabled:          p.AuditWebhookRef != nil,
		ConsolePublicURL:             p.ConsolePublicURL,
		DisableProfiling:             p.DisableProfiling,
	}
}

type KubeAPIServerConfigParams struct {
	ExternalIPConfig             *configv1.ExternalIPConfig
	ClusterNetwork               []string
	ServiceNetwork               []string
	NamedCertificates            []configv1.APIServerNamedServingCert
	APIServerPort                int32
	TLSSecurityProfile           *configv1.TLSSecurityProfile
	AdditionalCORSAllowedOrigins []string
	InternalRegistryHostName     string
	ExternalRegistryHostNames    []string
	DefaultNodeSelector          string
	AdvertiseAddress             string
	ServiceAccountIssuerURL      string
	CloudProvider                string
	CloudProviderConfigRef       *corev1.LocalObjectReference
	EtcdURL                      string
	FeatureGates                 []string
	NodePortRange                string
	AuditWebhookEnabled          bool
	ConsolePublicURL             string
	DisableProfiling             bool
}

func (p *KubeAPIServerParams) TLSSecurityProfile() *configv1.TLSSecurityProfile {
	if p.APIServer != nil {
		return p.APIServer.TLSSecurityProfile
	}
	return &configv1.TLSSecurityProfile{
		Type:         configv1.TLSProfileIntermediateType,
		Intermediate: &configv1.IntermediateTLSProfile{},
	}
}

func (p *KubeAPIServerParams) AdditionalCORSAllowedOrigins() []string {
	if p.APIServer != nil {
		return p.APIServer.AdditionalCORSAllowedOrigins
	}
	return []string{}
}

func (p *KubeAPIServerParams) InternalRegistryHostName() string {
	return config.DefaultImageRegistryHostname
}

func (p *KubeAPIServerParams) ExternalRegistryHostNames() []string {
	if p.Image != nil {
		return p.Image.ExternalRegistryHostnames
	} else {
		return []string{}
	}
}

func (p *KubeAPIServerParams) DefaultNodeSelector() string {
	if p.Scheduler != nil {
		return p.Scheduler.DefaultNodeSelector
	} else {
		return ""
	}
}

func (p *KubeAPIServerParams) ServiceAccountIssuerURL() string {
	if p.ServiceAccountIssuer != "" {
		return p.ServiceAccountIssuer
	} else {
		return config.DefaultServiceAccountIssuer
	}
}

func (p *KubeAPIServerParams) FeatureGates() []string {
	if p.FeatureGate != nil {
		return config.FeatureGates(&p.FeatureGate.FeatureGateSelection)
	} else {
		return config.FeatureGates(&configv1.FeatureGateSelection{
			FeatureSet: configv1.Default,
		})
	}
}

func (p *KubeAPIServerParams) ServiceNodePortRange() string {
	if p.Network != nil && len(p.Network.ServiceNodePortRange) > 0 {
		return p.Network.ServiceNodePortRange
	} else {
		return config.DefaultServiceNodePortRange
	}
}

func NewKubeAPIServerServiceParams(hcp *hyperv1.HostedControlPlane) *KubeAPIServerServiceParams {
	listenPort := util.BindAPIPortWithDefault(hcp, config.DefaultAPIServerPort)
	port := util.InternalAPIPortWithDefault(hcp, config.DefaultAPIServerPort)
	var allowedCIDRBlocks []string
	for _, block := range util.AllowedCIDRBlocks(hcp) {
		allowedCIDRBlocks = append(allowedCIDRBlocks, string(block))
	}
	return &KubeAPIServerServiceParams{
		APIServerPort:       int(port),
		APIServerListenPort: int(listenPort),
		AllowedCIDRBlocks:   allowedCIDRBlocks,
		OwnerReference:      config.ControllerOwnerRef(hcp),
	}
}
