package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"
	supportutil "github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
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

	node := &corev1.Node{}
	if err := r.guestClusterClient.Get(ctx, req.NamespacedName, node); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("not found", "request", req.String())
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get Node: %w", err)
	}

	var apiErr *apierrors.StatusError
	machine, err := r.getMachineForNode(ctx, node)
	if err != nil {
		if errors.As(err, &apiErr) && !apierrors.IsNotFound(err) {
			// Return error and retry only if the API interaction failed.
			// Other errors mean CAPI annotations aren't set on the Node yet;
			// we'll reconcile when the event that sets them fires.
			return ctrl.Result{}, err
		} else {
			log.Error(err, "failed to get Machine for Node")
			return ctrl.Result{}, nil
		}
	}

	nodePoolName, err := nodePoolNameFromMachine(machine)
	if err != nil {
		log.Error(err, "failed to get nodePool name from Machine")
		return ctrl.Result{}, nil
	}

	labelsToSync := getManagedLabels(machine.Labels)
	labelsToSync[hyperv1.NodePoolLabel] = nodePoolName

	var taints []corev1.Taint
	if taintsInJSON := machine.Annotations[nodePoolAnnotationTaints]; taintsInJSON != "" {
		if err = json.Unmarshal([]byte(taintsInJSON), &taints); err != nil {
			return reconcile.Result{}, err
		}
	}

	expectedHash, err := computeSyncHash(labelsToSync, taints)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to compute sync hash: %w", err)
	}
	if labelsHaveSynced(node, expectedHash) {
		return reconcile.Result{}, nil
	}

	result, err := r.CreateOrUpdate(ctx, r.guestClusterClient, node, func() error {
		if node.Annotations == nil {
			node.Annotations = make(map[string]string)
		}
		node.Annotations[labelsSyncedAnnotation] = expectedHash

		if node.Labels == nil {
			node.Labels = make(map[string]string)
		}
		// Additive-only: ensures managed labels are present on the Node.
		// Does not remove labels absent from labelsToSync, since the Node
		// may have labels set by kubelet, autoscaler, or other controllers.
		maps.Copy(node.Labels, labelsToSync)

		node.Spec.Taints = mergeTaints(node.Spec.Taints, taints)

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

func labelsHaveSynced(node *corev1.Node, expectedHash string) bool {
	val, ok := node.Annotations[labelsSyncedAnnotation]
	if !ok {
		return false
	}
	return val == expectedHash
}

type syncState struct {
	Labels []labelEntry `json:"labels"`
	Taints []taintEntry `json:"taints"`
}

type labelEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type taintEntry struct {
	Key    string             `json:"key"`
	Value  string             `json:"value"`
	Effect corev1.TaintEffect `json:"effect"`
}

func computeSyncHash(labels map[string]string, taints []corev1.Taint) (string, error) {
	state := syncState{
		Labels: make([]labelEntry, 0, len(labels)),
		Taints: make([]taintEntry, 0, len(taints)),
	}

	for _, k := range slices.Sorted(maps.Keys(labels)) {
		state.Labels = append(state.Labels, labelEntry{Key: k, Value: labels[k]})
	}

	for _, t := range taints {
		state.Taints = append(state.Taints, taintEntry{Key: t.Key, Value: t.Value, Effect: t.Effect})
	}
	slices.SortFunc(state.Taints, func(a, b taintEntry) int {
		return strings.Compare(taintEntryKey(a), taintEntryKey(b))
	})

	return supportutil.HashStruct(state)
}

func taintEntryKey(t taintEntry) string {
	return fmt.Sprintf("%s=%s:%s", t.Key, t.Value, t.Effect)
}

func mergeTaints(existing, desired []corev1.Taint) []corev1.Taint {
	seen := make(map[string]struct{}, len(existing))
	for _, t := range existing {
		seen[taintKey(t)] = struct{}{}
	}
	merged := make([]corev1.Taint, len(existing), len(existing)+len(desired))
	copy(merged, existing)
	for _, t := range desired {
		if _, ok := seen[taintKey(t)]; !ok {
			merged = append(merged, t)
		}
	}
	return merged
}

func taintKey(t corev1.Taint) string {
	return fmt.Sprintf("%s=%s:%s", t.Key, t.Value, t.Effect)
}

func nodePoolNameFromMachine(machine *capiv1.Machine) (string, error) {
	nodePoolName, ok := machine.Annotations[nodePoolAnnotation]
	if !ok || nodePoolName == "" {
		return "", fmt.Errorf("failed to find nodePoolAnnotation on Machine %q", machine.Name)
	}

	return supportutil.ParseNamespacedName(nodePoolName).Name, nil
}
