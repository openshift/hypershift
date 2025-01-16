package azure

import (
	"context"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	schedulerutil "github.com/openshift/hypershift/hypershift-operator/controllers/scheduler/util"
	"github.com/openshift/hypershift/support/util"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	azureScheduler = "AzureScheduler"
)

type Scheduler struct {
	client.Client
}

func (r *Scheduler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedCluster{}).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 1,
		}).
		Watches(&schedulingv1alpha1.ClusterSizingConfiguration{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
			hostedClusters := &hyperv1.HostedClusterList{}
			if err := r.List(ctx, hostedClusters); err != nil {
				return nil
			}
			var out []reconcile.Request
			for _, hc := range hostedClusters.Items {
				out = append(out, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: hc.Namespace, Name: hc.Name}})
			}
			return out
		})).
		Named(azureScheduler)
	return builder.Complete(r)
}

func (r *Scheduler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	hc := &hyperv1.HostedCluster{}
	log := ctrl.LoggerFrom(ctx)
	err := r.Get(ctx, req.NamespacedName, hc)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("hostedcluster not found, aborting reconcile")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get cluster %q: %w", req.NamespacedName, err)
	}

	if !hc.DeletionTimestamp.IsZero() {
		log.Info("hostedcluster is being deleted, aborting reconcile")
		return ctrl.Result{}, nil
	}

	isPaused, duration, err := util.ProcessPausedUntilField(hc.Spec.PausedUntil, time.Now())
	if err != nil {
		log.Error(err, "error processing hosted cluster paused field")
		return ctrl.Result{}, nil // user needs to reformat the field, returning error is useless
	}
	if isPaused {
		log.Info("Reconciliation paused", "pausedUntil", *hc.Spec.PausedUntil)
		return ctrl.Result{RequeueAfter: duration}, nil
	}

	desiredSize := hc.Labels[hyperv1.HostedClusterSizeLabel]
	if desiredSize == "" {
		log.Info("HostedCluster does not have a size label, skipping for now")
		return ctrl.Result{}, nil
	}
	log = log.WithValues("desiredSize", desiredSize)

	config := schedulingv1alpha1.ClusterSizingConfiguration{}
	if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, &config); err != nil {
		return ctrl.Result{}, fmt.Errorf("could not get cluster sizing configuration: %w", err)
	}

	if condition := meta.FindStatusCondition(config.Status.Conditions, schedulingv1alpha1.ClusterSizingConfigurationValidType); condition == nil || condition.Status != metav1.ConditionTrue {
		log.Info("Cluster sizing configuration is not valid, skipping for now")
		return ctrl.Result{}, nil
	}

	sizeConfig := schedulerutil.SizeConfiguration(&config, desiredSize)
	if sizeConfig == nil {
		return ctrl.Result{}, fmt.Errorf("could not find size configuration for size %s", desiredSize)
	}

	if err := schedulerutil.UpdateHostedCluster(ctx, r.Client, hc, desiredSize, &config, nil); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}
