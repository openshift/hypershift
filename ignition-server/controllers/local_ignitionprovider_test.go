package controllers

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
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

func TestMCSTLSCertCache(t *testing.T) {
	t.Run("When GetCertAndKey is called for the first time, it should generate a new certificate", func(t *testing.T) {
		g := NewWithT(t)

		generateCount := 0
		cache := &MCSTLSCertCache{
			generateCert: func(cfg *certs.CertCfg) (*rsa.PrivateKey, *x509.Certificate, error) {
				generateCount++
				return certs.GenerateSelfSignedCertificate(cfg)
			},
			nowFn: time.Now,
		}

		certPEM, keyPEM, err := cache.GetCertAndKey()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(certPEM).NotTo(BeEmpty())
		g.Expect(keyPEM).NotTo(BeEmpty())
		g.Expect(generateCount).To(Equal(1))
	})

	t.Run("When GetCertAndKey is called multiple times with a valid cached cert, it should reuse the cached certificate", func(t *testing.T) {
		g := NewWithT(t)

		generateCount := 0
		cache := &MCSTLSCertCache{
			generateCert: func(cfg *certs.CertCfg) (*rsa.PrivateKey, *x509.Certificate, error) {
				generateCount++
				return certs.GenerateSelfSignedCertificate(cfg)
			},
			nowFn: time.Now,
		}

		certPEM1, keyPEM1, err := cache.GetCertAndKey()
		g.Expect(err).NotTo(HaveOccurred())

		certPEM2, keyPEM2, err := cache.GetCertAndKey()
		g.Expect(err).NotTo(HaveOccurred())

		certPEM3, keyPEM3, err := cache.GetCertAndKey()
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(generateCount).To(Equal(1))
		g.Expect(certPEM2).To(Equal(certPEM1))
		g.Expect(keyPEM2).To(Equal(keyPEM1))
		g.Expect(certPEM3).To(Equal(certPEM1))
		g.Expect(keyPEM3).To(Equal(keyPEM1))
	})

	t.Run("When the cached certificate has expired, it should generate a new certificate", func(t *testing.T) {
		g := NewWithT(t)

		generateCount := 0
		now := time.Now()
		cache := &MCSTLSCertCache{
			generateCert: func(cfg *certs.CertCfg) (*rsa.PrivateKey, *x509.Certificate, error) {
				generateCount++
				return certs.GenerateSelfSignedCertificate(cfg)
			},
			nowFn: func() time.Time { return now },
		}

		certPEM1, _, err := cache.GetCertAndKey()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(generateCount).To(Equal(1))

		// Advance time past the certificate's validity (1 day + margin)
		now = now.Add(25 * time.Hour)

		certPEM2, _, err := cache.GetCertAndKey()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(generateCount).To(Equal(2))
		g.Expect(certPEM2).NotTo(Equal(certPEM1))
	})

	t.Run("When the cached certificate is within expiry margin, it should regenerate", func(t *testing.T) {
		g := NewWithT(t)

		generateCount := 0
		now := time.Now()
		cache := &MCSTLSCertCache{
			generateCert: func(cfg *certs.CertCfg) (*rsa.PrivateKey, *x509.Certificate, error) {
				generateCount++
				return certs.GenerateSelfSignedCertificate(cfg)
			},
			nowFn: func() time.Time { return now },
		}

		_, _, err := cache.GetCertAndKey()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(generateCount).To(Equal(1))

		// Advance time to within the expiry margin (less than 1 hour before expiry)
		// Certificate validity is 24 hours, so 23h30m puts us 30 min before expiry,
		// which is within the 1 hour margin.
		now = now.Add(23*time.Hour + 30*time.Minute)

		_, _, err = cache.GetCertAndKey()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(generateCount).To(Equal(2))
	})

	t.Run("When the certificate generator returns an error, it should propagate the error", func(t *testing.T) {
		g := NewWithT(t)

		cache := &MCSTLSCertCache{
			generateCert: func(cfg *certs.CertCfg) (*rsa.PrivateKey, *x509.Certificate, error) {
				return nil, nil, fmt.Errorf("simulated generation failure")
			},
			nowFn: time.Now,
		}

		certPEM, keyPEM, err := cache.GetCertAndKey()
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("simulated generation failure"))
		g.Expect(certPEM).To(BeNil())
		g.Expect(keyPEM).To(BeNil())
	})

	t.Run("When using NewMCSTLSCertCache constructor, it should generate valid certificates", func(t *testing.T) {
		g := NewWithT(t)

		cache := NewMCSTLSCertCache()
		certPEM, keyPEM, err := cache.GetCertAndKey()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(certPEM).NotTo(BeEmpty())
		g.Expect(keyPEM).NotTo(BeEmpty())

		// Verify the PEM data is valid
		cert, err := certs.PemToCertificate(certPEM)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cert.Subject.CommonName).To(Equal("machine-config-server"))
		g.Expect(cert.IsCA).To(BeTrue())

		_, err = certs.PemToPrivateKey(keyPEM)
		g.Expect(err).NotTo(HaveOccurred())
	})
}
