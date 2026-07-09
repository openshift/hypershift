package nodepool

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	haproxy "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/apiserver-haproxy"
	ignserver "github.com/openshift/hypershift/ignition-server/controllers"
	kvinfra "github.com/openshift/hypershift/kubevirtexternalinfra"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/releaseinfo"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/releaseinfo/fixtures"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/upsert"
	fakeimagemetadataprovider "github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	configv1 "github.com/openshift/api/config/v1"
	docker10 "github.com/openshift/api/image/docker10"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/coreos/stream-metadata-go/stream"
	"github.com/google/go-cmp/cmp"
	"github.com/vincent-petithory/dataurl"
)

func TestIsUpdatingConfig(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		target   string
		expect   bool
	}{
		{
			name: "it is not updating when strings match",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						nodePoolAnnotationCurrentConfig: "same",
					},
				},
			},
			target: "same",
			expect: false,
		},
		{
			name: "it is updating when strings does not match",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						nodePoolAnnotationCurrentConfig: "config1",
					},
				},
			},
			target: "config2",
			expect: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(isUpdatingConfig(tc.nodePool, tc.target)).To(Equal(tc.expect))
		})
	}
}

func TestIsUpdatingVersion(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		target   string
		expect   bool
	}{
		{
			name: "it is not updating when strings match",
			nodePool: &hyperv1.NodePool{
				Status: hyperv1.NodePoolStatus{
					Version: "same",
				},
			},
			target: "same",
			expect: false,
		},
		{
			name: "it is updating when strings does not match",
			nodePool: &hyperv1.NodePool{
				Status: hyperv1.NodePoolStatus{
					Version: "v1",
				},
			},
			target: "v2",
			expect: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(isUpdatingVersion(tc.nodePool, tc.target)).To(Equal(tc.expect))
		})
	}
}

func TestIsAutoscalingEnabled(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		expect   bool
	}{
		{
			name: "it is enabled when the struct is not nil and has no values",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](0),
						Max: 0,
					},
				},
			},
			expect: true,
		},
		{
			name: "it is enabled when the struct is not nil and has values",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](1),
						Max: 2,
					},
				},
			},
			expect: true,
		},
		{
			name: "it is not enabled when the struct is nil",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{},
			},
			expect: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(isAutoscalingEnabled(tc.nodePool)).To(Equal(tc.expect))
		})
	}
}

func TestValidateManagement(t *testing.T) {
	t.Parallel()
	intstrPointer1 := intstr.FromInt(1)
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		error    bool
	}{
		{
			name: "it fails with bad upgradeType",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						UpgradeType: "bad",
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy:      hyperv1.UpgradeStrategyRollingUpdate,
							RollingUpdate: nil,
						},
					},
				},
			},
			error: true,
		},
		{
			name: "it fails with Replace type and no Replace settings",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
					},
				},
			},
			error: true,
		},
		{
			name: "it fails with Replace type and bad strategy",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy: "bad",
							RollingUpdate: &hyperv1.RollingUpdate{
								MaxUnavailable: &intstrPointer1,
								MaxSurge:       &intstrPointer1,
							},
						},
					},
				},
			},
			error: true,
		},
		{
			name: "it fails with Replace type, RollingUpdate strategy and no rollingUpdate settings",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy:      hyperv1.UpgradeStrategyRollingUpdate,
							RollingUpdate: nil,
						},
					},
				},
			},
			error: true,
		},
		{
			name: "it passes with Replace type, RollingUpdate strategy and RollingUpdate settings",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy: hyperv1.UpgradeStrategyRollingUpdate,
							RollingUpdate: &hyperv1.RollingUpdate{
								MaxUnavailable: &intstrPointer1,
								MaxSurge:       &intstrPointer1,
							},
						},
					},
				},
			},
			error: false,
		},
		{
			name: "it passes with Replace type and OnDelete strategy",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy: hyperv1.UpgradeStrategyOnDelete,
						},
					},
				},
			},
			error: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			err := validateManagement(tc.nodePool)
			if tc.error {
				g.Expect(err).Should(HaveOccurred())
				return
			}
			g.Expect(err).ShouldNot(HaveOccurred())
		})
	}
}

func TestValidateInfraID(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	err := validateInfraID("")
	g.Expect(err).To(HaveOccurred())

	err = validateInfraID("123")
	g.Expect(err).ToNot(HaveOccurred())
}

func TestGetName(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	alphaNumeric := regexp.MustCompile(`^[a-z0-9]*$`)
	base := "infraID-clusterName" // length 19
	suffix := "nodePoolName"      // length 12
	length := len(base) + len(suffix)

	// When maxLength == base+suffix
	name := getName(base, suffix, length)
	g.Expect(alphaNumeric.MatchString(string(name[0]))).To(BeTrue())

	// When maxLength < base+suffix
	name = getName(base, suffix, length-1)
	g.Expect(alphaNumeric.MatchString(string(name[0]))).To(BeTrue())

	// When maxLength > base+suffix
	name = getName(base, suffix, length+1)
	g.Expect(alphaNumeric.MatchString(string(name[0]))).To(BeTrue())
}

func TestGetNodePoolNamespacedName(t *testing.T) {
	t.Parallel()
	testControlPlaneNamespace := "control-plane-ns"
	testNodePoolNamespace := "clusters"
	testNodePoolName := "nodepool-1"
	testCases := []struct {
		name                  string
		nodePoolName          string
		controlPlaneNamespace string
		hostedControlPlane    *hyperv1.HostedControlPlane
		expect                string
		error                 bool
	}{
		{
			name:                  "gets correct NodePool namespaced name",
			nodePoolName:          testNodePoolName,
			controlPlaneNamespace: testControlPlaneNamespace,
			hostedControlPlane: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testControlPlaneNamespace,
					Annotations: map[string]string{
						k8sutil.HostedClusterAnnotation: types.NamespacedName{Name: "hosted-cluster-1", Namespace: testNodePoolNamespace}.String(),
					},
				},
			},
			expect: types.NamespacedName{Name: testNodePoolName, Namespace: testNodePoolNamespace}.String(),
			error:  false,
		},
		{
			name:                  "fails if HostedControlPlane missing HostedClusterAnnotation",
			nodePoolName:          testNodePoolName,
			controlPlaneNamespace: testControlPlaneNamespace,
			hostedControlPlane: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testControlPlaneNamespace,
				},
			},
			expect: "",
			error:  true,
		},
		{
			name:                  "fails if HostedControlPlane does not exist",
			nodePoolName:          testNodePoolName,
			controlPlaneNamespace: testControlPlaneNamespace,
			hostedControlPlane:    nil,
			expect:                "",
			error:                 true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			var r NodePoolReconciler
			if tc.hostedControlPlane == nil {
				r = NodePoolReconciler{
					Client: fake.NewClientBuilder().WithObjects().Build(),
				}
			} else {
				r = NodePoolReconciler{
					Client: fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.hostedControlPlane).Build(),
				}
			}

			got, err := r.getNodePoolNamespacedName(testNodePoolName, testControlPlaneNamespace)

			if tc.error {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			if diff := cmp.Diff(got.String(), tc.expect); diff != "" {
				t.Errorf("actual NodePool namespaced name differs from expected: %s", diff)
				t.Logf("got: %s \n, expected: \n %s", got, tc.expect)
			}
		})
	}
}

func TestNodepoolDeletionDoesntRequireHCluster(t *testing.T) {
	t.Parallel()
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "some-nodepool",
			Namespace:  "clusters",
			Finalizers: []string{finalizer},
		},
	}

	ctx := t.Context()
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(nodePool).Build()
	if err := c.Delete(ctx, nodePool); err != nil {
		t.Fatalf("failed to delete nodepool: %v", err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(nodePool), nodePool); err != nil {
		t.Errorf("expected to be able to get nodepool after deletion because of finalizer, but got err: %v", err)
	}

	r := &NodePoolReconciler{
		Client:               c,
		KubevirtInfraClients: newKVInfraMapMock([]client.Object{nodePool}),
	}
	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nodePool)}); err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}

	if err := c.Get(ctx, client.ObjectKeyFromObject(nodePool), nodePool); !apierrors.IsNotFound(err) {
		t.Errorf("expected to get NotFound after deleted nodePool was reconciled, got %v", err)
	}
}

func TestCreateValidGeneratedPayloadCondition(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                    string
		tokenSecret             *corev1.Secret
		tokenSecretDoesNotExist bool
		expectedCondition       *hyperv1.NodePoolCondition
	}{
		{
			name: "when token secret is not found it should report it in the condition",
			tokenSecret: &corev1.Secret{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
			},
			tokenSecretDoesNotExist: true,
			expectedCondition: &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidGeneratedPayloadConditionType,
				Status:             corev1.ConditionFalse,
				Severity:           "",
				LastTransitionTime: metav1.Time{},
				Reason:             hyperv1.NodePoolNotFoundReason,
				Message:            "secrets \"test\" not found",
				ObservedGeneration: 1,
			},
		},
		{
			name: "when token secret has data it should report it in the condition",
			tokenSecret: &corev1.Secret{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Data: map[string][]byte{
					ignserver.TokenSecretReasonKey:  []byte(hyperv1.AsExpectedReason),
					ignserver.TokenSecretMessageKey: []byte("Payload generated successfully"),
				},
			},
			expectedCondition: &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidGeneratedPayloadConditionType,
				Status:             corev1.ConditionTrue,
				Severity:           "",
				LastTransitionTime: metav1.Time{},
				Reason:             hyperv1.AsExpectedReason,
				Message:            "Payload generated successfully",
				ObservedGeneration: 1,
			},
		},
		{
			name: "when token secret has no data it should report unknown in the condition",
			tokenSecret: &corev1.Secret{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Data: map[string][]byte{},
			},
			expectedCondition: &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidGeneratedPayloadConditionType,
				Status:             corev1.ConditionUnknown,
				Severity:           "",
				Reason:             "",
				Message:            "Unable to get status data from token secret",
				LastTransitionTime: metav1.Time{},
				ObservedGeneration: 1,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			var client client.Client
			if tc.tokenSecretDoesNotExist {
				client = fake.NewClientBuilder().WithObjects().Build()
			} else {
				client = fake.NewClientBuilder().WithObjects(tc.tokenSecret).Build()
			}

			r := NodePoolReconciler{
				Client: client,
			}

			got, err := r.createValidGeneratedPayloadCondition(t.Context(), tc.tokenSecret, 1)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(got).To(BeEquivalentTo(tc.expectedCondition))
		})
	}
}

func TestDefaultNodePoolAMI(t *testing.T) {
	t.Parallel()

	basicReleaseImage := &releaseinfo.ReleaseImage{
		StreamMetadata: &stream.Stream{
			Architectures: map[string]stream.Arch{
				"x86_64": {
					Images: stream.Images{
						Aws: &stream.AwsImage{
							Regions: map[string]stream.SingleImage{
								"us-east-1": {Release: "4.12.0", Image: "us-east-1-x86_64-image"},
							},
						},
					},
				},
				"aarch64": {
					Images: stream.Images{
						Aws: &stream.AwsImage{
							Regions: map[string]stream.SingleImage{
								"us-east-1": {Release: "4.12.0", Image: "us-east-1-aarch64-image"},
								"us-west-1": {Release: "4.12.0", Image: ""},
							},
						},
					},
				},
			},
		},
	}

	defaultStream, osStreams, err := releaseinfo.DeserializeImageMetadata(fixtures.CoreOSBootImagesYAML_5_0)
	if err != nil {
		t.Fatalf("failed to parse multi-stream fixture: %v", err)
	}
	multiStreamReleaseImage := &releaseinfo.ReleaseImage{StreamMetadata: defaultStream, OSStreams: osStreams}

	testCases := []struct {
		name          string
		region        string
		specifiedArch string
		streamName    string
		releaseImage  *releaseinfo.ReleaseImage
		expectedImage string
		expectedErr   string
	}{
		// --- Happy paths ---
		{
			name:          "When resolving amd64 AMI it should return the correct image",
			region:        "us-east-1",
			specifiedArch: "amd64",
			releaseImage:  basicReleaseImage,
			expectedImage: "us-east-1-x86_64-image",
		},
		{
			name:          "When resolving arm64 AMI it should return the correct image",
			region:        "us-east-1",
			specifiedArch: "arm64",
			releaseImage:  basicReleaseImage,
			expectedImage: "us-east-1-aarch64-image",
		},
		{
			name:          "When resolving rhel-9 stream it should return the rhel-9 AMI",
			region:        "us-east-1",
			specifiedArch: "amd64",
			streamName:    "rhel-9",
			releaseImage:  multiStreamReleaseImage,
			expectedImage: "ami-06a6b025350ff1e23",
		},
		{
			name:          "When resolving rhel-10 stream it should return the rhel-10 AMI",
			region:        "us-east-1",
			specifiedArch: "amd64",
			streamName:    "rhel-10",
			releaseImage:  multiStreamReleaseImage,
			expectedImage: "ami-04b3d999e39d62c5b",
		},
		{
			name:          "When resolving rhel-10 arm64 stream it should return the rhel-10 arm64 AMI",
			region:        "us-east-1",
			specifiedArch: "arm64",
			streamName:    "rhel-10",
			releaseImage:  multiStreamReleaseImage,
			expectedImage: "ami-0d7237e6b04d9a9e1",
		},
		{
			name:          "When using default stream it should return the default AMI",
			region:        "us-east-1",
			specifiedArch: "amd64",
			releaseImage:  multiStreamReleaseImage,
			expectedImage: "ami-06a6b025350ff1e23",
		},
		// --- Sad paths ---
		{
			name:          "When region is not found it should return error",
			region:        "us-east-2",
			specifiedArch: "amd64",
			releaseImage:  basicReleaseImage,
			expectedErr:   `couldn't find AWS image for region "us-east-2"`,
		},
		{
			name:          "When architecture is not found it should return error",
			region:        "us-east-1",
			specifiedArch: "s390x",
			releaseImage:  basicReleaseImage,
			expectedErr:   `couldn't find OS metadata for architecture "s390x"`,
		},
		{
			name:          "When image data is empty for region it should return error",
			region:        "us-west-1",
			specifiedArch: "arm64",
			releaseImage:  basicReleaseImage,
			expectedErr:   `release image metadata has no image for region "us-west-1"`,
		},
		{
			name:          "When stream metadata is nil it should return error",
			region:        "us-east-1",
			specifiedArch: "amd64",
			releaseImage:  &releaseinfo.ReleaseImage{StreamMetadata: nil},
			expectedErr:   "couldn't resolve stream metadata: no default stream metadata available",
		},
		{
			name:          "When release image is nil it should return error",
			region:        "us-east-1",
			specifiedArch: "amd64",
			releaseImage:  nil,
			expectedErr:   "release image is nil",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			image, err := defaultNodePoolAMI(tc.region, tc.specifiedArch, tc.streamName, tc.releaseImage)
			if tc.expectedErr != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(tc.expectedErr))
				g.Expect(image).To(BeEmpty())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(image).To(Equal(tc.expectedImage))
			}
		})
	}
}

func TestGetHostedClusterVersion(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                string
		versionStatus       *hyperv1.ClusterVersionStatus
		releaseImageVersion string
		expectedVersion     string
	}{
		{
			name:                "version history status is empty, should return release image version",
			releaseImageVersion: "4.15.0",
			expectedVersion:     "4.15.0",
		},
		{
			name: "version history status has a completed entry, should return the completed version",
			versionStatus: &hyperv1.ClusterVersionStatus{
				History: []configv1.UpdateHistory{
					{
						Version:        "4.14.0",
						CompletionTime: ptr.To(metav1.Now()),
					},
				},
			},
			releaseImageVersion: "4.15.0",
			expectedVersion:     "4.14.0",
		},
		{
			name: "version history status has no completed entries, should return release image version",
			versionStatus: &hyperv1.ClusterVersionStatus{
				History: []configv1.UpdateHistory{
					{
						Version:        "4.14.0",
						CompletionTime: nil,
					},
				},
			},
			releaseImageVersion: "4.15.0",
			expectedVersion:     "4.15.0",
		},
		{
			name: "version history status has multiple entries, should return the first completed version",
			versionStatus: &hyperv1.ClusterVersionStatus{
				History: []configv1.UpdateHistory{
					{
						Version:        "4.16.0",
						CompletionTime: nil,
					},
					{
						Version:        "4.15.0",
						CompletionTime: ptr.To(metav1.Now()),
					},
				},
			},
			releaseImageVersion: "4.16.0",
			expectedVersion:     "4.15.0",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			releaseProvider := &fakereleaseprovider.FakeReleaseProvider{
				Version: tc.releaseImageVersion,
			}
			r := NodePoolReconciler{
				ReleaseProvider: releaseProvider,
			}
			hc := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: "image",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: tc.versionStatus,
				},
			}

			version, err := r.getHostedClusterVersion(t.Context(), hc, nil)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(version).ToNot(BeNil())
			g.Expect(version.String()).To(Equal(tc.expectedVersion))
		})
	}
}

func TestFindMachineStatusCondition(t *testing.T) {
	for _, tc := range []struct {
		name          string
		machine       *capiv1.Machine
		conditionType string
		expected      *machineConditionResult
	}{
		{
			name: "When condition is False it should return the condition values",
			machine: &capiv1.Machine{
				Status: capiv1.MachineStatus{
					Conditions: []metav1.Condition{
						{
							Type:    capiv1.ReadyCondition,
							Status:  metav1.ConditionFalse,
							Reason:  "InstanceTerminated",
							Message: "i-0abc123def456 instance is in terminated state",
						},
					},
				},
			},
			conditionType: string(capiv1.ReadyCondition),
			expected: &machineConditionResult{
				Status:  metav1.ConditionFalse,
				Reason:  "InstanceTerminated",
				Message: "i-0abc123def456 instance is in terminated state",
			},
		},
		{
			name: "When neither has condition it should return nil",
			machine: &capiv1.Machine{
				Status: capiv1.MachineStatus{
					Conditions: []metav1.Condition{
						{
							Type:   capiv1.InfrastructureReadyCondition,
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			conditionType: string(capiv1.ReadyCondition),
			expected:      nil,
		},
		{
			name: "When condition is True it should return the condition values",
			machine: &capiv1.Machine{
				Status: capiv1.MachineStatus{
					Conditions: []metav1.Condition{
						{
							Type:    capiv1.ReadyCondition,
							Status:  metav1.ConditionTrue,
							Reason:  "InstanceProvisionStarted",
							Message: "started provisioning i-0abc123def456",
						},
					},
				},
			},
			conditionType: string(capiv1.ReadyCondition),
			expected: &machineConditionResult{
				Status:  metav1.ConditionTrue,
				Reason:  "InstanceProvisionStarted",
				Message: "started provisioning i-0abc123def456",
			},
		},
		{
			name: "When machine has no conditions it should return nil",
			machine: &capiv1.Machine{
				Status: capiv1.MachineStatus{
					Conditions: []metav1.Condition{},
				},
			},
			conditionType: string(capiv1.ReadyCondition),
			expected:      nil,
		},
		{
			name: "When looking up MachineNodeHealthyCondition it should return matching values",
			machine: &capiv1.Machine{
				Status: capiv1.MachineStatus{
					Conditions: []metav1.Condition{
						{
							Type:   capiv1.ReadyCondition,
							Status: metav1.ConditionTrue,
						},
						{
							Type:    capiv1.MachineNodeHealthyCondition,
							Status:  metav1.ConditionFalse,
							Reason:  capiv1.NodeConditionsFailedV1Beta1Reason,
							Message: "Condition Ready on node is reporting status False",
						},
					},
				},
			},
			conditionType: string(capiv1.MachineNodeHealthyCondition),
			expected: &machineConditionResult{
				Status:  metav1.ConditionFalse,
				Reason:  capiv1.NodeConditionsFailedV1Beta1Reason,
				Message: "Condition Ready on node is reporting status False",
			},
		},
	} {
		t.Run(tc.name, func(tt *testing.T) {
			g := NewWithT(tt)
			result := findMachineStatusCondition(tc.machine, tc.conditionType)
			if tc.expected == nil {
				g.Expect(result).To(BeNil())
			} else {
				g.Expect(result).ToNot(BeNil())
				g.Expect(result.Status).To(Equal(tc.expected.Status))
				g.Expect(result.Reason).To(Equal(tc.expected.Reason))
				g.Expect(result.Message).To(Equal(tc.expected.Message))
			}
		})
	}
}

type testCondition struct {
	Status        corev1.ConditionStatus
	Reason        string
	Messages      []string
	MaxMessageLen int // if > 0, assert that len(cond.Message) <= this value
	MaxReasonLen  int // if > 0, assert that len(cond.Reason) <= this value
}

func (t *testCondition) Compare(g Gomega, cond *hyperv1.NodePoolCondition) {
	if t == nil {
		return
	}

	g.Expect(cond.Status).To(Equal(t.Status))
	if t.Reason != "" {
		g.Expect(cond.Reason).To(Equal(t.Reason))
	}

	for _, msg := range t.Messages {
		g.ExpectWithOffset(1, cond.Message).To(ContainSubstring(msg))
	}

	if t.MaxMessageLen > 0 {
		g.ExpectWithOffset(1, len(cond.Message)).To(BeNumerically("<=", t.MaxMessageLen))
	}

	if t.MaxReasonLen > 0 {
		g.ExpectWithOffset(1, len(cond.Reason)).To(BeNumerically("<=", t.MaxReasonLen))
	}
}

func TestSetMachineAndNodeConditions(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	s := runtime.NewScheme()
	g.Expect(hyperv1.AddToScheme(s)).To(Succeed())
	g.Expect(capiv1.AddToScheme(s)).To(Succeed())

	for _, tc := range []struct {
		name                  string
		machinesGenerator     func() []client.Object
		expectedAllMachine    *testCondition
		expectedAllNodes      *testCondition
		expectedCIDRCollision *testCondition
	}{
		{
			name:              "no cluster-api machines",
			machinesGenerator: func() []client.Object { return nil },
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   hyperv1.NodePoolNotFoundReason,
				Messages: []string{"No Machines are created"},
			},
			expectedAllNodes: &testCondition{
				Status: corev1.ConditionFalse,
				Reason: hyperv1.NodePoolNotFoundReason,
			},
		},
		{
			name: "good machines",
			machinesGenerator: func() []client.Object {
				return []client.Object{
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: metav1.ConditionTrue,
								},
								{
									Type:   capiv1.MachineNodeHealthyCondition,
									Status: metav1.ConditionTrue,
								},
							},
						},
					},
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node2",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: metav1.ConditionTrue,
								},
								{
									Type:   capiv1.MachineNodeHealthyCondition,
									Status: metav1.ConditionTrue,
								},
							},
						},
					},
				}
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
		},
		{
			name: "no InfrastructureReady condition",
			machinesGenerator: func() []client.Object {
				return []client.Object{
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "test message node 1",
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "test message node 1",
								},
							},
						},
					},
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node2",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "test message node 2",
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "test message node 2",
								},
							},
						},
					},
				}
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   "TestReasonNode1,TestReasonNode2",
				Messages: []string{"2 of 2 machines are not ready", "Machine node1: TestReasonNode1", "Machine node2: TestReasonNode2"},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   "TestReasonNode1,TestReasonNode2",
				Messages: []string{"2 of 2 machines are not healthy", "Machine node1: TestReasonNode1: test message node 1", "Machine node2: TestReasonNode2: test message node 2"},
			},
		},
		{
			name: "mix InfrastructureReady condition; setup counter first",
			machinesGenerator: func() []client.Object {
				return []client.Object{
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
								"testDescription":  "message is setup counter",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "test message node 1",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "12 of 34 completed",
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "test message node 1",
								},
							},
						},
					},
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node2",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
								"testDescription":  "message some error text",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "test message node 2",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "some real failed message",
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "test message node 2",
								},
							},
						},
					},
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node3",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
								"testDescription":  "this machine is ready",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: metav1.ConditionTrue,
								},
								{
									Type:   capiv1.MachineNodeHealthyCondition,
									Status: metav1.ConditionTrue,
								},
							},
						},
					},
				}
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   "TestReasonNode1,TestReasonNode2",
				Messages: []string{"2 of 3 machines are not ready", "Machine node1: TestReasonNode1", "Machine node2: TestReasonNode2: some real failed message"},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   "TestReasonNode1,TestReasonNode2",
				Messages: []string{"2 of 3 machines are not healthy", "Machine node1: TestReasonNode1: test message node 1", "Machine node2: TestReasonNode2: test message node 2"},
			},
		},
		{
			name: "mix InfrastructureReady condition; failure text first",
			machinesGenerator: func() []client.Object {
				return []client.Object{
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
								"testDescription":  "message some error text",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "test message node 1",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "some real failed message",
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "test message node 1",
								},
							},
						},
					},
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node2",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
								"testDescription":  "message is setup counter",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "test message node 2",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "12 of 34 completed",
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "test message node 2",
								},
							},
						},
					},
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node3",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
								"testDescription":  "this machine is ready",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: metav1.ConditionTrue,
								},
								{
									Type:   capiv1.MachineNodeHealthyCondition,
									Status: metav1.ConditionTrue,
								},
							},
						},
					},
				}
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   "TestReasonNode1,TestReasonNode2",
				Messages: []string{"2 of 3 machines are not ready", "Machine node1: TestReasonNode1: some real failed message", "Machine node2: TestReasonNode2"},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   "TestReasonNode1,TestReasonNode2",
				Messages: []string{"2 of 3 machines are not healthy", "Machine node1: TestReasonNode1: test message node 1", "Machine node2: TestReasonNode2: test message node 2"},
			},
		},
		{
			name: "too many not ready machines",
			machinesGenerator: func() []client.Object {
				longMessage := strings.Repeat("msg ", 50)

				machines := make([]client.Object, 15) // two reasons with 5 machine each (too long message), one reason with only 3 machines, and 2 ready machines

				i := 0

				for ; i < 5; i++ { // create 5 machine. should exceed max message length
					machines[i] = &capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      fmt.Sprintf("node%d", i),
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "not ready",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: longMessage,
								},
							},
						},
					}
				}
				for ; i < 8; i++ { // create 3 machine. should not exceed max message length
					machines[i] = &capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      fmt.Sprintf("node%d", i),
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "not ready",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: longMessage,
								},
							},
						},
					}
				}
				for ; i < 13; i++ { // create 5 machine. should exceed max message length
					machines[i] = &capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      fmt.Sprintf("node%d", i),
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode3",
									Message: "not ready",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "TestReasonNode3",
									Message: longMessage,
								},
							},
						},
					}
				}
				for ; i < 15; i++ { // 2 ready machines
					machines[i] = &capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      fmt.Sprintf("node%d", i),
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: metav1.ConditionTrue,
								},
							},
						},
					}
				}

				return machines
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   "TestReasonNode1,TestReasonNode2,TestReasonNode3",
				Messages: []string{"13 of 15 machines are not ready", endOfMessage},
			},
		},
		{
			name: "machine cidr collision",
			machinesGenerator: func() []client.Object {
				return []client.Object{
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: metav1.ConditionTrue,
								},
								{
									Type:   capiv1.MachineNodeHealthyCondition,
									Status: metav1.ConditionTrue,
								},
							},
							Addresses: capiv1.MachineAddresses{
								{
									Type:    capiv1.MachineInternalIP,
									Address: "10.10.10.5",
								},
							},
						},
					},
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node2",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: metav1.ConditionTrue,
								},
								{
									Type:   capiv1.MachineNodeHealthyCondition,
									Status: metav1.ConditionTrue,
								},
							},
							Addresses: capiv1.MachineAddresses{
								{
									Type:    capiv1.MachineInternalIP,
									Address: "10.10.10.6",
								},
							},
						},
					},
				}
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
			expectedCIDRCollision: &testCondition{
				Status: corev1.ConditionTrue,
				Reason: hyperv1.InvalidConfigurationReason,
				Messages: []string{
					"machine [node1] with ip [10.10.10.5] collides with cluster-network cidr [10.10.10.0/14]",
					"machine [node2] with ip [10.10.10.6] collides with cluster-network cidr [10.10.10.0/14]",
				},
			},
		},
		{
			name: "When machines have no NodeHealthy condition it should report WaitingForNodeRef",
			machinesGenerator: func() []client.Object {
				return []client.Object{
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: metav1.ConditionTrue,
								},
							},
						},
					},
				}
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   capiv1.WaitingForNodeRefV1Beta1Reason,
				Messages: []string{"1 of 1 machines are not healthy", "Machine node1: WaitingForNodeRef"},
			},
		},
		{
			name: "When machines have no Ready condition it should report WaitingForInfrastructure",
			machinesGenerator: func() []client.Object {
				return []client.Object{
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:   capiv1.MachineNodeHealthyCondition,
									Status: metav1.ConditionTrue,
								},
							},
						},
					},
				}
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   capiv1.WaitingForInfrastructureFallbackV1Beta1Reason,
				Messages: []string{"1 of 1 machines are not ready", "Machine node1: WaitingForInfrastructure"},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
		},
		{
			name: "When machine has NodeHealthy False with empty message it should report reason only",
			machinesGenerator: func() []client.Object {
				return []client.Object{
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: metav1.ConditionTrue,
								},
								{
									Type:   capiv1.MachineNodeHealthyCondition,
									Status: metav1.ConditionFalse,
									Reason: capiv1.NodeProvisioningV1Beta1Reason,
								},
							},
						},
					},
				}
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   capiv1.NodeProvisioningV1Beta1Reason,
				Messages: []string{"1 of 1 machines are not healthy", "Machine node1: " + capiv1.NodeProvisioningV1Beta1Reason},
			},
		},
		{
			name: "When machines mix nil and False NodeHealthy it should aggregate both",
			machinesGenerator: func() []client.Object {
				return []client.Object{
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: metav1.ConditionTrue,
								},
							},
						},
					},
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node2",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: metav1.ConditionTrue,
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  capiv1.NodeConditionsFailedV1Beta1Reason,
									Message: "Condition Ready on node is reporting status False",
								},
							},
						},
					},
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node3",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: metav1.ConditionTrue,
								},
								{
									Type:   capiv1.MachineNodeHealthyCondition,
									Status: metav1.ConditionTrue,
								},
							},
						},
					},
				}
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   capiv1.NodeConditionsFailedV1Beta1Reason + "," + capiv1.WaitingForNodeRefV1Beta1Reason,
				Messages: []string{"2 of 3 machines are not healthy", "Machine node1: WaitingForNodeRef", "Machine node2: " + capiv1.NodeConditionsFailedV1Beta1Reason + ": Condition Ready on node is reporting status False"},
			},
		},
		{
			name: "When machine has Ready False with non-counter message and no InfraReady it should include message",
			machinesGenerator: func() []client.Object {
				return []client.Object{
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "InstanceTerminated",
									Message: "i-0abc123def456 instance is in terminated state",
								},
								{
									Type:   capiv1.MachineNodeHealthyCondition,
									Status: metav1.ConditionTrue,
								},
							},
						},
					},
				}
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   "InstanceTerminated",
				Messages: []string{"1 of 1 machines are not ready", "Machine node1: InstanceTerminated: i-0abc123def456 instance is in terminated state"},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
		},
		{
			name: "When machine has Ready False with setup-counter message and no InfraReady it should omit message",
			machinesGenerator: func() []client.Object {
				return []client.Object{
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  "InstanceProvisionStarted",
									Message: "3 of 7 completed",
								},
								{
									Type:   capiv1.MachineNodeHealthyCondition,
									Status: metav1.ConditionTrue,
								},
							},
						},
					},
				}
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   "InstanceProvisionStarted",
				Messages: []string{"1 of 1 machines are not ready", "Machine node1: InstanceProvisionStarted"},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
		},
		{
			name: "When machines mix nil and False Ready conditions it should aggregate both",
			machinesGenerator: func() []client.Object {
				return []client.Object{
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:   capiv1.MachineNodeHealthyCondition,
									Status: metav1.ConditionTrue,
								},
							},
						},
					},
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node2",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  capiv1.MachineHasFailureV1Beta1Reason,
									Message: "Machine has FailureMessage: i-0abc123def456 is in terminated state",
								},
								{
									Type:   capiv1.MachineNodeHealthyCondition,
									Status: metav1.ConditionTrue,
								},
							},
						},
					},
				}
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   capiv1.MachineHasFailureV1Beta1Reason + "," + capiv1.WaitingForInfrastructureFallbackV1Beta1Reason,
				Messages: []string{"2 of 2 machines are not ready", "Machine node1: WaitingForInfrastructure", "Machine node2: " + capiv1.MachineHasFailureV1Beta1Reason + ": Machine has FailureMessage: i-0abc123def456 is in terminated state"},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
		},
		{
			name: "When many machines fail across many reasons it should truncate global message",
			machinesGenerator: func() []client.Object {
				longMsg := strings.Repeat("x", 80)
				// Create 10 distinct reasons with 20 machines each = 200 failing machines.
				// Each per-reason block can reach ~1000 chars, and 10 blocks would produce
				// ~10000 chars total, well above maxGlobalMessageLength (3000).
				numReasons := 10
				machinesPerReason := 20
				total := numReasons * machinesPerReason
				machines := make([]client.Object, total)
				for r := range numReasons {
					for m := range machinesPerReason {
						idx := r*machinesPerReason + m
						machines[idx] = &capiv1.Machine{
							ObjectMeta: metav1.ObjectMeta{
								Name:      fmt.Sprintf("node-%d-%d", r, m),
								Namespace: "myns-cluster-name",
								Annotations: map[string]string{
									nodePoolAnnotation: "myns/np-name",
								},
							},
							Status: capiv1.MachineStatus{
								Conditions: []metav1.Condition{
									{
										Type:    capiv1.ReadyCondition,
										Status:  metav1.ConditionFalse,
										Reason:  fmt.Sprintf("Reason%02d", r),
										Message: longMsg,
									},
									{
										Type:    capiv1.MachineNodeHealthyCondition,
										Status:  metav1.ConditionFalse,
										Reason:  fmt.Sprintf("Reason%02d", r),
										Message: longMsg,
									},
								},
							},
						}
					}
				}
				return machines
			},
			expectedAllMachine: &testCondition{
				Status:        corev1.ConditionFalse,
				Messages:      []string{"200 of 200 machines are not ready", endOfGlobalMessage},
				MaxMessageLen: maxGlobalMessageLength,
			},
			expectedAllNodes: &testCondition{
				Status:        corev1.ConditionFalse,
				Messages:      []string{"200 of 200 machines are not healthy", endOfGlobalMessage},
				MaxMessageLen: maxGlobalMessageLength,
			},
		},
		{
			name: "When machines fail with many distinct reasons it should truncate the reason field to fit MaxLength=1024",
			machinesGenerator: func() []client.Object {
				// Use real CAPI v1beta1 condition reasons plus realistic cloud-provider reasons.
				// 48 distinct reasons produce a comma-joined string of ~1033 chars, exceeding
				// the NodePoolCondition.Reason MaxLength=1024 validation limit.
				failureReasons := []struct {
					reason  string
					message string
				}{
					{capiv1.WaitingForInfrastructureFallbackV1Beta1Reason, ""},
					{capiv1.MachineHasFailureV1Beta1Reason, "Machine has FailureReason: InsufficientCapacity"},
					{capiv1.DeletingV1Beta1Reason, "Waiting for machine volumes to be detached"},
					{capiv1.DeletionFailedV1Beta1Reason, "failed to delete machine"},
					{capiv1.DrainingV1Beta1Reason, "Draining node node-4"},
					{capiv1.DrainingFailedV1Beta1Reason, "failed to drain node: cannot evict pod"},
					{capiv1.WaitingForVolumeDetachV1Beta1Reason, "Waiting for 2 volumes to be detached"},
					{capiv1.WaitingExternalHookV1Beta1Reason, "Waiting for external hook to complete"},
					{capiv1.PreflightCheckFailedV1Beta1Reason, "Machine pre-flight checks failed"},
					{capiv1.MachineCreationFailedV1Beta1Reason, "failed to create machine: quota exceeded"},
					{capiv1.ScalingUpV1Beta1Reason, "Scaling up to 10 replicas"},
					{capiv1.ScalingDownV1Beta1Reason, "Scaling down to 5 replicas"},
					{capiv1.WaitingForDataSecretFallbackV1Beta1Reason, ""},
					{capiv1.WaitingForControlPlaneFallbackV1Beta1Reason, ""},
					{capiv1.WaitingForControlPlaneAvailableV1Beta1Reason, "Control plane is not available"},
					{capiv1.BootstrapTemplateCloningFailedV1Beta1Reason, "failed to clone bootstrap template"},
					{capiv1.InfrastructureTemplateCloningFailedV1Beta1Reason, "failed to clone infrastructure template"},
					{capiv1.IncorrectExternalRefV1Beta1Reason, "external ref is incorrect"},
					{capiv1.RemediationFailedV1Beta1Reason, "remediation failed"},
					{capiv1.RemediationInProgressV1Beta1Reason, "remediation in progress for node-18"},
					{capiv1.WaitingForRemediationV1Beta1Reason, "waiting for remediation to complete"},
					{capiv1.NodeStartupTimeoutV1Beta1Reason, "Node failed to report NodeReady condition within 20m0s"},
					{capiv1.WaitingForNodeRefV1Beta1Reason, ""},
					{capiv1.NodeProvisioningV1Beta1Reason, "Node is provisioning"},
					{capiv1.NodeNotFoundV1Beta1Reason, "node not found in cluster"},
					{capiv1.NodeConditionsFailedV1Beta1Reason, "Condition Ready on node is reporting status False"},
					{capiv1.NodeInspectionFailedV1Beta1Reason, "failed to inspect node"},
					{capiv1.UnhealthyNodeConditionV1Beta1Reason, "Node condition ReadonlyFilesystem is True"},
					{capiv1.HasRemediateMachineAnnotationV1Beta1Reason, "machine has remediate annotation"},
					{capiv1.TooManyUnhealthyV1Beta1Reason, "too many unhealthy machines: 10 of 20"},
					{capiv1.ExternalRemediationTemplateNotFoundV1Beta1Reason, "external remediation template not found"},
					{capiv1.ExternalRemediationRequestCreationFailedV1Beta1Reason, "failed to create external remediation request"},
					{capiv1.WaitingForControlPlaneProviderInitializedV1Beta1Reason, "control plane provider is not initialized"},
					{capiv1.MissingNodeRefV1Beta1Reason, "machine does not have a node ref"},
					// Realistic cloud-provider reasons (CAPA/CAPZ) — already used in existing tests.
					{"InstanceTerminated", "i-0abc123def456 instance is in terminated state"},
					{"InstanceProvisionFailed", "failed to create instance: InsufficientInstanceCapacity"},
					{"InstanceProvisionStarted", "3 of 7 completed"},
					{"InsufficientCapacity", "not enough capacity in az us-east-1a"},
					{"SubnetExhausted", "no available IPs in subnet-0abc123"},
					{"SecurityGroupNotFound", "security group sg-0abc123 not found"},
					{"AMINotFound", "AMI ami-0abc123 not found"},
					{"VPCNotAvailable", "VPC vpc-0abc123 is not available"},
					{"LaunchTemplateFailed", "failed to create launch template"},
					{"SpotInstanceTerminated", "spot instance i-0abc123 was terminated"},
					{"NetworkInterfaceLimitExceeded", "ENI limit reached for instance type m5.xlarge"},
					{"EBSVolumeLimitExceeded", "EBS volume limit exceeded in us-east-1a"},
					{"InsufficientInstanceCapacity", "not enough m5.xlarge capacity in us-east-1b"},
					{"UnsupportedInstanceType", "instance type m5.metal not supported in us-east-1c"},
				}

				machines := make([]client.Object, len(failureReasons))
				for i, fr := range failureReasons {
					machines[i] = &capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      fmt.Sprintf("machine-%d", i),
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  fr.reason,
									Message: fr.message,
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  fr.reason,
									Message: fr.message,
								},
							},
						},
					}
				}
				return machines
			},
			expectedAllMachine: &testCondition{
				Status:       corev1.ConditionFalse,
				MaxReasonLen: maxReasonLength,
				Messages:     []string{fmt.Sprintf("%d of %d machines are not ready", 48, 48)},
			},
			expectedAllNodes: &testCondition{
				Status:       corev1.ConditionFalse,
				MaxReasonLen: maxReasonLength,
				Messages:     []string{fmt.Sprintf("%d of %d machines are not healthy", 48, 48)},
			},
		},
		{
			name: "When 10 of 20 machines are not ready with different reasons it should aggregate correctly",
			machinesGenerator: func() []client.Object {
				machines := make([]client.Object, 20)
				// Use real CAPI v1beta1 and AWS CAPA reasons with realistic messages.
				failureReasons := []struct {
					reason  string
					message string
				}{
					{capiv1.NodeStartupTimeoutV1Beta1Reason, "Node failed to report NodeReady condition within 20m0s"},
					{capiv1.NodeStartupTimeoutV1Beta1Reason, "Node failed to report NodeReady condition within 20m0s"},
					{capiv1.MachineHasFailureV1Beta1Reason, "Machine has FailureReason: InsufficientCapacity"},
					{capiv1.MachineHasFailureV1Beta1Reason, "Machine has FailureMessage: i-0abc123def456 is in terminated state"},
					{"InstanceTerminated", "i-0abc123def456 instance is in terminated state"},
					{"InstanceTerminated", "i-0def456abc789 instance is in terminated state"},
					{"InstanceProvisionFailed", "failed to create instance: InsufficientInstanceCapacity: We currently do not have sufficient capacity in the Availability Zone you requested"},
					{"InstanceProvisionFailed", "failed to create instance: Unsupported: The requested configuration is currently not supported"},
					{capiv1.WaitingForInfrastructureFallbackV1Beta1Reason, ""},
					{capiv1.WaitingForInfrastructureFallbackV1Beta1Reason, ""},
				}
				for i := range 10 {
					machines[i] = &capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      fmt.Sprintf("failing-node-%d", i),
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  metav1.ConditionFalse,
									Reason:  failureReasons[i].reason,
									Message: failureReasons[i].message,
								},
								{
									Type:   capiv1.MachineNodeHealthyCondition,
									Status: metav1.ConditionTrue,
								},
							},
						},
					}
				}
				for i := range 10 {
					machines[10+i] = &capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      fmt.Sprintf("healthy-node-%d", i),
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: metav1.ConditionTrue,
								},
								{
									Type:   capiv1.MachineNodeHealthyCondition,
									Status: metav1.ConditionTrue,
								},
							},
						},
					}
				}
				return machines
			},
			expectedAllMachine: &testCondition{
				Status: corev1.ConditionFalse,
				Reason: "InstanceProvisionFailed,InstanceTerminated,MachineHasFailure,NodeStartupTimeout,WaitingForInfrastructure",
				Messages: []string{
					"10 of 20 machines are not ready",
					"Machine failing-node-0: NodeStartupTimeout: Node failed to report NodeReady condition within 20m0s",
					"Machine failing-node-2: MachineHasFailure: Machine has FailureReason: InsufficientCapacity",
					"Machine failing-node-4: InstanceTerminated: i-0abc123def456 instance is in terminated state",
					"Machine failing-node-6: InstanceProvisionFailed: failed to create instance: InsufficientInstanceCapacity",
					"Machine failing-node-8: WaitingForInfrastructure",
				},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
		},
		{
			name: "When machine has addresses both inside and outside cluster network it should not report cidr collision",
			machinesGenerator: func() []client.Object {
				return []client.Object{
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []capiv1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: corev1.ConditionTrue,
								},
								{
									Type:   capiv1.MachineNodeHealthyCondition,
									Status: corev1.ConditionTrue,
								},
							},
							Addresses: capiv1.MachineAddresses{
								{
									Type:    capiv1.MachineInternalIP,
									Address: "192.168.1.10",
								},
								{
									Type:    capiv1.MachineExternalIP,
									Address: "192.168.1.10",
								},
								{
									Type:    capiv1.MachineInternalIP,
									Address: "10.10.10.2",
								},
								{
									Type:    capiv1.MachineExternalIP,
									Address: "10.10.10.2",
								},
							},
						},
					},
				}
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
			expectedCIDRCollision: nil,
		},
		{
			name: "When machine has out-of-cluster address alongside link-local and in-cluster addresses it should not report cidr collision",
			machinesGenerator: func() []client.Object {
				return []client.Object{
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []capiv1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: corev1.ConditionTrue,
								},
								{
									Type:   capiv1.MachineNodeHealthyCondition,
									Status: corev1.ConditionTrue,
								},
							},
							Addresses: capiv1.MachineAddresses{
								{
									Type:    capiv1.MachineInternalIP,
									Address: "192.168.1.10",
								},
								{
									Type:    capiv1.MachineInternalIP,
									Address: "169.254.0.2",
								},
								{
									Type:    capiv1.MachineInternalIP,
									Address: "fe80::1",
								},
								{
									Type:    capiv1.MachineInternalIP,
									Address: "10.10.10.2",
								},
							},
						},
					},
				}
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
			expectedCIDRCollision: nil,
		},
	} {
		t.Run(tc.name, func(tt *testing.T) {
			gg := NewWithT(tt)
			r := NodePoolReconciler{
				Client: fake.NewClientBuilder().WithScheme(s).WithObjects(tc.machinesGenerator()...).Build(),
			}

			np := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "np-name", Namespace: "myns"},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "cluster-name",
				},
			}

			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster-name", Namespace: "myns"},
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{
								CIDR: *ipnet.MustParseCIDR("10.10.10.0/14"),
							},
						},
					},
				},
			}

			gg.Expect(r.setMachineAndNodeConditions(t.Context(), np, hc)).To(Succeed())

			cond := FindStatusCondition(np.Status.Conditions, hyperv1.NodePoolAllMachinesReadyConditionType)
			gg.Expect(cond).ToNot(BeNil())
			tc.expectedAllMachine.Compare(gg, cond)

			cond = FindStatusCondition(np.Status.Conditions, hyperv1.NodePoolAllNodesHealthyConditionType)
			gg.Expect(cond).ToNot(BeNil())
			tc.expectedAllNodes.Compare(gg, cond)

			cond = FindStatusCondition(np.Status.Conditions, hyperv1.NodePoolClusterNetworkCIDRConflictType)
			if tc.expectedCIDRCollision == nil {
				gg.Expect(cond).To(BeNil())
			} else {
				gg.Expect(cond).ToNot(BeNil())
				tc.expectedCIDRCollision.Compare(gg, cond)
			}
		})
	}
}

func TestTruncateReasons(t *testing.T) {
	g := NewWithT(t)

	for _, tc := range []struct {
		name           string
		reasons        []string
		expectSuffix   string
		expectMaxLen   int
		expectExactLen int // if > 0, expect exact length
	}{
		{
			name:         "When reasons fit within limit it should return them unchanged",
			reasons:      []string{capiv1.MachineHasFailureV1Beta1Reason, capiv1.NodeConditionsFailedV1Beta1Reason, capiv1.WaitingForNodeRefV1Beta1Reason},
			expectSuffix: capiv1.WaitingForNodeRefV1Beta1Reason,
			expectMaxLen: maxReasonLength,
		},
		{
			name:         "When a single reason fits within limit it should return it unchanged",
			reasons:      []string{capiv1.ExternalRemediationRequestCreationFailedV1Beta1Reason},
			expectSuffix: capiv1.ExternalRemediationRequestCreationFailedV1Beta1Reason,
			expectMaxLen: maxReasonLength,
		},
		{
			name:           "When empty reasons it should return empty string",
			reasons:        []string{},
			expectExactLen: 0,
		},
		{
			name: "When many CAPI reasons exceed limit it should truncate with ReasonsTruncated suffix",
			reasons: []string{
				capiv1.WaitingForInfrastructureFallbackV1Beta1Reason,
				capiv1.MachineHasFailureV1Beta1Reason,
				capiv1.DeletingV1Beta1Reason,
				capiv1.DeletionFailedV1Beta1Reason,
				capiv1.DrainingV1Beta1Reason,
				capiv1.DrainingFailedV1Beta1Reason,
				capiv1.WaitingForVolumeDetachV1Beta1Reason,
				capiv1.WaitingExternalHookV1Beta1Reason,
				capiv1.PreflightCheckFailedV1Beta1Reason,
				capiv1.MachineCreationFailedV1Beta1Reason,
				capiv1.ScalingUpV1Beta1Reason,
				capiv1.ScalingDownV1Beta1Reason,
				capiv1.WaitingForDataSecretFallbackV1Beta1Reason,
				capiv1.WaitingForControlPlaneFallbackV1Beta1Reason,
				capiv1.WaitingForControlPlaneAvailableV1Beta1Reason,
				capiv1.BootstrapTemplateCloningFailedV1Beta1Reason,
				capiv1.InfrastructureTemplateCloningFailedV1Beta1Reason,
				capiv1.IncorrectExternalRefV1Beta1Reason,
				capiv1.RemediationFailedV1Beta1Reason,
				capiv1.RemediationInProgressV1Beta1Reason,
				capiv1.WaitingForRemediationV1Beta1Reason,
				capiv1.NodeStartupTimeoutV1Beta1Reason,
				capiv1.WaitingForNodeRefV1Beta1Reason,
				capiv1.NodeProvisioningV1Beta1Reason,
				capiv1.NodeNotFoundV1Beta1Reason,
				capiv1.NodeConditionsFailedV1Beta1Reason,
				capiv1.NodeInspectionFailedV1Beta1Reason,
				capiv1.UnhealthyNodeConditionV1Beta1Reason,
				capiv1.HasRemediateMachineAnnotationV1Beta1Reason,
				capiv1.TooManyUnhealthyV1Beta1Reason,
				capiv1.ExternalRemediationTemplateNotFoundV1Beta1Reason,
				capiv1.ExternalRemediationRequestCreationFailedV1Beta1Reason,
				capiv1.WaitingForControlPlaneProviderInitializedV1Beta1Reason,
				capiv1.MissingNodeRefV1Beta1Reason,
				// Realistic cloud-provider reasons (CAPA/CAPZ).
				"InstanceTerminated",
				"InstanceProvisionFailed",
				"InstanceProvisionStarted",
				"InsufficientCapacity",
				"SubnetExhausted",
				"SecurityGroupNotFound",
				"AMINotFound",
				"VPCNotAvailable",
				"LaunchTemplateFailed",
				"SpotInstanceTerminated",
				"NetworkInterfaceLimitExceeded",
				"EBSVolumeLimitExceeded",
				"InsufficientInstanceCapacity",
				"UnsupportedInstanceType",
			},
			expectSuffix: endOfReasons,
			expectMaxLen: maxReasonLength,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := truncateReasons(tc.reasons)

			if tc.expectMaxLen > 0 {
				g.Expect(len(result)).To(BeNumerically("<=", tc.expectMaxLen),
					"reason length %d exceeds max %d: %s", len(result), tc.expectMaxLen, result)
			}
			if tc.expectExactLen > 0 {
				g.Expect(len(result)).To(Equal(tc.expectExactLen))
			}
			if tc.expectSuffix != "" {
				g.Expect(result).To(HaveSuffix(tc.expectSuffix))
			}
		})
	}
}

func TestAggregateMachineReasonsAndMessages(t *testing.T) {
	g := NewWithT(t)

	for _, tc := range []struct {
		name              string
		messageMap        map[string][]string
		numMachines       int
		numNotReady       int
		state             string
		expectReason      string
		expectMessages    []string
		expectNotContains []string
		expectMaxMsgLen   int
	}{
		{
			name: "When a single machine fails it should return its reason and message",
			messageMap: map[string][]string{
				capiv1.NodeConditionsFailedV1Beta1Reason: {
					"Machine machine-0: NodeConditionsFailed: Condition Ready on node is reporting status False\n",
				},
			},
			numMachines:    3,
			numNotReady:    1,
			state:          aggregatorMachineStateHealthy,
			expectReason:   capiv1.NodeConditionsFailedV1Beta1Reason,
			expectMessages: []string{"1 of 3 machines are not healthy", "Machine machine-0: NodeConditionsFailed: Condition Ready on node is reporting status False"},
		},
		{
			name: "When machines fail with two reasons it should join reasons with comma",
			messageMap: map[string][]string{
				capiv1.MachineHasFailureV1Beta1Reason: {
					"Machine machine-0: MachineHasFailure: Machine has FailureReason: InsufficientCapacity\n",
				},
				capiv1.WaitingForInfrastructureFallbackV1Beta1Reason: {
					"Machine machine-1: WaitingForInfrastructure\n",
				},
			},
			numMachines:    5,
			numNotReady:    2,
			state:          aggregatorMachineStateReady,
			expectReason:   capiv1.MachineHasFailureV1Beta1Reason + "," + capiv1.WaitingForInfrastructureFallbackV1Beta1Reason,
			expectMessages: []string{"2 of 5 machines are not ready", "Machine machine-0: MachineHasFailure", "Machine machine-1: WaitingForInfrastructure"},
		},
		{
			name: "When many machines share one reason it should truncate per-reason messages",
			messageMap: func() map[string][]string {
				msgs := make([]string, 30)
				for i := range 30 {
					msgs[i] = fmt.Sprintf("Machine machine-%d: MachineHasFailure: Machine has FailureMessage: i-%012d is in terminated state\n", i, i)
				}
				return map[string][]string{
					capiv1.MachineHasFailureV1Beta1Reason: msgs,
				}
			}(),
			numMachines:    30,
			numNotReady:    30,
			state:          aggregatorMachineStateReady,
			expectReason:   capiv1.MachineHasFailureV1Beta1Reason,
			expectMessages: []string{"30 of 30 machines are not ready", "Machine machine-0: MachineHasFailure", endOfMessage},
		},
		{
			name: "When many reasons produce large message blocks it should truncate global message",
			messageMap: func() map[string][]string {
				m := make(map[string][]string)
				// 10 distinct reasons, each with 20 machines producing ~1000 char blocks.
				reasons := []string{
					capiv1.MachineHasFailureV1Beta1Reason,
					capiv1.NodeConditionsFailedV1Beta1Reason,
					capiv1.WaitingForInfrastructureFallbackV1Beta1Reason,
					capiv1.DeletingV1Beta1Reason,
					capiv1.DrainingV1Beta1Reason,
					capiv1.NodeStartupTimeoutV1Beta1Reason,
					capiv1.WaitingForNodeRefV1Beta1Reason,
					capiv1.RemediationInProgressV1Beta1Reason,
					capiv1.PreflightCheckFailedV1Beta1Reason,
					capiv1.MachineCreationFailedV1Beta1Reason,
				}
				longMsg := strings.Repeat("x", 80)
				machineIdx := 0
				for _, reason := range reasons {
					msgs := make([]string, 20)
					for j := range 20 {
						msgs[j] = fmt.Sprintf("Machine machine-%d: %s: %s\n", machineIdx, reason, longMsg)
						machineIdx++
					}
					m[reason] = msgs
				}
				return m
			}(),
			numMachines:     200,
			numNotReady:     200,
			state:           aggregatorMachineStateReady,
			expectMessages:  []string{"200 of 200 machines are not ready", endOfGlobalMessage},
			expectMaxMsgLen: maxGlobalMessageLength,
		},
		{
			name: "When messages within a reason are unsorted it should sort them deterministically",
			messageMap: map[string][]string{
				capiv1.NodeConditionsFailedV1Beta1Reason: {
					"Machine machine-2: NodeConditionsFailed: Condition MemoryPressure is True\n",
					"Machine machine-0: NodeConditionsFailed: Condition Ready is False\n",
					"Machine machine-1: NodeConditionsFailed: Condition DiskPressure is True\n",
				},
			},
			numMachines:  5,
			numNotReady:  3,
			state:        aggregatorMachineStateHealthy,
			expectReason: capiv1.NodeConditionsFailedV1Beta1Reason,
			// After sorting, machine-0 should come first, then machine-1, then machine-2.
			expectMessages: []string{
				"3 of 5 machines are not healthy",
				"Machine machine-0: NodeConditionsFailed: Condition Ready is False",
				"Machine machine-1: NodeConditionsFailed: Condition DiskPressure is True",
				"Machine machine-2: NodeConditionsFailed: Condition MemoryPressure is True",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reason, message := aggregateMachineReasonsAndMessages(tc.messageMap, tc.numMachines, tc.numNotReady, tc.state)

			if tc.expectReason != "" {
				g.Expect(reason).To(Equal(tc.expectReason))
			}
			for _, msg := range tc.expectMessages {
				g.Expect(message).To(ContainSubstring(msg))
			}
			for _, msg := range tc.expectNotContains {
				g.Expect(message).ToNot(ContainSubstring(msg))
			}
			if tc.expectMaxMsgLen > 0 {
				g.Expect(len(message)).To(BeNumerically("<=", tc.expectMaxMsgLen),
					"message length %d exceeds max %d", len(message), tc.expectMaxMsgLen)
			}
		})
	}
}

// TestSetCIDRConflictConditionDualStack tests setCIDRConflictCondition directly
// (unit-level isolation) while TestSetMachineAndNodeConditions above exercises it
// indirectly through the parent setMachineAndNodeConditions flow (integration-level).
// Both exist because the dual-stack and condition-clearing scenarios are easier to
// express with direct calls, while the integration cases verify end-to-end wiring.
func TestSetCIDRConflictConditionDualStack(t *testing.T) {
	t.Parallel()
	r := NodePoolReconciler{}

	for _, tc := range []struct {
		name                  string
		clusterNetwork        []hyperv1.ClusterNetworkEntry
		machines              []*capiv1.Machine
		preExistingCondition  bool
		expectConflict        bool
		expectMessageContains []string
	}{
		{
			name: "When all addresses are inside cluster networks it should report dual-stack collision",
			clusterNetwork: []hyperv1.ClusterNetworkEntry{
				{CIDR: *ipnet.MustParseCIDR("10.128.0.0/14")},
				{CIDR: *ipnet.MustParseCIDR("fd01::/48")},
			},
			machines: []*capiv1.Machine{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					Status: capiv1.MachineStatus{
						Addresses: capiv1.MachineAddresses{
							{Type: capiv1.MachineInternalIP, Address: "10.128.0.5"},
							{Type: capiv1.MachineInternalIP, Address: "fd01::5"},
						},
					},
				},
			},
			expectConflict:        true,
			expectMessageContains: []string{"10.128.0.5", "10.128.0.0/14", "fd01::5", "fd01::/48"},
		},
		{
			name: "When machine has address outside all cluster networks it should not report dual-stack collision",
			clusterNetwork: []hyperv1.ClusterNetworkEntry{
				{CIDR: *ipnet.MustParseCIDR("10.128.0.0/14")},
				{CIDR: *ipnet.MustParseCIDR("fd01::/48")},
			},
			machines: []*capiv1.Machine{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					Status: capiv1.MachineStatus{
						Addresses: capiv1.MachineAddresses{
							{Type: capiv1.MachineExternalIP, Address: "192.168.1.10"},
							{Type: capiv1.MachineInternalIP, Address: "10.128.0.5"},
							{Type: capiv1.MachineInternalIP, Address: "fd01::5"},
						},
					},
				},
			},
			expectConflict: false,
		},
		{
			name: "When IPv4-only machine has address outside cluster network on dual-stack cluster it should not report collision",
			clusterNetwork: []hyperv1.ClusterNetworkEntry{
				{CIDR: *ipnet.MustParseCIDR("10.128.0.0/14")},
				{CIDR: *ipnet.MustParseCIDR("fd01::/48")},
			},
			machines: []*capiv1.Machine{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					Status: capiv1.MachineStatus{
						Addresses: capiv1.MachineAddresses{
							{Type: capiv1.MachineExternalIP, Address: "192.168.1.10"},
							{Type: capiv1.MachineInternalIP, Address: "10.128.0.5"},
						},
					},
				},
			},
			expectConflict: false,
		},
		{
			name: "When IPv6-only address is in second CIDR it should detect dual-stack collision",
			clusterNetwork: []hyperv1.ClusterNetworkEntry{
				{CIDR: *ipnet.MustParseCIDR("10.128.0.0/14")},
				{CIDR: *ipnet.MustParseCIDR("fd01::/48")},
			},
			machines: []*capiv1.Machine{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					Status: capiv1.MachineStatus{
						Addresses: capiv1.MachineAddresses{
							{Type: capiv1.MachineInternalIP, Address: "fd01::a"},
						},
					},
				},
			},
			expectConflict:        true,
			expectMessageContains: []string{"fd01::a", "fd01::/48"},
		},
		{
			name: "When machine has only link-local and in-cluster addresses it should report cidr collision",
			clusterNetwork: []hyperv1.ClusterNetworkEntry{
				{CIDR: *ipnet.MustParseCIDR("10.128.0.0/14")},
			},
			machines: []*capiv1.Machine{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					Status: capiv1.MachineStatus{
						Addresses: capiv1.MachineAddresses{
							{Type: capiv1.MachineInternalIP, Address: "169.254.0.2"},
							{Type: capiv1.MachineInternalIP, Address: "fe80::1"},
							{Type: capiv1.MachineInternalIP, Address: "10.128.0.5"},
						},
					},
				},
			},
			expectConflict:        true,
			expectMessageContains: []string{"10.128.0.5", "10.128.0.0/14"},
		},
		{
			name: "When machine has only link-local addresses it should not report cidr collision",
			clusterNetwork: []hyperv1.ClusterNetworkEntry{
				{CIDR: *ipnet.MustParseCIDR("10.128.0.0/14")},
			},
			machines: []*capiv1.Machine{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					Status: capiv1.MachineStatus{
						Addresses: capiv1.MachineAddresses{
							{Type: capiv1.MachineInternalIP, Address: "169.254.0.2"},
							{Type: capiv1.MachineInternalIP, Address: "fe80::1"},
						},
					},
				},
			},
			expectConflict: false,
		},
		{
			name: "When previously-conflicting machine gains out-of-cluster address it should clear the cidr conflict condition",
			clusterNetwork: []hyperv1.ClusterNetworkEntry{
				{CIDR: *ipnet.MustParseCIDR("10.128.0.0/14")},
			},
			machines: []*capiv1.Machine{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					Status: capiv1.MachineStatus{
						Addresses: capiv1.MachineAddresses{
							{Type: capiv1.MachineExternalIP, Address: "192.168.1.10"},
							{Type: capiv1.MachineInternalIP, Address: "10.128.0.5"},
						},
					},
				},
			},
			preExistingCondition: true,
			expectConflict:       false,
		},
		{
			name: "When multiple machines have mixed conflict states it should only report the conflicting one",
			clusterNetwork: []hyperv1.ClusterNetworkEntry{
				{CIDR: *ipnet.MustParseCIDR("10.128.0.0/14")},
			},
			machines: []*capiv1.Machine{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "conflicting-node"},
					Status: capiv1.MachineStatus{
						Addresses: capiv1.MachineAddresses{
							{Type: capiv1.MachineInternalIP, Address: "10.128.0.5"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "healthy-node"},
					Status: capiv1.MachineStatus{
						Addresses: capiv1.MachineAddresses{
							{Type: capiv1.MachineExternalIP, Address: "192.168.1.10"},
							{Type: capiv1.MachineInternalIP, Address: "10.128.0.6"},
						},
					},
				},
			},
			expectConflict:        true,
			expectMessageContains: []string{"conflicting-node", "10.128.0.5"},
		},
	} {
		t.Run(tc.name, func(tt *testing.T) {
			tt.Parallel()
			gg := NewWithT(tt)
			np := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "np-ds", Namespace: "myns"},
			}
			if tc.preExistingCondition {
				np.Status.Conditions = []hyperv1.NodePoolCondition{
					{
						Type:   hyperv1.NodePoolClusterNetworkCIDRConflictType,
						Status: corev1.ConditionTrue,
						Reason: hyperv1.InvalidConfigurationReason,
					},
				}
			}
			hc := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: tc.clusterNetwork,
					},
				},
			}

			err := r.setCIDRConflictCondition(tt.Context(), np, tc.machines, hc)
			gg.Expect(err).ToNot(HaveOccurred())

			cond := FindStatusCondition(np.Status.Conditions, hyperv1.NodePoolClusterNetworkCIDRConflictType)
			if tc.expectConflict {
				gg.Expect(cond).ToNot(BeNil(), "expected CIDR conflict condition to be set")
				gg.Expect(cond.Status).To(Equal(corev1.ConditionTrue))
				for _, want := range tc.expectMessageContains {
					gg.Expect(cond.Message).To(ContainSubstring(want))
				}
			} else {
				gg.Expect(cond).To(BeNil(), "expected no CIDR conflict condition")
			}
		})
	}
}

func newKVInfraMapMock(objects []client.Object) kvinfra.KubevirtInfraClientMap {
	return kvinfra.NewMockKubevirtInfraClientMap(
		fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build(),
		"",
		"")
}

func TestIsArchAndPlatformSupported(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		expect   bool
	}{
		{
			name: "supported arch and platform used",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
					},
					Arch: hyperv1.ArchitectureAMD64,
				},
			},
			expect: true,
		},
		{
			name: "supported arch and platform used - s390x",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
					},
					Arch: hyperv1.ArchitectureS390X,
				},
			},
			expect: true,
		},
		{
			name: "supported platform with multiple arch baremetal - arm64",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AgentPlatform,
					},
					Arch: hyperv1.ArchitectureARM64,
				},
			},
			expect: true,
		},
		{
			name: "supported platform with multiple arch - amd64",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AgentPlatform,
					},
					Arch: hyperv1.ArchitectureAMD64,
				},
			},
			expect: true,
		},
		{
			name: "supported platform with multiple arch - ppc64le",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AgentPlatform,
					},
					Arch: hyperv1.ArchitecturePPC64LE,
				},
			},
			expect: true,
		},
		{
			name: "supported platform with multiple arch baremetal - arm64",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.NonePlatform,
					},
					Arch: hyperv1.ArchitectureARM64,
				},
			},
			expect: true,
		},
		{
			name: "supported platform with multiple arch baremetal - amd64",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.NonePlatform,
					},
					Arch: hyperv1.ArchitectureAMD64,
				},
			},
			expect: true,
		},
		{
			name: "unsupported arch and platform used",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
					},
					Arch: hyperv1.ArchitecturePPC64LE,
				},
			},
			expect: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(isArchAndPlatformSupported(tc.nodePool)).To(Equal(tc.expect))
		})
	}
}

func Test_validateHCPayloadSupportsNodePoolCPUArch(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name        string
		hc          *hyperv1.HostedCluster
		np          *hyperv1.NodePool
		expectedErr bool
	}{
		{
			name: "payload is multi",
			hc: &hyperv1.HostedCluster{
				Status: hyperv1.HostedClusterStatus{
					PayloadArch: hyperv1.Multi,
				},
			},
			expectedErr: false,
		},
		{
			name: "payload is amd64; np is amd64",
			hc: &hyperv1.HostedCluster{
				Status: hyperv1.HostedClusterStatus{
					PayloadArch: hyperv1.AMD64,
				},
			},
			np: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
				},
			},
			expectedErr: false,
		},
		{
			name: "payload is amd64; np is arm64",
			hc: &hyperv1.HostedCluster{
				Status: hyperv1.HostedClusterStatus{
					PayloadArch: hyperv1.AMD64,
				},
			},
			np: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureARM64,
				},
			},
			expectedErr: true,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			err := validateHCPayloadSupportsNodePoolCPUArch(tt.hc, tt.np)
			if tt.expectedErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err).To(Not(BeNil()))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestResolveHAProxyImage(t *testing.T) {
	const (
		testAnnotationImage    = "quay.io/test/haproxy:custom"
		testSharedIngressImage = "quay.io/test/haproxy:shared-ingress"
		testReleaseImage       = "registry.test.io/openshift/haproxy-router:v4.16"
	)

	testCases := []struct {
		name                  string
		nodePoolAnnotations   map[string]string
		nodePoolStatusVersion string
		useSharedIngress      bool
		envVarImage           string
		canonicalComponents   map[string]string
		componentImage        string
		expectedImage         string
	}{
		{
			name: "When NodePool annotation is set it should use annotation image",
			nodePoolAnnotations: map[string]string{
				hyperv1.NodePoolHAProxyImageAnnotation: testAnnotationImage,
			},
			useSharedIngress: false,
			expectedImage:    testAnnotationImage,
		},
		{
			name: "When NodePool annotation is set it should override shared ingress image",
			nodePoolAnnotations: map[string]string{
				hyperv1.NodePoolHAProxyImageAnnotation: testAnnotationImage,
			},
			useSharedIngress: true,
			envVarImage:      testSharedIngressImage,
			expectedImage:    testAnnotationImage,
		},
		{
			name: "When NodePool annotation is empty it should use shared ingress image",
			nodePoolAnnotations: map[string]string{
				hyperv1.NodePoolHAProxyImageAnnotation: "",
			},
			useSharedIngress: true,
			envVarImage:      testSharedIngressImage,
			expectedImage:    testSharedIngressImage,
		},
		{
			name:             "When no annotation and shared ingress enabled it should use shared ingress image",
			useSharedIngress: true,
			envVarImage:      testSharedIngressImage,
			expectedImage:    testSharedIngressImage,
		},
		{
			name:             "When no annotation and shared ingress disabled it should use release payload image",
			useSharedIngress: false,
			expectedImage:    testReleaseImage,
		},
		{
			name: "When annotation is empty and shared ingress disabled it should use release payload image",
			nodePoolAnnotations: map[string]string{
				hyperv1.NodePoolHAProxyImageAnnotation: "",
			},
			useSharedIngress: false,
			expectedImage:    testReleaseImage,
		},
		{
			name: "When annotation is whitespace and shared ingress disabled it should use release payload image",
			nodePoolAnnotations: map[string]string{
				hyperv1.NodePoolHAProxyImageAnnotation: "  ",
			},
			useSharedIngress: false,
			expectedImage:    testReleaseImage,
		},
		{
			name:                "When registry overrides exist and NodePool is new it should use canonical image",
			useSharedIngress:    false,
			componentImage:      "mirror.example.com/openshift/haproxy-router:v4.16",
			canonicalComponents: map[string]string{haproxy.HAProxyRouterImageName: "registry.test.io/openshift/haproxy-router:v4.16"},
			expectedImage:       "registry.test.io/openshift/haproxy-router:v4.16",
		},
		{
			name:                  "When registry overrides exist and NodePool is upgrading it should use canonical image",
			nodePoolStatusVersion: "4.17.0",
			useSharedIngress:      false,
			componentImage:        "mirror.example.com/openshift/haproxy-router:v4.16",
			canonicalComponents:   map[string]string{haproxy.HAProxyRouterImageName: "registry.test.io/openshift/haproxy-router:v4.16"},
			expectedImage:         "registry.test.io/openshift/haproxy-router:v4.16",
		},
		{
			name: "When registry overrides exist and canonical-data-plane-images annotation is set it should use canonical image",
			nodePoolAnnotations: map[string]string{
				nodePoolAnnotationCanonicalDataPlaneImages: "true",
			},
			nodePoolStatusVersion: "4.18.0",
			useSharedIngress:      false,
			componentImage:        "mirror.example.com/openshift/haproxy-router:v4.16",
			canonicalComponents:   map[string]string{haproxy.HAProxyRouterImageName: "registry.test.io/openshift/haproxy-router:v4.16"},
			expectedImage:         "registry.test.io/openshift/haproxy-router:v4.16",
		},
		{
			name:                  "When registry overrides exist and NodePool is stable without annotation it should preserve overridden image",
			nodePoolStatusVersion: "4.18.0",
			useSharedIngress:      false,
			componentImage:        "mirror.example.com/openshift/haproxy-router:v4.16",
			canonicalComponents:   map[string]string{haproxy.HAProxyRouterImageName: "registry.test.io/openshift/haproxy-router:v4.16"},
			expectedImage:         "mirror.example.com/openshift/haproxy-router:v4.16",
		},
		{
			name: "When registry overrides exist it should not affect an annotation image",
			nodePoolAnnotations: map[string]string{
				hyperv1.NodePoolHAProxyImageAnnotation: "mirror.example.com/custom/haproxy:latest",
			},
			componentImage:      "mirror.example.com/openshift/haproxy-router:v4.16",
			canonicalComponents: map[string]string{haproxy.HAProxyRouterImageName: "registry.test.io/openshift/haproxy-router:v4.16"},
			expectedImage:       "mirror.example.com/custom/haproxy:latest",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			// Set up environment variables for shared ingress
			if tc.useSharedIngress {
				t.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
				if tc.envVarImage != "" {
					t.Setenv("IMAGE_SHARED_INGRESS_HAPROXY", tc.envVarImage)
				}
			}

			// Create test NodePool
			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-nodepool",
					Namespace:   "clusters",
					Annotations: tc.nodePoolAnnotations,
				},
				Status: hyperv1.NodePoolStatus{
					Version: tc.nodePoolStatusVersion,
				},
			}

			// Create kubeconfig secret
			kubeconfigSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kk",
					Namespace: "clusters",
				},
				Data: map[string][]byte{
					"kubeconfig": []byte(`apiVersion: v1
clusters:
- cluster:
    server: https://kubeconfig-host:443
  name: cluster
contexts:
- context:
    cluster: cluster
    user: ""
    namespace: default
  name: cluster
current-context: cluster
kind: Config`),
				},
			}

			// Create fake pull secret
			pullSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pull-secret",
					Namespace: "clusters",
				},
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: []byte(`{"auths":{"test":{"auth":"dGVzdDp0ZXN0"}}}`),
				},
			}

			// Create list of objects for the fake client
			objects := []client.Object{kubeconfigSecret, pullSecret}

			// Add router service if shared ingress is enabled
			if tc.useSharedIngress {
				routerService := &corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "router",
						Namespace: "hypershift-sharedingress",
					},
					Spec: corev1.ServiceSpec{
						Ports: []corev1.ServicePort{
							{
								Name: "https",
								Port: 443,
							},
						},
					},
					Status: corev1.ServiceStatus{
						LoadBalancer: corev1.LoadBalancerStatus{
							Ingress: []corev1.LoadBalancerIngress{
								{
									IP: "192.0.2.1",
								},
							},
						},
					},
				}
				objects = append(objects, routerService)
			}

			// Create fake client
			c := fake.NewClientBuilder().WithObjects(objects...).Build()

			componentImage := testReleaseImage
			if tc.componentImage != "" {
				componentImage = tc.componentImage
			}

			// Create fake release provider with component images
			releaseProvider := &fakereleaseprovider.FakeReleaseProvider{
				Version: "4.18.0",
				Components: map[string]string{
					haproxy.HAProxyRouterImageName: componentImage,
				},
				CanonicalComponents: tc.canonicalComponents,
			}

			// Create test HostedCluster
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: func() hyperv1.PlatformSpec {
						if tc.useSharedIngress {
							return hyperv1.PlatformSpec{
								Type: hyperv1.AzurePlatform,
								Azure: &hyperv1.AzurePlatformSpec{
									Topology: hyperv1.AzureTopologyPublicAndPrivate,
									Private: hyperv1.AzurePrivateSpec{
										Type:  hyperv1.AzurePrivateTypeSwift,
										Swift: hyperv1.AzureSwiftSpec{PodNetworkInstance: "test-pni"},
									},
									AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
										AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeManagedIdentities,
									},
								},
							}
						}
						return hyperv1.PlatformSpec{
							Type: hyperv1.AWSPlatform,
							AWS: &hyperv1.AWSPlatformSpec{
								EndpointAccess: hyperv1.Public,
							},
						}
					}(),
					Networking: hyperv1.ClusterNetworking{
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: hyperv1.Release{
						Image: "test-release:latest",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					KubeConfig: &corev1.LocalObjectReference{Name: "kk"},
				},
			}

			// Create NodePoolReconciler
			reconciler := &NodePoolReconciler{
				Client:                  c,
				ReleaseProvider:         releaseProvider,
				HypershiftOperatorImage: "hypershift-operator:latest",
				ImageMetadataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
					Result: &dockerv1client.DockerImageConfig{
						Config: &docker10.DockerConfig{
							Labels: map[string]string{
								haproxy.ControlPlaneOperatorSkipsHAProxyConfigGenerationLabel: "true",
							},
						},
					},
				},
			}

			// Get release image using the fake provider
			ctx := t.Context()
			releaseImage := fakereleaseprovider.GetReleaseImage(ctx, hc, c, releaseProvider)
			g.Expect(releaseImage).ToNot(BeNil())

			// Call generateHAProxyRawConfig
			cfg, err := reconciler.generateHAProxyRawConfig(ctx, nodePool, hc, releaseImage)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(cfg).ToNot(BeEmpty())

			// Unmarshal MachineConfig
			mcfg := &mcfgv1.MachineConfig{}
			err = yaml.Unmarshal([]byte(cfg), mcfg)
			g.Expect(err).ToNot(HaveOccurred())

			// Unmarshal Ignition config
			ignitionCfg := &ignitionapi.Config{}
			err = yaml.Unmarshal(mcfg.Spec.Config.Raw, ignitionCfg)
			g.Expect(err).ToNot(HaveOccurred())

			// Find the kube-apiserver-proxy.yaml file
			var kubeAPIServerProxyManifest *dataurl.DataURL
			for _, file := range ignitionCfg.Storage.Files {
				if file.Path == "/etc/kubernetes/manifests/kube-apiserver-proxy.yaml" {
					kubeAPIServerProxyManifest, err = dataurl.DecodeString(*file.Contents.Source)
					g.Expect(err).ToNot(HaveOccurred())
					break
				}
			}

			g.Expect(kubeAPIServerProxyManifest).ToNot(BeNil(), "couldn't find /etc/kubernetes/manifests/kube-apiserver-proxy.yaml in ignition config")

			// Verify the expected HAProxy image is present in the manifest
			manifestContent := string(kubeAPIServerProxyManifest.Data)
			g.Expect(manifestContent).To(ContainSubstring(tc.expectedImage),
				"expected HAProxy image %q to be present in kube-apiserver-proxy.yaml manifest", tc.expectedImage)
		})
	}
}

func TestSupportedVersionSkewCondition(t *testing.T) {
	t.Parallel()
	basePullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-secret",
			Namespace: "test-ns",
		},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{"quay.io":{"auth":"","email":""}}}`),
		},
	}

	baseNodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-np",
			Namespace: "test-ns",
		},
		Spec: hyperv1.NodePoolSpec{
			Release: hyperv1.Release{
				Image: "quay.io/openshift-release-dev/ocp-release:4.18.5-x86_64",
			},
		},
		Status: hyperv1.NodePoolStatus{
			Conditions: []hyperv1.NodePoolCondition{},
		},
	}

	baseHostedCluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hc",
			Namespace: "test-ns",
		},
		Spec: hyperv1.HostedClusterSpec{
			PullSecret: corev1.LocalObjectReference{
				Name: "pull-secret",
			},
		},
		Status: hyperv1.HostedClusterStatus{
			Version: &hyperv1.ClusterVersionStatus{
				History: []configv1.UpdateHistory{
					{
						State:   configv1.CompletedUpdate,
						Version: "4.18.5",
					},
				},
			},
		},
	}

	testCases := []struct {
		name              string
		nodePool          *hyperv1.NodePool
		hostedCluster     *hyperv1.HostedCluster
		releaseProvider   *fakereleaseprovider.FakeReleaseProvider
		expectedCondition *hyperv1.NodePoolCondition
		expectedError     string
	}{
		{
			name: "when nodePool version matches control plane version it should report valid condition",
			nodePool: func() *hyperv1.NodePool {
				np := baseNodePool.DeepCopy()
				np.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.18.5-x86_64"
				return np
			}(),
			hostedCluster: baseHostedCluster.DeepCopy(),
			releaseProvider: &fakereleaseprovider.FakeReleaseProvider{
				Version: "4.18.5",
			},
			expectedCondition: &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolSupportedVersionSkewConditionType,
				Status:             corev1.ConditionTrue,
				Reason:             hyperv1.AsExpectedReason,
				Message:            "Release image version is valid",
				ObservedGeneration: 0,
			},
			expectedError: "",
		},
		{
			name: "when nodePool version is higher than control plane version it should report invalid condition",
			nodePool: func() *hyperv1.NodePool {
				np := baseNodePool.DeepCopy()
				np.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.19.0-x86_64"
				return np
			}(),
			hostedCluster: baseHostedCluster.DeepCopy(),
			releaseProvider: &fakereleaseprovider.FakeReleaseProvider{
				Version: "4.19.0",
			},
			expectedCondition: &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolSupportedVersionSkewConditionType,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.NodePoolUnsupportedSkewReason,
				Message:            "NodePool version 4.19.0 cannot be higher than the HostedCluster version 4.18.5",
				ObservedGeneration: 0,
			},
			expectedError: "",
		},
		{
			name: "when nodePool version is two minor versions lower than control plane (odd version) it should report valid condition with n-3 support",
			nodePool: func() *hyperv1.NodePool {
				np := baseNodePool.DeepCopy()
				np.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.15.0-x86_64"
				return np
			}(),
			hostedCluster: func() *hyperv1.HostedCluster {
				hc := baseHostedCluster.DeepCopy()
				hc.Status.Version.History[0].Version = "4.17.0"
				return hc
			}(),
			releaseProvider: &fakereleaseprovider.FakeReleaseProvider{
				Version: "4.15.0",
			},
			expectedCondition: &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolSupportedVersionSkewConditionType,
				Status:             corev1.ConditionTrue,
				Reason:             hyperv1.AsExpectedReason,
				Message:            "Release image version is valid",
				ObservedGeneration: 0,
			},
			expectedError: "",
		},
		{
			name: "when nodePool version is two minor versions lower than control plane (even version) it should report valid condition",
			nodePool: func() *hyperv1.NodePool {
				np := baseNodePool.DeepCopy()
				np.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.16.0-x86_64"
				return np
			}(),
			hostedCluster: func() *hyperv1.HostedCluster {
				hc := baseHostedCluster.DeepCopy()
				hc.Status.Version.History[0].Version = "4.18.0"
				return hc
			}(),
			releaseProvider: &fakereleaseprovider.FakeReleaseProvider{
				Version: "4.16.0",
			},
			expectedCondition: &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolSupportedVersionSkewConditionType,
				Status:             corev1.ConditionTrue,
				Reason:             hyperv1.AsExpectedReason,
				Message:            "Release image version is valid",
				ObservedGeneration: 0,
			},
			expectedError: "",
		},
		{
			name: "when hosted cluster version history is empty it should report valid condition",
			nodePool: func() *hyperv1.NodePool {
				np := baseNodePool.DeepCopy()
				np.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.18.5-x86_64"
				return np
			}(),
			hostedCluster: func() *hyperv1.HostedCluster {
				hc := baseHostedCluster.DeepCopy()
				hc.Status.Version.History = []configv1.UpdateHistory{}
				hc.Status.Version.Desired = configv1.Release{
					Version: "4.18.5",
				}
				return hc
			}(),
			releaseProvider: &fakereleaseprovider.FakeReleaseProvider{
				Version: "4.18.5",
			},
			expectedCondition: &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolSupportedVersionSkewConditionType,
				Status:             corev1.ConditionTrue,
				Reason:             hyperv1.AsExpectedReason,
				Message:            "Release image version is valid",
				ObservedGeneration: 0,
			},
			expectedError: "",
		},
		{
			name: "when nodePool version is three minor versions lower (n-3) it should report valid condition",
			nodePool: func() *hyperv1.NodePool {
				np := baseNodePool.DeepCopy()
				np.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.15.0-x86_64"
				return np
			}(),
			hostedCluster: func() *hyperv1.HostedCluster {
				hc := baseHostedCluster.DeepCopy()
				hc.Status.Version.History[0].Version = "4.18.0"
				return hc
			}(),
			releaseProvider: &fakereleaseprovider.FakeReleaseProvider{
				Version: "4.15.0",
			},
			expectedCondition: &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolSupportedVersionSkewConditionType,
				Status:             corev1.ConditionTrue,
				Reason:             hyperv1.AsExpectedReason,
				Message:            "Release image version is valid",
				ObservedGeneration: 0,
			},
			expectedError: "",
		},
		{
			name: "when nodePool version is four minor versions lower (n-4) it should report invalid condition",
			nodePool: func() *hyperv1.NodePool {
				np := baseNodePool.DeepCopy()
				np.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.14.0-x86_64"
				return np
			}(),
			hostedCluster: func() *hyperv1.HostedCluster {
				hc := baseHostedCluster.DeepCopy()
				hc.Status.Version.History[0].Version = "4.18.0"
				return hc
			}(),
			releaseProvider: &fakereleaseprovider.FakeReleaseProvider{
				Version: "4.14.0",
			},
			expectedCondition: &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolSupportedVersionSkewConditionType,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.NodePoolUnsupportedSkewReason,
				Message:            "NodePool minor version 4.14 is less than 4.15, which is the minimum NodePool version compatible with the 4.18 HostedCluster",
				ObservedGeneration: 0,
			},
			expectedError: "",
		},
		{
			name: "when nodePool patch version is lower than control plane (same minor version) it should report valid condition",
			nodePool: func() *hyperv1.NodePool {
				np := baseNodePool.DeepCopy()
				np.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.18.5-x86_64"
				return np
			}(),
			hostedCluster: func() *hyperv1.HostedCluster {
				hc := baseHostedCluster.DeepCopy()
				hc.Status.Version.History[0].Version = "4.18.10"
				return hc
			}(),
			releaseProvider: &fakereleaseprovider.FakeReleaseProvider{
				Version: "4.18.5",
			},
			expectedCondition: &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolSupportedVersionSkewConditionType,
				Status:             corev1.ConditionTrue,
				Reason:             hyperv1.AsExpectedReason,
				Message:            "Release image version is valid",
				ObservedGeneration: 0,
			},
			expectedError: "",
		},
		{
			name: "when nodePool patch version is higher than control plane (same minor version) it should report invalid condition",
			nodePool: func() *hyperv1.NodePool {
				np := baseNodePool.DeepCopy()
				np.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.18.10-x86_64"
				return np
			}(),
			hostedCluster: func() *hyperv1.HostedCluster {
				hc := baseHostedCluster.DeepCopy()
				hc.Status.Version.History[0].Version = "4.18.5"
				return hc
			}(),
			releaseProvider: &fakereleaseprovider.FakeReleaseProvider{
				Version: "4.18.10",
			},
			expectedCondition: &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolSupportedVersionSkewConditionType,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.NodePoolUnsupportedSkewReason,
				Message:            "NodePool version 4.18.10 cannot be higher than the HostedCluster version 4.18.5",
				ObservedGeneration: 0,
			},
			expectedError: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			r := NodePoolReconciler{
				Client:          fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects([]client.Object{tc.nodePool, tc.hostedCluster, basePullSecret}...).Build(),
				ReleaseProvider: tc.releaseProvider,
			}

			// Run the test
			result, err := r.supportedVersionSkewCondition(context.Background(), tc.nodePool, tc.hostedCluster)

			// Check the results
			if tc.expectedError == "" {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(result).To(BeNil())
			} else {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(tc.expectedError))
				g.Expect(result).NotTo(BeNil())
			}

			// Check the condition
			condition := FindStatusCondition(tc.nodePool.Status.Conditions, hyperv1.NodePoolSupportedVersionSkewConditionType)
			g.Expect(condition).NotTo(BeNil())

			g.Expect(condition.Type).To(Equal(tc.expectedCondition.Type))
			g.Expect(condition.Status).To(Equal(tc.expectedCondition.Status))
			g.Expect(condition.Reason).To(Equal(tc.expectedCondition.Reason))
			g.Expect(condition.Message).To(Equal(tc.expectedCondition.Message))
		})
	}
}

func TestNodePoolReconciler_reconcile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		hcluster *hyperv1.HostedCluster
		nodePool *hyperv1.NodePool
		want     ctrl.Result
		wantErr  bool
	}{
		{
			name: "when NodePool and HostedCluster are valid it should reconcile successfully",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: "quay.io/openshift-release-dev/ocp-release:4.18.5-x86_64",
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								State:   configv1.CompletedUpdate,
								Version: "4.18.5",
							},
						},
					},
				},
			},
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-np",
					Namespace: "test-ns",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-hc",
					Replicas:    ptr.To(int32(3)),
					Release: hyperv1.Release{
						Image: "quay.io/openshift-release-dev/ocp-release:4.18.5-x86_64",
					},
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			want:    ctrl.Result{},
			wantErr: false,
		},
		{
			name: "when reconciling it should set conditions in the expected order",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: "quay.io/openshift-release-dev/ocp-release:4.18.5-x86_64",
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								State:   configv1.CompletedUpdate,
								Version: "4.18.5",
							},
						},
					},
				},
			},
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-np",
					Namespace: "test-ns",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-hc",
					Replicas:    ptr.To(int32(3)),
					Release: hyperv1.Release{
						Image: "quay.io/openshift-release-dev/ocp-release:4.18.5-x86_64",
					},
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			want:    ctrl.Result{},
			wantErr: false,
		},
		{
			name: "when ignition endpoint is missing it should exit early from condition loop",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: "quay.io/openshift-release-dev/ocp-release:4.18.5-x86_64",
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
				Status: hyperv1.HostedClusterStatus{
					// Missing IgnitionEndpoint - this should cause early exit
					IgnitionEndpoint: "",
					Version: &hyperv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								State:   configv1.CompletedUpdate,
								Version: "4.18.5",
							},
						},
					},
				},
			},
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-np",
					Namespace: "test-ns",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-hc",
					Replicas:    ptr.To(int32(3)),
					Release: hyperv1.Release{
						Image: "quay.io/openshift-release-dev/ocp-release:4.18.5-x86_64",
					},
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
					},
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy: hyperv1.UpgradeStrategyRollingUpdate,
							RollingUpdate: &hyperv1.RollingUpdate{
								MaxUnavailable: ptr.To(intstr.FromInt(0)),
								MaxSurge:       ptr.To(intstr.FromInt(1)),
							},
						},
					},
				},
			},
			want:    ctrl.Result{},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			pullSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pull-secret",
					Namespace: "test-ns",
				},
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: []byte(`{"auths":{"quay.io":{"auth":"","email":""}}}`),
				},
			}

			r := NodePoolReconciler{
				Client: fake.NewClientBuilder().
					WithScheme(api.Scheme).
					WithObjects([]client.Object{tt.hcluster, tt.nodePool, pullSecret}...).
					Build(),
				ReleaseProvider: &fakereleaseprovider.FakeReleaseProvider{
					Version: "4.18.5",
				},
				ImageMetadataProvider:  &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{},
				CreateOrUpdateProvider: upsert.New(false),
			}

			got, gotErr := r.reconcile(context.Background(), tt.hcluster, tt.nodePool)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("reconcile() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("reconcile() succeeded unexpectedly")
			}
			g.Expect(got).To(Equal(tt.want))

			// For the condition order test, verify conditions are set in the expected order
			if tt.name == "when reconciling it should set conditions in the expected order" {
				// Expected condition order based on reconcile() signalConditions array
				expectedConditionOrder := []string{
					hyperv1.NodePoolAutoscalingEnabledConditionType,
					hyperv1.NodePoolUpdateManagementEnabledConditionType,
					hyperv1.NodePoolValidReleaseImageConditionType,
					string(hyperv1.IgnitionEndpointAvailable),
					hyperv1.NodePoolValidArchPlatform,
					hyperv1.NodePoolReconciliationActiveConditionType,
					hyperv1.NodePoolSupportedVersionSkewConditionType,
					hyperv1.NodePoolValidMachineConfigConditionType,
					hyperv1.NodePoolUpdatingConfigConditionType,
					hyperv1.NodePoolUpdatingVersionConditionType,
					hyperv1.NodePoolValidGeneratedPayloadConditionType,
					hyperv1.NodePoolReachedIgnitionEndpoint,
					hyperv1.NodePoolReadyConditionType,
					hyperv1.NodePoolAllMachinesReadyConditionType,
					hyperv1.NodePoolAllNodesHealthyConditionType,
					hyperv1.NodePoolValidPlatformConfigConditionType,
				}

				// Build a map of condition type to index in the expected order
				expectedOrderMap := make(map[string]int)
				for i, condType := range expectedConditionOrder {
					expectedOrderMap[condType] = i
				}

				// Verify that conditions that are present appear in the correct relative order
				lastSeenIndex := -1
				for _, condition := range tt.nodePool.Status.Conditions {
					if expectedIndex, found := expectedOrderMap[condition.Type]; found {
						if expectedIndex < lastSeenIndex {
							t.Errorf("Condition %s (expected index %d) appears after condition with index %d, violating expected order",
								condition.Type, expectedIndex, lastSeenIndex)
						}
						lastSeenIndex = expectedIndex
					}
				}
			}

			// For the early exit test, verify the function exited early from the condition loop
			if tt.name == "when ignition endpoint is missing it should exit early from condition loop" {
				// Verify IgnitionEndpointAvailable condition is set to False
				ignitionCondition := FindStatusCondition(tt.nodePool.Status.Conditions, string(hyperv1.IgnitionEndpointAvailable))
				g.Expect(ignitionCondition).NotTo(BeNil(), "IgnitionEndpointAvailable condition should be set")
				g.Expect(ignitionCondition.Status).To(Equal(corev1.ConditionFalse), "IgnitionEndpointAvailable should be False")
				g.Expect(ignitionCondition.Reason).To(Equal(hyperv1.IgnitionEndpointMissingReason), "Reason should be IgnitionEndpointMissing")

				// Verify that conditions processed after ignitionEndpointAvailableCondition are NOT set
				// These conditions come later in the signalConditions array and should not be evaluated
				// due to early exit at nodepool_controller.go:308-310
				laterConditions := []string{
					hyperv1.NodePoolSupportedVersionSkewConditionType,
					hyperv1.NodePoolValidMachineConfigConditionType,
					hyperv1.NodePoolUpdatingConfigConditionType,
					hyperv1.NodePoolUpdatingVersionConditionType,
					hyperv1.NodePoolValidGeneratedPayloadConditionType,
					hyperv1.NodePoolReachedIgnitionEndpoint,
				}
				for _, condType := range laterConditions {
					condition := FindStatusCondition(tt.nodePool.Status.Conditions, condType)
					if condition != nil {
						t.Errorf("Condition %s should not be set due to early exit, but was found", condType)
					}
				}
			}
		})
	}
}
