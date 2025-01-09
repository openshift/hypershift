package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"
	supportutil "github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	nodePoolAnnotation       = "hypershift.openshift.io/nodePool"
	labelsSyncedAnnotation   = "hypershift.openshift.io/labelsSynced"
	nodePoolAnnotationTaints = "hypershift.openshift.io/nodePoolTaints"
	labelManagedPrefix       = "managed.hypershift.openshift.io"
)

type reconciler struct {
	client             client.Client
	guestClusterClient client.Client
	upsert.CreateOrUpdateProvider
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling")

	node := &corev1.Node{}
	if err := r.guestClusterClient.Get(ctx, req.NamespacedName, node); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("not found", "request", req.String())
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get Node: %w", err)
	}

	var apiErr *apierrors.StatusError
	nodePoolName, err := r.nodeToNodePoolName(ctx, node)
	if err != nil {
		if errors.As(err, &apiErr) && !apierrors.IsNotFound(err) {
			// Return error and retry only if the API interaction failed. Other errors are because the nodeToNodePoolName expected
			// annotations are not in place yet, so we'll reconcile triggered by the event which sets them in the Node.
			return ctrl.Result{}, err
		} else {
			log.Error(err, "failed to get nodePool name from Node")
			return ctrl.Result{}, nil
		}
	}

	if labelsHaveSynced(node) {
		return reconcile.Result{}, nil
	}

	machine, err := r.getMachineForNode(ctx, node)
	if err != nil {
		return reconcile.Result{}, err
	}
	labelsToSync := getManagedLabels(machine.Labels)
	labelsToSync[hyperv1.NodePoolLabel] = nodePoolName

	var taints []corev1.Taint
	taintsInJSON := machine.Annotations[nodePoolAnnotationTaints]
	err = json.Unmarshal([]byte(taintsInJSON), &taints)
	if err != nil {
		return reconcile.Result{}, err
	}

	result, err := r.CreateOrUpdate(ctx, r.guestClusterClient, node, func() error {
		node.Annotations[labelsSyncedAnnotation] = "true"

		// Sync labels.
		for k, v := range labelsToSync {
			node.Labels[k] = v
		}

		// Sync taints.
		node.Spec.Taints = append(node.Spec.Taints, taints...)

		return nil
	})
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to reconcile Node: %w", err)
	}

	log.Info("Reconciled Node", "result", result)
	return reconcile.Result{}, nil
}

func getManagedLabels(labels map[string]string) map[string]string {
	managedLabels := make(map[string]string)
	for k, v := range labels {
		if !strings.HasPrefix(k, labelManagedPrefix) {
			continue
		}
		managedLabels[strings.TrimPrefix(k, labelManagedPrefix+".")] = v
	}

	return managedLabels
}

func (r *reconciler) getMachineForNode(ctx context.Context, node *corev1.Node) (*capiv1.Machine, error) {
	machineName, ok := node.GetAnnotations()[capiv1.MachineAnnotation]
	if !ok || machineName == "" {
		return nil, fmt.Errorf("failed to find MachineAnnotation on Node %q", node.Name)
	}

	machineNamespace, ok := node.GetAnnotations()[capiv1.ClusterNamespaceAnnotation]
	if !ok || machineNamespace == "" {
		return nil, fmt.Errorf("failed to find ClusterNamespaceAnnotation on Node %q", node.Name)
	}

	machine := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineNamespace,
			Name:      machineName,
		},
	}
	if err := r.client.Get(ctx, client.ObjectKeyFromObject(machine), machine); err != nil {
		return nil, fmt.Errorf("failed to get Machine: %w", err)
	}

	return machine, nil
}

func labelsHaveSynced(node *corev1.Node) bool {
	if _, ok := node.Annotations[labelsSyncedAnnotation]; ok {
		return true
	}

	return false
}
func (r *reconciler) nodeToNodePoolName(ctx context.Context, node *corev1.Node) (string, error) {
	machine, err := r.getMachineForNode(ctx, node)
	if err != nil {
		return "", err
	}

	nodePoolName, ok := machine.Annotations[nodePoolAnnotation]
	if !ok || nodePoolName == "" {
		return "", fmt.Errorf("failed to find nodePoolAnnotation on Machine %q", machine.Name)
	}

	return supportutil.ParseNamespacedName(nodePoolName).Name, nil
}
