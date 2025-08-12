package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +kubebuilder:resource:path=certificaterevocationrequests,shortName=crr;crrs,scope=Namespaced
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion

// CertificateRevocationRequest defines the desired state of CertificateRevocationRequest.
// A request denotes the user's desire to revoke a signer certificate of the class indicated in spec.
type CertificateRevocationRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CertificateRevocationRequestSpec   `json:"spec,omitempty"`
	Status CertificateRevocationRequestStatus `json:"status,omitempty"`
}

// CertificateRevocationRequestSpec defines the desired state of CertificateRevocationRequest
type CertificateRevocationRequestSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=customer-break-glass;sre-break-glass
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="signerClass is immutable"
	// +kubebuilder:validation:MaxLength=255
	// SignerClass identifies the class of signer to revoke. All the active signing CAs for the
	// signer class will be revoked.
	SignerClass string `json:"signerClass"`
}

const (
	SignerClassValidType     string = "SignerClassValid"
	SignerClassUnknownReason string = "SignerClassUnknown"

	RootCertificatesRegeneratedType string = "RootCertificatesRegenerated"
	RootCertificatesStaleReason     string = "RootCertificatesStale"

	LeafCertificatesRegeneratedType string = "LeafCertificatesRegenerated"
	LeafCertificatesStaleReason     string = "LeafCertificatesStale"

	NewCertificatesTrustedType      = "NewCertificatesTrusted"
	PreviousCertificatesRevokedType = "PreviousCertificatesRevoked"
)

// CertificateRevocationRequestStatus defines the observed state of CertificateRevocationRequest
type CertificateRevocationRequestStatus struct {
	// RevocationTimestamp is the cut-off time for signing CAs to be revoked. All certificates that
	// are valid before this time will be revoked; all re-generated certificates will not be valid
	// at or before this time.
	// +optional
	RevocationTimestamp *metav1.Time `json:"revocationTimestamp,omitempty"` //nolint:kubeapilinter

	// PreviousSigner stores a reference to the previous signer certificate. We require
	// storing this data to ensure that we can validate that the old signer is no longer
	// valid before considering revocation complete.
	// +optional
	PreviousSigner *corev1.LocalObjectReference `json:"previousSigner,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +kubebuilder:validation:MaxItems=10
	// Conditions contain details about the various aspects of certificate revocation.
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true

// CertificateRevocationRequestList contains a list of CertificateRevocationRequest.
type CertificateRevocationRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CertificateRevocationRequest `json:"items"`
}
