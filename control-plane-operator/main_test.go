package main

import (
	"testing"

	. "github.com/onsi/gomega"

	apiversion "k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakekubeclient "k8s.io/client-go/kubernetes/fake"
)

func TestMgmtClusterSupportsNativeSidecars(t *testing.T) {
	tests := []struct {
		name            string
		gitVersion      string
		expectedSupport bool
	}{
		{
			name:            "When K8s version is 1.29.0 it should support native sidecars",
			gitVersion:      "v1.29.0",
			expectedSupport: true,
		},
		{
			name:            "When K8s version is 1.30.0 it should support native sidecars",
			gitVersion:      "v1.30.0",
			expectedSupport: true,
		},
		{
			name:            "When K8s version is 1.28.0 it should not support native sidecars",
			gitVersion:      "v1.28.0",
			expectedSupport: false,
		},
		{
			name:            "When K8s version is 1.27.0 it should not support native sidecars",
			gitVersion:      "v1.27.0",
			expectedSupport: false,
		},
		{
			name:            "When K8s version is an OCP-style version it should parse correctly",
			gitVersion:      "v1.29.3+abcdef1",
			expectedSupport: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			fakeKubeClient := fakekubeclient.NewSimpleClientset()
			fakeDiscovery, ok := fakeKubeClient.Discovery().(*fakediscovery.FakeDiscovery)
			g.Expect(ok).To(BeTrue())

			fakeDiscovery.FakedServerVersion = &apiversion.Info{
				GitVersion: tc.gitVersion,
			}

			supported, err := mgmtClusterSupportsNativeSidecars(fakeDiscovery)
			g.Expect(err).To(BeNil())
			g.Expect(supported).To(Equal(tc.expectedSupport))
		})
	}
}
