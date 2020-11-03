package clusterversion

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"

	configclient "github.com/openshift/client-go/config/clientset/versioned"
	configlister "github.com/openshift/client-go/config/listers/config/v1"
)

type ClusterVersionReconciler struct {
	Client configclient.Interface
	Lister configlister.ClusterVersionLister
	Log    logr.Logger
}

func (r *ClusterVersionReconciler) Reconcile(_ context.Context, req ctrl.Request) (ctrl.Result, error) {
	clusterVersion, err := r.Lister.Get(req.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("cannot fetch cluster version %s: %v", req.Name, err)
	}
	updateNeeded := false
	// Always default to empty upstream
	if len(clusterVersion.Spec.Upstream) > 0 {
		clusterVersion.Spec.Upstream = ""
		updateNeeded = true
	}
	// Always default to empty channel
	if len(clusterVersion.Spec.Channel) > 0 {
		clusterVersion.Spec.Channel = ""
		updateNeeded = true
	}
	// Remove any attempt at changing the clusterVersion
	if clusterVersion.Spec.DesiredUpdate != nil {
		clusterVersion.Spec.DesiredUpdate = nil
		updateNeeded = true
	}
	if updateNeeded {
		r.Log.Info("Updating clusterversion resource to desired values")
		_, err := r.Client.ConfigV1().ClusterVersions().Update(context.TODO(), clusterVersion, metav1.UpdateOptions{})
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}
