package configoperator

import (
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sutilspointer "k8s.io/utils/pointer"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

func ReconcileServiceAccount(sa *corev1.ServiceAccount, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(sa)
	util.EnsurePullSecret(sa, common.PullSecret("").Name)
	return nil
}

func ReconcileRole(role *rbacv1.Role, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(role)
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{corev1.SchemeGroupVersion.Group},
			Resources: []string{
				"configmaps",
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
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		},
	}
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
		"app":                         "hosted-cluster-config-operator",
		hyperv1.ControlPlaneComponent: "hosted-cluster-config-operator",
	}
)

func ReconcileDeployment(deployment *appsv1.Deployment, image, hcpName, openShiftVersion, kubeVersion string, ownerRef config.OwnerRef, config *config.DeploymentConfig, availabilityProberImage string, enableCIDebugOutput bool, platformType hyperv1.PlatformType, apiInternalPort *int32, konnectivityAddress string, konnectivityPort int32, explicitNonRootSecurityContext bool) error {
	ownerRef.ApplyTo(deployment)
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: hccLabels,
		},
		Strategy: appsv1.DeploymentStrategy{
			Type: appsv1.RecreateDeploymentStrategyType,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: hccLabels,
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					util.BuildContainer(hccContainerMain(), buildHCCContainerMain(image, hcpName, openShiftVersion, kubeVersion, enableCIDebugOutput, platformType, konnectivityAddress, konnectivityPort)),
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

	if explicitNonRootSecurityContext {
		deployment.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
			RunAsUser: k8sutilspointer.Int64Ptr(1001),
		}
	}

	config.ApplyTo(deployment)
	util.AvailabilityProber(kas.InClusterKASReadyURL(deployment.Namespace, apiInternalPort), availabilityProberImage, &deployment.Spec.Template.Spec)
	return nil
}

func hccContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "hosted-cluster-config-operator",
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

func buildHCCContainerMain(image, hcpName, openShiftVersion, kubeVersion string, enableCIDebugOutput bool, platformType hyperv1.PlatformType, konnectivityAddress string, konnectivityPort int32) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullAlways
		c.Command = []string{
			"/usr/bin/hosted-cluster-config-operator",
			fmt.Sprintf("--initial-ca-file=%s", path.Join(volumeMounts.Path(c.Name, hccVolumeRootCA().Name), pki.CASignerCertMapKey)),
			fmt.Sprintf("--cluster-signer-ca-file=%s", path.Join(volumeMounts.Path(c.Name, hccVolumeClusterSignerCA().Name), pki.CASignerCertMapKey)),
			fmt.Sprintf("--target-kubeconfig=%s", path.Join(volumeMounts.Path(c.Name, hccVolumeKubeconfig().Name), kas.KubeconfigKey)),
			"--namespace", "$(POD_NAMESPACE)",
			"--platform-type", string(platformType),
			fmt.Sprintf("--enable-ci-debug-output=%t", enableCIDebugOutput),
			fmt.Sprintf("--hosted-control-plane=%s", hcpName),
			fmt.Sprintf("--konnectivity-address=%s", konnectivityAddress),
			fmt.Sprintf("--konnectivity-port=%d", konnectivityPort),
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
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func buildHCCVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KASServiceKubeconfigSecret("").Name,
	}
}

func buildHCCVolumeRootCA(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.RootCASecret("").Name,
	}
}

func buildHCCClusterSignerCA(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.ClusterSignerCASecret("").Name,
	}
}
