package nodepool

import (
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/api"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	"github.com/openshift/api/image/docker10"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/blang/semver"
)

func TestGenerateReconciliationPausedCondition(t *testing.T) {
	fakeInputGeneration := int64(5)
	fakeFutureDate := ptr.To(time.Now().Add(4 * time.Hour).Format(time.RFC3339))
	fakePastDate := ptr.To(time.Now().Add(-4 * time.Hour).Format(time.RFC3339))
	testsCases := []struct {
		name              string
		inputPausedField  *string
		expectedCondition hyperv1.NodePoolCondition
	}{
		{
			name:             "if the pausedUntil field does not exist then ReconciliationActive condition is true",
			inputPausedField: nil,
			expectedCondition: hyperv1.NodePoolCondition{
				Type:               string(hyperv1.NodePoolReconciliationActiveConditionType),
				Status:             corev1.ConditionTrue,
				Reason:             reconciliationActiveConditionReason,
				Message:            "Reconciliation active on resource",
				ObservedGeneration: fakeInputGeneration,
			},
		},
		{
			name:             "if pausedUntil field is later than time.Now ReconciliationActive condition is false",
			inputPausedField: fakeFutureDate,
			expectedCondition: hyperv1.NodePoolCondition{
				Type:               string(hyperv1.NodePoolReconciliationActiveConditionType),
				Status:             corev1.ConditionFalse,
				Reason:             reconciliationPausedConditionReason,
				Message:            fmt.Sprintf("Reconciliation paused until: %s", *fakeFutureDate),
				ObservedGeneration: fakeInputGeneration,
			},
		},
		{
			name:             "if pausedUntil field is before time.Now then ReconciliationActive condition is true",
			inputPausedField: fakePastDate,
			expectedCondition: hyperv1.NodePoolCondition{
				Type:               string(hyperv1.NodePoolReconciliationActiveConditionType),
				Status:             corev1.ConditionTrue,
				Reason:             reconciliationActiveConditionReason,
				Message:            "Reconciliation active on resource",
				ObservedGeneration: fakeInputGeneration,
			},
		},
		{
			name:             "if pausedUntil field is true then ReconciliationActive condition is false",
			inputPausedField: ptr.To("true"),
			expectedCondition: hyperv1.NodePoolCondition{
				Type:               string(hyperv1.NodePoolReconciliationActiveConditionType),
				Status:             corev1.ConditionFalse,
				Reason:             reconciliationPausedConditionReason,
				Message:            "Reconciliation paused until field removed",
				ObservedGeneration: fakeInputGeneration,
			},
		},
		{
			name:             "if pausedUntil field has an improper value then ReconciliationActive condition is true with a reason indicating invalid value provided",
			inputPausedField: ptr.To("badValue"),
			expectedCondition: hyperv1.NodePoolCondition{
				Type:               string(hyperv1.NodePoolReconciliationActiveConditionType),
				Status:             corev1.ConditionTrue,
				Reason:             reconciliationInvalidPausedUntilConditionReason,
				Message:            "Invalid value provided for PausedUntil field",
				ObservedGeneration: fakeInputGeneration,
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			actualReconciliationActiveCondition := generateReconciliationActiveCondition(tc.inputPausedField, fakeInputGeneration)
			g.Expect(actualReconciliationActiveCondition).To(BeEquivalentTo(tc.expectedCondition))
		})
	}
}

func TestUpdatingConfigCondition(t *testing.T) {
	g := NewGomegaWithT(t)

	tests := []struct {
		name                  string
		upgradeType           hyperv1.UpgradeType
		machineSetExists      bool
		machineSetUpgradeFail bool
		isUpdatingConfig      bool
		expectedStatus        corev1.ConditionStatus
		expectedReason        string
		expectedMessagePart   string
	}{
		{
			name:                "NodePool is Replace and not updating config",
			expectedStatus:      corev1.ConditionFalse,
			expectedReason:      hyperv1.AsExpectedReason,
			expectedMessagePart: "",
			machineSetExists:    true,
			isUpdatingConfig:    false,
		},
		{
			name:                "NodePool is Replace and updating config",
			upgradeType:         hyperv1.UpgradeTypeReplace,
			expectedStatus:      corev1.ConditionTrue,
			expectedReason:      hyperv1.AsExpectedReason,
			expectedMessagePart: "Updating config in progress. Target config:",
			machineSetExists:    true,
			isUpdatingConfig:    true,
		},
		{
			name:                "NodePool is InPlace, machineSet does not exist and updating config",
			upgradeType:         hyperv1.UpgradeTypeInPlace,
			machineSetExists:    false,
			isUpdatingConfig:    true,
			expectedStatus:      corev1.ConditionTrue,
			expectedReason:      hyperv1.AsExpectedReason,
			expectedMessagePart: "Updating config in progress. Target config:",
		},
		{
			name:                  "NodePool is InPlace, machineSet exists, and updating config",
			upgradeType:           hyperv1.UpgradeTypeInPlace,
			machineSetExists:      true,
			machineSetUpgradeFail: false,
			isUpdatingConfig:      true,
			expectedStatus:        corev1.ConditionTrue,
			expectedReason:        hyperv1.AsExpectedReason,
			expectedMessagePart:   "true",
		},
		{
			name:                  "NodePool is InPlace, machineSet exists, and updating config fails",
			upgradeType:           hyperv1.UpgradeTypeInPlace,
			machineSetExists:      true,
			machineSetUpgradeFail: true,
			isUpdatingConfig:      true,
			expectedStatus:        corev1.ConditionFalse,
			expectedReason:        hyperv1.NodePoolInplaceUpgradeFailedReason,
			expectedMessagePart:   "true",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()

			pullSecret, ignitionServerCACert, machineConfig, ignitionConfig, ignitionConfig2, ignitionConfig3 := setupTestObjects()

			hostedCluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster-name", Namespace: "myns"},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{Name: pullSecret.Name},
					InfraID:    "fake-infra-id",
				},
				Status: hyperv1.HostedClusterStatus{
					IgnitionEndpoint: "https://ignition.cluster-name.myns.devcluster.openshift.com",
				},
			}

			//change pull secret name to simulate node pool config update
			if tc.isUpdatingConfig {
				hostedCluster.Spec.PullSecret.Name = "new-pull"
				pullSecret.ObjectMeta.Name = "new-pull"
			}

			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nodepool-name",
					Namespace: "myns",
					Annotations: map[string]string{
						nodePoolAnnotationCurrentConfig: "08e4f890",
					},
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: hostedCluster.Name,
					Release: hyperv1.Release{
						Image: "fake-release-image"},
					Management: hyperv1.NodePoolManagement{
						UpgradeType: tc.upgradeType,
					},
					Config: []corev1.LocalObjectReference{
						{Name: "machineconfig-1"},
					},
				},
				Status: hyperv1.NodePoolStatus{
					Version: semver.MustParse("4.18.0").String(),
				},
			}

			client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(
				nodePool,
				hostedCluster,
				pullSecret,
				ignitionServerCACert,
				machineConfig,
				ignitionConfig,
				ignitionConfig2,
				ignitionConfig3,
			).Build()

			r := &NodePoolReconciler{
				Client:          client,
				ReleaseProvider: &fakereleaseprovider.FakeReleaseProvider{Version: semver.MustParse("4.18.0").String()},
				ImageMetadataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
					Result: &dockerv1client.DockerImageConfig{
						Config: &docker10.DockerConfig{
							Labels: map[string]string{},
						},
					},
				},
			}

			if tc.machineSetExists {
				machineSet := setUpDummyMachineSet(nodePool, hostedCluster, tc.machineSetUpgradeFail)

				err := client.Create(ctx, machineSet)
				if err != nil {
					return
				}
			}

			_, err := r.updatingConfigCondition(ctx, nodePool, hostedCluster)
			g.Expect(err).To(BeNil())

			condition := FindStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolUpdatingConfigConditionType)
			g.Expect(condition).NotTo(BeNil())
			g.Expect(condition.Status).To(Equal(tc.expectedStatus))
			g.Expect(condition.Reason).To(Equal(tc.expectedReason))
			if tc.expectedMessagePart != "" {
				g.Expect(condition.Message).To(ContainSubstring(tc.expectedMessagePart))
			}
		})
	}
}

func TestUpdatingVersionCondition(t *testing.T) {
	g := NewGomegaWithT(t)

	tests := []struct {
		name                  string
		upgradeType           hyperv1.UpgradeType
		machineSetExists      bool
		machineSetUpgradeFail bool
		isUpdatingVersion     bool
		expectedStatus        corev1.ConditionStatus
		expectedReason        string
		expectedMessagePart   string
	}{
		{
			name:                "NodePool is Replace and not updating version",
			expectedStatus:      corev1.ConditionFalse,
			expectedReason:      hyperv1.AsExpectedReason,
			expectedMessagePart: "",
			machineSetExists:    true,
			isUpdatingVersion:   false,
		},
		{
			name:                "NodePool is Replace and updating version",
			upgradeType:         hyperv1.UpgradeTypeReplace,
			expectedStatus:      corev1.ConditionTrue,
			expectedReason:      hyperv1.AsExpectedReason,
			expectedMessagePart: "Updating version in progress. Target version:",
			machineSetExists:    true,
			isUpdatingVersion:   true,
		},
		{
			name:                "NodePool is InPlace, machineSet does not exist and updating version",
			upgradeType:         hyperv1.UpgradeTypeInPlace,
			machineSetExists:    false,
			isUpdatingVersion:   true,
			expectedStatus:      corev1.ConditionTrue,
			expectedReason:      hyperv1.AsExpectedReason,
			expectedMessagePart: "Updating version in progress. Target version:",
		},
		{
			name:                  "NodePool is InPlace, machineSet exists, and updating version",
			upgradeType:           hyperv1.UpgradeTypeInPlace,
			machineSetExists:      true,
			machineSetUpgradeFail: false,
			isUpdatingVersion:     true,
			expectedStatus:        corev1.ConditionTrue,
			expectedReason:        hyperv1.AsExpectedReason,
			expectedMessagePart:   "true",
		},
		{
			name:                  "NodePool is InPlace, machineSet exists, and updating version fails",
			upgradeType:           hyperv1.UpgradeTypeInPlace,
			machineSetExists:      true,
			machineSetUpgradeFail: true,
			isUpdatingVersion:     true,
			expectedStatus:        corev1.ConditionFalse,
			expectedReason:        hyperv1.NodePoolInplaceUpgradeFailedReason,
			expectedMessagePart:   "true",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()

			pullSecret, ignitionServerCACert, machineConfig, ignitionConfig, ignitionConfig2, ignitionConfig3 := setupTestObjects()

			hostedCluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster-name", Namespace: "myns"},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{Name: pullSecret.Name},
					InfraID:    "fake-infra-id",
				},
				Status: hyperv1.HostedClusterStatus{
					IgnitionEndpoint: "https://ignition.cluster-name.myns.devcluster.openshift.com",
				},
			}

			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nodepool-name",
					Namespace: "myns",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: hostedCluster.Name,
					Release: hyperv1.Release{
						Image: "fake-release-image"},
					Management: hyperv1.NodePoolManagement{
						UpgradeType: tc.upgradeType,
					},
					Config: []corev1.LocalObjectReference{
						{Name: "machineconfig-1"},
					},
				},
				Status: hyperv1.NodePoolStatus{
					Version: semver.MustParse("4.18.0").String(),
				},
			}

			if tc.isUpdatingVersion {
				nodePool.Spec.Release.Image = "new-release-image"
				nodePool.Status.Version = semver.MustParse("4.18.1").String()
			}

			client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(
				nodePool,
				hostedCluster,
				pullSecret,
				ignitionServerCACert,
				machineConfig,
				ignitionConfig,
				ignitionConfig2,
				ignitionConfig3,
			).Build()

			r := &NodePoolReconciler{
				Client:          client,
				ReleaseProvider: &fakereleaseprovider.FakeReleaseProvider{Version: semver.MustParse("4.18.0").String()},
				ImageMetadataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
					Result: &dockerv1client.DockerImageConfig{
						Config: &docker10.DockerConfig{
							Labels: map[string]string{},
						},
					},
				},
			}

			if tc.machineSetExists {
				machineSet := setUpDummyMachineSet(nodePool, hostedCluster, tc.machineSetUpgradeFail)

				err := client.Create(ctx, machineSet)
				if err != nil {
					return
				}
			}

			_, err := r.updatingVersionCondition(ctx, nodePool, hostedCluster)
			g.Expect(err).To(BeNil())

			condition := FindStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolUpdatingVersionConditionType)
			g.Expect(condition).NotTo(BeNil())
			g.Expect(condition.Status).To(Equal(tc.expectedStatus))
			g.Expect(condition.Reason).To(Equal(tc.expectedReason))
			if tc.expectedMessagePart != "" {
				g.Expect(condition.Message).To(ContainSubstring(tc.expectedMessagePart))
			}
		})
	}
}

func setupTestObjects() (*corev1.Secret, *corev1.Secret, *corev1.ConfigMap, *corev1.ConfigMap, *corev1.ConfigMap, *corev1.ConfigMap) {
	coreMachineConfig := `
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: master
  name: config-1
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents:
        source: "[Service]\nType=oneshot\nExecStart=/usr/bin/echo Hello World\n\n[Install]\nWantedBy=multi-user.target"
        filesystem: root
        mode: 493
        path: /usr/local/bin/file1.sh
`

	machineConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "machineconfig-1",
			Namespace: "myns",
		},
		Data: map[string]string{
			TokenSecretConfigKey: coreMachineConfig,
		},
	}

	ignitionConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "core-machineconfig",
			Namespace: "myns-cluster-name",
			Labels: map[string]string{
				nodePoolCoreIgnitionConfigLabel: "true",
			},
		},
		Data: map[string]string{
			TokenSecretConfigKey: coreMachineConfig,
		},
	}
	ignitionConfig2 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "core-machineconfig-2",
			Namespace: "myns-cluster-name",
			Labels: map[string]string{
				nodePoolCoreIgnitionConfigLabel: "true",
			},
		},
		Data: map[string]string{
			TokenSecretConfigKey: coreMachineConfig,
		},
	}
	ignitionConfig3 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "core-machineconfig-3",
			Namespace: "myns-cluster-name",
			Labels: map[string]string{
				nodePoolCoreIgnitionConfigLabel: "true",
			},
		},
		Data: map[string]string{
			TokenSecretConfigKey: coreMachineConfig,
		},
	}

	ignitionServerCACert := ignitionserver.IgnitionCACertSecret("myns-cluster-name")
	ignitionServerCACert.Data = map[string][]byte{
		corev1.TLSCertKey: []byte("test-ignition-ca-cert"),
	}

	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "myns"},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte("whatever"),
		},
	}

	return pullSecret, ignitionServerCACert, machineConfig, ignitionConfig, ignitionConfig2, ignitionConfig3
}

func setUpDummyMachineSet(nodePool *hyperv1.NodePool, hostedCluster *hyperv1.HostedCluster, machineSetUpgradeFail bool) *v1beta1.MachineSet {
	machineSet := &v1beta1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nodePool.Name,
			Namespace: hostedCluster.Namespace + "-" + hostedCluster.Name,
			Annotations: map[string]string{
				nodePoolAnnotationUpgradeInProgressTrue: "true",
			},
		},
	}

	if machineSetUpgradeFail {
		machineSet.Annotations = map[string]string{
			nodePoolAnnotationUpgradeInProgressFalse: "true",
		}
	}
	return machineSet
}

func TestAutoscalerEnabledCondition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		nodePool       *hyperv1.NodePool
		expectedStatus corev1.ConditionStatus
		expectedReason string
		expectMessage  string
	}{
		{
			name: "When autoscaling is not enabled, it should set condition to false with AsExpected",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Replicas: ptr.To[int32](2),
				},
			},
			expectedStatus: corev1.ConditionFalse,
			expectedReason: hyperv1.AsExpectedReason,
		},
		{
			name: "When autoscaling is enabled with min greater than zero, it should set condition to true",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](1),
						Max: 5,
					},
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			expectedStatus: corev1.ConditionTrue,
			expectedReason: hyperv1.AsExpectedReason,
			expectMessage:  "Minimum nodes: 1",
		},
		{
			name: "When autoscaling from zero on a supported platform, it should set condition to true",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](0),
						Max: 5,
					},
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			expectedStatus: corev1.ConditionTrue,
			expectedReason: hyperv1.AsExpectedReason,
			expectMessage:  "Minimum nodes: 0",
		},
		{
			name: "When autoscaling from zero on an unsupported platform, it should set condition to false with validation failure",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](0),
						Max: 5,
					},
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
					},
				},
			},
			expectedStatus: corev1.ConditionFalse,
			expectedReason: hyperv1.NodePoolValidationFailedReason,
			expectMessage:  "not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)

			r := &NodePoolReconciler{}
			_, err := r.autoscalerEnabledCondition(t.Context(), tt.nodePool, nil)
			g.Expect(err).To(BeNil())

			condition := FindStatusCondition(tt.nodePool.Status.Conditions, hyperv1.NodePoolAutoscalingEnabledConditionType)
			g.Expect(condition).NotTo(BeNil())
			g.Expect(condition.Status).To(Equal(tt.expectedStatus))
			g.Expect(condition.Reason).To(Equal(tt.expectedReason))
			if tt.expectMessage != "" {
				g.Expect(condition.Message).To(ContainSubstring(tt.expectMessage))
			}
		})
	}
}
