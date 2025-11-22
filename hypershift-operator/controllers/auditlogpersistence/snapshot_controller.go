package auditlogpersistence

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	auditlogpersistencev1alpha1 "github.com/openshift/hypershift/api/auditlogpersistence/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
)

const (
	lastObservedRestartCountAnnotation = "hypershift.openshift.io/last-observed-restart-count"
	lastSnapshotTimeAnnotation         = "hypershift.openshift.io/last-snapshot-time"
	snapshotControllerName             = "audit-log-snapshot"
	// snapshotTimestampFormat is the format used for timestamps in snapshot names.
	// Uses Go's reference time format: 2006-01-02 15:04:05 becomes 20060102-150405
	// This format is filesystem-friendly and chronologically sortable.
	snapshotTimestampFormat = "20060102-150405"
	// Label keys for VolumeSnapshot resources
	auditLogsPVCLabelKey          = "hypershift.openshift.io/audit-logs-pvc"
	auditLogsPodLabelKey          = "hypershift.openshift.io/audit-logs-pod"
	controlPlaneNamespaceLabelKey = "hypershift.openshift.io/hosted-control-plane-namespace"
)

type SnapshotReconciler struct {
	client client.Client
	log    logr.Logger
}

// SetupSnapshotController sets up the snapshot controller that watches Pods and creates
// VolumeSnapshots for kube-apiserver pods when they crash (restart count increases).
func SetupSnapshotController(mgr ctrl.Manager) error {
	reconciler := &SnapshotReconciler{
		client: mgr.GetClient(),
		log:    mgr.GetLogger().WithName(snapshotControllerName),
	}

	err := ctrl.NewControllerManagedBy(mgr).
		Named(snapshotControllerName).
		For(&corev1.Pod{}).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 10*time.Second),
		}).
		WithEventFilter(predicateForKubeAPIServerPods()).
		Complete(reconciler)
	if err != nil {
		return fmt.Errorf("failed to set up snapshot controller: %w", err)
	}

	return nil
}

func (r *SnapshotReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.log.WithValues("pod", req.NamespacedName)

	// Get the pod
	pod := &corev1.Pod{}
	if err := r.client.Get(ctx, req.NamespacedName, pod); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get pod: %w", err)
	}

	// Check if this is a kube-apiserver pod
	if !isKubeAPIServerPod(pod) {
		return ctrl.Result{}, nil
	}

	// Check if namespace is a control plane namespace
	ns := &corev1.Namespace{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: pod.Namespace}, ns); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get namespace: %w", err)
	}

	if ns.Labels == nil || ns.Labels[controlPlaneNamespaceLabel] != "true" {
		return ctrl.Result{}, nil
	}

	// Get the AuditLogPersistenceConfig
	config := &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "cluster"}, config); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get AuditLogPersistenceConfig: %w", err)
	}

	// Apply defaults to a copy of the spec to avoid modifying the original
	spec := config.Spec.DeepCopy()
	ApplyDefaults(spec)

	// Check if feature is enabled
	if !IsEnabled(spec) {
		return ctrl.Result{}, nil
	}

	// Check if snapshots are enabled
	if !IsSnapshotsEnabled(spec) {
		return ctrl.Result{}, nil
	}

	// Get the kube-apiserver container restart count
	var restartCount int32
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if containerStatus.Name == "kube-apiserver" {
			restartCount = containerStatus.RestartCount
			break
		}
	}

	// Get the last observed restart count from annotation
	lastObservedRestartCount := int32(0)
	if val, ok := pod.Annotations[lastObservedRestartCountAnnotation]; ok {
		var err error
		lastObservedRestartCount, err = parseInt32(val)
		if err != nil {
			log.V(1).Info("Failed to parse last observed restart count annotation, resetting to 0", "annotationValue", val, "error", err)
			// Reset corrupted annotation to 0
			podCopy := pod.DeepCopy()
			if podCopy.Annotations == nil {
				podCopy.Annotations = make(map[string]string)
			}
			podCopy.Annotations[lastObservedRestartCountAnnotation] = "0"
			if patchErr := r.client.Patch(ctx, podCopy, client.MergeFrom(pod)); patchErr != nil {
				log.Error(patchErr, "Failed to reset corrupted annotation")
				// Continue anyway - the annotation will be fixed on next reconciliation
			}
			lastObservedRestartCount = 0
		}
	}

	// Check if restart count increased (indicating a crash)
	if restartCount <= lastObservedRestartCount {
		return ctrl.Result{}, nil
	}

	// Always update the last observed restart count when we see a new restart
	podCopy := pod.DeepCopy()
	if podCopy.Annotations == nil {
		podCopy.Annotations = make(map[string]string)
	}
	podCopy.Annotations[lastObservedRestartCountAnnotation] = fmt.Sprintf("%d", restartCount)
	if patchErr := r.client.Patch(ctx, podCopy, client.MergeFrom(pod)); patchErr != nil {
		log.Error(patchErr, "Failed to update last observed restart count annotation")
		// Continue anyway - we'll try again on next reconciliation
	}

	// Check if we should create a snapshot based on minimum interval
	shouldSnapshot := false
	if lastSnapshotTimeStr, ok := pod.Annotations[lastSnapshotTimeAnnotation]; ok {
		lastSnapshotTime, err := time.Parse(time.RFC3339, lastSnapshotTimeStr)
		if err != nil {
			log.V(1).Info("Failed to parse last snapshot time annotation, will create snapshot", "annotationValue", lastSnapshotTimeStr, "error", err)
			// Remove corrupted annotation - it will be set correctly after snapshot creation
			podCopy := pod.DeepCopy()
			if podCopy.Annotations == nil {
				podCopy.Annotations = make(map[string]string)
			}
			delete(podCopy.Annotations, lastSnapshotTimeAnnotation)
			if patchErr := r.client.Patch(ctx, podCopy, client.MergeFrom(pod)); patchErr != nil {
				log.Error(patchErr, "Failed to remove corrupted last snapshot time annotation")
				// Continue anyway - the annotation will be fixed on next reconciliation
			}
			shouldSnapshot = true
		} else {
			minInterval, err := time.ParseDuration(spec.Snapshots.MinInterval)
			if err != nil {
				log.Error(err, "Failed to parse minimum interval from config, will create snapshot", "minInterval", spec.Snapshots.MinInterval)
				shouldSnapshot = true
			} else {
				if time.Since(lastSnapshotTime) >= minInterval {
					shouldSnapshot = true
				} else {
					log.V(1).Info("Skipping snapshot due to minimum interval", "timeSinceLastSnapshot", time.Since(lastSnapshotTime), "minInterval", minInterval, "restartCount", restartCount)
					return ctrl.Result{}, nil
				}
			}
		}
	} else {
		// No previous snapshot, create one
		shouldSnapshot = true
	}

	// If we shouldn't snapshot, return early (we've already updated lastObservedRestartCount)
	if !shouldSnapshot {
		return ctrl.Result{}, nil
	}

	// Find the PVC for this pod
	pvcName := pvcNamePrefix + pod.Name
	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: pod.Namespace}, pvc); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("PVC not found for pod, skipping snapshot", "pvc", pvcName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get PVC: %w", err)
	}

	// Create snapshot
	if err := r.createSnapshot(ctx, pod, pvc, spec); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create snapshot: %w", err)
	}

	// Update pod annotation with snapshot time (lastObservedRestartCount was already updated above)
	podCopy = pod.DeepCopy()
	if podCopy.Annotations == nil {
		podCopy.Annotations = make(map[string]string)
	}
	podCopy.Annotations[lastSnapshotTimeAnnotation] = time.Now().Format(time.RFC3339)
	if err := r.client.Patch(ctx, podCopy, client.MergeFrom(pod)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update pod annotation: %w", err)
	}

	// Manage retention
	if err := r.manageRetention(ctx, pod, pvc, spec); err != nil {
		log.Error(err, "Failed to manage snapshot retention")
		// Don't fail reconciliation on retention errors
	}

	log.Info("Successfully created snapshot for pod crash", "restartCount", restartCount, "previousObservedRestartCount", lastObservedRestartCount)
	return ctrl.Result{}, nil
}

func (r *SnapshotReconciler) createSnapshot(ctx context.Context, pod *corev1.Pod, pvc *corev1.PersistentVolumeClaim, spec *auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec) error {
	timestamp := time.Now().Format(snapshotTimestampFormat)
	snapshotName := fmt.Sprintf("%s-snapshot-%s", pvc.Name, timestamp)

	snapshot := &snapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      snapshotName,
			Namespace: pod.Namespace,
			Labels: map[string]string{
				auditLogsPVCLabelKey:          pvc.Name,
				auditLogsPodLabelKey:          pod.Name,
				controlPlaneNamespaceLabelKey: pod.Namespace,
			},
		},
		Spec: snapshotv1.VolumeSnapshotSpec{
			Source: snapshotv1.VolumeSnapshotSource{
				PersistentVolumeClaimName: &pvc.Name,
			},
		},
	}

	if spec.Snapshots.VolumeSnapshotClassName != "" {
		snapshot.Spec.VolumeSnapshotClassName = &spec.Snapshots.VolumeSnapshotClassName
	}

	return r.client.Create(ctx, snapshot)
}

func (r *SnapshotReconciler) manageRetention(ctx context.Context, pod *corev1.Pod, pvc *corev1.PersistentVolumeClaim, spec *auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec) error {
	// ApplyDefaults should have been called before this function, so these should never be nil
	// But be defensive in case they are
	if spec.Snapshots.PerPodRetentionCount == nil {
		return fmt.Errorf("PerPodRetentionCount is nil, ApplyDefaults should have been called")
	}
	if spec.Snapshots.NamespaceRetentionCount == nil {
		return fmt.Errorf("NamespaceRetentionCount is nil, ApplyDefaults should have been called")
	}

	perPodRetention := int(*spec.Snapshots.PerPodRetentionCount)
	namespaceRetention := int(*spec.Snapshots.NamespaceRetentionCount)

	// List all snapshots for this PVC
	snapshotList := &snapshotv1.VolumeSnapshotList{}
	if err := r.client.List(ctx, snapshotList, client.InNamespace(pod.Namespace), client.MatchingLabels{
		auditLogsPVCLabelKey: pvc.Name,
	}); err != nil {
		return fmt.Errorf("failed to list snapshots: %w", err)
	}

	// Sort snapshots by creation time (oldest first)
	snapshots := snapshotList.Items
	sortSnapshotsByCreationTime(snapshots)

	// Per-pod retention: delete oldest snapshots if over limit
	if len(snapshots) > perPodRetention {
		toDelete := len(snapshots) - perPodRetention
		for i := 0; i < toDelete; i++ {
			if err := r.client.Delete(ctx, &snapshots[i]); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete snapshot %s: %w", snapshots[i].Name, err)
			}
		}
	}

	// Namespace retention: list all snapshots in namespace and delete oldest if over limit
	allSnapshots := &snapshotv1.VolumeSnapshotList{}
	if err := r.client.List(ctx, allSnapshots, client.InNamespace(pod.Namespace), client.MatchingLabels{
		controlPlaneNamespaceLabelKey: pod.Namespace,
	}); err != nil {
		return fmt.Errorf("failed to list all snapshots in namespace: %w", err)
	}

	if len(allSnapshots.Items) > namespaceRetention {
		sortSnapshotsByCreationTime(allSnapshots.Items)
		toDelete := len(allSnapshots.Items) - namespaceRetention
		for i := 0; i < toDelete; i++ {
			if err := r.client.Delete(ctx, &allSnapshots.Items[i]); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete snapshot %s: %w", allSnapshots.Items[i].Name, err)
			}
		}
	}

	return nil
}

func sortSnapshotsByCreationTime(snapshots []snapshotv1.VolumeSnapshot) {
	// Sort by creation timestamp (oldest first)
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].CreationTimestamp.Time.Before(snapshots[j].CreationTimestamp.Time)
	})
}

func parseInt32(s string) (int32, error) {
	result, err := strconv.ParseInt(s, 10, 32)
	return int32(result), err
}

// predicateForKubeAPIServerPods creates a predicate that filters pods to only kube-apiserver pods.
// Note: Control plane namespace check is done in Reconcile since we need client access.
func predicateForKubeAPIServerPods() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			return false
		}
		// Check if it's a kube-apiserver pod
		if !isKubeAPIServerPod(pod) {
			return false
		}
		// Note: Namespace label check is done in Reconcile since we need client access
		return true
	})
}

func isKubeAPIServerPod(pod *corev1.Pod) bool {
	if pod.Labels == nil {
		return false
	}
	return pod.Labels[kubeAPIServerLabel] == kubeAPIServerLabelValue
}
