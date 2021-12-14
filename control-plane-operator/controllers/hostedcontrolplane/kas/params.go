package kas

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sutilspointer "k8s.io/utils/pointer"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/aws"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
)

type KubeAPIServerImages struct {
	ClusterConfigOperator string `json:"clusterConfigOperator"`
	CLI                   string `json:"cli"`
	HyperKube             string `json:"hyperKube"`
	IBMCloudKMS           string `json:"ibmcloudKMS"`
	AWSKMS                string `json:"awsKMS"`
	Portieris             string `json:"portieris"`
	TokenMinterImage      string
}

type KubeAPIServerParams struct {
	APIServer           *configv1.APIServer          `json:"apiServer"`
	FeatureGate         *configv1.FeatureGate        `json:"featureGate"`
	Network             *configv1.Network            `json:"network"`
	Image               *configv1.Image              `json:"image"`
	Scheduler           *configv1.Scheduler          `json:"scheduler"`
	CloudProvider       string                       `json:"cloudProvider"`
	CloudProviderConfig *corev1.LocalObjectReference `json:"cloudProviderConfig"`
	CloudProviderCreds  *corev1.LocalObjectReference `json:"cloudProviderCreds"`

	ServiceAccountIssuer string                       `json:"serviceAccountIssuer"`
	ServiceCIDR          string                       `json:"serviceCIDR"`
	PodCIDR              string                       `json:"podCIDR"`
	AdvertiseAddress     string                       `json:"advertiseAddress"`
	ExternalAddress      string                       `json:"externalAddress"`
	ExternalPort         int32                        `json:"externalPort"`
	ExternalOAuthAddress string                       `json:"externalOAuthAddress"`
	ExternalOAuthPort    int32                        `json:"externalOAuthPort"`
	EtcdURL              string                       `json:"etcdAddress"`
	APIServerPort        int32                        `json:"apiServerPort"`
	KubeConfigRef        *hyperv1.KubeconfigSecretRef `json:"kubeConfigRef"`
	AuditWebhookRef      *corev1.LocalObjectReference `json:"auditWebhookRef"`
	config.DeploymentConfig
	config.OwnerRef

	Images KubeAPIServerImages `json:"images"`

	Availability hyperv1.AvailabilityPolicy
}

type KubeAPIServerServiceParams struct {
	APIServerPort  int
	OwnerReference *metav1.OwnerReference
}

func NewKubeAPIServerParams(ctx context.Context, hcp *hyperv1.HostedControlPlane, globalConfig globalconfig.GlobalConfig, images map[string]string, externalOAuthAddress string, externalOAuthPort int32, explicitNonRootSecurityContext bool) *KubeAPIServerParams {
	params := &KubeAPIServerParams{
		APIServer:            globalConfig.APIServer,
		FeatureGate:          globalConfig.FeatureGate,
		Network:              globalConfig.Network,
		Image:                globalConfig.Image,
		Scheduler:            globalConfig.Scheduler,
		ExternalAddress:      hcp.Status.ControlPlaneEndpoint.Host,
		ExternalPort:         hcp.Status.ControlPlaneEndpoint.Port,
		ExternalOAuthAddress: externalOAuthAddress,
		ExternalOAuthPort:    externalOAuthPort,
		ServiceAccountIssuer: hcp.Spec.IssuerURL,
		ServiceCIDR:          hcp.Spec.ServiceCIDR,
		PodCIDR:              hcp.Spec.PodCIDR,
		Availability:         hcp.Spec.ControllerAvailabilityPolicy,

		Images: KubeAPIServerImages{
			HyperKube:             images["hyperkube"],
			CLI:                   images["cli"],
			ClusterConfigOperator: images["cluster-config-operator"],
			TokenMinterImage:      images["hosted-cluster-config-operator"],
		},
	}
	if hcp.Spec.APIAdvertiseAddress != nil {
		params.AdvertiseAddress = *hcp.Spec.APIAdvertiseAddress
	} else {
		params.AdvertiseAddress = config.DefaultAdvertiseAddress
	}
	if hcp.Spec.APIPort != nil {
		params.APIServerPort = *hcp.Spec.APIPort
	} else {
		params.APIServerPort = config.DefaultAPIServerPort
	}
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
				corev1.ResourceMemory: resource.MustParse("1500Mi"),
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
	}
	params.DeploymentConfig.SetColocation(hcp)
	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.DeploymentConfig.SetControlPlaneIsolation(hcp)

	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		params.CloudProvider = aws.Provider
		params.CloudProviderConfig = &corev1.LocalObjectReference{Name: manifests.AWSProviderConfig("").Name}
		params.CloudProviderCreds = &corev1.LocalObjectReference{Name: hcp.Spec.Platform.AWS.KubeCloudControllerCreds.Name}
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

	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		params.Replicas = 3
		params.DeploymentConfig.SetMultizoneSpread(kasLabels())
	default:
		params.Replicas = 1
	}
	params.KubeConfigRef = hcp.Spec.KubeConfig
	params.OwnerRef = config.OwnerRefFrom(hcp)

	if explicitNonRootSecurityContext {
		// iterate over resources and set security context to all the containers
		securityContextsObj := make(config.SecurityContextSpec)
		for containerName := range params.DeploymentConfig.Resources {
			securityContextsObj[containerName] = corev1.SecurityContext{RunAsUser: k8sutilspointer.Int64Ptr(1001)}
		}
		params.DeploymentConfig.SecurityContexts = securityContextsObj
	}

	return params
}

func (p *KubeAPIServerParams) NamedCertificates() []configv1.APIServerNamedServingCert {
	if p.APIServer != nil {
		return p.APIServer.Spec.ServingCerts.NamedCertificates
	} else {
		return []configv1.APIServerNamedServingCert{}
	}
}

func (p *KubeAPIServerParams) AuditPolicyProfile() configv1.AuditProfileType {
	if p.APIServer != nil {
		return p.APIServer.Spec.Audit.Profile
	} else {
		return configv1.DefaultAuditProfileType
	}
}

func (p *KubeAPIServerParams) ExternalURL() string {
	return fmt.Sprintf("https://%s:%d", p.ExternalAddress, p.ExternalPort)
}

func (p *KubeAPIServerParams) ExternalKubeconfigKey() string {
	if p.KubeConfigRef == nil {
		return ""
	}
	return p.KubeConfigRef.Key
}

func (p *KubeAPIServerParams) ExternalIPConfig() *configv1.ExternalIPConfig {
	if p.Network != nil {
		return p.Network.Spec.ExternalIP
	} else {
		return nil
	}
}

func (p *KubeAPIServerParams) ClusterNetwork() string {
	return p.PodCIDR
}

func (p *KubeAPIServerParams) ServiceNetwork() string {
	return p.ServiceCIDR
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
	}
}

type KubeAPIServerConfigParams struct {
	ExternalIPConfig             *configv1.ExternalIPConfig
	ClusterNetwork               string
	ServiceNetwork               string
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
}

func (p *KubeAPIServerParams) TLSSecurityProfile() *configv1.TLSSecurityProfile {
	if p.APIServer != nil {
		return p.APIServer.Spec.TLSSecurityProfile
	}
	return &configv1.TLSSecurityProfile{
		Type:         configv1.TLSProfileIntermediateType,
		Intermediate: &configv1.IntermediateTLSProfile{},
	}
}

func (p *KubeAPIServerParams) AdditionalCORSAllowedOrigins() []string {
	if p.APIServer != nil {
		return p.APIServer.Spec.AdditionalCORSAllowedOrigins
	}
	return []string{}
}

func (p *KubeAPIServerParams) InternalRegistryHostName() string {
	return config.DefaultImageRegistryHostname
}

func (p *KubeAPIServerParams) ExternalRegistryHostNames() []string {
	if p.Image != nil {
		return p.Image.Spec.ExternalRegistryHostnames
	} else {
		return []string{}
	}
}

func (p *KubeAPIServerParams) DefaultNodeSelector() string {
	if p.Scheduler != nil {
		return p.Scheduler.Spec.DefaultNodeSelector
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
		return config.FeatureGates(&p.FeatureGate.Spec.FeatureGateSelection)
	} else {
		return config.FeatureGates(&configv1.FeatureGateSelection{
			FeatureSet: configv1.Default,
		})
	}
}

func (p *KubeAPIServerParams) ServiceNodePortRange() string {
	if p.Network != nil && len(p.Network.Spec.ServiceNodePortRange) > 0 {
		return p.Network.Spec.ServiceNodePortRange
	} else {
		return config.DefaultServiceNodePortRange
	}
}

func NewKubeAPIServerServiceParams(hcp *hyperv1.HostedControlPlane) *KubeAPIServerServiceParams {
	port := config.DefaultAPIServerPort
	if hcp.Spec.APIPort != nil {
		port = int(*hcp.Spec.APIPort)
	}
	return &KubeAPIServerServiceParams{
		APIServerPort:  port,
		OwnerReference: config.ControllerOwnerRef(hcp),
	}
}
