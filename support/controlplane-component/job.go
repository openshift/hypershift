package controlplanecomponent

import (
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

var _ WorkloadProvider[*batchv1.Job] = &jobProvider{}

type jobProvider struct {
}

// ApplyOptionsTo implements WorkloadProvider.
func (c *jobProvider) ApplyOptionsTo(cpContext ControlPlaneContext, object *batchv1.Job, oldObject *batchv1.Job, deploymentConfig *config.DeploymentConfig) {
	// preserve existing resource requirements.
	existingResources := make(map[string]corev1.ResourceRequirements)
	for _, container := range oldObject.Spec.Template.Spec.Containers {
		existingResources[container.Name] = container.Resources
	}

	deploymentConfig.Resources = existingResources
	deploymentConfig.ApplyToJob(object)
}

func (c *jobProvider) NewObject() *batchv1.Job {
	return &batchv1.Job{}
}

// LoadManifest implements WorkloadProvider.
func (c *jobProvider) LoadManifest(componentName string) (*batchv1.Job, error) {
	return assets.LoadJobManifest(componentName)
}

// PodTemplateSpec implements WorkloadProvider.
func (c *jobProvider) PodTemplateSpec(object *batchv1.Job) *corev1.PodTemplateSpec {
	return &object.Spec.Template
}

func (c *jobProvider) Replicas(object *batchv1.Job) *int32 {
	return ptr.To(int32(1))
}

// IsReady implements WorkloadProvider.
func (c *jobProvider) IsReady(job *batchv1.Job) (status metav1.ConditionStatus, reason string, message string) {
	complete := util.FindJobCondition(job, batchv1.JobComplete)
	if complete != nil {
		return metav1.ConditionStatus(complete.Status), complete.Reason, complete.Message
	}
	failed := util.FindJobCondition(job, batchv1.JobFailed)
	if failed != nil {
		return metav1.ConditionFalse, failed.Reason, failed.Message
	}
	if job.Status.Active > 0 {
		return metav1.ConditionFalse, "JobActive", "Job is still running"
	}
	return metav1.ConditionFalse, "Unknown", "Job status unknown"
}

// IsProgressing implements WorkloadProvider.
func (c *jobProvider) IsProgressing(job *batchv1.Job) (status metav1.ConditionStatus, reason string, message string) {
	complete := util.FindJobCondition(job, batchv1.JobComplete)
	if complete != nil && complete.Status == corev1.ConditionTrue {
		return metav1.ConditionFalse, "JobComplete", "Job has completed"
	}
	failed := util.FindJobCondition(job, batchv1.JobFailed)
	if failed != nil {
		return metav1.ConditionFalse, "JobFailed", failed.Message
	}
	if job.Status.Active > 0 {
		return metav1.ConditionTrue, "JobActive", "Job is still running"
	}
	return metav1.ConditionFalse, "Unknown", "Job status unknown"
}
