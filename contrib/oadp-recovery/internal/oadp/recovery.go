package oadp

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	// OADP audit paused annotations
	OADPAuditPausedAtAnnotation = "oadp.openshift.io/paused-at"
	OADPAuditPausedByAnnotation = "oadp.openshift.io/paused-by"
	OADPAuditPausedPluginAuthor = "hypershift-oadp-plugin"
)

var (
	// TerminalStates is the list of terminal states for Velero backups
	TerminalStates = []string{"Completed", "Failed", "PartiallyFailed", "Deleted"}
)

// HasOADPPauseAnnotations checks if the HostedCluster has the specific OADP pause annotations
func HasOADPPauseAnnotations(hc *hyperv1.HostedCluster) bool {
	if hc == nil {
		return false
	}

	annotations := hc.GetAnnotations()
	if annotations == nil {
		return false
	}

	pausedBy := annotations[OADPAuditPausedByAnnotation]
	pausedAt := annotations[OADPAuditPausedAtAnnotation]

	return pausedBy == OADPAuditPausedPluginAuthor && pausedAt != ""
}

// IsBackupInTerminalState checks if a Velero backup is in a terminal state
func IsBackupInTerminalState(ctx context.Context, backup unstructured.Unstructured, logger logr.Logger) (bool, string, error) {
	logger.V(4).Info("checking backup terminal state", "backup", backup.GetName())

	// Extract status.phase from the backup using unstructured access
	phase, found, err := unstructured.NestedString(backup.Object, "status", "phase")
	logger.V(4).Info("backup phase", "phase", phase, "found", found, "err", err)

	if err != nil || !found {
		return false, "", fmt.Errorf("error getting backup phase: %w", err)
	}

	// Check if the phase is one of the terminal states
	for _, terminalState := range TerminalStates {
		if phase == terminalState {
			logger.V(4).Info("terminal state found", "terminalState", terminalState)
			return true, phase, nil
		}
	}

	return false, phase, nil
}

// IsBackupRelatedToCluster determines if a backup is related to the given HostedCluster
func IsBackupRelatedToCluster(backup unstructured.Unstructured, hc *hyperv1.HostedCluster) bool {
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