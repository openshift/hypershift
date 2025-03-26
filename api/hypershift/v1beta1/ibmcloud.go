package v1beta1

import corev1 "k8s.io/api/core/v1"

// IBMCloudKMSSpec defines metadata for the IBM Cloud KMS encryption strategy
type IBMCloudKMSSpec struct {
	// Region is the IBM Cloud region
	Region string `json:"region"`
	// Auth defines metadata for how authentication is done with IBM Cloud KMS
	Auth IBMCloudKMSAuthSpec `json:"auth"`
	// KeyList defines the list of keys used for data encryption
	KeyList []IBMCloudKMSKeyEntry `json:"keyList"`
}

// IBMCloudKMSKeyEntry defines metadata for an IBM Cloud KMS encryption key
type IBMCloudKMSKeyEntry struct {
	// CRKID is the customer rook key id
	CRKID string `json:"crkID"`
	// InstanceID is the id for the key protect instance
	InstanceID string `json:"instanceID"`
	// CorrelationID is an identifier used to track all api call usage from hypershift
	CorrelationID string `json:"correlationID"`
	// URL is the url to call key protect apis over
	// +kubebuilder:validation:Pattern=`^https://`
	URL string `json:"url"`
	// KeyVersion is a unique number associated with the key. The number increments whenever a new
	// key is enabled for data encryption.
	KeyVersion int `json:"keyVersion"`
}

// IBMCloudKMSAuthSpec defines metadata for how authentication is done with IBM Cloud KMS
type IBMCloudKMSAuthSpec struct {
	// Type defines the IBM Cloud KMS authentication strategy
	// +unionDiscriminator
	Type IBMCloudKMSAuthType `json:"type"`
	// Unmanaged defines the auth metadata the customer provides to interact with IBM Cloud KMS
	// +optional
	Unmanaged *IBMCloudKMSUnmanagedAuthSpec `json:"unmanaged,omitempty"`
	// Managed defines metadata around the service to service authentication strategy for the IBM Cloud
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
	// Credentials should reference a secret with a key field of IBMCloudIAMAPIKeySecretKey that contains a apikey to
	// call IBM Cloud KMS APIs
	Credentials corev1.LocalObjectReference `json:"credentials"`
}

// IBMCloudKMSManagedAuthSpec defines metadata around the service to service authentication strategy for the IBM Cloud
// KMS system (all provider managed).
type IBMCloudKMSManagedAuthSpec struct {
}
