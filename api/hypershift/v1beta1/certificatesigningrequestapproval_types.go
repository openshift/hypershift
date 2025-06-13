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
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CertificateSigningRequestApprovalSpec   `json:"spec,omitempty"`
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
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CertificateSigningRequestApproval `json:"items"`
}
