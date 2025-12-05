package hostedcluster

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/support/upsert"
	hyperutil "github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// OADP audit paused annotations
	// These annotations are used to track the paused state of the cluster for OADP audit purposes.
	// In case of Velero pod got deleted or the backup get stuck, HO should look for these 2 annotations
	// + a HostedCluster paused, if that's the case, HO should unpause the cluster and remove the annotations.
	oadpAuditPausedAtAnnotation = "oadp.openshift.io/paused-at"
	oadpAuditPausedByAnnotation = "oadp.openshift.io/paused-by"
	oadpAuditPausedPluginAuthor = "hypershift-oadp-plugin"
	oadpBackupNamespace         = "openshift-adp"
	oadpCacheTTL                = 5 * time.Minute
)

// VeleroBackupCacheEntry represents a cache entry for Velero backups in a namespace
type VeleroBackupCacheEntry struct {
	Backups   []unstructured.Unstructured
	Timestamp time.Time
}

// VeleroBackupCache manages cached Velero backup data across namespaces
type VeleroBackupCache struct {
	mutex      sync.RWMutex
	cache      map[string]*VeleroBackupCacheEntry // namespace -> cache entry
	defaultTTL time.Duration
}

// Global cache instance (initialized thread-safely using sync.Once)
var veleroBackupCache *VeleroBackupCache
var veleroBackupCacheOnce sync.Once

// initVeleroBackupCache initializes the global Velero backup cache thread-safely
func initVeleroBackupCache() {
	veleroBackupCacheOnce.Do(func() {
		veleroBackupCache = &VeleroBackupCache{
			cache:      make(map[string]*VeleroBackupCacheEntry),
			defaultTTL: oadpCacheTTL,
		}
	})
}

// GetBackups returns cached backups for a namespace, refreshing if needed
func (c *VeleroBackupCache) GetBackups(ctx context.Context, k8sClient client.Client, namespace string, backupGVK schema.GroupVersionKind) ([]unstructured.Unstructured, error) {
	log := ctrl.LoggerFrom(ctx)

	// First, check cache with read lock
	c.mutex.RLock()
	if entry, exists := c.cache[namespace]; exists && time.Since(entry.Timestamp) < c.defaultTTL {
		log.V(4).Info("Using cached Velero backups", "namespace", namespace, "count", len(entry.Backups), "age", time.Since(entry.Timestamp))
		backups := entry.Backups
		c.mutex.RUnlock()
		return backups, nil
	}
	c.mutex.RUnlock()

	// Cache miss or expired - fetch fresh data outside of lock
	log.V(4).Info("Cache miss or expired, fetching fresh Velero backups", "namespace", namespace)

	backupList := &unstructured.UnstructuredList{}
	backupList.SetGroupVersionKind(backupGVK)

	err := k8sClient.List(ctx, backupList, client.InNamespace(namespace))
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.V(4).Info("No Velero backups found", "namespace", namespace)
			// Cache empty result with write lock
			c.mutex.Lock()
			c.cache[namespace] = &VeleroBackupCacheEntry{
				Backups:   []unstructured.Unstructured{},
				Timestamp: time.Now(),
			}
			c.mutex.Unlock()
			return []unstructured.Unstructured{}, nil
		}
		return nil, fmt.Errorf("error listing Velero backups in namespace %s: %w", namespace, err)
	}

	// Cache the results with write lock
	c.mutex.Lock()
	c.cache[namespace] = &VeleroBackupCacheEntry{
		Backups:   backupList.Items,
		Timestamp: time.Now(),
	}
	backups := backupList.Items
	c.mutex.Unlock()

	log.V(4).Info("Cached fresh Velero backups", "namespace", namespace, "count", len(backups))
	return backups, nil
}

// ClearNamespace removes cached data for a specific namespace
func (c *VeleroBackupCache) ClearNamespace(namespace string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.cache, namespace)
}

// ClearAll removes all cached data
func (c *VeleroBackupCache) ClearAll() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.cache = make(map[string]*VeleroBackupCacheEntry)
}

// SetTTL updates the cache TTL
func (c *VeleroBackupCache) SetTTL(ttl time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.defaultTTL = ttl
}

// GetVeleroBackupCache returns the global cache instance for testing/external access
func GetVeleroBackupCache() *VeleroBackupCache {
	initVeleroBackupCache()
	return veleroBackupCache
}

// hasOADPPauseAnnotations checks if the HostedCluster has the specific OADP pause annotations
func hasOADPPauseAnnotations(hc *hyperv1.HostedCluster) bool {
	if hc == nil {
		return false
	}

	annotations := hc.GetAnnotations()
	if annotations == nil {
		return false
	}

	pausedBy := annotations[oadpAuditPausedByAnnotation]
	pausedAt := annotations[oadpAuditPausedAtAnnotation]

	return pausedBy == oadpAuditPausedPluginAuthor && pausedAt != ""
}

// isBackupInTerminalState checks if a Velero backup is in a terminal state
func isBackupInTerminalState(backup unstructured.Unstructured) (bool, string) {
	// Extract status.phase from the backup using unstructured access
	phase, found, err := unstructured.NestedString(backup.Object, "status", "phase")
	if err != nil || !found {
		return false, ""
	}

	// Check if the phase is one of the terminal states
	terminalStates := []string{
		"Completed",        // Backup successful
		"Failed",           // Backup failed completely
		"PartiallyFailed",  // Backup completed with errors
		"FailedValidation", // Backup failed validation
	}

	for _, terminalState := range terminalStates {
		if phase == terminalState {
			return true, phase
		}
	}

	return false, phase
}

// findLastRelatedBackup searches for the most recent Velero backup related to the given HostedCluster
func (r *HostedClusterReconciler) findLastRelatedBackup(ctx context.Context, hc *hyperv1.HostedCluster) (*unstructured.Unstructured, error) {
	log := ctrl.LoggerFrom(ctx)

	// Define the GVK for Velero Backup resources
	backupGVK := schema.GroupVersionKind{
		Group:   "velero.io",
		Version: "v1",
		Kind:    "BackupList",
	}

	// Search only in the OADP namespace
	log.V(4).Info("Searching for Velero backups", "namespace", oadpBackupNamespace, "cluster", hc.Name)

	relatedBackups, err := r.findBackupsInNamespace(ctx, oadpBackupNamespace, hc, backupGVK)
	if err != nil {
		return nil, fmt.Errorf("error searching backups in namespace %s: %w", oadpBackupNamespace, err)
	}

	if len(relatedBackups) == 0 {
		log.V(4).Info("No related backups found", "cluster", hc.Name)
		return nil, nil
	}

	// Sort backups by creation time, newest first
	sort.Slice(relatedBackups, func(i, j int) bool {
		timeI := relatedBackups[i].GetCreationTimestamp()
		timeJ := relatedBackups[j].GetCreationTimestamp()
		return timeI.Time.After(timeJ.Time)
	})

	// Return only the most recent backup
	lastBackup := &relatedBackups[0]
	log.V(4).Info("Backup search completed", "cluster", hc.Name, "foundBackups", len(relatedBackups), "lastBackup", lastBackup.GetName())
	return lastBackup, nil
}

// findBackupsInNamespace searches for backups related to the cluster in a specific namespace with cached data
func (r *HostedClusterReconciler) findBackupsInNamespace(ctx context.Context, namespace string, hc *hyperv1.HostedCluster, backupGVK schema.GroupVersionKind) ([]unstructured.Unstructured, error) {
	log := ctrl.LoggerFrom(ctx)

	// Ensure cache is initialized
	initVeleroBackupCache()

	// Get all backups from cache (cache handles API calls and TTL internally)
	allBackups, err := veleroBackupCache.GetBackups(ctx, r.Client, namespace, backupGVK)
	if err != nil {
		return nil, err
	}

	// Filter backups that might be related to this HostedCluster using name patterns and other strategies
	var foundBackups []unstructured.Unstructured
	for _, backup := range allBackups {
		if r.isBackupRelatedToCluster(backup, hc) {
			foundBackups = append(foundBackups, backup)
			log.V(4).Info("Found related backup", "backup", backup.GetName(), "namespace", backup.GetNamespace())
		}
	}

	log.V(4).Info("Filtered backups for cluster", "namespace", namespace, "cluster", hc.Name, "totalBackups", len(allBackups), "relatedBackups", len(foundBackups))
	return foundBackups, nil
}

// isBackupRelatedToCluster determines if a backup is related to the given HostedCluster
func (r *HostedClusterReconciler) isBackupRelatedToCluster(backup unstructured.Unstructured, hc *hyperv1.HostedCluster) bool {
	// Strategy 1: Check backup name for cluster name patterns
	backupName := backup.GetName()
	// Check if backup name contains the cluster name
	if strings.Contains(backupName, hc.Name) {
		return true
	}
	// Check if backup name contains cluster namespace and name pattern
	if strings.Contains(backupName, hc.Namespace+"-"+hc.Name) {
		return true
	}

	// Strategy 2: Check IncludedNamespaces for our cluster's namespace
	includedNamespaces, found, err := unstructured.NestedStringSlice(backup.Object, "spec", "includedNamespaces")
	if err == nil && found {
		for _, ns := range includedNamespaces {
			if ns == hc.Namespace || ns == hc.Namespace+"-"+hc.Name {
				return true
			}
		}
	}

	return false
}

// checkOADPRecovery checks if a HostedCluster paused by OADP should be unpaused
func (r *HostedClusterReconciler) checkOADPRecovery(ctx context.Context, hc *hyperv1.HostedCluster) (bool, error) {
	log := ctrl.LoggerFrom(ctx)

	// First, verify this cluster was paused by the OADP plugin
	if !hasOADPPauseAnnotations(hc) {
		log.V(4).Info("hostedcluster not paused by OADP plugin", "cluster", hc.Name)
		return false, nil
	}

	log.V(4).Info("hostedcluster paused by OADP plugin, checking backup status", "HostedCluster", hc.Name, "Namespace", hc.Namespace, "PausedAt", hc.Annotations[oadpAuditPausedAtAnnotation])

	// Find backups related to this cluster
	lastRelatedBackup, err := r.findLastRelatedBackup(ctx, hc)
	if err != nil {
		return false, fmt.Errorf("error searching for related backups: %w", err)
	}

	// If no backups are found, we should remove the pause annotations and unpause the cluster
	if lastRelatedBackup == nil {
		log.V(4).Info("no related backups found for OADP-paused cluster, should unpause the cluster", "cluster", hc.Name)
		return true, nil
	}

	// Check if the last backup is in terminal state
	isTerminal, phase := isBackupInTerminalState(*lastRelatedBackup)
	if isTerminal {
		log.Info("last backup is in terminal state - should unpause the cluster",
			"cluster", hc.Name,
			"backup", lastRelatedBackup.GetName(),
			"namespace", lastRelatedBackup.GetNamespace(),
			"phase", phase)
		return true, nil
	}

	// If the last backup is still in progress, keep the cluster paused
	log.V(4).Info("last backup still in progress, should keep the cluster paused",
		"cluster", hc.Name,
		"backup", lastRelatedBackup.GetName(),
		"namespace", lastRelatedBackup.GetNamespace(),
		"phase", phase)
	return false, nil
}

// resumeClusterFromHangedOADPBackup removes OADP pause annotations and resumes the HostedCluster and its NodePools
func (r *HostedClusterReconciler) resumeClusterFromHangedOADPBackup(ctx context.Context, hc *hyperv1.HostedCluster, createOrUpdate upsert.CreateOrUpdateFN) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	log.Info("resuming cluster from hanged OADP backup", "cluster", hc.Name, "namespace", hc.Namespace, "PausedAt", hc.Annotations[oadpAuditPausedAtAnnotation])

	// Update the HostedCluster to remove OADP annotations and unpause
	updatedHC := hc.DeepCopy()
	if _, err := createOrUpdate(ctx, r.Client, updatedHC, func() error {
		// Remove OADP pause annotations
		annotations := updatedHC.GetAnnotations()
		if annotations != nil {
			delete(annotations, "oadp.openshift.io/paused-by")
			delete(annotations, "oadp.openshift.io/paused-at")
			updatedHC.SetAnnotations(annotations)
		}

		// Clear the pausedUntil field to unpause the cluster
		updatedHC.Spec.PausedUntil = nil
		return nil
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update HostedCluster to remove OADP pause: %w", err)
	}

	log.Info("successfully resumed HostedCluster from OADP",
		"cluster", hc.Name,
		"namespace", hc.Namespace)

	// Get all NodePools associated with this HostedCluster
	nodePools, err := listNodePools(ctx, r.Client, hc.Namespace, hc.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list NodePools for cluster %s: %w", hc.Name, err)
	}

	// Resume all NodePools associated with this cluster
	for i := range nodePools {
		nodePool := &nodePools[i]
		if _, err := createOrUpdate(ctx, r.Client, nodePool, func() error {
			// Remove OADP pause annotations from NodePool
			annotations := nodePool.GetAnnotations()
			if annotations != nil {
				delete(annotations, "oadp.openshift.io/paused-by")
				delete(annotations, "oadp.openshift.io/paused-at")
				nodePool.SetAnnotations(annotations)
			}

			// Clear the pausedUntil field to unpause the NodePool
			nodePool.Spec.PausedUntil = nil
			return nil
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update NodePool %s to remove OADP pause: %w", nodePool.Name, err)
		}

		log.Info("successfully resumed NodePool from hanged OADP backup",
			"cluster", hc.Name,
			"nodePool", nodePool.Name,
			"namespace", nodePool.Namespace)
	}

	log.Info("successfully resumed cluster and all associated NodePools from hanged OADP backup",
		"cluster", hc.Name,
		"namespace", hc.Namespace,
		"nodePoolsResumed", len(nodePools))

	// Return without requeue since the cluster and nodepools are now unpaused
	// The next reconciliation will proceed normally
	return ctrl.Result{}, nil
}

// reconcileAdditionalTrustBundle reconciles the HostedControlPlane AdditionalTrustBundle ConfigMap by resolving
// the source reference from the HostedCluster and syncing the CM in the control plane namespace.
func (r *HostedClusterReconciler) reconcileAdditionalTrustBundle(ctx context.Context, hcluster *hyperv1.HostedCluster, createOrUpdate upsert.CreateOrUpdateFN, controlPlaneNamespace string) error {
	dest := controlplaneoperator.UserCABundle(controlPlaneNamespace)
	if hcluster.Spec.AdditionalTrustBundle == nil {
		// If the HostedCluster has no additional trust bundle, delete the destination ConfigMap if it exists
		if _, err := hyperutil.DeleteIfNeeded(ctx, r.Client, dest); err != nil {
			return fmt.Errorf("failed to delete unused additionalTrustBundle: %w", err)
		}
		return nil
	}

	var src corev1.ConfigMap
	err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.Namespace, Name: hcluster.Spec.AdditionalTrustBundle.Name}, &src)
	if err != nil {
		return fmt.Errorf("failed to get hostedcluster AdditionalTrustBundle ConfigMap %s: %w", hcluster.Spec.AdditionalTrustBundle.Name, err)
	}
	if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcluster.Name, &src); err != nil {
		return fmt.Errorf("failed to set referenced resource annotation: %w", err)
	}
	_, err = createOrUpdate(ctx, r.Client, dest, func() error {
		srcData, srcHasData := src.Data["ca-bundle.crt"]
		if !srcHasData {
			return fmt.Errorf("hostedcluster AdditionalTrustBundle configmap %q must have a ca-bundle.crt key", src.Name)
		}
		if dest.Data == nil {
			dest.Data = map[string]string{}
		}
		dest.Data["ca-bundle.crt"] = srcData
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane AdditionalTrustBundle configmap: %w", err)
	}

	return nil
}
