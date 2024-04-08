package core

import (
	"testing"

	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/releaseinfo/fake"
)

func TestDefaultNetworkType(t *testing.T) {
	testCases := []struct {
		name     string
		opts     *CreateOptions
		provider releaseinfo.Provider
		expected string
	}{
		{
			name: "Already configured, no change",
			opts: &CreateOptions{
				NetworkType:  "foo",
				ReleaseImage: "4.11.0",
			},
			expected: "foo",
		},
		{
			name: "4.11, ovn-k",
			opts: &CreateOptions{
				ReleaseImage: "4.11.0",
			},
			provider: &fake.FakeReleaseProvider{Version: "4.11.0"},
			expected: "OVNKubernetes",
		},
		{
			name: "4.12, ovn-k",
			opts: &CreateOptions{
				ReleaseImage: "4.12.0",
			},
			provider: &fake.FakeReleaseProvider{Version: "4.12.0"},
			expected: "OVNKubernetes",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defaultNetworkType(tc.opts)
			if tc.opts.NetworkType != tc.expected {
				t.Errorf("expected network type %s, got %s", tc.expected, tc.opts.NetworkType)
			}
		})
	}
}
