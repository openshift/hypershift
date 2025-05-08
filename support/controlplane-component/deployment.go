package controlplanecomponent

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

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
	// SetReplicasAndStrategy knows how to set a strategy and replicas on the given workload object.
	SetReplicasAndStrategy(object T, replicas int32, isRequestServing bool)

	// IsAvailable returns the status, reason and message describing the availability status of the workload object.
	IsAvailable(object T) (status metav1.ConditionStatus, reason string, message string)
	// IsReady returns the status, reason and message describing the readiness status of the workload object.
	IsReady(object T) (status metav1.ConditionStatus, reason string, message string)
}

var _ WorkloadProvider[*appsv1.Deployment] = &deploymentProvider{}

type deploymentProvider struct {
}

func (d *deploymentProvider) NewObject() *appsv1.Deployment {
	return &appsv1.Deployment{}
}

// SetReplicasAndStrategy implements WorkloadProvider.
func (d *deploymentProvider) SetReplicasAndStrategy(object *appsv1.Deployment, replicas int32, isRequestServing bool) {
	object.Spec.Replicas = ptr.To(replicas)
	object.Spec.RevisionHistoryLimit = ptr.To[int32](2)

	// there are three standard cases currently with hypershift: HA mode where there are 3 replicas spread across
	// zones, HA mode with 2 replicas, and then non ha with one replica. When only 3 zones are available you need
	// to be able to set maxUnavailable in order to progress the rollout. However, you do not want to set that in
	// the single replica case because it will result in downtime.
	if replicas > 1 {
		maxSurge := intstr.FromInt(1)
		maxUnavailable := intstr.FromInt(0)
		if isRequestServing {
			maxUnavailable = intstr.FromInt(1)
		}
		if replicas > 2 {
			maxSurge = intstr.FromInt(0)
			maxUnavailable = intstr.FromInt(1)
		}
		if object.Spec.Strategy.RollingUpdate == nil {
			object.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{}
			object.Spec.Strategy.Type = appsv1.RollingUpdateDeploymentStrategyType
		}
		object.Spec.Strategy.RollingUpdate.MaxSurge = &maxSurge
		object.Spec.Strategy.RollingUpdate.MaxUnavailable = &maxUnavailable
	}
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

// IsAvailable implements WorkloadProvider.
func (d *deploymentProvider) IsAvailable(object *appsv1.Deployment) (status metav1.ConditionStatus, reason string, message string) {
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
		message = fmt.Sprintf("Deployment %s is available", object.Name)
	} else {
		status = metav1.ConditionFalse
		reason = hyperv1.WaitingForAvailableReason
		message = fmt.Sprintf("Deployment %s is not available: %s", object.Name, deploymentAvailableCond.Message)
	}
	return
}

// IsReady implements WorkloadProvider.
func (d *deploymentProvider) IsReady(object *appsv1.Deployment) (status metav1.ConditionStatus, reason string, message string) {
	if util.IsDeploymentReady(context.TODO(), object) {
		status = metav1.ConditionTrue
		reason = hyperv1.AsExpectedReason
		message = fmt.Sprintf("Deployment %s successfully rolled out", object.Name)
	} else {
		status = metav1.ConditionFalse
		reason = "WaitingForRolloutComplete"
		message = fmt.Sprintf("Waiting for deployment %s rollout to finish: %d out of %d new replicas have been updated", object.Name, object.Status.UpdatedReplicas, *object.Spec.Replicas)
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
