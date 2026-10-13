package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +kubebuilder:resource:path=certificatesigningrequestapprovals,shortName=csra;csras,scope=Namespaced
// +kubebuilder:object:root=true
// +kubebuilder:storageversion

// CertificateSigningRequestApproval defines the desired state of CertificateSigningRequestApproval
type CertificateSigningRequestApproval struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the specification of the desired behavior of the CertificateSigningRequestApproval.
	// +optional
	Spec CertificateSigningRequestApprovalSpec `json:"spec,omitempty"`

	// status is the most recently observed status of the CertificateSigningRequestApproval.
	// +optional
	Status CertificateSigningRequestApprovalStatus `json:"status,omitempty"`
}

// CertificateSigningRequestApprovalSpec defines the desired state of CertificateSigningRequestApproval
type CertificateSigningRequestApprovalSpec struct{}

// CertificateSigningRequestApprovalStatus defines the observed state of CertificateSigningRequestApproval
type CertificateSigningRequestApprovalStatus struct{}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// CertificateSigningRequestApprovalList contains a list of CertificateSigningRequestApprovals.
type CertificateSigningRequestApprovalList struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is standard list metadata.
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// items is the list of CertificateSigningRequestApprovals.
	// +required
	// +kubebuilder:validation:MaxItems=1000
	Items []CertificateSigningRequestApproval `json:"items"`
}
