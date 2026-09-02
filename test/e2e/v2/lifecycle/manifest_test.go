//go:build e2ev2

package lifecycle

import (
	"reflect"
	"testing"
)

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
