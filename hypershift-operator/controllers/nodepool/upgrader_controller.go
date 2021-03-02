package nodepool

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	clusterv1 "github.com/openshift/hypershift/thirdparty/clusterapi/api/v1alpha4"
	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type UpgraderReconciler struct {
	ctrlclient.Client
	recorder record.EventRecorder
	Log      logr.Logger
}

func (r *UpgraderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedCluster{}).
		Watches(&source.Kind{Type: &hyperv1.NodePool{}}, &handler.EnqueueRequestForOwner{
			OwnerType:    &hyperv1.HostedCluster{},
			IsController: false,
		}).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
		}).
		Build(r)
	if err != nil {
		return errors.Wrap(err, "failed setting up with a controller manager")
	}

	r.recorder = mgr.GetEventRecorderFor("upgrader-controller")

	return nil
}

// DeploymentComplete considers a deployment to be complete once all of its desired replicas
// are updated and available, and no old machines are running.
func MachineDeploymentComplete(deployment *clusterv1.MachineDeployment) bool {
	newStatus := &deployment.Status
	return newStatus.UpdatedReplicas == *(deployment.Spec.Replicas) &&
		newStatus.Replicas == *(deployment.Spec.Replicas) &&
		newStatus.AvailableReplicas == *(deployment.Spec.Replicas) &&
		newStatus.ObservedGeneration >= deployment.Generation
}

func (r *UpgraderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Log = ctrl.LoggerFrom(ctx).WithName("NodePoolUpgrader")
	r.Log.Info("Reconciling")

	// Fetch the hostedCluster instance
	hostedCluster := &hyperv1.HostedCluster{}
	err := r.Client.Get(ctx, req.NamespacedName, hostedCluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			r.Log.Info("not found")
			return ctrl.Result{}, nil
		}
		r.Log.Error(err, "error getting nodePool")
		return ctrl.Result{}, err
	}

	// Ignore deleted nodePools, this can happen when foregroundDeletion
	// is enabled
	if !hostedCluster.DeletionTimestamp.IsZero() {
		r.Log.Info("hostedCluster is being deleted", "hostedCluster", hostedCluster.GetName())
		return ctrl.Result{}, nil
	}

	// Get the targetVersion from the hostedCluster
	if hostedCluster.Status.Version == nil {
		r.Log.Info("Skipping. hostedCluster has no version yet")
		return ctrl.Result{}, nil
	}
	targetVersion := hostedCluster.Status.Version.History[0].Version
	r.Log.Info("AutoUpgrader targeting hostedCluster version", "targetVersion", targetVersion)

	nodePoolList := &hyperv1.NodePoolList{}
	if err := r.List(ctx, nodePoolList); err != nil {
		return ctrl.Result{}, err
	}

	// Drop nodePools that are already targeting the targetedVersion
	allNodePools := nodePoolList.Items
	var nodePoolsOutdated []hyperv1.NodePool
	var nodePoolToUpgrade []hyperv1.NodePool

	// Check if there is a nodePool currently being upgraded
	for k := range allNodePools {
		if isUpgrading(&allNodePools[k]) {
			nodePoolToUpgrade = append(nodePoolToUpgrade, allNodePools[k])
		}
	}
	if len(nodePoolToUpgrade) > 1 {
		r.Log.Info("More than one nodePool are being upgraded. Skipping")
		return ctrl.Result{}, nil
	}

	if len(nodePoolToUpgrade) == 1 {
		r.Log.Info("Auto upgrade for nodePools is in progress",
			"nodePool", nodePoolToUpgrade[0].GetName())

		return ctrl.Result{Requeue: true}, nil
	}

	// Check if there are nodePools not pointing to the targetVersion
	for k := range allNodePools {
		if allNodePools[k].Spec.Version != targetVersion {
			nodePoolsOutdated = append(nodePoolsOutdated, allNodePools[k])
		}
	}
	if len(nodePoolsOutdated) == 0 {
		r.Log.Info("All nodePools are upgraded to the targetVersion",
			"targetVersion", targetVersion)
		return ctrl.Result{}, nil
	}

	// if there's outdated nodePools but none is being upgraded, pick one.
	if len(nodePoolToUpgrade) == 0 {
		nodePoolToUpgrade = append(nodePoolToUpgrade, nodePoolsOutdated[0])
	}

	// Set the targetVersion to trigger the machineDeployment rolling upgrade
	nodePoolToUpgrade[0].Spec.Version = targetVersion
	r.Log.Info("Setting new target version for nodePool",
		"nodePool", nodePoolToUpgrade[0].GetName(), "targetVersion", targetVersion)
	if err := r.Client.Update(ctx, &nodePoolToUpgrade[0]); err != nil {
		return ctrl.Result{}, err
	}

	r.Log.Info("Successfully reconciled")
	return ctrl.Result{}, nil
}

func isUpgrading(nodePool *hyperv1.NodePool) bool {
	return nodePool.Spec.Version != nodePool.Status.Version
}
