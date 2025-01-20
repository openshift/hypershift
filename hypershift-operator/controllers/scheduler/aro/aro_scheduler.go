package aro

import (
	"context"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	aroScheduler       = "AROScheduler"
	schedulerFinalizer = "hypershift.openshift.io/aro-scheduler"
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
		Named(aroScheduler)
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
		log.Info("hostedcluster is deleted, cleaning up")
		if controllerutil.ContainsFinalizer(hc, schedulerFinalizer) {
			if controllerutil.ContainsFinalizer(hc, hostedcluster.HostedClusterFinalizer) {
				// Wait until the hosted cluster finalizer is removed
				return ctrl.Result{}, nil
			}
			// Ensure that any placeholder deployment is deleted
			controllerutil.RemoveFinalizer(hc, schedulerFinalizer)
			if err := r.Update(ctx, hc); err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}
	if !controllerutil.ContainsFinalizer(hc, schedulerFinalizer) {
		controllerutil.AddFinalizer(hc, schedulerFinalizer)
		if err := r.Update(ctx, hc); err != nil {
			return ctrl.Result{}, err
		}
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

	// Find existing dedicated serving content Nodes for this HC.
	dedicatedNodes := &corev1.NodeList{}
	if err := r.List(ctx, dedicatedNodes,
		client.HasLabels{hyperv1.RequestServingComponentLabel},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list nodes: %w", err)
	}

	sizeConfig := util.SizeConfiguration(&config, desiredSize)
	if sizeConfig == nil {
		return ctrl.Result{}, fmt.Errorf("could not find size configuration for size %s", desiredSize)
	}

	if err := r.updateAroHostedCluster(ctx, hc, desiredSize, &config); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *Scheduler) updateAroHostedCluster(ctx context.Context, hc *hyperv1.HostedCluster, size string, config *schedulingv1alpha1.ClusterSizingConfiguration) error {
	original := hc.DeepCopy()

	hc, err := util.UpdateHostedCluster(hc, size, config, nil)
	if err != nil {
		return fmt.Errorf("failed to update hostedcluster: %w", err)
	}

	if !equality.Semantic.DeepEqual(hc, original) {
		if err := r.Patch(ctx, hc, client.MergeFrom(original)); err != nil {
			return fmt.Errorf("failed to update hostedcluster: %w", err)
		}
	}
	return nil
}
