package configoperator

import (
	"fmt"
	"os"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	hostedClusterConfigOperatorName = "hosted-cluster-config-operator"
)

func ReconcileServiceAccount(sa *corev1.ServiceAccount, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(sa)
	util.EnsurePullSecret(sa, common.PullSecret("").Name)
	return nil
}

func ReconcileRole(role *rbacv1.Role, ownerRef config.OwnerRef, platform hyperv1.PlatformType) error {
	ownerRef.ApplyTo(role)
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{corev1.SchemeGroupVersion.Group},
			Resources: []string{
				"pods",
			},
			Verbs: []string{
				"get",
				"patch",
				"update",
				"create",
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{corev1.SchemeGroupVersion.Group},
			Resources: []string{
				"configmaps",
			},
			Verbs: []string{
				"get",
				"patch",
				"update",
				"create",
				"list",
				"watch",
				"delete", // Needed to be able to set owner reference on configmaps
			},
		},
		{
			APIGroups: []string{appsv1.SchemeGroupVersion.Group},
			Resources: []string{
				"deployments",
			},
			Verbs: []string{
				"get",
				"patch",
				"update",
				"list",
				"watch",
			},
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
			Verbs: []string{
				"patch",
				"update",
			},
		},
		{
			// Access to the finalizers subresource is required by the
			// hosted-cluster-config-operator due to an OpenShift requirement
			// that setting an owner of a resource requires write access
			// to the finalizers of the owner resource. The hcco sets the
			// hosted control plane as the owner of configmaps that contain
			// observed global configuration from the guest cluster.
			APIGroups: []string{hyperv1.GroupVersion.Group},
			Resources: []string{
				"hostedcontrolplanes/finalizers",
			},
			Verbs: []string{
				"get",
				"update",
				"patch",
				"delete",
			},
		},
		{
			APIGroups: []string{coordinationv1.SchemeGroupVersion.Group},
			Resources: []string{
				"leases",
			},
			Verbs: []string{
				"create",
				"get",
				"list",
				"update",
			},
		},
		{
			APIGroups: []string{corev1.SchemeGroupVersion.Group},
			Resources: []string{
				"secrets",
				"services",
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{capiv1.GroupVersion.Group},
			Resources: []string{
				"machinesets",
				"machines",
			},
			Verbs: []string{
				"get",
				"patch",
				"update",
				"list",
				"watch",
			},
		},
	}

	switch platform {
	case hyperv1.KubevirtPlatform:
		// By isolating these rules behind the KubevirtPlatform switch case,
		// we know we can add/remove from this list in the future without
		// impacting other platforms.
		role.Rules = append(role.Rules, []rbacv1.PolicyRule{
			// These are needed by the KubeVirt platform in order to
			// use a subdomain route for the guest cluster's default
			// ingress
			{
				APIGroups: []string{"route.openshift.io"},
				Resources: []string{"routes"},
				Verbs: []string{
					"create",
					"get",
					"patch",
					"update",
					"list",
					"watch",
				},
			},
			{
				APIGroups: []string{"route.openshift.io"},
				Resources: []string{"routes/custom-host"},
				Verbs: []string{
					"create",
				},
			},
			{
				APIGroups: []string{discoveryv1.SchemeGroupVersion.Group},
				Resources: []string{
					"endpointslices",
					"endpointslices/restricted",
				},
				Verbs: []string{
					"delete",
					"create",
					"get",
					"patch",
					"update",
					"list",
					"watch",
				},
			},
			{
				APIGroups: []string{
					"kubevirt.io",
				},
				Resources: []string{"virtualmachines"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{
					"kubevirt.io",
				},
				Resources: []string{"virtualmachines/finalizers"},
				Verbs:     []string{"update"},
			},
			{
				APIGroups: []string{corev1.SchemeGroupVersion.Group},
				Resources: []string{
					"services",
				},
				Verbs: []string{
					"create",
					"get",
					"patch",
					"update",
					"list",
					"watch",
				},
			},
		}...)
	}
	// TODO (jparrill): Add RBAC specific needs for Agent platform
	return nil
}

func ReconcileRoleBinding(rb *rbacv1.RoleBinding, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(rb)
	rb.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "Role",
		Name:     manifests.ConfigOperatorRole("").Name,
	}
	rb.Subjects = []rbacv1.Subject{
		{
			Kind: "ServiceAccount",
			Name: manifests.ConfigOperatorServiceAccount("").Name,
		},
	}
	return nil
}

var (
	volumeMounts = util.PodVolumeMounts{
		hccContainerMain().Name: util.ContainerVolumeMounts{
			hccVolumeKubeconfig().Name:      "/etc/kubernetes/kubeconfig",
			hccVolumeRootCA().Name:          "/etc/kubernetes/root-ca",
			hccVolumeClusterSignerCA().Name: "/etc/kubernetes/cluster-signer-ca",
		},
	}
	hccLabels = map[string]string{
		"app":                              hostedClusterConfigOperatorName,
		hyperv1.ControlPlaneComponentLabel: hostedClusterConfigOperatorName,
	}
)

func ReconcileDeployment(deployment *appsv1.Deployment, image, hcpName, openShiftVersion, kubeVersion string, ownerRef config.OwnerRef, deploymentConfig *config.DeploymentConfig, availabilityProberImage string, enableCIDebugOutput bool, platformType hyperv1.PlatformType, konnectivityAddress string, konnectivityPort int32, oauthAddress string, oauthPort int32, releaseImage string, additionalTrustBundle *corev1.LocalObjectReference, hcp *hyperv1.HostedControlPlane, openShiftTrustedCABundleConfigMapForCPOExists bool, registryOverrides map[string]string, openShiftImageRegistryOverrides map[string][]string) error {
	// Before this change we did
	// 		Selector: &metav1.LabelSelector{
	//			MatchLabels: hccLabels,
	//		},
	//		Template: corev1.PodTemplateSpec{
	//			ObjectMeta: metav1.ObjectMeta{
	//				Labels: hccLabels,
	//			}
	// As a consequence of using the same memory address for both MatchLabels and Labels, when setColocation set the colocationLabelKey in additionalLabels
	// it got also silently included in MatchLabels. This made any additional additionalLabel to break reconciliation because MatchLabels is an immutable field.
	// So now we leave Selector.MatchLabels if it has something already and use a different var from .Labels so the former is not impacted by additionalLabels changes.
	selectorLabels := hccLabels
	if deployment.Spec.Selector != nil && deployment.Spec.Selector.MatchLabels != nil {
		selectorLabels = deployment.Spec.Selector.MatchLabels
	}

	ownerRef.ApplyTo(deployment)

	// preserve existing resource requirements for main scheduler container
	mainContainer := util.FindContainer(hostedClusterConfigOperatorName, deployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		deploymentConfig.SetContainerResourcesIfPresent(mainContainer)
	}

	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: selectorLabels,
		},
		Strategy: appsv1.DeploymentStrategy{
			Type: appsv1.RecreateDeploymentStrategyType,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				// We copy the map here, otherwise this .Labels would point to the same address that .MatchLabels
				// Then when additionalLabels are applied it silently modifies .MatchLabels.
				// We could also change additionalLabels.ApplyTo but that might have a bigger impact.
				// TODO (alberto): Refactor support.config package and gate all components definition on the library.
				Labels: config.CopyStringMap(hccLabels),
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					util.BuildContainer(hccContainerMain(), buildHCCContainerMain(image, hcpName, openShiftVersion, kubeVersion, enableCIDebugOutput, platformType, konnectivityAddress, konnectivityPort, oauthAddress, oauthPort, releaseImage, registryOverrides, openShiftImageRegistryOverrides)),
				},
				Volumes: []corev1.Volume{
					util.BuildVolume(hccVolumeKubeconfig(), buildHCCVolumeKubeconfig),
					util.BuildVolume(hccVolumeRootCA(), buildHCCVolumeRootCA),
					util.BuildVolume(hccVolumeClusterSignerCA(), buildHCCClusterSignerCA),
				},
				ServiceAccountName: manifests.ConfigOperatorServiceAccount("").Name,
			},
		},
	}
	if additionalTrustBundle != nil {
		util.DeploymentAddTrustBundleVolume(additionalTrustBundle, deployment)
	}
	if openShiftTrustedCABundleConfigMapForCPOExists {
		util.DeploymentAddOpenShiftTrustedCABundleConfigMap(deployment)
	}
	if IsExternalInfraKv(hcp) {
		// injects the kubevirt credentials secret volume, volume mount path, and appends cli arg.
		util.DeploymentAddKubevirtInfraCredentials(deployment)
	}

	deploymentConfig.ApplyTo(deployment)
	util.AvailabilityProber(kas.InClusterKASReadyURL(hcp.Spec.Platform.Type), availabilityProberImage, &deployment.Spec.Template.Spec, func(o *util.AvailabilityProberOpts) {
		o.KubeconfigVolumeName = "kubeconfig"
		o.RequiredAPIs = []schema.GroupVersionKind{
			{Group: "imageregistry.operator.openshift.io", Version: "v1", Kind: "Config"},
			{Group: "config.openshift.io", Version: "v1", Kind: "Infrastructure"},
			{Group: "config.openshift.io", Version: "v1", Kind: "DNS"},
			{Group: "config.openshift.io", Version: "v1", Kind: "Ingress"},
			{Group: "config.openshift.io", Version: "v1", Kind: "Network"},
			{Group: "config.openshift.io", Version: "v1", Kind: "Proxy"},
			{Group: "config.openshift.io", Version: "v1", Kind: "Build"},
			{Group: "config.openshift.io", Version: "v1", Kind: "Image"},
			{Group: "config.openshift.io", Version: "v1", Kind: "Project"},
			{Group: "config.openshift.io", Version: "v1", Kind: "ClusterVersion"},
			{Group: "config.openshift.io", Version: "v1", Kind: "FeatureGate"},
			{Group: "config.openshift.io", Version: "v1", Kind: "ClusterOperator"},
			{Group: "config.openshift.io", Version: "v1", Kind: "OperatorHub"},
			{Group: "operator.openshift.io", Version: "v1", Kind: "Network"},
			{Group: "operator.openshift.io", Version: "v1", Kind: "CloudCredential"},
			{Group: "operator.openshift.io", Version: "v1", Kind: "IngressController"},
		}
	})
	return nil
}

func hccContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: hostedClusterConfigOperatorName,
	}
}

func hccVolumeKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func hccVolumeRootCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "root-ca",
	}
}

func hccVolumeClusterSignerCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "cluster-signer-ca",
	}
}

func buildHCCContainerMain(image, hcpName, openShiftVersion, kubeVersion string, enableCIDebugOutput bool, platformType hyperv1.PlatformType, konnectivityAddress string, konnectivityPort int32, oauthAddress string, oauthPort int32, releaseImage string, registryOverrides map[string]string, openShiftImageRegistryOverrides map[string][]string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Command = []string{
			"/usr/bin/control-plane-operator",
			hostedClusterConfigOperatorName,
			fmt.Sprintf("--initial-ca-file=%s", path.Join(volumeMounts.Path(c.Name, hccVolumeRootCA().Name), certs.CASignerCertMapKey)),
			fmt.Sprintf("--cluster-signer-ca-file=%s", path.Join(volumeMounts.Path(c.Name, hccVolumeClusterSignerCA().Name), certs.CASignerCertMapKey)),
			fmt.Sprintf("--target-kubeconfig=%s", path.Join(volumeMounts.Path(c.Name, hccVolumeKubeconfig().Name), kas.KubeconfigKey)),
			"--namespace", "$(POD_NAMESPACE)",
			"--platform-type", string(platformType),
			fmt.Sprintf("--enable-ci-debug-output=%t", enableCIDebugOutput),
			fmt.Sprintf("--hosted-control-plane=%s", hcpName),
			fmt.Sprintf("--konnectivity-address=%s", konnectivityAddress),
			fmt.Sprintf("--konnectivity-port=%d", konnectivityPort),
			fmt.Sprintf("--oauth-address=%s", oauthAddress),
			fmt.Sprintf("--oauth-port=%d", oauthPort),
			"--registry-overrides", util.ConvertRegistryOverridesToCommandLineFlag(registryOverrides),
		}
		if platformType == hyperv1.IBMCloudPlatform {
			c.Command = append(c.Command, "--controllers=controller-manager-ca,resources,inplaceupgrader,drainer,hcpstatus")
		}
		c.Ports = []corev1.ContainerPort{{Name: "metrics", ContainerPort: 8080}}
		c.Env = []corev1.EnvVar{
			{
				Name: "POD_NAMESPACE",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.namespace",
					},
				},
			},
			{
				Name:  "OPENSHIFT_RELEASE_VERSION",
				Value: openShiftVersion,
			},
			{
				Name:  "KUBERNETES_VERSION",
				Value: kubeVersion,
			},
			{
				Name:  "OPERATE_ON_RELEASE_IMAGE",
				Value: releaseImage,
			},
			{
				Name:  "OPENSHIFT_IMG_OVERRIDES",
				Value: util.ConvertOpenShiftImageRegistryOverridesToCommandLineFlag(openShiftImageRegistryOverrides),
			},
		}
		proxy.SetEnvVars(&c.Env)
		if os.Getenv("ENABLE_SIZE_TAGGING") == "1" {
			c.Env = append(c.Env,
				corev1.EnvVar{
					Name:  "ENABLE_SIZE_TAGGING",
					Value: "1",
				},
			)
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func buildHCCVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName:  manifests.HCCOKubeconfigSecret("").Name,
		DefaultMode: ptr.To[int32](0640),
	}
}

func buildHCCVolumeRootCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.DefaultMode = ptr.To[int32](420)
	v.ConfigMap.Name = manifests.RootCAConfigMap("").Name
}

func buildHCCClusterSignerCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.DefaultMode = ptr.To[int32](0640)
	v.ConfigMap.Name = manifests.KubeletClientCABundle("").Name
}

func IsExternalInfraKv(hcp *hyperv1.HostedControlPlane) bool {
	if hcp.Spec.Platform.Kubevirt != nil &&
		hcp.Spec.Platform.Kubevirt.Credentials != nil &&
		hcp.Spec.Platform.Kubevirt.Credentials.InfraKubeConfigSecret != nil &&
		hcp.Spec.Platform.Kubevirt.Credentials.InfraNamespace != "" {
		return true
	} else {
		return false
	}
}
