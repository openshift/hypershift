package controlplanecomponent

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/config"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type WorkloadProvider[T client.Object] interface {
	// NewObject returns a new object of the generic type. This is useful when getting/deleting the workload.
	NewObject() T
	// LoadManifest know how to load the correct workload manifest and return a workload object of the correct type.
	LoadManifest(componentName string) (T, error)

	// PodTemplateSpec knows how to extract corev1.PodTemplateSpec field from the given workload object.
	PodTemplateSpec(object T) *corev1.PodTemplateSpec
	// PodTemplateSpec knows how to extract replicas field from the given workload object.
	Replicas(object T) *int32
	// ApplyOptionsTo knows how to apply the given deploymentConfig options to the given workload object.
	// TODO(Mulham): remove all usage of deploymentConfig in cpov2 and remove this function eventually.
	ApplyOptionsTo(cpContext ControlPlaneContext, object T, oldObject T, deploymentConfig *config.DeploymentConfig)

	// IsReady returns the status, reason and message describing the readiness status of the workload object.
	IsReady(object T) (status metav1.ConditionStatus, reason string, message string)
	// IsProgressing returns the status, reason and message describing the progressing status of the workload object.
	IsProgressing(object T) (status metav1.ConditionStatus, reason string, message string)
}

var _ WorkloadProvider[*appsv1.Deployment] = &deploymentProvider{}

type deploymentProvider struct {
}

// ApplyOptionsTo implements WorkloadProvider.
func (d *deploymentProvider) ApplyOptionsTo(cpContext ControlPlaneContext, object *appsv1.Deployment, oldObject *appsv1.Deployment, deploymentConfig *config.DeploymentConfig) {
	// preserve existing resource requirements.
	existingResources := make(map[string]corev1.ResourceRequirements)
	for _, container := range oldObject.Spec.Template.Spec.Containers {
		existingResources[container.Name] = container.Resources
	}
	// preserve old label selector if it exist, this field is immutable and shouldn't be changed for the lifecycle of the component.
	if oldObject.Spec.Selector != nil {
		object.Spec.Selector = oldObject.Spec.Selector.DeepCopy()
	}

	deploymentConfig.Resources = existingResources
	deploymentConfig.ApplyTo(object)
}

func (d *deploymentProvider) NewObject() *appsv1.Deployment {
	return &appsv1.Deployment{}
}

// LoadManifest implements WorkloadProvider.
func (d *deploymentProvider) LoadManifest(componentName string) (*appsv1.Deployment, error) {
	return assets.LoadDeploymentManifest(componentName)
}

// PodTemplateSpec implements WorkloadProvider.
func (d *deploymentProvider) PodTemplateSpec(object *appsv1.Deployment) *corev1.PodTemplateSpec {
	return &object.Spec.Template
}

func (d *deploymentProvider) Replicas(object *appsv1.Deployment) *int32 {
	return object.Spec.Replicas
}

// IsReady implements WorkloadProvider.
func (d *deploymentProvider) IsReady(object *appsv1.Deployment) (status metav1.ConditionStatus, reason string, message string) {
	deploymentAvailableCond := findDeploymentCondition(object.Status.Conditions, appsv1.DeploymentAvailable)
	if deploymentAvailableCond == nil {
		status = metav1.ConditionFalse
		reason = hyperv1.NotFoundReason
		message = fmt.Sprintf("%s Deployment Available condition not found", object.Name)
		return
	}

	if deploymentAvailableCond.Status == corev1.ConditionTrue {
		status = metav1.ConditionTrue
		reason = hyperv1.AsExpectedReason
		message = fmt.Sprintf("%s Deployment is available", object.Name)
	} else {
		status = metav1.ConditionFalse
		reason = hyperv1.WaitingForAvailableReason
		message = fmt.Sprintf("%s Deployment is not available: %s", object.Name, deploymentAvailableCond.Message)
	}
	return
}

// IsProgressing implements WorkloadProvider.
func (d *deploymentProvider) IsProgressing(object *appsv1.Deployment) (status metav1.ConditionStatus, reason string, message string) {
	deploymentProgressingCond := findDeploymentCondition(object.Status.Conditions, appsv1.DeploymentProgressing)
	if deploymentProgressingCond == nil {
		status = metav1.ConditionFalse
		reason = hyperv1.NotFoundReason
		message = fmt.Sprintf("%s Deployment Progressing condition not found", object.Name)
	} else {
		// mirror deployment progressing condition
		status = metav1.ConditionStatus(deploymentProgressingCond.Status)
		reason = deploymentProgressingCond.Reason
		message = deploymentProgressingCond.Message
	}
	return
}

func findDeploymentCondition(conditions []appsv1.DeploymentCondition, conditionType appsv1.DeploymentConditionType) *appsv1.DeploymentCondition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
