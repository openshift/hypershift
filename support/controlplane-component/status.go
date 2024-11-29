package controlplanecomponent

import (
	"context"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	kubeAPIServerComponentName = "kube-apiserver"
	etcdComponentName          = "etcd"
)

func (c *controlPlaneWorkload) checkDependencies(cpContext ControlPlaneContext) ([]string, error) {
	unavailableDependencies := sets.New(c.dependencies...)
	// always add kube-apiserver as a dependency, except for etcd.
	if c.Name() != etcdComponentName {
		unavailableDependencies.Insert(kubeAPIServerComponentName)
	}
	// we don't deploy etcd for unmanaged, therfore components can't have a dependecny on it.
	if cpContext.HCP.Spec.Etcd.ManagementType != hyperv1.Managed && unavailableDependencies.Has(etcdComponentName) {
		unavailableDependencies.Delete(etcdComponentName)
	}
	// make sure component's don't have a circular dependency.
	if unavailableDependencies.Has(c.Name()) {
		unavailableDependencies.Delete(c.Name())
	}

	if len(unavailableDependencies) == 0 {
		return nil, nil
	}

	componentsList := &hyperv1.ControlPlaneComponentList{}
	if err := cpContext.Client.List(cpContext, componentsList, client.InNamespace(cpContext.HCP.Namespace)); err != nil {
		return nil, err
	}

	desiredVersion := cpContext.ReleaseImageProvider.Version()
	for _, component := range componentsList.Items {
		if !unavailableDependencies.Has(component.Name) {
			continue
		}

		availableCondition := meta.FindStatusCondition(component.Status.Conditions, string(hyperv1.ControlPlaneComponentAvailable))
		if availableCondition != nil && availableCondition.Status == metav1.ConditionTrue && component.Status.Version == desiredVersion {
			unavailableDependencies.Delete(component.Name)
		}
	}

	return sets.List(unavailableDependencies), nil
}

func (c *controlPlaneWorkload) reconcileComponentStatus(cpContext ControlPlaneContext, component *hyperv1.ControlPlaneComponent, unavailableDependencies []string, reconcilationError error) error {
	workloadContrext := cpContext.workloadContext()
	component.Status.Resources = []hyperv1.ComponentResource{}
	if err := assets.ForEachManifest(c.Name(), func(manifestName string) error {
		adapter, exist := c.manifestsAdapters[manifestName]
		if exist && adapter.predicate != nil && !adapter.predicate(workloadContrext) {
			return nil
		}

		obj, gvk, err := assets.LoadManifest(c.name, manifestName)
		if err != nil {
			return err
		}

		component.Status.Resources = append(component.Status.Resources, hyperv1.ComponentResource{
			Kind:  gvk.Kind,
			Group: gvk.Group,
			Name:  obj.GetName(),
		})

		return nil
	}); err != nil {
		return err
	}

	if len(unavailableDependencies) == 0 && reconcilationError == nil {
		// set version status only if reconcilation is not blocked on dependencies and if there was no reconcilation error.
		component.Status.Version = cpContext.ReleaseImageProvider.Version()
	}

	c.setAvailableCondition(cpContext, &component.Status.Conditions)
	c.setProgressingCondition(cpContext, &component.Status.Conditions, unavailableDependencies, reconcilationError)
	return nil
}

func (c *controlPlaneWorkload) setAvailableCondition(cpContext ControlPlaneContext, conditions *[]metav1.Condition) {
	workloadObject, err := c.getWorkloadObject(cpContext)
	if err != nil {
		meta.SetStatusCondition(conditions, metav1.Condition{
			Type:    string(hyperv1.ControlPlaneComponentAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  string(apierrors.ReasonForError(err)),
			Message: err.Error(),
		})
		return
	}

	var status metav1.ConditionStatus
	var reason, message string
	if c.workloadType == deploymentWorkloadType {
		status, reason, message = isDeploymentReady(workloadObject.(*appsv1.Deployment))
	} else {
		status, reason, message = isStatefulSetReady(workloadObject.(*appsv1.StatefulSet))
	}

	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:    string(hyperv1.ControlPlaneComponentAvailable),
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

func (c *controlPlaneWorkload) setProgressingCondition(cpContext ControlPlaneContext, conditions *[]metav1.Condition, unavailableDependencies []string, reconcilationError error) {
	if len(unavailableDependencies) > 0 {
		meta.SetStatusCondition(conditions, metav1.Condition{
			Type:    string(hyperv1.ControlPlaneComponentProgressing),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.WaitingForDependenciesReason,
			Message: fmt.Sprintf("Waiting for Dependencies: %s", strings.Join(unavailableDependencies, ", ")),
		})
		return
	}

	if reconcilationError != nil {
		meta.SetStatusCondition(conditions, metav1.Condition{
			Type:    string(hyperv1.ControlPlaneComponentProgressing),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.ReconciliationErrorReason,
			Message: reconcilationError.Error(),
		})
		return
	}

	workloadObject, err := c.getWorkloadObject(cpContext)
	if err != nil {
		meta.SetStatusCondition(conditions, metav1.Condition{
			Type:    string(hyperv1.ControlPlaneComponentProgressing),
			Status:  metav1.ConditionFalse,
			Reason:  string(apierrors.ReasonForError(err)),
			Message: err.Error(),
		})
		return
	}

	var status metav1.ConditionStatus
	var reason, message string
	if c.workloadType == deploymentWorkloadType {
		status, reason, message = isDeploymentProgressing(workloadObject.(*appsv1.Deployment))
	} else {
		status, reason, message = isStatefulSetProgressing(workloadObject.(*appsv1.StatefulSet))
	}

	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:    string(hyperv1.ControlPlaneComponentProgressing),
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

func isDeploymentReady(deployment *appsv1.Deployment) (status metav1.ConditionStatus, reason string, message string) {
	deploymentAvailableCond := findDeploymentCondition(deployment.Status.Conditions, appsv1.DeploymentAvailable)
	if deploymentAvailableCond == nil {
		status = metav1.ConditionFalse
		reason = hyperv1.NotFoundReason
		message = fmt.Sprintf("%s Deployment Available condition not found", deployment.Name)
		return
	}

	if deploymentAvailableCond.Status == corev1.ConditionTrue {
		status = metav1.ConditionTrue
		reason = hyperv1.AsExpectedReason
		message = fmt.Sprintf("%s Deployment is available", deployment.Name)
	} else {
		status = metav1.ConditionFalse
		reason = hyperv1.WaitingForAvailableReason
		message = fmt.Sprintf("%s Deployment is not available: %s", deployment.Name, deploymentAvailableCond.Message)
	}
	return
}

func isDeploymentProgressing(deployment *appsv1.Deployment) (status metav1.ConditionStatus, reason string, message string) {
	deploymentProgressingCond := findDeploymentCondition(deployment.Status.Conditions, appsv1.DeploymentProgressing)
	if deploymentProgressingCond == nil {
		status = metav1.ConditionFalse
		reason = hyperv1.NotFoundReason
		message = fmt.Sprintf("%s Deployment Progressing condition not found", deployment.Name)
	} else {
		// mirror deployment progressing condition
		status = metav1.ConditionStatus(deploymentProgressingCond.Status)
		reason = deploymentProgressingCond.Reason
		message = deploymentProgressingCond.Message
	}
	return
}

func isStatefulSetReady(statefulSet *appsv1.StatefulSet) (status metav1.ConditionStatus, reason string, message string) {
	// statefulSet is considered available if at least 1 replica is available.
	if ptr.Deref(statefulSet.Spec.Replicas, 0) == 0 || statefulSet.Status.AvailableReplicas > 0 {
		status = metav1.ConditionTrue
		reason = hyperv1.AsExpectedReason
		message = fmt.Sprintf("%s StatefulSet is available", statefulSet.Name)
	} else {
		status = metav1.ConditionFalse
		reason = hyperv1.WaitingForAvailableReason
		message = fmt.Sprintf("%s StatefulSet is not available: %d/%d replicas ready", statefulSet.Name, statefulSet.Status.ReadyReplicas, *statefulSet.Spec.Replicas)
	}
	return
}

func isStatefulSetProgressing(statefulSet *appsv1.StatefulSet) (status metav1.ConditionStatus, reason string, message string) {
	if util.IsStatefulSetReady(context.TODO(), statefulSet) {
		status = metav1.ConditionFalse
		reason = hyperv1.AsExpectedReason
	} else {
		status = metav1.ConditionTrue
		reason = hyperv1.WaitingForAvailableReason
		message = fmt.Sprintf("%s StatefulSet progressing: %d/%d replicas ready", statefulSet.Name, statefulSet.Status.ReadyReplicas, *statefulSet.Spec.Replicas)
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

func (c *controlPlaneWorkload) getWorkloadObject(cpContext ControlPlaneContext) (client.Object, error) {
	var obj client.Object
	switch c.workloadType {
	case deploymentWorkloadType:
		obj = &appsv1.Deployment{}
	case statefulSetWorkloadType:
		obj = &appsv1.StatefulSet{}
	}

	if err := cpContext.Client.Get(cpContext, client.ObjectKey{Namespace: cpContext.HCP.Namespace, Name: c.name}, obj); err != nil {
		return nil, err
	}
	return obj, nil
}
