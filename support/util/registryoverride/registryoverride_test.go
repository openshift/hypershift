package registryoverride

import (
	"reflect"
	"testing"
)

func TestReplace(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		image     string
		overrides map[string]string
		want      string
	}{
		{
			name:      "When nil overrides are provided, it should return input unchanged",
			image:     "quay.io/openshift-release-dev/ocp-release@sha256:abc",
			overrides: nil,
			want:      "quay.io/openshift-release-dev/ocp-release@sha256:abc",
		},
		{
			name:      "When empty overrides are provided, it should return input unchanged",
			image:     "quay.io/openshift-release-dev/ocp-release@sha256:abc",
			overrides: map[string]string{},
			want:      "quay.io/openshift-release-dev/ocp-release@sha256:abc",
		},
		{
			name:      "When no matching override exists, it should return input unchanged",
			image:     "quay.io/openshift-release-dev/ocp-release@sha256:abc",
			overrides: map[string]string{"registry.redhat.io": "mirror.example.com"},
			want:      "quay.io/openshift-release-dev/ocp-release@sha256:abc",
		},
		{
			name:      "When exact-match key is found, it should replace image",
			image:     "quay.io",
			overrides: map[string]string{"quay.io": "mirror.example.com"},
			want:      "mirror.example.com",
		},
		{
			name:      "When slash-boundary prefix matches, it should preserve path and digest",
			image:     "quay.io/openshift-release-dev/ocp-release@sha256:abc",
			overrides: map[string]string{"quay.io": "mirror.example.com"},
			want:      "mirror.example.com/openshift-release-dev/ocp-release@sha256:abc",
		},
		{
			name:      "When subdomain looks similar, it should not match",
			image:     "quay.io.example.com/foo/bar:latest",
			overrides: map[string]string{"quay.io": "mirror.example.com"},
			want:      "quay.io.example.com/foo/bar:latest",
		},
		{
			name:      "When trailing path component looks similar, it should not match",
			image:     "quay.io-evil/foo:latest",
			overrides: map[string]string{"quay.io": "mirror.example.com"},
			want:      "quay.io-evil/foo:latest",
		},
		{
			name:  "When multiple prefixes match, it should use longest matching prefix",
			image: "quay.io/openshift-release-dev/ocp-release@sha256:abc",
			overrides: map[string]string{
				"quay.io":                       "broad.example.com",
				"quay.io/openshift-release-dev": "narrow.example.com/mirror",
			},
			want: "narrow.example.com/mirror/ocp-release@sha256:abc",
		},
		{
			name:  "When longer prefix does not match, it should use shorter prefix",
			image: "quay.io/some-other-org/image:tag",
			overrides: map[string]string{
				"quay.io":                       "broad.example.com",
				"quay.io/openshift-release-dev": "narrow.example.com/mirror",
			},
			want: "broad.example.com/some-other-org/image:tag",
		},
		{
			name:  "When empty source key exists, it should skip it",
			image: "quay.io/foo/bar:latest",
			overrides: map[string]string{
				"":        "should-never-be-used",
				"quay.io": "mirror.example.com",
			},
			want: "mirror.example.com/foo/bar:latest",
		},
		{
			name:      "When image has tag, it should preserve tag",
			image:     "quay.io/foo/bar:v1.2.3",
			overrides: map[string]string{"quay.io": "mirror.example.com"},
			want:      "mirror.example.com/foo/bar:v1.2.3",
		},
		{
			name:      "When source matches full repository with digest separator, it should replace prefix",
			image:     "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:abc123",
			overrides: map[string]string{"quay.io/openshift-release-dev/ocp-v4.0-art-dev": "mirror.example.com/art-dev"},
			want:      "mirror.example.com/art-dev@sha256:abc123",
		},
		{
			name:      "When source matches full repository with tag separator, it should replace prefix",
			image:     "quay.io/openshift-release-dev/ocp-v4.0-art-dev:latest",
			overrides: map[string]string{"quay.io/openshift-release-dev/ocp-v4.0-art-dev": "mirror.example.com/art-dev"},
			want:      "mirror.example.com/art-dev:latest",
		},
		{
			name:  "When multiple overrides match with digest, it should pick longest prefix",
			image: "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:abc123",
			overrides: map[string]string{
				"quay.io": "broad.example.com",
				"quay.io/openshift-release-dev/ocp-v4.0-art-dev": "narrow.example.com/art-dev",
			},
			want: "narrow.example.com/art-dev@sha256:abc123",
		},
		{
			name:      "When source has trailing dash, it should not match similar prefix (no false positive)",
			image:     "quay.io/openshift-release-dev/ocp-v4.0-art-dev-extra@sha256:abc",
			overrides: map[string]string{"quay.io/openshift-release-dev/ocp-v4.0-art-dev": "mirror/art-dev"},
			want:      "quay.io/openshift-release-dev/ocp-v4.0-art-dev-extra@sha256:abc",
		},
		{
			name:      "When host-only source matches host:port image, it should not match (port is not a tag)",
			image:     "quay.io:5000/org/repo@sha256:abc",
			overrides: map[string]string{"quay.io": "mirror.example.com"},
			want:      "quay.io:5000/org/repo@sha256:abc",
		},
		{
			name:      "When host:port source matches host:port image, it should match via slash",
			image:     "myregistry:5000/org/repo@sha256:abc",
			overrides: map[string]string{"myregistry:5000": "mirror.example.com"},
			want:      "mirror.example.com/org/repo@sha256:abc",
		},
		{
			name:      "When image is empty, it should return empty",
			image:     "",
			overrides: map[string]string{"quay.io": "mirror.example.com"},
			want:      "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Replace(tc.image, tc.overrides)
			if got != tc.want {
				t.Errorf("Replace(%q, %v) = %q, want %q", tc.image, tc.overrides, got, tc.want)
			}
		})
	}
}

func TestReplace_DoesNotMutateOverrides(t *testing.T) {
	t.Parallel()

	overrides := map[string]string{
		"quay.io":                       "broad.example.com",
		"quay.io/openshift-release-dev": "narrow.example.com/mirror",
		"registry.redhat.io":            "rh.mirror.example.com",
	}
	snapshot := make(map[string]string, len(overrides))
	for k, v := range overrides {
		snapshot[k] = v
	}

	_ = Replace("quay.io/openshift-release-dev/ocp-release@sha256:abc", overrides)
	_ = Replace("registry.redhat.io/some/image:latest", overrides)
	_ = Replace("does.not.match/anything:tag", overrides)

	if !reflect.DeepEqual(overrides, snapshot) {
		t.Errorf("overrides map was mutated by Replace; got %v, want %v", overrides, snapshot)
	}
}
