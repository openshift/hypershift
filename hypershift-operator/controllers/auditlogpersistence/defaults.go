package auditlogpersistence

import (
	auditlogpersistencev1alpha1 "github.com/openshift/hypershift/api/auditlogpersistence/v1alpha1"

	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	// DefaultEnabled is the default value for Spec.Enabled
	DefaultEnabled = false

	// DefaultStorageSize is the default value for Storage.Size
	DefaultStorageSize = "5Gi"

	// DefaultAuditLogMaxSize is the default value for AuditLog.MaxSize
	DefaultAuditLogMaxSize = int32(200)

	// DefaultAuditLogMaxBackup is the default value for AuditLog.MaxBackup
	DefaultAuditLogMaxBackup = int32(10)

	// DefaultSnapshotsEnabled is the default value for Snapshots.Enabled
	DefaultSnapshotsEnabled = false

	// DefaultSnapshotsMinInterval is the default value for Snapshots.MinInterval
	DefaultSnapshotsMinInterval = "1h"

	// DefaultSnapshotsPerPodRetentionCount is the default value for Snapshots.PerPodRetentionCount
	DefaultSnapshotsPerPodRetentionCount = int32(10)

	// DefaultSnapshotsNamespaceRetentionCount is the default value for Snapshots.NamespaceRetentionCount
	DefaultSnapshotsNamespaceRetentionCount = int32(50)
)

// ApplyDefaults applies default values to an AuditLogPersistenceConfigSpec.
// This function ensures that all fields have their default values when not explicitly set.
func ApplyDefaults(spec *auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec) {
	// Apply storage defaults
	if spec.Storage.Size.IsZero() {
		qty, err := resource.ParseQuantity(DefaultStorageSize)
		if err == nil {
			spec.Storage.Size = qty
		}
	}

	// Apply audit log defaults
	if spec.AuditLog.MaxSize == nil {
		defaultVal := DefaultAuditLogMaxSize
		spec.AuditLog.MaxSize = &defaultVal
	}
	if spec.AuditLog.MaxBackup == nil {
		defaultVal := DefaultAuditLogMaxBackup
		spec.AuditLog.MaxBackup = &defaultVal
	}

	// Apply snapshot defaults
	// For Snapshots.Enabled, default is false (zero value), so no action needed
	if spec.Snapshots.MinInterval == "" {
		spec.Snapshots.MinInterval = DefaultSnapshotsMinInterval
	}
	if spec.Snapshots.PerPodRetentionCount == nil {
		defaultVal := DefaultSnapshotsPerPodRetentionCount
		spec.Snapshots.PerPodRetentionCount = &defaultVal
	}
	if spec.Snapshots.NamespaceRetentionCount == nil {
		defaultVal := DefaultSnapshotsNamespaceRetentionCount
		spec.Snapshots.NamespaceRetentionCount = &defaultVal
	}
}

// IsEnabled returns the enabled value (default is false, so no special handling needed)
func IsEnabled(spec *auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec) bool {
	return spec.Enabled
}

// IsSnapshotsEnabled returns the snapshots enabled value.
// ApplyDefaults should be called first to ensure defaults are applied.
func IsSnapshotsEnabled(spec *auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec) bool {
	return spec.Snapshots.Enabled
}
