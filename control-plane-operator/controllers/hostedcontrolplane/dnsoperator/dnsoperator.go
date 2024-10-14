package dnsoperator

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
)

const (
	// dnsOperatorContainerName is the name of the operator container.
	dnsOperatorContainerName = "dns-operator"
)

// Images stores the image pullspecs for the images that the DNS operator
// deployment references.
type Images struct {
	DNSOperator   string
	CoreDNS       string
	KubeRBACProxy string
	CLI           string
}

// Params stores the parameters that are required to configure the DNS operator
// deployment on HyperShift, as well as some additional information about the
// deployment.
type Params struct {
	// ReleaseVersion is the HyperShift release version.
	ReleaseVersion string
	// AvailabilityProberImage is the image for the prober, which runs on
	// the management cluster and probes the DNS operator.
	AvailabilityProberImage string
	// Images has the image pullspecs that the DNS operator deployment
	// references.
	Images Images
	// DeploymentConfig has information about the DNS operator deployment.
	DeploymentConfig config.DeploymentConfig
}

// NewParams creates a new Params object for a DNS operator deployment.
func NewParams(hcp *hyperv1.HostedControlPlane, version string, releaseImageProvider imageprovider.ReleaseImageProvider, userReleaseImageProvider imageprovider.ReleaseImageProvider, setDefaultSecurityContext bool) Params {
	p := Params{
		Images: Images{
			DNSOperator:   releaseImageProvider.GetImage("cluster-dns-operator"),
			CoreDNS:       userReleaseImageProvider.GetImage("coredns"),
			KubeRBACProxy: userReleaseImageProvider.GetImage("kube-rbac-proxy"),
			CLI:           userReleaseImageProvider.GetImage("cli"),
		},
		ReleaseVersion:          version,
		AvailabilityProberImage: releaseImageProvider.GetImage(util.AvailabilityProberImageName),
	}

	p.DeploymentConfig.AdditionalAnnotations = map[string]string{
		"target.workload.openshift.io/management": `{"effect": "PreferredDuringScheduling"}`,
	}
	p.DeploymentConfig.AdditionalLabels = map[string]string{
		"name":                             "dns-operator",
		"app":                              "dns-operator",
		hyperv1.ControlPlaneComponentLabel: "dns-operator",
	}
	p.DeploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		p.DeploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}
	p.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	p.DeploymentConfig.SetDefaults(hcp, nil, ptr.To(1))
	p.DeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext

	return p
}

// ReconcileDeployment reconciles a deployment of the DNS operator, which runs
// in the management cluster and manages operands in a hosted cluster.  For
// non-HyperShift clusters, the DNS operator is deployed by
// cluster-version-operator with the following manifest:
// <https://github.com/openshift/cluster-dns-operator/blob/master/manifests/0000_70_dns-operator_02-deployment.yaml>.
// For HyperShift, the deployment differs from non-HyperShift clusters in the
// following ways:
//
// * The operator is configured with a kubeconfig for the managed cluster.
//
//   - The operator metrics are exposed using cleartext rather than being
//     protected using kube-rbac-proxy.  (However, CoreDNS's metrics are still
//     protected using kube-rbac-proxy on the hosted cluster.)
//
//   - The operator has HyperShift-specific annotations, labels, owner reference,
//     and affinity rules and omits the node selector for control-plane nodes.
//
//   - The operator has an init container that probes the hosted cluster's
//     kube-apiserver to verify that the dnses.operator.openshift.io API is
//     available.
//
// The DNS operator does not require access to the cloud platform API,
// hosted-cluster services, or external services, so the operator does not
// require any special proxy configuration or permissions in the management
// cluster.
func ReconcileDeployment(dep *appsv1.Deployment, params Params, platformType hyperv1.PlatformType) {
	dep.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: map[string]string{"name": "dns-operator"},
	}
	dep.Spec.Template.Spec.AutomountServiceAccountToken = ptr.To(false)
	dep.Spec.Template.Spec.Containers = []corev1.Container{{
		Command: []string{"dns-operator"},
		Env: []corev1.EnvVar{
			{
				Name:  "RELEASE_VERSION",
				Value: params.ReleaseVersion,
			}, {
				Name:  "IMAGE",
				Value: params.Images.CoreDNS,
			}, {
				Name:  "OPENSHIFT_CLI_IMAGE",
				Value: params.Images.CLI,
			}, {
				Name:  "KUBE_RBAC_PROXY_IMAGE",
				Value: params.Images.KubeRBACProxy,
			}, {
				Name:  "KUBECONFIG",
				Value: "/etc/kubernetes/kubeconfig",
			},
		},
		Image: params.Images.DNSOperator,
		Name:  dnsOperatorContainerName,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("29Mi"),
			},
		},
		TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
		VolumeMounts: []corev1.VolumeMount{{
			Name:      manifests.DNSOperatorKubeconfig("").Name,
			MountPath: "/etc/kubernetes",
		}},
	}}
	dep.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyAlways
	dep.Spec.Template.Spec.TerminationGracePeriodSeconds = ptr.To[int64](2)
	dep.Spec.Template.Spec.Volumes = []corev1.Volume{{
		Name: "dns-operator-kubeconfig",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName:  "dns-operator-kubeconfig",
				DefaultMode: ptr.To[int32](0640),
			},
		},
	}}
	util.AvailabilityProber(
		kas.InClusterKASReadyURL(platformType),
		params.AvailabilityProberImage,
		&dep.Spec.Template.Spec,
		func(o *util.AvailabilityProberOpts) {
			o.KubeconfigVolumeName = "dns-operator-kubeconfig"
			o.RequiredAPIs = []schema.GroupVersionKind{
				{Group: "operator.openshift.io", Version: "v1", Kind: "DNS"},
			}
			o.WaitForLabeledPodsGone = "openshift-dns-operator/name=dns-operator"
		},
	)
	params.DeploymentConfig.ApplyTo(dep)
}
