//go:build e2ev2

package lifecycle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/util/mirrorregistry"

	imagev1 "github.com/openshift/api/image/v1"

	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"
)

func TestCliImage(t *testing.T) {
	const ref = testCLIImage
	for _, tt := range []struct {
		name      string
		stream    *imagev1.ImageStream
		wantError bool
	}{
		{name: "When latest has a resolved digest, it should return the imported reference", stream: cliStream(ref)},
		{name: "When the stream does not exist, it should return an error", wantError: true},
		{name: "When latest has no reference, it should return an error", stream: cliStream(""), wantError: true},
		{name: "When latest has only a mutable tag, it should reject it", stream: cliStream("quay.io/openshift-release-dev/ocp-v4.0-art-dev:latest"), wantError: true},
		{name: "When latest has no history, it should return an error", stream: func() *imagev1.ImageStream { s := cliStream(ref); s.Status.Tags[0].Items = nil; return s }(), wantError: true},
		{name: "When only another tag is resolved, it should return an error", stream: func() *imagev1.ImageStream { s := cliStream(ref); s.Status.Tags[0].Tag = "previous"; return s }(), wantError: true},
		{name: "When latest has multiple imports, it should use the newest", stream: func() *imagev1.ImageStream {
			s := cliStream(ref)
			s.Status.Tags[0].Items = append(s.Status.Tags[0].Items, imagev1.TagEvent{DockerImageReference: "older"})
			return s
		}()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := imagev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.stream != nil {
				builder.WithObjects(tt.stream)
			}
			got, err := cliImage(t.Context(), builder.Build())
			if tt.wantError {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil || got != ref {
				t.Fatalf("got %q, %v; want %q", got, err, ref)
			}
		})
	}
}

func TestWriteMirrorRegistryResult(t *testing.T) {
	for _, tt := range []struct {
		name        string
		blockedFile string
	}{
		{name: "When setup returns a result, it should write the CA and mappings and JSON without CA"},
		{name: "When the CA path cannot be written, it should return the file error", blockedFile: "mirror-registry-ca.pem"},
		{name: "When the mappings path cannot be written, it should return the file error", blockedFile: "mirror-registry-ics.yaml"},
		{name: "When the JSON path cannot be written, it should return the file error", blockedFile: "mirror-registry.json"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.blockedFile != "" {
				if err := os.Mkdir(filepath.Join(dir, tt.blockedFile), 0700); err != nil {
					t.Fatal(err)
				}
			}
			host := "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com"
			result := &mirrorregistry.Result{
				Host:                host,
				InternalHost:        "mirror-registry.test-mirror-registry.svc:5000",
				CABundlePEM:         []byte("-----BEGIN CERTIFICATE-----\ntest CA\n-----END CERTIFICATE-----\n"),
				ImageContentSources: []hyperv1.ImageContentSource{{Source: "quay.io/libpod/registry", Mirrors: []string{host + "/quay.io/libpod/registry"}}},
				Images:              []mirrorregistry.MirroredImage{{From: mirrorregistry.DefaultRegistryImage, SourceRef: mirrorregistry.DefaultRegistryImage, MirrorRef: host + "/" + mirrorregistry.DefaultRegistryImage}},
			}
			err := writeMirrorRegistryResult(dir, result)
			if tt.blockedFile != "" {
				if err == nil || !strings.Contains(err.Error(), tt.blockedFile) {
					t.Fatalf("got %v, want error for %s", err, tt.blockedFile)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ca, err := os.ReadFile(filepath.Join(dir, "mirror-registry-ca.pem"))
			if err != nil || string(ca) != string(result.CABundlePEM) {
				t.Fatalf("CA = %q, %v", ca, err)
			}
			ics, err := os.ReadFile(filepath.Join(dir, "mirror-registry-ics.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var sources []hyperv1.ImageContentSource
			if err := yaml.Unmarshal(ics, &sources); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(sources, result.ImageContentSources) {
				t.Fatalf("mappings = %+v", sources)
			}
			data, err := os.ReadFile(filepath.Join(dir, "mirror-registry.json"))
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if _, ok := fields["CABundlePEM"]; ok {
				t.Fatal("CA field must be absent from JSON")
			}
			var got mirrorregistry.Result
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			want := *result
			want.CABundlePEM = nil
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("JSON result = %+v, want %+v", got, want)
			}
			if len(result.CABundlePEM) == 0 {
				t.Fatal("writer mutated the input CA")
			}
		})
	}
}
