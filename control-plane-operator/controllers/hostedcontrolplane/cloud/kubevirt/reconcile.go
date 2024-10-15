package kubevirt

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ReconcileCloudConfig(cm *corev1.ConfigMap, hcp *hyperv1.HostedControlPlane) error {
	var infraNamespace string
	var infraKubeconfigPath string
	if hcp.Spec.Platform.Kubevirt.Credentials != nil {
		infraNamespace = hcp.Spec.Platform.Kubevirt.Credentials.InfraNamespace
		infraKubeconfigPath = "/etc/kubernetes/infra-kubeconfig/kubeconfig"
	}

	if infraNamespace == "" {
		// mgmt cluster is used as infra cluster
		infraNamespace = hcp.Namespace
	}
	cfg := cloudConfig(hcp, infraNamespace, infraKubeconfigPath)
	serializedCfg, err := cfg.serialize()
	if err != nil {
		return fmt.Errorf("failed to serialize cloudconfig: %w", err)
	}

	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[CloudConfigKey] = serializedCfg

	return nil
}

func ReconcileCCMServiceAccount(sa *corev1.ServiceAccount, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(sa)
	util.EnsurePullSecret(sa, common.PullSecret("").Name)
	return nil
}

func ReconcileCCMRole(role *rbacv1.Role, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(role)
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"kubevirt.io"},
			Resources: []string{"virtualmachines"},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{"kubevirt.io"},
			Resources: []string{"virtualmachineinstances"},
			Verbs: []string{
				"get",
				"list",
				"watch",
				"update",
			},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"services"},
			Verbs:     []string{rbacv1.VerbAll},
		},
	}
	return nil
}

func ReconcileCCMRoleBinding(roleBinding *rbacv1.RoleBinding, ownerRef config.OwnerRef, sa *corev1.ServiceAccount, role *rbacv1.Role) error {
	ownerRef.ApplyTo(roleBinding)
	roleBinding.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.GroupName,
		Kind:     "Role",
		Name:     role.Name,
	}
	roleBinding.Subjects = []rbacv1.Subject{
		{
			Namespace: sa.Namespace,
			Kind:      rbacv1.ServiceAccountKind,
			Name:      sa.Name,
		},
	}
	return nil
}

func ReconcileDeployment(deployment *appsv1.Deployment, hcp *hyperv1.HostedControlPlane, serviceAccountName string, releaseImageProvider imageprovider.ReleaseImageProvider) error {
	clusterName, ok := hcp.Labels["cluster.x-k8s.io/cluster-name"]
	if !ok {
		return fmt.Errorf("\"cluster.x-k8s.io/cluster-name\" label doesn't exist in HostedControlPlane")
	}
	isExternalInfra := false
	if hcp.Spec.Platform.Kubevirt.Credentials != nil {
		isExternalInfra = true
	}
	deploymentConfig := newDeploymentConfig()
	deploymentConfig.SetDefaults(hcp, ccmLabels(), nil)
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: ccmLabels(),
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: ccmLabels(),
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					util.BuildContainer(CCMContainer(), buildCCMContainer(clusterName, releaseImageProvider, isExternalInfra)),
				},
				Volumes:            []corev1.Volume{},
				ServiceAccountName: serviceAccountName,
			},
		},
	}

	addVolumes(deployment, hcp)

	config.OwnerRefFrom(hcp).ApplyTo(deployment)
	deploymentConfig.ApplyTo(deployment)
	return nil
}

func addVolumes(deployment *appsv1.Deployment, hcp *hyperv1.HostedControlPlane) {

	deployment.Spec.Template.Spec.Volumes = append(
		deployment.Spec.Template.Spec.Volumes,
		util.BuildVolume(ccmVolumeKubeconfig(), buildCCMVolumeKubeconfig),
	)
	deployment.Spec.Template.Spec.Volumes = append(
		deployment.Spec.Template.Spec.Volumes,
		util.BuildVolume(ccmCloudConfig(), buildCCMCloudConfig),
	)
	if hcp.Spec.Platform.Kubevirt.Credentials != nil {
		infraKubeconfigVolumePtr := ccmInfraKubeconfig()
		infraKubeconfigVolume := buildCCMInfraKubeconfig(infraKubeconfigVolumePtr)
		deployment.Spec.Template.Spec.Volumes = append(
			deployment.Spec.Template.Spec.Volumes,
			infraKubeconfigVolume,
		)
	}
}

func podVolumeMounts(isExternalInfra bool) util.PodVolumeMounts {
	cvm := util.ContainerVolumeMounts{
		ccmVolumeKubeconfig().Name: "/etc/kubernetes/kubeconfig",
		ccmCloudConfig().Name:      "/etc/cloud",
	}

	if isExternalInfra {
		cvm[ccmInfraKubeconfig().Name] = "/etc/kubernetes/infra-kubeconfig"
	}

	return util.PodVolumeMounts{
		CCMContainer().Name: cvm,
	}
}

func buildCCMContainer(clusterName string, releaseImageProvider imageprovider.ReleaseImageProvider, isExternalInfra bool) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = releaseImageProvider.GetImage("kubevirt-cloud-controller-manager")
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Command = []string{"/bin/kubevirt-cloud-controller-manager"}
		c.Args = []string{
			"--cloud-provider=kubevirt",
			"--cloud-config=/etc/cloud/cloud-config",
			"--kubeconfig=/etc/kubernetes/kubeconfig/kubeconfig",
			"--authentication-skip-lookup",
			"--cluster-name", clusterName,
		}
		c.VolumeMounts = podVolumeMounts(isExternalInfra).ContainerMounts(c.Name)
	}
}

func buildCCMVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KASServiceKubeconfigSecret("").Name,
	}
}

func buildCCMCloudConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{
		LocalObjectReference: corev1.LocalObjectReference{
			Name: CCMConfigMap("").Name,
		},
	}
}

func buildCCMInfraKubeconfig(v *corev1.Volume) corev1.Volume {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: hyperv1.KubeVirtInfraCredentialsSecretName,
	}
	return *v
}

func newDeploymentConfig() config.DeploymentConfig {
	result := config.DeploymentConfig{}
	result.Resources = config.ResourcesSpec{
		CCMContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("60Mi"),
				corev1.ResourceCPU:    resource.MustParse("75m"),
			},
		},
	}
	result.AdditionalLabels = additionalLabels()
	result.Scheduling.PriorityClass = config.DefaultPriorityClass

	result.Replicas = 1

	return result
}

func ccmLabels() map[string]string {
	return map[string]string{
		"app": "cloud-controller-manager",
	}
}

func additionalLabels() map[string]string {
	return map[string]string{
		hyperv1.ControlPlaneComponentLabel:  "cloud-controller-manager",
		config.NeedManagementKASAccessLabel: "true",
	}
}
