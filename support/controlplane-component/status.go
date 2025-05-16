package controlplanecomponent

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	kubeAPIServerComponentName        = "kube-apiserver"
	etcdComponentName                 = "etcd"
	featureGateGeneratorComponetName  = "featuregate-generator"
	controlPlaneOperatorComponentName = "control-plane-operator"
)

// checkDependencies checks the availability of dependencies for a control plane component.
// It returns a list of unavailable dependencies or an error if the dependency check fails.
//
// The function performs the following steps:
// 1. Adds kube-apiserver as a dependency for all components except etcd.
// 2. Removes etcd as a dependency for unmanaged control planes, as etcd is not deployed in this case.
// 3. Ensures the component does not have a circular dependency on itself.
// 4. Checks the availability and rollout completion status of each dependency against the desired version.
func (c *controlPlaneWorkload[T]) checkDependencies(cpContext ControlPlaneContext) ([]string, error) {
	unavailableDependencies := sets.New(c.dependencies...)
	// always add kube-apiserver as a dependency, except for etcd, feature-gate-generator and control-plane-operator.
	if c.Name() != etcdComponentName && c.Name() != featureGateGeneratorComponetName && c.Name() != controlPlaneOperatorComponentName {
		unavailableDependencies.Insert(kubeAPIServerComponentName)
	}
	// we don't deploy etcd for unmanaged, therefore components can't have a dependency on it.
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
		isAvailable := availableCondition != nil && availableCondition.Status == metav1.ConditionTrue

		rolloutCompleteCondition := meta.FindStatusCondition(component.Status.Conditions, string(hyperv1.ControlPlaneComponentRolloutComplete))
		isComplete := rolloutCompleteCondition != nil && rolloutCompleteCondition.Status == metav1.ConditionTrue && component.Status.Version == desiredVersion

		if isAvailable && isComplete {
			unavailableDependencies.Delete(component.Name)
		}
	}

	return sets.List(unavailableDependencies), nil
}

func (c *controlPlaneWorkload[T]) reconcileComponentStatus(cpContext ControlPlaneContext, component *hyperv1.ControlPlaneComponent, unavailableDependencies []string, reconcilationError error) error {
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

	if c.serviceAccountKubeConfigOpts != nil {
		_, disablePKIReconciliationAnnotation := cpContext.HCP.Annotations[hyperv1.DisablePKIReconciliationAnnotation]
		if !disablePKIReconciliationAnnotation {
			component.Status.Resources = append(component.Status.Resources, hyperv1.ComponentResource{
				Kind:  "Secret",
				Group: corev1.GroupName,
				Name:  c.serviceAccountKubeconfigSecretName(),
			})
		}
	}

	if len(unavailableDependencies) == 0 && reconcilationError == nil {
		// set version status only if reconciliation is not blocked on dependencies and if there was no reconciliation error.
		component.Status.Version = cpContext.ReleaseImageProvider.Version()
	}

	c.setAvailableCondition(cpContext, &component.Status.Conditions)
	c.setRolloutCompleteCondition(cpContext, &component.Status.Conditions, unavailableDependencies, reconcilationError)
	return nil
}

func (c *controlPlaneWorkload[T]) setAvailableCondition(cpContext ControlPlaneContext, conditions *[]metav1.Condition) {
	workloadObject := c.workloadProvider.NewObject()
	if err := cpContext.Client.Get(cpContext, client.ObjectKey{Namespace: cpContext.HCP.Namespace, Name: c.name}, workloadObject); err != nil {
		meta.SetStatusCondition(conditions, metav1.Condition{
			Type:    string(hyperv1.ControlPlaneComponentAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  string(apierrors.ReasonForError(err)),
			Message: err.Error(),
		})
		return
	}

	status, reason, message := c.workloadProvider.IsAvailable(workloadObject)
	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:    string(hyperv1.ControlPlaneComponentAvailable),
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

func (c *controlPlaneWorkload[T]) setRolloutCompleteCondition(cpContext ControlPlaneContext, conditions *[]metav1.Condition, unavailableDependencies []string, reconcilationError error) {
	if len(unavailableDependencies) > 0 {
		meta.SetStatusCondition(conditions, metav1.Condition{
			Type:    string(hyperv1.ControlPlaneComponentRolloutComplete),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.WaitingForDependenciesReason,
			Message: fmt.Sprintf("Waiting for Dependencies: %s", strings.Join(unavailableDependencies, ", ")),
		})
		return
	}

	if reconcilationError != nil {
		meta.SetStatusCondition(conditions, metav1.Condition{
			Type:    string(hyperv1.ControlPlaneComponentRolloutComplete),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.ReconciliationErrorReason,
			Message: reconcilationError.Error(),
		})
		return
	}

	workloadObject := c.workloadProvider.NewObject()
	if err := cpContext.Client.Get(cpContext, client.ObjectKey{Namespace: cpContext.HCP.Namespace, Name: c.name}, workloadObject); err != nil {
		meta.SetStatusCondition(conditions, metav1.Condition{
			Type:    string(hyperv1.ControlPlaneComponentRolloutComplete),
			Status:  metav1.ConditionFalse,
			Reason:  string(apierrors.ReasonForError(err)),
			Message: err.Error(),
		})
		return
	}

	status, reason, message := c.workloadProvider.IsReady(workloadObject)
	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:    string(hyperv1.ControlPlaneComponentRolloutComplete),
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}
