package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hyperv1 "openshift.io/hypershift/api/v1alpha1"
	"openshift.io/hypershift/hypershift-operator/releaseinfo"
)

type HostedControlPlaneReconciler struct {
	client.Client
	Log                             logr.Logger
	Infra                           *configv1.Infrastructure
	recorder                        record.EventRecorder
	LookupControlPlaneOperatorImage func(kubeClient client.Client) (string, error)
	ReleaseProvider                 releaseinfo.Provider
}

func (r *HostedControlPlaneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedControlPlane{}).
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

	r.recorder = mgr.GetEventRecorderFor("hosted-control-plane-controller")

	return nil
}

func (r *HostedControlPlaneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Log = ctrl.LoggerFrom(ctx)
	r.Log.Info("Reconciling")

	// Fetch the hostedControlPlane instance
	hostedControlPlane := &hyperv1.HostedControlPlane{}
	err := r.Client.Get(ctx, req.NamespacedName, hostedControlPlane)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Fetch the Cluster.
	cluster, err := util.GetOwnerCluster(ctx, r.Client, hostedControlPlane.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}

	if cluster == nil {
		r.Log.Info("Cluster Controller has not yet set OwnerRef")
		return ctrl.Result{}, nil
	}

	if util.IsPaused(cluster, hostedControlPlane) {
		r.Log.Info("HostedControlPlane or linked Cluster is marked as paused. Won't reconcile")
		return ctrl.Result{}, nil
	}

	// Return early if deleted
	if !hostedControlPlane.DeletionTimestamp.IsZero() {
		if err := r.delete(ctx, req.Name); err != nil {
			r.Log.Error(err, "failed to delete cluster")
			return ctrl.Result{}, err
		}
		if controllerutil.ContainsFinalizer(hostedControlPlane, finalizer) {
			controllerutil.RemoveFinalizer(hostedControlPlane, finalizer)
			if err := r.Update(ctx, hostedControlPlane); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from cluster: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure the hostedControlPlane has a finalizer for cleanup
	if !controllerutil.ContainsFinalizer(hostedControlPlane, finalizer) {
		controllerutil.AddFinalizer(hostedControlPlane, finalizer)
		if err := r.Update(ctx, hostedControlPlane); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to hostedControlPlane: %w", err)
		}
	}

	r.Log = r.Log.WithValues("cluster", cluster.Name)

	patchHelper, err := patch.NewHelper(hostedControlPlane, r.Client)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to init patch helper: %w", err)
	}

	var result ctrl.Result
	// TODO (alberto):
	// May be eventually just run a deployment with a CVO running a hostedControlPlane profile
	// passing the hostedControlPlane.spec.version through?

	if hostedControlPlane.Status.Ready {
		r.Log.Info("Is ready")
		return result, nil
	}

	// First, set up infrastructure
	infraStatus, err := r.ensureInfrastructure(ctx, hostedControlPlane)
	if err != nil {
		r.Log.Error(err, "failed to ensure infrastructure")
		return result, fmt.Errorf("failed to ensure infrastructure: %w", err)
	}

	// Wait for things like LB services to become available
	if !infraStatus.IsReady() {
		result.RequeueAfter = 5 * time.Second
		r.Log.Info("Cluster infrastructure is still provisioning, will try again later")
		return result, nil
	}
	hostedControlPlane.Status.ControlPlaneEndpoint = hyperv1.APIEndpoint{
		Host: infraStatus.APIAddress,
		Port: APIServerPort,
	}

	releaseImage, err := r.ReleaseProvider.Lookup(ctx, hostedControlPlane.Spec.ReleaseImage)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to look up release info: %w", err)
	}
	componentVersions, err := releaseImage.ComponentVersions()
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("invalid component versions found in release info: %w", err)
	}
	r.Log.Info("found release info for image", "releaseImage", hostedControlPlane.Spec.ReleaseImage, "info", releaseImage, "componentImages", releaseImage.ComponentImages(), "componentVersions", componentVersions)
	// Install the control plane into the infrastructure
	err = r.ensureControlPlane(ctx, hostedControlPlane, infraStatus, releaseImage)
	if err != nil {
		r.Log.Error(err, "failed to ensure control plane")
		return result, fmt.Errorf("failed to ensure control plane: %w", err)
	}

	hostedControlPlane.Status.Ready = true
	if err := patchHelper.Patch(ctx, hostedControlPlane); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to patch: %w", err)
	}

	r.Log.Info("Successfully reconciled")
	return ctrl.Result{}, nil
}

func (r *HostedControlPlaneReconciler) delete(ctx context.Context, name string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	if err := waitForDeletion(ctx, r.Log, r.Client, ns); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete namespace: %w", err)
	}
	r.Log.Info("deleted namespace", "name", name)

	machineSetConfig := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-machine-api",
			Name:      fmt.Sprintf("%s-user-data", name),
		},
	}
	if err := waitForDeletion(ctx, r.Log, r.Client, machineSetConfig); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete machineset secret %s: %w", machineSetConfig.Name, err)
	}
	r.Log.Info("deleted machineset secret", "name", machineSetConfig.Name)

	return nil
}
