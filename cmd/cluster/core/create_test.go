package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	corev1 "k8s.io/api/core/v1"
	apiversion "k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakekubeclient "k8s.io/client-go/kubernetes/fake"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestValidateMgmtClusterAndNodePoolCPUArchitectures(t *testing.T) {
	ctx := context.Background()

	fakeKubeClient := fakekubeclient.NewSimpleClientset()
	fakeDiscovery, ok := fakeKubeClient.Discovery().(*fakediscovery.FakeDiscovery)
	if !ok {
		t.Fatalf("failed to convert FakeDiscovery")
	}

	fakeMetadataProvider := &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
		Result:   &dockerv1client.DockerImageConfig{},
		Manifest: fakeimagemetadataprovider.FakeManifest{},
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
			err := validateMgmtClusterAndNodePoolCPUArchitectures(ctx, tc.opts, fakeKubeClient, fakeMetadataProvider)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

// This test will make sure the order of the objects is correct
// being the HC and NP the last ones and the first one is the namespace.
func TestAsObjects(t *testing.T) {
	tests := []struct {
		name         string
		resources    *resources
		expectedFail bool
	}{
		{
			name: "All resources are present",
			resources: &resources{
				Namespace:             &corev1.Namespace{},
				AdditionalTrustBundle: &corev1.ConfigMap{},
				PullSecret:            &corev1.Secret{},
				SSHKey:                &corev1.Secret{},
				Cluster:               &hyperv1.HostedCluster{},
				Resources: []crclient.Object{
					&corev1.ConfigMap{},
					&corev1.Secret{},
				},
				NodePools: []*hyperv1.NodePool{{}, {}},
			},
			expectedFail: false,
		},
		{
			name: "Namespace resource is nil",
			resources: &resources{
				Namespace:             nil,
				AdditionalTrustBundle: &corev1.ConfigMap{},
				PullSecret:            &corev1.Secret{},
				SSHKey:                &corev1.Secret{},
				Cluster:               &hyperv1.HostedCluster{},
				Resources: []crclient.Object{
					&corev1.ConfigMap{},
					&corev1.Secret{},
				},
				NodePools: []*hyperv1.NodePool{{}, {}},
			},
			expectedFail: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			objects := tc.resources.asObjects()
			if tc.expectedFail {
				g.Expect(objects[0]).To(Not(Equal(tc.resources.Namespace)))
				return
			}
			g.Expect(objects[0]).To(Equal(tc.resources.Namespace), "Namespace should be the first object in the slice")
			hcPosition := len(objects) - len(tc.resources.NodePools) - 1
			g.Expect(objects[hcPosition]).To(Equal(tc.resources.Cluster), "HostedCluster should be the secodn-to-last object in the slice")
			g.Expect(objects[len(objects)-1]).To(Equal(tc.resources.NodePools[len(tc.resources.NodePools)-1]), "NodePools should be the last object in the slice")
		})
	}
}

func TestPrototypeResources(t *testing.T) {
	g := NewWithT(t)
	opts := &CreateOptions{
		completedCreateOptions: &completedCreateOptions{
			ValidatedCreateOptions: &ValidatedCreateOptions{
				validatedCreateOptions: &validatedCreateOptions{
					RawCreateOptions: &RawCreateOptions{
						DisableClusterCapabilities: []string{string(hyperv1.ImageRegistryCapability)},
						KubeAPIServerDNSName:       "test-dns-name.example.com",
					},
				},
			},
		},
	}
	resources, err := prototypeResources(opts)
	g.Expect(err).To(BeNil())
	g.Expect(resources.Cluster.Spec.Capabilities.Disabled).
		To(Equal([]hyperv1.OptionalCapability{hyperv1.ImageRegistryCapability}))
	g.Expect(resources.Cluster.Spec.KubeAPIServerDNSName).To(Equal("test-dns-name.example.com"))
}

func TestValidate(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	tempDir := t.TempDir()

	pullSecretFile := filepath.Join(tempDir, "pull-secret.json")

	if err := os.WriteFile(pullSecretFile, []byte(`fake`), 0600); err != nil {
		t.Fatalf("failed to write pullSecret: %v", err)
	}

	tests := []struct {
		name        string
		rawOpts     *RawCreateOptions
		expectedErr string
	}{
		{
			name: "fails with unsupported capability",
			rawOpts: &RawCreateOptions{
				Name:                       "test-hc",
				Namespace:                  "test-hc",
				PullSecretFile:             pullSecretFile,
				Arch:                       "amd64",
				DisableClusterCapabilities: []string{"UnsupportedCapability"},
			},
			expectedErr: "unknown capability, accepted values are:",
		},
		{
			name: "passes with ImageRegistry capability",
			rawOpts: &RawCreateOptions{
				Name:                       "test-hc",
				Namespace:                  "test-hc",
				PullSecretFile:             pullSecretFile,
				Arch:                       "amd64",
				DisableClusterCapabilities: []string{"ImageRegistry"},
			},
			expectedErr: "",
		},
		{
			name: "fails with an invalid DNS name as KubeAPIServerDNSName",
			rawOpts: &RawCreateOptions{
				Name:                 "test-hc",
				Namespace:            "test-hc",
				PullSecretFile:       pullSecretFile,
				Arch:                 "amd64",
				KubeAPIServerDNSName: "INVALID-DNS-NAME.example.com",
			},
			expectedErr: "KubeAPIServerDNSName failed DNS validation: a lowercase RFC 1123 subdomain must consist of lower case alphanumeric characters",
		},
		{
			name: "passes with KubeAPIServerDNSName",
			rawOpts: &RawCreateOptions{
				Name:                 "test-hc",
				Namespace:            "test-hc",
				PullSecretFile:       pullSecretFile,
				Arch:                 "amd64",
				KubeAPIServerDNSName: "test-dns-name.example.com",
			},
			expectedErr: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// avoid actual client calls in Validate
			test.rawOpts.Render = true
			_, err := test.rawOpts.Validate(ctx)
			if test.expectedErr == "" {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(test.expectedErr))
			}
		})
	}
}
