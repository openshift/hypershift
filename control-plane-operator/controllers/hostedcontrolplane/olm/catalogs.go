package olm

import (
	"fmt"
	"math/big"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	// TODO: Switch to k8s.io/api/batch/v1 when all management clusters at 1.21+ OR 4.8_openshift+
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"

	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

var (
	certifiedCatalogService         = MustService("assets/catalog-certified.service.yaml")
	communityCatalogService         = MustService("assets/catalog-community.service.yaml")
	redHatMarketplaceCatalogService = MustService("assets/catalog-redhat-marketplace.service.yaml")
	redHatOperatorsCatalogService   = MustService("assets/catalog-redhat-operators.service.yaml")
)

func catalogLabels() map[string]string {
	return map[string]string{"app": "catalog-operator", hyperv1.ControlPlaneComponent: "catalog-operator"}
}

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
	// The service is assigned a cluster IP when it is created.
	// This field is immutable as shown here: https://github.com/kubernetes/api/blob/62998e98c313b2ca15b1da278aa702bdd7b84cb0/core/v1/types.go#L4114-L4130
	// As such, to avoid an error when updating the object, only update the fields OLM specifies.
	sourceServiceDeepCopy := sourceService.DeepCopy()
	svc.Spec.Ports = sourceServiceDeepCopy.Spec.Ports
	svc.Spec.Type = sourceServiceDeepCopy.Spec.Type
	svc.Spec.Selector = sourceServiceDeepCopy.Spec.Selector

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

func ReconcileCertifiedOperatorsCronJob(cronJob *batchv1beta1.CronJob, ownerRef config.OwnerRef, cliImage string) error {
	return reconcileCatalogCronJob(cronJob, ownerRef, cliImage, certifiedCatalogRolloutCronJob)
}
func ReconcileCommunityOperatorsCronJob(cronJob *batchv1beta1.CronJob, ownerRef config.OwnerRef, cliImage string) error {
	return reconcileCatalogCronJob(cronJob, ownerRef, cliImage, communityCatalogRolloutCronJob)
}
func ReconcileRedHatMarketplaceOperatorsCronJob(cronJob *batchv1beta1.CronJob, ownerRef config.OwnerRef, cliImage string) error {
	return reconcileCatalogCronJob(cronJob, ownerRef, cliImage, redHatMarketplaceCatalogRolloutCronJob)
}
func ReconcileRedHatOperatorsCronJob(cronJob *batchv1beta1.CronJob, ownerRef config.OwnerRef, cliImage string) error {
	return reconcileCatalogCronJob(cronJob, ownerRef, cliImage, redHatOperatorsCatalogRolloutCronJob)
}

func reconcileCatalogCronJob(cronJob *batchv1beta1.CronJob, ownerRef config.OwnerRef, cliImage string, sourceCronJob *batchv1beta1.CronJob) error {
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

func ReconcileCatalogServiceMonitor(sm *prometheusoperatorv1.ServiceMonitor, ownerRef config.OwnerRef, clusterID string) error {
	ownerRef.ApplyTo(sm)

	sm.Spec.Selector.MatchLabels = catalogLabels()
	sm.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{
		MatchNames: []string{sm.Namespace},
	}
	targetPort := intstr.FromString("metrics")
	sm.Spec.Endpoints = []prometheusoperatorv1.Endpoint{
		{
			Interval:   "15s",
			TargetPort: &targetPort,
			Scheme:     "https",
			TLSConfig: &prometheusoperatorv1.TLSConfig{
				SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
					ServerName: "catalog-operator-metrics",
					Cert: prometheusoperatorv1.SecretOrConfigMap{
						Secret: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: manifests.MetricsClientCertSecret(sm.Namespace).Name,
							},
							Key: "tls.crt",
						},
					},
					KeySecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: manifests.MetricsClientCertSecret(sm.Namespace).Name,
						},
						Key: "tls.key",
					},
					CA: prometheusoperatorv1.SecretOrConfigMap{
						Secret: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: manifests.MetricsClientCertSecret(sm.Namespace).Name,
							},
							Key: "ca.crt",
						},
					},
				},
			},
			MetricRelabelConfigs: []*prometheusoperatorv1.RelabelConfig{
				{
					Action:       "drop",
					Regex:        "etcd_(debugging|disk|server).*",
					SourceLabels: []string{"__name__"},
				},
			},
		},
	}

	util.ApplyClusterIDLabel(&sm.Spec.Endpoints[0], clusterID)

	return nil
}
