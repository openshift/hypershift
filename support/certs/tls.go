package certs

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math"
	"math/big"
	"net"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/pkg/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

const (
	ValidityOneDay   = 24 * time.Hour
	ValidityOneYear  = 365 * ValidityOneDay
	ValidityTenYears = 10 * ValidityOneYear
)

// CertCfg contains all needed fields to configure a new certificate
type CertCfg struct {
	DNSNames     []string
	ExtKeyUsages []x509.ExtKeyUsage
	IPAddresses  []net.IP
	KeyUsages    x509.KeyUsage
	Subject      pkix.Name
	Validity     time.Duration
	IsCA         bool
}

// GenerateSelfSignedCertificate generates a key/cert pair defined by CertCfg.
func GenerateSelfSignedCertificate(cfg *CertCfg) (*ecdsa.PrivateKey, *x509.Certificate, error) {
	key, err := PrivateKey()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to generate private key")
	}

	crt, err := SelfSignedCertificate(cfg, key)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to create self-signed certificate")
	}
	return key, crt, nil
}

// GenerateSignedCertificate generate a key and cert defined by CertCfg and signed by CA.
func GenerateSignedCertificate(caKey crypto.Signer, caCert *x509.Certificate,
	cfg *CertCfg) (*ecdsa.PrivateKey, *x509.Certificate, error) {

	// create a private key
	key, err := PrivateKey()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to generate private key")
	}

	// create a CSR
	csrTmpl := x509.CertificateRequest{Subject: cfg.Subject, DNSNames: cfg.DNSNames, IPAddresses: cfg.IPAddresses}
	csrBytes, err := x509.CreateCertificateRequest(rand.Reader, &csrTmpl, key)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to create certificate request")
	}
	csr, err := x509.ParseCertificateRequest(csrBytes)
	if err != nil {
		return nil, nil, errors.Wrap(err, "error parsing x509 certificate request")
	}

	// create a cert
	cert, err := signedCertificate(cfg, csr, key, caCert, caKey)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to create a signed certificate")
	}
	return key, cert, nil
}

// PrivateKey generates an ecdsa private key using the P512 curve and returns the value.
// The returned private key is FIPS compliant
func PrivateKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
}

// SelfSignedCertificate creates a self signed certificate
func SelfSignedCertificate(cfg *CertCfg, key crypto.Signer) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).SetInt64(math.MaxInt64))
	if err != nil {
		return nil, err
	}
	cert := x509.Certificate{
		BasicConstraintsValid: true,
		IsCA:                  cfg.IsCA,
		KeyUsage:              cfg.KeyUsages,
		NotAfter:              time.Now().Add(cfg.Validity),
		NotBefore:             time.Now(),
		SerialNumber:          serial,
		Subject:               cfg.Subject,
	}
	// verifies that the CN and/or OU for the cert is set
	if len(cfg.Subject.CommonName) == 0 || len(cfg.Subject.OrganizationalUnit) == 0 {
		return nil, errors.Errorf("certification's subject is not set, or invalid")
	}
	pub := key.Public()
	cert.SubjectKeyId, err = generateSubjectKeyID(pub)
	if err != nil {
		return nil, errors.Wrap(err, "failed to set subject key identifier")
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, &cert, &cert, key.Public(), key)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create certificate")
	}
	return x509.ParseCertificate(certBytes)
}

// signedCertificate creates a new X.509 certificate based on a template.
func signedCertificate(
	cfg *CertCfg,
	csr *x509.CertificateRequest,
	key crypto.Signer,
	caCert *x509.Certificate,
	caKey crypto.Signer,
) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).SetInt64(math.MaxInt64))
	if err != nil {
		return nil, err
	}

	certTmpl := x509.Certificate{
		DNSNames:              csr.DNSNames,
		ExtKeyUsage:           cfg.ExtKeyUsages,
		IPAddresses:           csr.IPAddresses,
		KeyUsage:              cfg.KeyUsages,
		NotAfter:              time.Now().Add(cfg.Validity),
		NotBefore:             caCert.NotBefore,
		SerialNumber:          serial,
		Subject:               csr.Subject,
		IsCA:                  cfg.IsCA,
		Version:               3,
		BasicConstraintsValid: true,
	}
	pub := caCert.PublicKey
	certTmpl.SubjectKeyId, err = generateSubjectKeyID(pub)
	if err != nil {
		return nil, errors.Wrap(err, "failed to set subject key identifier")
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, &certTmpl, caCert, key.Public(), caKey)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create x509 certificate")
	}
	return x509.ParseCertificate(certBytes)
}

// generateSubjectKeyID generates a SHA-1 hash of the subject public key.
func generateSubjectKeyID(pub crypto.PublicKey) ([]byte, error) {
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal public key: %w", err)
	}

	hash := sha1.Sum(publicKeyBytes)
	return hash[:], nil
}

// PrivateKeyToPem converts a PrivateKey object to a byte slice
func PrivateKeyToPem(key crypto.Signer) ([]byte, error) {
	switch key := key.(type) {
	case *rsa.PrivateKey:
		keyInBytes := x509.MarshalPKCS1PrivateKey(key)
		return pem.EncodeToMemory(
			&pem.Block{
				Type:  "RSA PRIVATE KEY",
				Bytes: keyInBytes,
			},
		), nil
	case *ecdsa.PrivateKey:
		derKey, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			return nil, err
		}
		return pem.EncodeToMemory(&pem.Block{
			Type:  "EC PRIVATE KEY",
			Bytes: derKey,
		}), nil
	case ed25519.PrivateKey:
		b, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return nil, err
		}
		return pem.EncodeToMemory(&pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: b,
		}), nil
	default:
		return nil, fmt.Errorf("unknown private key type %T", key)
	}
}

// CertToPem converts an x509.Certificate object to a pem string
func CertToPem(cert *x509.Certificate) []byte {
	certInPem := pem.EncodeToMemory(
		&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: cert.Raw,
		},
	)
	return certInPem
}

// CSRToPem converts an x509.CertificateRequest to a pem string
func CSRToPem(cert *x509.CertificateRequest) []byte {
	certInPem := pem.EncodeToMemory(
		&pem.Block{
			Type:  "CERTIFICATE REQUEST",
			Bytes: cert.Raw,
		},
	)
	return certInPem
}

// PublicKeyToPem converts an rsa.PublicKey object to pem string
func PublicKeyToPem(key crypto.PublicKey) ([]byte, error) {
	keyInBytes, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return nil, errors.Wrap(err, "failed to MarshalPKIXPublicKey")
	}
	keyinPem := pem.EncodeToMemory(
		&pem.Block{
			Type:  "PUBLIC KEY",
			Bytes: keyInBytes,
		},
	)
	return keyinPem, nil
}

// PemToPrivateKey converts a data block to private key
func PemToPrivateKey(data []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.Errorf("could not find a PEM block in the private key")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		keyRaw, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		// I can not see a success codepath in x509.ParsePKCS8PrivateKey
		// that does not return a crypto.Signer, it seems to be done to
		// futureproof it.
		cryptoSigner, ok := keyRaw.(crypto.Signer)
		if !ok {
			return nil, fmt.Errorf("private key was not of type crypto.Signer but %T", keyRaw)
		}
		return cryptoSigner, nil
	default:
		return nil, fmt.Errorf("unknown key type %q", block.Type)
	}
}

// PemToCertificate converts a data block to x509.Certificate.
func PemToCertificate(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.Errorf("could not find a PEM block in the certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

func Base64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func ValidateKeyPair(pemKey, pemCertificate []byte, cfg *CertCfg, minimumRemainingValidity time.Duration) error {
	tlsCert, err := tls.X509KeyPair(pemCertificate, pemKey)
	if err != nil {
		return fmt.Errorf("failed to load keypair: %w", err)
	}
	if n := len(tlsCert.Certificate); n != 1 {
		return fmt.Errorf("expected exactly one certificate, got %d", n)
	}
	cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		return fmt.Errorf("failed to parse certificate: %w", err)
	}

	var errs []error
	stringLessFN := func(a, b string) bool { return a < b }

	dnsNamesDiff := cmp.Diff(cert.DNSNames, cfg.DNSNames, cmpopts.SortSlices(stringLessFN))
	if dnsNamesDiff != "" {
		errs = append(errs, fmt.Errorf("actual dns names differ from expected: %s", dnsNamesDiff))
	}

	extUsageDiff := cmp.Diff(cert.ExtKeyUsage, cfg.ExtKeyUsages, cmpopts.SortSlices(func(a, b x509.ExtKeyUsage) bool { return a < b }))
	if extUsageDiff != "" {
		errs = append(errs, fmt.Errorf("actual extended key usages differ from expected: %s", extUsageDiff))
	}

	ipAddressDiff := cmp.Diff(cert.IPAddresses, cfg.IPAddresses, cmpopts.SortSlices(func(a, b []byte) bool { return bytes.Compare(a, b) == -1 }))
	if ipAddressDiff != "" {
		errs = append(errs, fmt.Errorf("actual ip addresses differ from expected: %s", ipAddressDiff))
	}

	if cert.KeyUsage != cfg.KeyUsages {
		errs = append(errs, fmt.Errorf("actual key usage %d differs from expected %d", cert.KeyUsage, cfg.KeyUsages))
	}

	// subjectDiff ignores the "Names" field, as it contains the parsed attributes but is ignored during marshalling.
	subjectDiff := cmp.Diff(cert.Subject, cfg.Subject, cmpopts.SortSlices(stringLessFN), cmpopts.IgnoreFields(pkix.Name{}, "Names"))
	if subjectDiff != "" {
		errs = append(errs, fmt.Errorf("actual subject differs from expected: %s", subjectDiff))
	}

	if remainingvalidity := time.Until(cert.NotAfter); remainingvalidity < minimumRemainingValidity {
		errs = append(errs, fmt.Errorf("remaining validity %s is smaller than the minimum remaining validity %s", remainingvalidity, minimumRemainingValidity))
	}

	if cert.IsCA != cfg.IsCA {
		errs = append(errs, fmt.Errorf("actual isCA %t does not match expected %t", cert.IsCA, cfg.IsCA))
	}

	return utilerrors.NewAggregate(errs)
}
