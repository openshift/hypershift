package olm

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/support/assets"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

var (
	olmCollectProfilesConfigMap   = assets.MustConfigMap(content.ReadFile, "assets/olm-collect-profiles.configmap.yaml")
	olmCollectProfilesCronJob     = assets.MustCronJob(content.ReadFile, "assets/olm-collect-profiles.cronjob.yaml")
	olmCollectProfilesRole        = assets.MustRole(content.ReadFile, "assets/olm-collect-profiles.role.yaml")
	olmCollectProfilesRoleBinding = assets.MustRoleBinding(content.ReadFile, "assets/olm-collect-profiles.rolebinding.yaml")
	olmCollectProfilesSecret      = assets.MustSecret(content.ReadFile, "assets/olm-collect-profiles.secret.yaml")
)

func ReconcileCollectProfilesCronJob(cronJob *batchv1.CronJob, ownerRef config.OwnerRef, olmImage string, hcp *hyperv1.HostedControlPlane) {
	mockDC := config.DeploymentConfig{}
	mockDC.SetDefaults(hcp, nil, nil)

	ownerRef.ApplyTo(cronJob)
	cronJob.Spec = olmCollectProfilesCronJob.DeepCopy().Spec
	cronJob.Spec.JobTemplate.Spec.Template.ObjectMeta.Labels = map[string]string{
		config.NeedManagementKASAccessLabel: "true",
	}
	cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Image = olmImage
	for i, arg := range cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Args {
		if arg == "OLM_NAMESPACE" {
			cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Args[i] = hcp.Namespace
		}
	}
	cronJob.Spec.JobTemplate.Spec.Template.Spec.Tolerations = mockDC.Scheduling.Tolerations
	cronJob.Spec.JobTemplate.Spec.Template.Spec.Affinity = mockDC.Scheduling.Affinity
	cronJob.Spec.Schedule = generateModularDailyCronSchedule([]byte(cronJob.Namespace))
}

func ReconcileCollectProfilesConfigMap(configMap *corev1.ConfigMap, ownerRef config.OwnerRef) {
	ownerRef.ApplyTo(configMap)
	configMap.Data = olmCollectProfilesConfigMap.DeepCopy().Data
}

func ReconcileCollectProfilesRole(role *rbacv1.Role, ownerRef config.OwnerRef) {
	ownerRef.ApplyTo(role)
	role.Rules = olmCollectProfilesRole.DeepCopy().Rules
}

func ReconcileCollectProfilesRoleBinding(roleBinding *rbacv1.RoleBinding, ownerRef config.OwnerRef) {
	ownerRef.ApplyTo(roleBinding)
	roleBinding.RoleRef = olmCollectProfilesRoleBinding.DeepCopy().RoleRef
	roleBinding.Subjects = olmCollectProfilesRoleBinding.DeepCopy().Subjects
}

func ReconcileCollectProfilesSecret(secret *corev1.Secret, ownerRef config.OwnerRef) {
	ownerRef.ApplyTo(secret)
	secret.Type = olmCollectProfilesSecret.Type
	secret.Data = olmCollectProfilesSecret.DeepCopy().Data
}

func ReconcileCollectProfilesServiceAccount(serviceAccount *corev1.ServiceAccount, ownerRef config.OwnerRef) {
	ownerRef.ApplyTo(serviceAccount)
	util.EnsurePullSecret(serviceAccount, common.PullSecret("").Name)
}
