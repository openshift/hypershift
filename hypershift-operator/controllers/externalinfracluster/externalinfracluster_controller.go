package externalinfracluster

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/thirdparty/clusterapi/util"
	"github.com/openshift/hypershift/thirdparty/clusterapi/util/patch"
)

type ExternalInfraClusterReconciler struct {
	ctrlclient.Client
	recorder record.EventRecorder
	Infra    *configv1.Infrastructure
	Log      logr.Logger
}

func (r *ExternalInfraClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// TODO (alberto): watch hostedControlPlane events too.
	// So when controlPlane.Status.Ready it triggers a reconcile here.
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.ExternalInfraCluster{}).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
		}).
		Build(r)
	if err != nil {
		return errors.Wrap(err, "failed setting up with a controller manager")
	}

	var infra configv1.Infrastructure
	if err := mgr.GetAPIReader().Get(context.Background(), client.ObjectKey{Name: "cluster"}, &infra); err != nil {
		return fmt.Errorf("failed to get cluster infra: %w", err)
	}
	r.Infra = &infra

	r.recorder = mgr.GetEventRecorderFor("external-infra-controller")

	return nil
}

func (r *ExternalInfraClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Log = ctrl.LoggerFrom(ctx)
	r.Log.Info("Reconciling")

	// Fetch the ExternalInfraCluster instance
	externalInfraCluster := &hyperv1.ExternalInfraCluster{}
	err := r.Client.Get(ctx, req.NamespacedName, externalInfraCluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			r.Log.Info("ExternalInfraCluster not found")
			return ctrl.Result{}, nil
		}
		r.Log.Error(err, "error getting ExternalInfraCluster")
		return ctrl.Result{}, err
	}

	// Fetch the Cluster.
	cluster, err := util.GetOwnerCluster(ctx, r.Client, externalInfraCluster.ObjectMeta)
	if err != nil {
		r.Log.Error(err, "error getting owner cluster")
		return ctrl.Result{}, err
	}
	if cluster == nil {
		r.Log.Info("Cluster Controller has not yet set OwnerRef")
		return ctrl.Result{}, nil
	}

	if util.IsPaused(cluster, externalInfraCluster) {
		r.Log.Info("ExternalInfraCluster or linked Cluster is marked as paused. Won't reconcile")
		return ctrl.Result{}, nil
	}

	// Return early if deleted
	if !externalInfraCluster.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	r.Log = r.Log.WithValues("cluster", cluster.Name)

	patchHelper, err := patch.NewHelper(externalInfraCluster, r.Client)
	if err != nil {
		r.Log.Error(err, "error building patchHelper")
		return ctrl.Result{}, err
	}

	hcp := &hyperv1.HostedControlPlane{}
	controlPlaneRef := types.NamespacedName{
		Name:      cluster.Spec.ControlPlaneRef.Name,
		Namespace: cluster.Namespace,
	}

	if err := r.Client.Get(ctx, controlPlaneRef, hcp); err != nil {
		r.Log.Error(err, "failed to get control plane ref")
		return reconcile.Result{}, err
	}

	// TODO (alberto): populate the API and create/consume infrastructure via aws sdk
	// role profile, sg, vpc, subnets.
	if !hcp.Status.Ready {
		r.Log.Info("Control plane is not ready yet. Requeuing")
		return reconcile.Result{Requeue: true}, nil
	}

	// Set the values for upper level controller
	externalInfraCluster.Status.Ready = true
	externalInfraCluster.Spec.ControlPlaneEndpoint = hyperv1.APIEndpoint{
		Host: hcp.Status.ControlPlaneEndpoint.Host,
		Port: hcp.Status.ControlPlaneEndpoint.Port,
	}

	if err := patchHelper.Patch(ctx, externalInfraCluster); err != nil {
		r.Log.Error(err, "failed to patch")
		return ctrl.Result{}, fmt.Errorf("failed to patch: %w", err)
	}

	r.Log.Info("Successfully reconciled")
	return ctrl.Result{}, nil
}
