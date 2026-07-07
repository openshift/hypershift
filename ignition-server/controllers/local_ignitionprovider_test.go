package controllers

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	sigs_yaml "sigs.k8s.io/yaml"

	"github.com/blang/semver"
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

func TestBuildMCOVersionArgs(t *testing.T) {
	t.Parallel()

	configDir := "/test/config"
	payloadVersionStr := "test-version"
	getImage := func(key string) string { return "image-" + key }

	tests := []struct {
		name                   string
		version                string
		expectPayloadVersion   bool
		expectImageReferences  bool
		expectSignerCA         bool
		expectLegacyImageFlags bool
		expectRootCA           bool
	}{
		{
			name:                   "When version is 4.12, it should use legacy per-image flags and root CA",
			version:                "4.12.0",
			expectLegacyImageFlags: true,
			expectRootCA:           true,
		},
		{
			name:                  "When version is 4.13, it should use image-references and signer CA without payload-version",
			version:               "4.13.0",
			expectImageReferences: true,
			expectSignerCA:        true,
		},
		{
			name:                  "When version is 4.14, it should include payload-version with image-references and signer CA",
			version:               "4.14.0",
			expectPayloadVersion:  true,
			expectImageReferences: true,
			expectSignerCA:        true,
		},
		{
			name:                  "When version is 4.16, it should include payload-version with image-references and signer CA",
			version:               "4.16.0",
			expectPayloadVersion:  true,
			expectImageReferences: true,
			expectSignerCA:        true,
		},
		{
			name:                  "When version is 5.0, it should include payload-version with image-references and signer CA",
			version:               "5.0.0",
			expectPayloadVersion:  true,
			expectImageReferences: true,
			expectSignerCA:        true,
		},
		{
			name:                  "When version is 5.0 nightly, it should include payload-version with image-references and signer CA",
			version:               "5.0.0-0.nightly-multi-2026-04-07-214955",
			expectPayloadVersion:  true,
			expectImageReferences: true,
			expectSignerCA:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			v, err := semver.Parse(tt.version)
			g.Expect(err).NotTo(HaveOccurred())

			args := buildMCOVersionArgs(v, payloadVersionStr, getImage, configDir)

			if tt.expectPayloadVersion {
				g.Expect(args).To(ContainElement(fmt.Sprintf("--payload-version=%s", payloadVersionStr)))
			} else {
				g.Expect(args).NotTo(ContainElement(ContainSubstring("--payload-version=")))
			}

			expectedImageRef := fmt.Sprintf("--image-references=%s", path.Join(configDir, "release-manifests", "image-references"))
			if tt.expectImageReferences {
				g.Expect(args).To(ContainElement(expectedImageRef))
			} else {
				g.Expect(args).NotTo(ContainElement(ContainSubstring("--image-references=")))
			}

			if tt.expectSignerCA {
				g.Expect(args).To(ContainElement(fmt.Sprintf("--kube-ca=%s/signer-ca.crt", configDir)))
				g.Expect(args).NotTo(ContainElement(ContainSubstring("root-ca.crt")))
			}

			if tt.expectRootCA {
				g.Expect(args).To(ContainElement(fmt.Sprintf("--kube-ca=%s/root-ca.crt", configDir)))
				g.Expect(args).NotTo(ContainElement(ContainSubstring("signer-ca.crt")))
			}

			if tt.expectLegacyImageFlags {
				g.Expect(args).To(ContainElement(fmt.Sprintf("--machine-config-operator-image=%s", getImage("machine-config-operator"))))
				g.Expect(args).To(ContainElement(fmt.Sprintf("--infra-image=%s", getImage("pod"))))
				g.Expect(args).To(ContainElement(fmt.Sprintf("--haproxy-image=%s", getImage("haproxy"))))
			} else {
				g.Expect(args).NotTo(ContainElement(ContainSubstring("--machine-config-operator-image=")))
			}
		})
	}
}

func TestSetupWorkDirs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		setupWorkDir func(t *testing.T) string
		expectError  bool
		expectDirs   []string
	}{
		{
			name: "When given a valid work directory, it should create all subdirectories",
			setupWorkDir: func(t *testing.T) string {
				return t.TempDir()
			},
			expectDirs: []string{"bin", "mco", "mcc", "mcs", "config"},
		},
		{
			name: "When given a non-existent work directory, it should return an error",
			setupWorkDir: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "nonexistent", "subdir")
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			workDir := tt.setupWorkDir(t)
			provider := &LocalIgnitionProvider{}

			dirs, err := provider.setupWorkDirs(workDir)
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(dirs.workDir).To(Equal(workDir))
			g.Expect(dirs.binDir).To(Equal(filepath.Join(workDir, "bin")))
			g.Expect(dirs.mcoDir).To(Equal(filepath.Join(workDir, "mco")))
			g.Expect(dirs.mccDir).To(Equal(filepath.Join(workDir, "mcc")))
			g.Expect(dirs.mcsDir).To(Equal(filepath.Join(workDir, "mcs")))
			g.Expect(dirs.configDir).To(Equal(filepath.Join(workDir, "config")))

			for _, dir := range tt.expectDirs {
				info, err := os.Stat(filepath.Join(workDir, dir))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(info.IsDir()).To(BeTrue())
			}
		})
	}
}

func TestWriteInitialFiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		customConfig        string
		bootstrapKubeConfig []byte
		mcsConfigData       map[string]string
		expectFiles         map[string]string
		unexpectedFiles     []string
	}{
		{
			name:                "When given valid inputs with configuration-hash, it should write all files except configuration-hash",
			customConfig:        "custom-config-content",
			bootstrapKubeConfig: []byte("kubeconfig-data"),
			mcsConfigData: map[string]string{
				"configuration-hash":         "abc123",
				"root-ca.crt":                "ca-cert-data",
				"install-config.yaml":        "install-config-data",
				"user-ca-bundle-config.yaml": "ca-bundle-data",
			},
			expectFiles: map[string]string{
				"mcc/custom.yaml":                   "custom-config-content",
				"mcs/kubeconfig":                    "kubeconfig-data",
				"config/root-ca.crt":                "ca-cert-data",
				"config/install-config.yaml":        "install-config-data",
				"config/user-ca-bundle-config.yaml": "ca-bundle-data",
			},
			unexpectedFiles: []string{"config/configuration-hash"},
		},
		{
			name:                "When given empty MCS config data, it should write only custom config and kubeconfig",
			customConfig:        "some-config",
			bootstrapKubeConfig: []byte("some-kubeconfig"),
			mcsConfigData:       map[string]string{},
			expectFiles: map[string]string{
				"mcc/custom.yaml": "some-config",
				"mcs/kubeconfig":  "some-kubeconfig",
			},
		},
		{
			name:                "When configuration-hash is the only MCS config entry, it should skip it and write no config files",
			customConfig:        "cfg",
			bootstrapKubeConfig: []byte("kc"),
			mcsConfigData: map[string]string{
				"configuration-hash": "hash-only",
			},
			expectFiles: map[string]string{
				"mcc/custom.yaml": "cfg",
				"mcs/kubeconfig":  "kc",
			},
			unexpectedFiles: []string{"config/configuration-hash"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			workDir := t.TempDir()
			provider := &LocalIgnitionProvider{}
			dirs, err := provider.setupWorkDirs(workDir)
			g.Expect(err).NotTo(HaveOccurred())

			mcsConfig := &corev1.ConfigMap{
				Data: tt.mcsConfigData,
			}

			err = provider.writeInitialFiles(dirs, tt.customConfig, tt.bootstrapKubeConfig, mcsConfig)
			g.Expect(err).NotTo(HaveOccurred())

			for relPath, expectedContent := range tt.expectFiles {
				content, err := os.ReadFile(filepath.Join(workDir, relPath))
				g.Expect(err).NotTo(HaveOccurred(), "file %s should exist", relPath)
				g.Expect(string(content)).To(Equal(expectedContent), "file %s content mismatch", relPath)
			}

			for _, relPath := range tt.unexpectedFiles {
				_, err := os.Stat(filepath.Join(workDir, relPath))
				g.Expect(os.IsNotExist(err)).To(BeTrue(), "file %s should not exist", relPath)
			}
		})
	}
}

func TestCopyFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		setup       func(t *testing.T, srcDir string) string
		expectError bool
		validate    func(g Gomega, srcPath, dstPath string)
	}{
		{
			name: "When copying a regular file, it should preserve content and permissions",
			setup: func(t *testing.T, srcDir string) string {
				srcPath := filepath.Join(srcDir, "source.txt")
				err := os.WriteFile(srcPath, []byte("hello world"), 0755)
				if err != nil {
					t.Fatalf("failed to write source file: %v", err)
				}
				return srcPath
			},
			validate: func(g Gomega, srcPath, dstPath string) {
				dstContent, err := os.ReadFile(dstPath)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(string(dstContent)).To(Equal("hello world"))

				srcInfo, err := os.Stat(srcPath)
				g.Expect(err).NotTo(HaveOccurred())
				dstInfo, err := os.Stat(dstPath)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(dstInfo.Mode()).To(Equal(srcInfo.Mode()))
			},
		},
		{
			name: "When the source file does not exist, it should return an error",
			setup: func(t *testing.T, srcDir string) string {
				return filepath.Join(srcDir, "nonexistent.txt")
			},
			expectError: true,
		},
		{
			name: "When copying an empty file, it should create an empty destination file",
			setup: func(t *testing.T, srcDir string) string {
				srcPath := filepath.Join(srcDir, "empty.txt")
				err := os.WriteFile(srcPath, []byte{}, 0644)
				if err != nil {
					t.Fatalf("failed to write source file: %v", err)
				}
				return srcPath
			},
			validate: func(g Gomega, srcPath, dstPath string) {
				dstContent, err := os.ReadFile(dstPath)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(dstContent).To(BeEmpty())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			srcDir := t.TempDir()
			dstDir := t.TempDir()
			srcPath := tt.setup(t, srcDir)
			dstPath := filepath.Join(dstDir, "dest.txt")

			err := copyFile(srcPath, dstPath)
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}

			g.Expect(err).NotTo(HaveOccurred())
			tt.validate(g, srcPath, dstPath)
		})
	}
}

func TestCopyMCOOutputToMCC(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		setupDirs       func(t *testing.T, destDir, mccDir, configDir string)
		expectFiles     []string
		unexpectedFiles []string
		expectError     bool
	}{
		{
			name: "When bootstrap manifests contain files and directories, it should copy only files",
			setupDirs: func(t *testing.T, destDir, mccDir, configDir string) {
				g := NewWithT(t)
				bootstrapManifests := filepath.Join(destDir, "bootstrap", "manifests")
				g.Expect(os.MkdirAll(bootstrapManifests, 0755)).To(Succeed())
				g.Expect(os.WriteFile(filepath.Join(bootstrapManifests, "manifest1.yaml"), []byte("m1"), 0644)).To(Succeed())
				g.Expect(os.WriteFile(filepath.Join(bootstrapManifests, "manifest2.yaml"), []byte("m2"), 0644)).To(Succeed())
				g.Expect(os.MkdirAll(filepath.Join(bootstrapManifests, "subdir"), 0755)).To(Succeed())
			},
			expectFiles:     []string{"manifest1.yaml", "manifest2.yaml"},
			unexpectedFiles: []string{"subdir"},
		},
		{
			name: "When config directory has machineconfigpool files, it should copy them to mcc",
			setupDirs: func(t *testing.T, destDir, mccDir, configDir string) {
				g := NewWithT(t)
				bootstrapManifests := filepath.Join(destDir, "bootstrap", "manifests")
				g.Expect(os.MkdirAll(bootstrapManifests, 0755)).To(Succeed())
				g.Expect(os.WriteFile(filepath.Join(configDir, "worker.machineconfigpool.yaml"), []byte("worker-pool"), 0644)).To(Succeed())
				g.Expect(os.WriteFile(filepath.Join(configDir, "master.machineconfigpool.yaml"), []byte("master-pool"), 0644)).To(Succeed())
				g.Expect(os.WriteFile(filepath.Join(configDir, "other-config.yaml"), []byte("other"), 0644)).To(Succeed())
			},
			expectFiles:     []string{"worker.machineconfigpool.yaml", "master.machineconfigpool.yaml"},
			unexpectedFiles: []string{"other-config.yaml"},
		},
		{
			name: "When bootstrap manifests directory does not exist, it should return an error",
			setupDirs: func(t *testing.T, destDir, mccDir, configDir string) {
				// Don't create bootstrap/manifests
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			tmpDir := t.TempDir()
			destDir := filepath.Join(tmpDir, "dest")
			mccDir := filepath.Join(tmpDir, "mcc")
			configDir := filepath.Join(tmpDir, "config")
			g.Expect(os.MkdirAll(destDir, 0755)).To(Succeed())
			g.Expect(os.MkdirAll(mccDir, 0755)).To(Succeed())
			g.Expect(os.MkdirAll(configDir, 0755)).To(Succeed())

			tt.setupDirs(t, destDir, mccDir, configDir)

			provider := &LocalIgnitionProvider{}
			err := provider.copyMCOOutputToMCC(destDir, mccDir, configDir)
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}

			g.Expect(err).NotTo(HaveOccurred())
			for _, f := range tt.expectFiles {
				_, err := os.Stat(filepath.Join(mccDir, f))
				g.Expect(err).NotTo(HaveOccurred(), "expected file %s to exist in mccDir", f)
			}
			for _, f := range tt.unexpectedFiles {
				_, err := os.Stat(filepath.Join(mccDir, f))
				g.Expect(os.IsNotExist(err)).To(BeTrue(), "file %s should not exist in mccDir", f)
			}
		})
	}
}

func TestInvokeFeatureGateRenderScript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		version              string
		expectSubstring      []string
		unexpectedSubstrings []string
	}{
		{
			name:    "When version is 4.16, it should use the default script with cluster-profile and rendered-manifest-dir",
			version: "4.16.0",
			expectSubstring: []string{
				"--cluster-profile=ibm-cloud-managed",
				"--payload-version=4.16.0",
				"--rendered-manifest-dir=",
				"--asset-output-dir",
			},
			unexpectedSubstrings: []string{
				"--config-output-file",
				"--asset-input-dir",
				"--rendered-manifest-files=",
			},
		},
		{
			name:    "When version is 4.14, it should use the render subcommand with rendered-manifest-files and payload-version",
			version: "4.14.0",
			expectSubstring: []string{
				"render",
				"--config-output-file",
				"--asset-input-dir",
				"--rendered-manifest-files=",
				"--payload-version=4.14.0",
			},
			unexpectedSubstrings: []string{
				"--cluster-profile",
				"--rendered-manifest-dir=",
			},
		},
		{
			name:    "When version is 4.13, it should use the render subcommand without payload-version",
			version: "4.13.0",
			expectSubstring: []string{
				"render",
				"--config-output-file",
				"--asset-input-dir",
				"--asset-output-dir",
			},
			unexpectedSubstrings: []string{
				"--payload-version",
				"--cluster-profile",
				"--rendered-manifest-files=",
			},
		},
		{
			name:    "When version is 4.12, it should use the render subcommand without payload-version",
			version: "4.12.0",
			expectSubstring: []string{
				"render",
				"--config-output-file",
				"--asset-input-dir",
				"--asset-output-dir",
			},
			unexpectedSubstrings: []string{
				"--payload-version",
				"--cluster-profile",
				"--rendered-manifest-files=",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			v, err := semver.Parse(tt.version)
			g.Expect(err).NotTo(HaveOccurred())

			script := invokeFeatureGateRenderScript("/bin/render", "/work/cca", "/output/mcc", v, "featuregate-yaml-content")

			for _, substr := range tt.expectSubstring {
				g.Expect(script).To(ContainSubstring(substr), "script should contain %q", substr)
			}
			for _, substr := range tt.unexpectedSubstrings {
				g.Expect(script).NotTo(ContainSubstring(substr), "script should not contain %q", substr)
			}

			// All versions should embed the feature gate YAML
			g.Expect(script).To(ContainSubstring("featuregate-yaml-content"))
			// All versions should copy the feature gate to the output directory
			g.Expect(script).To(ContainSubstring("cp"))
			g.Expect(script).To(ContainSubstring("99_feature-gate.yaml"))
		})
	}
}

func newFakeClientWithScheme(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = hyperv1.AddToScheme(scheme)
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&hyperv1.HostedControlPlane{}).
		Build()
}

func TestFetchPullSecret(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		objects     []client.Object
		expectError string
		expectData  []byte
	}{
		{
			name: "When pull secret exists with correct key, it should return the data",
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: pullSecretName, Namespace: "test-ns"},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`),
					},
				},
			},
			expectData: []byte(`{"auths":{}}`),
		},
		{
			name: "When pull secret exists but is missing the docker config key, it should return an error",
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: pullSecretName, Namespace: "test-ns"},
					Data:       map[string][]byte{"wrong-key": []byte("data")},
				},
			},
			expectError: "missing",
		},
		{
			name:        "When pull secret does not exist, it should return an error",
			objects:     []client.Object{},
			expectError: "failed to get pull secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			provider := &LocalIgnitionProvider{
				Client:    newFakeClientWithScheme(tt.objects...),
				Namespace: "test-ns",
			}

			data, err := provider.fetchPullSecret(t.Context())
			if tt.expectError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.expectError))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(data).To(Equal(tt.expectData))
		})
	}
}

func TestFetchAdditionalTrustBundle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		objects     []client.Object
		expectError string
		expectData  string
	}{
		{
			name: "When trust bundle configmap exists with correct key, it should return the data",
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: additionalTrustBundleName, Namespace: "test-ns"},
					Data:       map[string]string{"ca-bundle.crt": "cert-data"},
				},
			},
			expectData: "cert-data",
		},
		{
			name:       "When trust bundle configmap does not exist, it should return empty string without error",
			objects:    []client.Object{},
			expectData: "",
		},
		{
			name: "When trust bundle configmap exists but is missing the ca-bundle.crt key, it should return an error",
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: additionalTrustBundleName, Namespace: "test-ns"},
					Data:       map[string]string{"wrong-key": "data"},
				},
			},
			expectError: "missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			provider := &LocalIgnitionProvider{
				Client:    newFakeClientWithScheme(tt.objects...),
				Namespace: "test-ns",
			}

			data, err := provider.fetchAdditionalTrustBundle(t.Context())
			if tt.expectError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.expectError))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(data).To(Equal(tt.expectData))
		})
	}
}

func TestFetchBootstrapKubeConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		objects     []client.Object
		expectError string
		expectData  []byte
	}{
		{
			name: "When bootstrap kubeconfig secret exists with correct key, it should return the data",
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "bootstrap-kubeconfig", Namespace: "test-ns"},
					Data:       map[string][]byte{"kubeconfig": []byte("kc-data")},
				},
			},
			expectData: []byte("kc-data"),
		},
		{
			name: "When bootstrap kubeconfig secret exists but is missing the kubeconfig key, it should return an error",
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "bootstrap-kubeconfig", Namespace: "test-ns"},
					Data:       map[string][]byte{"wrong-key": []byte("data")},
				},
			},
			expectError: "missing kubeconfig key",
		},
		{
			name:        "When bootstrap kubeconfig secret does not exist, it should return an error",
			objects:     []client.Object{},
			expectError: "failed to get bootstrap kubeconfig",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			provider := &LocalIgnitionProvider{
				Client:    newFakeClientWithScheme(tt.objects...),
				Namespace: "test-ns",
			}

			data, err := provider.fetchBootstrapKubeConfig(t.Context())
			if tt.expectError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.expectError))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(data).To(Equal(tt.expectData))
		})
	}
}

func TestFetchAndValidateMCSConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		objects             []client.Object
		hcConfigurationHash string
		expectError         string
	}{
		{
			name: "When MCS configmap exists and hash matches, it should return the configmap",
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "machine-config-server", Namespace: "test-ns"},
					Data: map[string]string{
						"configuration-hash":         "expected-hash",
						"user-ca-bundle-config.yaml": "{}",
					},
				},
			},
			hcConfigurationHash: "expected-hash",
		},
		{
			name: "When MCS configmap exists and hash is empty, it should skip hash validation",
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "machine-config-server", Namespace: "test-ns"},
					Data: map[string]string{
						"configuration-hash":         "any-hash",
						"user-ca-bundle-config.yaml": "{}",
					},
				},
			},
			hcConfigurationHash: "",
		},
		{
			name: "When MCS configmap hash does not match, it should return an error",
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "machine-config-server", Namespace: "test-ns"},
					Data: map[string]string{
						"configuration-hash":         "actual-hash",
						"user-ca-bundle-config.yaml": "{}",
					},
				},
			},
			hcConfigurationHash: "expected-hash",
			expectError:         "out of date",
		},
		{
			name:        "When MCS configmap does not exist, it should return an error",
			objects:     []client.Object{},
			expectError: "failed to get machine-config-server configmap",
		},
		{
			name: "When managed trust bundle disagrees with MCS user-ca-bundle, it should return an error",
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "machine-config-server", Namespace: "test-ns"},
					Data: map[string]string{
						"user-ca-bundle-config.yaml": `{"apiVersion":"v1","kind":"ConfigMap","data":{"ca-bundle.crt":"mcs-ca-data"}}`,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: managedTrustBundleName, Namespace: "test-ns"},
					Data:       map[string]string{"ca-bundle.crt": "different-ca-data"},
				},
			},
			expectError: "does not contain the same ca-bundle.crt",
		},
		{
			name: "When managed trust bundle matches MCS user-ca-bundle, it should succeed",
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "machine-config-server", Namespace: "test-ns"},
					Data: map[string]string{
						"user-ca-bundle-config.yaml": `{"apiVersion":"v1","kind":"ConfigMap","data":{"ca-bundle.crt":"same-ca-data"}}`,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: managedTrustBundleName, Namespace: "test-ns"},
					Data:       map[string]string{"ca-bundle.crt": "same-ca-data"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			provider := &LocalIgnitionProvider{
				Client:    newFakeClientWithScheme(tt.objects...),
				Namespace: "test-ns",
			}

			result, err := provider.fetchAndValidateMCSConfig(t.Context(), tt.hcConfigurationHash)
			if tt.expectError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.expectError))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(result).NotTo(BeNil())
		})
	}
}

func TestWriteCloudProviderConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		cloudProvider hyperv1.PlatformType
		objects       []client.Object
		expectFile    bool
		expectError   string
	}{
		{
			name:          "When cloud provider is AWS, it should return nil without writing any file",
			cloudProvider: hyperv1.AWSPlatform,
			expectFile:    false,
		},
		{
			name:          "When cloud provider is KubeVirt, it should return nil without writing any file",
			cloudProvider: hyperv1.KubevirtPlatform,
			expectFile:    false,
		},
		{
			name:          "When cloud provider is Azure and configmap exists, it should write cloud config file",
			cloudProvider: hyperv1.AzurePlatform,
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "azure-cloud-config", Namespace: "test-ns"},
					Data:       map[string]string{"cloud.conf": "azure-config-data"},
				},
			},
			expectFile: true,
		},
		{
			name:          "When cloud provider is OpenStack and configmap exists, it should write cloud config file",
			cloudProvider: hyperv1.OpenStackPlatform,
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "openstack-cloud-config", Namespace: "test-ns"},
					Data:       map[string]string{"cloud.conf": "openstack-config-data"},
				},
			},
			expectFile: true,
		},
		{
			name:          "When cloud provider is Azure and configmap does not exist, it should return an error",
			cloudProvider: hyperv1.AzurePlatform,
			objects:       []client.Object{},
			expectError:   "failed to get cloud provider configmap",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			mcoDir := filepath.Join(t.TempDir(), "mco")
			g.Expect(os.MkdirAll(mcoDir, 0755)).To(Succeed())

			provider := &LocalIgnitionProvider{
				Client:        newFakeClientWithScheme(tt.objects...),
				Namespace:     "test-ns",
				CloudProvider: tt.cloudProvider,
			}

			err := provider.writeCloudProviderConfig(t.Context(), mcoDir)
			if tt.expectError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.expectError))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())

			cloudConfPath := filepath.Join(mcoDir, "cloud.conf.configmap.yaml")
			if tt.expectFile {
				_, err := os.Stat(cloudConfPath)
				g.Expect(err).NotTo(HaveOccurred(), "cloud config file should exist")
			} else {
				_, err := os.Stat(cloudConfPath)
				g.Expect(os.IsNotExist(err)).To(BeTrue(), "cloud config file should not exist for non-cloud providers")
			}
		})
	}
}

func TestReconcileValidReleaseInfoCondition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		namespace         string
		objects           []client.Object
		missingImages     []string
		expectError       string
		expectCondStatus  metav1.ConditionStatus
		expectCondReason  string
		expectCondMessage string
	}{
		{
			name:      "When no images are missing, it should set condition to True with AsExpected reason",
			namespace: "test-ns",
			objects: []client.Object{
				&hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Name: "hcp", Namespace: "test-ns", Generation: 5},
				},
			},
			missingImages:     []string{},
			expectCondStatus:  metav1.ConditionTrue,
			expectCondReason:  hyperv1.AsExpectedReason,
			expectCondMessage: hyperv1.AllIsWellMessage,
		},
		{
			name:      "When images are missing, it should set condition to False with MissingReleaseImages reason",
			namespace: "test-ns",
			objects: []client.Object{
				&hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Name: "hcp", Namespace: "test-ns", Generation: 3},
				},
			},
			missingImages:     []string{"image-a", "image-b"},
			expectCondStatus:  metav1.ConditionFalse,
			expectCondReason:  hyperv1.MissingReleaseImagesReason,
			expectCondMessage: "image-a, image-b",
		},
		{
			name:        "When no HostedControlPlane exists in the namespace, it should return an error",
			namespace:   "empty-ns",
			objects:     []client.Object{},
			expectError: "failed to find HostedControlPlane",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			fakeClient := newFakeClientWithScheme(tt.objects...)
			provider := &LocalIgnitionProvider{
				Client:    fakeClient,
				Namespace: tt.namespace,
			}

			// Build a SimpleReleaseImageProvider with controlled missing images.
			imageProvider := imageprovider.NewFromImages(map[string]string{})
			// Access missing images by calling GetImage for each missing image name,
			// which adds them to the missingImages list.
			for _, img := range tt.missingImages {
				imageProvider.GetImage(img)
			}

			err := provider.reconcileValidReleaseInfoCondition(t.Context(), imageProvider)
			if tt.expectError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.expectError))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())

			// Verify the condition was set on the HostedControlPlane.
			hcpList := &hyperv1.HostedControlPlaneList{}
			g.Expect(fakeClient.List(t.Context(), hcpList, client.InNamespace(tt.namespace))).To(Succeed())
			g.Expect(hcpList.Items).To(HaveLen(1))

			cond := meta.FindStatusCondition(hcpList.Items[0].Status.Conditions, string(hyperv1.IgnitionServerValidReleaseInfo))
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(tt.expectCondStatus))
			g.Expect(cond.Reason).To(Equal(tt.expectCondReason))
			g.Expect(cond.Message).To(Equal(tt.expectCondMessage))
		})
	}
}

func TestInvokeFeatureGateRenderScriptEmbedsBinaryAndPaths(t *testing.T) {
	t.Parallel()

	t.Run("When given specific binary and directory paths, it should embed them correctly in the script", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		binary := "/custom/path/to/render-binary"
		workDir := "/tmp/workdir"
		outputDir := "/tmp/output"
		fgYAML := "apiVersion: config.openshift.io/v1\nkind: FeatureGate"
		v := semver.Version{Major: 4, Minor: 16}

		script := invokeFeatureGateRenderScript(binary, workDir, outputDir, v, fgYAML)

		g.Expect(script).To(ContainSubstring(binary))
		g.Expect(script).To(ContainSubstring(workDir))
		g.Expect(script).To(ContainSubstring(outputDir))
		g.Expect(script).To(ContainSubstring(fgYAML))
		g.Expect(script).To(ContainSubstring("set -e"))
		g.Expect(script).To(ContainSubstring("mkdir -p"))
		// The final cp should copy to the output directory
		g.Expect(script).To(ContainSubstring(fmt.Sprintf("cp %s/manifests/99_feature-gate.yaml %s/99_feature-gate.yaml", workDir, outputDir)))
	})
}

func TestWriteInitialFilesMultipleConfigEntries(t *testing.T) {
	t.Parallel()

	t.Run("When MCS config has multiple entries including configuration-hash, it should write all except configuration-hash", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		workDir := t.TempDir()
		provider := &LocalIgnitionProvider{}
		dirs, err := provider.setupWorkDirs(workDir)
		g.Expect(err).NotTo(HaveOccurred())

		configEntries := map[string]string{
			"configuration-hash":                    "skip-me",
			"root-ca.crt":                           "root-ca-data",
			"signer-ca.crt":                         "signer-ca-data",
			"cluster-infrastructure-02-config.yaml": "infra-config",
			"cluster-network-02-config.yaml":        "network-config",
			"cluster-dns-02-config.yaml":            "dns-config",
			"install-config.yaml":                   "install-config",
		}

		mcsConfig := &corev1.ConfigMap{Data: configEntries}
		err = provider.writeInitialFiles(dirs, "custom", []byte("kc"), mcsConfig)
		g.Expect(err).NotTo(HaveOccurred())

		// Count files in configDir -- should be len(configEntries) - 1 (excluding configuration-hash)
		entries, err := os.ReadDir(dirs.configDir)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(entries).To(HaveLen(len(configEntries) - 1))

		// Verify configuration-hash was not written
		_, err = os.Stat(filepath.Join(dirs.configDir, "configuration-hash"))
		g.Expect(os.IsNotExist(err)).To(BeTrue())
	})
}

func TestWriteOSImageStreamManifest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		osStream              string
		expectedAPIVersion    string
		expectedKind          string
		expectedName          string
		expectedDefaultStream string
	}{
		{
			name:                  "When osStream is rhel-10 it should write a valid OSImageStream CR with defaultStream rhel-10",
			osStream:              "rhel-10",
			expectedAPIVersion:    "machineconfiguration.openshift.io/v1alpha1",
			expectedKind:          "OSImageStream",
			expectedName:          "cluster",
			expectedDefaultStream: "rhel-10",
		},
		{
			name:                  "When osStream is rhel-9 it should write a valid OSImageStream CR with defaultStream rhel-9",
			osStream:              "rhel-9",
			expectedAPIVersion:    "machineconfiguration.openshift.io/v1alpha1",
			expectedKind:          "OSImageStream",
			expectedName:          "cluster",
			expectedDefaultStream: "rhel-9",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			mccDir := t.TempDir()
			err := writeOSImageStreamManifest(mccDir, tt.osStream)
			g.Expect(err).NotTo(HaveOccurred())

			content, err := os.ReadFile(filepath.Join(mccDir, "99_osimagestream.yaml"))
			g.Expect(err).NotTo(HaveOccurred())

			// Parse the YAML to verify structure.
			var obj map[string]any
			err = sigs_yaml.Unmarshal(content, &obj)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(obj["apiVersion"]).To(Equal(tt.expectedAPIVersion))
			g.Expect(obj["kind"]).To(Equal(tt.expectedKind))

			metadata, ok := obj["metadata"].(map[string]any)
			g.Expect(ok).To(BeTrue())
			g.Expect(metadata["name"]).To(Equal(tt.expectedName))

			spec, ok := obj["spec"].(map[string]any)
			g.Expect(ok).To(BeTrue())
			g.Expect(spec["defaultStream"]).To(Equal(tt.expectedDefaultStream))
		})
	}
}

func TestTruncateTail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		max    int
		expect string
	}{
		{
			name:   "When input is shorter than max, it should return the full string",
			input:  "short",
			max:    100,
			expect: "short",
		},
		{
			name:   "When input is exactly max length, it should return the full string",
			input:  "exact",
			max:    5,
			expect: "exact",
		},
		{
			name:   "When input is longer than max, it should return the tail",
			input:  "abcdefghij",
			max:    4,
			expect: "ghij",
		},
		{
			name:   "When input is empty, it should return empty string",
			input:  "",
			max:    10,
			expect: "",
		},
		{
			name:   "When max is zero, it should return empty string",
			input:  "anything",
			max:    0,
			expect: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(truncateTail(tt.input, tt.max)).To(Equal(tt.expect))
		})
	}
}

func TestSyncBuffer(t *testing.T) {
	t.Parallel()

	t.Run("When writing data, it should be readable via String", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		var b syncBuffer
		n, err := b.Write([]byte("hello "))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(n).To(Equal(6))

		n, err = b.Write([]byte("world"))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(n).To(Equal(5))

		g.Expect(b.String()).To(Equal("hello world"))
	})

	t.Run("When data exceeds maxMCSLogBytes, String should return truncated tail", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		var b syncBuffer
		// Write more than maxMCSLogBytes (8 KiB)
		large := make([]byte, maxMCSLogBytes+100)
		for i := range large {
			large[i] = byte('a' + (i % 26))
		}
		_, err := b.Write(large)
		g.Expect(err).NotTo(HaveOccurred())

		result := b.String()
		g.Expect(len(result)).To(Equal(maxMCSLogBytes))
		g.Expect(result).To(Equal(string(large[100:])))
	})

	t.Run("When writing from multiple goroutines, it should not panic", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		var b syncBuffer
		done := make(chan struct{})
		for i := 0; i < 10; i++ {
			go func() {
				defer func() { done <- struct{}{} }()
				for j := 0; j < 100; j++ {
					_, _ = b.Write([]byte("x"))
					_ = b.String()
				}
			}()
		}
		for i := 0; i < 10; i++ {
			<-done
		}
		g.Expect(len(b.String())).To(BeNumerically("<=", maxMCSLogBytes))
	})
}

func TestCopyMCOOutputToMCCMixedContent(t *testing.T) {
	t.Parallel()

	t.Run("When bootstrap manifests and config dir both have matching and non-matching files, it should only copy appropriate files", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		tmpDir := t.TempDir()
		destDir := filepath.Join(tmpDir, "dest")
		mccDir := filepath.Join(tmpDir, "mcc")
		configDir := filepath.Join(tmpDir, "config")

		bootstrapManifests := filepath.Join(destDir, "bootstrap", "manifests")
		g.Expect(os.MkdirAll(bootstrapManifests, 0755)).To(Succeed())
		g.Expect(os.MkdirAll(mccDir, 0755)).To(Succeed())
		g.Expect(os.MkdirAll(configDir, 0755)).To(Succeed())

		// Create bootstrap manifest files and a subdirectory
		g.Expect(os.WriteFile(filepath.Join(bootstrapManifests, "a.yaml"), []byte("a"), 0644)).To(Succeed())
		g.Expect(os.WriteFile(filepath.Join(bootstrapManifests, "b.yaml"), []byte("b"), 0644)).To(Succeed())
		g.Expect(os.MkdirAll(filepath.Join(bootstrapManifests, "skip-this-dir"), 0755)).To(Succeed())

		// Create config files: one matching machineconfigpool glob, one not
		g.Expect(os.WriteFile(filepath.Join(configDir, "custom.machineconfigpool.yaml"), []byte("pool"), 0644)).To(Succeed())
		g.Expect(os.WriteFile(filepath.Join(configDir, "other.yaml"), []byte("nope"), 0644)).To(Succeed())

		provider := &LocalIgnitionProvider{}
		err := provider.copyMCOOutputToMCC(destDir, mccDir, configDir)
		g.Expect(err).NotTo(HaveOccurred())

		// Manifest files should be copied
		content, err := os.ReadFile(filepath.Join(mccDir, "a.yaml"))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(string(content)).To(Equal("a"))

		content, err = os.ReadFile(filepath.Join(mccDir, "b.yaml"))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(string(content)).To(Equal("b"))

		// Machineconfigpool file should be copied
		content, err = os.ReadFile(filepath.Join(mccDir, "custom.machineconfigpool.yaml"))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(string(content)).To(Equal("pool"))

		// The subdirectory from bootstrap manifests should NOT be copied
		_, err = os.Stat(filepath.Join(mccDir, "skip-this-dir"))
		g.Expect(os.IsNotExist(err)).To(BeTrue())

		// The non-matching config file should NOT be copied
		_, err = os.Stat(filepath.Join(mccDir, "other.yaml"))
		g.Expect(os.IsNotExist(err)).To(BeTrue())
	})
}
