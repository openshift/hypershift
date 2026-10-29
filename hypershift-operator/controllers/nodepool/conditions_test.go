package nodepool

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	ignserver "github.com/openshift/hypershift/ignition-server/controllers"
	"github.com/openshift/hypershift/support/api"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	"github.com/openshift/api/image/docker10"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/blang/semver"
)

func TestSetValidMachineConfigConditionForError(t *testing.T) {
	testCases := []struct {
		name           string
		err            error
		expectedReason string
		expectError    bool
	}{
		{
			name: "When trust bundle ConfigMap is not found, it should soft-fail with NotFound reason",
			err: &TrustBundleConfigError{err: fmt.Errorf("cannot get ConfigMap clusters/user-ca: %w",
				apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, "user-ca"))},
			expectedReason: hyperv1.NodePoolNotFoundReason,
			expectError:    false,
		},
		{
			name: "When trust bundle ConfigMap is missing ca-bundle.crt, it should soft-fail with ValidationFailed reason",
			err: &TrustBundleConfigError{err: fmt.Errorf("ConfigMap clusters/user-ca missing %q key",
				"ca-bundle.crt")},
			expectedReason: hyperv1.NodePoolValidationFailedReason,
			expectError:    false,
		},
		{
			name:           "When a non-trust-bundle config error occurs, it should hard-fail with ValidationFailed reason",
			err:            fmt.Errorf("expected 3 core ignition configs, found 0"),
			expectedReason: hyperv1.NodePoolValidationFailedReason,
			expectError:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "workers", Namespace: "clusters", Generation: 2},
			}

			result, err := setValidMachineConfigConditionForError(nodePool, tc.err)
			g.Expect(result).NotTo(BeNil())
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}

			condition := FindStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolValidMachineConfigConditionType)
			g.Expect(condition).NotTo(BeNil())
			g.Expect(condition.Status).To(Equal(corev1.ConditionFalse))
			g.Expect(condition.Reason).To(Equal(tc.expectedReason))
			g.Expect(condition.Message).To(Equal(tc.err.Error()))
			g.Expect(condition.ObservedGeneration).To(Equal(nodePool.Generation))
		})
	}
}

func TestIgnitionEndpointAvailableCondition(t *testing.T) {
	const ignitionHostname = "ignition-example.service.hypershift.example.com"

	ignitionRouteService := func(hostname string) []hyperv1.ServicePublishingStrategyMapping {
		return []hyperv1.ServicePublishingStrategyMapping{
			{
				Service: hyperv1.Ignition,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					Type: hyperv1.Route,
					Route: &hyperv1.RoutePublishingStrategy{
						Hostname: hostname,
					},
				},
			},
		}
	}
	publicAzurePlatform := hyperv1.PlatformSpec{
		Type: hyperv1.AzurePlatform,
		Azure: &hyperv1.AzurePlatformSpec{
			Topology: hyperv1.AzureTopologyPublic,
		},
	}
	privateAzurePlatform := hyperv1.PlatformSpec{
		Type: hyperv1.AzurePlatform,
		Azure: &hyperv1.AzurePlatformSpec{
			Topology: hyperv1.AzureTopologyPrivate,
		},
	}
	awsPlatform := hyperv1.PlatformSpec{
		Type: hyperv1.AWSPlatform,
		AWS: &hyperv1.AWSPlatformSpec{
			EndpointAccess: hyperv1.Public,
		},
	}
	dnsLookupErr := fmt.Errorf("lookup %s: no such host", ignitionHostname)

	testCases := []struct {
		name                    string
		platform                hyperv1.PlatformSpec
		services                []hyperv1.ServicePublishingStrategyMapping
		ignitionEndpoint        string
		resolveErr              error
		expectResolverCall      bool
		expectedRequeueAfter    time.Duration
		expectedConditionReason string
		expectedMessagePart     string
	}{
		{
			name:                    "When an Azure public ignition hostname is not resolvable, it should wait before reconciling machines",
			platform:                publicAzurePlatform,
			services:                ignitionRouteService(ignitionHostname),
			ignitionEndpoint:        ignitionHostname,
			resolveErr:              dnsLookupErr,
			expectResolverCall:      true,
			expectedRequeueAfter:    10 * time.Second,
			expectedConditionReason: hyperv1.ExternalDNSHostNotReachableReason,
			expectedMessagePart:     ignitionHostname,
		},
		{
			name:               "When an Azure public ignition hostname is resolvable, it should continue reconciliation",
			platform:           publicAzurePlatform,
			services:           ignitionRouteService(ignitionHostname),
			ignitionEndpoint:   ignitionHostname,
			expectResolverCall: true,
		},
		{
			name:               "When an Azure ignition hostname is private, it should skip public DNS resolution",
			platform:           privateAzurePlatform,
			services:           ignitionRouteService(ignitionHostname),
			ignitionEndpoint:   ignitionHostname,
			expectResolverCall: false,
		},
		{
			name:               "When a non-Azure ignition hostname is configured, it should skip Azure DNS readiness",
			platform:           awsPlatform,
			services:           ignitionRouteService(ignitionHostname),
			ignitionEndpoint:   ignitionHostname,
			expectResolverCall: false,
		},
		{
			name:               "When Azure external DNS is not configured, it should skip DNS resolution",
			platform:           publicAzurePlatform,
			services:           ignitionRouteService(""),
			ignitionEndpoint:   "ignition-server.apps.management.example.com",
			expectResolverCall: false,
		},
		{
			name:                    "When the ignition endpoint is missing, it should wait without resolving DNS",
			platform:                publicAzurePlatform,
			services:                ignitionRouteService(ignitionHostname),
			expectResolverCall:      false,
			expectedConditionReason: hyperv1.IgnitionEndpointMissingReason,
			expectedMessagePart:     "Ignition endpoint not available",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := t.Context()

			hostedCluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "example",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: tc.platform,
					Services: tc.services,
				},
				Status: hyperv1.HostedClusterStatus{
					IgnitionEndpoint: tc.ignitionEndpoint,
				},
			}
			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "workers",
					Namespace:  "clusters",
					Generation: 3,
				},
				Status: hyperv1.NodePoolStatus{
					Conditions: []hyperv1.NodePoolCondition{
						{
							Type:   string(hyperv1.IgnitionEndpointAvailable),
							Status: corev1.ConditionFalse,
							Reason: hyperv1.IgnitionEndpointMissingReason,
						},
					},
				},
			}
			ignitionCACert := ignitionserver.IgnitionCACertSecret("clusters-example")
			ignitionCACert.Data = map[string][]byte{
				corev1.TLSCertKey: []byte("test-ignition-ca-cert"),
			}

			resolverCalled := false
			reconciler := &NodePoolReconciler{
				Client: fake.NewClientBuilder().
					WithScheme(api.Scheme).
					WithObjects(ignitionCACert).
					Build(),
				resolveDNSHostname: func(_ context.Context, hostname string) error {
					resolverCalled = true
					g.Expect(hostname).To(Equal(ignitionHostname))
					return tc.resolveErr
				},
			}

			result, err := reconciler.ignitionEndpointAvailableCondition(ctx, nodePool, hostedCluster)
			g.Expect(err).NotTo(HaveOccurred(), "ignitionEndpointAvailableCondition should not return an error")
			g.Expect(resolverCalled).To(Equal(tc.expectResolverCall))
			if tc.expectedConditionReason != "" {
				g.Expect(result).NotTo(BeNil())
				g.Expect(result).To(Equal(&ctrl.Result{RequeueAfter: tc.expectedRequeueAfter}))
			} else {
				g.Expect(result).To(BeNil())
			}

			condition := FindStatusCondition(nodePool.Status.Conditions, string(hyperv1.IgnitionEndpointAvailable))
			if tc.expectedConditionReason == "" {
				g.Expect(condition).To(BeNil())
				return
			}
			g.Expect(condition).NotTo(BeNil())
			g.Expect(condition.Status).To(Equal(corev1.ConditionFalse))
			g.Expect(condition.Reason).To(Equal(tc.expectedConditionReason))
			g.Expect(condition.Message).To(ContainSubstring(tc.expectedMessagePart))
			g.Expect(condition.ObservedGeneration).To(Equal(nodePool.Generation))
		})
	}
}

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
		freshNodePool         bool
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
			// A fresh NodePool has no rollout annotation until its first
			// reconcile seeds it; the initial config application is not a
			// change from a completed baseline, so UpdatingConfig is False
			// (UpdatingVersion surfaces the initial rollout instead).
			name:                "NodePool is fresh (no rollout annotation yet) and not updating config",
			freshNodePool:       true,
			expectedStatus:      corev1.ConditionFalse,
			expectedReason:      hyperv1.AsExpectedReason,
			expectedMessagePart: "",
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
			kubeconfig := withKubeconfig(hostedCluster)

			// change pull secret name to simulate node pool config update
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
						Image: "fake-release-image",
					},
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
				kubeconfig,
			).Build()

			releaseVersion := semver.MustParse("4.18.0").String()
			r := &NodePoolReconciler{
				Client:          client,
				ReleaseProvider: &fakereleaseprovider.FakeReleaseProvider{Version: releaseVersion},
				ImageMetadataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
					Result: &dockerv1client.DockerImageConfig{
						Config: &docker10.DockerConfig{
							Labels: map[string]string{"io.openshift.hypershift.control-plane-operator-manages.rendered-trust-bundle-validation": "true"},
						},
					},
				},
			}

			// Compute the baseline rollout hash from the pre-mutation inputs so
			// isUpdatingConfig can detect whether the mutation changed it.
			if tc.freshNodePool {
				// A fresh NodePool has no rollout annotation until its first
				// reconcile seeds it.
				nodePool.Annotations = map[string]string{}
			} else if !tc.isUpdatingConfig {
				baselineToken, tokenErr := r.token(ctx, hostedCluster, nodePool)
				g.Expect(tokenErr).To(BeNil())
				nodePool.Annotations[nodePoolAnnotationCurrentRolloutConfig] = baselineToken.RolloutHashWithoutVersion()
			} else {
				// For the "updating" cases, seed a stale rollout hash that won't
				// match the post-mutation token.
				nodePool.Annotations[nodePoolAnnotationCurrentRolloutConfig] = "stale-rollout-hash"
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

			kubeconfig := withKubeconfig(hostedCluster)

			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nodepool-name",
					Namespace: "myns",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: hostedCluster.Name,
					Release: hyperv1.Release{
						Image: "fake-release-image",
					},
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
				kubeconfig,
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
							Labels: map[string]string{"io.openshift.hypershift.control-plane-operator-manages.rendered-trust-bundle-validation": "true"},
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

func TestValidMachineConfigCondition(t *testing.T) {
	g := NewGomegaWithT(t)

	pullSecret, ignitionServerCACert, machineConfig, ignitionConfig, ignitionConfig2, ignitionConfig3 := setupTestObjects()

	hostedCluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-name", Namespace: "myns"},
		Spec: hyperv1.HostedClusterSpec{
			PullSecret: corev1.LocalObjectReference{Name: pullSecret.Name},
			InfraID:    "fake-infra-id",
		},
	}
	kubeconfig := withKubeconfig(hostedCluster)

	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nodepool-name",
			Namespace: "myns",
		},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: hostedCluster.Name,
			Release:     hyperv1.Release{Image: "fake-release-image"},
			Config:      []corev1.LocalObjectReference{{Name: "machineconfig-1"}},
		},
		Status: hyperv1.NodePoolStatus{
			Version: semver.MustParse("4.18.0").String(),
		},
	}

	client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(
		nodePool, hostedCluster, pullSecret, ignitionServerCACert,
		machineConfig, ignitionConfig, ignitionConfig2, ignitionConfig3,
		kubeconfig,
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

	_, err := r.validMachineConfigCondition(t.Context(), nodePool, hostedCluster)
	g.Expect(err).ToNot(HaveOccurred())

	cond := FindStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolValidMachineConfigConditionType)
	g.Expect(cond).ToNot(BeNil())
	g.Expect(cond.Status).To(Equal(corev1.ConditionTrue))
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

// withKubeconfig points the HostedCluster's status at an admin kubeconfig
// secret and returns that secret, mirroring an established public cluster:
// resolving the API server endpoint, which feeds the rollout hash, requires
// the secret to be present.
func withKubeconfig(hc *hyperv1.HostedCluster) *corev1.Secret {
	kubeconfig := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: hc.Name + "-kubeconfig", Namespace: hc.Namespace},
		Data: map[string][]byte{
			"kubeconfig": []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: cluster
  cluster:
    server: https://api.%s.%s.example.com:6443
contexts:
- name: context
  context:
    cluster: cluster
    user: user
current-context: context
users:
- name: user
  user: {}
`, hc.Name, hc.Namespace)),
		},
	}
	hc.Status.KubeConfig = &corev1.LocalObjectReference{Name: kubeconfig.Name}
	return kubeconfig
}

func setUpDummyMachineSet(nodePool *hyperv1.NodePool, hostedCluster *hyperv1.HostedCluster, machineSetUpgradeFail bool) *v1beta2.MachineSet {
	machineSet := &v1beta2.MachineSet{
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

func TestValidateKubevirtAdditionalNetworkNamespaces(t *testing.T) {
	tests := []struct {
		name        string
		nodePool    *hyperv1.NodePool
		hc          *hyperv1.HostedCluster
		expectError bool
		errContains string
	}{
		{
			name: "When NAD is in HCP namespace, it should pass validation",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtNodePoolPlatform{
							AdditionalNetworks: []hyperv1.KubevirtNetwork{
								{Name: "clusters-mycluster/mynad"},
							},
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "clusters",
					Name:      "mycluster",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
					},
				},
			},
			expectError: false,
		},
		{
			name: "When NAD is in default namespace, it should pass validation",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtNodePoolPlatform{
							AdditionalNetworks: []hyperv1.KubevirtNetwork{
								{Name: "default/mynad"},
							},
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "clusters",
					Name:      "mycluster",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
					},
				},
			},
			expectError: false,
		},
		{
			name: "When NAD is in HC namespace instead of HCP namespace, it should return namespace mismatch error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtNodePoolPlatform{
							AdditionalNetworks: []hyperv1.KubevirtNetwork{
								{Name: "clusters/mynad"},
							},
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "clusters",
					Name:      "mycluster",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
					},
				},
			},
			expectError: true,
			errContains: "virt-launcher pods run in namespace",
		},
		{
			name: "When NAD is in external infra namespace, it should pass validation",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtNodePoolPlatform{
							AdditionalNetworks: []hyperv1.KubevirtNetwork{
								{Name: "ext-infra/mynad"},
							},
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "clusters",
					Name:      "mycluster",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							Credentials: &hyperv1.KubevirtPlatformCredentials{
								InfraNamespace: "ext-infra",
							},
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "When NAD is in HC namespace with external infra configured, it should return namespace mismatch error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtNodePoolPlatform{
							AdditionalNetworks: []hyperv1.KubevirtNetwork{
								{Name: "clusters/mynad"},
							},
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "clusters",
					Name:      "mycluster",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							Credentials: &hyperv1.KubevirtPlatformCredentials{
								InfraNamespace: "ext-infra",
							},
						},
					},
				},
			},
			expectError: true,
			errContains: "virt-launcher pods run in namespace",
		},
		{
			name: "When HC name has dots, it should resolve HCP namespace with hyphens and pass",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtNodePoolPlatform{
							AdditionalNetworks: []hyperv1.KubevirtNetwork{
								{Name: "clusters-my-cluster/mynad"},
							},
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "clusters",
					Name:      "my.cluster",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
					},
				},
			},
			expectError: false,
		},
		{
			name: "When no additional networks are configured, it should pass validation",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtNodePoolPlatform{
							AdditionalNetworks: []hyperv1.KubevirtNetwork{},
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "clusters",
					Name:      "mycluster",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
					},
				},
			},
			expectError: false,
		},
		{
			name: "When NAD name has no namespace qualifier, it should pass validation",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtNodePoolPlatform{
							AdditionalNetworks: []hyperv1.KubevirtNetwork{
								{Name: "mynad-no-namespace"},
							},
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "clusters",
					Name:      "mycluster",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
					},
				},
			},
			expectError: false,
		},
		{
			name: "When multiple NADs with first valid and second in wrong namespace, it should return error for the invalid entry",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtNodePoolPlatform{
							AdditionalNetworks: []hyperv1.KubevirtNetwork{
								{Name: "clusters-mycluster/valid-nad"},
								{Name: "wrong-ns/bad-nad"},
							},
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "clusters",
					Name:      "mycluster",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
					},
				},
			},
			expectError: true,
			errContains: "wrong-ns/bad-nad",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			r := &NodePoolReconciler{}
			err := r.validateKubevirtAdditionalNetworkNamespaces(tc.nodePool, tc.hc)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred(), tc.name)
				g.Expect(err.Error()).To(ContainSubstring(tc.errContains), tc.name)
			} else {
				g.Expect(err).ToNot(HaveOccurred(), tc.name)
			}
		})
	}
}
func TestConfigUpdatePendingCondition(t *testing.T) {
	g := NewGomegaWithT(t)

	tests := []struct {
		name           string
		isUpdatingSpec bool
		hasDrift       bool
		expectedStatus corev1.ConditionStatus
		expectedReason string
	}{
		{
			name:           "When there is no drift and no update in progress, it should report condition as false",
			isUpdatingSpec: false,
			hasDrift:       false,
			expectedStatus: corev1.ConditionFalse,
			expectedReason: hyperv1.AsExpectedReason,
		},
		{
			name:           "When management drift is detected and no spec update is in progress, it should report condition as true",
			isUpdatingSpec: false,
			hasDrift:       true,
			expectedStatus: corev1.ConditionTrue,
			expectedReason: hyperv1.ManagementConfigDriftReason,
		},
		{
			name:           "When both drift and spec update are in progress, it should report condition as false because UpdatingConfig takes precedence",
			isUpdatingSpec: true,
			hasDrift:       true,
			expectedStatus: corev1.ConditionFalse,
			expectedReason: hyperv1.AsExpectedReason,
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
			kubeconfig := withKubeconfig(hostedCluster)

			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nodepool-name",
					Namespace: "myns",
					Annotations: map[string]string{
						nodePoolAnnotationCurrentConfig: "some-full-hash",
					},
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: hostedCluster.Name,
					Release: hyperv1.Release{
						Image: "fake-release-image",
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
				kubeconfig,
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

			// First, compute the actual token to get hashes
			token, err := r.token(ctx, hostedCluster, nodePool)
			g.Expect(err).To(BeNil())

			rolloutHash := token.RolloutHashWithoutVersion()
			fullHash := token.Hash()

			// Set the current rollout config to match (no spec change)
			nodePool.Annotations[token.RolloutConfigAnnotation()] = rolloutHash

			if tc.isUpdatingSpec {
				// Simulate a spec-driven rollout in progress by making the rollout hash differ
				nodePool.Annotations[token.RolloutConfigAnnotation()] = "stale-rollout-hash"
			}

			if tc.hasDrift {
				// Simulate management-side drift: full hash differs from current but rollout hash matches
				nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion] = "old-full-hash-differs-from-" + fullHash
			} else {
				nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion] = fullHash
			}

			_, err = r.configUpdatePendingCondition(ctx, nodePool, hostedCluster)
			g.Expect(err).To(BeNil())

			condition := FindStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolConfigUpdatePendingConditionType)
			g.Expect(condition).NotTo(BeNil())
			g.Expect(condition.Status).To(Equal(tc.expectedStatus))
			g.Expect(condition.Reason).To(Equal(tc.expectedReason))
		})
	}
}

// payloadConditionScenario selects a secret-maintenance state for the payload
// condition tests (ValidGeneratedPayload, ReachedIgnitionEndpoint).
type payloadConditionScenario struct {
	// inFlightRollout selects stateContinuedRollout in resolveEffectiveHash:
	// the completed rollout annotation is stale, the in-progress annotation
	// matches the current rollout hash, and the CAPI workload references a
	// full hash that differs from the currently calculated one
	// (management-side drift during an in-flight rollout).
	inFlightRollout bool
	// noWorkload means no CAPI workload exists yet (young NodePool).
	noWorkload bool
	// noInfraID means the HostedCluster has no InfraID yet.
	noInfraID bool
}

// setupPayloadConditionTest builds a reconciler with a NodePool in the given
// scenario, creating the CAPI workload and the token secret that the
// scenario's effective hash resolves to. The token secret carries both a
// valid payload reason and the ignition-reached annotation so it satisfies
// the ValidGeneratedPayload and ReachedIgnitionEndpoint conditions alike.
func setupPayloadConditionTest(t *testing.T, scenario payloadConditionScenario) (*NodePoolReconciler, *hyperv1.NodePool, *hyperv1.HostedCluster) {
	g := NewGomegaWithT(t)
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
	kubeconfig := withKubeconfig(hostedCluster)
	if scenario.noInfraID {
		hostedCluster.Spec.InfraID = ""
	}

	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "nodepool-name",
			Namespace:   "myns",
			Annotations: map[string]string{},
		},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: hostedCluster.Name,
			Release: hyperv1.Release{
				Image: "fake-release-image",
			},
			Management: hyperv1.NodePoolManagement{
				UpgradeType: hyperv1.UpgradeTypeReplace,
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
		kubeconfig,
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

	// Compute the hashes the condition's own token build will reproduce.
	// Annotations are not hash inputs, so setting them below does not change
	// the hashes the conditions calculate.
	baselineToken, err := r.token(ctx, hostedCluster, nodePool)
	g.Expect(err).To(BeNil())
	currentHash := baselineToken.Hash()

	// extractHashFromSecretName splits on "-", so the fake hash must not
	// contain hyphens.
	const deployedHash = "deadbeefcafe"

	if scenario.inFlightRollout {
		nodePool.Annotations[nodePoolAnnotationCurrentRolloutConfig] = "stale-rollout-hash"
		nodePool.Annotations[nodePoolAnnotationInProgressRolloutConfig] = baselineToken.RolloutHash()
		nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion] = "oldcompletedhash"
	} else {
		nodePool.Annotations[nodePoolAnnotationCurrentRolloutConfig] = baselineToken.RolloutHashWithoutVersion()
		nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion] = currentHash
	}

	// The hash the conditions are expected to resolve: during an in-flight
	// rollout with drift it is the deployed hash, otherwise the current one.
	resolvedHash := currentHash
	if !scenario.noWorkload {
		workloadHash := currentHash
		if scenario.inFlightRollout {
			workloadHash = deployedHash
			resolvedHash = deployedHash
		}
		machineDeployment := &v1beta2.MachineDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      nodePool.Name,
				Namespace: hostedCluster.Namespace + "-" + hostedCluster.Name,
			},
			Spec: v1beta2.MachineDeploymentSpec{
				Template: v1beta2.MachineTemplateSpec{
					Spec: v1beta2.MachineSpec{
						Bootstrap: v1beta2.Bootstrap{
							DataSecretName: ptr.To(fmt.Sprintf("%s-%s-%s", UserDataSecrePrefix, nodePool.Name, workloadHash)),
						},
					},
				},
			},
		}
		err = client.Create(ctx, machineDeployment)
		g.Expect(err).To(BeNil())
	}

	tokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s-%s", TokenSecretPrefix, nodePool.Name, resolvedHash),
			Namespace: hostedCluster.Namespace + "-" + hostedCluster.Name,
			Annotations: map[string]string{
				TokenSecretIgnitionReachedAnnotation: "True",
			},
		},
		Data: map[string][]byte{
			ignserver.TokenSecretReasonKey: []byte(hyperv1.AsExpectedReason),
		},
	}
	err = client.Create(ctx, tokenSecret)
	g.Expect(err).To(BeNil())

	return r, nodePool, hostedCluster
}

func TestValidGeneratedPayloadCondition(t *testing.T) {
	g := NewGomegaWithT(t)

	tests := []struct {
		name     string
		scenario payloadConditionScenario
	}{
		{
			name:     "steady state resolves the current token secret",
			scenario: payloadConditionScenario{},
		},
		{
			// Regression test: the token built by r.token() carries no
			// deployed bootstrap hash. Without resolving it,
			// resolveEffectiveHash cannot select ContinuedRollout and the
			// condition looks up the current Hash() secret, which does not
			// exist while the rollout is in flight.
			name: "in-flight rollout with management drift resolves the deployed token secret",
			scenario: payloadConditionScenario{
				inFlightRollout: true,
			},
		},
		{
			name: "no CAPI workload resolves the current token secret",
			scenario: payloadConditionScenario{
				noWorkload: true,
			},
		},
		{
			name: "HostedCluster without InfraID resolves the current token secret",
			scenario: payloadConditionScenario{
				noInfraID:  true,
				noWorkload: true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			r, nodePool, hostedCluster := setupPayloadConditionTest(t, tc.scenario)

			_, err := r.validGeneratedPayloadCondition(ctx, nodePool, hostedCluster)
			g.Expect(err).To(BeNil())

			condition := FindStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolValidGeneratedPayloadConditionType)
			g.Expect(condition).NotTo(BeNil())
			g.Expect(condition.Status).To(Equal(corev1.ConditionTrue))
			g.Expect(condition.Reason).To(Equal(hyperv1.AsExpectedReason))
		})
	}
}

func TestReachedIgnitionEndpointCondition(t *testing.T) {
	g := NewGomegaWithT(t)

	tests := []struct {
		name     string
		scenario payloadConditionScenario
	}{
		{
			name:     "steady state resolves the current token secret",
			scenario: payloadConditionScenario{},
		},
		{
			// Regression test: see TestValidGeneratedPayloadCondition.
			name: "in-flight rollout with management drift resolves the deployed token secret",
			scenario: payloadConditionScenario{
				inFlightRollout: true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			r, nodePool, hostedCluster := setupPayloadConditionTest(t, tc.scenario)

			_, err := r.reachedIgnitionEndpointCondition(ctx, nodePool, hostedCluster)
			g.Expect(err).To(BeNil())

			condition := FindStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolReachedIgnitionEndpoint)
			g.Expect(condition).NotTo(BeNil())
			g.Expect(condition.Status).To(Equal(corev1.ConditionTrue))
			g.Expect(condition.Reason).To(Equal(hyperv1.AsExpectedReason))
		})
	}
}
