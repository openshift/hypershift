package node

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

const fieldOwner = "karpenter-node-controller"

type Reconciler struct {
	GuestClient client.Client
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.GuestClient = mgr.GetClient()

	return ctrl.NewControllerManagedBy(mgr).
		Named("karpenter-node-controller").
		// Watch Nodes in the guest cluster (main manager).
		For(&corev1.Node{}).
		WithEventFilter(karpenterNodePredicate()).
		Complete(r)
}

func karpenterNodePredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		node, ok := obj.(*corev1.Node)
		if !ok {
			return false
		}
		return isKarpenterNode(node)
	})
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	node := &corev1.Node{}
	if err := r.GuestClient.Get(ctx, req.NamespacedName, node); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("failed to get Node: %w", err)
	}

	if !isKarpenterNode(node) {
		return reconcile.Result{}, nil
	}

	nodeClaim, err := findNodeClaimForNode(ctx, r.GuestClient, node.Name)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to find NodeClaim for Node %q: %w", node.Name, err)
	}
	if nodeClaim == nil {
		log.Info("NodeClaim not found for Node, skipping")
		return reconcile.Result{}, nil
	}

	nodePoolName, err := nodePoolNameFromNodeClaim(nodeClaim)
	if err != nil {
		log.Error(err, "failed to get nodePool name from NodeClaim")
		return reconcile.Result{}, nil
	}

	if node.Labels[hyperv1.NodePoolLabel] == nodePoolName {
		return reconcile.Result{}, nil
	}

	if err := r.applyNodePoolLabel(ctx, node.Name, nodePoolName); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to apply nodePool label: %w", err)
	}

	log.Info("Applied nodePool label", "nodePool", nodePoolName)
	return reconcile.Result{}, nil
}

func (r *Reconciler) applyNodePoolLabel(ctx context.Context, nodeName, nodePoolName string) error {
	applyNode := &corev1.Node{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Node",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Labels: map[string]string{
				hyperv1.NodePoolLabel: nodePoolName,
			},
		},
	}

	return r.GuestClient.Patch(ctx, applyNode, client.Apply, client.FieldOwner(fieldOwner), client.ForceOwnership)
}

func isKarpenterNode(node *corev1.Node) bool {
	if node == nil || node.Labels == nil {
		return false
	}
	_, ok := node.Labels[karpenterv1.NodePoolLabelKey]
	return ok
}

func findNodeClaimForNode(ctx context.Context, c client.Client, nodeName string) (*karpenterv1.NodeClaim, error) {
	nodeClaimList := &karpenterv1.NodeClaimList{}
	if err := c.List(ctx, nodeClaimList); err != nil {
		return nil, fmt.Errorf("failed to list NodeClaims: %w", err)
	}

	for i := range nodeClaimList.Items {
		if nodeClaimList.Items[i].Status.NodeName == nodeName {
			return &nodeClaimList.Items[i], nil
		}
	}

	return nil, nil
}

func nodePoolNameFromNodeClaim(nodeClaim *karpenterv1.NodeClaim) (string, error) {
	if nodeClaim.Spec.NodeClassRef == nil || nodeClaim.Spec.NodeClassRef.Name == "" {
		return "", fmt.Errorf("NodeClaim %q is missing nodeClassRef name", nodeClaim.Name)
	}

	// OpenshiftEC2NodeClass and EC2NodeClass share the same name in AutoNode.
	return karpenterutil.KarpenterNodePoolName(&hyperkarpenterv1.OpenshiftEC2NodeClass{
		ObjectMeta: metav1.ObjectMeta{Name: nodeClaim.Spec.NodeClassRef.Name},
	}), nil
}
