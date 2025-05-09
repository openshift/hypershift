package controlplanecomponent

import (
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/util"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

var _ WorkloadProvider[*batchv1.Job] = &jobProvider{}

type jobProvider struct {
}

func (c *jobProvider) NewObject() *batchv1.Job {
	return &batchv1.Job{}
}

// SetReplicasAndStrategy implements WorkloadProvider.
func (d *jobProvider) SetReplicasAndStrategy(object *batchv1.Job, replicas int32, isRequestServing bool) {
	// nothing to do.
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

// IsAvailable implements WorkloadProvider.
func (c *jobProvider) IsAvailable(job *batchv1.Job) (status metav1.ConditionStatus, reason string, message string) {
	if job.Status.Active > 0 {
		return metav1.ConditionTrue, "JobActive", "Job is still running"
	}
	return JobCompletionStatus(job)
}

// IsReady implements WorkloadProvider.
func (c *jobProvider) IsReady(job *batchv1.Job) (status metav1.ConditionStatus, reason string, message string) {
	if job.Status.Active > 0 {
		return metav1.ConditionFalse, "JobActive", "Job is still running"
	}
	return JobCompletionStatus(job)
}

// JobCompletionStatus checks the status of a job and returns the appropriate condition status, reason, and message.
// It checks if the job is complete or failed and returns the corresponding status.
// If the job is neither complete nor failed, it returns unknown status.
func JobCompletionStatus(job *batchv1.Job) (status metav1.ConditionStatus, reason string, message string) {
	complete := util.FindJobCondition(job, batchv1.JobComplete)
	if complete != nil && complete.Status == corev1.ConditionTrue {
		return metav1.ConditionTrue, "JobComplete", "Job completed successfully"
	}

	failed := util.FindJobCondition(job, batchv1.JobFailed)
	if failed != nil && failed.Status == corev1.ConditionTrue {
		// If the job failed, we return false and the reason and message from the condition to provide more context.
		// we set default values for the reason and message if not set to avoid errors as reason and message are required fields
		// in the ControlPlaneComponent status conditions.
		if failed.Reason == "" {
			failed.Reason = "JobFailed"
		}
		if failed.Message == "" {
			failed.Message = "Job failed"
		}
		return metav1.ConditionFalse, failed.Reason, failed.Message
	}

	return metav1.ConditionFalse, "Unknown", "Job status unknown"
}
