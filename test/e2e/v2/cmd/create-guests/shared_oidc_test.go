//go:build e2ev2

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/blang/semver"
)

func sharedOIDCTestKey(t *testing.T) (string, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	path := filepath.Join(t.TempDir(), "signing-key.pem")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path, data
}

func TestConfigureSharedOIDC(t *testing.T) {
	keyPath, keyData := sharedOIDCTestKey(t)
	block, _ := pem.Decode(keyData)
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8Path := filepath.Join(t.TempDir(), "pkcs8.pem")
	if err := os.WriteFile(pkcs8Path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}), 0600); err != nil {
		t.Fatal(err)
	}
	invalidKey := filepath.Join(t.TempDir(), "invalid-key")
	if err := os.WriteFile(invalidKey, []byte("not a private key"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name, issuer, path, conflicting string
		nonAWS, invalid                 bool
	}{
		{name: "When shared inputs are absent, it should preserve existing fixture configuration"},
		{name: "When both shared inputs are supplied, it should reuse them for every selected AWS fixture", issuer: "https://oidc.example.org/suite", path: keyPath},
		{name: "When only issuer is supplied, it should reject partial input", issuer: "https://oidc.example.org/suite", invalid: true},
		{name: "When only key is supplied, it should reject partial input", path: keyPath, invalid: true},
		{name: "When another platform is selected, it should reject AWS shared input", issuer: "https://oidc.example.org/suite", path: keyPath, nonAWS: true, invalid: true},
		{name: "When issuer is not HTTPS, it should reject the URL", issuer: "http://oidc.example.org", path: keyPath, invalid: true},
		{name: "When issuer contains credentials, it should reject the URL", issuer: "https://name:password@oidc.example.org", path: keyPath, invalid: true},
		{name: "When issuer contains a query, it should reject the URL", issuer: "https://oidc.example.org?query=value", path: keyPath, invalid: true},
		{name: "When issuer contains an empty query marker, it should reject the URL", issuer: "https://oidc.example.org?", path: keyPath, invalid: true},
		{name: "When issuer contains a fragment, it should reject the URL", issuer: "https://oidc.example.org#fragment", path: keyPath, invalid: true},
		{name: "When issuer contains an empty fragment marker, it should reject the URL", issuer: "https://oidc.example.org#", path: keyPath, invalid: true},
		{name: "When key is missing, it should reject before provisioning", issuer: "https://oidc.example.org", path: filepath.Join(t.TempDir(), "missing"), invalid: true},
		{name: "When key is malformed, it should reject before provisioning", issuer: "https://oidc.example.org", path: invalidKey, invalid: true},
		{name: "When key uses PKCS8 unsupported by the HostedCluster consumer, it should reject before provisioning", issuer: "https://oidc.example.org", path: pkcs8Path, invalid: true},
		{name: "When provider identity matches a selected cluster, it should reject before per-cluster cleanup can delete it", issuer: "https://oidc.example.org/" + lifecycle.DeriveClusterName("", "public"), path: keyPath, invalid: true},
		{name: "When fixture already supplies an issuer, it should reject conflicting flags", issuer: "https://oidc.example.org", path: keyPath, conflicting: "--oidc-issuer-url=existing", invalid: true},
		{name: "When fixture supplies a split signing-key flag, it should reject conflicting flags", issuer: "https://oidc.example.org", path: keyPath, conflicting: "--sa-token-issuer-private-key-path", invalid: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := envConfig{platform: &lifecycle.AWSPlatformConfig{}, sharedOIDCIssuer: testCase.issuer, sharedOIDCKeyFile: testCase.path}
			if testCase.nonAWS {
				cfg.platform = &lifecycle.AzurePlatformConfig{}
			}
			specs := []lifecycle.ClusterSpec{{Variant: "public", CreateArgs: []string{"--region=us-east-1"}}, {Variant: "private", CreateArgs: []string{"--endpoint-access=Private"}}}
			if testCase.conflicting != "" {
				specs[1].CreateArgs = append(specs[1].CreateArgs, testCase.conflicting)
			}
			original := append([]lifecycle.ClusterSpec(nil), specs...)
			for index := range original {
				original[index].CreateArgs = slices.Clone(specs[index].CreateArgs)
			}
			configured, err := configureSharedOIDC(cfg, specs)
			if (err != nil) != testCase.invalid {
				t.Fatalf("unexpected configuration result: %v", err)
			}
			if !reflect.DeepEqual(specs, original) {
				t.Fatal("configuration mutated platform fixture inputs")
			}
			if testCase.invalid {
				return
			}
			if testCase.issuer == "" {
				if !reflect.DeepEqual(configured, original) {
					t.Fatal("absent input changed fixture arguments")
				}
				return
			}
			for _, spec := range configured {
				args := buildCreateArgs(cfg, namedSpec{ClusterSpec: spec, name: "fixture-" + spec.Variant})
				for _, argument := range []string{"--oidc-issuer-url=" + testCase.issuer, "--sa-token-issuer-private-key-path=" + testCase.path, spec.CreateArgs[0]} {
					if !slices.Contains(args, argument) {
						t.Fatalf("actual create arguments for %s lost shared input", spec.Variant)
					}
				}
			}
		})
	}
}

func TestValidateSharedOIDCFixture(t *testing.T) {
	path, key := sharedOIDCTestKey(t)
	for _, testCase := range []struct {
		name                                                    string
		change                                                  func(*hyperv1.HostedCluster, *corev1.Secret)
		absent, omitSecret, duplicateSecret, noCluster, invalid bool
	}{
		{name: "When actual rendered issuer and key match, it should accept the fixture"},
		{name: "When shared input is absent, it should preserve legacy rendering", absent: true, omitSecret: true},
		{name: "When rendered issuer differs, it should reject before applying", invalid: true, change: func(cluster *hyperv1.HostedCluster, _ *corev1.Secret) {
			cluster.Spec.IssuerURL = "https://other.example.org"
		}},
		{name: "When signing-key reference is absent, it should reject before applying", invalid: true, change: func(cluster *hyperv1.HostedCluster, _ *corev1.Secret) { cluster.Spec.ServiceAccountSigningKey = nil }},
		{name: "When signing-key contents differ, it should reject without disclosing key material", invalid: true, change: func(_ *hyperv1.HostedCluster, secret *corev1.Secret) { secret.Data["key"] = []byte("wrong key") }},
		{name: "When signing-key namespace differs, it should reject the foreign Secret", invalid: true, change: func(_ *hyperv1.HostedCluster, secret *corev1.Secret) { secret.Namespace = "other" }},
		{name: "When signing-key Secret is missing, it should reject before applying", invalid: true, omitSecret: true},
		{name: "When signing-key Secret is duplicated, it should reject ambiguous rendering", invalid: true, duplicateSecret: true},
		{name: "When HostedCluster is absent, it should reject incomplete rendering", invalid: true, noCluster: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := envConfig{sharedOIDCIssuer: "https://oidc.example.org/suite", sharedOIDCKeyFile: path}
			cluster := &hyperv1.HostedCluster{TypeMeta: metav1.TypeMeta{Kind: "HostedCluster", APIVersion: hyperv1.GroupVersion.String()}, ObjectMeta: metav1.ObjectMeta{Name: "fixture", Namespace: "clusters"}, Spec: hyperv1.HostedClusterSpec{IssuerURL: cfg.sharedOIDCIssuer, ServiceAccountSigningKey: &corev1.LocalObjectReference{Name: "signing-key"}}}
			secret := &corev1.Secret{TypeMeta: metav1.TypeMeta{Kind: "Secret", APIVersion: "v1"}, ObjectMeta: metav1.ObjectMeta{Name: "signing-key", Namespace: "clusters"}, Data: map[string][]byte{"key": key}}
			if testCase.absent {
				cfg.sharedOIDCIssuer, cfg.sharedOIDCKeyFile = "", ""
			}
			if testCase.change != nil {
				testCase.change(cluster, secret)
			}
			var objects []*unstructured.Unstructured
			for _, object := range []runtime.Object{cluster, secret} {
				if object == cluster && testCase.noCluster || object == secret && testCase.omitSecret {
					continue
				}
				data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(object)
				if err != nil {
					t.Fatal(err)
				}
				objects = append(objects, &unstructured.Unstructured{Object: data})
			}
			if testCase.duplicateSecret {
				objects = append(objects, objects[len(objects)-1].DeepCopy())
			}
			if !testCase.invalid {
				var rendered bytes.Buffer
				for _, object := range objects {
					if err := json.NewEncoder(&rendered).Encode(object.Object); err != nil {
						t.Fatal(err)
					}
				}
				decoded, err := readAWSFixture(&rendered, namedSpec{name: "fixture"}, semver.MustParse("4.21.0"))
				if err != nil {
					t.Fatal(err)
				}
				objects = decoded
			}
			if err := validateSharedOIDCFixture(cfg, objects); (err != nil) != testCase.invalid {
				t.Fatalf("unexpected rendered fixture validation: %v", err)
			}
		})
	}
}

func TestRun(t *testing.T) {
	t.Run("When shared OIDC input is incomplete, it should fail before creating a manifest or API client", func(t *testing.T) {
		cfg := envConfig{platform: &lifecycle.AWSPlatformConfig{}, sharedDir: t.TempDir(), sharedOIDCIssuer: "https://oidc.example.org/suite"}
		if err := run(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "E2E_AWS_SHARED_OIDC") {
			t.Fatalf("did not reject incomplete shared issuer input: %v", err)
		}
		if _, err := os.Stat(filepath.Join(cfg.sharedDir, lifecycle.ManifestFileName)); !os.IsNotExist(err) {
			t.Fatalf("invalid configuration created manifest: %v", err)
		}
	})
	t.Run("When owned shared OIDC state remains, it should not overwrite the prior manifest or key", func(t *testing.T) {
		cfg := envConfig{platform: &lifecycle.AWSPlatformConfig{}, sharedDir: t.TempDir()}
		for _, file := range []string{lifecycle.ManifestFileName, lifecycle.SharedOIDCStateFileName} {
			if err := os.WriteFile(filepath.Join(cfg.sharedDir, file), []byte("retained"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := run(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "ownership state already exists") {
			t.Fatalf("reused partial suite state: %v", err)
		}
		for _, file := range []string{lifecycle.ManifestFileName, lifecycle.SharedOIDCStateFileName} {
			data, err := os.ReadFile(filepath.Join(cfg.sharedDir, file))
			if err != nil || string(data) != "retained" {
				t.Fatal("existing ownership state was overwritten")
			}
		}
	})
}

func TestLoadEnvConfig(t *testing.T) {
	for _, configured := range []bool{false, true} {
		name := "When shared inputs are absent, it should preserve empty defaults"
		if configured {
			name = "When shared inputs are present, it should load their exact issuer and key path"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("HYPERSHIFT_PLATFORM", "aws")
			t.Setenv("SHARED_DIR", t.TempDir())
			t.Setenv("ARTIFACT_DIR", t.TempDir())
			t.Setenv("PROW_JOB_ID", "12345")
			t.Setenv("RELEASE_IMAGE_LATEST", "quay.io/openshift-release-dev/ocp-release:4.21.0-x86_64")
			issuer, keyPath := "", ""
			if configured {
				issuer, keyPath = "https://oidc.example.org/suite", filepath.Join(t.TempDir(), "key.pem")
			}
			t.Setenv("E2E_AWS_SHARED_OIDC_ISSUER_URL", issuer)
			t.Setenv("E2E_AWS_SHARED_OIDC_SIGNING_KEY_FILE", keyPath)
			t.Setenv("AWS_OIDC_S3_BUCKET_NAME", "hypershift-ci-oidc")
			t.Setenv("AWS_GUEST_INFRA_CREDENTIALS_FILE", "/fixtures/iam")
			t.Setenv("HYPERSHIFT_AWS_REGION", "us-east-2")
			t.Setenv("AWS_OIDC_S3_CREDENTIALS_FILE", "/fixtures/s3")
			t.Setenv("AWS_OIDC_S3_REGION", "us-west-2")
			cfg := loadEnvConfig()
			if cfg.sharedOIDCIssuer != issuer || cfg.sharedOIDCKeyFile != keyPath {
				t.Fatal("actual environment consumer lost shared issuer inputs")
			}
			wantOptions := lifecycle.SharedOIDCOptions{Bucket: "hypershift-ci-oidc", IAMCredentialsFile: "/fixtures/iam", IAMRegion: "us-east-2", S3CredentialsFile: "/fixtures/s3", S3Region: "us-west-2", ProwJobID: "12345"}
			if !reflect.DeepEqual(cfg.sharedOIDCOptions, wantOptions) {
				t.Fatal("automatic shared OIDC environment routing differs")
			}
		})
	}
}

func TestPrepareScenarioInputs(t *testing.T) {
	for _, testCase := range []struct {
		name, variant, issuer, expectedError string
	}{
		{name: "When ordinary public inputs need no setup, it should retain the selected fixture", variant: "public"},
		{name: "When private-route DNS is absent, it should retain the existing early prerequisite check", variant: "private-route", expectedError: "HYPERSHIFT_EXTERNAL_DNS_DOMAIN"},
		{name: "When shared issuer input is incomplete, it should fail in the same preparation phase", variant: "public", issuer: "https://oidc.example.org", expectedError: "E2E_AWS_SHARED_OIDC"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := envConfig{platform: &lifecycle.AWSPlatformConfig{}, sharedOIDCIssuer: testCase.issuer}
			specs := []lifecycle.ClusterSpec{{Variant: testCase.variant, CreateArgs: []string{"--region=us-east-1"}, DestroyArgs: []string{"--region=us-east-1"}}}
			got, err := prepareScenarioInputs(cfg, specs)
			if testCase.expectedError != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.expectedError) {
					t.Fatalf("unexpected preflight error: %v", err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, specs) {
				t.Fatalf("lost selected scenario: %v", err)
			}
		})
	}
}

func TestPrepareAutomaticSharedOIDC(t *testing.T) {
	for _, testCase := range []struct {
		name                           string
		external, disabled, setupFails bool
	}{
		{name: "When AWS has no external input, it should configure every selected fixture from automatic setup"},
		{name: "When an external provider is supplied, it should not acquire or delete its resources", external: true},
		{name: "When the platform is not AWS, it should leave shared AWS setup disabled", disabled: true},
		{name: "When automatic setup fails, it should preserve the originating error", setupFails: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			keyFile, _ := sharedOIDCTestKey(t)
			cfg := envConfig{platform: &lifecycle.AWSPlatformConfig{}, sharedDir: t.TempDir(), sharedOIDCOptions: lifecycle.SharedOIDCOptions{Bucket: "hypershift-ci-oidc", IAMRegion: "us-east-1", IAMCredentialsFile: "/fixtures/iam", S3Region: "us-west-2", S3CredentialsFile: "/fixtures/s3", ProwJobID: "12345"}}
			if testCase.external {
				cfg.sharedOIDCIssuer, cfg.sharedOIDCKeyFile = "https://external.example.org/suite", keyFile
			}
			if testCase.disabled {
				cfg.platform = lifecycle.NewAzurePlatformConfig(cfg.sharedDir)
			}
			specs := []lifecycle.ClusterSpec{{Variant: "public", CreateArgs: []string{"--additional-tags=expirationDate=2026-10-22", "--additional-tags", "owner=ci"}}, {Variant: "private"}}
			original := append([]lifecycle.ClusterSpec(nil), specs...)
			called := false
			failure := errors.New("automatic setup failed")
			configured, got, err := prepareAutomaticSharedOIDC(t.Context(), cfg, specs, func(ctx context.Context, directory string, options lifecycle.SharedOIDCOptions) (lifecycle.SharedOIDCInputs, error) {
				called = true
				want := cfg.sharedOIDCOptions
				want.AdditionalTags = []string{"expirationDate=2026-10-22", "owner=ci"}
				if ctx != t.Context() || directory != cfg.sharedDir || !reflect.DeepEqual(options, want) {
					t.Fatal("automatic setup lost configuration routing or suite tags")
				}
				if testCase.setupFails {
					return lifecycle.SharedOIDCInputs{}, failure
				}
				return lifecycle.SharedOIDCInputs{IssuerURL: "https://hypershift-ci-oidc.s3.us-west-2.amazonaws.com/e2e-oidc-provider-independent", SigningKeyFile: keyFile}, nil
			})
			if testCase.setupFails {
				if !errors.Is(err, failure) {
					t.Fatalf("lost setup error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if testCase.external || testCase.disabled {
				if called || !reflect.DeepEqual(got, specs) {
					t.Fatal("external or non-AWS setup acquired ownership")
				}
				return
			}
			if !called || configured.sharedOIDCKeyFile != keyFile || len(got) != len(specs) {
				t.Fatal("automatic setup was bypassed")
			}
			for index, spec := range got {
				if !slices.Contains(spec.CreateArgs, "--oidc-issuer-url="+configured.sharedOIDCIssuer) || !slices.Contains(spec.CreateArgs, "--sa-token-issuer-private-key-path="+keyFile) || !reflect.DeepEqual(specs[index], original[index]) {
					t.Fatal("fixture transport missing or original spec mutated")
				}
			}
		})
	}
}
