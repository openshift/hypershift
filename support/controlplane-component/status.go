package controlplanecomponent

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (c *controlPlaneWorkload) checkDependencies(cpContext ControlPlaneContext) ([]string, error) {
	if len(c.dependencies) == 0 {
		return nil, nil
	}

	componentsList := &hyperv1.ControlPlaneComponentList{}
	if err := cpContext.Client.List(cpContext, componentsList, client.InNamespace(cpContext.HCP.Namespace)); err != nil {
		return nil, err
	}

	unavailableDependencies := sets.New(c.dependencies...)
	for _, component := range componentsList.Items {
		if !unavailableDependencies.Has(component.Name) {
			continue
		}

		availableCondition := meta.FindStatusCondition(component.Status.Conditions, string(hyperv1.ControlPlaneComponentAvailable))
		if availableCondition != nil && availableCondition.Status == metav1.ConditionTrue {
			unavailableDependencies.Delete(component.Name)
		}
	}

	return sets.List(unavailableDependencies), nil
}

func (c *controlPlaneWorkload) reconcileComponentStatus(cpContext ControlPlaneContext, component *hyperv1.ControlPlaneComponent, unavailableDependencies []string, reconcilationError error) error {
	component.Status.Resources = []hyperv1.ComponentResource{}
	if err := assets.ForEachManifest(c.Name(), func(manifestName string) error {
		adapter, exist := c.manifestsAdapters[manifestName]
		if exist && adapter.predicate != nil && !adapter.predicate(cpContext) {
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

	if len(unavailableDependencies) > 0 {
		meta.SetStatusCondition(&component.Status.Conditions, metav1.Condition{
			Type:    string(hyperv1.ControlPlaneComponentAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.WaitingForDependenciesReason,
			Message: fmt.Sprintf("Waiting for Dependencies: %s", strings.Join(unavailableDependencies, ", ")),
		})
		return nil
	}

	if reconcilationError != nil {
		meta.SetStatusCondition(&component.Status.Conditions, metav1.Condition{
			Type:    string(hyperv1.ControlPlaneComponentAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.ReconciliationErrorReason,
			Message: reconcilationError.Error(),
		})
		return nil
	}

	// set version status only if there was no reconcilationError
	component.Status.Version = cpContext.ReleaseImageProvider.Version()

	var status metav1.ConditionStatus
	var reason, message string
	if c.workloadType == deploymentWorkloadType {
		status, reason, message = c.isDeploymentReady(cpContext)
	} else {
		status, reason, message = c.isStatefulSetReady(cpContext)
	}

	meta.SetStatusCondition(&component.Status.Conditions, metav1.Condition{
		Type:    string(hyperv1.ControlPlaneComponentAvailable),
		Status:  status,
		Reason:  reason,
		Message: message,
	})
	return nil
}

func (c *controlPlaneWorkload) isDeploymentReady(cpContext ControlPlaneContext) (status metav1.ConditionStatus, reason string, message string) {
	status = metav1.ConditionFalse
	deployment := &appsv1.Deployment{}
	if err := cpContext.Client.Get(cpContext, client.ObjectKey{Namespace: cpContext.HCP.Namespace, Name: c.name}, deployment); err != nil {
		if !apierrors.IsNotFound(err) {
			reason = "Error"
			message = err.Error()
		} else {
			reason = hyperv1.NotFoundReason
			message = fmt.Sprintf("%s Deployment not found", deployment.Name)
		}
		return
	}

	for _, cond := range deployment.Status.Conditions {
		if cond.Type == appsv1.DeploymentAvailable {
			if cond.Status == corev1.ConditionTrue {
				status = metav1.ConditionTrue
				reason = hyperv1.AsExpectedReason
				message = fmt.Sprintf("%s Deployment is available", deployment.Name)
			} else {
				reason = hyperv1.WaitingForAvailableReason
				message = fmt.Sprintf("%s Deployment is not available: %s", deployment.Name, cond.Message)
			}
			return
		}
	}

	// DeploymentAvailable condition not found
	reason = hyperv1.WaitingForAvailableReason
	message = fmt.Sprintf("%s Deployment is not available", deployment.Name)
	return
}

func (c *controlPlaneWorkload) isStatefulSetReady(cpContext ControlPlaneContext) (status metav1.ConditionStatus, reason string, message string) {
	status = metav1.ConditionFalse
	statefulSet := &appsv1.StatefulSet{}
	if err := cpContext.Client.Get(cpContext, client.ObjectKey{Namespace: cpContext.HCP.Namespace, Name: c.name}, statefulSet); err != nil {
		if !apierrors.IsNotFound(err) {
			reason = "Error"
			message = err.Error()
		} else {
			reason = hyperv1.NotFoundReason
			message = fmt.Sprintf("%s StatefulSet not found", statefulSet.Name)
		}
		return
	}

	if statefulSet.Status.ReadyReplicas >= ptr.Deref(statefulSet.Spec.Replicas, 0) {
		status = metav1.ConditionTrue
		reason = hyperv1.AsExpectedReason
		message = fmt.Sprintf("%s StatefulSet is available", statefulSet.Name)
	} else {
		reason = hyperv1.WaitingForAvailableReason
		message = fmt.Sprintf("%s StatefulSet is not available: %d/%d replicas ready", statefulSet.Name, statefulSet.Status.ReadyReplicas, *statefulSet.Spec.Replicas)
	}
	return
}
