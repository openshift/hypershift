package olm

import (
	"fmt"
	"math/big"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

var (
	certifiedOperatorsCatalogSource         = MustAsset("assets/catalog-certified-operators-catalogsource.yaml")
	communityOperatorsCatalogSource         = MustAsset("assets/catalog-community-operators-catalogsource.yaml")
	redHatMarketplaceOperatorsCatalogSource = MustAsset("assets/catalog-redhat-marketplace-catalogsource.yaml")
	redHatOperatorsCatalogSource            = MustAsset("assets/catalog-redhat-operators-catalogsource.yaml")
)

func ReconcileCertifiedOperatorsCatalogSourceWorkerManifest(cm *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	return reconcileCatalogSourceWorkerManifest(cm, ownerRef, certifiedOperatorsCatalogSource)
}

func ReconcileCommunityOperatorsCatalogSourceWorkerManifest(cm *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	return reconcileCatalogSourceWorkerManifest(cm, ownerRef, communityOperatorsCatalogSource)
}

func ReconcileRedHatMarketplaceOperatorsCatalogSourceWorkerManifest(cm *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	return reconcileCatalogSourceWorkerManifest(cm, ownerRef, redHatMarketplaceOperatorsCatalogSource)
}

func ReconcileRedHatOperatorsCatalogSourceWorkerManifest(cm *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	return reconcileCatalogSourceWorkerManifest(cm, ownerRef, redHatOperatorsCatalogSource)
}

func reconcileCatalogSourceWorkerManifest(cm *corev1.ConfigMap, ownerRef config.OwnerRef, catalogSourceBytes []byte) error {
	ownerRef.ApplyTo(cm)
	util.ReconcileWorkerManifestString(cm, string(catalogSourceBytes))
	return nil
}

var (
	certifiedCatalogService         = MustService("assets/catalog-certified.service.yaml")
	communityCatalogService         = MustService("assets/catalog-community.service.yaml")
	redHatMarketplaceCatalogService = MustService("assets/catalog-redhat-marketplace.service.yaml")
	redHatOperatorsCatalogService   = MustService("assets/catalog-redhat-operators.service.yaml")
)

func ReconcileCertifiedOperatorsService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	return reconcileCatalogService(svc, ownerRef, certifiedCatalogService)
}

func ReconcileCommunityOperatorsService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	return reconcileCatalogService(svc, ownerRef, communityCatalogService)
}

func ReconcileRedHatMarketplaceOperatorsService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	return reconcileCatalogService(svc, ownerRef, redHatMarketplaceCatalogService)
}

func ReconcileRedHatOperatorsService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	return reconcileCatalogService(svc, ownerRef, redHatOperatorsCatalogService)
}

func reconcileCatalogService(svc *corev1.Service, ownerRef config.OwnerRef, sourceService *corev1.Service) error {
	ownerRef.ApplyTo(svc)
	svc.Spec = sourceService.DeepCopy().Spec
	return nil
}

var (
	certifiedCatalogDeployment         = MustDeployment("assets/catalog-certified.deployment.yaml")
	communityCatalogDeployment         = MustDeployment("assets/catalog-community.deployment.yaml")
	redHatMarketplaceCatalogDeployment = MustDeployment("assets/catalog-redhat-marketplace.deployment.yaml")
	redHatOperatorsCatalogDeployment   = MustDeployment("assets/catalog-redhat-operators.deployment.yaml")
)

func ReconcileCertifiedOperatorsDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, dc config.DeploymentConfig) error {
	return reconcileCatalogDeployment(deployment, ownerRef, dc, certifiedCatalogDeployment)
}

func ReconcileCommunityOperatorsDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, dc config.DeploymentConfig) error {
	return reconcileCatalogDeployment(deployment, ownerRef, dc, communityCatalogDeployment)
}

func ReconcileRedHatMarketplaceOperatorsDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, dc config.DeploymentConfig) error {
	return reconcileCatalogDeployment(deployment, ownerRef, dc, redHatMarketplaceCatalogDeployment)
}

func ReconcileRedHatOperatorsDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, dc config.DeploymentConfig) error {
	return reconcileCatalogDeployment(deployment, ownerRef, dc, redHatOperatorsCatalogDeployment)
}

func reconcileCatalogDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, dc config.DeploymentConfig, sourceDeployment *appsv1.Deployment) error {
	ownerRef.ApplyTo(deployment)
	deployment.Spec = sourceDeployment.DeepCopy().Spec
	dc.ApplyTo(deployment)
	return nil
}

var (
	certifiedCatalogRolloutCronJob         = MustCronJob("assets/catalog-certified-rollout.cronjob.yaml")
	communityCatalogRolloutCronJob         = MustCronJob("assets/catalog-community-rollout.cronjob.yaml")
	redHatMarketplaceCatalogRolloutCronJob = MustCronJob("assets/catalog-redhat-marketplace-rollout.cronjob.yaml")
	redHatOperatorsCatalogRolloutCronJob   = MustCronJob("assets/catalog-redhat-operators-rollout.cronjob.yaml")
)

func ReconcileCertifiedOperatorsCronJob(cronJob *batchv1.CronJob, ownerRef config.OwnerRef, cliImage string) error {
	return reconcileCatalogCronJob(cronJob, ownerRef, cliImage, certifiedCatalogRolloutCronJob)
}
func ReconcileCommunityOperatorsCronJob(cronJob *batchv1.CronJob, ownerRef config.OwnerRef, cliImage string) error {
	return reconcileCatalogCronJob(cronJob, ownerRef, cliImage, communityCatalogRolloutCronJob)
}
func ReconcileRedHatMarketplaceOperatorsCronJob(cronJob *batchv1.CronJob, ownerRef config.OwnerRef, cliImage string) error {
	return reconcileCatalogCronJob(cronJob, ownerRef, cliImage, redHatMarketplaceCatalogRolloutCronJob)
}
func ReconcileRedHatOperatorsCronJob(cronJob *batchv1.CronJob, ownerRef config.OwnerRef, cliImage string) error {
	return reconcileCatalogCronJob(cronJob, ownerRef, cliImage, redHatOperatorsCatalogRolloutCronJob)
}

func reconcileCatalogCronJob(cronJob *batchv1.CronJob, ownerRef config.OwnerRef, cliImage string, sourceCronJob *batchv1.CronJob) error {
	ownerRef.ApplyTo(cronJob)
	cronJob.Spec = sourceCronJob.DeepCopy().Spec
	cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Image = cliImage
	cronJob.Spec.Schedule = generateModularDailyCronSchedule([]byte(cronJob.Namespace))
	return nil
}

// generateModularDailyCronSchedule returns a daily crontab schedule
// where, given a is input's integer representation, the minute is a % 60
// and hour is a % 24.
func generateModularDailyCronSchedule(input []byte) string {
	a := big.NewInt(0).SetBytes(input)
	var hi, mi big.Int
	m := mi.Mod(a, big.NewInt(60))
	h := hi.Mod(a, big.NewInt(24))
	return fmt.Sprintf("%d %d * * *", m.Int64(), h.Int64())
}

var (
	catalogRolloutRole        = MustRole("assets/catalog-rollout.role.yaml")
	catalogRolloutRoleBinding = MustRoleBinding("assets/catalog-rollout.rolebinding.yaml")
)

func ReconcileCatalogRolloutServiceAccount(sa *corev1.ServiceAccount, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(sa)
	return nil
}

func ReconcileCatalogRolloutRole(role *rbacv1.Role, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(role)
	role.Rules = catalogRolloutRole.DeepCopy().Rules
	return nil
}

func ReconcileCatalogRolloutRoleBinding(roleBinding *rbacv1.RoleBinding, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(roleBinding)
	roleBinding.RoleRef = catalogRolloutRoleBinding.DeepCopy().RoleRef
	roleBinding.Subjects = catalogRolloutRoleBinding.DeepCopy().Subjects
	return nil
}
