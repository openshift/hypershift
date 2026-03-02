package spotremediation

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// nthTaintPrefix is the prefix used by aws-node-termination-handler for taints
	// applied to nodes that receive spot interruption or rebalance recommendation events.
	nthTaintPrefix = "aws-node-termination-handler/"

	// interruptibleInstanceLabel is the label applied to CAPI Machines backed by spot instances.
	interruptibleInstanceLabel = "hypershift.openshift.io/interruptible-instance"

	// spotInterruptionSignalAnnotation is applied to the Machine before deletion for auditability.
	spotInterruptionSignalAnnotation = "hypershift.openshift.io/spot-interruption-signal"
)

type reconciler struct {
	client             client.Client
	guestClusterClient client.Client
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("node", req.NamespacedName)

	node := &corev1.Node{}
	if err := r.guestClusterClient.Get(ctx, req.NamespacedName, node); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get Node: %w", err)
	}

	taintKey := nthTaintKey(node)
	if taintKey == "" {
		return ctrl.Result{}, nil
	}

	log.Info("Node has node-termination-handler taint, looking up Machine", "taintKey", taintKey)

	machine, err := getMachineForNode(ctx, r.client, node)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Machine not found for node, skipping")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if _, ok := machine.Labels[interruptibleInstanceLabel]; !ok {
		log.Info("Machine is not interruptible, skipping", "machine", machine.Name)
		return ctrl.Result{}, nil
	}

	if machine.DeletionTimestamp != nil {
		log.Info("Machine is already deleting, skipping", "machine", machine.Name)
		return ctrl.Result{}, nil
	}

	// Annotate the machine with the interruption signal for auditability.
	if machine.Annotations == nil {
		machine.Annotations = map[string]string{}
	}
	machine.Annotations[spotInterruptionSignalAnnotation] = taintKey
	if err := r.client.Update(ctx, machine); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to annotate Machine %s: %w", machine.Name, err)
	}

	log.Info("Deleting Machine for spot interruption remediation", "machine", machine.Name, "namespace", machine.Namespace, "taintKey", taintKey)
	if err := r.client.Delete(ctx, machine); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to delete Machine %s: %w", machine.Name, err)
	}

	log.Info("Machine deleted for spot interruption remediation", "machine", machine.Name, "namespace", machine.Namespace)
	return ctrl.Result{}, nil
}

// nthTaintKey returns the first taint key with the node-termination-handler prefix, or empty string if none found.
func nthTaintKey(node *corev1.Node) string {
	for _, taint := range node.Spec.Taints {
		if strings.HasPrefix(taint.Key, nthTaintPrefix) {
			return taint.Key
		}
	}
	return ""
}

// getMachineForNode looks up the CAPI Machine for a given Node using standard CAPI annotations.
func getMachineForNode(ctx context.Context, c client.Client, node *corev1.Node) (*capiv1.Machine, error) {
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
	if err := c.Get(ctx, client.ObjectKeyFromObject(machine), machine); err != nil {
		return nil, fmt.Errorf("failed to get Machine: %w", err)
	}

	return machine, nil
}
