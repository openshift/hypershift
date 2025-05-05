package v1beta1

import corev1 "k8s.io/api/core/v1"

// IBMCloudKMSSpec defines metadata for the IBM Cloud KMS encryption strategy
type IBMCloudKMSSpec struct {
	// region is the IBM Cloud region
	// +kubebuilder:validation:MaxLength=255
	// +required
	Region string `json:"region"`
	// auth defines metadata for how authentication is done with IBM Cloud KMS
	// +required
	Auth IBMCloudKMSAuthSpec `json:"auth"`
	// keyList defines the list of keys used for data encryption
	// +kubebuilder:validation:MaxItems=100
	// +required
	KeyList []IBMCloudKMSKeyEntry `json:"keyList"`
}

// IBMCloudKMSKeyEntry defines metadata for an IBM Cloud KMS encryption key
type IBMCloudKMSKeyEntry struct {
	// crkID is the customer rook key id
	// +kubebuilder:validation:MaxLength=255
	// +required
	CRKID string `json:"crkID"`
	// instanceID is the id for the key protect instance
	// +kubebuilder:validation:MaxLength=255
	// +required
	InstanceID string `json:"instanceID"`
	// correlationID is an identifier used to track all api call usage from hypershift
	// +kubebuilder:validation:MaxLength=255
	// +required
	CorrelationID string `json:"correlationID"`
	// url is the url to call key protect apis over
	// +kubebuilder:validation:Pattern=`^https://`
	// +kubebuilder:validation:MaxLength=2048
	// +required
	URL string `json:"url"`
	// keyVersion is a unique number associated with the key. The number increments whenever a new
	// key is enabled for data encryption.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=2147483647
	// +required
	KeyVersion int `json:"keyVersion"`
}

// IBMCloudKMSAuthSpec defines metadata for how authentication is done with IBM Cloud KMS
type IBMCloudKMSAuthSpec struct {
	// type defines the IBM Cloud KMS authentication strategy
	// +unionDiscriminator
	// +required
	Type IBMCloudKMSAuthType `json:"type"`
	// unmanaged defines the auth metadata the customer provides to interact with IBM Cloud KMS
	// +optional
	Unmanaged *IBMCloudKMSUnmanagedAuthSpec `json:"unmanaged,omitempty"`
	// managed defines metadata around the service to service authentication strategy for the IBM Cloud
	// KMS system (all provider managed).
	// +optional
	Managed *IBMCloudKMSManagedAuthSpec `json:"managed,omitempty"`
}

// IBMCloudKMSAuthType defines the IBM Cloud KMS authentication strategy
// +kubebuilder:validation:Enum=Managed;Unmanaged
type IBMCloudKMSAuthType string

const (
	// IBMCloudKMSManagedAuth defines the KMS authentication strategy where the IKS/ROKS platform uses
	// service to service auth to call IBM Cloud KMS APIs (no customer credentials required)
	IBMCloudKMSManagedAuth IBMCloudKMSAuthType = "Managed"
	// IBMCloudKMSUnmanagedAuth defines the KMS authentication strategy where a customer supplies IBM Cloud
	// authentication to interact with IBM Cloud KMS APIs
	IBMCloudKMSUnmanagedAuth IBMCloudKMSAuthType = "Unmanaged"
)

// IBMCloudKMSUnmanagedAuthSpec defines the auth metadata the customer provides to interact with IBM Cloud KMS
type IBMCloudKMSUnmanagedAuthSpec struct {
	// credentials should reference a secret with a key field of IBMCloudIAMAPIKeySecretKey that contains a apikey to
	// call IBM Cloud KMS APIs
	// +required
	Credentials corev1.LocalObjectReference `json:"credentials"`
}

// IBMCloudKMSManagedAuthSpec defines metadata around the service to service authentication strategy for the IBM Cloud
// KMS system (all provider managed).
type IBMCloudKMSManagedAuthSpec struct {
}
