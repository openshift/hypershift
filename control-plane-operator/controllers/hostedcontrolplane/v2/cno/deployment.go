package cno

import (
	"fmt"
	"os"
	"strconv"

	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/rhobsmonitoring"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/blang/semver"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	cnoEnvVars, err := buildCNOEnvVars(cpContext)
	if err != nil {
		return err
	}
	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Env = append(c.Env, cnoEnvVars...)
	})

	util.UpdateContainer("client-token-minter", deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args, "--token-audience", cpContext.HCP.Spec.IssuerURL)
	})

	util.UpdateContainer("init-client-token-minter", deployment.Spec.Template.Spec.InitContainers, func(c *corev1.Container) {
		c.Args = append(c.Args, "--token-audience", cpContext.HCP.Spec.IssuerURL)
	})

	relaseVersion := cpContext.UserReleaseImageProvider.Version()
	parsedReleaseVersion, err := semver.Parse(relaseVersion)
	if err != nil {
		return fmt.Errorf("parsing ReleaseVersion (%s): %w", relaseVersion, err)
	}
	if parsedReleaseVersion.Minor == uint64(12) {
		deployment.Spec.Template.Spec.InitContainers = append(deployment.Spec.Template.Spec.InitContainers, corev1.Container{
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
			Image: "cli",
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

	return nil
}

func buildCNOEnvVars(cpContext component.WorkloadContext) ([]corev1.EnvVar, error) {
	hcp := cpContext.HCP
	releaseImageProvider := cpContext.ReleaseImageProvider
	userReleaseImageProvider := cpContext.UserReleaseImageProvider

	apiServerAddress := hcp.Status.ControlPlaneEndpoint.Host
	apiServerPort := hcp.Status.ControlPlaneEndpoint.Port
	if util.IsPrivateHCP(hcp) {
		apiServerAddress = fmt.Sprintf("api.%s.hypershift.local", hcp.Name)
		apiServerPort = util.APIPortForLocalZone(util.IsLBKAS(hcp))
	}

	cnoEnv := []corev1.EnvVar{
		{Name: "HOSTED_CLUSTER_NAME", Value: hcp.Name},
		{Name: "TOKEN_AUDIENCE", Value: hcp.Spec.IssuerURL},

		{Name: "RELEASE_VERSION", Value: cpContext.UserReleaseImageProvider.Version()},
		{Name: "OPENSHIFT_RELEASE_IMAGE", Value: util.HCPControlPlaneReleaseImage(hcp)},
		{Name: "APISERVER_OVERRIDE_HOST", Value: apiServerAddress}, // We need to pass this down to networking components on the nodes
		{Name: "APISERVER_OVERRIDE_PORT", Value: fmt.Sprint(apiServerPort)},

		{Name: "MULTUS_ADMISSION_CONTROLLER_IMAGE", Value: releaseImageProvider.GetImage("multus-admission-controller")},
		{Name: "OVN_CONTROL_PLANE_IMAGE", Value: releaseImageProvider.GetImage("ovn-kubernetes")},
		{Name: "CLOUD_NETWORK_CONFIG_CONTROLLER_IMAGE", Value: releaseImageProvider.GetImage("cloud-network-config-controller")},
		{Name: "TOKEN_MINTER_IMAGE", Value: releaseImageProvider.GetImage("token-minter")},
		{Name: "CLI_CONTROL_PLANE_IMAGE", Value: releaseImageProvider.GetImage("cli")},
		{Name: "SOCKS5_PROXY_IMAGE", Value: releaseImageProvider.GetImage("socks5-proxy")},

		{Name: "KUBE_PROXY_IMAGE", Value: userReleaseImageProvider.GetImage("kube-proxy")},
		{Name: "KUBE_RBAC_PROXY_IMAGE", Value: userReleaseImageProvider.GetImage("kube-rbac-proxy")},
		{Name: "MULTUS_IMAGE", Value: userReleaseImageProvider.GetImage("multus-cni")},
		{Name: "CNI_PLUGINS_IMAGE", Value: userReleaseImageProvider.GetImage("container-networking-plugins")},
		{Name: "BOND_CNI_PLUGIN_IMAGE", Value: userReleaseImageProvider.GetImage("network-interface-bond-cni")},
		{Name: "WHEREABOUTS_CNI_IMAGE", Value: userReleaseImageProvider.GetImage("multus-whereabouts-ipam-cni")},
		{Name: "ROUTE_OVERRRIDE_CNI_IMAGE", Value: userReleaseImageProvider.GetImage("multus-route-override-cni")},
		{Name: "MULTUS_NETWORKPOLICY_IMAGE", Value: userReleaseImageProvider.GetImage("multus-networkpolicy")},
		{Name: "OVN_IMAGE", Value: userReleaseImageProvider.GetImage("ovn-kubernetes")},
		{Name: "EGRESS_ROUTER_CNI_IMAGE", Value: userReleaseImageProvider.GetImage("egress-router-cni")},
		{Name: "NETWORK_METRICS_DAEMON_IMAGE", Value: userReleaseImageProvider.GetImage("network-metrics-daemon")},
		{Name: "NETWORK_CHECK_SOURCE_IMAGE", Value: userReleaseImageProvider.GetImage("cluster-network-operator")},
		{Name: "NETWORK_CHECK_TARGET_IMAGE", Value: userReleaseImageProvider.GetImage("cluster-network-operator")},
		{Name: "NETWORKING_CONSOLE_PLUGIN_IMAGE", Value: userReleaseImageProvider.GetImage("networking-console-plugin")},
		{Name: "CLI_IMAGE", Value: userReleaseImageProvider.GetImage("cli")},
	}

	if !util.IsPrivateHCP(hcp) {
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

	var proxyVars []corev1.EnvVar
	proxy.SetEnvVars(&proxyVars)
	// CNO requires the proxy values to deploy cloud network config controller in the management cluster,
	// but it should not use the proxy itself, hence the prefix
	for _, v := range proxyVars {
		cnoEnv = append(cnoEnv, corev1.EnvVar{Name: fmt.Sprintf("MGMT_%s", v.Name), Value: v.Value})
	}

	// If CP is running on kube cluster, pass user ID for CNO to run its managed services with
	if cpContext.SetDefaultSecurityContext {
		cnoEnv = append(cnoEnv, corev1.EnvVar{
			Name: "RUN_AS_USER", Value: strconv.Itoa(config.DefaultSecurityContextUser),
		})
	}

	// For managed Azure deployments, we pass the env variables for:
	// - the SecretProviderClass for the Secrets Store CSI driver to use on the CNCC deployment
	// - the filepath of the credentials
	if azureutil.IsAroHCP() {
		cnoEnv = append(cnoEnv,
			corev1.EnvVar{
				Name:  config.ManagedAzureSecretProviderClassEnvVarKey,
				Value: config.ManagedAzureNetworkSecretStoreProviderClassName,
			},
			corev1.EnvVar{
				Name:  config.ManagedAzureCredentialsFilePath,
				Value: hcp.Spec.Platform.Azure.ManagedIdentities.ControlPlane.Network.CredentialsSecretName,
			},
		)
	}

	return cnoEnv, nil
}
