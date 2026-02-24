package controllers

import (
	"context"
	"crypto/x509"
	"encoding/pem"
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

// verifyMCSCertPEM decodes and parses a PEM-encoded certificate and verifies
// it has the expected MCS TLS certificate properties.
func verifyMCSCertPEM(g Gomega, certPEM []byte) *x509.Certificate {
	block, _ := pem.Decode(certPEM)
	g.Expect(block).NotTo(BeNil())
	cert, err := x509.ParseCertificate(block.Bytes)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(cert.Subject.CommonName).To(Equal(mcsTLSCertCommonName))
	g.Expect(cert.Subject.OrganizationalUnit).To(Equal([]string{"openshift"}))
	g.Expect(cert.IsCA).To(BeTrue())
	return cert
}

func TestGetOrGenerateMCSTLSCert(t *testing.T) {
	t.Parallel()

	t.Run("When no cached cert exists, it should generate a new certificate", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		provider := &LocalIgnitionProvider{}

		certPEM, keyPEM, err := provider.getOrGenerateMCSTLSCert()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(certPEM).NotTo(BeEmpty())
		g.Expect(keyPEM).NotTo(BeEmpty())

		// Verify the cert is valid PEM and has the expected properties
		verifyMCSCertPEM(g, certPEM)

		// Verify the cache was populated
		g.Expect(provider.mcsTLSCache).NotTo(BeNil())
		g.Expect(provider.mcsTLSCache.certPEM).To(Equal(certPEM))
		g.Expect(provider.mcsTLSCache.keyPEM).To(Equal(keyPEM))
	})

	t.Run("When a valid cached cert exists, it should reuse the cached certificate", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		provider := &LocalIgnitionProvider{}

		// Generate initial cert
		certPEM1, keyPEM1, err := provider.getOrGenerateMCSTLSCert()
		g.Expect(err).NotTo(HaveOccurred())

		// Call again - should return the same cached cert
		certPEM2, keyPEM2, err := provider.getOrGenerateMCSTLSCert()
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(certPEM2).To(Equal(certPEM1))
		g.Expect(keyPEM2).To(Equal(keyPEM1))
	})

	t.Run("When the cached cert is near expiry, it should generate a new certificate", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// Use a controllable time function for deterministic testing
		fixedNow := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

		// Set up a provider with a cert that expires in 30 minutes (less than the 1h threshold)
		provider := &LocalIgnitionProvider{
			now: func() time.Time { return fixedNow },
			mcsTLSCache: &mcsTLSCertCache{
				certPEM: []byte("old-cert"),
				keyPEM:  []byte("old-key"),
				expiry:  fixedNow.Add(30 * time.Minute),
			},
		}

		certPEM, keyPEM, err := provider.getOrGenerateMCSTLSCert()
		g.Expect(err).NotTo(HaveOccurred())

		// Should have generated a new cert, not returned the old one
		g.Expect(string(certPEM)).NotTo(Equal("old-cert"))
		g.Expect(string(keyPEM)).NotTo(Equal("old-key"))

		// Verify it's a valid MCS cert
		verifyMCSCertPEM(g, certPEM)

		// Verify the cache is updated with the new cert
		g.Expect(provider.mcsTLSCache.expiry).To(BeTemporally(">", fixedNow))
	})

	t.Run("When the cached cert has expired, it should generate a new certificate", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// Use a controllable time function for deterministic testing
		fixedNow := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

		provider := &LocalIgnitionProvider{
			now: func() time.Time { return fixedNow },
			mcsTLSCache: &mcsTLSCertCache{
				certPEM: []byte("expired-cert"),
				keyPEM:  []byte("expired-key"),
				expiry:  fixedNow.Add(-1 * time.Hour),
			},
		}

		certPEM, keyPEM, err := provider.getOrGenerateMCSTLSCert()
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(string(certPEM)).NotTo(Equal("expired-cert"))
		g.Expect(string(keyPEM)).NotTo(Equal("expired-key"))

		// Verify the cache is updated with the new cert
		g.Expect(provider.mcsTLSCache.expiry).To(BeTemporally(">", fixedNow))
	})

	t.Run("When the cached cert has exactly the minimum remaining validity, it should regenerate", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// Use a controllable time function for deterministic testing
		fixedNow := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

		// Set expiry to exactly now + mcsTLSCertMinRemaining (1h).
		// The condition is nowFn().Add(mcsTLSCertMinRemaining).Before(expiry),
		// which is false when they're equal, so it should regenerate.
		provider := &LocalIgnitionProvider{
			now: func() time.Time { return fixedNow },
			mcsTLSCache: &mcsTLSCertCache{
				certPEM: []byte("boundary-cert"),
				keyPEM:  []byte("boundary-key"),
				expiry:  fixedNow.Add(mcsTLSCertMinRemaining),
			},
		}

		certPEM, keyPEM, err := provider.getOrGenerateMCSTLSCert()
		g.Expect(err).NotTo(HaveOccurred())

		// Should have regenerated since exactly at the boundary
		g.Expect(string(certPEM)).NotTo(Equal("boundary-cert"))
		g.Expect(string(keyPEM)).NotTo(Equal("boundary-key"))

		// Verify it's a valid MCS cert
		verifyMCSCertPEM(g, certPEM)
	})

	t.Run("When the cert has ample remaining validity, it should not regenerate", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// Use a controllable time function
		fixedNow := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
		expiry := fixedNow.Add(certs.ValidityOneDay)

		provider := &LocalIgnitionProvider{
			now: func() time.Time { return fixedNow },
			mcsTLSCache: &mcsTLSCertCache{
				certPEM: []byte("valid-cert"),
				keyPEM:  []byte("valid-key"),
				expiry:  expiry,
			},
		}

		certPEM, keyPEM, err := provider.getOrGenerateMCSTLSCert()
		g.Expect(err).NotTo(HaveOccurred())

		// Should return the cached cert since it has ~24h of validity
		g.Expect(string(certPEM)).To(Equal("valid-cert"))
		g.Expect(string(keyPEM)).To(Equal("valid-key"))
	})
}
