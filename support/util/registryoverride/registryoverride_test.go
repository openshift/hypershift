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
			name:      "nil overrides returns input unchanged",
			image:     "quay.io/openshift-release-dev/ocp-release@sha256:abc",
			overrides: nil,
			want:      "quay.io/openshift-release-dev/ocp-release@sha256:abc",
		},
		{
			name:      "empty overrides returns input unchanged",
			image:     "quay.io/openshift-release-dev/ocp-release@sha256:abc",
			overrides: map[string]string{},
			want:      "quay.io/openshift-release-dev/ocp-release@sha256:abc",
		},
		{
			name:      "no matching override returns input unchanged",
			image:     "quay.io/openshift-release-dev/ocp-release@sha256:abc",
			overrides: map[string]string{"registry.redhat.io": "mirror.example.com"},
			want:      "quay.io/openshift-release-dev/ocp-release@sha256:abc",
		},
		{
			name:      "exact-match key replaces image",
			image:     "quay.io",
			overrides: map[string]string{"quay.io": "mirror.example.com"},
			want:      "mirror.example.com",
		},
		{
			name:      "slash-boundary prefix match preserves path and digest",
			image:     "quay.io/openshift-release-dev/ocp-release@sha256:abc",
			overrides: map[string]string{"quay.io": "mirror.example.com"},
			want:      "mirror.example.com/openshift-release-dev/ocp-release@sha256:abc",
		},
		{
			name:      "subdomain does not match (no false positive)",
			image:     "quay.io.example.com/foo/bar:latest",
			overrides: map[string]string{"quay.io": "mirror.example.com"},
			want:      "quay.io.example.com/foo/bar:latest",
		},
		{
			name:      "trailing path component does not match (no false positive)",
			image:     "quay.io-evil/foo:latest",
			overrides: map[string]string{"quay.io": "mirror.example.com"},
			want:      "quay.io-evil/foo:latest",
		},
		{
			name:  "longest matching prefix wins",
			image: "quay.io/openshift-release-dev/ocp-release@sha256:abc",
			overrides: map[string]string{
				"quay.io":                       "broad.example.com",
				"quay.io/openshift-release-dev": "narrow.example.com/mirror",
			},
			want: "narrow.example.com/mirror/ocp-release@sha256:abc",
		},
		{
			name:  "shorter prefix used when longer prefix does not match",
			image: "quay.io/some-other-org/image:tag",
			overrides: map[string]string{
				"quay.io":                       "broad.example.com",
				"quay.io/openshift-release-dev": "narrow.example.com/mirror",
			},
			want: "broad.example.com/some-other-org/image:tag",
		},
		{
			name:  "empty source key is skipped",
			image: "quay.io/foo/bar:latest",
			overrides: map[string]string{
				"":        "should-never-be-used",
				"quay.io": "mirror.example.com",
			},
			want: "mirror.example.com/foo/bar:latest",
		},
		{
			name:      "tag is preserved",
			image:     "quay.io/foo/bar:v1.2.3",
			overrides: map[string]string{"quay.io": "mirror.example.com"},
			want:      "mirror.example.com/foo/bar:v1.2.3",
		},
		{
			name:      "empty image returns empty",
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
