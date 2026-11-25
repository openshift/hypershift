package controlplanecomponent

import (
	"errors"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

const (
	kubeAPIServerComponentName        = "kube-apiserver"
	etcdComponentName                 = "etcd"
	featureGateGeneratorComponetName  = "featuregate-generator"
	controlPlaneOperatorComponentName = "control-plane-operator"
	capiProviderComponentName         = "capi-provider"
)

// isEtcdComponent returns true for the default etcd component and any etcd shard
// component (e.g. "etcd-events"). It avoids matching unrelated components whose
// names happen to start with "etcd" (e.g. a hypothetical "etcd-backup").
func isEtcdComponent(name string) bool {
	return name == etcdComponentName || strings.HasPrefix(name, etcdComponentName+"-")
}

var (
	// componentsExcludedFromKubeAPIServerDependency lists components that must
	// not wait for the kube-apiserver to roll out: the components the
	// kube-apiserver itself needs (etcd, featuregate-generator), the operator
	// running the rollout, and components that historically started before it.
	// capi-provider is intentionally not in this set: its deployment mounts the
	// service-network-admin-kubeconfig secret, which the kube-apiserver
	// component applies, so the deployment must wait for the kube-apiserver
	// rollout to avoid FailedMount events during initial cluster bootstrap.
	// On releases that may run a v1 CPO (pre-4.20), the dependency is instead
	// skipped via componentsDependingOnKubeAPIServerOnlyOnV2CPO below, because
	// a v1 CPO never publishes a kube-apiserver ControlPlaneComponent.
	componentsExcludedFromKubeAPIServerDependency = sets.New(
		etcdComponentName,
		featureGateGeneratorComponetName,
		controlPlaneOperatorComponentName,
		"cluster-api",
		"karpenter-operator",
		"karpenter",
		"router",
		"ignition-server",
		"ignition-server-proxy",
		// Allow External OIDC webhook resources, including the CA needed by KAS, to reconcile before KAS.
		"external-oidc-webhook",
	)

	// minV2OnlyCPORelease is the first release whose CPO only ships the v2
	// code path. Releases before it may run a v1 CPO (when the HO no longer
	// sets the v2 opt-in), and a v1 CPO never publishes a kube-apiserver
	// ControlPlaneComponent.
	minV2OnlyCPORelease = semver.MustParse("4.20.0")

	// componentsDependingOnKubeAPIServerOnlyOnV2CPO lists components whose
	// kube-apiserver dependency only applies when the control plane is
	// guaranteed to be reconciled by a v2 CPO (release 4.20 and later). For
	// older releases (4.14-4.19), which may run a v1 CPO, waiting for a
	// kube-apiserver ControlPlaneComponent would block the component forever.
	// capi-provider is in this set because it is reconciled by the HO through
	// the v2 component framework unconditionally, regardless of which CPO
	// code path the HostedCluster runs.
	componentsDependingOnKubeAPIServerOnlyOnV2CPO = sets.New(
		capiProviderComponentName,
	)
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
	// always add kube-apiserver as a dependency, except execluded components.
	dependsOnKubeAPIServer := !componentsExcludedFromKubeAPIServerDependency.Has(c.Name()) && !isEtcdComponent(c.Name())
	if dependsOnKubeAPIServer && componentsDependingOnKubeAPIServerOnlyOnV2CPO.Has(c.Name()) {
		// A v1 CPO never publishes a kube-apiserver ControlPlaneComponent, so
		// for releases that may run one the dependency must be skipped.
		// On parse errors, skip the dependency as well: a spurious FailedMount
		// is transient and self-healing, while a dependency that can never be
		// satisfied blocks the component forever.
		version, err := semver.ParseTolerant(cpContext.ReleaseImageProvider.Version())
		// Ignore the pre-release so that 4.20 ec/rc/nightly payloads, which
		// already ship a v2-only CPO, keep the dependency.
		version.Pre = nil
		if err != nil || version.LT(minV2OnlyCPORelease) {
			dependsOnKubeAPIServer = false
		}
	}
	if dependsOnKubeAPIServer {
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

// checkPreconditions returns the list of preconditions whose HCP condition is not
// currently True, across all enforced precondition groups. For each group, the
// group's gate decides whether its conditions are enforced on this reconcile: a
// gate returning false skips the group (treats its conditions as met), a gate
// returning true enforces them, and a gate error aborts the check. A component with
// unmet preconditions is not reconciled and reports RolloutComplete=False until
// every enforced precondition's condition becomes True.
func (c *controlPlaneWorkload[T]) checkPreconditions(cpContext ControlPlaneContext) ([]hyperv1.ConditionType, error) {
	workloadContext := cpContext.workloadContext()
	var unmet []hyperv1.ConditionType
	for _, group := range c.preconditionGroups {
		// A nil gate means the group is always enforced.
		if group.gate != nil {
			enforce, err := group.gate(workloadContext)
			if err != nil {
				return nil, err
			}
			if !enforce {
				continue
			}
		}
		for _, p := range group.conditions {
			condition := meta.FindStatusCondition(cpContext.HCP.Status.Conditions, string(p))
			if condition == nil || condition.Status != metav1.ConditionTrue {
				unmet = append(unmet, p)
			}
		}
	}
	return unmet, nil
}

func (c *controlPlaneWorkload[T]) reconcileComponentStatus(cpContext ControlPlaneContext, component *hyperv1.ControlPlaneComponent, unmetPreconditions []hyperv1.ConditionType, unavailableDependencies []string, reconcilationError error) error {
	workloadContrext := cpContext.workloadContext()
	component.Status.Resources = []hyperv1.ComponentResource{}
	if err := assets.ForEachManifest(c.AssetDirName(), func(manifestName string) error {
		adapter, exist := c.manifestsAdapters[manifestName]
		if exist && adapter.predicate != nil && !adapter.predicate(workloadContrext) {
			return nil
		}

		obj, gvk, err := c.loadManifest(manifestName)
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

	var diagReason, diagMessage string
	workloadObject := c.workloadProvider.NewObject()
	if err := cpContext.Client.Get(cpContext, client.ObjectKey{Namespace: cpContext.HCP.Namespace, Name: c.name}, workloadObject); err == nil {
		diagReason, diagMessage = podspec.DiagnoseWorkloadPodFailure(cpContext, cpContext.Client, workloadObject)
	}
	c.setAvailableCondition(cpContext, &component.Status.Conditions, diagReason, diagMessage)
	rolloutStatus := c.setRolloutCompleteCondition(cpContext, &component.Status.Conditions, unmetPreconditions, unavailableDependencies, reconcilationError, diagReason, diagMessage)
	if rolloutStatus == metav1.ConditionTrue {
		// set the version only if the rollout is complete
		component.Status.Version = cpContext.ReleaseImageProvider.Version()
	}

	component.Status.ObservedGeneration = cpContext.HCP.Generation

	return nil
}

func (c *controlPlaneWorkload[T]) setAvailableCondition(cpContext ControlPlaneContext, conditions *[]metav1.Condition, diagReason, diagMessage string) {
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
	if status == metav1.ConditionFalse && diagReason != "" {
		reason = diagReason
		message = fmt.Sprintf("%s; %s", message, diagMessage)
	}
	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:    string(hyperv1.ControlPlaneComponentAvailable),
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

func (c *controlPlaneWorkload[T]) setRolloutCompleteCondition(cpContext ControlPlaneContext, conditions *[]metav1.Condition, unmetPreconditions []hyperv1.ConditionType, unavailableDependencies []string, reconcilationError error, diagReason, diagMessage string) metav1.ConditionStatus {
	if len(unmetPreconditions) > 0 {
		preconditionNames := make([]string, 0, len(unmetPreconditions))
		for _, precondition := range unmetPreconditions {
			preconditionNames = append(preconditionNames, string(precondition))
		}
		meta.SetStatusCondition(conditions, metav1.Condition{
			Type:    string(hyperv1.ControlPlaneComponentRolloutComplete),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.WaitingForPreconditionsReason,
			Message: fmt.Sprintf("Waiting for HCP conditions: %s", strings.Join(preconditionNames, ", ")),
		})
		return metav1.ConditionFalse
	}

	if len(unavailableDependencies) > 0 {
		meta.SetStatusCondition(conditions, metav1.Condition{
			Type:    string(hyperv1.ControlPlaneComponentRolloutComplete),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.WaitingForDependenciesReason,
			Message: fmt.Sprintf("Waiting for Dependencies: %s", strings.Join(unavailableDependencies, ", ")),
		})
		return metav1.ConditionFalse
	}

	if reconcilationError != nil {
		meta.SetStatusCondition(conditions, metav1.Condition{
			Type:    string(hyperv1.ControlPlaneComponentRolloutComplete),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.ReconciliationErrorReason,
			Message: reconcilationError.Error(),
		})
		return metav1.ConditionFalse
	}

	workloadObject := c.workloadProvider.NewObject()
	if err := cpContext.Client.Get(cpContext, client.ObjectKey{Namespace: cpContext.HCP.Namespace, Name: c.name}, workloadObject); err != nil {
		meta.SetStatusCondition(conditions, metav1.Condition{
			Type:    string(hyperv1.ControlPlaneComponentRolloutComplete),
			Status:  metav1.ConditionFalse,
			Reason:  string(apierrors.ReasonForError(err)),
			Message: err.Error(),
		})
		return metav1.ConditionFalse
	}

	checkOperandsRolloutStatusFn := c.checkOperandsRolloutStatus
	if c.customOperandsRolloutCheck != nil {
		checkOperandsRolloutStatusFn = c.customOperandsRolloutCheck
	}

	status, reason, message := c.workloadProvider.IsReady(workloadObject)
	if status == metav1.ConditionFalse && diagReason != "" {
		reason = diagReason
		message = fmt.Sprintf("%s; %s", message, diagMessage)
	}

	operandsReady, err := checkOperandsRolloutStatusFn(cpContext.workloadContext())
	if !operandsReady && status == metav1.ConditionTrue {
		status = metav1.ConditionFalse
		reason = "WaitingForOperands"
		message = err.Error()
	}

	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:    string(hyperv1.ControlPlaneComponentRolloutComplete),
		Status:  status,
		Reason:  reason,
		Message: message,
	})
	return status
}

func (c *controlPlaneWorkload[T]) checkOperandsRolloutStatus(cpContext WorkloadContext) (bool, error) {
	if !c.monitorOperandsRolloutStatus {
		return true, nil
	}

	deploymentList := &appsv1.DeploymentList{}
	if err := cpContext.Client.List(cpContext, deploymentList, client.InNamespace(cpContext.HCP.Namespace), client.MatchingLabels{
		"hypershift.openshift.io/managed-by": c.Name(),
	}); err != nil {
		return false, fmt.Errorf("failed to list managed deployments for component %s: %w", c.Name(), err)
	}

	expectedVersion := cpContext.ReleaseImageProvider.Version()
	var errs []error
	for _, deployment := range deploymentList.Items {
		releaseVersion, ok := deployment.Annotations["release.openshift.io/version"]
		if !ok {
			errs = append(errs, fmt.Errorf("deployment %s/%s does not have the required annotation 'release.openshift.io/version'", deployment.Namespace, deployment.Name))
			continue
		}
		if releaseVersion != expectedVersion {
			errs = append(errs, fmt.Errorf("deployment %s/%s has version %s, expected %s", deployment.Namespace, deployment.Name, releaseVersion, expectedVersion))
			continue
		}
		if !podspec.IsDeploymentReady(cpContext, &deployment) {
			errs = append(errs, fmt.Errorf("deployment %s/%s is not ready", deployment.Namespace, deployment.Name))
		}
	}

	if len(errs) > 0 {
		return false, errors.Join(errs...)
	}

	return true, nil
}
