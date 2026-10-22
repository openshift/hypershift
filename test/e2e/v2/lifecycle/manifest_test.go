//go:build e2ev2

package lifecycle

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestValidateReleaseImages(t *testing.T) {
	for _, test := range []struct {
		name      string
		images    map[string]string
		wantError bool
	}{
		{name: "When an older manifest has no image inputs, it should remain accepted"},
		{name: "When registered image inputs are valid, it should accept tagged and digest references", images: map[string]string{"E2E_N1_RELEASE_IMAGE": "quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64", "E2E_LATEST_RELEASE_IMAGE": "quay.io/openshift-release-dev/ocp-release@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}},
		{name: "When a manifest attempts arbitrary environment injection, it should reject it", images: map[string]string{"KUBECONFIG": "management"}, wantError: true},
		{name: "When an image reference contains shell syntax, it should reject it without evaluation", images: map[string]string{"E2E_N1_RELEASE_IMAGE": "$(echo unwanted)"}, wantError: true},
		{name: "When a recorded image is empty, it should reject it", images: map[string]string{"E2E_N2_RELEASE_IMAGE": ""}, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateReleaseImages(test.images); (err != nil) != test.wantError {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
}

func TestReleaseImagesFromEnvironment(t *testing.T) {
	for _, name := range []string{"E2E_LATEST_RELEASE_IMAGE", "RELEASE_IMAGE_LATEST", "E2E_PREVIOUS_RELEASE_IMAGE", "OCP_IMAGE_N1", "E2E_N1_RELEASE_IMAGE", "E2E_N2_RELEASE_IMAGE", "OCP_IMAGE_N2", "E2E_N3_RELEASE_IMAGE", "OCP_IMAGE_N3", "E2E_N4_RELEASE_IMAGE", "OCP_IMAGE_N4"} {
		t.Setenv(name, "")
	}
	t.Run("When older release inputs are missing, it should not invent previous or skew images", func(t *testing.T) {
		images := ReleaseImagesFromEnvironment("quay.io/openshift-release-dev/ocp-release:4.22.0-x86_64")
		if len(images) != 1 || images["E2E_LATEST_RELEASE_IMAGE"] == "" {
			t.Fatalf("expected only the latest release input, got %d entries", len(images))
		}
	})
	t.Run("When create has release inputs, it should preserve them through serialization", func(t *testing.T) {
		t.Setenv("OCP_IMAGE_N1", "quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64")
		t.Setenv("OCP_IMAGE_N2", "quay.io/openshift-release-dev/ocp-release:4.20.10-x86_64")
		t.Setenv("E2E_N2_RELEASE_IMAGE", "quay.io/openshift-release-dev/ocp-release:4.20.11-x86_64")
		images := ReleaseImagesFromEnvironment("quay.io/openshift-release-dev/ocp-release:4.22.0-x86_64")
		dir := t.TempDir()
		if err := WriteManifest(dir, &ClusterManifest{ReleaseImages: images}); err != nil {
			t.Fatal(err)
		}
		manifest, err := ReadManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(manifest.ReleaseImages, images) || images["E2E_PREVIOUS_RELEASE_IMAGE"] != os.Getenv("OCP_IMAGE_N1") || images["E2E_N1_RELEASE_IMAGE"] != os.Getenv("OCP_IMAGE_N1") || images["E2E_N2_RELEASE_IMAGE"] != os.Getenv("E2E_N2_RELEASE_IMAGE") {
			t.Fatal("resolved release-image inputs were not preserved")
		}
	})
}

func TestReadManifestReleaseImages(t *testing.T) {
	t.Run("When the on-disk image inputs are untrusted, it should reject them before applying environment", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(`{"clusters":[],"releaseImages":{"KUBECONFIG":"untrusted"}}`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadManifest(dir); err == nil {
			t.Fatal("expected manifest validation error")
		}
	})
}

func TestManifestRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		want *ClusterManifest
	}{
		{
			name: "When written with a single cluster, it should deserialize identically",
			want: &ClusterManifest{
				Clusters: []ClusterEntry{
					{Variant: "public", Name: "public-abc1234567", InfraID: "public-abc1234567", Namespace: "clusters"},
				},
			},
		},
		{
			name: "When written with destroy arguments, it should deserialize identically",
			want: &ClusterManifest{
				Clusters: []ClusterEntry{
					{Variant: "public", Name: "public-abc1234567", InfraID: "public-abc1234567", Namespace: "clusters", DestroyArgs: []string{"--region=us-east-1", "--base-domain=ci.hypershift.devcluster.openshift.com"}},
				},
			},
		},
		{
			name: "When written with multiple clusters, it should deserialize identically",
			want: &ClusterManifest{
				Clusters: []ClusterEntry{
					{Variant: "public", Name: "public-abc1234567", InfraID: "public-abc1234567", Namespace: "clusters"},
					{Variant: "private", Name: "private-abc1234567", InfraID: "private-abc1234567", Namespace: "clusters"},
					{Variant: "external-oidc", Name: "external-oidc-abc1234567", InfraID: "external-oidc-abc1234567", Namespace: "clusters"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := WriteManifest(dir, tt.want); err != nil {
				t.Fatalf("WriteManifest: %v", err)
			}
			got, err := ReadManifest(dir)
			if err != nil {
				t.Fatalf("ReadManifest: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolveVariants(t *testing.T) {
	manifest := &ClusterManifest{
		Clusters: []ClusterEntry{
			{Variant: "public", Name: "public-abc", InfraID: "public-abc", Namespace: "clusters"},
			{Variant: "private", Name: "private-abc", InfraID: "private-abc", Namespace: "clusters"},
		},
	}

	tests := []struct {
		name      string
		matrix    TestMatrix
		want      map[string]ClusterEntry
		wantError bool
	}{
		{
			name: "When all parallel variants exist, it should return resolved map",
			matrix: TestMatrix{
				Parallel: []TestGroup{
					{Variant: "public"},
					{Variant: "private"},
				},
			},
			want: map[string]ClusterEntry{
				"public":  {Variant: "public", Name: "public-abc", InfraID: "public-abc", Namespace: "clusters"},
				"private": {Variant: "private", Name: "private-abc", InfraID: "private-abc", Namespace: "clusters"},
			},
		},
		{
			name: "When sequential variants exist, it should return resolved map",
			matrix: TestMatrix{
				Sequential: []SequentialGroup{
					{Steps: []TestGroup{{Variant: "public"}, {Variant: "private"}}},
				},
			},
			want: map[string]ClusterEntry{
				"public":  {Variant: "public", Name: "public-abc", InfraID: "public-abc", Namespace: "clusters"},
				"private": {Variant: "private", Name: "private-abc", InfraID: "private-abc", Namespace: "clusters"},
			},
		},
		{
			name: "When a parallel variant is missing, it should return an error",
			matrix: TestMatrix{
				Parallel: []TestGroup{
					{Variant: "public"},
					{Variant: "nonexistent"},
				},
			},
			wantError: true,
		},
		{
			name: "When a sequential variant is missing, it should return an error",
			matrix: TestMatrix{
				Sequential: []SequentialGroup{
					{Steps: []TestGroup{{Variant: "nonexistent"}}},
				},
			},
			wantError: true,
		},
		{
			name: "When duplicate variants exist across parallel and sequential, it should deduplicate",
			matrix: TestMatrix{
				Parallel:   []TestGroup{{Variant: "public"}},
				Sequential: []SequentialGroup{{Steps: []TestGroup{{Variant: "public"}}}},
			},
			want: map[string]ClusterEntry{
				"public": {Variant: "public", Name: "public-abc", InfraID: "public-abc", Namespace: "clusters"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.matrix.ResolveVariants(manifest)
			if tt.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLookupCluster(t *testing.T) {
	m := &ClusterManifest{
		Clusters: []ClusterEntry{
			{Variant: "public", Name: "pub-name", InfraID: "pub-name", Namespace: "clusters"},
			{Variant: "private", Name: "priv-name", InfraID: "priv-name", Namespace: "clusters"},
		},
	}

	tests := []struct {
		name      string
		variant   string
		want      ClusterEntry
		wantError bool
	}{
		{"when variant exists should return the entry", "public", ClusterEntry{Variant: "public", Name: "pub-name", InfraID: "pub-name", Namespace: "clusters"}, false},
		{"when variant does not exist should return an error", "nonexistent", ClusterEntry{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m.LookupCluster(tt.variant)
			if tt.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
