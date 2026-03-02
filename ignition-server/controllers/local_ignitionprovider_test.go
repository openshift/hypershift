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

func TestGetOrGenerateMCSCert(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		setupProvider func(t testing.TB, p *LocalIgnitionProvider)
		expectCached  bool
		expectNewCert bool
	}{
		{
			name: "When no cached certificate exists, it should generate a new certificate",
			setupProvider: func(t testing.TB, p *LocalIgnitionProvider) {
				// No setup needed, cache is empty by default.
			},
			expectNewCert: true,
		},
		{
			name: "When a valid cached certificate exists, it should return the cached certificate",
			setupProvider: func(t testing.TB, p *LocalIgnitionProvider) {
				// Pre-populate the cache with a valid cert.
				certPEM, keyPEM, err := p.getOrGenerateMCSCert()
				if err != nil {
					t.Fatalf("failed to generate initial cert: %v", err)
				}
				// Verify the cache was populated
				if certPEM == nil || keyPEM == nil {
					t.Fatal("expected cert and key to be non-nil")
				}
			},
			expectCached: true,
		},
		{
			name: "When the cached certificate has expired, it should generate a new certificate",
			setupProvider: func(t testing.TB, p *LocalIgnitionProvider) {
				// Pre-populate the cache with an expired cert.
				_, _, err := p.getOrGenerateMCSCert()
				if err != nil {
					t.Fatalf("failed to generate initial cert: %v", err)
				}
				// Force the certificate to appear expired
				p.mcsCertExpiry = time.Now().Add(-1 * time.Hour)
			},
			expectNewCert: true,
		},
		{
			name: "When the cached certificate is within the refresh margin, it should generate a new certificate",
			setupProvider: func(t testing.TB, p *LocalIgnitionProvider) {
				// Pre-populate the cache.
				_, _, err := p.getOrGenerateMCSCert()
				if err != nil {
					t.Fatalf("failed to generate initial cert: %v", err)
				}
				// Set expiry to be within the refresh margin (30 minutes from now,
				// which is less than the 1-hour margin).
				p.mcsCertExpiry = time.Now().Add(30 * time.Minute)
			},
			expectNewCert: true,
		},
		{
			name: "When the cached certificate expires exactly at the refresh margin boundary, it should generate a new certificate",
			setupProvider: func(t testing.TB, p *LocalIgnitionProvider) {
				// Pre-populate the cache.
				_, _, err := p.getOrGenerateMCSCert()
				if err != nil {
					t.Fatalf("failed to generate initial cert: %v", err)
				}
				// Set expiry to exactly the refresh margin from now.
				// time.Now().Add(mcsCertRefreshMargin) is NOT before the expiry,
				// so this should trigger regeneration.
				p.mcsCertExpiry = time.Now().Add(mcsCertRefreshMargin)
			},
			expectNewCert: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			provider := &LocalIgnitionProvider{}
			tc.setupProvider(t, provider)

			// Capture the cached cert before the call (if any).
			cachedCertPEM := make([]byte, len(provider.mcsCertPEM))
			copy(cachedCertPEM, provider.mcsCertPEM)

			certPEM, keyPEM, err := provider.getOrGenerateMCSCert()
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(certPEM).NotTo(BeEmpty())
			g.Expect(keyPEM).NotTo(BeEmpty())

			// Verify the certificate is valid PEM
			block, _ := pem.Decode(certPEM)
			g.Expect(block).NotTo(BeNil(), "certificate should be valid PEM")
			g.Expect(block.Type).To(Equal("CERTIFICATE"))

			cert, err := x509.ParseCertificate(block.Bytes)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(cert.Subject.CommonName).To(Equal("machine-config-server"))
			g.Expect(cert.Subject.OrganizationalUnit).To(Equal([]string{"openshift"}))
			g.Expect(cert.IsCA).To(BeTrue())

			// Verify the certificate validity duration is ~24 hours
			g.Expect(cert.NotAfter.Sub(cert.NotBefore)).To(BeNumerically("~", 24*time.Hour, time.Minute))

			// Verify the key is valid PEM
			keyBlock, _ := pem.Decode(keyPEM)
			g.Expect(keyBlock).NotTo(BeNil(), "key should be valid PEM")
			g.Expect(keyBlock.Type).To(Equal("RSA PRIVATE KEY"))

			if tc.expectCached {
				// The returned cert should be the same as the cached one
				g.Expect(certPEM).To(Equal(cachedCertPEM))
			}

			if tc.expectNewCert && len(cachedCertPEM) > 0 {
				// The returned cert should be different from the old cached one
				g.Expect(certPEM).NotTo(Equal(cachedCertPEM))
			}

			// Verify internal cache state was updated
			g.Expect(provider.mcsCertPEM).To(Equal(certPEM))
			g.Expect(provider.mcsKeyPEM).To(Equal(keyPEM))
			g.Expect(provider.mcsCertExpiry).NotTo(BeZero())
			g.Expect(provider.mcsCertExpiry.After(time.Now())).To(BeTrue())
		})
	}
}

func TestGetOrGenerateMCSCertCacheReuse(t *testing.T) {
	t.Parallel()

	t.Run("When calling getOrGenerateMCSCert multiple times, it should return identical results for cached certificates", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		provider := &LocalIgnitionProvider{}

		// First call should generate a new certificate.
		certPEM1, keyPEM1, err := provider.getOrGenerateMCSCert()
		g.Expect(err).NotTo(HaveOccurred())

		// Second call should return the cached certificate.
		certPEM2, keyPEM2, err := provider.getOrGenerateMCSCert()
		g.Expect(err).NotTo(HaveOccurred())

		// Both calls should return the same certificate and key.
		g.Expect(certPEM2).To(Equal(certPEM1))
		g.Expect(keyPEM2).To(Equal(keyPEM1))

		// Third call should still return the cached certificate.
		certPEM3, keyPEM3, err := provider.getOrGenerateMCSCert()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(certPEM3).To(Equal(certPEM1))
		g.Expect(keyPEM3).To(Equal(keyPEM1))
	})
}
