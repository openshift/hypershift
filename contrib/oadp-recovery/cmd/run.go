package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"

	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/contrib/oadp-recovery/internal/oadp"
	"github.com/openshift/hypershift/contrib/oadp-recovery/internal/scheme"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// OADPRecoveryRunner manages OADP recovery operations
type OADPRecoveryRunner struct {
	Client        client.Client
	OADPNamespace string
	DryRun        bool
	Logger        logr.Logger
}

func newRunCmd() *cobra.Command {
	var (
		oadpNamespace string
		dryRun        bool
		logDev        bool
		logLevel      int
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the OADP recovery check",
		Long: `Performs a single OADP recovery check across all HyperShift clusters.
This command identifies clusters paused by OADP and resumes them when their
associated Velero backups reach terminal states (Completed, Failed, PartiallyFailed, or Deleted).`,
		RunE: func(c *cobra.Command, args []string) error {
			return runOADPRecovery(c.Context(), oadpNamespace, dryRun, logDev, logLevel)
		},
	}

	cmd.Flags().StringVar(&oadpNamespace, "oadp-namespace", "openshift-adp", "Namespace where OADP/Velero backups are stored")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Enable dry-run mode (no actual changes made)")
	cmd.Flags().BoolVar(&logDev, "log-dev", false, "Enable development logging (human-friendly)")
	cmd.Flags().IntVar(&logLevel, "log-level", 0, "Log verbosity level (0=info only, 1=verbose, 2=debug)")

	// Support environment variable overrides
	if envNamespace := os.Getenv("OADP_NAMESPACE"); envNamespace != "" {
		oadpNamespace = envNamespace
	}
	if envDryRun := os.Getenv("DRY_RUN"); envDryRun != "" {
		if parsed, err := strconv.ParseBool(envDryRun); err == nil {
			dryRun = parsed
		}
	}
	if envLogLevel := os.Getenv("LOG_LEVEL"); envLogLevel != "" {
		switch envLogLevel {
		case "debug":
			logLevel = 2
		case "verbose":
			logLevel = 1
		case "info":
			logLevel = 0
		}
	}

	return cmd
}

func runOADPRecovery(ctx context.Context, oadpNamespace string, dryRun, logDev bool, logLevel int) error {
	// Setup logging
	logger := zap.New(zap.UseDevMode(logDev), zap.Level(zapcore.Level(-1*logLevel)))
	ctrl.SetLogger(logger)
	runLogger := logger.WithName("run")

	runLogger.Info("Starting OADP recovery check",
		"oadpNamespace", oadpNamespace,
		"dryRun", dryRun)

	// Setup Kubernetes client
	k8sConfig := ctrl.GetConfigOrDie()
	k8sClient, err := client.New(k8sConfig, client.Options{Scheme: scheme.New()})
	if err != nil {
		return fmt.Errorf("failed to create kubernetes Client: %w", err)
	}

	// Create and run the OADP recovery runner
	runner := &OADPRecoveryRunner{
		Client:        k8sClient,
		OADPNamespace: oadpNamespace,
		DryRun:        dryRun,
		Logger:        runLogger,
	}

	if err := runner.Run(ctx); err != nil {
		return fmt.Errorf("OADP recovery run failed: %w", err)
	}

	runLogger.Info("OADP recovery run completed successfully")
	return nil
}

// Run executes the OADP recovery logic for all hosted clusters
func (r *OADPRecoveryRunner) Run(ctx context.Context) error {
	logger := r.Logger.WithName("recovery")

	// List all hosted clusters
	var hostedClusters hyperv1.HostedClusterList
	if err := r.Client.List(ctx, &hostedClusters); err != nil {
		return fmt.Errorf("failed to list hosted clusters: %w", err)
	}

	logger.Info("Found hosted clusters", "count", len(hostedClusters.Items))

	// Process each hosted cluster
	processedCount := 0
	recoveredCount := 0
	errorCount := 0
	var recoveredClusterNames []string
	var recoveredNodePoolNames []string

	for i := range hostedClusters.Items {
		hc := &hostedClusters.Items[i]
		clusterLogger := logger.WithValues("cluster", hc.Name, "namespace", hc.Namespace)

		clusterLogger.V(1).Info("Processing hosted cluster", "cluster", hc.Name, "namespace", hc.Namespace)

		recovered, nodePoolNames, err := r.processHostedCluster(ctx, hc, clusterLogger)
		if err != nil {
			clusterLogger.Error(err, "failed to process hosted cluster")
			errorCount++
		} else {
			processedCount++
			if recovered {
				recoveredCount++
				recoveredClusterNames = append(recoveredClusterNames, hc.Name)
				recoveredNodePoolNames = append(recoveredNodePoolNames, nodePoolNames...)
			}
		}
	}

	logger.Info("OADP recovery completed",
		"totalClusters", len(hostedClusters.Items),
		"processedSuccessfully", processedCount,
		"clustersRecovered", recoveredCount,
		"clusterNames", recoveredClusterNames,
		"nodePoolsRecovered", len(recoveredNodePoolNames),
		"nodePoolNames", recoveredNodePoolNames,
		"errors", errorCount)

	return nil
}

// processHostedCluster processes a single hosted cluster for OADP recovery
// Returns (wasRecovered, nodePoolNames, error)
func (r *OADPRecoveryRunner) processHostedCluster(ctx context.Context, hc *hyperv1.HostedCluster, logger logr.Logger) (bool, []string, error) {
	// Check if this cluster needs OADP recovery
	shouldRecover, err := r.CheckOADPRecovery(ctx, hc, logger)
	if err != nil {
		return false, nil, fmt.Errorf("error checking OADP recovery: %w", err)
	}

	if !shouldRecover {
		logger.V(4).Info("cluster does not need OADP recovery")
		return false, nil, nil
	}

	logger.Info("Cluster needs to be unpaused", "cluster", hc.Name)

	if r.DryRun {
		logger.Info("DRY RUN: would recover cluster from OADP backup issue")
		// In dry run, we still want to count it as recovered and get NodePool names
		nodePools, err := r.listNodePools(ctx, hc.Namespace, hc.Name)
		if err != nil {
			return false, nil, fmt.Errorf("failed to list NodePools for cluster %s: %w", hc.Name, err)
		}
		var nodePoolNames []string
		for i := range nodePools.Items {
			nodePoolNames = append(nodePoolNames, nodePools.Items[i].Name)
		}
		return true, nodePoolNames, nil
	}

	// Perform the actual recovery
	nodePoolNames, err := r.resumeClusterFromHangedOADPBackup(ctx, hc, logger)
	if err != nil {
		return false, nil, fmt.Errorf("failed to resume cluster from hanged OADP backup: %w", err)
	}

	logger.Info("successfully recovered cluster from OADP backup issue")
	return true, nodePoolNames, nil
}


// findLastRelatedBackup searches for the most recent Velero backup related to the given HostedCluster
// This version eliminates the cache and queries directly
func (r *OADPRecoveryRunner) findLastRelatedBackup(ctx context.Context, hc *hyperv1.HostedCluster, logger logr.Logger) (*unstructured.Unstructured, error) {
	logger.V(4).Info("searching for Velero backups", "namespace", r.OADPNamespace, "cluster", hc.Name)

	// Define the GVK for Velero Backup resources
	backupList := &unstructured.UnstructuredList{}
	backupList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "velero.io",
		Version: "v1",
		Kind:    "BackupList",
	})

	// List all backups in the OADP namespace
	if err := r.Client.List(ctx, backupList, client.InNamespace(r.OADPNamespace)); err != nil {
		if errors.IsNotFound(err) {
			logger.V(4).Info("no Velero backups found", "namespace", r.OADPNamespace)
			return nil, nil
		}
		return nil, fmt.Errorf("error listing Velero backups in namespace %s: %w", r.OADPNamespace, err)
	}

	// Filter backups that might be related to this HostedCluster
	var relatedBackups []unstructured.Unstructured
	for _, backup := range backupList.Items {
		if oadp.IsBackupRelatedToCluster(backup, hc) {
			relatedBackups = append(relatedBackups, backup)
			logger.V(4).Info("found related backup", "backup", backup.GetName(), "namespace", backup.GetNamespace())
		}
	}

	if len(relatedBackups) == 0 {
		logger.V(4).Info("no related backups found", "cluster", hc.Name)
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
	logger.V(4).Info("backup search completed",
		"cluster", hc.Name,
		"foundBackups", len(relatedBackups),
		"lastBackup", lastBackup.GetName())

	return lastBackup, nil
}

// checkOADPRecovery checks if a HostedCluster paused by OADP should be unpaused
func (r *OADPRecoveryRunner) CheckOADPRecovery(ctx context.Context, hc *hyperv1.HostedCluster, logger logr.Logger) (bool, error) {
	// First, verify this cluster was paused by the OADP plugin
	if !oadp.HasOADPPauseAnnotations(hc) {
		logger.V(4).Info("cluster not paused by OADP plugin")
		return false, nil
	}

	logger.V(4).Info("cluster paused by OADP plugin, checking backup status",
		"pausedAt", hc.Annotations[oadp.OADPAuditPausedAtAnnotation])

	// Find backups related to this cluster
	lastRelatedBackup, err := r.findLastRelatedBackup(ctx, hc, logger)
	if err != nil {
		return false, fmt.Errorf("error searching for related backups: %w", err)
	}

	// If no backups are found, we should remove the pause annotations and unpause the cluster
	if lastRelatedBackup == nil {
		logger.V(4).Info("no related backups found for OADP-paused cluster, should unpause the cluster")
		return true, nil
	}

	// Check if the last backup is in terminal state
	isTerminal, phase, err := oadp.IsBackupInTerminalState(ctx, *lastRelatedBackup, logger)
	if err != nil {
		return false, fmt.Errorf("error checking backup terminal state: %w", err)
	}

	if isTerminal {
		logger.V(4).Info("last backup is in terminal state - should unpause the cluster",
			"backup", lastRelatedBackup.GetName(),
			"namespace", lastRelatedBackup.GetNamespace(),
			"phase", phase)
		return true, nil
	}

	// If the last backup is still in progress, keep the cluster paused
	logger.V(4).Info("last backup still in progress, should keep the cluster paused",
		"backup", lastRelatedBackup.GetName(),
		"namespace", lastRelatedBackup.GetNamespace(),
		"phase", phase)

	return false, nil
}

// resumeClusterFromHangedOADPBackup removes OADP pause annotations and resumes the HostedCluster and its NodePools
// Returns the list of NodePool names that were resumed
func (r *OADPRecoveryRunner) resumeClusterFromHangedOADPBackup(ctx context.Context, hc *hyperv1.HostedCluster, logger logr.Logger) ([]string, error) {
	logger.Info("resuming cluster from hanged OADP backup",
		"pausedAt", hc.Annotations[oadp.OADPAuditPausedAtAnnotation])

	// Update the HostedCluster to remove OADP annotations and unpause
	updatedHC := hc.DeepCopy()

	// Remove OADP pause annotations
	annotations := updatedHC.GetAnnotations()
	if annotations != nil {
		delete(annotations, oadp.OADPAuditPausedByAnnotation)
		delete(annotations, oadp.OADPAuditPausedAtAnnotation)
		updatedHC.SetAnnotations(annotations)
	}

	// Clear the pausedUntil field to unpause the cluster
	updatedHC.Spec.PausedUntil = nil

	if err := r.Client.Update(ctx, updatedHC); err != nil {
		return nil, fmt.Errorf("failed to update HostedCluster to remove OADP pause: %w", err)
	}

	logger.Info("successfully resumed HostedCluster from OADP")

	// Get all NodePools associated with this HostedCluster
	nodePools, err := r.listNodePools(ctx, hc.Namespace, hc.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to list NodePools for cluster %s: %w", hc.Name, err)
	}

	var nodePoolNames []string

	// Resume all NodePools associated with this cluster
	for i := range nodePools.Items {
		nodePool := &nodePools.Items[i]

		logger.Info("NodePool needs to be unpaused", "cluster", hc.Name, "nodepool", nodePool.Name)

		// Remove OADP pause annotations from NodePool
		annotations := nodePool.GetAnnotations()
		if annotations != nil {
			delete(annotations, oadp.OADPAuditPausedByAnnotation)
			delete(annotations, oadp.OADPAuditPausedAtAnnotation)
			nodePool.SetAnnotations(annotations)
		}

		// Clear the pausedUntil field to unpause the NodePool
		nodePool.Spec.PausedUntil = nil

		if err := r.Client.Update(ctx, nodePool); err != nil {
			return nil, fmt.Errorf("failed to update NodePool %s to remove OADP pause: %w", nodePool.Name, err)
		}

		logger.Info("successfully resumed NodePool from hanged OADP backup",
			"nodePool", nodePool.Name)

		nodePoolNames = append(nodePoolNames, nodePool.Name)
	}

	logger.Info("successfully resumed cluster and all associated NodePools from hanged OADP backup",
		"nodePoolsResumed", len(nodePools.Items))

	return nodePoolNames, nil
}

// listNodePools lists NodePools for a specific HostedCluster
func (r *OADPRecoveryRunner) listNodePools(ctx context.Context, namespace, clusterName string) (*hyperv1.NodePoolList, error) {
	var nodePools hyperv1.NodePoolList

	// List NodePools in the same namespace
	if err := r.Client.List(ctx, &nodePools, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list NodePools in namespace %s: %w", namespace, err)
	}

	// Filter NodePools that belong to this cluster
	var filteredPools hyperv1.NodePoolList
	for _, pool := range nodePools.Items {
		if pool.Spec.ClusterName == clusterName {
			filteredPools.Items = append(filteredPools.Items, pool)
		}
	}

	return &filteredPools, nil
}
