package core

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	apiversion "k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakekubeclient "k8s.io/client-go/kubernetes/fake"
)

func TestValidateMgmtClusterAndNodePoolCPUArchitectures(t *testing.T) {
	ctx := context.Background()

	fakeKubeClient := fakekubeclient.NewSimpleClientset()
	fakeDiscovery, ok := fakeKubeClient.Discovery().(*fakediscovery.FakeDiscovery)

	if !ok {
		t.Fatalf("failed to convert FakeDiscovery")
	}

	// if you want to fake a specific version
	fakeDiscovery.FakedServerVersion = &apiversion.Info{
		Platform: "linux/amd64",
	}

	tests := []struct {
		name        string
		opts        *RawCreateOptions
		expected    bool
		expectError bool
	}{
		{
			name: "When a multi-arch release is passed, the function should return no errors",
			opts: &RawCreateOptions{
				ReleaseImage:   "quay.io/openshift-release-dev/ocp-release:4.16.13-multi",
				PullSecretFile: "../../../hack/dev/fakePullSecret.json",
				ReleaseStream:  "4-stable",
				Arch:           "amd64",
			},
			expectError: false,
		},
		{
			name: "When no release image was passed and a valid multi-arch stream is passed, the function should return no errors",
			opts: &RawCreateOptions{
				ReleaseImage:   "",
				PullSecretFile: "../../../hack/dev/fakePullSecret.json",
				ReleaseStream:  "4-stable-multi",
				Arch:           "amd64",
			},
			expectError: false,
		},
		{
			name: "When a single arch release is passed and the NodePool arch matches the arch of the release, the function should return no errors",
			opts: &RawCreateOptions{
				ReleaseImage:   "quay.io/openshift-release-dev/ocp-release:4.16.13-x86_64",
				PullSecretFile: "../../../hack/dev/fakePullSecret.json",
				ReleaseStream:  "4-stable",
				Arch:           "amd64",
			},
			expectError: false,
		},
		{
			name: "When a single arch release is passed and the NodePool arch doesn't match the arch of the release, the function should return an error",
			opts: &RawCreateOptions{
				ReleaseImage:   "quay.io/openshift-release-dev/ocp-release:4.16.13-x86_64",
				PullSecretFile: "../../../hack/dev/fakePullSecret.json",
				ReleaseStream:  "4-stable",
				Arch:           "arm64",
			},
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			err := validateMgmtClusterAndNodePoolCPUArchitectures(ctx, tc.opts, fakeKubeClient)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}
