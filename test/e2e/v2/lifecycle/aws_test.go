//go:build e2ev2

package lifecycle

import (
	"testing"

	e2eutil "github.com/openshift/hypershift/test/e2e/util"
)

func TestReleaseImageAtLeast(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		releaseImage string
		want         bool
	}{
		{
			name:         "When the release is a 5.1 GA build, it should be at least 5.1",
			releaseImage: "quay.io/openshift-release-dev/ocp-release:5.1.0-multi",
			want:         true,
		},
		{
			name:         "When the release is a 5.1 pre-release EC build, it should be at least 5.1",
			releaseImage: "quay.io/openshift-release-dev/ocp-release:5.1.0-ec.1-multi",
			want:         true,
		},
		{
			name:         "When the release is a 5.1 nightly pre-release, it should be at least 5.1",
			releaseImage: "quay.io/openshift-release-dev/ocp-release:5.1.0-0.nightly-2026-10-02-181926-multi",
			want:         true,
		},
		{
			name:         "When the release is a later y-stream, it should be at least 5.1",
			releaseImage: "quay.io/openshift-release-dev/ocp-release:5.2.0-ec.0-multi",
			want:         true,
		},
		{
			name:         "When the release is a 5.0 build, it should not be at least 5.1",
			releaseImage: "quay.io/openshift-release-dev/ocp-release:5.0.0-ec.6-multi",
			want:         false,
		},
		{
			name:         "When the release is an older y-stream, it should not be at least 5.1",
			releaseImage: "quay.io/openshift-release-dev/ocp-release:4.19.10-x86_64",
			want:         false,
		},
		{
			name:         "When the release image is unparsable, it should default to at least",
			releaseImage: "registry.ci.openshift.org/ocp/release:not-a-version",
			want:         true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := releaseImageAtLeast(tc.releaseImage, e2eutil.Version51); got != tc.want {
				t.Errorf("releaseImageAtLeast(%q, Version51) = %v, want %v", tc.releaseImage, got, tc.want)
			}
		})
	}
}
