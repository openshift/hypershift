package certificatesigningcontroller

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/pem"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificates"

	librarygocrypto "github.com/openshift/library-go/pkg/crypto"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	certificatesv1applyconfigurations "k8s.io/client-go/applyconfigurations/certificates/v1"
	metav1applyconfigurations "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/util/certificate/csr"
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

func privateKey(t *testing.T) crypto.PrivateKey {
	if os.Getenv("REGENERATE_PKI") != "" {
		t.Log("$REGENERATE_PKI set, generating a new private key")
		pk, err := ecdsa.GenerateKey(elliptic.P256(), insecureRand)
		if err != nil {
			t.Fatalf("failed to generate private key: %v", err)
		}

		der, err := x509.MarshalECPrivateKey(pk)
		if err != nil {
			t.Fatalf("failed to marshal private key: %v", err)
		}
		pkb := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})

		if err := os.WriteFile(filepath.Join("testdata", "client.key"), pkb, 0666); err != nil {
			t.Fatalf("failed to write re-generated private key: %v", err)
		}

		return pk
	}

	t.Log("loading private key from disk, use $REGENERATE_PKI to generate a new one")
	pemb, err := testdata.ReadFile(filepath.Join("testdata", "client.key"))
	if err != nil {
		t.Fatalf("failed to read private key: %v", err)
	}
	der, _ := pem.Decode(pemb)
	key, err := x509.ParseECPrivateKey(der.Bytes)
	if err != nil {
		t.Fatalf("failed to parse private key: %v", err)
	}
	return key
}

func certificateAuthority(t *testing.T) *librarygocrypto.CA {
	keyPem, certPem := certificateAuthorityRaw(t)

	ca, err := librarygocrypto.GetCAFromBytes(certPem, keyPem)
	if err != nil {
		t.Fatalf("error parsing CA cert and key: %v", err)
	}
	return ca
}

func certificateAuthorityRaw(t *testing.T) ([]byte, []byte) {
	if os.Getenv("REGENERATE_PKI") != "" {
		t.Log("$REGENERATE_PKI set, generating a new cert/key pair")
		cfg, err := librarygocrypto.MakeSelfSignedCAConfigForDuration("test-signer", time.Hour*24*365*100)
		if err != nil {
			t.Fatalf("could not generate self-signed CA: %v", err)
		}

		certb, keyb, err := cfg.GetPEMBytes()
		if err != nil {
			t.Fatalf("failed to marshal CA cert and key: %v", err)
		}

		if err := os.WriteFile(filepath.Join("testdata", "tls.key"), keyb, 0666); err != nil {
			t.Fatalf("failed to write re-generated private key: %v", err)
		}

		if err := os.WriteFile(filepath.Join("testdata", "tls.crt"), certb, 0666); err != nil {
			t.Fatalf("failed to write re-generated certificate: %v", err)
		}

		return keyb, certb
	}

	t.Log("loading certificate/key pair from disk, use $REGENERATE_PKI to generate new ones")
	keyPem, err := testdata.ReadFile(filepath.Join("testdata", "tls.key"))
	if err != nil {
		t.Fatalf("failed to read private key: %v", err)
	}
	certPem, err := testdata.ReadFile(filepath.Join("testdata", "tls.crt"))
	if err != nil {
		t.Fatalf("failed to read certificate: %v", err)
	}
	return keyPem, certPem
}

func TestCertificateSigningController_processCertificateSigningRequest(t *testing.T) {
	hcp := &hypershiftv1beta1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "hc-namespace-hc-name",
			Name:      "hcp-name",
		},
	}
	theTime, err := time.Parse(time.RFC3339Nano, "2006-01-02T15:04:05.999999999Z")
	if err != nil {
		t.Fatalf("could not parse time: %v", err)
	}
	fakeClock := testingclock.NewFakeClock(theTime)
	testCSRSpec := makeTestCSRSpec(t)
	cases := []struct {
		description string
		name        string
		signerName  string
		validator   certificates.ValidatorFunc
		getCSR      func(name string) (*certificatesv1.CertificateSigningRequest, error)

		expectedCfg           *certificatesv1applyconfigurations.CertificateSigningRequestApplyConfiguration
		expectedValidationErr bool
		expectedErr           bool
	}{
		{
			description: "csr missing",
			name:        "test-csr",
			getCSR: func(name string) (*certificatesv1.CertificateSigningRequest, error) {
				return nil, apierrors.NewNotFound(certificatesv1.SchemeGroupVersion.WithResource("certificatesigningrequests").GroupResource(), name)
			},
			expectedErr: false, // nothing to do, no need to error & requeue
		},
		{
			description: "csr not approved",
			name:        "test-csr",
			getCSR: func(name string) (*certificatesv1.CertificateSigningRequest, error) {
				if name != "test-csr" {
					return nil, apierrors.NewNotFound(certificatesv1.SchemeGroupVersion.WithResource("certificatesigningrequests").GroupResource(), name)
				}
				return &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
				}, nil
			},
		},
		{
			description: "csr failed",
			name:        "test-csr",
			getCSR: func(name string) (*certificatesv1.CertificateSigningRequest, error) {
				if name != "test-csr" {
					return nil, apierrors.NewNotFound(certificatesv1.SchemeGroupVersion.WithResource("certificatesigningrequests").GroupResource(), name)
				}
				return &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
					Status: certificatesv1.CertificateSigningRequestStatus{
						Conditions: []certificatesv1.CertificateSigningRequestCondition{{
							Type:   certificatesv1.CertificateFailed,
							Status: corev1.ConditionTrue,
						}},
					},
				}, nil
			},
		},
		{
			description: "csr fulfilled",
			name:        "test-csr",
			getCSR: func(name string) (*certificatesv1.CertificateSigningRequest, error) {
				if name != "test-csr" {
					return nil, apierrors.NewNotFound(certificatesv1.SchemeGroupVersion.WithResource("certificatesigningrequests").GroupResource(), name)
				}
				return &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
					Status: certificatesv1.CertificateSigningRequestStatus{
						Conditions: []certificatesv1.CertificateSigningRequestCondition{{
							Type:   certificatesv1.CertificateApproved,
							Status: corev1.ConditionTrue,
						}},
						Certificate: []byte(`already done!`),
					},
				}, nil
			},
		},
		{
			description: "invalid request encoding",
			name:        "test-csr",
			signerName:  certificates.SignerNameForHCP(hcp, certificates.CustomerBreakGlassSigner),
			getCSR: func(name string) (*certificatesv1.CertificateSigningRequest, error) {
				if name != "test-csr" {
					return nil, apierrors.NewNotFound(certificatesv1.SchemeGroupVersion.WithResource("certificatesigningrequests").GroupResource(), name)
				}
				return &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: certificates.SignerNameForHCP(hcp, certificates.CustomerBreakGlassSigner),
						Request:    []byte(`gobbly-gook`),
					},
					Status: certificatesv1.CertificateSigningRequestStatus{
						Conditions: []certificatesv1.CertificateSigningRequestCondition{{
							Type:   certificatesv1.CertificateApproved,
							Status: corev1.ConditionTrue,
						}},
					},
				}, nil
			},
			expectedErr: true,
		},
		{
			description: "invalid csr",
			name:        "test-csr",
			getCSR: func(name string) (*certificatesv1.CertificateSigningRequest, error) {
				if name != "test-csr" {
					return nil, apierrors.NewNotFound(certificatesv1.SchemeGroupVersion.WithResource("certificatesigningrequests").GroupResource(), name)
				}
				return &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
					Spec: testCSRSpec(csrBuilder{
						cn:         certificates.CommonNamePrefix(certificates.CustomerBreakGlassSigner) + "test-client",
						org:        []string{"anything"},
						signerName: "hypershift.openshift.io/hc-namespace-hc-name.customer-break-glass",
						usages:     []certificatesv1.KeyUsage{certificatesv1.UsageContentCommitment},
					}),
					Status: certificatesv1.CertificateSigningRequestStatus{
						Conditions: []certificatesv1.CertificateSigningRequestCondition{{
							Type:   certificatesv1.CertificateApproved,
							Status: corev1.ConditionTrue,
						}},
					},
				}, nil
			},
			signerName: certificates.SignerNameForHCP(hcp, certificates.CustomerBreakGlassSigner),
			validator: func(csr *certificatesv1.CertificateSigningRequest, x509cr *x509.CertificateRequest) error {
				return errors.New("invalid")
			},
			expectedCfg: &certificatesv1applyconfigurations.CertificateSigningRequestApplyConfiguration{
				Status: &certificatesv1applyconfigurations.CertificateSigningRequestStatusApplyConfiguration{
					Conditions: []certificatesv1applyconfigurations.CertificateSigningRequestConditionApplyConfiguration{
						{
							Type:    ptr.To(certificatesv1.CertificateFailed),
							Status:  ptr.To(corev1.ConditionTrue),
							Reason:  ptr.To("SignerValidationFailure"),
							Message: ptr.To("invalid"),
						},
					},
				},
			},
			expectedValidationErr: true,
		},
		{
			description: "valid csr",
			name:        "test-csr",
			getCSR: func(name string) (*certificatesv1.CertificateSigningRequest, error) {
				if name != "test-csr" {
					return nil, apierrors.NewNotFound(certificatesv1.SchemeGroupVersion.WithResource("certificatesigningrequests").GroupResource(), name)
				}
				return &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
					Spec: testCSRSpec(csrBuilder{
						cn:         certificates.CommonNamePrefix(certificates.CustomerBreakGlassSigner) + "test-client",
						org:        []string{"system:masters"},
						signerName: "hypershift.openshift.io/hc-namespace-hc-name.customer-break-glass",
						usages:     []certificatesv1.KeyUsage{certificatesv1.UsageClientAuth},
					}),
					Status: certificatesv1.CertificateSigningRequestStatus{
						Conditions: []certificatesv1.CertificateSigningRequestCondition{{
							Type:   certificatesv1.CertificateApproved,
							Status: corev1.ConditionTrue,
						}},
					},
				}, nil
			},
			signerName: certificates.SignerNameForHCP(hcp, certificates.CustomerBreakGlassSigner),
			validator:  certificates.Validator(hcp, certificates.CustomerBreakGlassSigner),
			expectedCfg: &certificatesv1applyconfigurations.CertificateSigningRequestApplyConfiguration{
				Status: &certificatesv1applyconfigurations.CertificateSigningRequestStatusApplyConfiguration{
					Certificate: []uint8(`testdata`),
				},
			},
		},
		{
			description: "valid sre csr",
			name:        "test-csr",
			getCSR: func(name string) (*certificatesv1.CertificateSigningRequest, error) {
				if name != "test-csr" {
					return nil, apierrors.NewNotFound(certificatesv1.SchemeGroupVersion.WithResource("certificatesigningrequests").GroupResource(), name)
				}
				return &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
					Spec: testCSRSpec(csrBuilder{
						cn:         certificates.CommonNamePrefix(certificates.SREBreakGlassSigner) + "test-client",
						org:        []string{"system:masters"},
						signerName: "hypershift.openshift.io/hc-namespace-hc-name.sre-break-glass",
						usages:     []certificatesv1.KeyUsage{certificatesv1.UsageClientAuth},
					}),
					Status: certificatesv1.CertificateSigningRequestStatus{
						Conditions: []certificatesv1.CertificateSigningRequestCondition{{
							Type:   certificatesv1.CertificateApproved,
							Status: corev1.ConditionTrue,
						}},
					},
				}, nil
			},
			signerName: certificates.SignerNameForHCP(hcp, certificates.SREBreakGlassSigner),
			validator:  certificates.Validator(hcp, certificates.SREBreakGlassSigner),
			expectedCfg: &certificatesv1applyconfigurations.CertificateSigningRequestApplyConfiguration{
				Status: &certificatesv1applyconfigurations.CertificateSigningRequestStatusApplyConfiguration{
					Certificate: []uint8(`testdata`),
				},
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.description, func(t *testing.T) {
			ca := certificateAuthority(t)

			c := CertificateSigningController{
				validator:  testCase.validator,
				signerName: testCase.signerName,
				getCSR:     testCase.getCSR,
				getCurrentCABundleContent: func(ctx context.Context) (*librarygocrypto.CA, error) {
					return ca, nil
				},
				certTTL: 12 * time.Hour,
			}

			cfg, _, validationErr, err := c.processCertificateSigningRequest(context.Background(), testCase.name, fakeClock.Now)
			if testCase.expectedErr && err == nil {
				t.Errorf("expected an error but got none")
			} else if !testCase.expectedErr && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
			if testCase.expectedValidationErr && validationErr == nil {
				t.Errorf("expected a validation error but got none")
			} else if !testCase.expectedValidationErr && validationErr != nil {
				t.Errorf("expected no validation error but got: %v", validationErr)
			}

			// signing the certificate necessarily uses cryptographic randomness, so we can't know
			// what the output will be a priori
			if testCase.expectedCfg != nil && testCase.expectedCfg.Status != nil && testCase.expectedCfg.Status.Certificate != nil &&
				cfg != nil && cfg.Status != nil && cfg.Status.Certificate != nil {
				testCase.expectedCfg.Status.Certificate = cfg.Status.Certificate
			}

			if d := cmp.Diff(testCase.expectedCfg, cfg,
				cmpopts.IgnoreTypes(
					metav1applyconfigurations.TypeMetaApplyConfiguration{},
					&metav1applyconfigurations.ObjectMetaApplyConfiguration{},
				),
				cmpopts.IgnoreFields(
					certificatesv1applyconfigurations.CertificateSigningRequestConditionApplyConfiguration{},
					"LastUpdateTime", "LastTransitionTime",
				),
			); d != "" {
				t.Errorf("got invalid CSR cfg: %v", d)
			}
		})
	}
}

// noncryptographic for faster testing
// DO NOT COPY THIS CODE
var insecureRand = rand.New(rand.NewSource(0))

type csrBuilder struct {
	cn         string
	dnsNames   []string
	org        []string
	signerName string
	usages     []certificatesv1.KeyUsage
}

func makeTestCSRSpec(t *testing.T) func(b csrBuilder) certificatesv1.CertificateSigningRequestSpec {
	return func(b csrBuilder) certificatesv1.CertificateSigningRequestSpec {
		pk := privateKey(t)
		csrb, err := x509.CreateCertificateRequest(insecureRand, &x509.CertificateRequest{
			Subject: pkix.Name{
				CommonName:   b.cn,
				Organization: b.org,
			},
			DNSNames: b.dnsNames,
		}, pk)
		if err != nil {
			t.Fatalf("failed to generate certificate request: %v", err)
		}
		spec := certificatesv1.CertificateSigningRequestSpec{
			Request: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrb}),
			Usages:  b.usages,
		}
		if b.signerName != "" {
			spec.SignerName = b.signerName
		}
		return spec
	}
}

func TestSign(t *testing.T) {
	fakeClock := testingclock.FakeClock{}
	ca := certificateAuthority(t)
	pk := privateKey(t)
	csrb, err := x509.CreateCertificateRequest(insecureRand, &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   "test-cn",
			Organization: []string{"test-org"},
		},
		DNSNames: []string{"example.com"},
	}, pk)
	if err != nil {
		t.Fatalf("failed to generate certificate request: %v", err)
	}

	x509cr, err := certificates.ParseCSR(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrb}))
	if err != nil {
		t.Fatalf("failed to parse CSR: %v", err)
	}

	certData, err := sign(ca, x509cr, []certificatesv1.KeyUsage{
		certificatesv1.UsageSigning,
		certificatesv1.UsageKeyEncipherment,
		certificatesv1.UsageServerAuth,
		certificatesv1.UsageClientAuth,
	},
		1*time.Hour,
		// requesting a duration that is greater than TTL is ignored
		csr.DurationToExpirationSeconds(3*time.Hour),
		fakeClock.Now,
	)
	if err != nil {
		t.Fatalf("failed to sign CSR: %v", err)
	}
	if len(certData) == 0 {
		t.Fatalf("expected a certificate after signing")
	}

	certs, err := librarygocrypto.CertsFromPEM(certData)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("expected one certificate")
	}

	want := x509.Certificate{
		Version: 3,
		Subject: pkix.Name{
			CommonName:   "test-cn",
			Organization: []string{"test-org"},
		},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		NotBefore:             fakeClock.Now(),
		NotAfter:              fakeClock.Now().Add(1 * time.Hour),
		PublicKeyAlgorithm:    x509.ECDSA,
		SignatureAlgorithm:    x509.SHA256WithRSA,
		MaxPathLen:            -1,
	}

	if d := cmp.Diff(*certs[0], want, diff.IgnoreUnset()); d != "" {
		t.Errorf("unexpected diff: %v", d)
	}
}

func TestDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		certTTL           time.Duration
		expirationSeconds *int32
		want              time.Duration
	}{
		{
			name:              "can request shorter duration than TTL",
			certTTL:           time.Hour,
			expirationSeconds: csr.DurationToExpirationSeconds(30 * time.Minute),
			want:              30 * time.Minute,
		},
		{
			name:              "cannot request longer duration than TTL",
			certTTL:           time.Hour,
			expirationSeconds: csr.DurationToExpirationSeconds(3 * time.Hour),
			want:              time.Hour,
		},
		{
			name:              "cannot request negative duration",
			certTTL:           time.Hour,
			expirationSeconds: csr.DurationToExpirationSeconds(-time.Minute),
			want:              10 * time.Minute,
		},
		{
			name:              "cannot request duration less than 10 mins",
			certTTL:           time.Hour,
			expirationSeconds: csr.DurationToExpirationSeconds(10*time.Minute - time.Second),
			want:              10 * time.Minute,
		},
		{
			name:              "can request duration of exactly 10 mins",
			certTTL:           time.Hour,
			expirationSeconds: csr.DurationToExpirationSeconds(10 * time.Minute),
			want:              10 * time.Minute,
		},
		{
			name:              "can request duration equal to the default",
			certTTL:           time.Hour,
			expirationSeconds: csr.DurationToExpirationSeconds(time.Hour),
			want:              time.Hour,
		},
		{
			name:              "can choose not to request a duration to get the default",
			certTTL:           time.Hour,
			expirationSeconds: nil,
			want:              time.Hour,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := duration(testCase.certTTL, testCase.expirationSeconds); got != testCase.want {
				t.Errorf("duration() = %v, want %v", got, testCase.want)
			}
		})
	}
}
