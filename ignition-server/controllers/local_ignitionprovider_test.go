package controllers

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/certs"
)

func TestExtractMCOBinaries(t *testing.T) {
	testCases := []struct {
		name                  string
		mcoOSReleaseVersion   string
		cpoOSReleaseVersion   string
		expectedBinaryVersion string
		cacheFunc             regClient
	}{
		{
			name:                  "When both MCO and CPO are on RHEL 8, it should extract the RHEL 8 binaries with no prefix",
			mcoOSReleaseVersion:   "8.1",
			cpoOSReleaseVersion:   "8.2",
			expectedBinaryVersion: "rhel8",
		},
		{
			name:                  "When both MCO is in RHEL 8 and CPO on RHEL 9, it should extract the RHEL 9 binaries with the .rhel9 prefix",
			mcoOSReleaseVersion:   "8.1",
			cpoOSReleaseVersion:   "9.1",
			expectedBinaryVersion: "rhel9",
		},
		{
			name:                  "When MCO is in too old version and CPO on RHEL 9, and the RHEL 9 binaries do not exist it should extract the RHEL 8 binaries with no prefix",
			mcoOSReleaseVersion:   "8.0",
			cpoOSReleaseVersion:   "9.1",
			expectedBinaryVersion: "rhel8",
			cacheFunc: func(ctx context.Context, imageRef string, pullSecret []byte, file string, out io.Writer) error {
				switch file {
				case "usr/lib/os-release":
					_, err := fmt.Fprintf(out, "VERSION_ID=\"%s\"\n", "8.0")
					return err
				case "usr/bin/machine-config-operator", "usr/bin/machine-config-controller", "usr/bin/machine-config-server":
					_, err := out.Write([]byte("rhel8"))
					return err
				case "usr/bin/machine-config-operator.rhel9", "usr/bin/machine-config-controller.rhel9", "usr/bin/machine-config-server.rhel9":
					return fmt.Errorf("file not found: %s", file)
				default:
					return fmt.Errorf("unexpected file: %s", file)
				}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			tempDir, err := os.MkdirTemp("", "testExtractBinaries-*")
			if err != nil {
				t.Fatalf("Failed to create temporary directory: %v", err)
			}
			defer func(path string) {
				err = os.RemoveAll(path)
				g.Expect(err).ToNot(HaveOccurred())
			}(tempDir)

			// Set up the necessary variables for testing.
			ctx := t.Context()
			mcoImage := "fake"
			pullSecret := []byte{}
			binDir := filepath.Join(tempDir, "bin")
			err = os.Mkdir(binDir, 0755)
			g.Expect(err).ToNot(HaveOccurred())

			// Create a fake file cache that returns the expected binaries.
			imageFileCache := &imageFileCache{
				cacheMap: make(map[cacheKey]cacheValue),
				cacheDir: tempDir,
			}
			imageFileCache.regClient = func(ctx context.Context, imageRef string, pullSecret []byte, file string, out io.Writer) error {
				switch file {
				case "usr/lib/os-release":
					_, err := fmt.Fprintf(out, "VERSION_ID=\"%s\"\n", tc.mcoOSReleaseVersion)
					return err
				case "usr/bin/machine-config-operator", "usr/bin/machine-config-controller", "usr/bin/machine-config-server":
					_, err := out.Write([]byte("rhel8"))
					return err
				case "usr/bin/machine-config-operator.rhel9", "usr/bin/machine-config-controller.rhel9", "usr/bin/machine-config-server.rhel9":
					_, err := out.Write([]byte("rhel9"))
					return err
				default:
					return fmt.Errorf("unexpected file: %s", file)
				}
			}

			// If the test case has a custom cache function, use it.
			// This is useful to simulate the case where the ocp release for the NodePool is too old that it doesn't have the RHEL binaries.
			if tc.cacheFunc != nil {
				imageFileCache.regClient = tc.cacheFunc
			}

			// Create a fake cpo os-release file
			cpoOSRelease := fmt.Sprintf("VERSION_ID=\"%s\"\n", tc.cpoOSReleaseVersion)
			cpoOSReleaseFilePath := filepath.Join(tempDir, "usr/lib/os-release")
			err = os.MkdirAll(filepath.Dir(cpoOSReleaseFilePath), 0755)
			g.Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(cpoOSReleaseFilePath, []byte(cpoOSRelease), 0644)
			g.Expect(err).NotTo(HaveOccurred())

			// Create a LocalIgnitionProvider instance for testing.
			provider := &LocalIgnitionProvider{
				ImageFileCache: imageFileCache,
				WorkDir:        tempDir,
			}

			// Call the extractMCOBinaries.
			err = provider.extractMCOBinaries(ctx, cpoOSReleaseFilePath, mcoImage, pullSecret, binDir)
			g.Expect(err).NotTo(HaveOccurred())

			// Verify the extracted binaries match the expected version.
			for _, name := range []string{"machine-config-operator", "machine-config-controller", "machine-config-server"} {
				filePath := filepath.Join(binDir, name)
				fileContent, err := os.ReadFile(filePath)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(string(fileContent)).To(Equal(tc.expectedBinaryVersion))
			}
		})
	}
}

func TestGetOrGenerateMCSTLSCert(t *testing.T) {
	testCases := []struct {
		name           string
		existingCache  *mcsTLSCertCache
		expectCacheHit bool
		expectNewCert  bool
	}{
		{
			name:           "When no cached certificate exists, it should generate a new certificate",
			existingCache:  nil,
			expectCacheHit: false,
			expectNewCert:  true,
		},
		{
			name: "When a valid cached certificate exists, it should reuse the cached certificate",
			existingCache: func() *mcsTLSCertCache {
				certPEM, keyPEM, expiry := generateTestCert(t)
				return &mcsTLSCertCache{
					certPEM: certPEM,
					keyPEM:  keyPEM,
					expiry:  expiry,
				}
			}(),
			expectCacheHit: true,
			expectNewCert:  false,
		},
		{
			name: "When the cached certificate is about to expire, it should generate a new certificate",
			existingCache: func() *mcsTLSCertCache {
				certPEM, keyPEM, _ := generateTestCert(t)
				return &mcsTLSCertCache{
					certPEM: certPEM,
					keyPEM:  keyPEM,
					// Set expiry to 30 minutes from now, which is below the 1-hour threshold
					expiry: time.Now().Add(30 * time.Minute),
				}
			}(),
			expectCacheHit: false,
			expectNewCert:  true,
		},
		{
			name: "When the cached certificate has exactly the minimum remaining validity, it should generate a new certificate",
			existingCache: func() *mcsTLSCertCache {
				certPEM, keyPEM, _ := generateTestCert(t)
				return &mcsTLSCertCache{
					certPEM: certPEM,
					keyPEM:  keyPEM,
					// Set expiry to exactly the threshold; time.Until will not be strictly greater
					expiry: time.Now().Add(mcsTLSCertMinRemainingValidity),
				}
			}(),
			expectCacheHit: false,
			expectNewCert:  true,
		},
		{
			name: "When the cached certificate has already expired, it should generate a new certificate",
			existingCache: func() *mcsTLSCertCache {
				certPEM, keyPEM, _ := generateTestCert(t)
				return &mcsTLSCertCache{
					certPEM: certPEM,
					keyPEM:  keyPEM,
					expiry:  time.Now().Add(-1 * time.Hour),
				}
			}(),
			expectCacheHit: false,
			expectNewCert:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			provider := &LocalIgnitionProvider{
				mcsTLSCache: tc.existingCache,
			}

			var originalCertPEM []byte
			if tc.existingCache != nil {
				originalCertPEM = make([]byte, len(tc.existingCache.certPEM))
				copy(originalCertPEM, tc.existingCache.certPEM)
			}

			certPEM, keyPEM, err := provider.getOrGenerateMCSTLSCert()
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(certPEM).NotTo(BeEmpty())
			g.Expect(keyPEM).NotTo(BeEmpty())

			// Verify the cache was populated
			g.Expect(provider.mcsTLSCache).NotTo(BeNil())
			g.Expect(provider.mcsTLSCache.certPEM).To(Equal(certPEM))
			g.Expect(provider.mcsTLSCache.keyPEM).To(Equal(keyPEM))
			g.Expect(provider.mcsTLSCache.expiry).NotTo(BeZero())

			// Verify cache hit vs new generation
			if tc.expectCacheHit {
				g.Expect(certPEM).To(Equal(originalCertPEM), "expected cached certificate to be reused")
			} else if tc.expectNewCert && tc.existingCache != nil {
				g.Expect(certPEM).NotTo(Equal(originalCertPEM), "expected a new certificate to be generated")
			}

			// Verify the returned PEM data is parseable
			cert, err := certs.PemToCertificate(certPEM)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(cert.IsCA).To(BeTrue())

			key, err := certs.PemToPrivateKey(keyPEM)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(key).NotTo(BeNil())

			// For newly generated certs, verify the MCS-specific certificate attributes
			if tc.expectNewCert {
				g.Expect(cert.Subject.CommonName).To(Equal("machine-config-server"))
				g.Expect(cert.Subject.OrganizationalUnit).To(Equal([]string{"openshift"}))
				g.Expect(cert.KeyUsage & x509.KeyUsageKeyEncipherment).NotTo(BeZero())
				g.Expect(cert.KeyUsage & x509.KeyUsageDigitalSignature).NotTo(BeZero())
				g.Expect(cert.KeyUsage & x509.KeyUsageCertSign).NotTo(BeZero())
			}
		})
	}
}

func TestGetOrGenerateMCSTLSCertConsecutiveCalls(t *testing.T) {
	t.Run("When called consecutively, it should return the same cached certificate", func(t *testing.T) {
		g := NewWithT(t)

		provider := &LocalIgnitionProvider{}

		// First call should generate a new certificate
		certPEM1, keyPEM1, err := provider.getOrGenerateMCSTLSCert()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(certPEM1).NotTo(BeEmpty())
		g.Expect(keyPEM1).NotTo(BeEmpty())

		// Second call should return the same cached certificate
		certPEM2, keyPEM2, err := provider.getOrGenerateMCSTLSCert()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(certPEM2).To(Equal(certPEM1))
		g.Expect(keyPEM2).To(Equal(keyPEM1))

		// Third call should also return the same cached certificate
		certPEM3, keyPEM3, err := provider.getOrGenerateMCSTLSCert()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(certPEM3).To(Equal(certPEM1))
		g.Expect(keyPEM3).To(Equal(keyPEM1))
	})
}

// generateTestCert generates a test MCS TLS certificate and returns the PEM bytes and expiry.
func generateTestCert(t *testing.T) (certPEM []byte, keyPEM []byte, expiry time.Time) {
	t.Helper()
	g := NewWithT(t)

	key, crt, err := certs.GenerateSelfSignedCertificate(&certs.CertCfg{
		Subject: pkix.Name{
			CommonName:         "test-cert",
			OrganizationalUnit: []string{"test"},
		},
		KeyUsages: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		Validity:  certs.ValidityOneDay,
		IsCA:      true,
	})
	g.Expect(err).NotTo(HaveOccurred())

	return certs.CertToPem(crt), certs.PrivateKeyToPem(key), crt.NotAfter
}
