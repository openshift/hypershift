package kas

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/aws"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
)

type KubeAPIServerImages struct {
	ClusterConfigOperator string `json:"clusterConfigOperator"`
	CLI                   string `json:"cli"`
	HyperKube             string `json:"hyperKube"`
	VPN                   string `json:"vpn"`
}

type KubeAPIServerParams struct {
	APIServer           configv1.APIServer          `json:"apiServer"`
	Authentication      configv1.Authentication     `json:"authentication"`
	FeatureGate         configv1.FeatureGate        `json:"featureGate"`
	Network             configv1.Network            `json:"network"`
	OAuth               configv1.OAuth              `json:"oauth"`
	Image               configv1.Image              `json:"image"`
	Scheduler           configv1.Scheduler          `json:"scheduler"`
	CloudProvider       string                      `json:"cloudProvider"`
	CloudProviderConfig corev1.LocalObjectReference `json:"cloudProviderConfig"`

	AdvertiseAddress     string                       `json:"advertiseAddress"`
	ExternalAddress      string                       `json:"externalAddress"`
	ExternalPort         int32                        `json:"externalPort"`
	ExternalOAuthAddress string                       `json:"externalOAuthAddress"`
	ExternalOAuthPort    int32                        `json:"externalOAuthPort"`
	EtcdURL              string                       `json:"etcdAddress"`
	APIServerPort        int32                        `json:"apiServerPort"`
	Replicas             int32                        `json:"replicas"`
	Scheduling           config.Scheduling            `json:"scheduling"`
	AdditionalLabels     map[string]string            `json:"additionalLabels"`
	SecurityContexts     config.SecurityContextSpec   `json:"securityContexts"`
	LivenessProbes       config.LivenessProbes        `json:"livenessProbes"`
	ReadinessProbes      config.ReadinessProbes       `json:"readinessProbes"`
	Resources            config.ResourcesSpec         `json:"resources"`
	KubeConfigRef        *hyperv1.KubeconfigSecretRef `json:"kubeConfigRef"`
	OwnerReference       *metav1.OwnerReference       `json:"ownerReference"`

	Images KubeAPIServerImages `json:"images"`
}

func NewKubeAPIServerParams(hcp *hyperv1.HostedControlPlane, images map[string]string, externalOAuthAddress string, externalOAuthPort int32) *KubeAPIServerParams {
	params := &KubeAPIServerParams{
		APIServer: configv1.APIServer{
			Spec: configv1.APIServerSpec{
				ServingCerts: configv1.APIServerServingCerts{
					NamedCertificates: []configv1.APIServerNamedServingCert{},
				},
				ClientCA: configv1.ConfigMapNameReference{
					Name: "",
				},
				AdditionalCORSAllowedOrigins: []string{},
				TLSSecurityProfile: &configv1.TLSSecurityProfile{
					Type:         configv1.TLSProfileIntermediateType,
					Intermediate: &configv1.IntermediateTLSProfile{},
				},
				Audit: configv1.Audit{
					Profile: configv1.AuditProfileDefaultType,
				},
			},
		},
		Authentication: configv1.Authentication{
			Spec: configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeIntegratedOAuth,
				OAuthMetadata: configv1.ConfigMapNameReference{
					Name: manifests.KASOAuthMetadata(hcp.Namespace).Name,
				},
				WebhookTokenAuthenticator: nil,
				ServiceAccountIssuer:      hcp.Spec.IssuerURL,
			},
		},
		FeatureGate: configv1.FeatureGate{
			Spec: configv1.FeatureGateSpec{
				FeatureGateSelection: configv1.FeatureGateSelection{
					FeatureSet:      configv1.Default,
					CustomNoUpgrade: nil,
				},
			},
		},
		Network: config.Network(hcp),
		OAuth: configv1.OAuth{
			Spec: configv1.OAuthSpec{
				TokenConfig: configv1.TokenConfig{
					AccessTokenInactivityTimeout: nil, // Use default
				},
			},
		},
		Image: configv1.Image{
			Spec: configv1.ImageSpec{
				ExternalRegistryHostnames:  []string{},
				AllowedRegistriesForImport: []configv1.RegistryLocation{},
			},
			Status: configv1.ImageStatus{
				InternalRegistryHostname: config.DefaultImageRegistryHostname,
			},
		},
		Scheduler: configv1.Scheduler{
			Spec: configv1.SchedulerSpec{
				DefaultNodeSelector: "",
			},
		},
		AdvertiseAddress:     config.DefaultAdvertiseAddress,
		ExternalAddress:      hcp.Status.ControlPlaneEndpoint.Host,
		ExternalPort:         hcp.Status.ControlPlaneEndpoint.Port,
		ExternalOAuthAddress: externalOAuthAddress,
		ExternalOAuthPort:    externalOAuthPort,
		EtcdURL:              config.DefaultEtcdURL,
		APIServerPort:        config.DefaultAPIServerPort,

		// TODO: Come up with sane defaults for scheduling APIServer pods
		// Expose configuration
		AdditionalLabels: map[string]string{},
		Scheduling: config.Scheduling{
			PriorityClass: config.DefaultPriorityClass,
		},

		Images: KubeAPIServerImages{
			HyperKube:             images["hyperkube"],
			CLI:                   images["cli"],
			ClusterConfigOperator: images["cluster-config-operator"],
			VPN:                   images["vpn"],
		},
	}
	unprivilegedSecurityContext := corev1.SecurityContext{
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{
				"MKNOD",
				"NET_ADMIN",
			},
		},
		RunAsUser: pointer.Int64Ptr(int64(1001)),
	}
	params.SecurityContexts = config.SecurityContextSpec{
		kasContainerBootstrap().Name:      unprivilegedSecurityContext,
		kasContainerApplyBootstrap().Name: unprivilegedSecurityContext,
		kasContainerMain().Name:           unprivilegedSecurityContext,
		kasContainerVPNClient().Name: {
			Privileged: pointer.BoolPtr(true),
		},
	}
	params.LivenessProbes = config.LivenessProbes{
		kasContainerMain().Name: {
			Handler: corev1.Handler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTPS,
					Port:   intstr.FromInt(int(params.APIServerPort)),
					Path:   "livez",
				},
			},
			InitialDelaySeconds: 45,
			TimeoutSeconds:      10,
		},
	}
	params.ReadinessProbes = config.ReadinessProbes{
		kasContainerMain().Name: {
			Handler: corev1.Handler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTPS,
					Port:   intstr.FromInt(int(params.APIServerPort)),
					Path:   "readyz",
				},
			},
			InitialDelaySeconds: 10,
			TimeoutSeconds:      10,
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
				corev1.ResourceMemory: resource.MustParse("1Gi"),
				corev1.ResourceCPU:    resource.MustParse("265m"),
			},
		},
	}
	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		params.CloudProvider = aws.Provider
		params.CloudProviderConfig.Name = manifests.AWSProviderConfig("").Name
	}

	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		params.Replicas = 3
	default:
		params.Replicas = 1
	}
	params.KubeConfigRef = hcp.Spec.KubeConfig
	params.OwnerReference = config.ControllerOwnerRef(hcp)
	return params
}

func externalAddress(endpoint hyperv1.APIEndpoint) string {
	return fmt.Sprintf("%s:%d", endpoint.Host, endpoint.Port)
}
