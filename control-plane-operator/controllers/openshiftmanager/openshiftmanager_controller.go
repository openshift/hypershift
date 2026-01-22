package openshiftmanager

import (
	"context"
	"fmt"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// Reconciler is responsible for integration with OpenShiftManager.
// This controller is experimental and will be disabled by default.
//
// For more information on OpenShiftManager, see
// https://github.com/openshift/enhancements/blob/master/dev-guide/multi-operator-manager.md.
type Reconciler struct {
}

func (r *Reconciler) Reconcile(ctx context.Context, operatorName string) (ctrl.Result, error) {
	return ctrl.Result{}, fmt.Errorf("not implemented")
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	inputResInitializer := newInputResourceInitializer(mgr.GetRESTMapper(), mgr.GetCache())

	omCtrl, err := controller.NewTyped[string]("openshift-manager", mgr, controller.TypedOptions[string]{Reconciler: r})
	if err != nil {
		return err
	}

	channelSource := source.TypedChannel[string, string](inputResInitializer.ResultChan(), handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, operatorName string) []string {
		return []string{operatorName}
	}))

	// TODO wire channelSource
	if err = omCtrl.Watch(...); err != nil {
		return err
	}
	return mgr.Add(inputResInitializer)
}
