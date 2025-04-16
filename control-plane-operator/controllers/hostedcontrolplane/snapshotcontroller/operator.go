package snapshotcontroller

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/snapshotcontroller/assets"
	assets2 "github.com/openshift/hypershift/support/assets"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	operatorDeployment     = assets2.MustDeployment(assets.ReadFile, "07_deployment-hypershift.yaml")
	operatorRole           = assets2.MustRole(assets.ReadFile, "05_operator_role-hypershift.yaml")
	operatorRoleBinding    = assets2.MustRoleBinding(assets.ReadFile, "06_operator_rolebinding-hypershift.yaml")
	operatorServiceAccount = assets2.MustServiceAccount(assets.ReadFile, "04_serviceaccount-hypershift.yaml")
)

func ReconcileOperatorDeployment(
	deployment *appsv1.Deployment,
	params *Params,
	platformType hyperv1.PlatformType,
	setDefaultSecurityContext bool) error {

	params.OwnerRef.ApplyTo(deployment)
	deployment.Spec = operatorDeployment.DeepCopy().Spec
	for i, container := range deployment.Spec.Template.Spec.Containers {
		switch container.Name {
		case "csi-snapshot-controller-operator":
			deployment.Spec.Template.Spec.Containers[i].Image = params.SnapshotControllerOperatorImage
		}
	}

	for i, env := range deployment.Spec.Template.Spec.Containers[0].Env {
		switch env.Name {
		case "OPERATOR_IMAGE_VERSION":
			deployment.Spec.Template.Spec.Containers[0].Env[i].Value = params.Version
		case "OPERAND_IMAGE_VERSION":
			deployment.Spec.Template.Spec.Containers[0].Env[i].Value = params.Version
		case "OPERAND_IMAGE":
			deployment.Spec.Template.Spec.Containers[0].Env[i].Value = params.SnapshotControllerImage
		}
	}

	// We set this so cluster-csi-storage-controller operator knows which User ID to run the csi-snapshot-controller pods as.
	// This is needed when these pods are run on a management cluster that is non-OpenShift such as AKS.
	if setDefaultSecurityContext {
		deployment.Spec.Template.Spec.Containers[0].Env = append(deployment.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{Name: "RUN_AS_USER", Value: "1001"})
	}

	params.DeploymentConfig.ApplyTo(deployment)
	util.AvailabilityProber(kas.InClusterKASReadyURL(platformType), params.AvailabilityProberImage, &deployment.Spec.Template.Spec, func(o *util.AvailabilityProberOpts) {
		o.KubeconfigVolumeName = "guest-kubeconfig"
		o.RequiredAPIs = []schema.GroupVersionKind{
			{Group: "operator.openshift.io", Version: "v1", Kind: "CSISnapshotController"},
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
	roleBinding.Subjects = operatorRoleBinding.DeepCopy().Subjects
	roleBinding.RoleRef = operatorRoleBinding.DeepCopy().RoleRef
	return nil
}

func ReconcileOperatorServiceAccount(
	sa *corev1.ServiceAccount,
	params *Params) error {

	params.OwnerRef.ApplyTo(sa)
	sa.AutomountServiceAccountToken = operatorServiceAccount.AutomountServiceAccountToken
	util.EnsurePullSecret(sa, common.PullSecret("").Name)
	return nil
}
