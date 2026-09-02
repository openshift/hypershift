package capicrdmigrator

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	CurrentStorageVersion = "v1beta1"
	TargetStorageVersion  = "v1beta2"

	CAPIStorageVersionEnvVar = "CAPI_STORAGE_VERSION"

	// MigrationCompleteCondition is True when all configured CRDs have been migrated.
	MigrationCompleteCondition = "MigrationComplete"
	// ProgressingCondition is True while migration is actively running.
	ProgressingCondition = "Progressing"
	// DegradedCondition is True when errors occurred during migration.
	DegradedCondition = "Degraded"
)

type CAPIMigrationStatus struct {
	// Total number of CRDs configured for migration.
	TotalCRDs int `json:"totalCRDs"`
	// Number of CRDs that have completed migration (have the observed-generation annotation set).
	MigratedCRDs int                `json:"migratedCRDs"`
	Conditions   []metav1.Condition `json:"conditions,omitempty"`
}
