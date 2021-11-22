package olm

import (
	"github.com/openshift/hypershift/support/config"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

var (
	olmCollectProfilesConfigMap      = MustConfigMap("assets/olm-collect-profiles.configmap.yaml")
	olmCollectProfilesCronJob        = MustCronJob("assets/olm-collect-profiles.cronjob.yaml")
	olmCollectProfilesRole           = MustRole("assets/olm-collect-profiles.role.yaml")
	olmCollectProfilesRoleBinding    = MustRoleBinding("assets/olm-collect-profiles.rolebinding.yaml")
	olmCollectProfilesSecret         = MustSecret("assets/olm-collect-profiles.secret.yaml")
	olmCollectProfilesServiceAccount = MustServiceAccount("assets/olm-collect-profiles.serviceaccount.yaml")
)

func ReconcileCollectProfilesCronJob(cronJob *batchv1beta1.CronJob, ownerRef config.OwnerRef, olmImage, namespace string) error {
	return reconcileProfilingCronJob(cronJob, ownerRef, olmImage, namespace, olmCollectProfilesCronJob)
}

func reconcileProfilingCronJob(cronJob *batchv1beta1.CronJob, ownerRef config.OwnerRef, olmImage, namespace string, sourceCronJob *batchv1beta1.CronJob) error {
	ownerRef.ApplyTo(cronJob)
	cronJob.Spec = sourceCronJob.DeepCopy().Spec
	cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Image = olmImage
	for i, arg := range cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Args {
		if arg == "OLM_NAMESPACE" {
			cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Args[i] = namespace
		}
	}
	return nil
}

func ReconcileCollectProfilesConfigMap(configMap *corev1.ConfigMap, ownerRef config.OwnerRef, olmImage, namespace string) error {
	return reconcileProfilingConfigMap(configMap, ownerRef, olmImage, namespace, olmCollectProfilesConfigMap)
}

func reconcileProfilingConfigMap(configMap *corev1.ConfigMap, ownerRef config.OwnerRef, olmImage, namespace string, sourceConfigMap *corev1.ConfigMap) error {
	ownerRef.ApplyTo(configMap)
	configMap.Data = sourceConfigMap.DeepCopy().Data
	return nil
}

func ReconcileCollectProfilesRole(role *rbacv1.Role, ownerRef config.OwnerRef, olmImage, namespace string) error {
	return reconcileProfilingRole(role, ownerRef, olmImage, namespace, olmCollectProfilesRole)
}

func reconcileProfilingRole(role *rbacv1.Role, ownerRef config.OwnerRef, olmImage, namespace string, sourceRole *rbacv1.Role) error {
	ownerRef.ApplyTo(role)
	role.Rules = sourceRole.DeepCopy().Rules
	return nil
}

func ReconcileCollectProfilesRoleBinding(roleBinding *rbacv1.RoleBinding, ownerRef config.OwnerRef, olmImage, namespace string) error {
	return reconcileProfilingRoleBinding(roleBinding, ownerRef, olmImage, namespace, olmCollectProfilesRoleBinding)
}

func reconcileProfilingRoleBinding(roleBinding *rbacv1.RoleBinding, ownerRef config.OwnerRef, olmImage, namespace string, sourceRoleBinding *rbacv1.RoleBinding) error {
	ownerRef.ApplyTo(roleBinding)
	roleBinding.RoleRef = sourceRoleBinding.DeepCopy().RoleRef
	roleBinding.Subjects = sourceRoleBinding.DeepCopy().Subjects
	return nil
}

func ReconcileCollectProfilesSecret(secret *corev1.Secret, ownerRef config.OwnerRef, olmImage, namespace string) error {
	return reconcileProfilingSecret(secret, ownerRef, olmImage, namespace, olmCollectProfilesSecret)
}

func reconcileProfilingSecret(secret *corev1.Secret, ownerRef config.OwnerRef, olmImage, namespace string, sourceSecret *corev1.Secret) error {
	ownerRef.ApplyTo(secret)
	secret.Type = sourceSecret.DeepCopy().Type
	secret.Data = sourceSecret.DeepCopy().Data
	return nil
}

func ReconcileCollectProfilesServiceAccount(serviceAccount *corev1.ServiceAccount, ownerRef config.OwnerRef, olmImage, namespace string) error {
	return reconcileProfilingServiceAccount(serviceAccount, ownerRef, olmImage, namespace, olmCollectProfilesServiceAccount)
}

func reconcileProfilingServiceAccount(serviceAccount *corev1.ServiceAccount, ownerRef config.OwnerRef, olmImage, namespace string, sourceSecret *corev1.ServiceAccount) error {
	ownerRef.ApplyTo(serviceAccount)
	return nil
}
