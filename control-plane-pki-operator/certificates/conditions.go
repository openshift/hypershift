package certificates

import (
	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
)

// The following code comes from core Kube, which can't be imported, unfortunately. These methods are copied from:
// https://github.com/kubernetes/kubernetes/blob/ec5096fa869b801d6eb1bf019819287ca61edc4d/pkg/controller/certificates/certificate_controller_utils.go#L24-L51

// IsCertificateRequestApproved returns true if a certificate request has the
// "Approved" condition and no "Denied" conditions; false otherwise.
func IsCertificateRequestApproved(csr *certificatesv1.CertificateSigningRequest) bool {
	approved, denied := GetCertApprovalCondition(&csr.Status)
	return approved && !denied
}

// HasTrueCondition returns true if the csr contains a condition of the specified type with a status that is set to True or is empty
func HasTrueCondition(csr *certificatesv1.CertificateSigningRequest, conditionType certificatesv1.RequestConditionType) bool {
	for _, c := range csr.Status.Conditions {
		if c.Type == conditionType && (len(c.Status) == 0 || c.Status == corev1.ConditionTrue) {
			return true
		}
	}
	return false
}

// IsCertificateRequestPending returns true if a certificate request has no
// "Approved" or "Denied" conditions; false otherwise.
func IsCertificateRequestPending(csr *certificatesv1.CertificateSigningRequest) bool {
	approved, denied := GetCertApprovalCondition(&csr.Status)
	return !approved && !denied
}

func GetCertApprovalCondition(status *certificatesv1.CertificateSigningRequestStatus) (approved bool, denied bool) {
	for _, c := range status.Conditions {
		if c.Type == certificatesv1.CertificateApproved {
			approved = true
		}
		if c.Type == certificatesv1.CertificateDenied {
			denied = true
		}
	}
	return
}
