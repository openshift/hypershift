package cno

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/rhobsmonitoring"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/awsprivatelink"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilpointer "k8s.io/utils/pointer"
)

const (
	operatorName          = "cluster-network-operator"
	konnectivityProxyName = "konnectivity-proxy"
	caConfigMap           = "root-ca"
	caConfigMapKey        = "ca.crt"
)

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
	OVNControlPlane              string
	EgressRouterCNI              string
	NetworkMetricsDaemon         string
	NetworkCheckSource           string
	NetworkCheckTarget           string
	CloudNetworkConfigController string
	TokenMinter                  string
	CLI                          string
	CLIControlPlane              string
	Socks5Proxy                  string
}

type Params struct {
	ReleaseVersion          string
	AvailabilityProberImage string
	HostedClusterName       string
	CAConfigMap             string
	CAConfigMapKey          string
	APIServerAddress        string
	APIServerPort           int32
	TokenAudience           string
	Images                  Images
	OwnerRef                config.OwnerRef
	DeploymentConfig        config.DeploymentConfig
	IsPrivate               bool
	ExposedThroughHCPRouter bool
	SbDbPubStrategy         *hyperv1.ServicePublishingStrategy
	DefaultIngressDomain    string
}

func NewParams(hcp *hyperv1.HostedControlPlane, version string, releaseImageProvider *imageprovider.ReleaseImageProvider, userReleaseImageProvider *imageprovider.ReleaseImageProvider, setDefaultSecurityContext bool, defaultIngressDomain string) Params {
	p := Params{
		Images: Images{
			NetworkOperator:              releaseImageProvider.GetImage("cluster-network-operator"),
			SDN:                          userReleaseImageProvider.GetImage("sdn"),
			KubeProxy:                    userReleaseImageProvider.GetImage("kube-proxy"),
			KubeRBACProxy:                userReleaseImageProvider.GetImage("kube-rbac-proxy"),
			Multus:                       userReleaseImageProvider.GetImage("multus-cni"),
			MultusAdmissionController:    releaseImageProvider.GetImage("multus-admission-controller"),
			CNIPlugins:                   userReleaseImageProvider.GetImage("container-networking-plugins"),
			BondCNIPlugin:                userReleaseImageProvider.GetImage("network-interface-bond-cni"),
			WhereaboutsCNI:               userReleaseImageProvider.GetImage("multus-whereabouts-ipam-cni"),
			RouteOverrideCNI:             userReleaseImageProvider.GetImage("multus-route-override-cni"),
			MultusNetworkPolicy:          userReleaseImageProvider.GetImage("multus-networkpolicy"),
			OVN:                          userReleaseImageProvider.GetImage("ovn-kubernetes"),
			OVNControlPlane:              releaseImageProvider.GetImage("ovn-kubernetes"),
			EgressRouterCNI:              userReleaseImageProvider.GetImage("egress-router-cni"),
			NetworkMetricsDaemon:         userReleaseImageProvider.GetImage("network-metrics-daemon"),
			NetworkCheckSource:           userReleaseImageProvider.GetImage("cluster-network-operator"),
			NetworkCheckTarget:           userReleaseImageProvider.GetImage("cluster-network-operator"),
			CloudNetworkConfigController: releaseImageProvider.GetImage("cloud-network-config-controller"),
			TokenMinter:                  releaseImageProvider.GetImage("token-minter"),
			CLI:                          userReleaseImageProvider.GetImage("cli"),
			CLIControlPlane:              releaseImageProvider.GetImage("cli"),
			Socks5Proxy:                  releaseImageProvider.GetImage("socks5-proxy"),
		},
		ReleaseVersion:          version,
		AvailabilityProberImage: releaseImageProvider.GetImage(util.AvailabilityProberImageName),
		OwnerRef:                config.OwnerRefFrom(hcp),
		IsPrivate:               util.IsPrivateHCP(hcp),
		ExposedThroughHCPRouter: isOVNSBDBExposedThroughHCPRouter(hcp),
		HostedClusterName:       hcp.Name,
		TokenAudience:           hcp.Spec.IssuerURL,
		SbDbPubStrategy:         util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.OVNSbDb),
		DefaultIngressDomain:    defaultIngressDomain,
		CAConfigMap:             caConfigMap,
		CAConfigMapKey:          caConfigMapKey,
	}

	p.DeploymentConfig.AdditionalLabels = map[string]string{
		config.NeedManagementKASAccessLabel: "true",
	}
	p.DeploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	// No support for multus-admission-controller at the moment. TODO: add support after https://issues.redhat.com/browse/OCPBUGS-7942 is resolved.
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		p.DeploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}
	p.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	p.DeploymentConfig.SetDefaults(hcp, nil, utilpointer.Int(1))
	p.DeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext
	if util.IsPrivateHCP(hcp) {
		p.APIServerAddress = fmt.Sprintf("api.%s.hypershift.local", hcp.Name)
		p.APIServerPort = util.APIPortForLocalZone(util.IsLBKAS(hcp))
	} else {
		p.APIServerAddress = hcp.Status.ControlPlaneEndpoint.Host
		p.APIServerPort = hcp.Status.ControlPlaneEndpoint.Port
	}

	return p
}

func ReconcileRole(role *rbacv1.Role, ownerRef config.OwnerRef, networkType hyperv1.NetworkType) error {
	ownerRef.ApplyTo(role)
	// The RBAC below is required when the networkType is not OVNKubernetes https://issues.redhat.com/browse/OCPBUGS-26977
	if networkType != hyperv1.OVNKubernetes {
		role.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{corev1.SchemeGroupVersion.Group},
				Resources: []string{
					"configmaps",
				},
				ResourceNames: []string{
					"openshift-service-ca.crt",
					caConfigMap,
				},
				Verbs: []string{
					"get",
					"list",
					"watch",
				},
			},
			{
				APIGroups: []string{corev1.SchemeGroupVersion.Group},
				Resources: []string{
					"configmaps",
				},
				ResourceNames: []string{
					"ovnkube-identity-cm",
				},
				Verbs: []string{
					"list",
					"get",
					"watch",
					"create",
					"patch",
					"update",
				},
			},
			{
				APIGroups: []string{appsv1.SchemeGroupVersion.Group},
				Resources: []string{"statefulsets", "deployments"},
				Verbs:     []string{"list", "watch"},
			},
			{
				APIGroups: []string{appsv1.SchemeGroupVersion.Group},
				Resources: []string{"deployments"},
				ResourceNames: []string{
					"multus-admission-controller",
					"network-node-identity",
				},
				Verbs: []string{"*"},
			},
			{
				APIGroups: []string{corev1.SchemeGroupVersion.Group},
				Resources: []string{"services"},
				ResourceNames: []string{
					"multus-admission-controller",
					"network-node-identity",
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
			APIGroups: []string{"monitoring.coreos.com", "monitoring.rhobs"},
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
		Name:     manifests.ClusterNetworkOperatorRole("").Name,
	}
	rb.Subjects = []rbacv1.Subject{
		{
			Kind: "ServiceAccount",
			Name: manifests.ClusterNetworkOperatorServiceAccount("").Name,
		},
	}
	return nil
}

func ReconcileServiceAccount(sa *corev1.ServiceAccount, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(sa)
	util.EnsurePullSecret(sa, common.PullSecret("").Name)
	return nil
}

func ReconcileDeployment(dep *appsv1.Deployment, params Params, platformType hyperv1.PlatformType) error {
	params.OwnerRef.ApplyTo(dep)

	cnoResources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("100Mi"),
			corev1.ResourceCPU:    resource.MustParse("10m"),
		},
	}
	// preserve existing resource requirements for the CNO container
	mainContainer := util.FindContainer(operatorName, dep.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		if len(mainContainer.Resources.Requests) > 0 || len(mainContainer.Resources.Limits) > 0 {
			cnoResources = mainContainer.Resources
		}
	}

	kProxyResources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("10Mi"),
			corev1.ResourceCPU:    resource.MustParse("10m"),
		},
	}
	// preserve existing resource requirements for the konnectivity-proxy container
	kProxyContainer := util.FindContainer(konnectivityProxyName, dep.Spec.Template.Spec.Containers)
	if kProxyContainer != nil {
		if len(kProxyContainer.Resources.Requests) > 0 || len(kProxyContainer.Resources.Limits) > 0 {
			kProxyResources = kProxyContainer.Resources
		}
	}

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

	sbDbRouteHost := util.ShortenRouteHostnameIfNeeded("ovnkube-sbdb", dep.Namespace, params.DefaultIngressDomain)
	if params.IsPrivate {
		sbDbRouteHost = "ovnkube-sbdb." + awsprivatelink.RouterZoneName(params.HostedClusterName)
	} else if params.SbDbPubStrategy != nil && params.SbDbPubStrategy.Route != nil && params.SbDbPubStrategy.Route.Hostname != "" {
		sbDbRouteHost = params.SbDbPubStrategy.Route.Hostname
	}
	cnoEnv = append(cnoEnv, corev1.EnvVar{
		Name: "OVN_SBDB_ROUTE_HOST", Value: sbDbRouteHost,
	})

	if !params.IsPrivate {
		cnoEnv = append(cnoEnv, corev1.EnvVar{
			Name: "PROXY_INTERNAL_APISERVER_ADDRESS", Value: "true",
		})
	}

	if os.Getenv(rhobsmonitoring.EnvironmentVariable) == "1" {
		cnoEnv = append(cnoEnv, corev1.EnvVar{
			Name:  rhobsmonitoring.EnvironmentVariable,
			Value: "1",
		})
	}

	if params.ExposedThroughHCPRouter && !params.IsPrivate {
		cnoEnv = append(cnoEnv, corev1.EnvVar{Name: "OVN_SBDB_ROUTE_LABELS", Value: util.HCPRouteLabel + "=" + dep.Namespace})
	}
	if params.IsPrivate {
		cnoEnv = append(cnoEnv, corev1.EnvVar{Name: "OVN_SBDB_ROUTE_LABELS", Value: fmt.Sprintf("%v=%v,%v=%v",
			util.HCPRouteLabel, dep.Namespace,
			util.InternalRouteLabel, "true"),
		})
	}

	var proxyVars []corev1.EnvVar
	proxy.SetEnvVars(&proxyVars)
	// CNO requires the proxy values to deploy cloud network config controller in the management cluster,
	// but it should not use the proxy itself, hence the prefix
	for _, v := range proxyVars {
		cnoEnv = append(cnoEnv, corev1.EnvVar{Name: fmt.Sprintf("MGMT_%s", v.Name), Value: v.Value})
	}

	// If CP is running on kube cluster, pass user ID for CNO to run its managed services with
	if params.DeploymentConfig.SetDefaultSecurityContext {
		cnoEnv = append(cnoEnv, corev1.EnvVar{
			Name: "RUN_AS_USER", Value: strconv.Itoa(config.DefaultSecurityContextUser),
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
			Image: params.Images.CLIControlPlane,
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
			Image: params.Images.CLIControlPlane,
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

	if semver.MustParse(params.ReleaseVersion).Minor == uint64(12) {
		dep.Spec.Template.Spec.InitContainers = append(dep.Spec.Template.Spec.InitContainers, corev1.Container{
			Command: []string{"/bin/bash"},
			Args: []string{
				"-c",
				`
set -xeuo pipefail
kc=/etc/hosted-kubernetes/kubeconfig
sc=$(kubectl --kubeconfig $kc get --ignore-not-found validatingwebhookconfiguration multus.openshift.io -o jsonpath='{.webhooks[?(@.name == "multus-validating-config.k8s.io")].clientConfig.service}')
if [[ -n $sc ]]; then kubectl --kubeconfig $kc delete --ignore-not-found validatingwebhookconfiguration multus.openshift.io; fi`,
			},
			Name:  "remove-old-multus-validating-webhook-configuration",
			Image: params.Images.CLIControlPlane,
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("50Mi"),
			}},
			TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
			VolumeMounts: []corev1.VolumeMount{
				{Name: "hosted-etc-kube", MountPath: "/etc/hosted-kubernetes"},
			},
		})
	}

	dep.Spec.Template.Spec.ServiceAccountName = manifests.ClusterNetworkOperatorServiceAccount("").Name
	dep.Spec.Template.Spec.Containers = []corev1.Container{{
		Command: []string{"/usr/bin/cluster-network-operator"},
		Args:    cnoArgs,
		Env: append(cnoEnv, []corev1.EnvVar{
			{Name: "HYPERSHIFT", Value: "true"},
			{Name: "HOSTED_CLUSTER_NAME", Value: params.HostedClusterName},
			{Name: "CA_CONFIG_MAP", Value: params.CAConfigMap},
			{Name: "CA_CONFIG_MAP_KEY", Value: params.CAConfigMapKey},
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
			{Name: "OVN_CONTROL_PLANE_IMAGE", Value: params.Images.OVNControlPlane},
			{Name: "EGRESS_ROUTER_CNI_IMAGE", Value: params.Images.EgressRouterCNI},
			{Name: "NETWORK_METRICS_DAEMON_IMAGE", Value: params.Images.NetworkMetricsDaemon},
			{Name: "NETWORK_CHECK_SOURCE_IMAGE", Value: params.Images.NetworkCheckSource},
			{Name: "NETWORK_CHECK_TARGET_IMAGE", Value: params.Images.NetworkCheckTarget},
			{Name: "CLOUD_NETWORK_CONFIG_CONTROLLER_IMAGE", Value: params.Images.CloudNetworkConfigController},
			{Name: "TOKEN_MINTER_IMAGE", Value: params.Images.TokenMinter},
			{Name: "CLI_IMAGE", Value: params.Images.CLI},
			{Name: "CLI_CONTROL_PLANE_IMAGE", Value: params.Images.CLIControlPlane},
			{Name: "SOCKS5_PROXY_IMAGE", Value: params.Images.Socks5Proxy},
			{Name: "OPENSHIFT_RELEASE_IMAGE", Value: params.DeploymentConfig.AdditionalAnnotations[hyperv1.ReleaseImageAnnotation]},
		}...),
		Name:                     operatorName,
		Image:                    params.Images.NetworkOperator,
		ImagePullPolicy:          corev1.PullIfNotPresent,
		Resources:                cnoResources,
		TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
		VolumeMounts: []corev1.VolumeMount{
			{Name: "hosted-etc-kube", MountPath: "/etc/hosted-kubernetes"},
			{Name: "configs", MountPath: "/configs"},
		},
	},
		{
			// CNO uses konnectivity-proxy to perform proxy readiness checks through the hosted cluster's network
			Name:    konnectivityProxyName,
			Image:   params.Images.Socks5Proxy,
			Command: []string{"/usr/bin/control-plane-operator", "konnectivity-socks5-proxy", "--disable-resolver"},
			Args:    []string{"run"},
			Env: []corev1.EnvVar{{
				Name:  "KUBECONFIG",
				Value: "/etc/kubernetes/kubeconfig",
			}},
			Resources: kProxyResources,
			VolumeMounts: []corev1.VolumeMount{
				{Name: "hosted-etc-kube", MountPath: "/etc/kubernetes"},
				{Name: "konnectivity-proxy-cert", MountPath: "/etc/konnectivity/proxy-client"},
				{Name: "konnectivity-proxy-ca", MountPath: "/etc/konnectivity/proxy-ca"},
			},
		},
	}
	dep.Spec.Template.Spec.Volumes = []corev1.Volume{
		{Name: "hosted-etc-kube", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: manifests.KASServiceKubeconfigSecret("").Name}}},
		{Name: "configs", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "konnectivity-proxy-cert", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: manifests.KonnectivityClientSecret("").Name, DefaultMode: utilpointer.Int32(0640)}}},
		{Name: "konnectivity-proxy-ca", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: manifests.KonnectivityCAConfigMap("").Name}, DefaultMode: utilpointer.Int32(0640)}}},
	}

	params.DeploymentConfig.ApplyTo(dep)
	util.AvailabilityProber(kas.InClusterKASReadyURL(platformType), params.AvailabilityProberImage, &dep.Spec.Template.Spec, func(o *util.AvailabilityProberOpts) {
		o.KubeconfigVolumeName = "hosted-etc-kube"
		o.RequiredAPIs = []schema.GroupVersionKind{
			{Group: "operator.openshift.io", Version: "v1", Kind: "Network"},
			{Group: "network.operator.openshift.io", Version: "v1", Kind: "EgressRouter"},
			{Group: "network.operator.openshift.io", Version: "v1", Kind: "OperatorPKI"},
		}
		o.WaitForInfrastructureResource = true
	})
	return nil
}

func isOVNSBDBExposedThroughHCPRouter(hcp *hyperv1.HostedControlPlane) bool {
	publishingStrategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.OVNSbDb)
	if publishingStrategy == nil || publishingStrategy.Type != hyperv1.Route {
		return false
	}

	if util.IsPrivateHCP(hcp) {
		return true
	}

	return util.IsPublicKASWithDNS(hcp) && publishingStrategy.Route.Hostname != ""
}

func SetRestartAnnotationAndPatch(ctx context.Context, crclient client.Client, dep *appsv1.Deployment, c config.DeploymentConfig) error {
	if c.AdditionalAnnotations[hyperv1.RestartDateAnnotation] == "" {
		return nil
	}

	if err := crclient.Get(ctx, client.ObjectKeyFromObject(dep), dep); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed retrieve deployment: %w", err)
	}

	patch := dep.DeepCopy()
	podMeta := patch.Spec.Template.ObjectMeta
	if podMeta.Annotations == nil {
		podMeta.Annotations = map[string]string{}
	}
	podMeta.Annotations[hyperv1.RestartDateAnnotation] = c.AdditionalAnnotations[hyperv1.RestartDateAnnotation]

	if err := crclient.Patch(ctx, patch, client.MergeFrom(dep)); err != nil {
		return fmt.Errorf("failed to set restart annotation: %w", err)
	}

	return nil
}
