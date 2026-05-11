package core

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/api"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestValidateHostedClusterPayloadSupportsNodePoolCPUArch(t *testing.T) {
	for _, testCase := range []struct {
		name                     string
		hc                       *hyperv1.HostedCluster
		nodePoolCPUArch          string
		buildHostedClusterObject bool
		expectedErr              bool
	}{
		{
			name: "when a valid HC exists and the payload type is Multi, then there are no errors",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "clusters",
				},
				Status: hyperv1.HostedClusterStatus{
					PayloadArch: hyperv1.Multi,
				},
			},
			buildHostedClusterObject: true,
			expectedErr:              false,
		},
		{
			name: "when a valid HC exists and the payload type is AMD64 and the NodePool CPU arch is AMD64, then there are no errors",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "clusters",
				},
				Status: hyperv1.HostedClusterStatus{
					PayloadArch: hyperv1.AMD64,
				},
			},
			nodePoolCPUArch:          hyperv1.ArchitectureAMD64,
			buildHostedClusterObject: true,
			expectedErr:              false,
		},
		{
			name: "when a valid HC exists and the payload type is AMD64 and the NodePool CPU arch is ARM64, then there is an error",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "clusters",
				},
				Status: hyperv1.HostedClusterStatus{
					PayloadArch: hyperv1.AMD64,
				},
			},
			nodePoolCPUArch:          hyperv1.ArchitectureARM64,
			buildHostedClusterObject: true,
			expectedErr:              true,
		},
		{
			name: "when a valid HC does not exist, then there are no errors",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "clusters",
				},
				Status: hyperv1.HostedClusterStatus{
					PayloadArch: hyperv1.AMD64,
				},
			},
			buildHostedClusterObject: false,
			expectedErr:              false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			g := NewWithT(t)

			var objs []crclient.Object

			if testCase.buildHostedClusterObject {
				objs = append(objs, testCase.hc)
			}

			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objs...).Build()

			err := validateHostedClusterPayloadSupportsNodePoolCPUArch(t.Context(), c, testCase.hc.Name, testCase.hc.Namespace, testCase.nodePoolCPUArch)
			if testCase.expectedErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestValidMinorVersionCompatibility(t *testing.T) {
	// Define base HostedCluster structure
	baseHC := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedClusterSpec{
			PullSecret: corev1.LocalObjectReference{
				Name: "pull-secret",
			},
		},
		Status: hyperv1.HostedClusterStatus{
			Version: &hyperv1.ClusterVersionStatus{
				History: []configv1.UpdateHistory{},
			},
		},
	}

	basePullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-secret",
			Namespace: "test-namespace",
		},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{"quay.io":{"auth":"","email":""}}}`),
		},
	}

	tests := []struct {
		name                 string
		controlPlaneVersion  string
		nodePoolReleaseImage string
		nodePoolVersion      string
		expectedError        string
	}{
		{
			name:                 "when nodePool version matches control plane version it should not return error",
			controlPlaneVersion:  "4.18.5",
			nodePoolReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.18.5-x86_64",
			nodePoolVersion:      "4.18.5",
			expectedError:        "",
		},
		{
			name:                 "when nodePool version is higher than control plane version it should return error",
			controlPlaneVersion:  "4.17.0",
			nodePoolReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.18.5-x86_64",
			nodePoolVersion:      "4.18.5",
			expectedError:        "NodePool version 4.18.5 cannot be higher than the HostedCluster version 4.17.0",
		},
		{
			name:                 "when nodePool version is one minor version lower (n-1) it should not return error",
			controlPlaneVersion:  "4.18.0",
			nodePoolReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.17.37-x86_64",
			nodePoolVersion:      "4.17.37",
			expectedError:        "",
		},
		{
			name:                 "when nodePool version is two minor versions lower (n-2) it should not return error",
			controlPlaneVersion:  "4.18.0",
			nodePoolReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.16.0-x86_64",
			nodePoolVersion:      "4.16.0",
			expectedError:        "",
		},
		{
			name:                 "when nodePool version is three minor versions lower (n-3) it should not return error",
			controlPlaneVersion:  "4.18.0",
			nodePoolReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.15.0-x86_64",
			nodePoolVersion:      "4.15.0",
			expectedError:        "",
		},
		{
			name:                 "when nodePool version is four minor versions lower (n-4) it should return error",
			controlPlaneVersion:  "4.18.0",
			nodePoolReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.14.0-x86_64",
			nodePoolVersion:      "4.14.0",
			expectedError:        "NodePool minor version 4.14 is less than 4.15, which is the minimum NodePool version compatible with the 4.18 HostedCluster",
		},
		{
			name:                 "when nodePool major version is higher than control plane version it should return error",
			controlPlaneVersion:  "4.18.0",
			nodePoolReleaseImage: "quay.io/openshift-release-dev/ocp-release:5.0.0-x86_64",
			nodePoolVersion:      "5.0.0",
			expectedError:        "NodePool version 5.0.0 cannot be higher than the HostedCluster version 4.18.0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			t.Logf("Running test case: %s", test.name)

			// Create a copy of the base HostedCluster and modify only the version
			hc := baseHC.DeepCopy()
			hc.Status.Version.History = []configv1.UpdateHistory{
				{
					State:   configv1.CompletedUpdate,
					Version: test.controlPlaneVersion,
				},
			}

			// Create the resources in the fake client
			objs := []crclient.Object{hc, basePullSecret}
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objs...).Build()

			releaseProvider := &fakereleaseprovider.FakeReleaseProvider{
				Version: test.nodePoolVersion,
			}

			// Run the test
			err := validMinorVersionCompatibility(t.Context(), c, "test-cluster", "test-namespace", test.nodePoolReleaseImage, releaseProvider)

			// Check the results
			if test.expectedError == "" {
				g.Expect(err).NotTo(HaveOccurred())
				t.Log("Test passed as expected with no error")
			} else {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(test.expectedError))
				t.Logf("Test passed as expected with error: %s", test.expectedError)
			}
		})
	}

	t.Run("when multiple history entries exist it should use History[0] as the control plane version", func(t *testing.T) {
		g := NewWithT(t)

		hc := baseHC.DeepCopy()
		hc.Status.Version.History = []configv1.UpdateHistory{
			{
				State:   configv1.CompletedUpdate,
				Version: "4.18.0", // Newest - should be used
			},
			{
				State:   configv1.CompletedUpdate,
				Version: "4.17.5",
			},
			{
				State:   configv1.CompletedUpdate,
				Version: "4.17.0", // Oldest - should NOT be used
			},
		}

		objs := []crclient.Object{hc, basePullSecret}
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objs...).Build()

		releaseProvider := &fakereleaseprovider.FakeReleaseProvider{
			Version: "4.14.0",
		}

		err := validMinorVersionCompatibility(t.Context(), c, "test-cluster", "test-namespace", "quay.io/openshift-release-dev/ocp-release:4.14.0-x86_64", releaseProvider)

		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(Equal("NodePool minor version 4.14 is less than 4.15, which is the minimum NodePool version compatible with the 4.18 HostedCluster"))
	})
}

func TestCreateNodePoolGetClient(t *testing.T) {
	t.Run("When Client is injected it should return the injected client", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		opts := &CreateNodePoolOptions{
			ClientHolder: util.ClientHolder{Client: fakeClient},
		}
		c, err := opts.GetClient()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(c).To(Equal(fakeClient))
	})

	t.Run("When Client is nil it should fall back to util.GetClient", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		opts := &CreateNodePoolOptions{}
		c, err := opts.GetClient()
		if err != nil {
			g.Expect(c).To(BeNil())
		} else {
			g.Expect(c).NotTo(BeNil())
		}
	})
}

func TestCreateNodePoolWithInjectedClient(t *testing.T) {
	t.Run("When Client is injected and NodePool already exists it should detect the duplicate", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		existingHC := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "clusters",
			},
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AWSPlatform,
				},
				Release: hyperv1.Release{
					Image: "quay.io/openshift-release-dev/ocp-release:4.18.0-x86_64",
				},
				PullSecret: corev1.LocalObjectReference{
					Name: "pull-secret",
				},
			},
			Status: hyperv1.HostedClusterStatus{
				PayloadArch: hyperv1.AMD64,
				Version: &hyperv1.ClusterVersionStatus{
					History: []configv1.UpdateHistory{
						{
							State:   configv1.CompletedUpdate,
							Version: "4.18.0",
						},
					},
				},
			},
		}

		existingNP := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-np",
				Namespace: "clusters",
			},
		}

		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pull-secret",
				Namespace: "clusters",
			},
			Data: map[string][]byte{
				corev1.DockerConfigJsonKey: []byte(`{"auths":{"quay.io":{"auth":"","email":""}}}`),
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(api.Scheme).
			WithObjects(existingHC, existingNP, pullSecret).
			Build()

		opts := &CreateNodePoolOptions{
			Name:         "test-np",
			Namespace:    "clusters",
			ClusterName:  "test-cluster",
			Arch:         "amd64",
			ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.18.0-x86_64",
			ClientHolder: util.ClientHolder{Client: fakeClient},
		}

		mockPlatform := &mockPlatformOptions{platformType: hyperv1.AWSPlatform}
		err := opts.CreateNodePool(context.Background(), mockPlatform)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("already exists"))
	})
}

type mockPlatformOptions struct {
	platformType hyperv1.PlatformType
}

func (m *mockPlatformOptions) UpdateNodePool(_ context.Context, _ *hyperv1.NodePool, _ *hyperv1.HostedCluster, _ crclient.Client) error {
	return nil
}

func (m *mockPlatformOptions) Type() hyperv1.PlatformType {
	return m.platformType
}
