package nodecount

import (
	"context"
	"fmt"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftv1beta1applyconfigurations "github.com/openshift/hypershift/client/applyconfiguration/hypershift/v1beta1"
	hypershiftclient "github.com/openshift/hypershift/client/clientset/clientset"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type reconciler struct {
	hcpName, hcpNamespace string
	client                hypershiftclient.Interface
	lister                client.Client

	guestClusterClient client.Client
}

func (r *reconciler) Reconcile(ctx context.Context, _ reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling")

	var hcp hypershiftv1beta1.HostedControlPlane
	if err := r.lister.Get(ctx, client.ObjectKey{
		Namespace: r.hcpNamespace,
		Name:      r.hcpName,
	}, &hcp); err != nil {
		return reconcile.Result{}, err
	}
	if isPaused, duration := util.IsReconciliationPaused(log, hcp.Spec.PausedUntil); isPaused {
		log.Info("Reconciliation paused", "pausedUntil", *hcp.Spec.PausedUntil)
		return ctrl.Result{
			RequeueAfter: duration,
		}, nil
	}
	if hcp.DeletionTimestamp != nil {
		return reconcile.Result{}, nil
	}

	nodes := &corev1.NodeList{}
	if err := r.guestClusterClient.List(ctx, nodes); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Nodes: %w", err)
	}

	cfg := hypershiftv1beta1applyconfigurations.HostedControlPlane(r.hcpName, r.hcpNamespace)
	cfg.Status = hypershiftv1beta1applyconfigurations.HostedControlPlaneStatus().WithNodeCount(len(nodes.Items))
	_, err := r.client.HypershiftV1beta1().HostedControlPlanes(r.hcpNamespace).ApplyStatus(ctx, cfg, metav1.ApplyOptions{FieldManager: ControllerName})
	return reconcile.Result{}, err
}
