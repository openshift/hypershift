package cno

import (
	"fmt"

	"github.com/blang/semver"
	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilpointer "k8s.io/utils/pointer"
)

const operatorName = "cluster-network-operator"

type Images struct {
	NetworkOperator              string
	SDN                          string
	KubeProxy                    string
	KubeRBACProxy                string
	Multus                       string
	MultusAdmissionController    string
	CNIPlugins                   string
	BondCNIPlugin                string
	WhereaboutsCNI               string
	RouteOverrideCNI             string
	MultusNetworkPolicy          string
	OVN                          string
	EgressRouterCNI              string
	KuryrDaemon                  string
	KuryrController              string
	NetworkMetricsDaemon         string
	NetworkCheckSource           string
	NetworkCheckTarget           string
	CloudNetworkConfigController string
	TokenMinter                  string
	CLI                          string
}

type Params struct {
	ReleaseVersion                        string
	AvailabilityProberImage               string
	HostedClusterName                     string
	APIServerAddress                      string
	APIServerPort                         int32
	TokenAudience                         string
	Images                                Images
	OwnerRef                              config.OwnerRef
	DeploymentConfig                      config.DeploymentConfig
	ConnectsThroughInternetToControlplane bool
}

func NewParams(hcp *hyperv1.HostedControlPlane, version string, images map[string]string, setDefaultSecurityContext bool) Params {
	p := Params{
		Images: Images{
			NetworkOperator:              images["cluster-network-operator"],
			SDN:                          images["sdn"],
			KubeProxy:                    images["kube-proxy"],
			KubeRBACProxy:                images["kube-rbac-proxy"],
			Multus:                       images["multus-cni"],
			MultusAdmissionController:    images["multus-admission-controller"],
			CNIPlugins:                   images["container-networking-plugins"],
			BondCNIPlugin:                images["network-interface-bond-cni"],
			WhereaboutsCNI:               images["multus-whereabouts-ipam-cni"],
			RouteOverrideCNI:             images["multus-route-override-cni"],
			MultusNetworkPolicy:          images["multus-networkpolicy"],
			OVN:                          images["ovn-kubernetes"],
			EgressRouterCNI:              images["egress-router-cni"],
			KuryrDaemon:                  images["kuryr-cni"],
			KuryrController:              images["kuryr-controller"],
			NetworkMetricsDaemon:         images["network-metrics-daemon"],
			NetworkCheckSource:           images["cluster-network-operator"],
			NetworkCheckTarget:           images["cluster-network-operator"],
			CloudNetworkConfigController: images["cloud-network-config-controller"],
			TokenMinter:                  images["token-minter"],
			CLI:                          images["cli"],
		},
		ReleaseVersion:                        version,
		AvailabilityProberImage:               images[util.AvailabilityProberImageName],
		OwnerRef:                              config.OwnerRefFrom(hcp),
		ConnectsThroughInternetToControlplane: util.ConnectsThroughInternetToControlplane(hcp.Spec.Platform),
	}

	p.DeploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	p.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	p.DeploymentConfig.SetDefaults(hcp, nil, utilpointer.IntPtr(1))
	p.DeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext
	if util.IsPrivateHCP(hcp) {
		p.APIServerAddress = fmt.Sprintf("api.%s.hypershift.local", hcp.Name)
		p.APIServerPort = util.APIPortWithDefault(hcp, config.DefaultAPIServerPort)
	} else {
		p.APIServerAddress = hcp.Status.ControlPlaneEndpoint.Host
		p.APIServerPort = hcp.Status.ControlPlaneEndpoint.Port
	}
	p.HostedClusterName = hcp.Name
	p.TokenAudience = hcp.Spec.IssuerURL

	return p
}

func ReconcileRole(role *rbacv1.Role, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(role)
	// Required by CNO to manage ovn-kubernetes and cloud-network-config-controller control plane components
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{corev1.SchemeGroupVersion.Group},
			Resources: []string{
				"events",
				"configmaps",
				"pods",
				"secrets",
				"services",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{"policy"},
			Resources: []string{"poddisruptionbudgets"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{appsv1.SchemeGroupVersion.Group},
			Resources: []string{"statefulsets", "deployments"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{routev1.SchemeGroupVersion.Group},
			Resources: []string{"routes", "routes/custom-host"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"monitoring.coreos.com"},
			Resources: []string{
				"servicemonitors",
				"prometheusrules",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{hyperv1.GroupVersion.Group},
			Resources: []string{
				"hostedcontrolplanes",
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{hyperv1.GroupVersion.Group},
			Resources: []string{
				"hostedcontrolplanes/status",
			},
			Verbs: []string{"*"},
		},
	}
	return nil
}

func ReconcileRoleBinding(rb *rbacv1.RoleBinding, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(rb)
	rb.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "Role",
		Name:     manifests.ClusterNetworkOperatorRoleBinding("").Name,
	}
	rb.Subjects = []rbacv1.Subject{
		{
			Kind: "ServiceAccount",
			Name: "default",
		},
	}
	return nil
}

func ReconcileDeployment(dep *appsv1.Deployment, params Params, apiPort *int32) error {
	params.OwnerRef.ApplyTo(dep)

	dep.Spec.Replicas = utilpointer.Int32(1)
	dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"name": operatorName}}
	dep.Spec.Strategy.Type = appsv1.RecreateDeploymentStrategyType
	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = map[string]string{}
	}
	dep.Spec.Template.Annotations["target.workload.openshift.io/management"] = `{"effect": "PreferredDuringScheduling"}`
	if dep.Spec.Template.Labels == nil {
		dep.Spec.Template.Labels = map[string]string{}
	}
	dep.Spec.Template.Labels = map[string]string{
		"name":                        operatorName,
		"app":                         operatorName,
		hyperv1.ControlPlaneComponent: operatorName,
	}

	cnoArgs := []string{"start",
		"--listen=0.0.0.0:9104",
		"--kubeconfig=/etc/hosted-kubernetes/kubeconfig",
		"--namespace=openshift-network-operator",
	}
	var cnoEnv []corev1.EnvVar
	ver, err := semver.Parse(params.ReleaseVersion)
	if err != nil {
		return fmt.Errorf("failed to parse release version %w", err)
	}

	// This is a hack for hypershift CI
	if ver.Minor < 11 {
		// CNO <4.11 doesn't support APISERVER_OVERRIDE_[HOST/PORT] or extra-clusters
		cnoEnv = append(cnoEnv,
			corev1.EnvVar{Name: "KUBERNETES_SERVICE_HOST", Value: params.APIServerAddress},
			corev1.EnvVar{Name: "KUBERNETES_SERVICE_PORT", Value: fmt.Sprint(params.APIServerPort)})
	} else {
		cnoArgs = append(cnoArgs, "--extra-clusters=management=/configs/management")
	}

	if params.ConnectsThroughInternetToControlplane {
		cnoEnv = append(cnoEnv, corev1.EnvVar{
			Name: "PROXY_INTERNAL_APISERVER_ADDRESS", Value: "true",
		})
	}

	dep.Spec.Template.Spec.InitContainers = []corev1.Container{
		// Hack: add an initContainer that deletes the old (in-cluster) CNO first
		// This is because the CVO doesn't "delete" objects, and we need to
		// handle adopting existing clusters
		{
			Command: []string{"/usr/bin/kubectl"},
			Args: []string{
				"--kubeconfig=/etc/hosted-kubernetes/kubeconfig",
				"-n=openshift-network-operator",
				"delete",
				"--ignore-not-found=true",
				"deployment",
				"network-operator",
			},
			Name:  "remove-old-cno",
			Image: params.Images.CLI,
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("50Mi"),
			}},
			TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
			VolumeMounts: []corev1.VolumeMount{
				{Name: "hosted-etc-kube", MountPath: "/etc/hosted-kubernetes"},
			},
		},

		// Add an InitContainer that transmutes the in-cluster config to a Kubeconfig
		// So CNO thinks the "default" config is the hosted cluster
		{
			Command: []string{"/bin/bash"},
			Args: []string{
				"-c",
				`
set -xeuo pipefail
kc=/configs/management
kubectl --kubeconfig $kc config set clusters.default.server "https://[${KUBERNETES_SERVICE_HOST}]:${KUBERNETES_SERVICE_PORT}"
kubectl --kubeconfig $kc config set clusters.default.certificate-authority /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
kubectl --kubeconfig $kc config set users.admin.tokenFile /var/run/secrets/kubernetes.io/serviceaccount/token
kubectl --kubeconfig $kc config set contexts.default.cluster default
kubectl --kubeconfig $kc config set contexts.default.user admin
kubectl --kubeconfig $kc config set contexts.default.namespace $(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace)
kubectl --kubeconfig $kc config use-context default`,
			},
			Name:  "rewrite-config",
			Image: params.Images.CLI,
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("50Mi"),
			}},
			TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
			VolumeMounts: []corev1.VolumeMount{
				{Name: "hosted-etc-kube", MountPath: "/etc/hosted-kubernetes"},
				{Name: "configs", MountPath: "/configs"},
			},
		},
	}

	dep.Spec.Template.Spec.Containers = []corev1.Container{{
		Command: []string{"/usr/bin/cluster-network-operator"},
		Args:    cnoArgs,
		Env: append(cnoEnv, []corev1.EnvVar{
			{Name: "HYPERSHIFT", Value: "true"},
			{Name: "HOSTED_CLUSTER_NAME", Value: params.HostedClusterName},
			{Name: "TOKEN_AUDIENCE", Value: params.TokenAudience},

			{Name: "RELEASE_VERSION", Value: params.ReleaseVersion},
			{Name: "APISERVER_OVERRIDE_HOST", Value: params.APIServerAddress}, // We need to pass this down to networking components on the nodes
			{Name: "APISERVER_OVERRIDE_PORT", Value: fmt.Sprint(params.APIServerPort)},
			{Name: "OVN_NB_RAFT_ELECTION_TIMER", Value: "10"},
			{Name: "OVN_SB_RAFT_ELECTION_TIMER", Value: "16"},
			{Name: "OVN_NORTHD_PROBE_INTERVAL", Value: "5000"},
			{Name: "OVN_CONTROLLER_INACTIVITY_PROBE", Value: "180000"},
			{Name: "OVN_NB_INACTIVITY_PROBE", Value: "60000"},
			{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			}},
			{Name: "HOSTED_CLUSTER_NAMESPACE", ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.namespace",
				},
			}},

			{Name: "SDN_IMAGE", Value: params.Images.SDN},
			{Name: "KUBE_PROXY_IMAGE", Value: params.Images.KubeProxy},
			{Name: "KUBE_RBAC_PROXY_IMAGE", Value: params.Images.KubeRBACProxy},
			{Name: "MULTUS_IMAGE", Value: params.Images.Multus},
			{Name: "MULTUS_ADMISSION_CONTROLLER_IMAGE", Value: params.Images.MultusAdmissionController},
			{Name: "CNI_PLUGINS_IMAGE", Value: params.Images.CNIPlugins},
			{Name: "BOND_CNI_PLUGIN_IMAGE", Value: params.Images.BondCNIPlugin},
			{Name: "WHEREABOUTS_CNI_IMAGE", Value: params.Images.WhereaboutsCNI},
			{Name: "ROUTE_OVERRRIDE_CNI_IMAGE", Value: params.Images.RouteOverrideCNI},
			{Name: "MULTUS_NETWORKPOLICY_IMAGE", Value: params.Images.MultusNetworkPolicy},
			{Name: "OVN_IMAGE", Value: params.Images.OVN},
			{Name: "EGRESS_ROUTER_CNI_IMAGE", Value: params.Images.EgressRouterCNI},
			{Name: "KURYR_DAEMON_IMAGE", Value: params.Images.KuryrDaemon},
			{Name: "KURYR_CONTROLLER_IMAGE", Value: params.Images.KuryrController},
			{Name: "NETWORK_METRICS_DAEMON_IMAGE", Value: params.Images.NetworkMetricsDaemon},
			{Name: "NETWORK_CHECK_SOURCE_IMAGE", Value: params.Images.NetworkCheckSource},
			{Name: "NETWORK_CHECK_TARGET_IMAGE", Value: params.Images.NetworkCheckTarget},
			{Name: "CLOUD_NETWORK_CONFIG_CONTROLLER_IMAGE", Value: params.Images.CloudNetworkConfigController},
			{Name: "TOKEN_MINTER_IMAGE", Value: params.Images.TokenMinter},
			{Name: "CLI_IMAGE", Value: params.Images.CLI},
		}...),
		Name:            operatorName,
		Image:           params.Images.NetworkOperator,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("10m"),
			corev1.ResourceMemory: resource.MustParse("50Mi"),
		}},
		TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
		VolumeMounts: []corev1.VolumeMount{
			{Name: "hosted-etc-kube", MountPath: "/etc/hosted-kubernetes"},
			{Name: "configs", MountPath: "/configs"},
		},
	}}
	dep.Spec.Template.Spec.Volumes = []corev1.Volume{
		{Name: "hosted-etc-kube", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: manifests.KASServiceKubeconfigSecret("").Name}}},
		{Name: "configs", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
	}

	params.DeploymentConfig.ApplyTo(dep)
	util.AvailabilityProber(kas.InClusterKASReadyURL(dep.Namespace, apiPort), params.AvailabilityProberImage, &dep.Spec.Template.Spec, func(o *util.AvailabilityProberOpts) {
		o.KubeconfigVolumeName = "hosted-etc-kube"
		o.RequiredAPIs = []schema.GroupVersionKind{
			{Group: "operator.openshift.io", Version: "v1", Kind: "Network"},
			{Group: "network.operator.openshift.io", Version: "v1", Kind: "EgressRouter"},
			{Group: "network.operator.openshift.io", Version: "v1", Kind: "OperatorPKI"},
		}
	})
	return nil
}
