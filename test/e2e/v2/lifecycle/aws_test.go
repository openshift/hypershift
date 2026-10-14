//go:build e2ev2

package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/openshift/hypershift/test/e2e/v2/util/mirrorregistry"

	imagev1 "github.com/openshift/api/image/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// testCLIImage is a digest-pinned openshift/cli image reference used in lookup and setup tests.
const testCLIImage = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:0a9d58f9d8dd969c7bd107319a2498a674fe4e6477185a44558f168d994b39a5"

func TestNewPlatformConfig(t *testing.T) {
	for _, tt := range []struct {
		name      string
		env       string
		want      bool
		wantError bool
	}{
		{name: "When mirroring is unset, it should remain disabled"},
		{name: "When mirroring is false, it should remain disabled", env: "false"},
		{name: "When mirroring is true, it should enable the AWS option", env: "true", want: true},
		{name: "When mirroring is True, it should enable the AWS option", env: "True", want: true},
		{name: "When mirroring is one, it should enable the AWS option", env: "1", want: true},
		{name: "When mirroring is False, it should remain disabled", env: "False"},
		{name: "When mirroring is zero, it should remain disabled", env: "0"},
		{name: "When mirroring is yes, it should return a descriptive error", env: "yes", wantError: true},
		{name: "When mirroring is invalid, it should return a descriptive error", env: "enabled", wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HYPERSHIFT_E2E_IMAGE_MIRRORING", tt.env)
			cfg, err := NewPlatformConfig("aws", t.TempDir())
			if tt.wantError {
				if err == nil || !strings.Contains(err.Error(), "HYPERSHIFT_E2E_IMAGE_MIRRORING") {
					t.Fatalf("got %v, want error identifying HYPERSHIFT_E2E_IMAGE_MIRRORING", err)
				}
				if cfg != nil {
					t.Fatal("invalid mirroring value returned a platform config")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.(*AWSPlatformConfig).imageMirroring != tt.want {
				t.Fatalf("imageMirroring = %v, want %v", cfg.(*AWSPlatformConfig).imageMirroring, tt.want)
			}
		})
	}
}

func TestNewAWSPlatformConfig(t *testing.T) {
	for _, tt := range []struct {
		name    string
		enabled bool
	}{
		{name: "When mirroring is disabled, it should retain the disabled option"},
		{name: "When mirroring is enabled, it should retain the enabled option", enabled: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := NewAWSPlatformConfig(AWSPlatformOptions{Region: "us-east-1", Zones: "us-east-1a,us-east-1b", ImageMirroring: tt.enabled}, dir)
			if cfg.imageMirroring != tt.enabled || cfg.sharedDir != dir || cfg.newMirrorRegistry == nil {
				t.Fatalf("unexpected config: %+v", cfg)
			}
		})
	}
}

func TestAWSPlatformConfigClusterSpecs(t *testing.T) {
	for _, tt := range []struct {
		name    string
		enabled bool
	}{
		{name: "When mirroring is disabled, it should preserve the existing variants"},
		{name: "When mirroring is enabled, it should add only the opt-in variant", enabled: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("EXTRA_ARGS", "--node-pool-replicas=1 --instance-type=m5.large")
			dir := t.TempDir()
			cfg := NewAWSPlatformConfig(AWSPlatformOptions{ImageMirroring: tt.enabled}, dir)
			specs := cfg.ClusterSpecs("current", "previous")
			wantCount := 6
			if tt.enabled {
				wantCount++
			}
			if len(specs) != wantCount {
				t.Fatalf("got %d specs, want %d", len(specs), wantCount)
			}
			if tt.enabled {
				want := ClusterSpec{Variant: "image-mirroring", ExtraArgs: []string{
					"--node-pool-replicas=1", "--instance-type=m5.large",
					"--additional-trust-bundle=" + filepath.Join(dir, "mirror-registry-ca.pem"),
					"--image-content-sources=" + filepath.Join(dir, "mirror-registry-ics.yaml"),
				}}
				if !reflect.DeepEqual(specs[6], want) {
					t.Fatalf("got %+v, want %+v", specs[6], want)
				}
				baseline := NewAWSPlatformConfig(AWSPlatformOptions{}, dir)
				if !reflect.DeepEqual(specs[:6], baseline.ClusterSpecs("current", "previous")) {
					t.Fatal("existing variants changed")
				}
				if !reflect.DeepEqual(cfg.DefaultTestPlan(), baseline.DefaultTestPlan()) {
					t.Fatal("default AWS plan changed")
				}
			}
			if slices.Contains(cfg.TestMatrix().Variants(), "image-mirroring") {
				t.Fatal("default matrix selects mirroring")
			}
			plan := TestPlan{TestMatrix: TestMatrix{Parallel: []TestGroup{{Name: "image-mirroring", Variant: "image-mirroring"}}}}
			if err := plan.Validate(specs); (err == nil) != tt.enabled {
				t.Fatalf("custom plan validation: %v", err)
			}
		})
	}
}

type stubMirrorRegistry struct {
	setup func(context.Context, mirrorregistry.MirrorRequest) (*mirrorregistry.Result, error)
}

func (s stubMirrorRegistry) Setup(ctx context.Context, req mirrorregistry.MirrorRequest) (*mirrorregistry.Result, error) {
	return s.setup(ctx, req)
}

func TestAWSPlatformConfigPreCreate(t *testing.T) {
	for _, tt := range []struct {
		name          string
		disabled      bool
		emptyDir      bool
		missingStream bool
		newError      bool
		setupError    bool
		writeError    bool
		wantError     string
	}{
		{name: "When mirroring is disabled, it should do nothing", disabled: true},
		{name: "When mirroring is enabled, it should set up the fixture and write artifacts"},
		{name: "When the shared directory is empty, it should fail before setup", emptyDir: true, wantError: "requires a shared directory"},
		{name: "When the CLI ImageStream is missing, it should return the lookup error", missingStream: true, wantError: "read ImageStream"},
		{name: "When registry initialization fails, it should return the error", newError: true, wantError: "initialize mirror registry"},
		{name: "When registry setup fails, it should return the error", setupError: true, wantError: "set up mirror registry"},
		{name: "When artifact writing fails, it should return the error", writeError: true, wantError: "write mirror registry file"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.emptyDir {
				dir = ""
			}
			if tt.writeError {
				dir = filepath.Join(dir, "missing")
			}
			cfg := NewAWSPlatformConfig(AWSPlatformOptions{ImageMirroring: !tt.disabled}, dir)
			factoryCalled, setupCalled := false, false
			cfg.newMirrorRegistry = func(_ crclient.Client, opts mirrorregistry.Options) (mirrorRegistry, error) {
				factoryCalled = true
				if opts.CopierImage != testCLIImage {
					t.Fatalf("copier image = %s", opts.CopierImage)
				}
				if tt.newError {
					return nil, errors.New("initialization failed")
				}
				return stubMirrorRegistry{setup: func(ctx context.Context, req mirrorregistry.MirrorRequest) (*mirrorregistry.Result, error) {
					setupCalled = true
					if _, ok := ctx.Deadline(); !ok {
						t.Fatal("setup has no deadline")
					}
					want := mirrorregistry.MirrorRequest{Images: []mirrorregistry.ImageMapping{
						{From: mirrorregistry.DefaultRegistryImage},
						{From: mirrorregistry.DefaultRegistryImage, SourceRepository: "mirror-source.hypershift-e2e.invalid/libpod/registry"},
					}}
					if !reflect.DeepEqual(req, want) {
						t.Fatalf("request = %+v, want %+v", req, want)
					}
					if tt.setupError {
						return nil, errors.New("copy failed")
					}
					return &mirrorregistry.Result{Host: "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com", CABundlePEM: []byte("test-ca")}, nil
				}}, nil
			}
			scheme := runtime.NewScheme()
			if err := imagev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if !tt.missingStream {
				builder.WithObjects(cliStream(testCLIImage))
			}
			err := cfg.setupImageMirroring(t.Context(), builder.Build())
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("got %v, want error containing %q", err, tt.wantError)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			wantFactory := !tt.disabled && !tt.emptyDir && !tt.missingStream
			if factoryCalled != wantFactory || setupCalled != (wantFactory && !tt.newError) {
				t.Fatalf("factory called=%v, setup called=%v", factoryCalled, setupCalled)
			}
			if tt.disabled {
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 0 {
					t.Fatalf("disabled hook wrote artifacts: %v, %v", entries, err)
				}
			} else if tt.wantError == "" {
				for _, name := range []string{"mirror-registry-ca.pem", "mirror-registry-ics.yaml", "mirror-registry.json"} {
					if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

func cliStream(ref string) *imagev1.ImageStream {
	return &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{Namespace: "openshift", Name: "cli"},
		Status:     imagev1.ImageStreamStatus{Tags: []imagev1.NamedTagEventList{{Tag: "latest", Items: []imagev1.TagEvent{{DockerImageReference: ref}}}}},
	}
}
