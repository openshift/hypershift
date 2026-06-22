package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func init() {
	SchemeBuilder.Register(func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(SchemeGroupVersion,
			&HCPEtcdBackup{},
			&HCPEtcdBackupList{},
		)
		return nil
	})
}

// Condition types and reasons for HCPEtcdBackup.
const (
	// BackupCompleted indicates whether the etcd backup has completed.
	BackupCompleted ConditionType = "BackupCompleted"

	BackupSucceededReason  string = "BackupSucceeded"
	BackupFailedReason     string = "BackupFailed"
	BackupInProgressReason string = "BackupInProgress"
	BackupRejectedReason   string = "BackupRejected"
	EtcdUnhealthyReason    string = "EtcdUnhealthy"
)

// HCPEtcdBackupStorageType is the type of storage for etcd backups.
// +kubebuilder:validation:Enum=S3;AzureBlob
type HCPEtcdBackupStorageType string

const (
	// S3BackupStorage indicates that the backup is stored in AWS S3.
	S3BackupStorage HCPEtcdBackupStorageType = "S3"

	// AzureBlobBackupStorage indicates that the backup is stored in Azure Blob Storage.
	AzureBlobBackupStorage HCPEtcdBackupStorageType = "AzureBlob"
)

// SecretReference contains a reference to a Secret by name.
// The Secret must exist in the same namespace as the referencing resource.
type SecretReference struct {
	// name is the name of the Secret. It must be a valid DNS-1123 subdomain: at most
	// 253 characters, consisting of lowercase alphanumeric characters, hyphens, and periods.
	// Each period-separated segment must start and end with an alphanumeric character.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$')",message="name must consist only of lowercase alphanumeric characters, hyphens, and periods. Each period-separated segment must start and end with an alphanumeric character."
	Name string `json:"name,omitempty"`
}

// HCPEtcdBackupSpec defines the desired state of HCPEtcdBackup.
// HCPEtcdBackup is a one-shot backup request; the entire spec is immutable once created.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="HCPEtcdBackupSpec is immutable"
type HCPEtcdBackupSpec struct {
	// storage defines the cloud storage backend where the etcd snapshot will be uploaded.
	// +required
	Storage HCPEtcdBackupStorage `json:"storage,omitzero"`
}

// HCPEtcdBackupStorage defines the cloud storage backend configuration for the backup.
// Exactly one storage backend must be specified, matching the storageType discriminator.
// +union
// +kubebuilder:validation:XValidation:rule="self.storageType == 'S3' ? has(self.s3) : !has(self.s3)",message="s3 configuration is required when storageType is S3, and forbidden otherwise"
// +kubebuilder:validation:XValidation:rule="self.storageType == 'AzureBlob' ? has(self.azureBlob) : !has(self.azureBlob)",message="azureBlob configuration is required when storageType is AzureBlob, and forbidden otherwise"
type HCPEtcdBackupStorage struct {
	// storageType specifies the type of cloud storage backend for the etcd backup.
	// Valid values are "S3" for AWS S3 storage and "AzureBlob" for Azure Blob Storage.
	// +unionDiscriminator
	// +required
	StorageType HCPEtcdBackupStorageType `json:"storageType,omitempty"`

	// s3 specifies the S3 storage configuration for the etcd backup.
	// Required when storageType is "S3", and forbidden otherwise.
	// +optional
	// +unionMember
	S3 HCPEtcdBackupS3 `json:"s3,omitzero"`

	// azureBlob specifies the Azure Blob storage configuration for the etcd backup.
	// Required when storageType is "AzureBlob", and forbidden otherwise.
	// +optional
	// +unionMember
	AzureBlob HCPEtcdBackupAzureBlob `json:"azureBlob,omitzero"`
}

// HCPEtcdBackupS3 defines the S3 storage configuration for etcd backups.
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.kmsKeyARN) || has(self.kmsKeyARN)",message="kmsKeyARN cannot be removed once set"
type HCPEtcdBackupS3 struct {
	// bucket is the name of the S3 bucket where backups are stored.
	// Must be 3-63 characters, lowercase letters, numbers, hyphens, and periods only.
	// Must start and end with a letter or number. Consecutive periods are not allowed.
	// See https://docs.aws.amazon.com/AmazonS3/latest/userguide/bucketnamingrules.html
	// +required
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z0-9][a-z0-9.-]*[a-z0-9]$')",message="bucket must consist of lowercase letters, numbers, hyphens, and periods, and must start and end with a letter or number"
	// +kubebuilder:validation:XValidation:rule="!self.contains('..')",message="bucket must not contain consecutive periods"
	Bucket string `json:"bucket,omitempty"`

	// region is the AWS region where the S3 bucket is located (e.g. "us-east-1").
	// Must be a valid AWS region identifier: lowercase letters, digits, and hyphens.
	// Must start and end with an alphanumeric character, no consecutive hyphens.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z][a-z0-9-]*[a-z0-9]$')",message="region must consist of lowercase letters, digits, and hyphens, must start with a letter and end with an alphanumeric character"
	// +kubebuilder:validation:XValidation:rule="!self.contains('--')",message="region must not contain consecutive hyphens"
	Region string `json:"region,omitempty"`

	// keyPrefix is the S3 key prefix for the backup file.
	// Must consist of safe S3 object key characters: alphanumeric characters,
	// forward slashes, hyphens, underscores, periods, exclamation marks,
	// asterisks, single quotes, and parentheses.
	// See https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-keys.html
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-zA-Z0-9!_.*\\'()/-]+$')",message="keyPrefix must consist of safe S3 key characters: alphanumeric characters, forward slashes, hyphens, underscores, periods, exclamation marks, asterisks, single quotes, and parentheses"
	KeyPrefix string `json:"keyPrefix,omitempty"`

	// credentials references a Secret containing AWS credentials for uploading
	// to S3. The Secret must exist in the Hypershift Operator namespace and contain a
	// 'credentials' key with a valid AWS credentials file.
	// +required
	Credentials SecretReference `json:"credentials,omitzero"`

	// kmsKeyARN is the ARN of the KMS key used for server-side encryption of the backup.
	// Must be a valid AWS KMS key ARN in the format
	// "arn:<partition>:kms:<region>:<account-id>:key/<key-id>"
	// where partition is one of aws, aws-cn, or aws-us-gov.
	// This field is immutable once set and cannot be removed.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:XValidation:rule="self.matches('^arn:(aws|aws-cn|aws-us-gov):kms:[a-z0-9-]+:[0-9]{12}:key/[a-zA-Z0-9-]+$')",message="kmsKeyARN must be a valid AWS KMS key ARN (arn:<partition>:kms:<region>:<account-id>:key/<key-id>)"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="kmsKeyARN is immutable"
	KMSKeyARN string `json:"kmsKeyARN,omitempty"`
}

// HCPEtcdBackupAzureBlob defines the Azure Blob storage configuration for etcd backups.
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.encryptionKeyURL) || has(self.encryptionKeyURL)",message="encryptionKeyURL cannot be removed once set"
type HCPEtcdBackupAzureBlob struct {
	// container is the name of the Azure Blob container where backups are stored.
	// Must be 3-63 characters, lowercase letters, numbers, and hyphens only.
	// Must start and end with a letter or number. Consecutive hyphens are not allowed.
	// See https://learn.microsoft.com/en-us/rest/api/storageservices/naming-and-referencing-containers--blobs--and-metadata#container-names
	// +required
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z0-9]([a-z0-9-]*[a-z0-9])?$')",message="container must consist of lowercase letters, numbers, and hyphens, and must start and end with a letter or number"
	// +kubebuilder:validation:XValidation:rule="!self.contains('--')",message="container must not contain consecutive hyphens"
	Container string `json:"container,omitempty"`

	// storageAccount is the name of the Azure Storage Account.
	// Must be 3-24 characters, lowercase letters and numbers only.
	// See https://learn.microsoft.com/en-us/azure/storage/common/storage-account-overview#storage-account-name
	// +required
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=24
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z0-9]+$')",message="storageAccount must consist of lowercase letters and numbers only"
	StorageAccount string `json:"storageAccount,omitempty"`

	// keyPrefix is the blob name prefix for the backup file.
	// Must consist of valid blob name characters: alphanumeric characters, forward slashes,
	// hyphens, underscores, and periods.
	// See https://learn.microsoft.com/en-us/rest/api/storageservices/naming-and-referencing-containers--blobs--and-metadata#blob-names
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-zA-Z0-9/_.-]+$')",message="keyPrefix must consist of alphanumeric characters, forward slashes, hyphens, underscores, and periods"
	KeyPrefix string `json:"keyPrefix,omitempty"`

	// credentials references a Secret containing Azure credentials for uploading
	// to Blob Storage. The Secret must exist in the Hypershift Operator namespace.
	// +required
	Credentials SecretReference `json:"credentials,omitzero"`

	// encryptionKeyURL is the URL of the Azure Key Vault key used for encryption.
	// Must be a valid Azure Key Vault key URL in the format
	// "https://<vault-name>.vault.azure.net/keys/<key-name>[/<key-version>]".
	// This field is immutable once set and cannot be removed.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=210
	// +kubebuilder:validation:XValidation:rule="self.matches('^https://[a-zA-Z0-9-]+\\\\.vault\\\\.azure\\\\.net/keys/[a-zA-Z0-9-]+(/[a-zA-Z0-9]+)?$')",message="encryptionKeyURL must be a valid Azure Key Vault HTTPS URL (https://<vault>.vault.azure.net/keys/<key-name>[/<key-version>])"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="encryptionKeyURL is immutable"
	EncryptionKeyURL string `json:"encryptionKeyURL,omitempty"`
}

// HCPEtcdBackupStatus defines the observed state of HCPEtcdBackup.
// +kubebuilder:validation:MinProperties=1
type HCPEtcdBackupStatus struct {
	// conditions contains details for the current state of the etcd backup.
	// The following condition types are expected:
	// - "BackupCompleted": indicates whether the etcd backup has completed (True=success, False=failure).
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=10
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// snapshotURL is the URL of the completed backup snapshot in cloud storage.
	// Must be a valid URL with scheme https or s3.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="self.matches('^(https|s3)://.*')",message="snapshotURL must be a valid URL with scheme https or s3"
	SnapshotURL string `json:"snapshotURL,omitempty"`

	// encryptionMetadata contains metadata about the encryption of the backup.
	// When present, at least one platform-specific encryption block must be set.
	// +optional
	EncryptionMetadata HCPEtcdBackupEncryptionMetadata `json:"encryptionMetadata,omitzero"`
}

// HCPEtcdBackupEncryptionMetadata contains platform-specific metadata about the
// encryption applied to the backup artifact in cloud storage.
// The presence of a platform block indicates that encryption was applied.
// +kubebuilder:validation:MinProperties=1
// +kubebuilder:validation:MaxProperties=1
type HCPEtcdBackupEncryptionMetadata struct {
	// aws contains AWS-specific encryption metadata for the backup.
	// +optional
	AWS HCPEtcdBackupEncryptionMetadataAWS `json:"aws,omitzero"`

	// azure contains Azure-specific encryption metadata for the backup.
	// +optional
	Azure HCPEtcdBackupEncryptionMetadataAzure `json:"azure,omitzero"`
}

// HCPEtcdBackupEncryptionMetadataAWS contains AWS-specific encryption metadata.
// The values here reflect the encryption settings from the HCPEtcdBackupConfig input.
type HCPEtcdBackupEncryptionMetadataAWS struct {
	// kmsKeyARN is the ARN of the KMS key used for server-side encryption of the backup in S3.
	// Must be a valid AWS KMS key ARN in the format
	// "arn:<partition>:kms:<region>:<account-id>:key/<key-id>"
	// where partition is one of aws, aws-cn, or aws-us-gov.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:XValidation:rule="self.matches('^arn:(aws|aws-cn|aws-us-gov):kms:[a-z0-9-]+:[0-9]{12}:key/[a-zA-Z0-9-]+$')",message="kmsKeyARN must be a valid AWS KMS key ARN (arn:<partition>:kms:<region>:<account-id>:key/<key-id>)"
	KMSKeyARN string `json:"kmsKeyARN,omitempty"`
}

// HCPEtcdBackupEncryptionMetadataAzure contains Azure-specific encryption metadata.
// The values here reflect the encryption settings from the HCPEtcdBackupConfig input.
type HCPEtcdBackupEncryptionMetadataAzure struct {
	// encryptionKeyURL is the URL of the Azure Key Vault key used for encryption of the backup.
	// Must be a valid Azure Key Vault key URL in the format
	// "https://<vault-name>.vault.azure.net/keys/<key-name>[/<key-version>]".
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=210
	// +kubebuilder:validation:XValidation:rule="self.matches('^https://[a-zA-Z0-9-]+\\\\.vault\\\\.azure\\\\.net/keys/[a-zA-Z0-9-]+(/[a-zA-Z0-9]+)?$')",message="encryptionKeyURL must be a valid Azure Key Vault HTTPS URL (https://<vault>.vault.azure.net/keys/<key-name>[/<key-version>])"
	EncryptionKeyURL string `json:"encryptionKeyURL,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=hcpetcdbackups,scope=Namespaced,shortName=hcpetcdbk
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Completed",type="string",JSONPath=".status.conditions[?(@.type==\"BackupCompleted\")].status",description="Backup completion status"
// +kubebuilder:printcolumn:name="URL",type="string",JSONPath=".status.snapshotURL",description="Snapshot URL"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +openshift:enable:FeatureGate=HCPEtcdBackup

// HCPEtcdBackup represents a request to back up etcd for a hosted control plane.
// This resource is feature-gated behind the HCPEtcdBackup feature gate.
type HCPEtcdBackup struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the metadata for the HCPEtcdBackup.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// spec is the specification for the HCPEtcdBackup.
	// +required
	Spec HCPEtcdBackupSpec `json:"spec,omitzero"`
	// status is the status of the HCPEtcdBackup.
	// +optional
	Status HCPEtcdBackupStatus `json:"status,omitzero"`
}

// HCPEtcdBackupList contains a list of HCPEtcdBackup.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type HCPEtcdBackupList struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is standard list metadata.
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	// items is the list of HCPEtcdBackups.
	// +required
	Items []HCPEtcdBackup `json:"items,omitempty"`
}

// HCPEtcdBackupConfigPlatform identifies the cloud platform for backup encryption configuration.
// +kubebuilder:validation:Enum=AWS;Azure
type HCPEtcdBackupConfigPlatform string

const (
	// AWSBackupConfigPlatform indicates AWS KMS encryption for backup artifacts.
	AWSBackupConfigPlatform HCPEtcdBackupConfigPlatform = "AWS"

	// AzureBackupConfigPlatform indicates Azure Key Vault encryption for backup artifacts.
	AzureBackupConfigPlatform HCPEtcdBackupConfigPlatform = "Azure"
)

// HCPEtcdBackupConfig defines the backup encryption configuration that is propagated
// from the HostedCluster to the HostedControlPlane via ManagedEtcdSpec.
// Exactly one platform-specific block must be specified, matching the platform discriminator.
// +union
// +kubebuilder:validation:XValidation:rule="self.platform == 'AWS' ? has(self.aws) : !has(self.aws)",message="aws configuration is required when platform is AWS, and forbidden otherwise"
// +kubebuilder:validation:XValidation:rule="self.platform == 'Azure' ? has(self.azure) : !has(self.azure)",message="azure configuration is required when platform is Azure, and forbidden otherwise"
type HCPEtcdBackupConfig struct {
	// platform specifies the cloud platform for backup encryption configuration.
	// Valid values are "AWS" for AWS KMS encryption and "Azure" for Azure Key Vault encryption.
	// +unionDiscriminator
	// +required
	Platform HCPEtcdBackupConfigPlatform `json:"platform,omitempty"`

	// aws contains AWS-specific backup encryption configuration.
	// Required when platform is "AWS", and forbidden otherwise.
	// +optional
	// +unionMember
	AWS HCPEtcdBackupConfigAWS `json:"aws,omitzero"`

	// azure contains Azure-specific backup encryption configuration.
	// Required when platform is "Azure", and forbidden otherwise.
	// +optional
	// +unionMember
	Azure HCPEtcdBackupConfigAzure `json:"azure,omitzero"`
}

// HCPEtcdBackupConfigAWS defines AWS-specific encryption settings for etcd backups.
type HCPEtcdBackupConfigAWS struct {
	// kmsKeyARN is the ARN of the AWS KMS key to use for encrypting etcd backup artifacts in S3.
	// Must be a valid AWS KMS key ARN in the format
	// "arn:<partition>:kms:<region>:<account-id>:key/<key-id>"
	// where partition is one of aws, aws-cn, or aws-us-gov.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:XValidation:rule="self.matches('^arn:(aws|aws-cn|aws-us-gov):kms:[a-z0-9-]+:[0-9]{12}:key/[a-zA-Z0-9-]+$')",message="kmsKeyARN must be a valid AWS KMS key ARN (arn:<partition>:kms:<region>:<account-id>:key/<key-id>)"
	KMSKeyARN string `json:"kmsKeyARN,omitempty"`
}

// HCPEtcdBackupConfigAzure defines Azure-specific encryption settings for etcd backups.
type HCPEtcdBackupConfigAzure struct {
	// encryptionKeyURL is the URL of the Azure Key Vault key to use for encrypting etcd backup artifacts.
	// Must be a valid Azure Key Vault key URL in the format
	// "https://<vault-name>.vault.azure.net/keys/<key-name>[/<key-version>]".
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=210
	// +kubebuilder:validation:XValidation:rule="self.matches('^https://[a-zA-Z0-9-]+\\\\.vault\\\\.azure\\\\.net/keys/[a-zA-Z0-9-]+(/[a-zA-Z0-9]+)?$')",message="encryptionKeyURL must be a valid Azure Key Vault HTTPS URL (https://<vault>.vault.azure.net/keys/<key-name>[/<key-version>])"
	EncryptionKeyURL string `json:"encryptionKeyURL,omitempty"`
}
