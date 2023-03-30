package nodepool

import (
	"context"
	"fmt"
	"strconv"

	"github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sutilspointer "k8s.io/utils/pointer"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

func (r *NodePoolReconciler) reconcileMachineSet(ctx context.Context,
	machineSet *capiv1.MachineSet,
	nodePool *hyperv1.NodePool,
	userDataSecret *corev1.Secret,
	machineTemplateCR client.Object,
	CAPIClusterName string,
	targetVersion,
	targetConfigHash, targetConfigVersionHash, machineTemplateSpecJSON string) error {

	log := ctrl.LoggerFrom(ctx)
	// Set annotations and labels
	if machineSet.GetAnnotations() == nil {
		machineSet.Annotations = map[string]string{}
	}
	machineSet.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
	// Delete any paused annotation
	delete(machineSet.Annotations, capiv1.PausedAnnotation)
	if machineSet.GetLabels() == nil {
		machineSet.Labels = map[string]string{}
	}
	machineSet.Labels[capiv1.ClusterLabelName] = CAPIClusterName

	resourcesName := generateName(CAPIClusterName, nodePool.Spec.ClusterName, nodePool.GetName())
	machineSet.Spec.MinReadySeconds = int32(0)

	gvk, err := apiutil.GVKForObject(machineTemplateCR, api.Scheme)
	if err != nil {
		return err
	}

	// Set MaxUnavailable for the inplace upgrader to use
	maxUnavailable, err := getInPlaceMaxUnavailable(nodePool)
	if err != nil {
		return err
	}
	machineSet.Annotations[nodePoolAnnotationMaxUnavailable] = strconv.Itoa(maxUnavailable)

	// Set selector and template
	machineSet.Spec.ClusterName = CAPIClusterName
	if machineSet.Spec.Selector.MatchLabels == nil {
		machineSet.Spec.Selector.MatchLabels = map[string]string{}
	}
	machineSet.Spec.Selector.MatchLabels[resourcesName] = resourcesName
	machineSet.Spec.Template = capiv1.MachineTemplateSpec{
		ObjectMeta: capiv1.ObjectMeta{
			Labels: map[string]string{
				resourcesName:           resourcesName,
				capiv1.ClusterLabelName: CAPIClusterName,
			},
			// Annotations here propagate down to Machines
			// https://cluster-api.sigs.k8s.io/developer/architecture/controllers/metadata-propagation.html#machinedeployment.
			Annotations: map[string]string{
				// TODO (alberto): Use conditions to signal an in progress rolling upgrade
				// similar to what we do with nodePoolAnnotationCurrentConfig
				nodePoolAnnotationPlatformMachineTemplate: machineTemplateSpecJSON, // This will trigger a deployment rolling upgrade when its value changes.
				nodePoolAnnotation:                        client.ObjectKeyFromObject(nodePool).String(),
			},
		},

		Spec: capiv1.MachineSpec{
			ClusterName: CAPIClusterName,
			Bootstrap: capiv1.Bootstrap{
				// Keep current user data for later check.
				DataSecretName: machineSet.Spec.Template.Spec.Bootstrap.DataSecretName,
			},
			InfrastructureRef: corev1.ObjectReference{
				Kind:       gvk.Kind,
				APIVersion: gvk.GroupVersion().String(),
				Namespace:  machineTemplateCR.GetNamespace(),
				Name:       machineTemplateCR.GetName(),
			},
			// Keep current version for later check.
			Version:          machineSet.Spec.Template.Spec.Version,
			NodeDrainTimeout: nodePool.Spec.NodeDrainTimeout,
		},
	}

	// Propagate labels.
	for k, v := range nodePool.Spec.NodeLabels {
		// Propagated managed labels down to Machines with a known hardcoded prefix
		// so the CPO HCCO Node controller can recongnise them and apply them to Nodes.
		labelKey := fmt.Sprintf("%s.%s", labelManagedPrefix, k)
		machineSet.Spec.Template.Labels[labelKey] = v
	}

	// Propagate taints.
	taintsInJSON, err := taintsToJSON(nodePool.Spec.Taints)
	if err != nil {
		return err
	}
	machineSet.Spec.Template.Annotations[nodePoolAnnotationTaints] = taintsInJSON

	// Propagate version and userData Secret to the MachineSet.
	if userDataSecret.Name != k8sutilspointer.StringPtrDerefOr(machineSet.Spec.Template.Spec.Bootstrap.DataSecretName, "") {
		log.Info("New user data Secret has been generated",
			"current", machineSet.Spec.Template.Spec.Bootstrap.DataSecretName,
			"target", userDataSecret.Name)

		// TODO (alberto): possibly compare with NodePool here instead so we don't rely on impl details to drive decisions.
		if targetVersion != k8sutilspointer.StringPtrDerefOr(machineSet.Spec.Template.Spec.Version, "") {
			log.Info("Starting version upgrade: Propagating new version to the MachineSet",
				"releaseImage", nodePool.Spec.Release.Image, "target", targetVersion)
		}

		if targetConfigHash != nodePool.Annotations[nodePoolAnnotationCurrentConfig] {
			log.Info("Starting config upgrade: Propagating new config to the MachineSet",
				"current", nodePool.Annotations[nodePoolAnnotationCurrentConfig], "target", targetConfigHash)
		}
		machineSet.Spec.Template.Spec.Version = &targetVersion
		machineSet.Spec.Template.Spec.Bootstrap.DataSecretName = k8sutilspointer.StringPtr(userDataSecret.Name)

		// Signal in-place upgrade request.
		machineSet.Annotations[nodePoolAnnotationTargetConfigVersion] = targetConfigVersionHash

		// If the machineSet is brand new, set current version to target so in-place upgrade no-op.
		if _, ok := machineSet.Annotations[nodePoolAnnotationCurrentConfigVersion]; !ok {
			machineSet.Annotations[nodePoolAnnotationCurrentConfigVersion] = targetConfigVersionHash
		}

		// We return early here during a version/config upgrade to persist the resource with new user data Secret,
		// so in the next reconciling loop we get a new machineSet.Generation
		// and we can do a legit MachineSetComplete/MachineSet.Status.ObservedGeneration check.
		// Before persisting, if the NodePool is brand new we want to make sure the replica number is set so the MachineSet controller
		// does not panic.
		if machineSet.Spec.Replicas == nil {
			setMachineSetReplicas(nodePool, machineSet)
		}
		return nil
	}

	if machineSetInPlaceRolloutIsComplete(machineSet) {
		if nodePool.Status.Version != targetVersion {
			log.Info("Version upgrade complete",
				"previous", nodePool.Status.Version, "new", targetVersion)
			nodePool.Status.Version = targetVersion
		}

		if nodePool.Annotations == nil {
			nodePool.Annotations = make(map[string]string)
		}
		if nodePool.Annotations[nodePoolAnnotationCurrentConfig] != targetConfigHash {
			log.Info("Config upgrade complete",
				"previous", nodePool.Annotations[nodePoolAnnotationCurrentConfig], "new", targetConfigHash)

			nodePool.Annotations[nodePoolAnnotationCurrentConfig] = targetConfigHash
		}
		nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion] = targetConfigVersionHash
	}

	setMachineSetReplicas(nodePool, machineSet)

	// Bubble up upgrading NodePoolUpdatingVersionConditionType.
	var status corev1.ConditionStatus
	reason := ""
	message := ""
	status = "unknown"
	removeStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolUpdatingVersionConditionType)

	if _, ok := machineSet.Annotations[nodePoolAnnotationUpgradeInProgressTrue]; ok {
		status = corev1.ConditionTrue
		reason = hyperv1.AsExpectedReason
	}

	if _, ok := machineSet.Annotations[nodePoolAnnotationUpgradeInProgressFalse]; ok {
		status = corev1.ConditionFalse
		reason = hyperv1.NodePoolInplaceUpgradeFailedReason
	}

	// Check if config needs to be updated.
	isUpdatingConfig := isUpdatingConfig(nodePool, targetConfigHash)

	// Check if version needs to be updated.
	isUpdatingVersion := isUpdatingVersion(nodePool, targetVersion)

	if isUpdatingVersion {
		message = fmt.Sprintf("Updating Version, Target: %v", machineSet.Annotations[nodePoolAnnotationTargetConfigVersion])
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdatingVersionConditionType,
			Status:             status,
			ObservedGeneration: nodePool.Generation,
			Message:            message,
			Reason:             reason,
		})
	}

	if isUpdatingConfig {
		message = fmt.Sprintf("Updating Config, Target: %v", machineSet.Annotations[nodePoolAnnotationTargetConfigVersion])
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdatingConfigConditionType,
			Status:             status,
			ObservedGeneration: nodePool.Generation,
			Message:            message,
			Reason:             reason,
		})
	}

	// Bubble up AvailableReplicas and Ready condition from MachineSet.
	nodePool.Status.Replicas = machineSet.Status.AvailableReplicas
	for _, c := range machineSet.Status.Conditions {
		// This condition should aggregate and summarise readiness from underlying MachineSets and Machines
		// https://github.com/kubernetes-sigs/cluster-api/issues/3486.
		if c.Type == capiv1.ReadyCondition {
			// this is so api server does not complain
			// invalid value: \"\": status.conditions.reason in body should be at least 1 chars long"
			reason := hyperv1.AsExpectedReason
			if c.Reason != "" {
				reason = c.Reason
			}

			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolReadyConditionType,
				Status:             c.Status,
				ObservedGeneration: nodePool.Generation,
				Message:            c.Message,
				Reason:             reason,
			})
			break
		}
	}

	return nil
}

func machineSetInPlaceRolloutIsComplete(machineSet *capiv1.MachineSet) bool {
	return machineSet.Annotations[nodePoolAnnotationCurrentConfigVersion] == machineSet.Annotations[nodePoolAnnotationTargetConfigVersion]
}

// setMachineSetReplicas sets wanted replicas:
// If autoscaling is enabled we reconcile min/max annotations and leave replicas untouched.
func setMachineSetReplicas(nodePool *hyperv1.NodePool, machineSet *capiv1.MachineSet) {
	if machineSet.Annotations == nil {
		machineSet.Annotations = make(map[string]string)
	}

	if isAutoscalingEnabled(nodePool) {
		// The MachineSet replicas field should default to a value inside the (min size, max size) range based on the autoscaler annotations
		// so the autoscaler can take control of the replicas field.
		//
		// 1. if it’s a new MachineSet, or the replicas field of the old MachineSet is < min size, use min size
		// 2. if the replicas field of the old MachineSet is > max size, use max size
		msReplicas := k8sutilspointer.Int32Deref(machineSet.Spec.Replicas, 0)
		if msReplicas < nodePool.Spec.AutoScaling.Min {
			machineSet.Spec.Replicas = &nodePool.Spec.AutoScaling.Min
		} else if msReplicas > nodePool.Spec.AutoScaling.Max {
			machineSet.Spec.Replicas = &nodePool.Spec.AutoScaling.Max
		}

		machineSet.Annotations[autoscalerMaxAnnotation] = strconv.Itoa(int(nodePool.Spec.AutoScaling.Max))
		machineSet.Annotations[autoscalerMinAnnotation] = strconv.Itoa(int(nodePool.Spec.AutoScaling.Min))
	}

	// If autoscaling is NOT enabled we reset min/max annotations and reconcile replicas.
	if !isAutoscalingEnabled(nodePool) {
		machineSet.Annotations[autoscalerMaxAnnotation] = "0"
		machineSet.Annotations[autoscalerMinAnnotation] = "0"
		machineSet.Spec.Replicas = k8sutilspointer.Int32(k8sutilspointer.Int32Deref(nodePool.Spec.Replicas, 0))
	}
}

func getInPlaceMaxUnavailable(nodePool *hyperv1.NodePool) (int, error) {
	intOrPercent := intstr.FromInt(1)
	if nodePool.Spec.Management.InPlace != nil {
		if nodePool.Spec.Management.InPlace.MaxUnavailable != nil {
			intOrPercent = *nodePool.Spec.Management.InPlace.MaxUnavailable
		}
	}
	replicas := int(k8sutilspointer.Int32PtrDerefOr(nodePool.Spec.Replicas, 0))
	maxUnavailable, err := intstr.GetScaledValueFromIntOrPercent(&intOrPercent, replicas, false)
	if err != nil {
		return 0, err
	}
	if maxUnavailable == 0 {
		maxUnavailable = 1
	}
	return maxUnavailable, nil
}
