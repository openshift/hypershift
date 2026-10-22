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

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/util/mirrorregistry"

	imagev1 "github.com/openshift/api/image/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAWSPlatformConfigClusterSpecs(t *testing.T) {
	t.Run("When AWS cluster specs are built, it should omit Azure dump credentials for every variant", func(t *testing.T) {
		g := NewWithT(t)
		specs := (&AWSPlatformConfig{}).ClusterSpecs(t.Context(), "release-image", "n1-image")
		g.Expect(specs).NotTo(BeEmpty())
		for _, spec := range specs {
			g.Expect(spec.DumpArgs).To(BeEmpty(), "variant %s", spec.Variant)
		}
	})
}

// testCLIImage is a digest-pinned openshift/cli image reference used in lookup and setup tests.
const testCLIImage = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:0a9d58f9d8dd969c7bd107319a2498a674fe4e6477185a44558f168d994b39a5"

func TestNewAWSPlatformConfig(t *testing.T) {
	t.Run("When the AWS config is built, it should retain the shared directory and the mirror registry factory", func(t *testing.T) {
		dir := t.TempDir()
		cfg := NewAWSPlatformConfig(AWSPlatformOptions{Region: "us-east-1", Zones: "us-east-1a,us-east-1b"}, dir)
		if cfg.sharedDir != dir || cfg.newMirrorRegistry == nil {
			t.Fatalf("unexpected config: %+v", cfg)
		}
	})
}

func TestAWSPlatformConfigImageMirroringClusterSpecs(t *testing.T) {
	t.Run("When AWS cluster specs are built, it should declare a single-replica image-mirroring variant consuming the fixture artifacts", func(t *testing.T) {
		t.Setenv("EXTRA_ARGS", "--instance-type=m5.large")
		dir := t.TempDir()
		cfg := NewAWSPlatformConfig(AWSPlatformOptions{}, dir)
		specs := cfg.ClusterSpecs(t.Context(), "current", "previous")
		var mirroring, autoscaling *ClusterSpec
		for i := range specs {
			switch specs[i].Variant {
			case "image-mirroring":
				if mirroring != nil {
					t.Fatal("duplicate image-mirroring variant")
				}
				mirroring = &specs[i]
			case "autoscaling":
				autoscaling = &specs[i]
			}
		}
		if mirroring == nil || autoscaling == nil {
			t.Fatalf("missing image-mirroring or autoscaling variant in %+v", specs)
		}
		// The autoscaling variant uses only the common args plus EXTRA_ARGS.
		commonAndExtra := autoscaling.CreateArgs
		if !slices.Contains(commonAndExtra, "--root-volume-type=gp3") || !slices.Contains(commonAndExtra, "--instance-type=m5.large") {
			t.Fatalf("unexpected common args: %v", commonAndExtra)
		}
		one := 1
		want := ClusterSpec{
			Variant:                 "image-mirroring",
			DestroyArgs:             autoscaling.DestroyArgs,
			InitialNodePoolReplicas: &one,
			CreateArgs: slices.Concat(commonAndExtra, []string{
				"--additional-trust-bundle=" + filepath.Join(dir, "mirror-registry-ca.pem"),
				"--image-content-sources=" + filepath.Join(dir, "mirror-registry-ics.yaml"),
			}),
		}
		if !reflect.DeepEqual(*mirroring, want) {
			t.Fatalf("got %+v, want %+v", *mirroring, want)
		}
	})

	t.Run("When the default AWS plan is built, it should validate and run the image-mirroring tests as a sequential lane ending with the removal", func(t *testing.T) {
		cfg := NewAWSPlatformConfig(AWSPlatformOptions{}, t.TempDir())
		plan := cfg.DefaultTestPlan()
		if err := plan.Validate(cfg.ClusterSpecs(t.Context(), "current", "previous")); err != nil {
			t.Fatalf("default AWS plan is invalid: %v", err)
		}
		for _, group := range plan.TestMatrix.Parallel {
			if group.Variant == "image-mirroring" {
				t.Fatalf("image-mirroring variant must not run in a parallel group: %+v", group)
			}
		}
		var lanes []SequentialGroup
		for _, group := range plan.TestMatrix.Sequential {
			if slices.ContainsFunc(group.Steps, func(step TestGroup) bool { return step.Variant == "image-mirroring" }) {
				lanes = append(lanes, group)
			}
		}
		if len(lanes) != 1 {
			t.Fatalf("expected exactly one sequential group using the image-mirroring variant, got %+v", lanes)
		}
		want := SequentialGroup{
			Name: "image-mirroring",
			Steps: []TestGroup{
				{Name: "image-mirroring", Variant: "image-mirroring", LabelFilter: "image-mirroring"},
				// The removal mutates the HC without restoring it, so it must be last.
				{Name: "image-mirroring-removal", Variant: "image-mirroring", LabelFilter: "image-mirroring-removal"},
			},
		}
		if !reflect.DeepEqual(lanes[0], want) {
			t.Fatalf("got %+v, want %+v", lanes[0], want)
		}
	})
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
		emptyDir      bool
		missingStream bool
		newError      bool
		setupError    bool
		emptySources  bool
		writeError    bool
		wantError     string
	}{
		{name: "When the shared directory is set, it should set up the fixture and write artifacts"},
		{name: "When the shared directory is empty, it should fail before setup", emptyDir: true, wantError: "requires a shared directory"},
		{name: "When the CLI ImageStream is missing, it should return the lookup error", missingStream: true, wantError: "read ImageStream"},
		{name: "When registry initialization fails, it should return the error", newError: true, wantError: "initialize mirror registry"},
		{name: "When registry setup fails, it should return the error", setupError: true, wantError: "set up mirror registry"},
		{name: "When setup returns no image content sources, it should return the validation error", emptySources: true, wantError: "no image content sources"},
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
			cfg := NewAWSPlatformConfig(AWSPlatformOptions{}, dir)
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
					host := "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com"
					result := &mirrorregistry.Result{
						Host:                host,
						CABundlePEM:         []byte("test-ca"),
						ImageContentSources: []hyperv1.ImageContentSource{{Source: "quay.io/libpod/registry", Mirrors: []string{host + "/quay.io/libpod/registry"}}},
					}
					if tt.emptySources {
						result.ImageContentSources = nil
					}
					return result, nil
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
			wantFactory := !tt.emptyDir && !tt.missingStream
			if factoryCalled != wantFactory || setupCalled != (wantFactory && !tt.newError) {
				t.Fatalf("factory called=%v, setup called=%v", factoryCalled, setupCalled)
			}
			if tt.wantError == "" {
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
