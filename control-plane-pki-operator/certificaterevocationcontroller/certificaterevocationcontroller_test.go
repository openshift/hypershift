package certificaterevocationcontroller

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	certificatesv1alpha1applyconfigurations "github.com/openshift/hypershift/client/applyconfiguration/certificates/v1alpha1"
	hcpmanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificates"
	"github.com/openshift/hypershift/control-plane-pki-operator/manifests"

	librarygocrypto "github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/certrotation"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	corev1applyconfigurations "k8s.io/client-go/applyconfigurations/core/v1"
	metav1applyconfigurations "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/util/cert"
	testingclock "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// generating lots of PKI in environments where compute and/or entropy is limited (like in test containers)
// can be very slow - instead, we use precomputed PKI and allow for re-generating it if necessary
//
//go:embed testdata
var testdata embed.FS

type pkiContainer struct {
	signer        *librarygocrypto.TLSCertificateConfig
	clientCertKey *librarygocrypto.TLSCertificateConfig
	signedCert    *x509.Certificate

	raw *rawPKIContainer
}

type testData struct {
	original, future *pkiContainer
}

type rawPKIContainer struct {
	signerCert, signerKey []byte
	clientCert, clientKey []byte //todo: only need pkey for client
	signedCert            []byte
}

var revocationOffset = 1 * 365 * 24 * time.Hour

func pki(t *testing.T, rotationTime time.Time) *testData {
	td := &testData{
		original: &pkiContainer{
			raw: &rawPKIContainer{},
		},
		future: &pkiContainer{
			raw: &rawPKIContainer{},
		},
	}
	for when, into := range map[time.Time]struct {
		name string
		cfg  *pkiContainer
	}{
		rotationTime.Add(-revocationOffset): {name: "original", cfg: td.original},
		rotationTime.Add(revocationOffset):  {name: "future", cfg: td.future},
	} {
		into.cfg.raw.signerKey, into.cfg.raw.signerCert = certificateAuthorityRaw(t, into.name, testingclock.NewFakeClock(when).Now)
		signer, err := librarygocrypto.GetCAFromBytes(into.cfg.raw.signerCert, into.cfg.raw.signerKey)
		if err != nil {
			t.Fatalf("error parsing signer CA cert and key: %v", err)
		}
		into.cfg.signer = signer.Config

		into.cfg.raw.clientKey, into.cfg.raw.clientCert = certificateAuthorityRaw(t, into.name+"-client", testingclock.NewFakeClock(when).Now)
		client, err := librarygocrypto.GetCAFromBytes(into.cfg.raw.clientCert, into.cfg.raw.clientKey)
		if err != nil {
			t.Fatalf("error parsing client cert and key: %v", err)
		}
		into.cfg.clientCertKey = client.Config

		if os.Getenv("REGENERATE_PKI") != "" {
			t.Log("$REGENERATE_PKI set, generating a new signed certificate")
			signedCert, err := signer.SignCertificate(&x509.Certificate{
				Subject: pkix.Name{
					CommonName:   "customer-break-glass-test-whatever",
					Organization: []string{"system:masters"},
				},
				NotBefore:             signer.Config.Certs[0].NotBefore,
				NotAfter:              signer.Config.Certs[0].NotAfter,
				KeyUsage:              x509.KeyUsageDigitalSignature,
				ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
				BasicConstraintsValid: true,
				IsCA:                  false,
			}, client.Config.Certs[0].PublicKey)
			if err != nil {
				t.Fatalf("couldn't sign the client's certificate with the signer: %v", err)
			}
			into.cfg.signedCert = signedCert

			certPEM, err := librarygocrypto.EncodeCertificates(signedCert)
			if err != nil {
				t.Fatalf("couldn't encode signed cert: %v", err)
			}
			if err := os.WriteFile(filepath.Join("testdata", into.name+"-client.signed.tls.crt"), certPEM, 0666); err != nil {
				t.Fatalf("failed to write re-generated certificate: %v", err)
			}
			into.cfg.raw.signedCert = certPEM
		} else {
			t.Log("loading signed certificate from disk, use $REGENERATE_PKI to generate a new one")
			pemb, err := testdata.ReadFile(filepath.Join("testdata", into.name+"-client.signed.tls.crt"))
			if err != nil {
				t.Fatalf("failed to read signed cert: %v", err)
			}
			certs, err := cert.ParseCertsPEM(pemb)
			if err != nil {
				t.Fatalf("failed to parse signed cert: %v", err)
			}
			if len(certs) != 1 {
				t.Fatalf("got %d signed certs, expected one", len(certs))
			}
			into.cfg.signedCert = certs[0]
			into.cfg.raw.signedCert = pemb
		}
	}

	return td

}

func certificateAuthorityRaw(t *testing.T, prefix string, now func() time.Time) ([]byte, []byte) {
	if os.Getenv("REGENERATE_PKI") != "" {
		t.Log("$REGENERATE_PKI set, generating a new cert/key pair")
		cfg, err := librarygocrypto.UnsafeMakeSelfSignedCAConfigForDurationAtTime("test-signer", now, time.Hour*24*365*100)
		if err != nil {
			t.Fatalf("could not generate self-signed CA: %v", err)
		}

		certb, keyb, err := cfg.GetPEMBytes()
		if err != nil {
			t.Fatalf("failed to marshal CA cert and key: %v", err)
		}

		if err := os.WriteFile(filepath.Join("testdata", prefix+".tls.key"), keyb, 0666); err != nil {
			t.Fatalf("failed to write re-generated private key: %v", err)
		}

		if err := os.WriteFile(filepath.Join("testdata", prefix+".tls.crt"), certb, 0666); err != nil {
			t.Fatalf("failed to write re-generated certificate: %v", err)
		}

		return keyb, certb
	}

	t.Log("loading certificate/key pair from disk, use $REGENERATE_PKI to generate new ones")
	keyPem, err := testdata.ReadFile(filepath.Join("testdata", prefix+".tls.key"))
	if err != nil {
		t.Fatalf("failed to read private key: %v", err)
	}
	certPem, err := testdata.ReadFile(filepath.Join("testdata", prefix+".tls.crt"))
	if err != nil {
		t.Fatalf("failed to read certificate: %v", err)
	}
	return keyPem, certPem
}

func TestCertificateRevocationController_processCertificateRevocationRequest(t *testing.T) {
	revocationTime, err := time.Parse(time.RFC3339Nano, "2006-01-02T15:04:05.999999999Z")
	if err != nil {
		t.Fatalf("could not parse time: %v", err)
	}
	revocationClock := testingclock.NewFakeClock(revocationTime)
	postRevocationClock := testingclock.NewFakeClock(revocationTime.Add(revocationOffset + 1*time.Hour))

	data := pki(t, revocationTime)

	for _, testCase := range []struct {
		name                  string
		crrNamespace, crrName string
		crr                   *certificatesv1alpha1.CertificateRevocationRequest
		secrets               []*corev1.Secret
		cm                    *corev1.ConfigMap
		cms                   []*corev1.ConfigMap
		now                   func() time.Time

		expectedErr     bool
		expectedRequeue bool
		expected        *actions
	}{
		{
			name:         "invalid signer class is flagged",
			now:          revocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &certificatesv1alpha1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       certificatesv1alpha1.CertificateRevocationRequestSpec{SignerClass: "invalid"},
			},
			expected: &actions{
				crr: &certificatesv1alpha1applyconfigurations.CertificateRevocationRequestApplyConfiguration{
					ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
						Namespace: ptr.To("crr-ns"),
						Name:      ptr.To("crr-name"),
					},
					Status: &certificatesv1alpha1applyconfigurations.CertificateRevocationRequestStatusApplyConfiguration{
						Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{{
							Type:               ptr.To(certificatesv1alpha1.SignerClassValidType),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(revocationClock.Now())),
							Reason:             ptr.To(certificatesv1alpha1.SignerClassUnknownReason),
							Message:            ptr.To(`Signer class "invalid" unknown.`),
						}},
					},
				},
			},
		},
		{
			name:         "a timestamp is chosen if one does not exist",
			now:          revocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &certificatesv1alpha1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       certificatesv1alpha1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
			},
			expected: &actions{
				crr: &certificatesv1alpha1applyconfigurations.CertificateRevocationRequestApplyConfiguration{
					ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
						Namespace: ptr.To("crr-ns"),
						Name:      ptr.To("crr-name"),
					},
					Status: &certificatesv1alpha1applyconfigurations.CertificateRevocationRequestStatusApplyConfiguration{
						RevocationTimestamp: ptr.To(metav1.NewTime(revocationClock.Now())),
						Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{{
							Type:               ptr.To(certificatesv1alpha1.SignerClassValidType),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(revocationClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To(`Signer class "customer-break-glass" known.`),
						}},
					},
				},
			},
		},
		{
			name:         "current signer is copied if none exists",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &certificatesv1alpha1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       certificatesv1alpha1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
					RevocationTimestamp: ptr.To(metav1.NewTime(revocationClock.Now())),
				},
			},
			secrets: []*corev1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      manifests.CustomerSystemAdminSigner("").Name,
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.original.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.original.raw.signerKey,
				},
			}},
			expected: &actions{
				secret: &corev1applyconfigurations.SecretApplyConfiguration{
					ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
						Name:      ptr.To("1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw"),
						Namespace: ptr.To("crr-ns"),
						OwnerReferences: []metav1applyconfigurations.OwnerReferenceApplyConfiguration{{
							APIVersion: ptr.To(hypershiftv1beta1.SchemeGroupVersion.String()),
							Kind:       ptr.To("CertificateRevocationRequest"),
							Name:       ptr.To("crr-name"),
						}},
					},
					Type: ptr.To(corev1.SecretTypeTLS),
					Data: map[string][]byte{
						corev1.TLSCertKey:       data.original.raw.signerCert,
						corev1.TLSPrivateKeyKey: data.original.raw.signerKey,
					},
				},
			},
		},
		{
			name:         "status updated to contain copied signer when copy exists",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &certificatesv1alpha1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       certificatesv1alpha1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
					RevocationTimestamp: ptr.To(metav1.NewTime(revocationClock.Now())),
				},
			},
			secrets: []*corev1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      manifests.CustomerSystemAdminSigner("").Name,
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.original.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.original.raw.signerKey,
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw",
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.original.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.original.raw.signerKey,
				},
			}},
			expected: &actions{
				crr: &certificatesv1alpha1applyconfigurations.CertificateRevocationRequestApplyConfiguration{
					ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
						Namespace: ptr.To("crr-ns"),
						Name:      ptr.To("crr-name"),
					},
					Status: &certificatesv1alpha1applyconfigurations.CertificateRevocationRequestStatusApplyConfiguration{
						RevocationTimestamp: ptr.To(metav1.NewTime(revocationClock.Now())),
						PreviousSigner: &corev1.LocalObjectReference{
							Name: "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw",
						},
						Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{{
							Type:               ptr.To(certificatesv1alpha1.RootCertificatesRegeneratedType),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(postRevocationClock.Now())),
							Reason:             ptr.To(certificatesv1alpha1.RootCertificatesStaleReason),
							Message:            ptr.To(`Signer certificate crr-ns/customer-system-admin-signer needs to be regenerated.`),
						}},
					},
				},
			},
		},
		{
			name:         "copies finished means we annotate for regeneration",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &certificatesv1alpha1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       certificatesv1alpha1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
					RevocationTimestamp: ptr.To(metav1.NewTime(revocationClock.Now())),
					PreviousSigner:      &corev1.LocalObjectReference{Name: "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw"},
				},
			},
			secrets: []*corev1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      manifests.CustomerSystemAdminSigner("").Name,
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.original.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.original.raw.signerKey,
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw",
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.original.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.original.raw.signerKey,
				},
			}},
			expected: &actions{
				secret: &corev1applyconfigurations.SecretApplyConfiguration{
					ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
						Name:      ptr.To(manifests.CustomerSystemAdminSigner("").Name),
						Namespace: ptr.To("crr-ns"),
						Annotations: map[string]string{
							certrotation.CertificateNotAfterAnnotation: "force-regeneration",
						},
					},
				},
			},
		},
		{
			name:         "new signer generated, mark as such",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &certificatesv1alpha1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       certificatesv1alpha1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
					RevocationTimestamp: ptr.To(metav1.NewTime(revocationClock.Now())),
					PreviousSigner:      &corev1.LocalObjectReference{Name: "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw"},
				},
			},
			secrets: []*corev1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      manifests.CustomerSystemAdminSigner("").Name,
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.future.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.future.raw.signerKey,
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw",
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.original.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.original.raw.signerKey,
				},
			}},
			expected: &actions{
				crr: &certificatesv1alpha1applyconfigurations.CertificateRevocationRequestApplyConfiguration{
					ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
						Namespace: ptr.To("crr-ns"),
						Name:      ptr.To("crr-name"),
					},
					Status: &certificatesv1alpha1applyconfigurations.CertificateRevocationRequestStatusApplyConfiguration{
						RevocationTimestamp: ptr.To(metav1.NewTime(revocationClock.Now())),
						PreviousSigner: &corev1.LocalObjectReference{
							Name: "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw",
						},
						Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{{
							Type:               ptr.To(certificatesv1alpha1.RootCertificatesRegeneratedType),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(postRevocationClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To(`Signer certificate crr-ns/customer-system-admin-signer regenerated.`),
						}, {
							Type:               ptr.To(certificatesv1alpha1.NewCertificatesTrustedType),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(postRevocationClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.WaitingForAvailableReason),
							Message:            ptr.To(`New signer certificate crr-ns/customer-system-admin-signer not yet trusted.`),
						}},
					},
				},
			},
		},
		{
			name:            "not yet propagated, nothing to do",
			now:             postRevocationClock.Now,
			crrNamespace:    "crr-ns",
			crrName:         "crr-name",
			expectedRequeue: true, // New cert not yet in total bundle, requeue to wait for TargetConfigController
			crr: &certificatesv1alpha1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       certificatesv1alpha1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
					RevocationTimestamp: ptr.To(metav1.NewTime(revocationClock.Now())),
					PreviousSigner:      &corev1.LocalObjectReference{Name: "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw"},
					Conditions: []metav1.Condition{{
						Type:               certificatesv1alpha1.RootCertificatesRegeneratedType,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
						Reason:             hypershiftv1beta1.AsExpectedReason,
						Message:            `Signer certificate crr-ns/customer-system-admin-signer regenerated.`,
					}},
				},
			},
			secrets: []*corev1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      manifests.CustomerSystemAdminSigner("").Name,
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.future.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.future.raw.signerKey,
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw",
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.original.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.original.raw.signerKey,
				},
			}},
			cms: []*corev1.ConfigMap{{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.TotalKASClientCABundle("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.original.raw.signerCert),
				},
			}},
		},
		{
			name:         "propagated, mark as trusted",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &certificatesv1alpha1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       certificatesv1alpha1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
					RevocationTimestamp: ptr.To(metav1.NewTime(revocationClock.Now())),
					PreviousSigner:      &corev1.LocalObjectReference{Name: "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw"},
					Conditions: []metav1.Condition{{
						Type:               certificatesv1alpha1.RootCertificatesRegeneratedType,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
						Reason:             hypershiftv1beta1.AsExpectedReason,
						Message:            `Signer certificate crr-ns/customer-system-admin-signer regenerated.`,
					}},
				},
			},
			secrets: []*corev1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      manifests.CustomerSystemAdminSigner("").Name,
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.future.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.future.raw.signerKey,
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw",
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.original.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.original.raw.signerKey,
				},
			}},
			cms: []*corev1.ConfigMap{{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.TotalKASClientCABundle("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.original.raw.signerCert) + string(data.future.raw.signerCert),
				},
			}},
			expected: &actions{
				crr: &certificatesv1alpha1applyconfigurations.CertificateRevocationRequestApplyConfiguration{
					ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
						Namespace: ptr.To("crr-ns"),
						Name:      ptr.To("crr-name"),
					},
					Status: &certificatesv1alpha1applyconfigurations.CertificateRevocationRequestStatusApplyConfiguration{
						RevocationTimestamp: ptr.To(metav1.NewTime(revocationClock.Now())),
						PreviousSigner: &corev1.LocalObjectReference{
							Name: "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw",
						},
						Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{{
							Type:               ptr.To(certificatesv1alpha1.NewCertificatesTrustedType),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(postRevocationClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To(`New signer certificate crr-ns/customer-system-admin-signer trusted.`),
						}, {
							Type:               ptr.To(certificatesv1alpha1.RootCertificatesRegeneratedType),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(postRevocationClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To(`Signer certificate crr-ns/customer-system-admin-signer regenerated.`),
						}},
					},
				},
			},
		},
		{
			name:         "leaf certificate not yet regenerated, annotate them",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &certificatesv1alpha1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       certificatesv1alpha1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
					RevocationTimestamp: ptr.To(metav1.NewTime(revocationClock.Now())),
					PreviousSigner:      &corev1.LocalObjectReference{Name: "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw"},
					Conditions: []metav1.Condition{{
						Type:               certificatesv1alpha1.RootCertificatesRegeneratedType,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
						Reason:             hypershiftv1beta1.AsExpectedReason,
						Message:            `Signer certificate crr-ns/customer-system-admin-signer regenerated.`,
					}, {
						Type:               certificatesv1alpha1.NewCertificatesTrustedType,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
						Reason:             hypershiftv1beta1.AsExpectedReason,
						Message:            `New signer certificate crr-ns/customer-system-admin-signer trusted.`,
					}},
				},
			},
			secrets: []*corev1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "crr-ns",
					Name:        manifests.CustomerSystemAdminSigner("").Name,
					Annotations: map[string]string{certrotation.CertificateIssuer: "crr-ns_customer-break-glass-signer@1234"},
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.future.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.future.raw.signerKey,
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw",
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.original.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.original.raw.signerKey,
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "crr-ns",
					Name:        manifests.CustomerSystemAdminClientCertSecret("").Name,
					Annotations: map[string]string{certrotation.CertificateIssuer: "crr-ns_customer-break-glass-signer@0123"},
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.original.raw.signedCert,
					corev1.TLSPrivateKeyKey: data.original.raw.clientKey,
				},
			}},
			cms: []*corev1.ConfigMap{{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.CustomerSystemAdminSignerCA("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.original.raw.signerCert) + string(data.future.raw.signerCert),
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.TotalKASClientCABundle("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.original.raw.signerCert) + string(data.future.raw.signerCert),
				},
			}},
			expected: &actions{
				secret: &corev1applyconfigurations.SecretApplyConfiguration{
					ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
						Name:      ptr.To(manifests.CustomerSystemAdminClientCertSecret("").Name),
						Namespace: ptr.To("crr-ns"),
						Annotations: map[string]string{
							certrotation.CertificateNotAfterAnnotation: "force-regeneration",
						},
					},
				},
			},
		},
		{
			name:         "leaf certificate already regenerated",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &certificatesv1alpha1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       certificatesv1alpha1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
					RevocationTimestamp: ptr.To(metav1.NewTime(revocationClock.Now())),
					PreviousSigner:      &corev1.LocalObjectReference{Name: "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw"},
					Conditions: []metav1.Condition{{
						Type:               certificatesv1alpha1.RootCertificatesRegeneratedType,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
						Reason:             hypershiftv1beta1.AsExpectedReason,
						Message:            `Signer certificate crr-ns/customer-system-admin-signer regenerated.`,
					}, {
						Type:               certificatesv1alpha1.NewCertificatesTrustedType,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
						Reason:             hypershiftv1beta1.AsExpectedReason,
						Message:            `New signer certificate crr-ns/customer-system-admin-signer trusted.`,
					}},
				},
			},
			secrets: []*corev1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "crr-ns",
					Name:        manifests.CustomerSystemAdminSigner("").Name,
					Annotations: map[string]string{certrotation.CertificateIssuer: "crr-ns_customer-break-glass-signer@1234"},
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.future.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.future.raw.signerKey,
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw",
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.original.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.original.raw.signerKey,
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "crr-ns",
					Name:        manifests.CustomerSystemAdminClientCertSecret("").Name,
					Annotations: map[string]string{certrotation.CertificateIssuer: "crr-ns_customer-break-glass-signer@1234"},
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.future.raw.signedCert,
					corev1.TLSPrivateKeyKey: data.future.raw.clientKey,
				},
			}},
			cms: []*corev1.ConfigMap{{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.CustomerSystemAdminSignerCA("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.original.raw.signerCert) + string(data.future.raw.signerCert),
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.TotalKASClientCABundle("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.original.raw.signerCert) + string(data.future.raw.signerCert),
				},
			}},
			expected: &actions{
				cm: &corev1applyconfigurations.ConfigMapApplyConfiguration{
					ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
						Name:      ptr.To(manifests.CustomerSystemAdminSignerCA("").Name),
						Namespace: ptr.To("crr-ns"),
					},
					Data: map[string]string{
						"ca-bundle.crt": string(data.future.raw.signerCert),
					},
				},
			},
		},
		{
			name:         "bundle only has new signers",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &certificatesv1alpha1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       certificatesv1alpha1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
					RevocationTimestamp: ptr.To(metav1.NewTime(revocationClock.Now())),
					PreviousSigner:      &corev1.LocalObjectReference{Name: "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw"},
					Conditions: []metav1.Condition{{
						Type:               certificatesv1alpha1.RootCertificatesRegeneratedType,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
						Reason:             hypershiftv1beta1.AsExpectedReason,
						Message:            `Signer certificate crr-ns/customer-system-admin-signer regenerated.`,
					}, {
						Type:               certificatesv1alpha1.NewCertificatesTrustedType,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
						Reason:             hypershiftv1beta1.AsExpectedReason,
						Message:            `New signer certificate crr-ns/customer-system-admin-signer trusted.`,
					}},
				},
			},
			secrets: []*corev1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "crr-ns",
					Name:        manifests.CustomerSystemAdminSigner("").Name,
					Annotations: map[string]string{certrotation.CertificateIssuer: "crr-ns_customer-break-glass-signer@1234"},
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.future.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.future.raw.signerKey,
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw",
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.original.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.original.raw.signerKey,
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "crr-ns",
					Name:        manifests.CustomerSystemAdminClientCertSecret("").Name,
					Annotations: map[string]string{certrotation.CertificateIssuer: "crr-ns_customer-break-glass-signer@1234"},
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.future.raw.signedCert,
					corev1.TLSPrivateKeyKey: data.future.raw.clientKey,
				},
			}},
			cms: []*corev1.ConfigMap{{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.CustomerSystemAdminSignerCA("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.future.raw.signerCert),
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.TotalKASClientCABundle("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.original.raw.signerCert) + string(data.future.raw.signerCert),
				},
			}},
			expected: &actions{
				crr: &certificatesv1alpha1applyconfigurations.CertificateRevocationRequestApplyConfiguration{
					ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
						Namespace: ptr.To("crr-ns"),
						Name:      ptr.To("crr-name"),
					},
					Status: &certificatesv1alpha1applyconfigurations.CertificateRevocationRequestStatusApplyConfiguration{
						RevocationTimestamp: ptr.To(metav1.NewTime(revocationClock.Now())),
						PreviousSigner: &corev1.LocalObjectReference{
							Name: "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw",
						},
						Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{{
							Type:               ptr.To(certificatesv1alpha1.LeafCertificatesRegeneratedType),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(postRevocationClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To(`All leaf certificates are re-generated.`),
						}, {
							Type:               ptr.To(certificatesv1alpha1.PreviousCertificatesRevokedType),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(postRevocationClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.WaitingForAvailableReason),
							Message:            ptr.To(`Previous signer certificate not yet revoked.`),
						}, {
							Type:               ptr.To(certificatesv1alpha1.RootCertificatesRegeneratedType),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(postRevocationClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To(`Signer certificate crr-ns/customer-system-admin-signer regenerated.`),
						}, {
							Type:               ptr.To(certificatesv1alpha1.NewCertificatesTrustedType),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(postRevocationClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To(`New signer certificate crr-ns/customer-system-admin-signer trusted.`),
						}},
					},
				},
			},
		},
		{
			name:            "validating, previous still valid",
			now:             postRevocationClock.Now,
			crrNamespace:    "crr-ns",
			crrName:         "crr-name",
			expectedRequeue: true, // Old cert still in total bundle, requeue to wait for TargetConfigController
			crr: &certificatesv1alpha1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       certificatesv1alpha1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
					RevocationTimestamp: ptr.To(metav1.NewTime(revocationClock.Now())),
					PreviousSigner:      &corev1.LocalObjectReference{Name: "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw"},
					Conditions: []metav1.Condition{{
						Type:               certificatesv1alpha1.LeafCertificatesRegeneratedType,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
						Reason:             hypershiftv1beta1.AsExpectedReason,
						Message:            `All leaf certificates are re-generated.`,
					}, {
						Type:               certificatesv1alpha1.RootCertificatesRegeneratedType,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
						Reason:             hypershiftv1beta1.AsExpectedReason,
						Message:            `Signer certificate crr-ns/customer-system-admin-signer regenerated.`,
					}, {
						Type:               certificatesv1alpha1.NewCertificatesTrustedType,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
						Reason:             hypershiftv1beta1.AsExpectedReason,
						Message:            `New signer certificate crr-ns/customer-system-admin-signer trusted.`,
					}},
				},
			},
			secrets: []*corev1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "crr-ns",
					Name:        manifests.CustomerSystemAdminSigner("").Name,
					Annotations: map[string]string{certrotation.CertificateIssuer: "crr-ns_customer-break-glass-signer@1234"},
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.future.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.future.raw.signerKey,
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw",
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.original.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.original.raw.signerKey,
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "crr-ns",
					Name:        manifests.CustomerSystemAdminClientCertSecret("").Name,
					Annotations: map[string]string{certrotation.CertificateIssuer: "crr-ns_customer-break-glass-signer@1234"},
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.future.raw.signedCert,
					corev1.TLSPrivateKeyKey: data.future.raw.clientKey,
				},
			}},
			cms: []*corev1.ConfigMap{{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.CustomerSystemAdminSignerCA("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.future.raw.signerCert),
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.TotalKASClientCABundle("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.original.raw.signerCert) + string(data.future.raw.signerCert),
				},
			}},
		},
		{
			name:         "validating, previous invalid",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &certificatesv1alpha1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       certificatesv1alpha1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
					RevocationTimestamp: ptr.To(metav1.NewTime(revocationClock.Now())),
					PreviousSigner:      &corev1.LocalObjectReference{Name: "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw"},
					Conditions: []metav1.Condition{{
						Type:               certificatesv1alpha1.LeafCertificatesRegeneratedType,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
						Reason:             hypershiftv1beta1.AsExpectedReason,
						Message:            `All leaf certificates are re-generated.`,
					}, {
						Type:               certificatesv1alpha1.RootCertificatesRegeneratedType,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
						Reason:             hypershiftv1beta1.AsExpectedReason,
						Message:            `Signer certificate crr-ns/customer-system-admin-signer regenerated.`,
					}, {
						Type:               certificatesv1alpha1.NewCertificatesTrustedType,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
						Reason:             hypershiftv1beta1.AsExpectedReason,
						Message:            `New signer certificate crr-ns/customer-system-admin-signer trusted.`,
					}},
				},
			},
			secrets: []*corev1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "crr-ns",
					Name:        manifests.CustomerSystemAdminSigner("").Name,
					Annotations: map[string]string{certrotation.CertificateIssuer: "crr-ns_customer-break-glass-signer@1234"},
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.future.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.future.raw.signerKey,
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw",
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.original.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.original.raw.signerKey,
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "crr-ns",
					Name:        manifests.CustomerSystemAdminClientCertSecret("").Name,
					Annotations: map[string]string{certrotation.CertificateIssuer: "crr-ns_customer-break-glass-signer@1234"},
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.future.raw.signedCert,
					corev1.TLSPrivateKeyKey: data.future.raw.clientKey,
				},
			}},
			cms: []*corev1.ConfigMap{{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.CustomerSystemAdminSignerCA("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.future.raw.signerCert),
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.TotalKASClientCABundle("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.future.raw.signerCert),
				},
			}},
			expected: &actions{
				crr: &certificatesv1alpha1applyconfigurations.CertificateRevocationRequestApplyConfiguration{
					ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
						Namespace: ptr.To("crr-ns"),
						Name:      ptr.To("crr-name"),
					},
					Status: &certificatesv1alpha1applyconfigurations.CertificateRevocationRequestStatusApplyConfiguration{
						RevocationTimestamp: ptr.To(metav1.NewTime(revocationClock.Now())),
						PreviousSigner: &corev1.LocalObjectReference{
							Name: "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw",
						},
						Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{{
							Type:               ptr.To(certificatesv1alpha1.PreviousCertificatesRevokedType),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(postRevocationClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To(`Previous signer certificate revoked.`),
						}, {
							Type:               ptr.To(certificatesv1alpha1.LeafCertificatesRegeneratedType),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(postRevocationClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To(`All leaf certificates are re-generated.`),
						}, {
							Type:               ptr.To(certificatesv1alpha1.RootCertificatesRegeneratedType),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(postRevocationClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To(`Signer certificate crr-ns/customer-system-admin-signer regenerated.`),
						}, {
							Type:               ptr.To(certificatesv1alpha1.NewCertificatesTrustedType),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(postRevocationClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To(`New signer certificate crr-ns/customer-system-admin-signer trusted.`),
						}},
					},
				},
			},
		},
		{
			name:            "SRE signer: validating, previous still valid (requeue path)",
			now:             postRevocationClock.Now,
			crrNamespace:    "crr-ns",
			crrName:         "crr-name-sre",
			expectedRequeue: true, // Old cert still in total bundle for SRE signer, must requeue
			crr: &certificatesv1alpha1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name-sre"},
				Spec:       certificatesv1alpha1.CertificateRevocationRequestSpec{SignerClass: string(certificates.SREBreakGlassSigner)},
				Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
					RevocationTimestamp: ptr.To(metav1.NewTime(revocationClock.Now())),
					PreviousSigner:      &corev1.LocalObjectReference{Name: "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw"},
					Conditions: []metav1.Condition{{
						Type:               certificatesv1alpha1.LeafCertificatesRegeneratedType,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
						Reason:             hypershiftv1beta1.AsExpectedReason,
						Message:            `All leaf certificates are re-generated.`,
					}, {
						Type:               certificatesv1alpha1.RootCertificatesRegeneratedType,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
						Reason:             hypershiftv1beta1.AsExpectedReason,
						Message:            `Signer certificate crr-ns/sre-system-admin-signer regenerated.`,
					}, {
						Type:               certificatesv1alpha1.NewCertificatesTrustedType,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
						Reason:             hypershiftv1beta1.AsExpectedReason,
						Message:            `New signer certificate crr-ns/sre-system-admin-signer trusted.`,
					}},
				},
			},
			secrets: []*corev1.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "crr-ns",
					Name:        manifests.SRESystemAdminSigner("").Name,
					Annotations: map[string]string{certrotation.CertificateIssuer: "crr-ns_sre-break-glass-signer@1234"},
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.future.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.future.raw.signerKey,
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw",
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.original.raw.signerCert,
					corev1.TLSPrivateKeyKey: data.original.raw.signerKey,
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "crr-ns",
					Name:        manifests.SRESystemAdminClientCertSecret("").Name,
					Annotations: map[string]string{certrotation.CertificateIssuer: "crr-ns_sre-break-glass-signer@1234"},
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.future.raw.signedCert,
					corev1.TLSPrivateKeyKey: data.future.raw.clientKey,
				},
			}},
			cms: []*corev1.ConfigMap{{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.SRESystemAdminSignerCA("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.future.raw.signerCert),
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.TotalKASClientCABundle("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.original.raw.signerCert) + string(data.future.raw.signerCert),
				},
			}},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			c := &CertificateRevocationController{
				getCRR: func(namespace, name string) (*certificatesv1alpha1.CertificateRevocationRequest, error) {
					if namespace == testCase.crr.Namespace && name == testCase.crr.Name {
						return testCase.crr, nil
					}
					return nil, apierrors.NewNotFound(hypershiftv1beta1.SchemeGroupVersion.WithResource("certificaterevovcationrequest").GroupResource(), name)
				},
				getSecret: func(namespace, name string) (*corev1.Secret, error) {
					for _, secret := range testCase.secrets {
						if secret.Namespace == namespace && secret.Name == name {
							return secret, nil
						}
					}
					return nil, apierrors.NewNotFound(corev1.SchemeGroupVersion.WithResource("secrets").GroupResource(), name)
				},
				listSecrets: func(namespace string) ([]*corev1.Secret, error) {
					return testCase.secrets, nil
				},
				getConfigMap: func(namespace, name string) (*corev1.ConfigMap, error) {
					for _, cm := range testCase.cms {
						if namespace == cm.Namespace && name == cm.Name {
							return cm, nil
						}
					}
					return nil, apierrors.NewNotFound(corev1.SchemeGroupVersion.WithResource("configmaps").GroupResource(), name)
				},
				listPods: func(namespace string, selector labels.Selector) ([]*corev1.Pod, error) {
					return nil, nil
				},
				skipKASConnections: true,
			}
			a, requeue, err := c.processCertificateRevocationRequest(t.Context(), testCase.crrNamespace, testCase.crrName, testCase.now)
			if actual, expected := requeue, testCase.expectedRequeue; actual != expected {
				t.Errorf("incorrect requeue: %v != %v", actual, expected)
			}
			if testCase.expectedErr && err == nil {
				t.Errorf("expected an error but got none")
			} else if !testCase.expectedErr && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
			if diff := cmp.Diff(a, testCase.expected, compareActions()...); diff != "" {
				t.Errorf("invalid actions: %v", diff)
			}
		})
	}
}

func compareActions() []cmp.Option {
	return []cmp.Option{
		cmp.AllowUnexported(actions{}),
		cmpopts.IgnoreTypes(
			&eventInfo{}, // these are just informative
			metav1applyconfigurations.TypeMetaApplyConfiguration{}, // these are entirely set by generated code
		),
		cmpopts.IgnoreFields(metav1applyconfigurations.OwnerReferenceApplyConfiguration{}, "UID"),
		cmpopts.IgnoreFields(metav1applyconfigurations.ConditionApplyConfiguration{}, "ObservedGeneration"),
	}
}

func kasPodSpec() corev1.PodSpec {
	return corev1.PodSpec{
		Containers: []corev1.Container{{
			Name: "kube-apiserver",
			Ports: []corev1.ContainerPort{{
				Name:          "client",
				ContainerPort: 6443,
			}},
		}},
	}
}

func readyKASPod(name, ip string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
		Spec:       kasPodSpec(),
		Status: corev1.PodStatus{
			PodIP: ip,
			Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			}},
		},
	}
}

func newTestController(crr *certificatesv1alpha1.CertificateRevocationRequest, secrets []*corev1.Secret, cms []*corev1.ConfigMap) *CertificateRevocationController {
	return &CertificateRevocationController{
		getCRR: func(namespace, name string) (*certificatesv1alpha1.CertificateRevocationRequest, error) {
			return crr, nil
		},
		getSecret: func(namespace, name string) (*corev1.Secret, error) {
			for _, s := range secrets {
				if s.Namespace == namespace && s.Name == name {
					return s, nil
				}
			}
			return nil, apierrors.NewNotFound(corev1.SchemeGroupVersion.WithResource("secrets").GroupResource(), name)
		},
		listSecrets: func(namespace string) ([]*corev1.Secret, error) {
			return secrets, nil
		},
		getConfigMap: func(namespace, name string) (*corev1.ConfigMap, error) {
			for _, cm := range cms {
				if cm.Namespace == namespace && cm.Name == name {
					return cm, nil
				}
			}
			return nil, apierrors.NewNotFound(corev1.SchemeGroupVersion.WithResource("configmaps").GroupResource(), name)
		},
		listPods: func(namespace string, selector labels.Selector) ([]*corev1.Pod, error) {
			return nil, nil
		},
		skipKASConnections: false,
	}
}

func fakeKubeClientWithKASDeployment(replicas int32) kubernetes.Interface {
	return kubefake.NewClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hcpmanifests.KubeAPIServerServiceName,
			Namespace: "test-ns",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(replicas),
		},
	})
}

func TestVerifyCertificateAgainstAllKASPods(t *testing.T) {
	t.Parallel()
	now := time.Now()
	for _, testCase := range []struct {
		name        string
		pods        []*corev1.Pod
		listPodsErr error
		kasReplicas int32
		verifyFunc  func(ctx context.Context, client kubernetes.Interface) (bool, error)

		expectedResult bool
		expectedErr    bool
		expectedCalls  int
	}{
		{
			name: "When all pods are terminating it should requeue",
			pods: []*corev1.Pod{{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "kas-1",
					Namespace:         "test-ns",
					DeletionTimestamp: &metav1.Time{Time: now},
				},
				Spec:   kasPodSpec(),
				Status: corev1.PodStatus{PodIP: "10.0.0.1"},
			}},
			expectedResult: false,
		},
		{
			name:           "When no pods exist it should requeue",
			pods:           []*corev1.Pod{},
			expectedResult: false,
		},
		{
			name: "When a non-terminating pod is not ready it should requeue",
			pods: []*corev1.Pod{{
				ObjectMeta: metav1.ObjectMeta{Name: "kas-1", Namespace: "test-ns"},
				Spec:       kasPodSpec(),
				Status: corev1.PodStatus{
					PodIP: "10.0.0.1",
					Conditions: []corev1.PodCondition{{
						Type:   corev1.PodReady,
						Status: corev1.ConditionFalse,
					}},
				},
			}},
			expectedResult: false,
		},
		{
			name: "When a ready pod has empty PodIP it should requeue",
			pods: []*corev1.Pod{{
				ObjectMeta: metav1.ObjectMeta{Name: "kas-1", Namespace: "test-ns"},
				Spec:       kasPodSpec(),
				Status: corev1.PodStatus{
					PodIP: "",
					Conditions: []corev1.PodCondition{{
						Type:   corev1.PodReady,
						Status: corev1.ConditionTrue,
					}},
				},
			}},
			expectedResult: false,
		},
		{
			name: "When a mix of terminating and not-ready pods exists it should requeue",
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "kas-1",
						Namespace:         "test-ns",
						DeletionTimestamp: &metav1.Time{Time: now},
					},
					Spec: kasPodSpec(),
					Status: corev1.PodStatus{
						PodIP: "10.0.0.1",
						Conditions: []corev1.PodCondition{{
							Type:   corev1.PodReady,
							Status: corev1.ConditionTrue,
						}},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "kas-2", Namespace: "test-ns"},
					Spec:       kasPodSpec(),
					Status: corev1.PodStatus{
						PodIP: "10.0.0.2",
						Conditions: []corev1.PodCondition{{
							Type:   corev1.PodReady,
							Status: corev1.ConditionFalse,
						}},
					},
				},
			},
			expectedResult: false,
		},
		{
			name: "When ready non-terminating pods exist it should call verifyFunc for each",
			pods: []*corev1.Pod{
				readyKASPod("kas-1", "10.0.0.1"),
				readyKASPod("kas-2", "10.0.0.2"),
			},
			kasReplicas: 2,
			verifyFunc: func(_ context.Context, _ kubernetes.Interface) (bool, error) {
				return true, nil
			},
			expectedResult: true,
			expectedCalls:  2,
		},
		{
			name: "When ready pod count does not match expected replicas it should requeue",
			pods: []*corev1.Pod{
				readyKASPod("kas-1", "10.0.0.1"),
			},
			kasReplicas:    3,
			expectedResult: false,
		},
		{
			name: "When one pod's verifyFunc returns false it should return false",
			pods: []*corev1.Pod{
				readyKASPod("kas-1", "10.0.0.1"),
			},
			kasReplicas: 1,
			verifyFunc: func(_ context.Context, _ kubernetes.Interface) (bool, error) {
				return false, nil
			},
			expectedResult: false,
			expectedCalls:  1,
		},
		{
			name: "When second of three pods fails it should return false and stop early",
			pods: []*corev1.Pod{
				readyKASPod("kas-1", "10.0.0.1"),
				readyKASPod("kas-2", "10.0.0.2"),
				readyKASPod("kas-3", "10.0.0.3"),
			},
			kasReplicas: 3,
			verifyFunc: func() func(context.Context, kubernetes.Interface) (bool, error) {
				call := 0
				return func(_ context.Context, _ kubernetes.Interface) (bool, error) {
					call++
					if call == 2 {
						return false, nil // second pod fails
					}
					return true, nil
				}
			}(),
			expectedResult: false,
			expectedCalls:  2, // should stop after second pod fails, never reaching third
		},
		{
			name:           "When listing pods fails it should return an error",
			listPodsErr:    fmt.Errorf("connection refused"),
			expectedResult: false,
			expectedErr:    true,
		},
		{
			name: "When verifyFunc returns an error it should return an error",
			pods: []*corev1.Pod{
				readyKASPod("kas-1", "10.0.0.1"),
			},
			kasReplicas: 1,
			verifyFunc: func(_ context.Context, _ kubernetes.Interface) (bool, error) {
				return false, fmt.Errorf("SSR failed")
			},
			expectedResult: false,
			expectedErr:    true,
			expectedCalls:  1,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			c := &CertificateRevocationController{
				kubeClient: fakeKubeClientWithKASDeployment(testCase.kasReplicas),
				listPods: func(namespace string, selector labels.Selector) ([]*corev1.Pod, error) {
					g.Expect(namespace).To(Equal("test-ns"))
					g.Expect(selector.String()).To(Equal(kasAppLabelSelector.String()))
					if testCase.listPodsErr != nil {
						return nil, testCase.listPodsErr
					}
					return testCase.pods, nil
				},
			}

			// verifyFunc may not be called for all cases (e.g. when pods are terminating or not ready)
			callCount := 0
			verifyFunc := testCase.verifyFunc
			if verifyFunc == nil {
				verifyFunc = func(_ context.Context, _ kubernetes.Interface) (bool, error) {
					t.Fatal("verifyFunc should not have been called")
					return false, nil
				}
			} else {
				original := verifyFunc
				verifyFunc = func(ctx context.Context, client kubernetes.Interface) (bool, error) {
					callCount++
					return original(ctx, client)
				}
			}

			result, err := c.verifyCertificateAgainstAllKASPods(t.Context(), "test-ns", &rest.Config{}, nil, nil, verifyFunc)
			if testCase.expectedErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(result).To(Equal(testCase.expectedResult))
			g.Expect(callCount).To(Equal(testCase.expectedCalls))
		})
	}
}

func TestEnqueueKASPod(t *testing.T) {
	t.Parallel()
	crr := &certificatesv1alpha1.CertificateRevocationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "test-crr", Namespace: "test-ns"},
	}
	listCRRs := func(namespace string) ([]*certificatesv1alpha1.CertificateRevocationRequest, error) {
		return []*certificatesv1alpha1.CertificateRevocationRequest{crr}, nil
	}
	listCRRsErr := func(namespace string) ([]*certificatesv1alpha1.CertificateRevocationRequest, error) {
		return nil, fmt.Errorf("connection refused")
	}

	for _, testCase := range []struct {
		name         string
		obj          runtime.Object
		listCRRs     func(namespace string) ([]*certificatesv1alpha1.CertificateRevocationRequest, error)
		expectedKeys int
	}{
		{
			name: "When pod has KAS label it should enqueue all CRRs",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kas-1",
					Namespace: "test-ns",
					Labels:    map[string]string{"app": "kube-apiserver"},
				},
			},
			listCRRs:     listCRRs,
			expectedKeys: 1,
		},
		{
			name: "When pod does not have KAS label it should return nil",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd-0",
					Namespace: "test-ns",
					Labels:    map[string]string{"app": "etcd"},
				},
			},
			listCRRs:     listCRRs,
			expectedKeys: 0,
		},
		{
			name: "When pod has no labels it should return nil",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unlabeled",
					Namespace: "test-ns",
				},
			},
			listCRRs:     listCRRs,
			expectedKeys: 0,
		},
		{
			name:         "When object is not a pod it should return nil",
			obj:          &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm"}},
			listCRRs:     listCRRs,
			expectedKeys: 0,
		},
		{
			name: "When listing CRRs fails it should return nil",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kas-1",
					Namespace: "test-ns",
					Labels:    map[string]string{"app": "kube-apiserver"},
				},
			},
			listCRRs:     listCRRsErr,
			expectedKeys: 0,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			enqueue := enqueueKASPod(testCase.listCRRs, "test-ns")
			keys := enqueue(testCase.obj)
			g.Expect(keys).To(HaveLen(testCase.expectedKeys))
		})
	}
}

func TestVerifyCertificateTrusted(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name           string
		reactor        func(action k8stesting.Action) (bool, runtime.Object, error)
		expectedResult bool
		expectedErr    bool
	}{
		{
			name: "When SSR succeeds it should return true",
			reactor: func(action k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, nil
			},
			expectedResult: true,
		},
		{
			name: "When SSR returns Unauthorized it should return false",
			reactor: func(action k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, apierrors.NewUnauthorized("not authorized")
			},
			expectedResult: false,
		},
		{
			name: "When SSR returns other error it should return error",
			reactor: func(action k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, fmt.Errorf("connection refused")
			},
			expectedResult: false,
			expectedErr:    true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			fakeClient := kubefake.NewClientset()
			fakeClient.PrependReactor("create", "selfsubjectreviews", testCase.reactor)

			result, err := verifyCertificateTrusted(t.Context(), fakeClient)
			if testCase.expectedErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(result).To(Equal(testCase.expectedResult))
		})
	}
}

func TestVerifyCertificateRevoked(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name           string
		reactor        func(action k8stesting.Action) (bool, runtime.Object, error)
		expectedResult bool
		expectedErr    bool
	}{
		{
			name: "When SSR succeeds it should return false because pod still trusts the cert",
			reactor: func(action k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, nil
			},
			expectedResult: false,
		},
		{
			name: "When SSR returns Unauthorized it should return true because cert is revoked",
			reactor: func(action k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, apierrors.NewUnauthorized("not authorized")
			},
			expectedResult: true,
		},
		{
			name: "When SSR returns other error it should return error",
			reactor: func(action k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, fmt.Errorf("connection refused")
			},
			expectedResult: false,
			expectedErr:    true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			fakeClient := kubefake.NewClientset()
			fakeClient.PrependReactor("create", "selfsubjectreviews", testCase.reactor)

			result, err := verifyCertificateRevoked(t.Context(), fakeClient)
			if testCase.expectedErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(result).To(Equal(testCase.expectedResult))
		})
	}
}

func makeKubeconfig(t *testing.T) []byte {
	t.Helper()
	kubeconfigData, err := clientcmd.Write(clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"default": {
				Server:                "https://kube-apiserver:6443",
				InsecureSkipTLSVerify: true,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"admin": {},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"default": {
				Cluster:  "default",
				AuthInfo: "admin",
			},
		},
		CurrentContext: "default",
	})
	if err != nil {
		t.Fatalf("failed to write kubeconfig: %v", err)
	}
	return kubeconfigData
}

func TestEnsureNewSignerCertificatePropagated_KASVerification(t *testing.T) {
	t.Parallel()
	revocationTime, err := time.Parse(time.RFC3339Nano, "2006-01-02T15:04:05.999999999Z")
	if err != nil {
		t.Fatalf("could not parse time: %v", err)
	}
	postRevocationClock := testingclock.NewFakeClock(revocationTime.Add(revocationOffset + 1*time.Hour))

	data := pki(t, revocationTime)

	newPropagatedCRR := func() *certificatesv1alpha1.CertificateRevocationRequest {
		return &certificatesv1alpha1.CertificateRevocationRequest{
			ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
			Spec:       certificatesv1alpha1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
			Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
				RevocationTimestamp: ptr.To(metav1.NewTime(revocationTime)),
				PreviousSigner:      &corev1.LocalObjectReference{Name: "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw"},
				Conditions: []metav1.Condition{{
					Type:               certificatesv1alpha1.RootCertificatesRegeneratedType,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
					Reason:             hypershiftv1beta1.AsExpectedReason,
					Message:            `Signer certificate crr-ns/customer-system-admin-signer regenerated.`,
				}},
			},
		}
	}

	newPropagatedSecrets := func(kubeconfigData []byte) []*corev1.Secret {
		return []*corev1.Secret{{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "crr-ns",
				Name:      manifests.CustomerSystemAdminSigner("").Name,
			},
			Data: map[string][]byte{
				corev1.TLSCertKey:       data.future.raw.signerCert,
				corev1.TLSPrivateKeyKey: data.future.raw.signerKey,
			},
		}, {
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "crr-ns",
				Name:      "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw",
			},
			Data: map[string][]byte{
				corev1.TLSCertKey:       data.original.raw.signerCert,
				corev1.TLSPrivateKeyKey: data.original.raw.signerKey,
			},
		}, {
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "crr-ns",
				Name:      hcpmanifests.KASServiceKubeconfigSecret("").Name,
			},
			Data: map[string][]byte{
				"kubeconfig": kubeconfigData,
			},
		}}
	}

	newPropagatedCMs := func() []*corev1.ConfigMap {
		return []*corev1.ConfigMap{{
			ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.TotalKASClientCABundle("").Name},
			Data: map[string]string{
				"ca-bundle.crt": string(data.original.raw.signerCert) + string(data.future.raw.signerCert),
			},
		}}
	}

	newPropagatedController := func(crr *certificatesv1alpha1.CertificateRevocationRequest, secrets []*corev1.Secret, cms []*corev1.ConfigMap) *CertificateRevocationController {
		return newTestController(crr, secrets, cms)
	}

	t.Run("When no KAS pods exist it should requeue", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		kubeconfigData := makeKubeconfig(t)
		crr := newPropagatedCRR()
		secrets := newPropagatedSecrets(kubeconfigData)
		cms := newPropagatedCMs()
		c := newPropagatedController(crr, secrets, cms)

		_, requeue, err := c.processCertificateRevocationRequest(t.Context(), "crr-ns", "crr-name", postRevocationClock.Now)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(requeue).To(BeTrue(), "should requeue when no KAS pods exist")
	})

	t.Run("When all KAS pods accept new cert but reject old cert it should requeue", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		kubeconfigData := makeKubeconfig(t)
		crr := newPropagatedCRR()
		secrets := newPropagatedSecrets(kubeconfigData)
		cms := newPropagatedCMs()
		c := newPropagatedController(crr, secrets, cms)

		// Override verification: new cert is trusted, but old cert is rejected (mid-reload)
		c.overrideVerifyCertAgainstKASPods = func(_ context.Context, _ string, _ *rest.Config, certPEM, _ []byte, verifyFunc func(context.Context, kubernetes.Interface) (bool, error)) (bool, error) {
			fakeClient := kubefake.NewClientset()
			if string(certPEM) == string(data.future.raw.signerCert) {
				// new cert: trusted
				fakeClient.PrependReactor("create", "selfsubjectreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, nil
				})
			} else {
				// old cert: rejected (mid-reload)
				fakeClient.PrependReactor("create", "selfsubjectreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, apierrors.NewUnauthorized("not authorized")
				})
			}
			return verifyFunc(context.Background(), fakeClient)
		}

		_, requeue, err := c.processCertificateRevocationRequest(t.Context(), "crr-ns", "crr-name", postRevocationClock.Now)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(requeue).To(BeTrue(), "should requeue when KAS pods reject old cert during mid-reload")
	})

	t.Run("When all KAS pods accept both new and old certs it should mark trusted", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		kubeconfigData := makeKubeconfig(t)
		crr := newPropagatedCRR()
		secrets := newPropagatedSecrets(kubeconfigData)
		cms := newPropagatedCMs()
		c := newPropagatedController(crr, secrets, cms)

		// Override verification: both new and old certs are trusted
		c.overrideVerifyCertAgainstKASPods = func(_ context.Context, _ string, _ *rest.Config, _ []byte, _ []byte, verifyFunc func(context.Context, kubernetes.Interface) (bool, error)) (bool, error) {
			fakeClient := kubefake.NewClientset()
			fakeClient.PrependReactor("create", "selfsubjectreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, nil
			})
			return verifyFunc(context.Background(), fakeClient)
		}

		a, requeue, err := c.processCertificateRevocationRequest(t.Context(), "crr-ns", "crr-name", postRevocationClock.Now)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(requeue).To(BeFalse())
		g.Expect(a).ToNot(BeNil())
		g.Expect(a.crr).ToNot(BeNil())
		// Should have set NewCertificatesTrustedType to True
		var foundTrusted bool
		for _, cond := range a.crr.Status.Conditions {
			if cond.Type != nil && *cond.Type == certificatesv1alpha1.NewCertificatesTrustedType &&
				cond.Status != nil && *cond.Status == metav1.ConditionTrue {
				foundTrusted = true
			}
		}
		g.Expect(foundTrusted).To(BeTrue(), "should have set NewCertificatesTrustedType condition to True")
	})
}

func TestEnsureOldSignerCertificateRevoked_KASVerification(t *testing.T) {
	t.Parallel()
	revocationTime, err := time.Parse(time.RFC3339Nano, "2006-01-02T15:04:05.999999999Z")
	if err != nil {
		t.Fatalf("could not parse time: %v", err)
	}
	postRevocationClock := testingclock.NewFakeClock(revocationTime.Add(revocationOffset + 1*time.Hour))

	data := pki(t, revocationTime)

	newRevokedCRR := func() *certificatesv1alpha1.CertificateRevocationRequest {
		return &certificatesv1alpha1.CertificateRevocationRequest{
			ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
			Spec:       certificatesv1alpha1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
			Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
				RevocationTimestamp: ptr.To(metav1.NewTime(revocationTime)),
				PreviousSigner:      &corev1.LocalObjectReference{Name: "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw"},
				Conditions: []metav1.Condition{{
					Type:               certificatesv1alpha1.LeafCertificatesRegeneratedType,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
					Reason:             hypershiftv1beta1.AsExpectedReason,
					Message:            `All leaf certificates are re-generated.`,
				}, {
					Type:               certificatesv1alpha1.RootCertificatesRegeneratedType,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
					Reason:             hypershiftv1beta1.AsExpectedReason,
					Message:            `Signer certificate crr-ns/customer-system-admin-signer regenerated.`,
				}, {
					Type:               certificatesv1alpha1.NewCertificatesTrustedType,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(postRevocationClock.Now()),
					Reason:             hypershiftv1beta1.AsExpectedReason,
					Message:            `New signer certificate crr-ns/customer-system-admin-signer trusted.`,
				}},
			},
		}
	}

	newRevokedSecrets := func(kubeconfigData []byte) []*corev1.Secret {
		return []*corev1.Secret{{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   "crr-ns",
				Name:        manifests.CustomerSystemAdminSigner("").Name,
				Annotations: map[string]string{certrotation.CertificateIssuer: "crr-ns_customer-break-glass-signer@1234"},
			},
			Data: map[string][]byte{
				corev1.TLSCertKey:       data.future.raw.signerCert,
				corev1.TLSPrivateKeyKey: data.future.raw.signerKey,
			},
		}, {
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "crr-ns",
				Name:      "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw",
			},
			Data: map[string][]byte{
				corev1.TLSCertKey:       data.original.raw.signerCert,
				corev1.TLSPrivateKeyKey: data.original.raw.signerKey,
			},
		}, {
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   "crr-ns",
				Name:        manifests.CustomerSystemAdminClientCertSecret("").Name,
				Annotations: map[string]string{certrotation.CertificateIssuer: "crr-ns_customer-break-glass-signer@1234"},
			},
			Data: map[string][]byte{
				corev1.TLSCertKey:       data.future.raw.signedCert,
				corev1.TLSPrivateKeyKey: data.future.raw.clientKey,
			},
		}, {
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "crr-ns",
				Name:      hcpmanifests.KASServiceKubeconfigSecret("").Name,
			},
			Data: map[string][]byte{
				"kubeconfig": kubeconfigData,
			},
		}}
	}

	newRevokedCMs := func() []*corev1.ConfigMap {
		return []*corev1.ConfigMap{{
			ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.CustomerSystemAdminSignerCA("").Name},
			Data: map[string]string{
				"ca-bundle.crt": string(data.future.raw.signerCert),
			},
		}, {
			ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.TotalKASClientCABundle("").Name},
			Data: map[string]string{
				"ca-bundle.crt": string(data.future.raw.signerCert),
			},
		}}
	}

	newRevokedController := func(crr *certificatesv1alpha1.CertificateRevocationRequest, secrets []*corev1.Secret, cms []*corev1.ConfigMap) *CertificateRevocationController {
		return newTestController(crr, secrets, cms)
	}

	t.Run("When no KAS pods exist it should requeue", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		kubeconfigData := makeKubeconfig(t)
		crr := newRevokedCRR()
		secrets := newRevokedSecrets(kubeconfigData)
		cms := newRevokedCMs()
		c := newRevokedController(crr, secrets, cms)

		_, requeue, err := c.processCertificateRevocationRequest(t.Context(), "crr-ns", "crr-name", postRevocationClock.Now)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(requeue).To(BeTrue(), "should requeue when no KAS pods exist")
	})

	t.Run("When all KAS pods reject old cert but also reject current cert it should requeue", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		kubeconfigData := makeKubeconfig(t)
		crr := newRevokedCRR()
		secrets := newRevokedSecrets(kubeconfigData)
		cms := newRevokedCMs()
		c := newRevokedController(crr, secrets, cms)

		// Override verification: old cert is rejected, but current cert is also rejected (mid-reload)
		c.overrideVerifyCertAgainstKASPods = func(_ context.Context, _ string, _ *rest.Config, _ []byte, _ []byte, verifyFunc func(context.Context, kubernetes.Interface) (bool, error)) (bool, error) {
			fakeClient := kubefake.NewClientset()
			fakeClient.PrependReactor("create", "selfsubjectreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, apierrors.NewUnauthorized("not authorized")
			})
			return verifyFunc(context.Background(), fakeClient)
		}

		_, requeue, err := c.processCertificateRevocationRequest(t.Context(), "crr-ns", "crr-name", postRevocationClock.Now)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(requeue).To(BeTrue(), "should requeue when KAS pods reject both old and current cert during mid-reload")
	})

	t.Run("When all KAS pods reject old cert and accept current cert it should mark revoked", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		kubeconfigData := makeKubeconfig(t)
		crr := newRevokedCRR()
		secrets := newRevokedSecrets(kubeconfigData)
		cms := newRevokedCMs()
		c := newRevokedController(crr, secrets, cms)

		// Override verification: old cert is rejected (revoked), current cert is accepted
		c.overrideVerifyCertAgainstKASPods = func(_ context.Context, _ string, _ *rest.Config, certPEM, _ []byte, verifyFunc func(context.Context, kubernetes.Interface) (bool, error)) (bool, error) {
			fakeClient := kubefake.NewClientset()
			if string(certPEM) == string(data.original.raw.signerCert) {
				// old cert: revoked (unauthorized)
				fakeClient.PrependReactor("create", "selfsubjectreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, apierrors.NewUnauthorized("not authorized")
				})
			} else {
				// current cert: trusted
				fakeClient.PrependReactor("create", "selfsubjectreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, nil
				})
			}
			return verifyFunc(context.Background(), fakeClient)
		}

		a, requeue, err := c.processCertificateRevocationRequest(t.Context(), "crr-ns", "crr-name", postRevocationClock.Now)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(requeue).To(BeFalse())
		g.Expect(a).ToNot(BeNil())
		g.Expect(a.crr).ToNot(BeNil())
		// Should have set PreviousCertificatesRevokedType to True
		var foundRevoked bool
		for _, cond := range a.crr.Status.Conditions {
			if cond.Type != nil && *cond.Type == certificatesv1alpha1.PreviousCertificatesRevokedType &&
				cond.Status != nil && *cond.Status == metav1.ConditionTrue {
				foundRevoked = true
			}
		}
		g.Expect(foundRevoked).To(BeTrue(), "should have set PreviousCertificatesRevokedType condition to True")
	})

	t.Run("When some KAS pods still accept old cert it should requeue", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		kubeconfigData := makeKubeconfig(t)
		crr := newRevokedCRR()
		secrets := newRevokedSecrets(kubeconfigData)
		cms := newRevokedCMs()
		c := newRevokedController(crr, secrets, cms)

		// Override verification: old cert is still accepted (not yet revoked on all pods)
		c.overrideVerifyCertAgainstKASPods = func(_ context.Context, _ string, _ *rest.Config, certPEM, _ []byte, verifyFunc func(context.Context, kubernetes.Interface) (bool, error)) (bool, error) {
			if string(certPEM) == string(data.original.raw.signerCert) {
				// old cert: still accepted by at least one pod
				return false, nil
			}
			t.Fatal("unexpected verification call for current cert — only old cert should be checked in this scenario")
			return false, nil
		}

		_, requeue, err := c.processCertificateRevocationRequest(t.Context(), "crr-ns", "crr-name", postRevocationClock.Now)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(requeue).To(BeTrue(), "should requeue when some KAS pods still accept old cert")
	})

	t.Run("When current signer key is missing it should return an error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		kubeconfigData := makeKubeconfig(t)
		crr := newRevokedCRR()
		secrets := newRevokedSecrets(kubeconfigData)
		for _, s := range secrets {
			if s.Name == manifests.CustomerSystemAdminSigner("").Name {
				delete(s.Data, corev1.TLSPrivateKeyKey)
			}
		}
		cms := newRevokedCMs()
		c := newRevokedController(crr, secrets, cms)

		_, _, err := c.processCertificateRevocationRequest(t.Context(), "crr-ns", "crr-name", postRevocationClock.Now)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("current signer certificate"))
		g.Expect(err.Error()).To(ContainSubstring("had no data for"))
	})

	t.Run("When current signer cert is missing it should return an error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		kubeconfigData := makeKubeconfig(t)
		crr := newRevokedCRR()
		secrets := newRevokedSecrets(kubeconfigData)
		for _, s := range secrets {
			if s.Name == manifests.CustomerSystemAdminSigner("").Name {
				delete(s.Data, corev1.TLSCertKey)
			}
		}
		cms := newRevokedCMs()
		c := newRevokedController(crr, secrets, cms)

		_, _, err := c.processCertificateRevocationRequest(t.Context(), "crr-ns", "crr-name", postRevocationClock.Now)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("found no certificate in secret"))
	})
}
