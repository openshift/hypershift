package v1beta1

// AutomatedEtcdBackupConfig configures scheduled etcd backups to cloud storage.
// When set, a CronJob snapshots etcd on schedule and uploads to the configured
// storage backend. On cluster creation, if an existing backup is found for the
// cluster's infraID, it is automatically restored.
type AutomatedEtcdBackupConfig struct {
	// schedule is a cron expression for backup frequency.
	// Defaults to hourly ("0 * * * *") if not specified.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:XValidation:rule="self.matches('^(\\\\S+\\\\s+){4}\\\\S+$')",message="schedule must be a 5-field cron expression"
	Schedule string `json:"schedule,omitempty"`

	// storage configures the cloud storage backend for backup archives.
	// +required
	Storage AutomatedEtcdBackupStorage `json:"storage"`
}

// AutomatedEtcdBackupStorage configures the storage backend for automated etcd
// backups. Exactly one storage type must be specified.
//
// +union
// +kubebuilder:validation:XValidation:rule="self.type == 'GCS' ? has(self.gcs) : !has(self.gcs)",message="gcs configuration is required when type is GCS, and forbidden otherwise"
// +kubebuilder:validation:XValidation:rule="self.type == oldSelf.type",message="storage type is immutable once set"
type AutomatedEtcdBackupStorage struct {
	// type is the storage backend type.
	// +unionDiscriminator
	// +required
	Type AutomatedEtcdBackupStorageType `json:"type"`

	// gcs configures Google Cloud Storage as the backup destination.
	// +optional
	GCS *AutomatedEtcdBackupGCS `json:"gcs,omitempty"`
}

// AutomatedEtcdBackupStorageType is a string identifying a storage backend.
// +kubebuilder:validation:Enum=GCS
type AutomatedEtcdBackupStorageType string

const (
	AutomatedEtcdBackupStorageTypeGCS AutomatedEtcdBackupStorageType = "GCS"
)

// AutomatedEtcdBackupGCS configures Google Cloud Storage as the backup
// destination.
type AutomatedEtcdBackupGCS struct {
	// bucket is the name of the GCS bucket for storing etcd snapshots.
	// The bucket must already exist and the gcpServiceAccount must have
	// roles/storage.objectAdmin on it.
	// +required
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=63
	Bucket string `json:"bucket"`

	// gcpServiceAccount is the email of a GCP service account in the
	// management cluster's GCP project. The Kubernetes ServiceAccount used
	// by the backup/restore pods will be annotated with this email for
	// GKE Workload Identity access to the GCS bucket.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=320
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9.-]+\\\\.iam\\\\.gserviceaccount\\\\.com$')",message="gcpServiceAccount must be a valid GCP service account email"
	GCPServiceAccount string `json:"gcpServiceAccount"`
}
