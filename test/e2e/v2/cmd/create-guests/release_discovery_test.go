//go:build e2ev2

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const discoveredReleaseImage = "quay.io/openshift-release-dev/ocp-release:4.21.10-multi"

type releaseDiscoveryTransport func(*http.Request) (*http.Response, error)

func (transport releaseDiscoveryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func defaultReleaseFixture(t *testing.T, versions, response string, requestError error) crclient.WithWatch {
	t.Helper()
	previousClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = previousClient })
	http.DefaultClient = &http.Client{Transport: releaseDiscoveryTransport(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.String() != "https://multi.ocp.releases.ci.openshift.org/api/v1/releasestream/4-stable-multi/tags" {
			t.Fatalf("unexpected release lookup request: %s %s", request.Method, request.URL)
		}
		if requestError != nil {
			return nil, requestError
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
	})}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "supported-versions", Namespace: "hypershift", Labels: map[string]string{"hypershift.openshift.io/supported-versions": "true"}},
		Data:       map[string]string{"supported-versions": versions, "server-version": "a0aaf80"},
	}).Build()
}

func releaseDiscoveryResponse() string {
	return `{"tags":[{"name":"4.22.1","pullSpec":"quay.io/openshift-release-dev/ocp-release:4.22.1-multi"},{"name":"4.21.11-rc.1","pullSpec":"quay.io/openshift-release-dev/ocp-release:4.21.11-rc.1-multi"},{"name":"4.21.10","pullSpec":"` + discoveredReleaseImage + `"},{"name":"4.21.9","pullSpec":"quay.io/openshift-release-dev/ocp-release:4.21.9-multi"}]}`
}

func TestResolveReleaseImages(t *testing.T) {
	for _, test := range []struct {
		name, latest, previous string
	}{
		{name: "When latest is configured, it should avoid management lookup and default previous", latest: discoveredReleaseImage},
		{name: "When both images are configured, it should preserve their exact values without lookup", latest: discoveredReleaseImage, previous: "quay.io/openshift-release-dev/ocp-release:4.20.20-multi"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := resolveReleaseImages(t.Context(), envConfig{releaseImage: test.latest, n1Image: test.previous}, func() (crclient.WithWatch, error) {
				t.Fatal("configured release unexpectedly contacted management")
				return nil, nil
			})
			wantPrevious := test.previous
			if wantPrevious == "" {
				wantPrevious = test.latest
			}
			if err != nil || cfg.releaseImage != test.latest || cfg.n1Image != wantPrevious {
				t.Fatalf("configured images changed: latest=%s previous=%s err=%v", cfg.releaseImage, cfg.n1Image, err)
			}
		})
	}
	for _, previous := range []string{"", "quay.io/openshift-release-dev/ocp-release:4.20.20-multi"} {
		name := "When latest is absent, it should discover a supported non-RC pull spec and default previous"
		if previous != "" {
			name = "When latest is absent and previous is configured, it should discover latest and preserve previous"
		}
		t.Run(name, func(t *testing.T) {
			client := defaultReleaseFixture(t, `{"versions":["4.21"]}`, releaseDiscoveryResponse(), nil)
			cfg, err := resolveReleaseImages(t.Context(), envConfig{n1Image: previous}, func() (crclient.WithWatch, error) { return client, nil })
			wantPrevious := previous
			if wantPrevious == "" {
				wantPrevious = discoveredReleaseImage
			}
			if err != nil || cfg.releaseImage != discoveredReleaseImage || cfg.n1Image != wantPrevious {
				t.Fatalf("default discovery differs: latest=%s previous=%s err=%v", cfg.releaseImage, cfg.n1Image, err)
			}
		})
	}
}

func TestRunWithManagementClientReleaseErrors(t *testing.T) {
	lookupError := errors.New("release lookup unavailable")
	for _, test := range []struct {
		name, versions, response, want    string
		clientError, listError, httpError error
	}{
		{name: "When the management client fails, it should fail before plan lookup or provisioning", clientError: lookupError, want: "creating management cluster client for default release lookup"},
		{name: "When management listing fails, it should fail before plan lookup or provisioning", listError: lookupError, want: "failed to list ConfigMaps"},
		{name: "When management versions are malformed, it should fail before provisioning", versions: "{", want: "failed to parse supported versions"},
		{name: "When management has no supported versions, it should fail before provisioning", versions: `{"versions":[]}`, want: "no supported OCP versions"},
		{name: "When the release service fails, it should preserve the lookup error before provisioning", versions: `{"versions":["4.21"]}`, httpError: lookupError, want: "release lookup unavailable"},
		{name: "When the release response is malformed, it should fail before provisioning", versions: `{"versions":["4.21"]}`, response: "{", want: "failed to parse response"},
		{name: "When the release response has no pull spec, it should fail before provisioning", versions: `{"versions":["4.21"]}`, response: `{"tags":[{"name":"4.21.10"}]}`, want: "empty pull spec"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := defaultReleaseFixture(t, test.versions, test.response, test.httpError)
			if test.listError != nil {
				client = interceptor.NewClient(client, interceptor.Funcs{List: func(context.Context, crclient.WithWatch, crclient.ObjectList, ...crclient.ListOption) error {
					return test.listError
				}})
			}
			cfg := envConfig{platform: &lifecycle.AWSPlatformConfig{}, sharedDir: t.TempDir(), testPlanPath: filepath.Join(t.TempDir(), "must-not-read.yaml")}
			err := runWithManagementClient(t.Context(), cfg, func() (crclient.WithWatch, error) { return client, test.clientError })
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("unexpected lookup consumer error: %v", err)
			}
			if _, err := os.Stat(filepath.Join(cfg.sharedDir, lifecycle.ManifestFileName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed lookup wrote a manifest: %v", err)
			}
		})
	}
}

func TestPrepareCreatePlanReleaseImages(t *testing.T) {
	for _, test := range []struct {
		name, latest, previous string
		cpo                    bool
	}{
		{name: "When a default AWS plan discovers latest, it should render that image and default previous"},
		{name: "When a default AWS plan has configured previous, it should retain that creation input", previous: "quay.io/openshift-release-dev/ocp-release:4.20.20-multi"},
		{name: "When the CPO plan lacks generic images, it should use the production mapped pair without lookup", cpo: true},
		{name: "When the CPO plan has supplied latest, it should retain the production mapped pair without lookup", cpo: true, latest: discoveredReleaseImage},
		{name: "When the CPO plan has both generic images supplied, it should preserve them without lookup", cpo: true, latest: discoveredReleaseImage, previous: "quay.io/openshift-release-dev/ocp-release:4.20.20-multi"},
		{name: "When the CPO plan has only previous supplied, it should preserve previous and map latest without lookup", cpo: true, previous: "quay.io/openshift-release-dev/ocp-release:4.20.20-multi"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("TEST_CPO_OVERRIDE", "")
			client := defaultReleaseFixture(t, `{"versions":["4.21"]}`, releaseDiscoveryResponse(), nil)
			cfg := envConfig{platform: lifecycle.NewAWSPlatformConfig(lifecycle.AWSPlatformOptions{Region: "us-east-1", Zones: "us-east-1a"}, t.TempDir()), releaseImage: test.latest, n1Image: test.previous, sharedDir: t.TempDir()}
			if test.cpo {
				cfg.testPlanPath = filepath.Join("..", "..", "..", "..", "..", "contrib", "aws-e2e-v2-cpo-overrides.yaml")
			}
			resolved, specs, cpo, err := prepareCreatePlan(t.Context(), cfg, func() (crclient.WithWatch, error) {
				if test.cpo || test.latest != "" {
					t.Fatal("mapped or configured CPO input unexpectedly performed discovery")
				}
				return client, nil
			})
			wantLatest := discoveredReleaseImage
			wantPrevious := test.previous
			if test.cpo && test.latest == "" {
				mappedLatest, mappedPrevious := controlplaneoperatoroverrides.LatestOverrideTestReleases("aws")
				wantLatest = mappedLatest
				if wantPrevious == "" {
					wantPrevious = mappedPrevious
				}
			}
			if wantPrevious == "" {
				wantPrevious = wantLatest
			}
			if err != nil || cpo != test.cpo || len(specs) == 0 || resolved.releaseImage != wantLatest || resolved.n1Image != wantPrevious {
				t.Fatalf("unexpected rendered plan: cpo=%v specs=%v err=%v", cpo, specs, err)
			}
			assertPlanReleaseArgs(t, resolved, specs, test.cpo)
		})
	}
}

func TestPrepareCreatePlanCPOGuards(t *testing.T) {
	for _, test := range []struct {
		name, want string
		mutate     func(*lifecycle.TestPlan)
	}{
		{name: "When a CPO plan contains an ordinary fixture, it should reject the mixed selection without lookup", want: "exactly the two dedicated AWS override fixtures", mutate: func(plan *lifecycle.TestPlan) {
			plan.TestMatrix.Sequential[0].Steps[0].Variant = "public"
		}},
		{name: "When only one mapped CPO lane remains, it should reject the incomplete selection without lookup", want: "exactly the two dedicated AWS override fixtures", mutate: func(plan *lifecycle.TestPlan) {
			plan.TestMatrix.Sequential = plan.TestMatrix.Sequential[:1]
		}},
		{name: "When the CPO assertion is disabled, it should reject the malformed CPO shape without lookup", want: "without skips", mutate: func(plan *lifecycle.TestPlan) {
			plan.TestMatrix.Sequential[0].Steps[2].Skip = ".*"
		}},
		{name: "When a CPO plan references an unknown fixture, it should reject the invalid plan without lookup", want: "not found", mutate: func(plan *lifecycle.TestPlan) {
			plan.TestMatrix.Sequential[0].Steps[0].Variant = "unknown-fixture"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := cpoOverrideTestPlan(t)
			test.mutate(plan)
			data, err := json.Marshal(plan)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "cpo-plan.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			_, _, _, err = prepareCreatePlan(t.Context(), envConfig{platform: &lifecycle.AWSPlatformConfig{}, testPlanPath: path}, func() (crclient.WithWatch, error) {
				t.Fatal("invalid CPO plan reached management discovery")
				return nil, nil
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("unexpected CPO plan rejection: %v", err)
			}
		})
	}
	t.Run("When an ordinary plan is malformed and latest is supplied, it should retain the plan error without lookup", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ordinary-plan.yaml")
		if err := os.WriteFile(path, []byte("testMatrix: ["), 0600); err != nil {
			t.Fatal(err)
		}
		_, _, _, err := prepareCreatePlan(t.Context(), envConfig{platform: &lifecycle.AWSPlatformConfig{}, testPlanPath: path, releaseImage: discoveredReleaseImage}, func() (crclient.WithWatch, error) {
			t.Fatal("configured input reached management discovery")
			return nil, nil
		})
		if err == nil || !strings.Contains(err.Error(), "resolving test plan") {
			t.Fatalf("unexpected malformed plan error: %v", err)
		}
	})
}

func assertPlanReleaseArgs(t *testing.T, cfg envConfig, specs []lifecycle.ClusterSpec, cpo bool) {
	t.Helper()
	latest, previous := controlplaneoperatoroverrides.LatestOverrideTestReleases("aws")
	for _, spec := range specs {
		want := cfg.releaseImage
		if spec.ReleaseImage != "" {
			want = cfg.n1Image
		}
		if cpo {
			want = latest
			if spec.Variant == "cpo-override-previous" {
				want = previous
			}
			if spec.ReleaseImage != want {
				t.Fatalf("mapped CPO fixture changed: %s image=%s want=%s", spec.Variant, spec.ReleaseImage, want)
			}
		}
		args := buildCreateArgs(cfg, namedSpec{ClusterSpec: spec, name: "release-fixture"})
		if !slices.Contains(args, "--release-image="+want) {
			t.Fatalf("fixture %s lost release image %s: %v", spec.Variant, want, args)
		}
	}
}

func TestRunWithManagementClientReleaseManifest(t *testing.T) {
	for _, previous := range []string{"", "quay.io/openshift-release-dev/ocp-release:4.20.20-multi"} {
		name := "When latest is discovered, it should transport latest and previous fallback before pre-create"
		if previous != "" {
			name = "When previous is configured, it should preserve both manifest and creation inputs"
		}
		t.Run(name, func(t *testing.T) {
			for _, variable := range []string{"RELEASE_IMAGE_LATEST", "E2E_LATEST_RELEASE_IMAGE", "E2E_PREVIOUS_RELEASE_IMAGE", "E2E_N1_RELEASE_IMAGE", "TEST_CPO_OVERRIDE"} {
				t.Setenv(variable, "")
			}
			t.Setenv("OCP_IMAGE_N1", previous)
			client := defaultReleaseFixture(t, `{"versions":["4.21"]}`, releaseDiscoveryResponse(), nil)
			cfg := envConfig{platform: lifecycle.NewAWSPlatformConfig(lifecycle.AWSPlatformOptions{Region: "us-east-1", Zones: "us-east-1a"}, t.TempDir()), sharedDir: t.TempDir(), n1Image: previous}
			preCreateError := errors.New("stop before provisioning")
			calls := 0
			err := runWithManagementClient(t.Context(), cfg, func() (crclient.WithWatch, error) {
				calls++
				if calls == 1 {
					return client, nil
				}
				return nil, preCreateError
			})
			if !errors.Is(err, preCreateError) || calls != 2 {
				t.Fatalf("unexpected pre-create ordering: calls=%d err=%v", calls, err)
			}
			manifest, err := lifecycle.ReadManifest(cfg.sharedDir)
			if err != nil {
				t.Fatal(err)
			}
			wantPrevious := previous
			if wantPrevious == "" {
				wantPrevious = discoveredReleaseImage
			}
			if len(manifest.Clusters) == 0 || manifest.ReleaseImages["E2E_LATEST_RELEASE_IMAGE"] != discoveredReleaseImage || manifest.ReleaseImages["E2E_PREVIOUS_RELEASE_IMAGE"] != wantPrevious {
				t.Fatalf("resolved release transport differs: %+v", manifest)
			}
			if os.Getenv("RELEASE_IMAGE_LATEST") != "" || os.Getenv("OCP_IMAGE_N1") != previous {
				t.Fatal("release discovery mutated global environment")
			}
		})
	}
}
