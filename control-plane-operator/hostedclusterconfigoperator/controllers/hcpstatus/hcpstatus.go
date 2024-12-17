package hcpstatus

import (
	"context"
	"fmt"
	"reflect"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
	"github.com/openshift/hypershift/support/releaseinfo"

	configv1 "github.com/openshift/api/config/v1"

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

	return nil
}

type hcpStatusReconciler struct {
	mgtClusterClient    crclient.Client
	hostedClusterClient crclient.Client
	releaseProvider     releaseinfo.Provider
}

func (h *hcpStatusReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	hcp := &hyperv1.HostedControlPlane{}
	if err := h.mgtClusterClient.Get(ctx, req.NamespacedName, hcp); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to get hcp %s: %w", req, err)
	}
	originalHCP := hcp.DeepCopy()
	if err := h.reconcile(ctx, hcp); err != nil {
		return reconcile.Result{}, err
	}

	if !reflect.DeepEqual(hcp.Status, originalHCP.Status) {
		if err := h.mgtClusterClient.Status().Update(ctx, hcp); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to update hcp: %w", err)
		}
	}

	return reconcile.Result{}, nil
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
	log.Info("Reconciling hosted cluster version conditions")

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
	log.Info("Finished reconciling hosted cluster version conditions")

	return nil
}
