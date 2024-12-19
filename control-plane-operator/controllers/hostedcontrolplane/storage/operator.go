package storage

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/storage/assets"
	assets2 "github.com/openshift/hypershift/support/assets"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	operatorDeployment  = assets2.MustDeployment(assets.ReadFile, "10_deployment-hypershift.yaml")
	operatorRole        = assets2.MustRole(assets.ReadFile, "role.yaml")
	operatorRoleBinding = assets2.MustRoleBinding(assets.ReadFile, "rolebinding.yaml")
)

func ReconcileOperatorDeployment(
	deployment *appsv1.Deployment,
	params *Params,
	platformType hyperv1.PlatformType) error {

	params.OwnerRef.ApplyTo(deployment)
	deployment.Spec = operatorDeployment.DeepCopy().Spec
	for i, container := range deployment.Spec.Template.Spec.Containers {
		switch container.Name {
		case "cluster-storage-operator":
			deployment.Spec.Template.Spec.Containers[i].Image = params.StorageOperatorImage
			params.ImageReplacer.replaceEnvVars(deployment.Spec.Template.Spec.Containers[i].Env)

			// For managed Azure, we need to supply a couple of environment variables for CSO to pass on to the CSI controllers for disk and file.
			// CSO passes those on to the CSI deployment here - https://github.com/openshift/cluster-storage-operator/pull/517/files.
			// CSI then mounts the Secrets Provider Class here - https://github.com/openshift/csi-operator/pull/309/files.
			if azureutil.IsAroHCP() {
				if deployment.Spec.Template.Spec.Containers[i].Env == nil {
					deployment.Spec.Template.Spec.Containers[i].Env = make([]corev1.EnvVar, 0)
				}
				deployment.Spec.Template.Spec.Containers[i].Env = append(deployment.Spec.Template.Spec.Containers[i].Env,
					corev1.EnvVar{
						Name:  "ARO_HCP_SECRET_PROVIDER_CLASS_FOR_DISK",
						Value: config.ManagedAzureDiskCSISecretStoreProviderClassName,
					},
					corev1.EnvVar{
						Name:  "ARO_HCP_SECRET_PROVIDER_CLASS_FOR_FILE",
						Value: config.ManagedAzureFileCSISecretStoreProviderClassName,
					})
			}
		}
	}

	params.DeploymentConfig.ApplyTo(deployment)
	util.AvailabilityProber(kas.InClusterKASReadyURL(platformType), params.AvailabilityProberImage, &deployment.Spec.Template.Spec, func(o *util.AvailabilityProberOpts) {
		o.KubeconfigVolumeName = "guest-kubeconfig"
		o.RequiredAPIs = []schema.GroupVersionKind{
			{Group: "operator.openshift.io", Version: "v1", Kind: "Storage"},
		}
	})
	return nil
}

func ReconcileOperatorRole(
	role *rbacv1.Role,
	params *Params) error {

	params.OwnerRef.ApplyTo(role)
	role.Rules = operatorRole.DeepCopy().Rules
	return nil
}

func ReconcileOperatorRoleBinding(
	roleBinding *rbacv1.RoleBinding,
	params *Params) error {

	params.OwnerRef.ApplyTo(roleBinding)
	roleBinding.RoleRef = operatorRoleBinding.DeepCopy().RoleRef
	roleBinding.Subjects = operatorRoleBinding.DeepCopy().Subjects
	return nil
}

func ReconcileOperatorServiceAccount(
	sa *corev1.ServiceAccount,
	params *Params) error {

	params.OwnerRef.ApplyTo(sa)
	util.EnsurePullSecret(sa, common.PullSecret("").Name)
	return nil
}
