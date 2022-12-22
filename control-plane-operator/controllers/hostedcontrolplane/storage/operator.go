package storage

import (
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/storage/assets"
	assets2 "github.com/openshift/hypershift/support/assets"
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
	params *Params) error {

	params.OwnerRef.ApplyTo(deployment)
	deployment.Spec = operatorDeployment.DeepCopy().Spec
	for i, container := range deployment.Spec.Template.Spec.Containers {
		switch container.Name {
		case "cluster-storage-operator":
			deployment.Spec.Template.Spec.Containers[i].Image = params.StorageOperatorImage
			params.ImageReplacer.replaceEnvVars(deployment.Spec.Template.Spec.Containers[i].Env)
		}
	}

	params.DeploymentConfig.ApplyTo(deployment)
	util.AvailabilityProber(kas.InClusterKASReadyURL(deployment.Namespace, params.APIPort), params.AvailabilityProberImage, &deployment.Spec.Template.Spec, func(o *util.AvailabilityProberOpts) {
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
