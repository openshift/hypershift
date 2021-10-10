package configoperator

import (
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
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
			hccVolumeKubeconfig().Name: "/etc/kubernetes/kubeconfig",
			hccVolumeRootCA().Name:     "/etc/kubernetes/root-ca",
		},
	}
	hccLabels = map[string]string{
		"app":                         "hosted-cluster-config-operator",
		hyperv1.ControlPlaneComponent: "hosted-cluster-config-operator",
	}
)

func ReconcileDeployment(deployment *appsv1.Deployment, image, openShiftVersion, kubeVersion string, ownerRef config.OwnerRef, config *config.DeploymentConfig, availabilityProberImage string) error {
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
					util.BuildContainer(hccContainerMain(), buildHCCContainerMain(image, openShiftVersion, kubeVersion)),
				},
				Volumes: []corev1.Volume{
					util.BuildVolume(hccVolumeKubeconfig(), buildHCCVolumeKubeconfig),
					util.BuildVolume(hccVolumeRootCA(), buildHCCVolumeRootCA),
				},
				ServiceAccountName: manifests.ConfigOperatorServiceAccount("").Name,
			},
		},
	}
	config.ApplyTo(deployment)
	util.AvailabilityProber(kas.InClusterKASReadyURL(deployment.Namespace), availabilityProberImage, &deployment.Spec.Template.Spec)
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

func buildHCCContainerMain(image, openShiftVersion, kubeVersion string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullAlways
		c.Command = []string{
			"/usr/bin/hosted-cluster-config-operator",
			fmt.Sprintf("--initial-ca-file=%s", path.Join(volumeMounts.Path(c.Name, hccVolumeRootCA().Name), pki.CASignerCertMapKey)),
			fmt.Sprintf("--target-kubeconfig=%s", path.Join(volumeMounts.Path(c.Name, hccVolumeKubeconfig().Name), kas.KubeconfigKey)),
			"--namespace", "$(POD_NAMESPACE)",
		}
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
