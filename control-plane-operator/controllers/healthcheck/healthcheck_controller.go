package healthcheck

import (
	"context"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
)

const (
	successRequeueInterval = 5 * time.Minute
	failureRequeueInterval = 30 * time.Second
)

type HealthCheckUpdater struct {
	client.Client
	HostedControlPlane client.ObjectKey

	log logr.Logger
}

func (hcu *HealthCheckUpdater) SetupWithManager(mgr ctrl.Manager) error {
	return mgr.Add(hcu)
}

func (hcu *HealthCheckUpdater) Start(ctx context.Context) error {
	hcu.log = ctrl.LoggerFrom(ctx).WithName("health-check-updater")
	hcu.log.Info("Starting health check updater")
	ticker := time.NewTicker(failureRequeueInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			err := hcu.update(ctx)
			if err != nil {
				hcu.log.Error(err, "Failure occurred during health checks")
				ticker.Reset(failureRequeueInterval)
			} else {
				hcu.log.Info("Health checks succeeded")
				ticker.Reset(successRequeueInterval)
			}
		}
	}
}

func (hcu *HealthCheckUpdater) update(ctx context.Context) error {
	hcu.log.Info("Updating health checks")

	hostedControlPlane := &hyperv1.HostedControlPlane{}
	err := hcu.Client.Get(ctx, hcu.HostedControlPlane, hostedControlPlane)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get HostedControlPlane: %w", err)
	}

	// Return early if deleting
	if !hostedControlPlane.DeletionTimestamp.IsZero() {
		return nil
	}

	originalHostedControlPlane := hostedControlPlane.DeepCopy()

	errs := []error{}

	// Call generic health checks

	// Call platform-specific health checks
	if hostedControlPlane.Spec.Platform.Type == hyperv1.AWSPlatform {
		// This is the best effort ping to the identity provider
		// that enables access from the operator to the cloud provider resources.
		if err := awsHealthCheckIdentityProvider(ctx, hostedControlPlane); err != nil {
			errs = append(errs, err)
		}

	}

	// Update the status
	if err := hcu.Client.Status().Patch(ctx, hostedControlPlane, client.MergeFromWithOptions(originalHostedControlPlane, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	err = utilerrors.NewAggregate(errs)
	if len(errs) > 0 {
		return fmt.Errorf("some health checks failed: %w", err)
	}
	return nil
}
