package nodecount

import (
	"context"
	"fmt"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftv1beta1applyconfigurations "github.com/openshift/hypershift/client/applyconfiguration/hypershift/v1beta1"
	hypershiftclient "github.com/openshift/hypershift/client/clientset/clientset"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
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
	if hcp.ObjectMeta.DeletionTimestamp != nil {
		return reconcile.Result{}, nil
	}

	nodes := &corev1.NodeList{}
	if err := r.guestClusterClient.List(ctx, nodes); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Nodes: %w", err)
	}

	statusCfg := hypershiftv1beta1applyconfigurations.HostedControlPlaneStatus().WithNodeCount(len(nodes.Items))

	if karpenterutil.IsKarpenterEnabled(hcp.Spec.AutoNode) {
		autoNodeStatus, err := r.reconcileAutoNodeStatus(ctx, nodes.Items)
		if err != nil {
			return ctrl.Result{}, err
		}
		statusCfg = statusCfg.WithAutoNode(autoNodeStatus)
	}

	cfg := hypershiftv1beta1applyconfigurations.HostedControlPlane(r.hcpName, r.hcpNamespace)
	cfg.Status = statusCfg
	_, err := r.client.HypershiftV1beta1().HostedControlPlanes(r.hcpNamespace).ApplyStatus(ctx, cfg, metav1.ApplyOptions{FieldManager: ControllerName})
	return reconcile.Result{}, err
}

// reconcileAutoNodeStatus builds the AutoNodeStatus from the current state of Karpenter-managed
// nodes and NodeClaims in the guest cluster.
func (r *reconciler) reconcileAutoNodeStatus(ctx context.Context, allNodes []corev1.Node) (*hypershiftv1beta1applyconfigurations.AutoNodeStatusApplyConfiguration, error) {
	log := ctrl.LoggerFrom(ctx)

	karpenterNodeCount := 0
	for i := range allNodes {
		if _, hasLabel := allNodes[i].Labels[karpenterv1.NodePoolLabelKey]; hasLabel {
			karpenterNodeCount++
		}
	}

	nodeClaims := &karpenterv1.NodeClaimList{}
	if err := r.guestClusterClient.List(ctx, nodeClaims); err != nil {
		if meta.IsNoMatchError(err) {
			// Karpenter CRDs may not be installed yet; degrade gracefully.
			log.Info("NodeClaim CRD not available, skipping NodeClaim count")
			return hypershiftv1beta1applyconfigurations.AutoNodeStatus().
				WithNodeCount(karpenterNodeCount), nil
		}
		return nil, fmt.Errorf("failed to list NodeClaims: %w", err)
	}

	return hypershiftv1beta1applyconfigurations.AutoNodeStatus().
		WithNodeCount(karpenterNodeCount).
		WithNodeClaimCount(len(nodeClaims.Items)), nil
}
