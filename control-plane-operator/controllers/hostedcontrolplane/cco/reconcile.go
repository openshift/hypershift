package cco

import (
	"path"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/pointer"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/util"
)

const (
	WorkerNamespace      = "openshift-cloud-credential-operator"
	WorkerServiceAccount = "cloud-credential-operator"
)

func selectorLabels() map[string]string {
	return map[string]string{
		"app":                         "cloud-credential-operator",
		hyperv1.ControlPlaneComponent: "cloud-credential-operator",
	}
}

var (
	volumeMounts = util.PodVolumeMounts{
		containerMain().Name: {
			volumeServiceAccountKubeconfig().Name: "/etc/kubernetes",
		},
	}
)

type Params struct {
	operatorImage           string
	kubeRbacProxyImage      string
	availabilityProberImage string

	deploymentConfig config.DeploymentConfig
	releaseVersion   string
	issuerURL        string
	apiPort          *int32

	config.OwnerRef
}

func NewParams(hcp *hyperv1.HostedControlPlane, version string, releaseImageProvider imageprovider.ReleaseImageProvider, setDefaultSecurityContext bool) Params {
	params := Params{
		operatorImage:           releaseImageProvider.GetImage("cloud-credential-operator"),
		kubeRbacProxyImage:      releaseImageProvider.GetImage("kube-rbac-proxy"),
		availabilityProberImage: releaseImageProvider.GetImage(util.AvailabilityProberImageName),
		releaseVersion:          version,
		issuerURL:               hcp.Spec.IssuerURL,
		OwnerRef:                config.OwnerRefFrom(hcp),
		apiPort:                 pointer.Int32(util.KASPodPort(hcp)),
		deploymentConfig: config.DeploymentConfig{
			Scheduling: config.Scheduling{
				PriorityClass: config.DefaultPriorityClass,
			},
			SetDefaultSecurityContext: setDefaultSecurityContext,
			Resources: config.ResourcesSpec{
				containerMain().Name: {
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("10m"),
						corev1.ResourceMemory: resource.MustParse("75Mi"),
					},
				},
			},
		},
	}
	params.deploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		params.deploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}
	params.deploymentConfig.SetDefaults(hcp, selectorLabels(), pointer.Int(1))
	params.deploymentConfig.SetReleaseImageAnnotation(hcp.Spec.ReleaseImage)
	return params
}

func ReconcileDeployment(deployment *appsv1.Deployment, params Params, platformType hyperv1.PlatformType) error {
	params.OwnerRef.ApplyTo(deployment)
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: selectorLabels(),
		},
		Strategy: appsv1.DeploymentStrategy{
			Type: appsv1.RecreateDeploymentStrategyType,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: selectorLabels(),
			},
			Spec: corev1.PodSpec{
				AutomountServiceAccountToken: pointer.Bool(false),
				Containers: []corev1.Container{
					util.BuildContainer(containerMain(), buildMainContainer(params.operatorImage, params.releaseVersion)),
				},
				Volumes: []corev1.Volume{
					util.BuildVolume(volumeServiceAccountKubeconfig(), buildVolumeServiceAccountKubeconfig),
				},
			},
		},
	}

	params.deploymentConfig.ApplyTo(deployment)
	util.AvailabilityProber(kas.InClusterKASReadyURL(platformType), params.availabilityProberImage, &deployment.Spec.Template.Spec, func(o *util.AvailabilityProberOpts) {
		o.KubeconfigVolumeName = volumeServiceAccountKubeconfig().Name
		o.WaitForInfrastructureResource = true
		o.RequiredAPIs = []schema.GroupVersionKind{
			{Group: "operator.openshift.io", Version: "v1", Kind: "CloudCredential"},
		}
	})
	return nil
}

func containerMain() *corev1.Container {
	return &corev1.Container{
		Name: "cloud-credential-operator",
	}
}

func buildMainContainer(image, releaseVersion string) func(*corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.Command = []string{
			"/usr/bin/cloud-credential-operator",
		}
		c.Args = []string{
			"operator",
			"--kubeconfig=" + path.Join(volumeMounts.Path(containerMain().Name, volumeServiceAccountKubeconfig().Name), util.KubeconfigKey),
		}
		c.Env = []corev1.EnvVar{
			{
				Name:  "RELEASE_VERSION",
				Value: releaseVersion,
			},
			{
				Name:  "KUBECONFIG",
				Value: path.Join(volumeMounts.Path(containerMain().Name, volumeServiceAccountKubeconfig().Name), util.KubeconfigKey),
			},
		}
		proxy.SetEnvVars(&c.Env)
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
		c.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
		c.SecurityContext = &corev1.SecurityContext{
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			AllowPrivilegeEscalation: pointer.Bool(false),
		}
	}
}

func volumeServiceAccountKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "service-account-kubeconfig",
	}
}

func buildVolumeServiceAccountKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName:  manifests.CloudCredentialOperatorKubeconfig("").Name,
		DefaultMode: pointer.Int32(0640),
	}
}
