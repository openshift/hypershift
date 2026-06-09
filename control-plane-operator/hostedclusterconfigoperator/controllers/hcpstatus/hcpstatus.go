package hcpstatus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
	"github.com/openshift/hypershift/support/releaseinfo"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const ControllerName = "hcpstatus"

func Setup(ctx context.Context, opts *operator.HostedClusterConfigOperatorConfig) error {
	r := &hcpStatusReconciler{
		mgtClusterClient:    opts.CPCluster.GetClient(),
		hostedClusterClient: opts.Manager.GetClient(),
		releaseProvider:     opts.ReleaseProvider,
	}
	c, err := controller.New(ControllerName, opts.Manager, controller.Options{Reconciler: r})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}
	if err := c.Watch(source.Kind(opts.CPCluster.GetCache(), &hyperv1.HostedControlPlane{}, &handler.TypedEnqueueRequestForObject[*hyperv1.HostedControlPlane]{})); err != nil {
		return fmt.Errorf("failed to watch HCP: %w", err)
	}

	clusterVersionMapper := func(context.Context, crclient.Object) []reconcile.Request {
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: opts.Namespace, Name: opts.HCPName}}}
	}
	if err := c.Watch(source.Kind[crclient.Object](opts.Manager.GetCache(), &configv1.ClusterVersion{}, handler.EnqueueRequestsFromMapFunc(clusterVersionMapper))); err != nil {
		return fmt.Errorf("failed to watch clusterversion: %w", err)
	}

	clusterAuthenticationMapper := func(context.Context, crclient.Object) []reconcile.Request {
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: opts.Namespace, Name: opts.HCPName}}}
	}

	if err := c.Watch(source.Kind[crclient.Object](opts.Manager.GetCache(), &configv1.Authentication{}, handler.EnqueueRequestsFromMapFunc(clusterAuthenticationMapper))); err != nil {
		return fmt.Errorf("failed to watch authentication: %w", err)
	}

	return nil
}

type hcpStatusReconciler struct {
	mgtClusterClient    crclient.Client
	hostedClusterClient crclient.Client
	releaseProvider     releaseinfo.Provider
}

func (h *hcpStatusReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	hcp := &hyperv1.HostedControlPlane{}
	if err := h.mgtClusterClient.Get(ctx, req.NamespacedName, hcp); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to get hcp %s: %w", req, err)
	}
	originalHCP := hcp.DeepCopy()
	if err := h.reconcile(ctx, hcp); err != nil {
		return reconcile.Result{}, err
	}

	if equality.Semantic.DeepEqual(hcp.Status, originalHCP.Status) {
		return reconcile.Result{}, nil
	}

	// Use JSON Patch (RFC 6902) instead of JSON Merge Patch (RFC 7386).
	// Merge patch interprets null as "delete field" (RFC 7386 §7), which corrupts
	// +required +nullable fields in configv1.ClusterVersionStatus that have no
	// omitempty and serialize nil as JSON null:
	//   - AvailableUpdates []configv1.Release `json:"availableUpdates"` — nil slice → null
	//   - CompletionTime *metav1.Time `json:"completionTime"` (in UpdateHistory) — nil pointer → null
	// JSON Patch "replace"/"add" ops carry null as a literal value, preserving it correctly.
	patch, err := buildStatusPatch(originalHCP, hcp)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to build status patch: %w", err)
	}

	log.Info("Patching HCP status with new configuration and version status")
	if err := h.mgtClusterClient.Status().Patch(ctx, hcp,
		crclient.RawPatch(types.JSONPatchType, patch)); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to patch hcp status: %w", err)
	}
	log.Info("Successfully patched HCP status")

	return reconcile.Result{}, nil
}

type jsonPatchOp struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

func buildStatusPatch(original, modified *hyperv1.HostedControlPlane) ([]byte, error) {
	origJSON, err := json.Marshal(original.Status)
	if err != nil {
		return nil, err
	}
	modJSON, err := json.Marshal(modified.Status)
	if err != nil {
		return nil, err
	}

	var origMap, modMap map[string]json.RawMessage
	if err := json.Unmarshal(origJSON, &origMap); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(modJSON, &modMap); err != nil {
		return nil, err
	}

	// Optimistic lock: fail if the object was modified since our read.
	ops := []jsonPatchOp{{Op: "test", Path: "/metadata/resourceVersion", Value: original.ResourceVersion}}

	for key, modVal := range modMap {
		origVal, exists := origMap[key]
		if !exists {
			ops = append(ops, jsonPatchOp{Op: "add", Path: "/status/" + key, Value: modVal})
		} else if !bytes.Equal(origVal, modVal) {
			ops = append(ops, jsonPatchOp{Op: "replace", Path: "/status/" + key, Value: modVal})
		}
	}
	for key := range origMap {
		if _, exists := modMap[key]; !exists {
			ops = append(ops, jsonPatchOp{Op: "remove", Path: "/status/" + key})
		}
	}

	return json.Marshal(ops)
}

// findClusterOperatorStatusCondition is identical to meta.FindStatusCondition except that it works on config1.ClusterOperatorStatusCondition instead of
// metav1.StatusCondition
func findClusterOperatorStatusCondition(conditions []configv1.ClusterOperatorStatusCondition, conditionType configv1.ClusterStatusConditionType) *configv1.ClusterOperatorStatusCondition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}

	return nil
}

func (h *hcpStatusReconciler) reconcile(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	log := ctrl.LoggerFrom(ctx)

	var clusterVersion configv1.ClusterVersion
	err := h.hostedClusterClient.Get(ctx, crclient.ObjectKey{Name: "version"}, &clusterVersion)
	// We check err in loop below to build conditions with ConditionUnknown status for all types.

	if err == nil {
		hcp.Status.VersionStatus = &hyperv1.ClusterVersionStatus{
			Desired:            clusterVersion.Status.Desired,
			History:            clusterVersion.Status.History,
			ObservedGeneration: clusterVersion.Status.ObservedGeneration,
			AvailableUpdates:   clusterVersion.Status.AvailableUpdates,
			ConditionalUpdates: clusterVersion.Status.ConditionalUpdates,
		}
		//lint:ignore SA1019 populate the deprecated property until we can drop it in a later API version
		hcp.Status.Version = hcp.Status.VersionStatus.Desired.Version
		//lint:ignore SA1019 populate the deprecated property until we can drop it in a later API version
		hcp.Status.ReleaseImage = hcp.Status.VersionStatus.Desired.Image
	}

	cvoConditions := map[hyperv1.ConditionType]*configv1.ClusterOperatorStatusCondition{
		hyperv1.ClusterVersionFailing:          findClusterOperatorStatusCondition(clusterVersion.Status.Conditions, "Failing"),
		hyperv1.ClusterVersionReleaseAccepted:  findClusterOperatorStatusCondition(clusterVersion.Status.Conditions, "ReleaseAccepted"),
		hyperv1.ClusterVersionRetrievedUpdates: findClusterOperatorStatusCondition(clusterVersion.Status.Conditions, configv1.RetrievedUpdates),
		hyperv1.ClusterVersionProgressing:      findClusterOperatorStatusCondition(clusterVersion.Status.Conditions, configv1.OperatorProgressing),
		hyperv1.ClusterVersionUpgradeable:      findClusterOperatorStatusCondition(clusterVersion.Status.Conditions, configv1.OperatorUpgradeable),
		hyperv1.ClusterVersionAvailable:        findClusterOperatorStatusCondition(clusterVersion.Status.Conditions, configv1.OperatorAvailable),
	}

	for conditionType, condition := range cvoConditions {
		var hcpCVOCondition metav1.Condition
		// Set unknown status.
		var unknownStatusMessage string
		if condition == nil {
			unknownStatusMessage = "Condition not found in the CVO."
		}
		if err != nil {
			unknownStatusMessage = fmt.Sprintf("failed to get clusterVersion: %v", err)
		}

		hcpCVOCondition = metav1.Condition{
			Type:               string(conditionType),
			Status:             metav1.ConditionUnknown,
			Reason:             hyperv1.StatusUnknownReason,
			Message:            unknownStatusMessage,
			ObservedGeneration: hcp.Generation,
		}

		if err == nil && condition != nil {
			// Bubble up info from CVO.
			reason := condition.Reason
			// reason is not required in ClusterOperatorStatusCondition, but it's in metav1.conditions.
			// So we need to make sure the input does not break the KAS expectation.
			if reason == "" {
				reason = hyperv1.FromClusterVersionReason
			}
			hcpCVOCondition = metav1.Condition{
				Type:               string(conditionType),
				Status:             metav1.ConditionStatus(condition.Status),
				Reason:             reason,
				Message:            condition.Message,
				ObservedGeneration: hcp.Generation,
			}
		}

		// If CVO has no Upgradeable condition, consider the HCP upgradable according to the CVO
		if conditionType == hyperv1.ClusterVersionUpgradeable &&
			condition == nil {
			hcpCVOCondition.Status = metav1.ConditionTrue
			hcpCVOCondition.Reason = hyperv1.FromClusterVersionReason
		}

		meta.SetStatusCondition(&hcp.Status.Conditions, hcpCVOCondition)
	}

	var authentication configv1.Authentication
	if err = h.hostedClusterClient.Get(ctx, crclient.ObjectKey{Name: "cluster"}, &authentication); err != nil {
		return fmt.Errorf("failed to get Authentication resource \"cluster\": %w", err)
	}
	hcp.Status.Configuration = &hyperv1.ConfigurationStatus{
		Authentication: authentication.Status,
	}

	log.Info("Finished reconciling configuration and version status")
	return nil
}
