package node

import (
	"context"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	corev1 "k8s.io/api/core/v1"
	kubeclient "k8s.io/client-go/kubernetes"
	corev1lister "k8s.io/client-go/listers/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

var requiredLabels = map[string]string{
	"node-role.kubernetes.io/worker": "",
	"node-role.kubernetes.io/master": "",
}

const masterTaint = "node-role.kubernetes.io/master"

type NodeReconciler struct {
	Lister     corev1lister.NodeLister
	KubeClient kubeclient.Interface
	Log        logr.Logger
}

func (a *NodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := a.Log.WithValues("node", req.NamespacedName.String())
	logger.Info("Start reconcile")
	node, err := a.Lister.Get(req.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	if hasRequiredLabels(node) && !hasMasterTaint(node) {
		return ctrl.Result{}, nil
	}

	for k := range requiredLabels {
		if _, hasLabel := node.Labels[k]; hasLabel {
			continue
		}
		if node.Labels == nil {
			node.Labels = map[string]string{}
		}
		node.Labels[k] = requiredLabels[k]
	}

	removeMasterTaint(node)

	logger.Info("Updating node")
	_, err = a.KubeClient.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		a.Log.Error(err, "failed to update node")
	}
	return ctrl.Result{}, err
}

func hasRequiredLabels(node *corev1.Node) bool {
	for k := range requiredLabels {
		if _, hasLabel := node.Labels[k]; !hasLabel {
			return false
		}
	}
	return true
}

func hasMasterTaint(node *corev1.Node) bool {
	for _, taint := range node.Spec.Taints {
		if taint.Key == masterTaint {
			return true
		}
	}
	return false
}

func removeMasterTaint(node *corev1.Node) {
	taints := make([]corev1.Taint, 0, len(node.Spec.Taints))
	for _, taint := range node.Spec.Taints {
		if taint.Key == masterTaint {
			continue
		}
		taints = append(taints, taint)
	}
	node.Spec.Taints = taints
}
