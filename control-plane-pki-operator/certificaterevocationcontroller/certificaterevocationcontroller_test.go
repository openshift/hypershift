package certificaterevocationcontroller

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftv1beta1applyconfigurations "github.com/openshift/hypershift/client/applyconfiguration/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificates"
	"github.com/openshift/hypershift/control-plane-pki-operator/manifests"
	librarygocrypto "github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1applyconfigurations "k8s.io/client-go/applyconfigurations/core/v1"
	metav1applyconfigurations "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/util/cert"
	testingclock "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"
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
		crr                   *hypershiftv1beta1.CertificateRevocationRequest
		secrets               []*corev1.Secret
		cm                    *corev1.ConfigMap
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
			crr: &hypershiftv1beta1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       hypershiftv1beta1.CertificateRevocationRequestSpec{SignerClass: "invalid"},
			},
			expected: &actions{
				crr: &hypershiftv1beta1applyconfigurations.CertificateRevocationRequestApplyConfiguration{
					ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
						Namespace: ptr.To("crr-ns"),
						Name:      ptr.To("crr-name"),
					},
					Status: &hypershiftv1beta1applyconfigurations.CertificateRevocationRequestStatusApplyConfiguration{
						Phase: ptr.To(hypershiftv1beta1.CertificateRevocationRequestPhaseUnknown),
						Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{{
							Type:               ptr.To(hypershiftv1beta1.SignerClassValidType),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(revocationClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.SignerClassUnknownReason),
							Message:            ptr.To(`Signer class "invalid" unknown.`),
						}},
					},
				},
			},
		},
		{
			name:         "unknown transitions to regenerating with a timestamp",
			now:          revocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &hypershiftv1beta1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       hypershiftv1beta1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status:     hypershiftv1beta1.CertificateRevocationRequestStatus{Phase: hypershiftv1beta1.CertificateRevocationRequestPhaseUnknown},
			},
			expected: &actions{
				crr: &hypershiftv1beta1applyconfigurations.CertificateRevocationRequestApplyConfiguration{
					ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
						Namespace: ptr.To("crr-ns"),
						Name:      ptr.To("crr-name"),
					},
					Status: &hypershiftv1beta1applyconfigurations.CertificateRevocationRequestStatusApplyConfiguration{
						Phase:               ptr.To(hypershiftv1beta1.CertificateRevocationRequestPhaseRegenerating),
						RevocationTimestamp: ptr.To(metav1.NewTime(revocationClock.Now())),
						Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{{
							Type:               ptr.To(hypershiftv1beta1.SignerClassValidType),
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
			name:         "regenerating first copies the current leaf",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &hypershiftv1beta1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       hypershiftv1beta1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: hypershiftv1beta1.CertificateRevocationRequestStatus{
					Phase:               hypershiftv1beta1.CertificateRevocationRequestPhaseRegenerating,
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
			name:         "malformed leaf copy heals",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &hypershiftv1beta1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       hypershiftv1beta1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: hypershiftv1beta1.CertificateRevocationRequestStatus{
					Phase:               hypershiftv1beta1.CertificateRevocationRequestPhaseRegenerating,
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
					corev1.TLSCertKey:       data.original.raw.clientCert,
					corev1.TLSPrivateKeyKey: data.original.raw.clientKey,
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
			name:         "spec updated to contain copied signer",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &hypershiftv1beta1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       hypershiftv1beta1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: hypershiftv1beta1.CertificateRevocationRequestStatus{
					Phase:               hypershiftv1beta1.CertificateRevocationRequestPhaseRegenerating,
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
				crr: &hypershiftv1beta1applyconfigurations.CertificateRevocationRequestApplyConfiguration{
					ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
						Namespace: ptr.To("crr-ns"),
						Name:      ptr.To("crr-name"),
					},
					Status: &hypershiftv1beta1applyconfigurations.CertificateRevocationRequestStatusApplyConfiguration{
						PreviousSigner: &corev1.LocalObjectReference{
							Name: "1pfcydcz358pa1glirkmc72sdkf5zw21uam4jbnj03pw",
						},
					},
				},
			},
		},
		{
			name:         "copies finished means we annotate for regeneration",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &hypershiftv1beta1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       hypershiftv1beta1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: hypershiftv1beta1.CertificateRevocationRequestStatus{
					Phase:               hypershiftv1beta1.CertificateRevocationRequestPhaseRegenerating,
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
			name:         "new signer generated, move on to propagating",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &hypershiftv1beta1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       hypershiftv1beta1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: hypershiftv1beta1.CertificateRevocationRequestStatus{
					Phase:               hypershiftv1beta1.CertificateRevocationRequestPhaseRegenerating,
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
				crr: &hypershiftv1beta1applyconfigurations.CertificateRevocationRequestApplyConfiguration{
					ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
						Namespace: ptr.To("crr-ns"),
						Name:      ptr.To("crr-name"),
					},
					Status: &hypershiftv1beta1applyconfigurations.CertificateRevocationRequestStatusApplyConfiguration{
						Phase: ptr.To(hypershiftv1beta1.CertificateRevocationRequestPhasePropagating),
					},
				},
			},
		},
		{
			name:         "not yet propagated, nothing to do",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &hypershiftv1beta1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       hypershiftv1beta1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: hypershiftv1beta1.CertificateRevocationRequestStatus{
					Phase:               hypershiftv1beta1.CertificateRevocationRequestPhasePropagating,
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
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.TotalKASClientCABundle("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.original.raw.signerCert),
				},
			},
		},
		{
			name:         "propagated, move on to revoking",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &hypershiftv1beta1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       hypershiftv1beta1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: hypershiftv1beta1.CertificateRevocationRequestStatus{
					Phase:               hypershiftv1beta1.CertificateRevocationRequestPhasePropagating,
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
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.TotalKASClientCABundle("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.original.raw.signerCert) + string(data.future.raw.signerCert),
				},
			},
			expected: &actions{
				crr: &hypershiftv1beta1applyconfigurations.CertificateRevocationRequestApplyConfiguration{
					ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
						Namespace: ptr.To("crr-ns"),
						Name:      ptr.To("crr-name"),
					},
					Status: &hypershiftv1beta1applyconfigurations.CertificateRevocationRequestStatusApplyConfiguration{
						Phase: ptr.To(hypershiftv1beta1.CertificateRevocationRequestPhaseRevoking),
					},
				},
			},
		},
		{
			name:         "revoking, leaf certificate not yet regenerated",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &hypershiftv1beta1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       hypershiftv1beta1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: hypershiftv1beta1.CertificateRevocationRequestStatus{
					Phase:               hypershiftv1beta1.CertificateRevocationRequestPhaseRevoking,
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
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      manifests.CustomerSystemAdminClientCertSecret("").Name,
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.original.raw.signedCert,
					corev1.TLSPrivateKeyKey: data.original.raw.clientKey,
				},
			}},
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.CustomerSystemAdminSignerCA("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.original.raw.signerCert) + string(data.future.raw.signerCert),
				},
			},
			expected: &actions{
				crr: &hypershiftv1beta1applyconfigurations.CertificateRevocationRequestApplyConfiguration{
					ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
						Namespace: ptr.To("crr-ns"),
						Name:      ptr.To("crr-name"),
					},
					Status: &hypershiftv1beta1applyconfigurations.CertificateRevocationRequestStatusApplyConfiguration{
						Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{{
							Type:               ptr.To(hypershiftv1beta1.LeafCertificatesRegeneratedType),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(postRevocationClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.LeafCertificatesStaleReason),
							Message:            ptr.To(`Revocation would lose trust for leaf certificates: crr-ns/customer-system-admin-client-cert-key.`),
						}},
					},
				},
			},
		},
		{
			name:         "revoking, leaf certificate already regenerated",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &hypershiftv1beta1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       hypershiftv1beta1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: hypershiftv1beta1.CertificateRevocationRequestStatus{
					Phase:               hypershiftv1beta1.CertificateRevocationRequestPhaseRevoking,
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
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      manifests.CustomerSystemAdminClientCertSecret("").Name,
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.future.raw.signedCert,
					corev1.TLSPrivateKeyKey: data.future.raw.clientKey,
				},
			}},
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.CustomerSystemAdminSignerCA("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.original.raw.signerCert) + string(data.future.raw.signerCert),
				},
			},
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
			name:         "revoking, bundle only has new signers",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &hypershiftv1beta1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       hypershiftv1beta1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: hypershiftv1beta1.CertificateRevocationRequestStatus{
					Phase:               hypershiftv1beta1.CertificateRevocationRequestPhaseRevoking,
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
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      manifests.CustomerSystemAdminClientCertSecret("").Name,
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.future.raw.signedCert,
					corev1.TLSPrivateKeyKey: data.future.raw.clientKey,
				},
			}},
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.CustomerSystemAdminSignerCA("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.future.raw.signerCert),
				},
			},
			expected: &actions{
				crr: &hypershiftv1beta1applyconfigurations.CertificateRevocationRequestApplyConfiguration{
					ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
						Namespace: ptr.To("crr-ns"),
						Name:      ptr.To("crr-name"),
					},
					Status: &hypershiftv1beta1applyconfigurations.CertificateRevocationRequestStatusApplyConfiguration{
						Phase: ptr.To(hypershiftv1beta1.CertificateRevocationRequestPhaseValidating),
						Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{{
							Type:               ptr.To(hypershiftv1beta1.LeafCertificatesRegeneratedType),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(postRevocationClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To(`All leaf certificates are re-generated.`),
						}},
					},
				},
			},
		},
		{
			name:         "validating, previous still valid",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &hypershiftv1beta1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       hypershiftv1beta1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: hypershiftv1beta1.CertificateRevocationRequestStatus{
					Phase:               hypershiftv1beta1.CertificateRevocationRequestPhaseValidating,
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
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      manifests.CustomerSystemAdminClientCertSecret("").Name,
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.future.raw.signedCert,
					corev1.TLSPrivateKeyKey: data.future.raw.clientKey,
				},
			}},
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.TotalKASClientCABundle("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.original.raw.signerCert) + string(data.future.raw.signerCert),
				},
			},
		},
		{
			name:         "validating, previous invalid",
			now:          postRevocationClock.Now,
			crrNamespace: "crr-ns",
			crrName:      "crr-name",
			crr: &hypershiftv1beta1.CertificateRevocationRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: "crr-name"},
				Spec:       hypershiftv1beta1.CertificateRevocationRequestSpec{SignerClass: string(certificates.CustomerBreakGlassSigner)},
				Status: hypershiftv1beta1.CertificateRevocationRequestStatus{
					Phase:               hypershiftv1beta1.CertificateRevocationRequestPhaseValidating,
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
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "crr-ns",
					Name:      manifests.CustomerSystemAdminClientCertSecret("").Name,
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       data.future.raw.signedCert,
					corev1.TLSPrivateKeyKey: data.future.raw.clientKey,
				},
			}},
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: "crr-ns", Name: manifests.TotalKASClientCABundle("").Name},
				Data: map[string]string{
					"ca-bundle.crt": string(data.future.raw.signerCert),
				},
			},
			expected: &actions{
				crr: &hypershiftv1beta1applyconfigurations.CertificateRevocationRequestApplyConfiguration{
					ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
						Namespace: ptr.To("crr-ns"),
						Name:      ptr.To("crr-name"),
					},
					Status: &hypershiftv1beta1applyconfigurations.CertificateRevocationRequestStatusApplyConfiguration{
						Phase: ptr.To(hypershiftv1beta1.CertificateRevocationRequestPhaseComplete),
					},
				},
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			c := &CertificateRevocationController{
				getCRR: func(namespace, name string) (*hypershiftv1beta1.CertificateRevocationRequest, error) {
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
					if namespace == testCase.cm.Namespace && name == testCase.cm.Name {
						return testCase.cm, nil
					}
					return nil, apierrors.NewNotFound(corev1.SchemeGroupVersion.WithResource("configmaps").GroupResource(), name)
				},
			}
			a, requeue, err := c.processCertificateRevocationRequest(testCase.crrNamespace, testCase.crrName, testCase.now)
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
	}
}
