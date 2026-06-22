package certificatesigningrequestapprovalcontroller

import (
	"errors"
	"testing"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestCertificateSigningRequestApprovalController_processCertificateSigningRequest(t *testing.T) {
	for _, test := range []struct {
		description string
		namespace   string
		name        string
		signerName  string
		getCSR      func(name string) (*certificatesv1.CertificateSigningRequest, error)
		getCSRA     func(namespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error)
		expectedCSR *certificatesv1.CertificateSigningRequest
		expectedErr bool
	}{
		{
			description: "no CSR found, no error",
			namespace:   "test-ns",
			name:        "test-csr",
			signerName:  "test-signer",
			getCSR: func(name string) (*certificatesv1.CertificateSigningRequest, error) {
				return nil, apierrors.NewNotFound(certificatesv1.SchemeGroupVersion.WithResource("certificatesigningrequests").GroupResource(), name)
			},
			getCSRA: func(namespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error) {
				return nil, apierrors.NewNotFound(hypershiftv1beta1.SchemeGroupVersion.WithResource("certificatesigningrequestapprovals").GroupResource(), name)
			},
			expectedErr: false, // nothing to be done
		},
		{
			description: "wrong signer, no update",
			namespace:   "test-ns",
			name:        "test-csr",
			signerName:  "test-signer",
			getCSR: func(name string) (*certificatesv1.CertificateSigningRequest, error) {
				if name != "test-csr" {
					return nil, apierrors.NewNotFound(certificatesv1.SchemeGroupVersion.WithResource("certificatesigningrequests").GroupResource(), name)
				}
				return &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "wrong-one",
					},
				}, nil
			},
			getCSRA: func(namespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error) {
				if namespace != "test-ns" || name != "test-csr" {
					return nil, apierrors.NewNotFound(hypershiftv1beta1.SchemeGroupVersion.WithResource("certificatesigningrequestapprovals").GroupResource(), name)
				}
				return &certificatesv1alpha1.CertificateSigningRequestApproval{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
				}, nil
			},
		},
		{
			description: "already approved, no update",
			namespace:   "test-ns",
			name:        "test-csr",
			signerName:  "test-signer",
			getCSR: func(name string) (*certificatesv1.CertificateSigningRequest, error) {
				if name != "test-csr" {
					return nil, apierrors.NewNotFound(certificatesv1.SchemeGroupVersion.WithResource("certificatesigningrequests").GroupResource(), name)
				}
				return &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "test-signer",
					},
					Status: certificatesv1.CertificateSigningRequestStatus{
						Conditions: []certificatesv1.CertificateSigningRequestCondition{{
							Type: certificatesv1.CertificateApproved,
						}},
					},
				}, nil
			},
			getCSRA: func(namespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error) {
				if namespace != "test-ns" || name != "test-csr" {
					return nil, apierrors.NewNotFound(hypershiftv1beta1.SchemeGroupVersion.WithResource("certificatesigningrequestapprovals").GroupResource(), name)
				}
				return &certificatesv1alpha1.CertificateSigningRequestApproval{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
				}, nil
			},
		},
		{
			description: "already denied, no update",
			namespace:   "test-ns",
			name:        "test-csr",
			signerName:  "test-signer",
			getCSR: func(name string) (*certificatesv1.CertificateSigningRequest, error) {
				if name != "test-csr" {
					return nil, apierrors.NewNotFound(certificatesv1.SchemeGroupVersion.WithResource("certificatesigningrequests").GroupResource(), name)
				}
				return &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "test-signer",
					},
					Status: certificatesv1.CertificateSigningRequestStatus{
						Conditions: []certificatesv1.CertificateSigningRequestCondition{{
							Type: certificatesv1.CertificateDenied,
						}},
					},
				}, nil
			},
			getCSRA: func(namespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error) {
				if namespace != "test-ns" || name != "test-csr" {
					return nil, apierrors.NewNotFound(hypershiftv1beta1.SchemeGroupVersion.WithResource("certificatesigningrequestapprovals").GroupResource(), name)
				}
				return &certificatesv1alpha1.CertificateSigningRequestApproval{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
				}, nil
			},
		},
		{
			description: "no CSRA, no update",
			namespace:   "test-ns",
			name:        "test-csr",
			signerName:  "test-signer",
			getCSR: func(name string) (*certificatesv1.CertificateSigningRequest, error) {
				if name != "test-csr" {
					return nil, apierrors.NewNotFound(certificatesv1.SchemeGroupVersion.WithResource("certificatesigningrequests").GroupResource(), name)
				}
				return &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "test-signer",
					},
				}, nil
			},
			getCSRA: func(namespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error) {
				return nil, apierrors.NewNotFound(hypershiftv1beta1.SchemeGroupVersion.WithResource("certificatesigningrequestapprovals").GroupResource(), name)
			},
		},
		{
			description: "error getting CSRA, no update",
			namespace:   "test-ns",
			name:        "test-csr",
			signerName:  "test-signer",
			getCSR: func(name string) (*certificatesv1.CertificateSigningRequest, error) {
				if name != "test-csr" {
					return nil, apierrors.NewNotFound(certificatesv1.SchemeGroupVersion.WithResource("certificatesigningrequests").GroupResource(), name)
				}
				return &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "test-signer",
					},
				}, nil
			},
			getCSRA: func(namespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error) {
				return nil, apierrors.NewForbidden(hypershiftv1beta1.SchemeGroupVersion.WithResource("certificatesigningrequestapprovals").GroupResource(), name, errors.New("oops"))
			},
			expectedErr: true,
		},
		{
			description: "CSRA exists, update to approve",
			namespace:   "test-ns",
			name:        "test-csr",
			signerName:  "test-signer",
			getCSR: func(name string) (*certificatesv1.CertificateSigningRequest, error) {
				if name != "test-csr" {
					return nil, apierrors.NewNotFound(certificatesv1.SchemeGroupVersion.WithResource("certificatesigningrequests").GroupResource(), name)
				}
				return &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "test-signer",
					},
				}, nil
			},
			getCSRA: func(namespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error) {
				if namespace != "test-ns" || name != "test-csr" {
					return nil, apierrors.NewNotFound(hypershiftv1beta1.SchemeGroupVersion.WithResource("certificatesigningrequestapprovals").GroupResource(), name)
				}
				return &certificatesv1alpha1.CertificateSigningRequestApproval{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
				}, nil
			},
			expectedCSR: &certificatesv1.CertificateSigningRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-csr",
				},
				Spec: certificatesv1.CertificateSigningRequestSpec{
					SignerName: "test-signer",
				},
				Status: certificatesv1.CertificateSigningRequestStatus{
					Conditions: []certificatesv1.CertificateSigningRequestCondition{{
						Type:    certificatesv1.CertificateApproved,
						Status:  corev1.ConditionTrue,
						Reason:  "ApprovalPresent",
						Message: "The requisite approval resource exists.",
					}},
				},
			},
		},
	} {
		t.Run(test.description, func(t *testing.T) {
			c := CertificateSigningRequestApprovalController{
				namespace:  test.namespace,
				signerName: test.signerName,
				getCSR:     test.getCSR,
				getCSRA:    test.getCSRA,
			}
			out, _, err := c.processCertificateSigningRequest(test.name)
			if test.expectedErr && err == nil {
				t.Errorf("expected an error but got none")
			} else if !test.expectedErr && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
			if diff := cmp.Diff(test.expectedCSR, out, cmpopts.IgnoreFields(certificatesv1.CertificateSigningRequestCondition{}, "LastUpdateTime")); diff != "" {
				t.Errorf("got invalid CSR out: %v", diff)
			}
		})
	}
}
