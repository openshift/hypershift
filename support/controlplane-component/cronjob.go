package controlplanecomponent

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/config"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ WorkloadProvider[*batchv1.CronJob] = &cronJobProvider{}

type cronJobProvider struct {
}

// ApplyOptionsTo implements WorkloadProvider.
func (c *cronJobProvider) ApplyOptionsTo(cpContext ControlPlaneContext, object *batchv1.CronJob, oldObject *batchv1.CronJob, deploymentConfig *config.DeploymentConfig) {
	// preserve existing resource requirements.
	existingResources := make(map[string]corev1.ResourceRequirements)
	for _, container := range oldObject.Spec.JobTemplate.Spec.Template.Spec.Containers {
		existingResources[container.Name] = container.Resources
	}

	deploymentConfig.Resources = existingResources
	deploymentConfig.ApplyToCronJob(object)
}

func (c *cronJobProvider) NewObject() *batchv1.CronJob {
	return &batchv1.CronJob{}
}

// LoadManifest implements WorkloadProvider.
func (c *cronJobProvider) LoadManifest(componentName string) (*batchv1.CronJob, error) {
	return assets.LoadCronJobManifest(componentName)
}

// PodTemplateSpec implements WorkloadProvider.
func (c *cronJobProvider) PodTemplateSpec(object *batchv1.CronJob) *corev1.PodTemplateSpec {
	return &object.Spec.JobTemplate.Spec.Template
}

func (c *cronJobProvider) Replicas(object *batchv1.CronJob) *int32 {
	return nil
}

// IsReady implements WorkloadProvider.
func (c *cronJobProvider) IsReady(object *batchv1.CronJob) (status metav1.ConditionStatus, reason string, message string) {
	// TODO
	status = metav1.ConditionTrue
	reason = hyperv1.AsExpectedReason
	return
}

// IsProgressing implements WorkloadProvider.
func (c *cronJobProvider) IsProgressing(object *batchv1.CronJob) (status metav1.ConditionStatus, reason string, message string) {
	// TODO
	status = metav1.ConditionTrue
	reason = hyperv1.AsExpectedReason
	return
}
