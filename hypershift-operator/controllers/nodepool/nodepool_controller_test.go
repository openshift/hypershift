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
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/instancetype"
	ignserver "github.com/openshift/hypershift/ignition-server/controllers"
	kvinfra "github.com/openshift/hypershift/kubevirtexternalinfra"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
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

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/google/go-cmp/cmp"
	"github.com/vincent-petithory/dataurl"
)

func TestIsUpdatingConfig(t *testing.T) {
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
			g := NewWithT(t)
			g.Expect(isUpdatingConfig(tc.nodePool, tc.target)).To(Equal(tc.expect))
		})
	}
}

func TestIsUpdatingVersion(t *testing.T) {
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
			g := NewWithT(t)
			g.Expect(isUpdatingVersion(tc.nodePool, tc.target)).To(Equal(tc.expect))
		})
	}
}

func TestIsAutoscalingEnabled(t *testing.T) {
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
			g := NewWithT(t)
			g.Expect(isAutoscalingEnabled(tc.nodePool)).To(Equal(tc.expect))
		})
	}
}

func TestValidateManagement(t *testing.T) {
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
	g := NewWithT(t)
	err := validateInfraID("")
	g.Expect(err).To(HaveOccurred())

	err = validateInfraID("123")
	g.Expect(err).ToNot(HaveOccurred())
}

func TestGetName(t *testing.T) {
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
						util.HostedClusterAnnotation: types.NamespacedName{Name: "hosted-cluster-1", Namespace: testNodePoolNamespace}.String(),
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
	testCases := []struct {
		name          string
		region        string
		specifiedArch string
		releaseImage  *releaseinfo.ReleaseImage
		image         string
		err           error
		expectedImage string
	}{
		{
			name:          "successfully pull amd64 AMI",
			region:        "us-east-1",
			specifiedArch: "amd64",
			expectedImage: "us-east-1-x86_64-image",
		},
		{
			name:          "successfully pull arm64 AMI",
			region:        "us-east-1",
			specifiedArch: "arm64",
			expectedImage: "us-east-1-aarch64-image",
		},
		{
			name:          "fail to pull amd64 AMI because region can't be found",
			region:        "us-east-2",
			specifiedArch: "amd64",
			expectedImage: "",
		},
		{
			name:          "fail to pull arm64 AMI because region can't be found",
			region:        "us-east-2",
			specifiedArch: "arm64",
			expectedImage: "",
		},
		{
			name:          "fail because architecture can't be found",
			region:        "us-east-2",
			specifiedArch: "arm644",
			expectedImage: "",
		},
		{
			name:          "fail because architecture can't be found",
			region:        "us-east-2",
			specifiedArch: "s390x",
			expectedImage: "",
		},
		{
			name:          "fail because no image data is defined",
			region:        "us-west-1",
			specifiedArch: "arm64",
			expectedImage: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			other := []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "pull-secret"},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: nil,
					},
				},
			}

			client := fake.NewClientBuilder().WithObjects(other...).Build()
			releaseProvider := &fakereleaseprovider.FakeReleaseProvider{}
			hc := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: hyperv1.Release{
						Image: "image-4.12.0",
					},
				},
			}

			ctx := t.Context()
			tc.releaseImage = fakereleaseprovider.GetReleaseImage(ctx, hc, client, releaseProvider)

			tc.image, tc.err = defaultNodePoolAMI(tc.region, tc.specifiedArch, tc.releaseImage)
			if strings.Contains(tc.name, "successfully") {
				g.Expect(tc.image).To(Equal(tc.expectedImage))
				g.Expect(tc.err).To(BeNil())
			} else if strings.Contains(tc.name, "fail to pull") {
				g.Expect(tc.image).To(BeEmpty())
				g.Expect(tc.err.Error()).To(Equal("couldn't find AWS image for region \"" + tc.region + "\""))
			} else if strings.Contains(tc.name, "fail because architecture") {
				g.Expect(tc.image).To(BeEmpty())
				g.Expect(tc.err.Error()).To(Equal("couldn't find OS metadata for architecture \"" + tc.specifiedArch + "\""))
			} else {
				g.Expect(tc.image).To(BeEmpty())
				g.Expect(tc.err.Error()).To(Equal("release image metadata has no image for region \"" + tc.region + "\""))
			}
		})
	}
}

func TestGetHostedClusterVersion(t *testing.T) {
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

type testCondition struct {
	Status   corev1.ConditionStatus
	Reason   string
	Messages []string
}

func (t *testCondition) Compare(g Gomega, cond *hyperv1.NodePoolCondition) {
	if t == nil {
		return
	}

	g.Expect(cond.Status).To(Equal(t.Status))
	g.Expect(cond.Reason).To(Equal(t.Reason))

	for _, msg := range t.Messages {
		g.ExpectWithOffset(1, cond.Message).To(ContainSubstring(msg))
	}
}

func TestSetMachineAndNodeConditions(t *testing.T) {
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
							Conditions: []capiv1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: corev1.ConditionTrue,
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
							Conditions: []capiv1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: corev1.ConditionTrue,
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
							Conditions: []capiv1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "test message node 1",
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  corev1.ConditionFalse,
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
							Conditions: []capiv1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "test message node 2",
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  corev1.ConditionFalse,
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
				Reason:   "TestReasonNode2",
				Messages: []string{"TestReasonNode1", "TestReasonNode2"},
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
							Conditions: []capiv1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "test message node 1",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "12 of 34 completed",
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  corev1.ConditionFalse,
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
							Conditions: []capiv1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "test message node 2",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "some real failed message",
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  corev1.ConditionFalse,
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
				Reason:   "TestReasonNode2",
				Messages: []string{"TestReasonNode1", "TestReasonNode2"},
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
							Conditions: []capiv1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "test message node 1",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "some real failed message",
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  corev1.ConditionFalse,
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
							Conditions: []capiv1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "test message node 2",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "12 of 34 completed",
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  corev1.ConditionFalse,
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
				Reason:   "TestReasonNode2",
				Messages: []string{"TestReasonNode1", "TestReasonNode2"},
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
							Conditions: []capiv1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "not ready",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  corev1.ConditionFalse,
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
							Conditions: []capiv1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "not ready",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  corev1.ConditionFalse,
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
							Conditions: []capiv1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode3",
									Message: "not ready",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  corev1.ConditionFalse,
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
							Conditions: []capiv1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: corev1.ConditionTrue,
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
							Conditions: []capiv1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: corev1.ConditionTrue,
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
							Conditions: []capiv1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: corev1.ConditionTrue,
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

func newKVInfraMapMock(objects []client.Object) kvinfra.KubevirtInfraClientMap {
	return kvinfra.NewMockKubevirtInfraClientMap(
		fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build(),
		"",
		"")
}

func TestIsArchAndPlatformSupported(t *testing.T) {
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
			g := NewWithT(t)
			g.Expect(isArchAndPlatformSupported(tc.nodePool)).To(Equal(tc.expect))
		})
	}
}

func Test_validateHCPayloadSupportsNodePoolCPUArch(t *testing.T) {
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
		name                string
		nodePoolAnnotations map[string]string
		useSharedIngress    bool
		envVarImage         string
		expectedImage       string
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

			// Create fake release provider with component images
			releaseProvider := &fakereleaseprovider.FakeReleaseProvider{
				Components: map[string]string{
					haproxy.HAProxyRouterImageName: testReleaseImage,
				},
			}

			// Create test HostedCluster
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: hyperv1.Public,
						},
					},
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

func TestReconcileScaleFromZeroAnnotations(t *testing.T) {
	t.Parallel()

	const cpNamespace = "test-ns-test-cluster"
	const templateName = "test-aws-template"

	newAWSObjects := func() []client.Object {
		return []client.Object{
			&capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: "test-nodepool", Namespace: cpNamespace},
				Spec: capiv1.MachineDeploymentSpec{
					Template: capiv1.MachineTemplateSpec{
						Spec: capiv1.MachineSpec{
							InfrastructureRef: corev1.ObjectReference{Name: templateName, Namespace: cpNamespace},
						},
					},
				},
			},
			&capiaws.AWSMachineTemplate{
				ObjectMeta: metav1.ObjectMeta{Name: templateName, Namespace: cpNamespace},
				Spec: capiaws.AWSMachineTemplateSpec{
					Template: capiaws.AWSMachineTemplateResource{
						Spec: capiaws.AWSMachineSpec{InstanceType: "m5.large"},
					},
				},
			},
		}
	}

	newAzureObjects := func() []client.Object {
		return []client.Object{
			&capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: "test-nodepool", Namespace: cpNamespace},
				Spec: capiv1.MachineDeploymentSpec{
					Template: capiv1.MachineTemplateSpec{
						Spec: capiv1.MachineSpec{
							InfrastructureRef: corev1.ObjectReference{Name: templateName, Namespace: cpNamespace},
						},
					},
				},
			},
			&capiazure.AzureMachineTemplate{
				ObjectMeta: metav1.ObjectMeta{Name: templateName, Namespace: cpNamespace},
				Spec: capiazure.AzureMachineTemplateSpec{
					Template: capiazure.AzureMachineTemplateResource{
						Spec: capiazure.AzureMachineSpec{VMSize: "Standard_D4s_v5"},
					},
				},
			},
		}
	}

	tests := []struct {
		name         string
		platform     hyperv1.PlatformType
		objects      []client.Object
		provider     instancetype.Provider
		expectErr    bool
		errSubstring string
		validate     func(g Gomega, c client.Client)
	}{
		{
			name:         "When platform is unsupported, it should return an error",
			platform:     hyperv1.KubevirtPlatform,
			provider:     &mockProvider{},
			expectErr:    true,
			errSubstring: "unsupported platform",
		},
		{
			name:     "When MachineDeployment does not exist, it should skip gracefully",
			platform: hyperv1.AWSPlatform,
			provider: &mockProvider{},
		},
		{
			name:     "When AWS template and MachineDeployment exist, it should set annotations on MachineDeployment",
			platform: hyperv1.AWSPlatform,
			objects:  newAWSObjects(),
			provider: &mockProvider{info: &instancetype.InstanceTypeInfo{
				VCPU: 2, MemoryMb: 8192, CPUArchitecture: "amd64",
			}},
			validate: func(g Gomega, c client.Client) {
				md := &capiv1.MachineDeployment{}
				g.Expect(c.Get(context.Background(), client.ObjectKey{Namespace: cpNamespace, Name: "test-nodepool"}, md)).To(Succeed())
				g.Expect(md.GetAnnotations()).To(HaveKeyWithValue(cpuKey, "2"))
				g.Expect(md.GetAnnotations()).To(HaveKeyWithValue(memoryKey, "8192"))
			},
		},
		{
			name:     "When provider is nil, it should not set annotations",
			platform: hyperv1.AWSPlatform,
			objects:  newAWSObjects(),
			validate: func(g Gomega, c client.Client) {
				md := &capiv1.MachineDeployment{}
				g.Expect(c.Get(context.Background(), client.ObjectKey{Namespace: cpNamespace, Name: "test-nodepool"}, md)).To(Succeed())
				g.Expect(md.GetAnnotations()).ToNot(HaveKey(cpuKey))
			},
		},
		{
			name:     "When Azure template and MachineDeployment exist, it should set annotations on MachineDeployment",
			platform: hyperv1.AzurePlatform,
			objects:  newAzureObjects(),
			provider: &mockProvider{info: &instancetype.InstanceTypeInfo{
				VCPU: 4, MemoryMb: 16384, CPUArchitecture: "amd64",
			}},
			validate: func(g Gomega, c client.Client) {
				md := &capiv1.MachineDeployment{}
				g.Expect(c.Get(context.Background(), client.ObjectKey{Namespace: cpNamespace, Name: "test-nodepool"}, md)).To(Succeed())
				g.Expect(md.GetAnnotations()).To(HaveKeyWithValue(cpuKey, "4"))
				g.Expect(md.GetAnnotations()).To(HaveKeyWithValue(memoryKey, "16384"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)

			np := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "test-nodepool", Namespace: "test-ns"},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-cluster",
					Platform:    hyperv1.NodePoolPlatform{Type: tt.platform},
					Management:  hyperv1.NodePoolManagement{UpgradeType: hyperv1.UpgradeTypeReplace},
				},
			}

			builder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if len(tt.objects) > 0 {
				builder = builder.WithObjects(tt.objects...)
			}
			c := builder.Build()

			capi := &CAPI{
				Token: &Token{
					ConfigGenerator: &ConfigGenerator{
						Client:                c,
						nodePool:              np,
						controlplaneNamespace: cpNamespace,
					},
				},
				capiClusterName: "test-cluster",
			}

			r := &NodePoolReconciler{
				Client:               c,
				InstanceTypeProvider: tt.provider,
			}

			err := r.reconcileScaleFromZeroAnnotations(t.Context(), np, capi)
			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errSubstring))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				if tt.validate != nil {
					tt.validate(g, c)
				}
			}
		})
	}
}

func TestSupportedScaleFromZeroPlatform(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		platform hyperv1.PlatformType
		want     bool
	}{
		{
			name:     "When platform is AWS, it should be supported",
			platform: hyperv1.AWSPlatform,
			want:     true,
		},
		{
			name:     "When platform is Azure, it should be supported",
			platform: hyperv1.AzurePlatform,
			want:     true,
		},
		{
			name:     "When platform is KubeVirt, it should not be supported",
			platform: hyperv1.KubevirtPlatform,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := supportedScaleFromZeroPlatform(tt.platform); got != tt.want {
				t.Errorf("supportedScaleFromZeroPlatform(%s) = %v, want %v", tt.platform, got, tt.want)
			}
		})
	}
}
