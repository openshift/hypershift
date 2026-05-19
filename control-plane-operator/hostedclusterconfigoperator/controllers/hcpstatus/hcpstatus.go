package hcpstatus

import (
	"context"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftv1beta1applyconfigurations "github.com/openshift/hypershift/client/applyconfiguration/hypershift/v1beta1"
	hypershiftclient "github.com/openshift/hypershift/client/clientset/clientset"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
	"github.com/openshift/hypershift/support/conditions"
	"github.com/openshift/hypershift/support/releaseinfo"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	metav1applyconfigurations "k8s.io/client-go/applyconfigurations/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const ControllerName = "hcpstatus"

var managedConditions = sets.New[string](
	string(hyperv1.ClusterVersionFailing),
	string(hyperv1.ClusterVersionReleaseAccepted),
	string(hyperv1.ClusterVersionRetrievedUpdates),
	string(hyperv1.ClusterVersionProgressing),
	string(hyperv1.ClusterVersionUpgradeable),
	string(hyperv1.ClusterVersionAvailable),
)

func Setup(ctx context.Context, opts *operator.HostedClusterConfigOperatorConfig) error {
	hypershiftClient, err := hypershiftclient.NewForConfig(opts.CPCluster.GetConfig())
	if err != nil {
		return fmt.Errorf("failed to create hypershift client: %w", err)
	}
	r := &hcpStatusReconciler{
		hcpName:             opts.HCPName,
		hcpNamespace:        opts.Namespace,
		hypershiftClient:    hypershiftClient,
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
	hcpName, hcpNamespace string
	hypershiftClient      hypershiftclient.Interface
	mgtClusterClient      crclient.Client
	hostedClusterClient   crclient.Client
	releaseProvider       releaseinfo.Provider
}

func (h *hcpStatusReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	hcp := &hyperv1.HostedControlPlane{}
	if err := h.mgtClusterClient.Get(ctx, req.NamespacedName, hcp); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to get hcp %s: %w", req, err)
	}

	statusCfg := hypershiftv1beta1applyconfigurations.HostedControlPlaneStatus()
	var reconcileErr error

	var clusterVersion configv1.ClusterVersion
	cvErr := h.hostedClusterClient.Get(ctx, crclient.ObjectKey{Name: "version"}, &clusterVersion)

	if cvErr == nil {
		versionStatus := hypershiftv1beta1applyconfigurations.ClusterVersionStatus().
			WithDesired(clusterVersion.Status.Desired).
			WithHistory(clusterVersion.Status.History...).
			WithObservedGeneration(clusterVersion.Status.ObservedGeneration).
			WithAvailableUpdates(clusterVersion.Status.AvailableUpdates...).
			WithConditionalUpdates(clusterVersion.Status.ConditionalUpdates...)
		ensureRequiredSlices(versionStatus)
		statusCfg.
			WithVersionStatus(versionStatus).
			//lint:ignore SA1019 populate the deprecated property until we can drop it in a later API version
			WithVersion(clusterVersion.Status.Desired.Version).
			//lint:ignore SA1019 populate the deprecated property until we can drop it in a later API version
			WithReleaseImage(clusterVersion.Status.Desired.Image)
	} else if hcp.Status.VersionStatus != nil {
		// Preserve existing version fields so that SSA doesn't drop them
		// when the ClusterVersion read fails transiently.
		versionStatus := hypershiftv1beta1applyconfigurations.ClusterVersionStatus().
			WithDesired(hcp.Status.VersionStatus.Desired).
			WithHistory(hcp.Status.VersionStatus.History...).
			WithObservedGeneration(hcp.Status.VersionStatus.ObservedGeneration).
			WithAvailableUpdates(hcp.Status.VersionStatus.AvailableUpdates...).
			WithConditionalUpdates(hcp.Status.VersionStatus.ConditionalUpdates...)
		ensureRequiredSlices(versionStatus)
		statusCfg.
			WithVersionStatus(versionStatus).
			//lint:ignore SA1019 populate the deprecated property until we can drop it in a later API version
			WithVersion(hcp.Status.VersionStatus.Desired.Version).
			//lint:ignore SA1019 populate the deprecated property until we can drop it in a later API version
			WithReleaseImage(hcp.Status.VersionStatus.Desired.Image)
	}

	cvoConditionTypes := map[hyperv1.ConditionType]*configv1.ClusterOperatorStatusCondition{
		hyperv1.ClusterVersionFailing:          findClusterOperatorStatusCondition(clusterVersion.Status.Conditions, "Failing"),
		hyperv1.ClusterVersionReleaseAccepted:  findClusterOperatorStatusCondition(clusterVersion.Status.Conditions, "ReleaseAccepted"),
		hyperv1.ClusterVersionRetrievedUpdates: findClusterOperatorStatusCondition(clusterVersion.Status.Conditions, configv1.RetrievedUpdates),
		hyperv1.ClusterVersionProgressing:      findClusterOperatorStatusCondition(clusterVersion.Status.Conditions, configv1.OperatorProgressing),
		hyperv1.ClusterVersionUpgradeable:      findClusterOperatorStatusCondition(clusterVersion.Status.Conditions, configv1.OperatorUpgradeable),
		hyperv1.ClusterVersionAvailable:        findClusterOperatorStatusCondition(clusterVersion.Status.Conditions, configv1.OperatorAvailable),
	}

	var conditionUpdates []*metav1applyconfigurations.ConditionApplyConfiguration
	for conditionType, condition := range cvoConditionTypes {
		applyCond := metav1applyconfigurations.Condition().
			WithType(string(conditionType)).
			WithObservedGeneration(hcp.Generation)

		if cvErr != nil {
			applyCond.
				WithStatus(metav1.ConditionUnknown).
				WithReason(hyperv1.StatusUnknownReason).
				WithMessage(fmt.Sprintf("failed to get clusterVersion: %v", cvErr))
		} else if condition == nil {
			status := metav1.ConditionUnknown
			reason := hyperv1.StatusUnknownReason
			if conditionType == hyperv1.ClusterVersionUpgradeable {
				status = metav1.ConditionTrue
				reason = hyperv1.FromClusterVersionReason
			}
			applyCond.
				WithStatus(status).
				WithReason(reason).
				WithMessage("Condition not found in the CVO.")
		} else {
			reason := condition.Reason
			if reason == "" {
				reason = hyperv1.FromClusterVersionReason
			}
			applyCond.
				WithStatus(metav1.ConditionStatus(condition.Status)).
				WithReason(reason).
				WithMessage(condition.Message)
		}

		existingCondition := findCondition(hcp.Status.Conditions, string(conditionType))
		if existingCondition != nil && existingCondition.Status == *applyCond.Status {
			applyCond.WithLastTransitionTime(existingCondition.LastTransitionTime)
		} else {
			applyCond.WithLastTransitionTime(metav1.NewTime(time.Now()))
		}

		conditionUpdates = append(conditionUpdates, applyCond)
	}

	statusCfg.WithConditions(conditions.SSAConditions(hcp.Status.Conditions, managedConditions, conditionUpdates...)...)

	var authentication configv1.Authentication
	if err := h.hostedClusterClient.Get(ctx, crclient.ObjectKey{Name: "cluster"}, &authentication); err != nil {
		reconcileErr = fmt.Errorf("failed to get Authentication resource \"cluster\": %w", err)
		// Preserve existing Configuration so that SSA doesn't drop a field
		// this manager previously owned.
		if hcp.Status.Configuration != nil {
			statusCfg.WithConfiguration(
				hypershiftv1beta1applyconfigurations.ConfigurationStatus().
					WithAuthentication(sanitizeAuthenticationStatus(hcp.Status.Configuration.Authentication)),
			)
		}
	} else {
		statusCfg.WithConfiguration(
			hypershiftv1beta1applyconfigurations.ConfigurationStatus().
				WithAuthentication(sanitizeAuthenticationStatus(authentication.Status)),
		)
	}

	cfg := hypershiftv1beta1applyconfigurations.HostedControlPlane(h.hcpName, h.hcpNamespace)
	cfg.Status = statusCfg
	log.Info("Applying HCP status with configuration and version status")
	if _, err := h.hypershiftClient.HypershiftV1beta1().HostedControlPlanes(h.hcpNamespace).ApplyStatus(
		ctx, cfg, metav1.ApplyOptions{FieldManager: ControllerName, Force: true},
	); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to apply hcp status: %w", err)
	}
	log.Info("Successfully applied HCP status")

	if reconcileErr != nil {
		return reconcile.Result{}, reconcileErr
	}
	return reconcile.Result{}, nil
}

// ensureRequiredSlices ensures that +required slice fields on ClusterVersionStatus
// are non-nil. The generated apply configuration uses omitempty, so a nil slice is
// omitted from the JSON payload — but the API server rejects a missing +required
// field within a parent struct that IS present.
func ensureRequiredSlices(vs *hypershiftv1beta1applyconfigurations.ClusterVersionStatusApplyConfiguration) {
	if vs.AvailableUpdates == nil {
		vs.AvailableUpdates = []configv1.Release{}
	}
}

// sanitizeAuthenticationStatus returns a copy of the AuthenticationStatus with nil
// slices replaced by empty slices. The OIDCClients field uses json:"oidcClients"
// without omitempty, so a nil slice serializes as null which the API server rejects.
func sanitizeAuthenticationStatus(status configv1.AuthenticationStatus) configv1.AuthenticationStatus {
	if status.OIDCClients == nil {
		status.OIDCClients = []configv1.OIDCClientStatus{}
	}
	return status
}

func findClusterOperatorStatusCondition(conditions []configv1.ClusterOperatorStatusCondition, conditionType configv1.ClusterStatusConditionType) *configv1.ClusterOperatorStatusCondition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
