package nodepool

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	ignserver "github.com/openshift/hypershift/ignition-server/controllers"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	reconciliationActiveConditionReason             = "ReconciliationActive"
	reconciliationPausedConditionReason             = "ReconciliationPaused"
	reconciliationInvalidPausedUntilConditionReason = "InvalidPausedUntilValue"
)

// These are copies pf metav1.Condition to accept hyperv1.NodePoolCondition
// We use different conditions struct to relax metav1 input validation.
// We want to relax validation to ease bubbling up from CAPI which uses their own type not honouring metav1 validations, particularly "Reason" accepts pretty much free string.
// TODO (alberto): work upstream towards consolidation and programmatic Reasons.

// SetStatusCondition sets the corresponding condition in conditions to newCondition.
// conditions must be non-nil.
//  1. if the condition of the specified type already exists (all fields of the existing condition are updated to
//     newCondition, LastTransitionTime is set to now if the new status differs from the old status)
//  2. if a condition of the specified type does not exist (LastTransitionTime is set to now() if unset, and newCondition is appended)
func SetStatusCondition(conditions *[]hyperv1.NodePoolCondition, newCondition hyperv1.NodePoolCondition) {
	if conditions == nil {
		return
	}
	existingCondition := FindStatusCondition(*conditions, newCondition.Type)
	if existingCondition == nil {
		if newCondition.LastTransitionTime.IsZero() {
			newCondition.LastTransitionTime = metav1.NewTime(time.Now())
		}
		*conditions = append(*conditions, newCondition)
		return
	}

	if existingCondition.Status != newCondition.Status {
		existingCondition.Status = newCondition.Status
		if !newCondition.LastTransitionTime.IsZero() {
			existingCondition.LastTransitionTime = newCondition.LastTransitionTime
		} else {
			existingCondition.LastTransitionTime = metav1.NewTime(time.Now())
		}
	}

	existingCondition.Reason = newCondition.Reason
	existingCondition.Message = newCondition.Message
	existingCondition.ObservedGeneration = newCondition.ObservedGeneration
}

// removeStatusCondition removes the corresponding conditionType from conditions.
// conditions must be non-nil.
func removeStatusCondition(conditions *[]hyperv1.NodePoolCondition, conditionType string) {
	if conditions == nil || len(*conditions) == 0 {
		return
	}

	newConditions := make([]hyperv1.NodePoolCondition, 0, len(*conditions)-1)
	for _, condition := range *conditions {
		if condition.Type != conditionType {
			newConditions = append(newConditions, condition)
		}
	}

	*conditions = newConditions
}

// FindStatusCondition finds the conditionType in conditions.
func FindStatusCondition(conditions []hyperv1.NodePoolCondition, conditionType string) *hyperv1.NodePoolCondition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}

	return nil
}

// FindStatusCondition finds the conditionType in conditions.
func findCAPIStatusCondition(conditions []capiv1.Condition, conditionType capiv1.ConditionType) *capiv1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}

	return nil
}

// generateReconciliationActiveCondition will generate the resource condition that reflects the state of reconciliation
// on the resource.
// (copied from support/util/pausereconcile_test.go and adjusted to use NodePoolCondition)
func generateReconciliationActiveCondition(pausedUntilField *string, objectGeneration int64) hyperv1.NodePoolCondition {
	isPaused, _, err := util.ProcessPausedUntilField(pausedUntilField, time.Now())
	var msgString string
	if isPaused {
		if _, err := strconv.ParseBool(*pausedUntilField); err == nil {
			msgString = "Reconciliation paused until field removed"
		} else {
			msgString = fmt.Sprintf("Reconciliation paused until: %s", *pausedUntilField)
		}
		return hyperv1.NodePoolCondition{
			Type:               string(hyperv1.NodePoolReconciliationActiveConditionType),
			Status:             corev1.ConditionFalse,
			Reason:             reconciliationPausedConditionReason,
			Message:            msgString,
			ObservedGeneration: objectGeneration,
		}
	}
	msgString = "Reconciliation active on resource"
	reasonString := reconciliationActiveConditionReason
	if err != nil {
		reasonString = reconciliationInvalidPausedUntilConditionReason
		msgString = "Invalid value provided for PausedUntil field"
	}
	return hyperv1.NodePoolCondition{
		Type:               string(hyperv1.NodePoolReconciliationActiveConditionType),
		Status:             corev1.ConditionTrue,
		Reason:             reasonString,
		Message:            msgString,
		ObservedGeneration: objectGeneration,
	}
}

func (r *NodePoolReconciler) setPlatformConditions(ctx context.Context, hcluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool, controlPlaneNamespace string, releaseImage *releaseinfo.ReleaseImage) error {
	switch nodePool.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		return r.setKubevirtConditions(ctx, nodePool, hcluster, controlPlaneNamespace, releaseImage)
	case hyperv1.AWSPlatform:
		return r.setAWSConditions(ctx, nodePool, hcluster, controlPlaneNamespace, releaseImage)
	case hyperv1.PowerVSPlatform:
		return r.setPowerVSconditions(ctx, nodePool, hcluster, controlPlaneNamespace, releaseImage)
	default:
		return nil
	}
}

func (r *NodePoolReconciler) autoscalerEnabledCondition(_ context.Context, nodePool *hyperv1.NodePool, _ *hyperv1.HostedCluster) (*ctrl.Result, error) {
	if isAutoscalingEnabled(nodePool) {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolAutoscalingEnabledConditionType,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			Message:            fmt.Sprintf("Maximum nodes: %v, Minimum nodes: %v", nodePool.Spec.AutoScaling.Max, nodePool.Spec.AutoScaling.Min),
			ObservedGeneration: nodePool.Generation,
		})
	} else {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolAutoscalingEnabledConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.AsExpectedReason,
			ObservedGeneration: nodePool.Generation,
		})
	}
	return nil, nil
}

func (r *NodePoolReconciler) updateManagementEnabledCondition(ctx context.Context, nodePool *hyperv1.NodePool, _ *hyperv1.HostedCluster) (*ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	if err := validateManagement(nodePool); err != nil {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdateManagementEnabledConditionType,
			Status:             corev1.ConditionFalse,
			Message:            err.Error(),
			Reason:             hyperv1.NodePoolValidationFailedReason,
			ObservedGeneration: nodePool.Generation,
		})
		// We don't return the error here as reconciling won't solve the input problem.
		// An update event will trigger reconciliation.
		log.Error(err, "validating management parameters failed")
		return &ctrl.Result{}, nil

	} else {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdateManagementEnabledConditionType,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			ObservedGeneration: nodePool.Generation,
		})
	}
	return nil, nil
}

func (r *NodePoolReconciler) releaseImageCondition(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster) (*ctrl.Result, error) {
	_, err := r.getReleaseImage(ctx, hcluster, nodePool.Status.Version, nodePool.Spec.Release.Image)
	if err != nil {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidReleaseImageConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.NodePoolValidationFailedReason,
			Message:            fmt.Sprintf("Failed to get release image: %v", err.Error()),
			ObservedGeneration: nodePool.Generation,
		})
		return &ctrl.Result{}, fmt.Errorf("failed to look up release image metadata: %w", err)
	} else {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidReleaseImageConditionType,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			Message:            fmt.Sprintf("Using release image: %s", nodePool.Spec.Release.Image),
			ObservedGeneration: nodePool.Generation,
		})
	}
	return nil, nil
}

func (r *NodePoolReconciler) ignitionEndpointAvailableCondition(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster) (*ctrl.Result, error) {
	// Validate Ignition CA Secret.
	log := ctrl.LoggerFrom(ctx)
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)

	if hcluster.Status.IgnitionEndpoint == "" {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               string(hyperv1.IgnitionEndpointAvailable),
			Status:             corev1.ConditionFalse,
			Message:            "Ignition endpoint not available, waiting",
			Reason:             hyperv1.IgnitionEndpointMissingReason,
			ObservedGeneration: nodePool.Generation,
		})
		log.Info("Ignition endpoint not available, waiting")
		return &ctrl.Result{}, nil
	}
	removeStatusCondition(&nodePool.Status.Conditions, string(hyperv1.IgnitionEndpointAvailable))

	caSecret := ignitionserver.IgnitionCACertSecret(controlPlaneNamespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(caSecret), caSecret); err != nil {
		if apierrors.IsNotFound(err) {
			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               string(hyperv1.IgnitionEndpointAvailable),
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.IgnitionCACertMissingReason,
				Message:            "still waiting for ignition CA cert Secret to exist",
				ObservedGeneration: nodePool.Generation,
			})
			log.Info("Ignition endpoint not available, waiting")
			return &ctrl.Result{}, nil
		} else {
			return &ctrl.Result{}, fmt.Errorf("failed to get ignition CA Secret: %w", err)
		}
	}
	removeStatusCondition(&nodePool.Status.Conditions, string(hyperv1.IgnitionEndpointAvailable))

	_, hasCACert := caSecret.Data[corev1.TLSCertKey]
	if !hasCACert {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               string(hyperv1.IgnitionEndpointAvailable),
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.IgnitionCACertMissingReason,
			Message:            "CA Secret is missing tls.crt key",
			ObservedGeneration: nodePool.Generation,
		})
		log.Info("CA Secret is missing tls.crt key")
		return &ctrl.Result{}, nil

	}
	removeStatusCondition(&nodePool.Status.Conditions, string(hyperv1.IgnitionEndpointAvailable))
	return nil, nil
}

func (r *NodePoolReconciler) validArchPlatformCondition(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster) (*ctrl.Result, error) {
	// Validate modifying CPU arch support for platform
	if !isArchAndPlatformSupported(nodePool) {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidArchPlatform,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.NodePoolInvalidArchPlatform,
			Message:            fmt.Sprintf("CPU arch %s is not supported for platform: %s, use 'amd64' instead", nodePool.Spec.Arch, nodePool.Spec.Platform.Type),
			ObservedGeneration: nodePool.Generation,
		})
	} else {
		if err := validateHCPayloadSupportsNodePoolCPUArch(hcluster, nodePool); err != nil {
			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidArchPlatform,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.NodePoolInvalidArchPlatform,
				Message:            err.Error(),
				ObservedGeneration: nodePool.Generation,
			})
			return &ctrl.Result{}, err
		} else {
			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidArchPlatform,
				Status:             corev1.ConditionTrue,
				Reason:             hyperv1.AsExpectedReason,
				ObservedGeneration: nodePool.Generation,
			})
		}
	}
	return nil, nil
}

func (r *NodePoolReconciler) validMachineConfigCondition(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster) (*ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	releaseImage, err := r.getReleaseImage(ctx, hcluster, nodePool.Status.Version, nodePool.Spec.Release.Image)
	if err != nil {
		return &ctrl.Result{}, fmt.Errorf("failed to look up release image metadata: %w", err)
	}

	// TODO (alberto): we should hide haproxy config generation within NewConfigGenerator and let the kubeconfig absence to be an error type.
	// The func consumer can then choose how to handle it.
	if hcluster.Status.KubeConfig == nil {
		log.Info("waiting on hostedCluster.status.kubeConfig to be set")
		return &ctrl.Result{}, nil
	}

	haproxyRawConfig, err := r.generateHAProxyRawConfig(ctx, hcluster, releaseImage)
	if err != nil {
		return &ctrl.Result{}, fmt.Errorf("failed to generate HAProxy raw config: %w", err)
	}

	_, err = NewConfigGenerator(ctx, r.Client, hcluster, nodePool, releaseImage, haproxyRawConfig)
	if err != nil {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidMachineConfigConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.NodePoolValidationFailedReason,
			Message:            err.Error(),
			ObservedGeneration: nodePool.Generation,
		})
		return &ctrl.Result{}, fmt.Errorf("failed to generate config: %w", err)
	}
	SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolValidMachineConfigConditionType,
		Status:             corev1.ConditionTrue,
		Reason:             hyperv1.AsExpectedReason,
		ObservedGeneration: nodePool.Generation,
	})

	return nil, nil
}

func (r *NodePoolReconciler) updatingConfigCondition(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster) (*ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	token, err := r.token(ctx, hcluster, nodePool)
	if err != nil {
		return &ctrl.Result{}, fmt.Errorf("error getting token: %w", err)
	}

	targetConfigHash := token.HashWithoutVersion()
	isUpdatingConfig := isUpdatingConfig(nodePool, targetConfigHash)
	if isUpdatingConfig {
		reason := hyperv1.AsExpectedReason
		message := fmt.Sprintf("Updating config in progress. Target config: %s", targetConfigHash)
		status := corev1.ConditionTrue

		if nodePool.Spec.Management.UpgradeType == hyperv1.UpgradeTypeInPlace {
			capi, err := newCAPI(token, hcluster.Spec.InfraID)
			if err != nil {
				return &ctrl.Result{}, fmt.Errorf("error getting capi client: %w", err)
			}

			machineSet := capi.machineSet()
			err = r.Get(ctx, client.ObjectKeyFromObject(machineSet), machineSet)
			if err != nil {
				if !apierrors.IsNotFound(err) {
					return &ctrl.Result{}, fmt.Errorf("failed to get MachineSet: %w", err)
				}
			} else {
				if _, ok := machineSet.Annotations[nodePoolAnnotationUpgradeInProgressTrue]; ok {
					status = corev1.ConditionTrue
					reason = hyperv1.AsExpectedReason
					message = machineSet.Annotations[nodePoolAnnotationUpgradeInProgressTrue]
				}

				if _, ok := machineSet.Annotations[nodePoolAnnotationUpgradeInProgressFalse]; ok {
					status = corev1.ConditionFalse
					reason = hyperv1.NodePoolInplaceUpgradeFailedReason
					message = machineSet.Annotations[nodePoolAnnotationUpgradeInProgressFalse]
				}
			}
		}

		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdatingConfigConditionType,
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: nodePool.Generation,
		})
		log.Info("NodePool config is updating",
			"current", nodePool.GetAnnotations()[nodePoolAnnotationCurrentConfig],
			"target", targetConfigHash)
	} else {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdatingConfigConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.AsExpectedReason,
			ObservedGeneration: nodePool.Generation,
		})
	}
	return nil, nil
}

func (r *NodePoolReconciler) updatingVersionCondition(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster) (*ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	releaseImage, err := r.getReleaseImage(ctx, hcluster, nodePool.Status.Version, nodePool.Spec.Release.Image)
	if err != nil {
		return &ctrl.Result{}, fmt.Errorf("failed to look up release image metadata: %w", err)
	}

	targetVersion := releaseImage.Version()
	isUpdatingVersion := isUpdatingVersion(nodePool, targetVersion)
	if isUpdatingVersion {
		reason := hyperv1.AsExpectedReason
		message := fmt.Sprintf("Updating version in progress. Target version: %s", targetVersion)
		status := corev1.ConditionTrue

		if nodePool.Spec.Management.UpgradeType == hyperv1.UpgradeTypeInPlace {
			token, err := r.token(ctx, hcluster, nodePool)
			if err != nil {
				return &ctrl.Result{}, fmt.Errorf("error getting token: %w", err)
			}

			capi, err := newCAPI(token, hcluster.Spec.InfraID)
			if err != nil {
				return &ctrl.Result{}, fmt.Errorf("error getting capi client: %w", err)
			}

			machineSet := capi.machineSet()
			err = r.Get(ctx, client.ObjectKeyFromObject(machineSet), machineSet)
			if err != nil {
				if !apierrors.IsNotFound(err) {
					return &ctrl.Result{}, fmt.Errorf("failed to get MachineSet: %w", err)
				}
			} else {
				if _, ok := machineSet.Annotations[nodePoolAnnotationUpgradeInProgressTrue]; ok {
					status = corev1.ConditionTrue
					reason = hyperv1.AsExpectedReason
					message = machineSet.Annotations[nodePoolAnnotationUpgradeInProgressTrue]
				}

				if _, ok := machineSet.Annotations[nodePoolAnnotationUpgradeInProgressFalse]; ok {
					status = corev1.ConditionFalse
					reason = hyperv1.NodePoolInplaceUpgradeFailedReason
					message = machineSet.Annotations[nodePoolAnnotationUpgradeInProgressFalse]
				}
			}
		}

		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdatingVersionConditionType,
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: nodePool.Generation,
		})
		log.Info("NodePool version is updating",
			"current", nodePool.Status.Version, "target", targetVersion)
	} else {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdatingVersionConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.AsExpectedReason,
			ObservedGeneration: nodePool.Generation,
		})
	}

	return nil, nil
}

func (r NodePoolReconciler) validGeneratedPayloadCondition(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster) (*ctrl.Result, error) {
	// Signal ignition payload generation
	token, err := r.token(ctx, hcluster, nodePool)
	if err != nil {
		return &ctrl.Result{}, fmt.Errorf("error getting token: %w", err)
	}
	tokenSecret := token.TokenSecret()
	condition, err := r.createValidGeneratedPayloadCondition(ctx, tokenSecret, nodePool.Generation)
	if err != nil {
		return &ctrl.Result{}, fmt.Errorf("error setting ValidGeneratedPayload condition: %w", err)
	}
	SetStatusCondition(&nodePool.Status.Conditions, *condition)
	return nil, nil
}

func (r NodePoolReconciler) reachedIgnitionEndpointCondition(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster) (*ctrl.Result, error) {
	token, err := r.token(ctx, hcluster, nodePool)
	if err != nil {
		return &ctrl.Result{}, fmt.Errorf("error getting token: %w", err)
	}
	tokenSecret := token.TokenSecret()
	oldReachedIgnitionEndpointCondition := FindStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolReachedIgnitionEndpoint)
	// when an InPlace upgrade occurs, a new token-secret is generated, but since nodes don't reboot and reignite,
	// the new token-secret wouldn't have the `hypershift.openshift.io/ignition-reached` annotation set.
	// this results in the NodePoolReachedIgnitionEndpoint condition to report False, although the ignition-server could have been already reached.
	//
	// if ignition is already reached and InPlace upgrade is used, skip recomputing the NodePoolReachedIgnitionEndpoint condition
	// to avoid resetting the condition to False because of the missing the annotation on the new generated token-secret.
	if oldReachedIgnitionEndpointCondition == nil || oldReachedIgnitionEndpointCondition.Status != corev1.ConditionTrue || nodePool.Spec.Management.UpgradeType != hyperv1.UpgradeTypeInPlace {
		reachedIgnitionEndpointCondition, err := r.createReachedIgnitionEndpointCondition(ctx, tokenSecret, nodePool.Generation)
		if err != nil {
			return &ctrl.Result{}, fmt.Errorf("error setting IgnitionReached condition: %w", err)
		}

		SetStatusCondition(&nodePool.Status.Conditions, *reachedIgnitionEndpointCondition)
	}
	return nil, nil
}

func (r NodePoolReconciler) machineAndNodeConditions(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster) (*ctrl.Result, error) {
	// Set AllMachinesReadyCondition.
	// Get all Machines for NodePool.
	err := r.setMachineAndNodeConditions(ctx, nodePool, hcluster)
	if err != nil {
		return &ctrl.Result{}, err
	}
	return nil, nil
}

func (r NodePoolReconciler) reconciliationActiveCondition(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster) (*ctrl.Result, error) {
	// Set ReconciliationActive condition
	SetStatusCondition(&nodePool.Status.Conditions, generateReconciliationActiveCondition(nodePool.Spec.PausedUntil, nodePool.Generation))
	return nil, nil
}

// setMachineAndNodeConditions sets the nodePool's AllMachinesReady and AllNodesHealthy conditions.
func (r *NodePoolReconciler) setMachineAndNodeConditions(ctx context.Context, nodePool *hyperv1.NodePool, hc *hyperv1.HostedCluster) error {
	// Get all Machines for NodePool.
	machines, err := r.getMachinesForNodePool(ctx, nodePool)
	if err != nil {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolAllMachinesReadyConditionType,
			Status:             corev1.ConditionUnknown,
			Reason:             hyperv1.NodePoolFailedToGetReason,
			Message:            err.Error(),
			ObservedGeneration: nodePool.Generation,
		})
		return fmt.Errorf("failed to get Machines: %w", err)
	}

	r.setAllMachinesReadyCondition(nodePool, machines)

	r.setAllNodesHealthyCondition(nodePool, machines)

	err = r.setCIDRConflictCondition(nodePool, machines, hc)
	if err != nil {
		return err
	}

	if nodePool.Spec.Platform.Type == hyperv1.KubevirtPlatform {
		err = r.setAllMachinesLMCondition(ctx, nodePool, hc)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *NodePoolReconciler) setAllNodesHealthyCondition(nodePool *hyperv1.NodePool, machines []*capiv1.Machine) {
	status := corev1.ConditionTrue
	reason := hyperv1.AsExpectedReason
	var message string

	if len(machines) < 1 {
		status = corev1.ConditionFalse
		reason = hyperv1.NodePoolNotFoundReason
		message = "No Machines are created"
		if nodePool.Spec.Replicas != nil && *nodePool.Spec.Replicas == 0 {
			reason = hyperv1.AsExpectedReason
			message = "NodePool set to no replicas"
		}
	}

	for _, machine := range machines {
		condition := findCAPIStatusCondition(machine.Status.Conditions, capiv1.MachineNodeHealthyCondition)
		if condition != nil && condition.Status != corev1.ConditionTrue {
			status = corev1.ConditionFalse
			reason = condition.Reason
			message = message + fmt.Sprintf("Machine %s: %s\n", machine.Name, condition.Reason)
		}
	}

	if status == corev1.ConditionTrue {
		message = hyperv1.AllIsWellMessage
	}

	allMachinesHealthyCondition := &hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolAllNodesHealthyConditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: nodePool.Generation,
	}
	SetStatusCondition(&nodePool.Status.Conditions, *allMachinesHealthyCondition)
}

func (r *NodePoolReconciler) setAllMachinesReadyCondition(nodePool *hyperv1.NodePool, machines []*capiv1.Machine) {
	status := corev1.ConditionTrue
	reason := hyperv1.AsExpectedReason
	message := hyperv1.AllIsWellMessage

	if numMachines := len(machines); numMachines == 0 {
		status = corev1.ConditionFalse
		reason = hyperv1.NodePoolNotFoundReason
		message = "No Machines are created"
		if nodePool.Spec.Replicas != nil && *nodePool.Spec.Replicas == 0 {
			reason = hyperv1.AsExpectedReason
			message = "NodePool set to no replicas"
		}
	} else {
		// Aggregate conditions.
		// TODO (alberto): consider bubbling failureReason / failureMessage.
		// This a rudimentary approach which aggregates every Machine, until
		// https://github.com/kubernetes-sigs/cluster-api/pull/6218 and
		// https://github.com/kubernetes-sigs/cluster-api/pull/6025
		// are solved.
		// Eventually we should solve this in CAPI to make it available in MachineDeployments / MachineSets
		// with a consumable "Reason" and an aggregated "Message".

		numNotReady := 0
		messageMap := make(map[string][]string)

		for _, machine := range machines {
			readyCond := findCAPIStatusCondition(machine.Status.Conditions, capiv1.ReadyCondition)
			if readyCond != nil && readyCond.Status != corev1.ConditionTrue {
				status = corev1.ConditionFalse
				numNotReady++
				infraReadyCond := findCAPIStatusCondition(machine.Status.Conditions, capiv1.InfrastructureReadyCondition)
				// We append the reason as part of the higher Message, since the message is meaningless.
				// This is how a CAPI condition looks like in AWS for an instance deleted out of band failure.
				//	- lastTransitionTime: "2022-11-28T15:14:28Z"
				//		message: 1 of 2 completed
				//		reason: InstanceTerminated
				//		severity: Error
				//		status: "False"
				//		type: Ready
				var mapReason, mapMessage string
				if infraReadyCond != nil && infraReadyCond.Status != corev1.ConditionTrue && !isSetupCounterCondMessage.MatchString(infraReadyCond.Message) {
					mapReason = infraReadyCond.Reason
					mapMessage = fmt.Sprintf("Machine %s: %s: %s\n", machine.Name, infraReadyCond.Reason, infraReadyCond.Message)
				} else {
					mapReason = readyCond.Reason
					mapMessage = fmt.Sprintf("Machine %s: %s\n", machine.Name, readyCond.Reason)
				}

				messageMap[mapReason] = append(messageMap[mapReason], mapMessage)
			}
		}
		if numNotReady > 0 {
			reason, message = aggregateMachineReasonsAndMessages(messageMap, numMachines, numNotReady, aggregatorMachineStateReady)
		}
	}

	allMachinesReadyCondition := &hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolAllMachinesReadyConditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: nodePool.Generation,
	}

	SetStatusCondition(&nodePool.Status.Conditions, *allMachinesReadyCondition)
}

func (r *NodePoolReconciler) setCIDRConflictCondition(nodePool *hyperv1.NodePool, machines []*capiv1.Machine, hc *hyperv1.HostedCluster) error {
	maxMessageLength := 256

	if len(machines) < 1 || len(hc.Spec.Networking.ClusterNetwork) < 1 {
		removeStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolClusterNetworkCIDRConflictType)
		return nil
	}

	clusterNetworkStr := hc.Spec.Networking.ClusterNetwork[0].CIDR.String()
	clusterNetwork, err := netip.ParsePrefix(clusterNetworkStr)
	if err != nil {
		return err
	}

	messages := []string{}
	for _, machine := range machines {
		for _, addr := range machine.Status.Addresses {
			if addr.Type != capiv1.MachineExternalIP && addr.Type != capiv1.MachineInternalIP {
				continue
			}
			ipaddr, err := netip.ParseAddr(addr.Address)
			if err != nil {
				return err
			}
			if clusterNetwork.Contains(ipaddr) {
				messages = append(messages, fmt.Sprintf("machine [%s] with ip [%s] collides with cluster-network cidr [%s]", machine.Name, addr.Address, clusterNetworkStr))
			}
		}
	}

	if len(messages) > 0 {
		message := ""
		for _, entry := range messages {

			if len(message) == 0 {
				message = entry
			} else if len(entry)+len(message) < maxMessageLength {
				message = message + ", " + entry
			} else {
				message = message + ", too many similar errors..."
			}
		}

		cidrConflictCondition := &hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolClusterNetworkCIDRConflictType,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.InvalidConfigurationReason,
			Message:            message,
			ObservedGeneration: nodePool.Generation,
		}
		SetStatusCondition(&nodePool.Status.Conditions, *cidrConflictCondition)
	}

	return nil
}

// createReachedIgnitionEndpointCondition creates a condition for the NodePool based on the tokenSecret data.
func (r NodePoolReconciler) createReachedIgnitionEndpointCondition(ctx context.Context, tokenSecret *corev1.Secret, generation int64) (*hyperv1.NodePoolCondition, error) {
	var condition *hyperv1.NodePoolCondition
	if err := r.Get(ctx, client.ObjectKeyFromObject(tokenSecret), tokenSecret); err != nil {
		if !apierrors.IsNotFound(err) {
			condition = &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolReachedIgnitionEndpoint,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.NodePoolFailedToGetReason,
				Message:            err.Error(),
				ObservedGeneration: generation,
			}
		} else {
			condition = &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolReachedIgnitionEndpoint,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.NodePoolNotFoundReason,
				Message:            err.Error(),
				ObservedGeneration: generation,
			}
		}
		return condition, nil
	}

	if _, ok := tokenSecret.Annotations[TokenSecretIgnitionReachedAnnotation]; !ok {
		condition = &hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolReachedIgnitionEndpoint,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.IgnitionNotReached,
			Message:            "",
			ObservedGeneration: generation,
		}
		return condition, nil
	}

	condition = &hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolReachedIgnitionEndpoint,
		Status:             corev1.ConditionTrue,
		Reason:             hyperv1.AsExpectedReason,
		Message:            "",
		ObservedGeneration: generation,
	}

	return condition, nil
}

// createValidGeneratedPayloadCondition creates a condition for the NodePool based on the tokenSecret data.
func (r NodePoolReconciler) createValidGeneratedPayloadCondition(ctx context.Context, tokenSecret *corev1.Secret, generation int64) (*hyperv1.NodePoolCondition, error) {
	var condition *hyperv1.NodePoolCondition
	if err := r.Get(ctx, client.ObjectKeyFromObject(tokenSecret), tokenSecret); err != nil {
		if !apierrors.IsNotFound(err) {
			condition = &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidGeneratedPayloadConditionType,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.NodePoolFailedToGetReason,
				Message:            err.Error(),
				ObservedGeneration: generation,
			}
		} else {
			condition = &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidGeneratedPayloadConditionType,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.NodePoolNotFoundReason,
				Message:            err.Error(),
				ObservedGeneration: generation,
			}
		}
		return condition, nil
	}

	if _, ok := tokenSecret.Data[ignserver.TokenSecretReasonKey]; !ok {
		condition = &hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidGeneratedPayloadConditionType,
			Status:             corev1.ConditionUnknown,
			Reason:             string(tokenSecret.Data[ignserver.TokenSecretReasonKey]),
			Message:            "Unable to get status data from token secret",
			ObservedGeneration: generation,
		}
		return condition, nil
	}

	var status corev1.ConditionStatus
	if string(tokenSecret.Data[ignserver.TokenSecretReasonKey]) == hyperv1.AsExpectedReason {
		status = corev1.ConditionTrue
	}
	condition = &hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolValidGeneratedPayloadConditionType,
		Status:             status,
		Reason:             string(tokenSecret.Data[ignserver.TokenSecretReasonKey]),
		Message:            string(tokenSecret.Data[ignserver.TokenSecretMessageKey]),
		ObservedGeneration: generation,
	}

	return condition, nil
}
