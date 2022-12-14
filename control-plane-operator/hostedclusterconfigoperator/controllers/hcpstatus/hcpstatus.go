package hcpstatus

import (
	"context"
	"fmt"
	"reflect"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
	"github.com/openshift/hypershift/support/releaseinfo"
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

func Setup(opts *operator.HostedClusterConfigOperatorConfig) error {
	r := &hcpStatusReconciler{
		mgtClusterClient:    opts.CPCluster.GetClient(),
		hostedClusterClient: opts.Manager.GetClient(),
		releaseProvider:     opts.ReleaseProvider,
	}
	c, err := controller.New(ControllerName, opts.Manager, controller.Options{Reconciler: r})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}
	if err := c.Watch(source.NewKindWithCache(&hyperv1.HostedControlPlane{}, opts.CPCluster.GetCache()), &handler.EnqueueRequestForObject{}); err != nil {
		return fmt.Errorf("failed to watch HCP: %w", err)
	}

	clusterVersionMapper := func(crclient.Object) []reconcile.Request {
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: opts.Namespace, Name: opts.HCPName}}}
	}
	if err := c.Watch(&source.Kind{Type: &configv1.ClusterVersion{}}, handler.EnqueueRequestsFromMapFunc(clusterVersionMapper)); err != nil {
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
	failingCondition := func() metav1.Condition {
		if err != nil {
			return metav1.Condition{
				Type:    string(hyperv1.ClusterVersionFailing),
				Status:  metav1.ConditionUnknown,
				Reason:  hyperv1.StatusUnknownReason,
				Message: fmt.Sprintf("failed to get clusterversion: %v", err),
			}
		}
		message := ""
		for _, cond := range clusterVersion.Status.Conditions {
			if cond.Type == "Failing" {
				if cond.Status == configv1.ConditionTrue {
					return metav1.Condition{
						Type:    string(hyperv1.ClusterVersionFailing),
						Status:  metav1.ConditionTrue,
						Reason:  cond.Reason,
						Message: cond.Message,
					}
				}
			}
			if cond.Type == "Progressing" {
				message = cond.Message
			}
		}
		return metav1.Condition{
			Type:    string(hyperv1.ClusterVersionFailing),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.AsExpectedReason,
			Message: message,
		}
	}()
	upgradeableCondition := func() metav1.Condition {
		if err != nil {
			return metav1.Condition{
				Type:    string(hyperv1.ClusterVersionUpgradeable),
				Status:  metav1.ConditionUnknown,
				Reason:  hyperv1.StatusUnknownReason,
				Message: fmt.Sprintf("failed to get clusterversion: %v", err),
			}
		}
		for _, cond := range clusterVersion.Status.Conditions {
			if cond.Type == configv1.OperatorUpgradeable {
				if cond.Status == configv1.ConditionFalse {
					return metav1.Condition{
						Type:    string(hyperv1.ClusterVersionUpgradeable),
						Status:  metav1.ConditionFalse,
						Reason:  cond.Reason,
						Message: cond.Message,
					}
				}
			}
		}
		return metav1.Condition{
			Type:   string(hyperv1.ClusterVersionUpgradeable),
			Status: metav1.ConditionTrue,
			Reason: hyperv1.AsExpectedReason,
		}
	}()
	failingCondition.ObservedGeneration = hcp.Generation
	meta.SetStatusCondition(&hcp.Status.Conditions, failingCondition)
	upgradeableCondition.ObservedGeneration = hcp.Generation
	meta.SetStatusCondition(&hcp.Status.Conditions, upgradeableCondition)
	log.Info("Finished reconciling hosted cluster version conditions")

	return nil
}
