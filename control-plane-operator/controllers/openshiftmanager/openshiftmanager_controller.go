package openshiftmanager

import (
	ctrl "sigs.k8s.io/controller-runtime"
)

// Reconciler is responsible for integration with OpenShiftManager.
// This controller is experimental and will be disabled by default.
//
// For more information on OpenShiftManager, see
// https://github.com/openshift/enhancements/blob/master/dev-guide/multi-operator-manager.md.
type Reconciler struct {
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	inputResInitializer := newInputResourceInitializer(mgr.GetRESTMapper(), mgr.GetCache())
	return mgr.Add(inputResInitializer)
}
