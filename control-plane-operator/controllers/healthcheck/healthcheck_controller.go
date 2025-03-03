package healthcheck

import (
	"context"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
)

const (
	invalidRequeueInterval = 1 * time.Minute
	validRequeueInterval   = 5 * time.Minute
)

type HealthCheckReconciler struct {
	client.Client

	Log logr.Logger
}

func (r *HealthCheckReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedControlPlane{})
	if _, err := b.Build(r); err != nil {
		return fmt.Errorf("failed setting up with a controller manager %w", err)
	}

	return nil
}

func (r *HealthCheckReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Log = ctrl.LoggerFrom(ctx)
	r.Log.Info("Updating health check status")

	// Fetch the hostedControlPlane instance
	hostedControlPlane := &hyperv1.HostedControlPlane{}
	err := r.Client.Get(ctx, req.NamespacedName, hostedControlPlane)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		r.Log.Error(err, "failed to get hostedControlPlane")
		// do not return error here, we want to requeue
		return ctrl.Result{RequeueAfter: invalidRequeueInterval}, nil
	}
	originalHostedControlPlane := hostedControlPlane.DeepCopy()

	// Call generic health checks

	// Call platform-specific health checks
	if hostedControlPlane.Spec.Platform.Type == hyperv1.AWSPlatform {
		// This is the best effort ping to the identity provider
		// that enables access from the operator to the cloud provider resources.
		awsHealthCheckIdentityProvider(ctx, hostedControlPlane)
	}

	// Update the hostedControlPlane status
	if err := r.Client.Status().Patch(ctx, hostedControlPlane, client.MergeFromWithOptions(originalHostedControlPlane, client.MergeFromWithOptimisticLock{})); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	return ctrl.Result{RequeueAfter: validRequeueInterval}, nil
}
