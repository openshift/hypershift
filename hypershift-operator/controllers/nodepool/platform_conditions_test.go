package nodepool

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"

	v1 "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/coreos/stream-metadata-go/stream"
)

func TestSetOpenStackConditions(t *testing.T) {
	t.Parallel()

	releaseImageWithStreams := &releaseinfo.ReleaseImage{
		ImageStream:    &v1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.17.0"}},
		StreamMetadata: testOpenStackStream("417.94.202407010929-0"),
	}

	testCases := []struct {
		name              string
		nodePool          *hyperv1.NodePool
		hostedCluster     *hyperv1.HostedCluster
		releaseImage      *releaseinfo.ReleaseImage
		resolvedStream    string
		expectError       bool
		expectedCondType  string
		expectedCondValue corev1.ConditionStatus
	}{
		{
			name: "When OpenStack architecture is missing it should set ValidPlatformImage to false",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type:      hyperv1.OpenStackPlatform,
						OpenStack: &hyperv1.OpenStackNodePoolPlatform{},
					},
					Release: hyperv1.Release{Image: "quay.io/test:4.17"},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{OpenStack: &hyperv1.OpenStackPlatformSpec{}},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &v1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.17.0"}},
				StreamMetadata: &stream.Stream{
					Architectures: map[string]stream.Arch{
						"aarch64": {},
					},
				},
			},
			resolvedStream:    StreamRHEL9,
			expectedCondType:  string(hyperv1.NodePoolValidPlatformImageType),
			expectedCondValue: corev1.ConditionFalse,
			expectError:       true,
		},
		{
			name: "When explicit ImageName is set it should set ValidPlatformImage to true",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.OpenStackPlatform,
						OpenStack: &hyperv1.OpenStackNodePoolPlatform{
							ImageName: "my-custom-image",
						},
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{OpenStack: &hyperv1.OpenStackPlatformSpec{}},
				},
			},
			releaseImage:      releaseImageWithStreams,
			resolvedStream:    StreamRHEL9,
			expectedCondType:  string(hyperv1.NodePoolValidPlatformImageType),
			expectedCondValue: corev1.ConditionTrue,
		},
		{
			name: "When stream metadata is nil it should set ValidPlatformImage to false",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type:      hyperv1.OpenStackPlatform,
						OpenStack: &hyperv1.OpenStackNodePoolPlatform{},
					},
					Release: hyperv1.Release{Image: "quay.io/test:4.17"},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{OpenStack: &hyperv1.OpenStackPlatformSpec{}},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream:    &v1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.17.0"}},
				StreamMetadata: nil,
			},
			resolvedStream:    StreamRHEL9,
			expectError:       true,
			expectedCondType:  string(hyperv1.NodePoolValidPlatformImageType),
			expectedCondValue: corev1.ConditionFalse,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			r := &NodePoolReconciler{Client: fakeClient}
			err := r.setOpenStackConditions(t.Context(), tc.nodePool, tc.hostedCluster, "", tc.releaseImage, tc.resolvedStream)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			if tc.expectedCondType != "" {
				cond := FindStatusCondition(tc.nodePool.Status.Conditions, tc.expectedCondType)
				g.Expect(cond).ToNot(BeNil())
				g.Expect(cond.Status).To(Equal(tc.expectedCondValue))
			}
		})
	}
}

func TestSetPowerVSConditions(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name              string
		nodePool          *hyperv1.NodePool
		hostedCluster     *hyperv1.HostedCluster
		releaseImage      *releaseinfo.ReleaseImage
		resolvedStream    string
		expectError       bool
		expectedCondType  string
		expectedCondValue corev1.ConditionStatus
	}{
		{
			name: "When stream metadata is nil it should set ValidPlatformImage to false",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type:    hyperv1.PowerVSPlatform,
						PowerVS: &hyperv1.PowerVSNodePoolPlatform{},
					},
					Release: hyperv1.Release{Image: "quay.io/test:4.17"},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{PowerVS: &hyperv1.PowerVSPlatformSpec{Region: "us-south"}},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream:    &v1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.17.0"}},
				StreamMetadata: nil,
			},
			resolvedStream:    StreamRHEL9,
			expectError:       true,
			expectedCondType:  string(hyperv1.NodePoolValidPlatformImageType),
			expectedCondValue: corev1.ConditionFalse,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			r := &NodePoolReconciler{Client: fakeClient}
			err := r.setPowerVSconditions(t.Context(), tc.nodePool, tc.hostedCluster, "", tc.releaseImage, tc.resolvedStream)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			if tc.expectedCondType != "" {
				cond := FindStatusCondition(tc.nodePool.Status.Conditions, tc.expectedCondType)
				g.Expect(cond).ToNot(BeNil())
				g.Expect(cond.Status).To(Equal(tc.expectedCondValue))
			}
		})
	}
}

func TestSetKubevirtConditions(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name              string
		nodePool          *hyperv1.NodePool
		hostedCluster     *hyperv1.HostedCluster
		releaseImage      *releaseinfo.ReleaseImage
		resolvedStream    string
		expectError       bool
		expectedCondType  string
		expectedCondValue corev1.ConditionStatus
	}{
		{
			name: "When stream metadata is nil it should set ValidPlatformImage to false",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtNodePoolPlatform{
							RootVolume: &hyperv1.KubevirtRootVolume{
								KubevirtVolume: hyperv1.KubevirtVolume{
									Type: hyperv1.KubevirtVolumeTypePersistent,
									Persistent: &hyperv1.KubevirtPersistentVolume{
										Size: func() *resource.Quantity { q := resource.MustParse("32Gi"); return &q }(),
									},
								},
							},
						},
					},
					Release: hyperv1.Release{Image: "quay.io/test:4.17"},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test"},
				Spec:       hyperv1.HostedClusterSpec{},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream:    &v1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.17.0"}},
				StreamMetadata: nil,
			},
			resolvedStream:    StreamRHEL9,
			expectError:       true,
			expectedCondType:  string(hyperv1.NodePoolValidPlatformImageType),
			expectedCondValue: corev1.ConditionFalse,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			r := &NodePoolReconciler{Client: fakeClient}
			err := r.setKubevirtConditions(t.Context(), tc.nodePool, tc.hostedCluster, "test-cp", tc.releaseImage, tc.resolvedStream)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			if tc.expectedCondType != "" {
				cond := FindStatusCondition(tc.nodePool.Status.Conditions, tc.expectedCondType)
				g.Expect(cond).ToNot(BeNil())
				g.Expect(cond.Status).To(Equal(tc.expectedCondValue))
			}
		})
	}
}

func TestSetPlatformConditions(t *testing.T) {
	t.Parallel()

	nilStreamRelease := &releaseinfo.ReleaseImage{
		ImageStream:    &v1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.17.0"}},
		StreamMetadata: nil,
	}

	testCases := []struct {
		name              string
		nodePool          *hyperv1.NodePool
		hostedCluster     *hyperv1.HostedCluster
		releaseImage      *releaseinfo.ReleaseImage
		resolvedStream    string
		expectError       bool
		expectedCondType  string
		expectedCondValue corev1.ConditionStatus
	}{
		{
			name: "When platform is AWS and image discovery fails it should return error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSNodePoolPlatform{}},
					Arch:     hyperv1.ArchitectureAMD64,
					Release:  hyperv1.Release{Image: "quay.io/test:4.17"},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{AWS: &hyperv1.AWSPlatformSpec{Region: "us-east-1"}},
				},
			},
			releaseImage:      nilStreamRelease,
			resolvedStream:    StreamRHEL9,
			expectError:       true,
			expectedCondType:  string(hyperv1.NodePoolValidPlatformImageType),
			expectedCondValue: corev1.ConditionFalse,
		},
		{
			name: "When platform is OpenStack and image discovery fails it should return error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{Type: hyperv1.OpenStackPlatform, OpenStack: &hyperv1.OpenStackNodePoolPlatform{}},
					Release:  hyperv1.Release{Image: "quay.io/test:4.17"},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{OpenStack: &hyperv1.OpenStackPlatformSpec{}},
				},
			},
			releaseImage:      nilStreamRelease,
			resolvedStream:    StreamRHEL9,
			expectError:       true,
			expectedCondType:  string(hyperv1.NodePoolValidPlatformImageType),
			expectedCondValue: corev1.ConditionFalse,
		},
		{
			name: "When platform is KubeVirt and image discovery fails it should return error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{Type: hyperv1.KubevirtPlatform, Kubevirt: &hyperv1.KubevirtNodePoolPlatform{
						RootVolume: &hyperv1.KubevirtRootVolume{
							KubevirtVolume: hyperv1.KubevirtVolume{
								Type: hyperv1.KubevirtVolumeTypePersistent,
								Persistent: &hyperv1.KubevirtPersistentVolume{
									Size: func() *resource.Quantity { q := resource.MustParse("32Gi"); return &q }(),
								},
							},
						},
					}},
					Release: hyperv1.Release{Image: "quay.io/test:4.17"},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test"},
				Spec:       hyperv1.HostedClusterSpec{},
			},
			releaseImage:      nilStreamRelease,
			resolvedStream:    StreamRHEL9,
			expectError:       true,
			expectedCondType:  string(hyperv1.NodePoolValidPlatformImageType),
			expectedCondValue: corev1.ConditionFalse,
		},
		{
			name: "When platform is PowerVS and image discovery fails it should return error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{Type: hyperv1.PowerVSPlatform, PowerVS: &hyperv1.PowerVSNodePoolPlatform{}},
					Release:  hyperv1.Release{Image: "quay.io/test:4.17"},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{PowerVS: &hyperv1.PowerVSPlatformSpec{Region: "us-south"}},
				},
			},
			releaseImage:      nilStreamRelease,
			resolvedStream:    StreamRHEL9,
			expectError:       true,
			expectedCondType:  string(hyperv1.NodePoolValidPlatformImageType),
			expectedCondValue: corev1.ConditionFalse,
		},
		{
			name: "When platform is unsupported it should return nil",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{Type: hyperv1.AgentPlatform},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{},
			releaseImage:  nilStreamRelease,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			r := &NodePoolReconciler{Client: fakeClient}
			err := r.setPlatformConditions(t.Context(), tc.hostedCluster, tc.nodePool, "test-cp", tc.releaseImage, tc.resolvedStream)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			if tc.expectedCondType != "" {
				cond := FindStatusCondition(tc.nodePool.Status.Conditions, tc.expectedCondType)
				g.Expect(cond).ToNot(BeNil())
				g.Expect(cond.Status).To(Equal(tc.expectedCondValue))
			}
		})
	}
}
