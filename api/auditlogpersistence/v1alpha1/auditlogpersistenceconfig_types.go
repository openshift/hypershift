package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +kubebuilder:resource:path=auditlogpersistenceconfigs,shortName=alpc;alpcs,scope=Cluster
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +genclient:nonNamespaced
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'cluster'", message="exactly one configuration may exist and must be named 'cluster'"

// AuditLogPersistenceConfig defines the desired state of AuditLogPersistenceConfig.
// Configuration options here allow management cluster administrators to configure
// persistent audit logs with automatic snapshots for kube-apiserver pods in hosted clusters.
type AuditLogPersistenceConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Spec AuditLogPersistenceConfigSpec `json:"spec,omitempty"`
	// +optional
	Status AuditLogPersistenceConfigStatus `json:"status,omitempty"`
}

// AuditLogPersistenceConfigSpec defines the desired state of AuditLogPersistenceConfig
type AuditLogPersistenceConfigSpec struct {
	// Enabled enables or disables the audit log persistence feature globally.
	// When disabled, no PVCs will be created and no snapshots will be taken.
	// Defaults to false.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Storage defines the PVC configuration for audit log storage.
	// +optional
	Storage StorageConfig `json:"storage,omitempty"`

	// AuditLog defines audit log settings that will be applied to kube-apiserver.
	// +optional
	AuditLog AuditLogConfig `json:"auditLog,omitempty"`

	// Snapshots defines snapshot configuration for crash recovery.
	// +optional
	Snapshots SnapshotConfig `json:"snapshots,omitempty"`
}

// StorageConfig defines PVC storage configuration
type StorageConfig struct {
	// StorageClassName is the name of the StorageClass to use for PVCs.
	// If not specified, the default storage class will be used.
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`

	// Size is the size of each PVC created for kube-apiserver pods.
	// Must be a valid Kubernetes quantity (e.g., "5Gi", "10Gi").
	// Defaults to "5Gi".
	// +optional
	Size resource.Quantity `json:"size,omitempty"`
}

// AuditLogConfig defines audit log settings
type AuditLogConfig struct {
	// MaxSize is the maximum size in megabytes of the audit log file before it gets rotated.
	// This corresponds to the --audit-log-maxsize kube-apiserver argument.
	// If not specified, defaults to 200.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxSize *int32 `json:"maxSize,omitempty"`

	// MaxBackup is the maximum number of old audit log files to retain.
	// This corresponds to the --audit-log-maxbackup kube-apiserver argument.
	// If not specified, defaults to 10.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxBackup *int32 `json:"maxBackup,omitempty"`
}

// SnapshotConfig defines snapshot configuration
type SnapshotConfig struct {
	// Enabled enables or disables automatic snapshot creation on pod crashes.
	// Defaults to false.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// MinInterval is the minimum time interval between snapshots for the same pod.
	// This prevents creating too many snapshots in rapid succession.
	// Must be a valid duration string (e.g., "1h", "30m").
	// Defaults to "1h".
	// +kubebuilder:validation:Pattern=`^([0-9]+(ns|us|µs|ms|s|m|h))+$`
	// +optional
	MinInterval string `json:"minInterval,omitempty"`

	// PerPodRetentionCount is the maximum number of snapshots to retain per PVC.
	// When this limit is reached, the oldest snapshot for that PVC will be deleted.
	// If not specified, defaults to 10.
	// +kubebuilder:validation:Minimum=1
	// +optional
	PerPodRetentionCount *int32 `json:"perPodRetentionCount,omitempty"`

	// NamespaceRetentionCount is the maximum total number of snapshots to retain per namespace.
	// When this limit is reached, the oldest snapshot in the namespace will be deleted.
	// If not specified, defaults to 50.
	// +kubebuilder:validation:Minimum=1
	// +optional
	NamespaceRetentionCount *int32 `json:"namespaceRetentionCount,omitempty"`

	// VolumeSnapshotClassName is the name of the VolumeSnapshotClass to use for creating snapshots.
	// If not specified, the system will attempt to match the PVC's StorageClass provisioner
	// to an appropriate VolumeSnapshotClass.
	// +optional
	VolumeSnapshotClassName string `json:"volumeSnapshotClassName,omitempty"`
}

// AuditLogPersistenceConfigStatus defines the observed state of AuditLogPersistenceConfig
type AuditLogPersistenceConfigStatus struct {
	// Conditions represent the latest available observations of the configuration's state.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true

// AuditLogPersistenceConfigList contains a list of AuditLogPersistenceConfig
type AuditLogPersistenceConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	// +optional
	Items []AuditLogPersistenceConfig `json:"items"`
}
