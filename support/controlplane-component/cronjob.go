package controlplanecomponent

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ WorkloadProvider[*batchv1.CronJob] = &cronJobProvider{}

type cronJobProvider struct {
}

func (c *cronJobProvider) NewObject() *batchv1.CronJob {
	return &batchv1.CronJob{}
}

// SetReplicasAndStrategy implements WorkloadProvider.
func (d *cronJobProvider) SetReplicasAndStrategy(object *batchv1.CronJob, replicas int32, isRequestServing bool) {
	// nothing to do.
}

// LoadManifest implements WorkloadProvider.
func (c *cronJobProvider) LoadManifest(componentName string) (*batchv1.CronJob, error) {
	return assets.LoadCronJobManifest(componentName)
}

// LoadManifestTemplated implements WorkloadProvider.
func (c *cronJobProvider) LoadManifestTemplated(componentName string, templateData map[string]string) (*batchv1.CronJob, error) {
	if templateData == nil {
		return c.LoadManifest(componentName)
	}
	obj, _, err := assets.LoadManifestTemplated(componentName, "cronjob.yaml", templateData)
	if err != nil {
		return nil, err
	}
	cronJob, ok := obj.(*batchv1.CronJob)
	if !ok {
		return nil, fmt.Errorf("expected CronJob but got %T", obj)
	}
	return cronJob, nil
}

// PodTemplateSpec implements WorkloadProvider.
func (c *cronJobProvider) PodTemplateSpec(object *batchv1.CronJob) *corev1.PodTemplateSpec {
	return &object.Spec.JobTemplate.Spec.Template
}

func (c *cronJobProvider) Replicas(object *batchv1.CronJob) *int32 {
	return nil
}

// IsAvailable implements WorkloadProvider.
func (c *cronJobProvider) IsAvailable(object *batchv1.CronJob) (status metav1.ConditionStatus, reason string, message string) {
	// TODO
	status = metav1.ConditionTrue
	reason = hyperv1.AsExpectedReason
	return
}

// IsReady implements WorkloadProvider.
func (c *cronJobProvider) IsReady(object *batchv1.CronJob) (status metav1.ConditionStatus, reason string, message string) {
	// TODO
	status = metav1.ConditionTrue
	reason = hyperv1.AsExpectedReason
	return
}
