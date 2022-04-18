package core

import (
	"context"
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
			name:     "Already configured, no change",
			opts:     &CreateOptions{NetworkType: "foo"},
			expected: "foo",
		},
		{
			name:     "4.10, SDN",
			opts:     &CreateOptions{},
			provider: &fake.FakeReleaseProvider{Version: "4.10.0"},
			expected: "OpenShiftSDN",
		},
		{
			name:     "4.11, ovn-k",
			opts:     &CreateOptions{},
			provider: &fake.FakeReleaseProvider{Version: "4.11.0"},
			expected: "OVNKubernetes",
		},
		{
			name:     "4.12, ovn-k",
			opts:     &CreateOptions{},
			provider: &fake.FakeReleaseProvider{Version: "4.11.0"},
			expected: "OVNKubernetes",
		},
	}

	readFile := func(string) ([]byte, error) { return nil, nil }

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := defaultNetworkType(context.Background(), tc.opts, tc.provider, readFile); err != nil {
				t.Fatalf("defaultNetworkType failed: %v", err)
			}
			if tc.opts.NetworkType != tc.expected {
				t.Errorf("expected network type %s, got %s", tc.expected, tc.opts.NetworkType)
			}
		})
	}
}
