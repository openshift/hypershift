package storage

import (
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/storage/assets"
	assets2 "github.com/openshift/hypershift/support/assets"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const ClusterStorageOperatorContainerName = "cluster-storage-operator"

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

	csoContainer := util.FindContainer(ClusterStorageOperatorContainerName, deployment.Spec.Template.Spec.Containers)
	if csoContainer == nil {
		return fmt.Errorf("could not find ClusterStorageOperator container for Deployment")
	}

	csoContainer.Image = params.StorageOperatorImage
	params.ImageReplacer.replaceEnvVars(csoContainer.Env)

	if os.Getenv("MANAGED_SERVICE") == hyperv1.AroHCP {
		aroHCPEnvs := []corev1.EnvVar{
			{Name: "AZURE_ADAPTER_INIT_IMAGE", Value: azureutil.AdapterInitImage},
			{Name: "AZURE_ADAPTER_SERVER_IMAGE", Value: azureutil.AdapterServerImage},
			{Name: "ARO_HCP_DISK_MI_CLIENT_ID", Value: params.AzureDiskManagedIdentity},
			{Name: "ARO_HCP_FILE_MI_CLIENT_ID", Value: params.AzureFileManagedIdentity},
			{Name: "CLIENT_ID_SECRET", Value: params.ClientIDSecret},
			{Name: "TENANT_ID", Value: params.TenantID},
		}

		csoContainer.Env = append(csoContainer.Env, aroHCPEnvs...)
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
