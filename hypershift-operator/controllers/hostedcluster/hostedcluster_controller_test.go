package hostedcluster

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/cmd/version"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/kubevirt"
	hcmetrics "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/metrics"
	hcpmanifests "github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/clusterapi"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	kvinfra "github.com/openshift/hypershift/kubevirtexternalinfra"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/capabilities"
	fakecapabilities "github.com/openshift/hypershift/support/capabilities/fake"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/releaseinfo/registryclient"
	"github.com/openshift/hypershift/support/releaseinfo/testutils"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/upsert"
	hyperutil "github.com/openshift/hypershift/support/util"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	errors2 "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capibmv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	"sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/blang/semver"
	"github.com/docker/distribution"
	"github.com/docker/distribution/manifest/manifestlist"
	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap/zapcore"
)

var (
	Now   = metav1.NewTime(time.Now())
	Later = metav1.NewTime(Now.Add(5 * time.Minute))
)

const (
	ManifestListMediaType = "application/vnd.docker.distribution.manifest.list.v2+json"
	LinuxOS               = "linux"
	ArchitectureAMD64     = "amd64"
	ArchitecturePPC64LE   = "ppc64le"
)

func TestHasBeenAvailable(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	now := time.Now().Truncate(time.Second)
	reconcilerNow := metav1.Time{Time: now.Add(time.Second)}

	testCases := []struct {
		name                              string
		timestamp                         time.Time
		hcAnnotationsBeforeReconciliation map[string]string
		hcpConditions                     []metav1.Condition
		isExpectingAnnotationToBeSet      bool
	}{
		{
			name:      "When cluster just got created, annotation is not yet set",
			timestamp: now,
		},
		{
			name:      "When available condition is false, annotation is not set",
			timestamp: now.Add(5 * time.Minute),
			hcpConditions: []metav1.Condition{
				{
					Type:   string(hyperv1.HostedControlPlaneAvailable),
					Status: metav1.ConditionFalse,
				},
			},
		},
		{
			name:      "When available condition is true, annotation is set",
			timestamp: now.Add(5 * time.Minute),
			hcpConditions: []metav1.Condition{
				{
					Type:               string(hyperv1.HostedControlPlaneAvailable),
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Time{Time: now.Add(5 * time.Minute)},
				},
			},
			isExpectingAnnotationToBeSet: true,
		},
		{
			name:      "When available condition is false again, annotation is not unset if already set",
			timestamp: now.Add(10 * time.Minute),
			hcAnnotationsBeforeReconciliation: map[string]string{
				hcmetrics.HasBeenAvailableAnnotation: "true",
			},
			hcpConditions: []metav1.Condition{
				{
					Type:               string(hyperv1.HostedClusterAvailable),
					Status:             metav1.ConditionFalse,
					LastTransitionTime: metav1.Time{Time: now.Add(10 * time.Minute)},
				},
			},
			isExpectingAnnotationToBeSet: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "hc",
					Namespace:         "any",
					CreationTimestamp: metav1.Time{Time: now},
					Annotations:       tc.hcAnnotationsBeforeReconciliation,
				},
				Spec: hyperv1.HostedClusterSpec{
					ClusterID: "id",
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.1.0/24")}}, // Needed or some reconcile checks will fail
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.LoadBalancer,
							},
						},
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
			}

			hcpNs := hcpmanifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
			hcp := controlplaneoperator.HostedControlPlane(hcpNs, hcluster.Name)

			hcp.Status = hyperv1.HostedControlPlaneStatus{
				Conditions: tc.hcpConditions,
			}
			objects := []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "any"},
					Data: map[string][]byte{
						hyperv1.AESCBCKeySecretKey: {1, 2, 3, 4, 5, 6, 7, 8, 9, 0},
						".dockerconfigjson":        []byte("{}"),
					},
				},
				hcp,
				hcluster,
			}

			client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).WithStatusSubresource(hcluster).Build()
			clock := clocktesting.NewFakeClock(tc.timestamp)
			mockedProviderWithOpenShiftImageRegistryOverrides := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(mockCtrl)
			mockedProviderWithOpenShiftImageRegistryOverrides.EXPECT().
				Lookup(context.Background(), gomock.Any(), gomock.Any()).Return(testutils.InitReleaseImageOrDie("4.15.0"), nil).AnyTimes()
			r := &HostedClusterReconciler{
				Client:                        client,
				Clock:                         clock,
				CertRotationScale:             24 * time.Hour,
				createOrUpdate:                func(reconcile.Request) upsert.CreateOrUpdateFN { return ctrl.CreateOrUpdate },
				ManagementClusterCapabilities: &fakecapabilities.FakeSupportNoCapabilities{},
				ReconcileMetadataProviders: func(ctx context.Context, imgOverrides map[string]string) (releaseinfo.ProviderWithOpenShiftImageRegistryOverrides, hyperutil.ImageMetadataProvider, error) {
					return mockedProviderWithOpenShiftImageRegistryOverrides, &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
						Result:   &dockerv1client.DockerImageConfig{},
						Manifest: fakeimagemetadataprovider.FakeManifest{},
					}, nil
				},
				now: func() metav1.Time { return reconcilerNow },
			}

			ctx := context.Background()
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: crclient.ObjectKeyFromObject(hcluster)})
			if err != nil {
				t.Fatalf("error on %s reconciliation: %v", hcluster.Name, err)
			}

			if err := client.Get(ctx, crclient.ObjectKeyFromObject(hcluster), hcluster); err != nil {
				t.Fatalf("failed to get cluster after reconciliation: %v", err)
			}

			_, isAnnotationSet := hcluster.Annotations[hcmetrics.HasBeenAvailableAnnotation]

			if isAnnotationSet != tc.isExpectingAnnotationToBeSet {
				if tc.isExpectingAnnotationToBeSet {
					t.Errorf("expected annotation %s to be set, but annotation is not set", hcmetrics.HasBeenAvailableAnnotation)
				} else {
					t.Errorf("expected annotation %s not to be set, but annotation is set", hcmetrics.HasBeenAvailableAnnotation)
				}
			}
		})
	}
}

func TestReconcileHostedControlPlaneUpgrades(t *testing.T) {
	// TODO: the spec/status comparison of control plane is a weak check; the
	// conditions should give us more information about e.g. whether that
	// image ever _will_ be achieved (e.g. if the problem is fatal)
	tests := map[string]struct {
		Cluster       hyperv1.HostedCluster
		ControlPlane  hyperv1.HostedControlPlane
		ExpectedImage string
	}{
		"new controlplane has defaults matching hostedcluster": {
			Cluster: hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: Now},
				Spec: hyperv1.HostedClusterSpec{
					Etcd:    hyperv1.EtcdSpec{ManagementType: hyperv1.Managed},
					Release: hyperv1.Release{Image: "a"},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Image: "a"},
						History: []configv1.UpdateHistory{
							{Image: "a", State: configv1.PartialUpdate},
						},
					},
				},
			},
			ControlPlane: hyperv1.HostedControlPlane{
				Spec:   hyperv1.HostedControlPlaneSpec{},
				Status: hyperv1.HostedControlPlaneStatus{},
			},
			ExpectedImage: "a",
		},
		"hostedcontrolplane rollout happens after existing rollout is complete": {
			Cluster: hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: Now},
				Spec: hyperv1.HostedClusterSpec{
					Etcd:    hyperv1.EtcdSpec{ManagementType: hyperv1.Managed},
					Release: hyperv1.Release{Image: "b"},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Image: "a"},
						History: []configv1.UpdateHistory{
							{Image: "a", State: configv1.CompletedUpdate},
						},
					},
				},
			},
			ControlPlane: hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: Now},
				Spec:       hyperv1.HostedControlPlaneSpec{ReleaseImage: "a"},
				Status: hyperv1.HostedControlPlaneStatus{
					VersionStatus: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Image: "a"},
					},
				},
			},
			ExpectedImage: "b",
		},
		"hostedcontrolplane rollout happens after existing rollout is complete and desired rollout is partial": {
			Cluster: hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: Now},
				Spec: hyperv1.HostedClusterSpec{
					Etcd:    hyperv1.EtcdSpec{ManagementType: hyperv1.Managed},
					Release: hyperv1.Release{Image: "b"},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Image: "b"},
						History: []configv1.UpdateHistory{
							{Image: "b", State: configv1.PartialUpdate},
							{Image: "a", State: configv1.CompletedUpdate},
						},
					},
				},
			},
			ControlPlane: hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: Now},
				Spec:       hyperv1.HostedControlPlaneSpec{ReleaseImage: "a"},
				Status: hyperv1.HostedControlPlaneStatus{
					VersionStatus: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Image: "a"},
					},
				},
			},
			ExpectedImage: "b",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			updated := test.ControlPlane.DeepCopy()
			err := reconcileHostedControlPlane(updated, &test.Cluster, true, func() (map[string]string, error) { return nil, nil })
			if err != nil {
				t.Error(err)
			}
			actualImage := updated.Spec.ReleaseImage
			if !equality.Semantic.DeepEqual(test.ExpectedImage, actualImage) {
				t.Error(cmp.Diff(test.ExpectedImage, actualImage))
			}
		})
	}
}

func TestComputeHostedClusterAvailability(t *testing.T) {
	tests := map[string]struct {
		Cluster           hyperv1.HostedCluster
		ControlPlane      *hyperv1.HostedControlPlane
		ExpectedCondition metav1.Condition
	}{
		"missing hostedcluster should cause unavailability": {
			Cluster: hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Etcd: hyperv1.EtcdSpec{ManagementType: hyperv1.Managed},
				},
				Status: hyperv1.HostedClusterStatus{},
			},
			ControlPlane: nil,
			ExpectedCondition: metav1.Condition{
				Type:   string(hyperv1.HostedClusterAvailable),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.WaitingForAvailableReason,
			},
		},
		"hosted controlplane with availability false should cause unavailability": {
			Cluster: hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Etcd: hyperv1.EtcdSpec{ManagementType: hyperv1.Managed},
				},
				Status: hyperv1.HostedClusterStatus{},
			},
			ControlPlane: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{ReleaseImage: "a"},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.HostedControlPlaneAvailable),
							Status: metav1.ConditionFalse,
							Reason: hyperv1.KASLoadBalancerNotReachableReason,
						},
					},
				},
			},
			ExpectedCondition: metav1.Condition{
				Type:   string(hyperv1.HostedClusterAvailable),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.KASLoadBalancerNotReachableReason,
			},
		},
		"should be available": {
			Cluster: hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
				Spec: hyperv1.HostedClusterSpec{
					Etcd: hyperv1.EtcdSpec{ManagementType: hyperv1.Managed},
				},
				Status: hyperv1.HostedClusterStatus{},
			},
			ControlPlane: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{ReleaseImage: "a"},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{Type: string(hyperv1.HostedControlPlaneAvailable), Status: metav1.ConditionTrue},
					},
				},
			},
			ExpectedCondition: metav1.Condition{
				Type:   string(hyperv1.HostedClusterAvailable),
				Status: metav1.ConditionTrue,
				Reason: hyperv1.AsExpectedReason,
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			actualCondition := computeHostedClusterAvailability(&test.Cluster, test.ControlPlane)
			// Clear fields irrelevant for diffing
			actualCondition.ObservedGeneration = 0
			actualCondition.Message = ""
			if !equality.Semantic.DeepEqual(test.ExpectedCondition, actualCondition) {
				t.Error(cmp.Diff(test.ExpectedCondition, actualCondition))
			}
		})
	}
}

func TestReconcileHostedControlPlaneAPINetwork(t *testing.T) {
	tests := []struct {
		name                        string
		networking                  *hyperv1.APIServerNetworking
		expectedAPIAdvertiseAddress *string
		expectedAPIPort             *int32
	}{
		{
			name:                        "not specified",
			networking:                  nil,
			expectedAPIAdvertiseAddress: nil,
			expectedAPIPort:             nil,
		},
		{
			name: "advertise address specified",
			networking: &hyperv1.APIServerNetworking{
				AdvertiseAddress: ptr.To("1.2.3.4"),
			},
			expectedAPIAdvertiseAddress: ptr.To("1.2.3.4"),
		},
		{
			name: "port specified",
			networking: &hyperv1.APIServerNetworking{
				Port: ptr.To[int32](1234),
			},
			expectedAPIPort: ptr.To[int32](1234),
		},
		{
			name: "both specified",
			networking: &hyperv1.APIServerNetworking{
				Port:             ptr.To[int32](6789),
				AdvertiseAddress: ptr.To("9.8.7.6"),
			},
			expectedAPIPort:             ptr.To[int32](6789),
			expectedAPIAdvertiseAddress: ptr.To("9.8.7.6"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hostedCluster := &hyperv1.HostedCluster{}
			hostedCluster.Spec.Networking.APIServer = test.networking
			hostedControlPlane := &hyperv1.HostedControlPlane{}
			err := reconcileHostedControlPlane(hostedControlPlane, hostedCluster, true, func() (map[string]string, error) { return nil, nil })
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			g := NewGomegaWithT(t)
			if test.networking != nil {
				// new values should also be populated
				g.Expect(hostedControlPlane.Spec.Networking.APIServer).ToNot(BeNil())
				g.Expect(hostedControlPlane.Spec.Networking.APIServer.Port).To(Equal(test.expectedAPIPort))
				g.Expect(hostedControlPlane.Spec.Networking.APIServer.AdvertiseAddress).To(Equal(test.expectedAPIAdvertiseAddress))
			} else {
				g.Expect(hostedControlPlane.Spec.Networking.APIServer).To(BeNil())
			}
		})
	}
}

func TestReconcileHostedControlPlaneConfiguration(t *testing.T) {
	idp := configv1.IdentityProvider{
		Name: "htpasswd",
		IdentityProviderConfig: configv1.IdentityProviderConfig{
			Type: configv1.IdentityProviderTypeHTPasswd,
		},
	}

	tests := []struct {
		name          string
		configuration *hyperv1.ClusterConfiguration
	}{
		{
			name:          "not specified",
			configuration: nil,
		},
		{
			name: "cluster configuration specified",
			configuration: &hyperv1.ClusterConfiguration{
				OAuth: &configv1.OAuthSpec{
					IdentityProviders: []configv1.IdentityProvider{
						idp,
					},
				},
				Ingress: &configv1.IngressSpec{
					Domain: "test.domain.com",
				},
				Network: &configv1.NetworkSpec{
					NetworkType: "OpenShiftSDN",
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hostedCluster := &hyperv1.HostedCluster{}
			hostedCluster.Spec.Configuration = test.configuration
			hostedControlPlane := &hyperv1.HostedControlPlane{}
			g := NewGomegaWithT(t)

			err := reconcileHostedControlPlane(hostedControlPlane, hostedCluster, true, func() (map[string]string, error) { return nil, nil })
			g.Expect(err).ToNot(HaveOccurred())

			// DeepEqual to check that all ClusterConfiguration fields are deep copied to HostedControlPlane
			g.Expect(hostedControlPlane.Spec.Configuration).To(BeEquivalentTo(test.configuration))
		})
	}
}

func TestReconcileHostedControlPlaneAnnotations(t *testing.T) {
	type testCase struct {
		name                   string
		hcpAnnotations         map[string]string
		hcAnnotations          map[string]string
		isAutoscalingNeeded    bool
		certRenewalAnnotations map[string]string
		expectedAnnotations    map[string]string
	}

	hcNamespace := "clusters"
	hcName := "example"
	hcKey := hcNamespace + "/" + hcName

	tests := []testCase{
		{
			name: "Newly set restart annotation",
			hcAnnotations: map[string]string{
				hyperv1.RestartDateAnnotation: "01012024",
			},
			expectedAnnotations: map[string]string{
				hyperv1.RestartDateAnnotation:              "01012024",
				previouslySyncedRestartDateAnnotation:      "01012024",
				hyperutil.HostedClusterAnnotation:          hcKey,
				hyperv1.DisableClusterAutoscalerAnnotation: "true",
			},
		},
		{
			name: "Existing restart annotation (different value)",
			hcAnnotations: map[string]string{
				hyperv1.RestartDateAnnotation: "05012024",
			},
			hcpAnnotations: map[string]string{
				hyperv1.RestartDateAnnotation:         "01012024",
				previouslySyncedRestartDateAnnotation: "01012024",
			},
			expectedAnnotations: map[string]string{
				hyperv1.RestartDateAnnotation:              "05012024",
				previouslySyncedRestartDateAnnotation:      "05012024",
				hyperutil.HostedClusterAnnotation:          hcKey,
				hyperv1.DisableClusterAutoscalerAnnotation: "true",
			},
		},
		{
			name: "Previously applied restart annotation, different actual value",
			hcAnnotations: map[string]string{
				hyperv1.RestartDateAnnotation: "01012024",
			},
			hcpAnnotations: map[string]string{
				hyperv1.RestartDateAnnotation:         "some other value",
				previouslySyncedRestartDateAnnotation: "01012024",
			},
			expectedAnnotations: map[string]string{
				hyperv1.RestartDateAnnotation:              "some other value",
				previouslySyncedRestartDateAnnotation:      "01012024",
				hyperutil.HostedClusterAnnotation:          hcKey,
				hyperv1.DisableClusterAutoscalerAnnotation: "true",
			},
		},
		{
			name: "Previously applied restart annotation, new value",
			hcAnnotations: map[string]string{
				hyperv1.RestartDateAnnotation: "05012024",
			},
			hcpAnnotations: map[string]string{
				hyperv1.RestartDateAnnotation:         "some other value",
				previouslySyncedRestartDateAnnotation: "01012024",
			},
			expectedAnnotations: map[string]string{
				hyperv1.RestartDateAnnotation:              "05012024",
				previouslySyncedRestartDateAnnotation:      "05012024",
				hyperutil.HostedClusterAnnotation:          hcKey,
				hyperv1.DisableClusterAutoscalerAnnotation: "true",
			},
		},
		{
			name: "Initial reconcile",
			hcAnnotations: map[string]string{
				hyperutil.DebugDeploymentsAnnotation:                         "control-plane-operator",
				hyperv1.EtcdPriorityClass:                                    "high-priority",
				hyperv1.RequestServingNodeAdditionalSelectorAnnotation:       "node-size=m5xl",
				hyperv1.IdentityProviderOverridesAnnotationPrefix + "-test1": "test1",
				hyperv1.IdentityProviderOverridesAnnotationPrefix + "-test2": "test2",
				hyperv1.KubeAPIServerGoAwayChance:                            "0.001",
				"foo":                                                        "bar", // should not be copied
			},
			expectedAnnotations: map[string]string{
				hyperutil.DebugDeploymentsAnnotation:                         "control-plane-operator",
				hyperv1.EtcdPriorityClass:                                    "high-priority",
				hyperv1.IdentityProviderOverridesAnnotationPrefix + "-test1": "test1",
				hyperv1.IdentityProviderOverridesAnnotationPrefix + "-test2": "test2",
				hyperv1.KubeAPIServerGoAwayChance:                            "0.001",
				hyperv1.RequestServingNodeAdditionalSelectorAnnotation:       "node-size=m5xl",
				hyperutil.HostedClusterAnnotation:                            hcKey,
				hyperv1.DisableClusterAutoscalerAnnotation:                   "true",
			},
		},
		{
			name: "Initial reconcile - autoscaling needed",
			hcAnnotations: map[string]string{
				hyperutil.DebugDeploymentsAnnotation:                         "control-plane-operator",
				hyperv1.EtcdPriorityClass:                                    "high-priority",
				hyperv1.RequestServingNodeAdditionalSelectorAnnotation:       "node-size=m5xl",
				hyperv1.IdentityProviderOverridesAnnotationPrefix + "-test1": "test1",
				hyperv1.IdentityProviderOverridesAnnotationPrefix + "-test2": "test2",
				"foo": "bar",
			},
			expectedAnnotations: map[string]string{
				hyperutil.DebugDeploymentsAnnotation:                         "control-plane-operator",
				hyperv1.EtcdPriorityClass:                                    "high-priority",
				hyperv1.IdentityProviderOverridesAnnotationPrefix + "-test1": "test1",
				hyperv1.IdentityProviderOverridesAnnotationPrefix + "-test2": "test2",
				hyperv1.RequestServingNodeAdditionalSelectorAnnotation:       "node-size=m5xl",
				hyperutil.HostedClusterAnnotation:                            hcKey,
			},
			isAutoscalingNeeded: true,
		},
		{
			name: "Existing disable autoscaling annotation, autoscaling no longer needed",
			hcAnnotations: map[string]string{
				hyperutil.DebugDeploymentsAnnotation: "control-plane-operator",
			},
			hcpAnnotations: map[string]string{
				hyperv1.DisableClusterAutoscalerAnnotation: "true",
			},
			expectedAnnotations: map[string]string{
				hyperutil.DebugDeploymentsAnnotation: "control-plane-operator",
				hyperutil.HostedClusterAnnotation:    hcKey,
			},
			isAutoscalingNeeded: true,
		},
		{
			name: "Remove known annotations that are no longer set",
			hcAnnotations: map[string]string{
				hyperv1.EtcdPriorityClass:                                      "high-priority",
				hyperv1.RequestServingNodeAdditionalSelectorAnnotation:         "node-size=m5xl",
				hyperv1.IdentityProviderOverridesAnnotationPrefix + "-test1":   "test1",
				hyperv1.IdentityProviderOverridesAnnotationPrefix + "-test2":   "test2",
				hyperv1.ResourceRequestOverrideAnnotationPrefix + "-override1": "override1",
				hyperv1.ResourceRequestOverrideAnnotationPrefix + "-override2": "override2",
				"foo": "bar",
			},
			hcpAnnotations: map[string]string{
				hyperutil.DebugDeploymentsAnnotation:                           "control-plane-operator",
				hyperv1.IdentityProviderOverridesAnnotationPrefix + "-test1":   "test1",
				hyperv1.IdentityProviderOverridesAnnotationPrefix + "-test3":   "test3",
				hyperv1.ResourceRequestOverrideAnnotationPrefix + "-override4": "override4",
				hyperv1.ResourceRequestOverrideAnnotationPrefix + "-override2": "override2",
				"unrelated": "test", // should remain
			},
			expectedAnnotations: map[string]string{
				hyperv1.EtcdPriorityClass:                                      "high-priority",
				hyperv1.RequestServingNodeAdditionalSelectorAnnotation:         "node-size=m5xl",
				hyperv1.IdentityProviderOverridesAnnotationPrefix + "-test1":   "test1",
				hyperv1.IdentityProviderOverridesAnnotationPrefix + "-test2":   "test2",
				hyperv1.ResourceRequestOverrideAnnotationPrefix + "-override1": "override1",
				hyperv1.ResourceRequestOverrideAnnotationPrefix + "-override2": "override2",
				hyperutil.HostedClusterAnnotation:                              hcKey,
				hyperv1.DisableClusterAutoscalerAnnotation:                     "true",
				"unrelated": "test",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			hc := &hyperv1.HostedCluster{}
			hc.Name = hcName
			hc.Namespace = hcNamespace
			hcp := &hyperv1.HostedControlPlane{}
			hcp.Annotations = tc.hcpAnnotations
			hc.Annotations = tc.hcAnnotations
			err := reconcileHostedControlPlaneAnnotations(hcp, hc, tc.isAutoscalingNeeded, func() (map[string]string, error) { return tc.certRenewalAnnotations, nil })
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(hcp.Annotations).To(Equal(tc.expectedAnnotations))
		})
	}
}

func TestAnnotationsForCertRenewal(t *testing.T) {
	tests := []struct {
		name             string
		shouldSkip       bool
		hcpAnnotations   map[string]string
		hashFromSecret   string
		hashFromEndpoint string
		expected         map[string]string
	}{
		{
			name:             "should not check",
			shouldSkip:       true,
			hashFromSecret:   "12345",
			hashFromEndpoint: "67890",
			expected:         nil,
		},
		{
			name:             "no existing hash annotation on hcp, endpoint hash matches",
			hashFromSecret:   "12345",
			hashFromEndpoint: "12345",
			expected: map[string]string{
				kasServingCertHashAnnotation: "12345",
			},
		},
		{
			name:             "no existing hash annotation on hcp, endpoint hash does not match",
			hashFromSecret:   "12345",
			hashFromEndpoint: "67890",
			expected: map[string]string{
				kasServingCertHashAnnotation:  "12345",
				hyperv1.RestartDateAnnotation: "CertHash:12345",
			},
		},
		{
			name:           "existing hash annotation, secret hash matches",
			hashFromSecret: "12345",
			hcpAnnotations: map[string]string{
				kasServingCertHashAnnotation: "12345",
			},
			expected: nil,
		},
		{
			name:           "existing hash annotation, secret hash does not match",
			hashFromSecret: "67890",
			hcpAnnotations: map[string]string{
				kasServingCertHashAnnotation: "12345",
			},
			expected: map[string]string{
				kasServingCertHashAnnotation:  "67890",
				hyperv1.RestartDateAnnotation: "CertHash:67890",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			hcp := &hyperv1.HostedControlPlane{}
			hcp.Annotations = test.hcpAnnotations
			fn := annotationsForCertRenewal(
				ctrl.Log,
				hcp,
				func() bool { return !test.shouldSkip },
				func() (string, error) { return test.hashFromSecret, nil },
				func() (string, error) { return test.hashFromEndpoint, nil },
			)
			result, err := fn()
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(test.expected))
		})
	}
}

func TestShouldCheckForStaleCerts(t *testing.T) {
	tests := []struct {
		name           string
		hcAnnotations  map[string]string
		hcVersion      string
		expectedResult bool
	}{
		{
			name: "version older than 4.19",
			hcAnnotations: map[string]string{
				hcmetrics.HasBeenAvailableAnnotation: "true",
			},
			hcVersion:      "4.18.7",
			expectedResult: true,
		},
		{
			name: "ci version with 4.19",
			hcAnnotations: map[string]string{
				hcmetrics.HasBeenAvailableAnnotation: "true",
			},
			hcVersion:      "4.19.0-0.ci-2025-02-03-120046",
			expectedResult: false,
		},
		{
			name: "4.20",
			hcAnnotations: map[string]string{
				hcmetrics.HasBeenAvailableAnnotation: "true",
			},
			hcVersion:      "4.20.0",
			expectedResult: false,
		},
		{
			name:           "version older than 4.19, never been available",
			hcAnnotations:  nil,
			hcVersion:      "4.17.4",
			expectedResult: false,
		},
		{
			name: "version older than 4.19, has been available, does not reconcile pki",
			hcAnnotations: map[string]string{
				hcmetrics.HasBeenAvailableAnnotation:       "true",
				hyperv1.DisablePKIReconciliationAnnotation: "true",
			},
			hcVersion:      "4.16.20",
			expectedResult: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			hc := &hyperv1.HostedCluster{}
			hc.Annotations = test.hcAnnotations
			fn := shouldCheckForStaleCerts(hc, semver.MustParse(test.hcVersion))
			result := fn()
			g.Expect(result).To(Equal(test.expectedResult))
		})
	}
}

func TestServiceFirstNodePortAvailable(t *testing.T) {
	tests := []struct {
		name              string
		inputService      *corev1.Service
		expectedAvailable bool
	}{
		{
			name:              "not specified",
			inputService:      nil,
			expectedAvailable: false,
		},
		{
			name: "node port not available",
			inputService: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-service",
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "metrics",
							Protocol:   corev1.ProtocolTCP,
							Port:       9393,
							TargetPort: intstr.FromString("metrics"),
						},
					},
				},
			},
			expectedAvailable: false,
		},
		{
			name: "node port available",
			inputService: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-service",
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "metrics",
							Protocol:   corev1.ProtocolTCP,
							Port:       9393,
							TargetPort: intstr.FromString("metrics"),
							NodePort:   30000,
						},
					},
				},
			},
			expectedAvailable: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			isAvailable := serviceFirstNodePortAvailable(test.inputService)
			g := NewGomegaWithT(t)
			g.Expect(isAvailable).To(Equal(test.expectedAvailable))
		})
	}
}

func TestServicePublishingStrategyByType(t *testing.T) {
	tests := []struct {
		name                              string
		inputHostedCluster                *hyperv1.HostedCluster
		inputServiceType                  hyperv1.ServiceType
		expectedServicePublishingStrategy *hyperv1.ServicePublishingStrategyMapping
	}{
		{
			name: "ignition node port",
			inputHostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.NodePort,
							},
						},
					},
				},
			},
			inputServiceType: hyperv1.Ignition,
			expectedServicePublishingStrategy: &hyperv1.ServicePublishingStrategyMapping{
				Service: hyperv1.Ignition,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					Type: hyperv1.NodePort,
				},
			},
		},
		{
			name: "not found",
			inputHostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.NodePort,
							},
						},
					},
				},
			},
			inputServiceType:                  hyperv1.Konnectivity,
			expectedServicePublishingStrategy: nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			servicePubStrategy := servicePublishingStrategyByType(test.inputHostedCluster, test.inputServiceType)
			g := NewGomegaWithT(t)
			if test.expectedServicePublishingStrategy == nil {
				g.Expect(servicePubStrategy).To(BeNil())
			} else {
				g.Expect(test.inputServiceType).To(Equal(test.expectedServicePublishingStrategy.Service))
				g.Expect(servicePubStrategy.Type).To(Equal(test.expectedServicePublishingStrategy.Type))
			}
		})
	}
}

func TestReconcileCAPICluster(t *testing.T) {
	testCases := []struct {
		name               string
		capiCluster        *v1beta1.Cluster
		hostedCluster      *hyperv1.HostedCluster
		hostedControlPlane *hyperv1.HostedControlPlane
		infraCR            crclient.Object

		expectedCAPICluster *v1beta1.Cluster
	}{
		{
			name:        "IBM Cloud cluster",
			capiCluster: controlplaneoperator.CAPICluster("master-cluster1", "cluster1"),
			hostedCluster: &hyperv1.HostedCluster{
				TypeMeta: metav1.TypeMeta{
					Kind:       "HostedCluster",
					APIVersion: hyperv1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "master",
					Name:      "cluster1",
				},
			},
			hostedControlPlane: &hyperv1.HostedControlPlane{
				TypeMeta: metav1.TypeMeta{
					Kind:       "HostedControlPlane",
					APIVersion: hyperv1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "master-cluster1",
					Name:      "cluster1",
				},
			},
			infraCR: &capibmv1.IBMVPCCluster{
				TypeMeta: metav1.TypeMeta{
					Kind:       "IBMVPCCluster",
					APIVersion: capibmv1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster1",
					Namespace: "master-cluster1",
				},
			},
			expectedCAPICluster: &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperutil.HostedClusterAnnotation: "master/cluster1",
					},
					Namespace: "master-cluster1",
					Name:      "cluster1",
				},
				Spec: v1beta1.ClusterSpec{
					ControlPlaneEndpoint: v1beta1.APIEndpoint{},
					ControlPlaneRef: &corev1.ObjectReference{
						APIVersion: "hypershift.openshift.io/v1beta1",
						Kind:       "HostedControlPlane",
						Namespace:  "master-cluster1",
						Name:       "cluster1",
					},
					InfrastructureRef: &corev1.ObjectReference{
						APIVersion: capibmv1.GroupVersion.String(),
						Kind:       "IBMVPCCluster",
						Namespace:  "master-cluster1",
						Name:       "cluster1",
					},
				},
			},
		},
		{
			name:        "AWS cluster",
			capiCluster: controlplaneoperator.CAPICluster("master-cluster1", "cluster1"),
			hostedCluster: &hyperv1.HostedCluster{
				TypeMeta: metav1.TypeMeta{
					Kind:       "HostedCluster",
					APIVersion: hyperv1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "master",
					Name:      "cluster1",
				},
			},
			hostedControlPlane: &hyperv1.HostedControlPlane{
				TypeMeta: metav1.TypeMeta{
					Kind:       "HostedControlPlane",
					APIVersion: hyperv1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "master-cluster1",
					Name:      "cluster1",
				},
			},
			infraCR: &capiaws.AWSCluster{
				TypeMeta: metav1.TypeMeta{
					Kind:       "AWSCluster",
					APIVersion: capiaws.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster1",
					Namespace: "master-cluster1",
				},
			},
			expectedCAPICluster: &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperutil.HostedClusterAnnotation: "master/cluster1",
					},
					Namespace: "master-cluster1",
					Name:      "cluster1",
				},
				Spec: v1beta1.ClusterSpec{
					ControlPlaneEndpoint: v1beta1.APIEndpoint{},
					ControlPlaneRef: &corev1.ObjectReference{
						APIVersion: "hypershift.openshift.io/v1beta1",
						Kind:       "HostedControlPlane",
						Namespace:  "master-cluster1",
						Name:       "cluster1",
					},
					InfrastructureRef: &corev1.ObjectReference{
						APIVersion: capiaws.GroupVersion.String(),
						Kind:       "AWSCluster",
						Namespace:  "master-cluster1",
						Name:       "cluster1",
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := reconcileCAPICluster(tc.capiCluster, tc.hostedCluster, tc.hostedControlPlane, tc.infraCR); err != nil {
				t.Fatalf("reconcileCAPICluster failed: %v", err)
			}
			if diff := cmp.Diff(tc.capiCluster, tc.expectedCAPICluster); diff != "" {
				t.Errorf("reconciled CAPI cluster differs from expected CAPI cluster: %s", diff)
			}
		})
	}
}

func TestReconcileAWSResourceTags(t *testing.T) {
	testCases := []struct {
		name     string
		in       hyperv1.HostedClusterSpec
		expected hyperv1.HostedClusterSpec
	}{
		{
			name: "Not an aws cluster, no change",
		},
		{
			name: "Tag is added",
			in: hyperv1.HostedClusterSpec{
				InfraID: "123",
				Platform: hyperv1.PlatformSpec{
					AWS: &hyperv1.AWSPlatformSpec{},
				},
			},
			expected: hyperv1.HostedClusterSpec{
				InfraID: "123",
				Platform: hyperv1.PlatformSpec{
					AWS: &hyperv1.AWSPlatformSpec{
						ResourceTags: []hyperv1.AWSResourceTag{{
							Key:   "kubernetes.io/cluster/123",
							Value: "owned",
						}},
					},
				},
			},
		},
		{
			name: "Tag already exists, nothing to do",
			in: hyperv1.HostedClusterSpec{
				InfraID: "123",
				Platform: hyperv1.PlatformSpec{
					AWS: &hyperv1.AWSPlatformSpec{
						ResourceTags: []hyperv1.AWSResourceTag{{
							Key:   "kubernetes.io/cluster/123",
							Value: "owned",
						}},
					},
				},
			},
			expected: hyperv1.HostedClusterSpec{
				InfraID: "123",
				Platform: hyperv1.PlatformSpec{
					AWS: &hyperv1.AWSPlatformSpec{
						ResourceTags: []hyperv1.AWSResourceTag{{
							Key:   "kubernetes.io/cluster/123",
							Value: "owned",
						}},
					},
				},
			},
		},
		{
			name: "Tag already exists with wrong value",
			in: hyperv1.HostedClusterSpec{
				InfraID: "123",
				Platform: hyperv1.PlatformSpec{
					AWS: &hyperv1.AWSPlatformSpec{
						ResourceTags: []hyperv1.AWSResourceTag{{
							Key:   "kubernetes.io/cluster/123",
							Value: "borked",
						}},
					},
				},
			},
			expected: hyperv1.HostedClusterSpec{
				InfraID: "123",
				Platform: hyperv1.PlatformSpec{
					AWS: &hyperv1.AWSPlatformSpec{
						ResourceTags: []hyperv1.AWSResourceTag{{
							Key:   "kubernetes.io/cluster/123",
							Value: "owned",
						}},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "123",
				},
				Spec: tc.in,
			}

			client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(cluster).Build()
			r := &HostedClusterReconciler{
				Client:            client,
				CertRotationScale: 24 * time.Hour,
			}

			if err := r.reconcileAWSResourceTags(context.Background(), cluster); err != nil {
				t.Fatalf("reconcileAWSResourceTags failed: %v", err)
			}

			reconciledCluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "123"}}
			if err := client.Get(context.Background(), crclient.ObjectKeyFromObject(reconciledCluster), reconciledCluster); err != nil {
				t.Fatalf("failed to get cluster after reconcilding it: %v", err)
			}

			if diff := cmp.Diff(tc.expected, reconciledCluster.Spec); diff != "" {
				t.Errorf("expected clusterspec differs from actual: %s", diff)
			}
		})
	}
}

func TestReconcileCAPIProviderRole(t *testing.T) {
	p := kubevirt.Kubevirt{}
	role := &rbacv1.Role{}
	if err := reconcileCAPIProviderRole(role, p); err != nil {
		t.Fatalf("reconcileCAPIProviderRole failed: %v", err)
	}
	if diff := cmp.Diff(expectedRules(p.CAPIProviderPolicyRules()), role.Rules); diff != "" {
		t.Errorf("expected rules differs from actual: %s", diff)
	}
}

func expectedRules(addRules []rbacv1.PolicyRule) []rbacv1.PolicyRule {
	baseRules := []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{
				"events",
				"secrets",
				"configmaps",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{
				"bootstrap.cluster.x-k8s.io",
				"controlplane.cluster.x-k8s.io",
				"infrastructure.cluster.x-k8s.io",
				"machines.cluster.x-k8s.io",
				"exp.infrastructure.cluster.x-k8s.io",
				"addons.cluster.x-k8s.io",
				"exp.cluster.x-k8s.io",
				"cluster.x-k8s.io",
			},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"hypershift.openshift.io"},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"coordination.k8s.io"},
			Resources: []string{
				"leases",
			},
			Verbs: []string{"*"},
		},
	}
	return append(baseRules, addRules...)
}

func TestHostedClusterWatchesEverythingItCreates(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	releaseImage, _ := version.LookupDefaultOCPVersion("")
	manifests := []manifestlist.ManifestDescriptor{
		{
			Descriptor: distribution.Descriptor{
				MediaType: ManifestListMediaType,
				Digest:    "sha256:70fb4524d21e1b6c08477eb5d1ca2cf282b3270b1d008f70dd7e1cf13d8ba4ce",
			},
			Platform: manifestlist.PlatformSpec{
				Architecture: ArchitectureAMD64,
				OS:           LinuxOS,
			},
		},
		{
			Descriptor: distribution.Descriptor{
				MediaType: ManifestListMediaType,
				Digest:    "sha256:70fb4524d21e1b6c08477eb5d1ca2cf282b3270b1d008f70dd7e1cf13d8ba4ce",
			},
			Platform: manifestlist.PlatformSpec{
				Architecture: ArchitecturePPC64LE,
				OS:           LinuxOS,
			},
		},
	}
	deserializeFunc := func(payload []byte) (*manifestlist.DeserializedManifestList, error) {
		return &manifestlist.DeserializedManifestList{
			ManifestList: manifestlist.ManifestList{
				Manifests: manifests,
			},
		}, nil
	}
	hostedClusters := []*hyperv1.HostedCluster{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "agent",
				Namespace: "test",
			},
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{
					Type:  hyperv1.AgentPlatform,
					Agent: &hyperv1.AgentPlatformSpec{AgentNamespace: "agent-namespace"},
				},
				Release: hyperv1.Release{
					Image: releaseImage.PullSpec,
				},
			},
			Status: hyperv1.HostedClusterStatus{
				IgnitionEndpoint: "ign",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "aws",
				Namespace: "test",
			},
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AWSPlatform,
					AWS: &hyperv1.AWSPlatformSpec{
						EndpointAccess: hyperv1.Public,
						RolesRef: hyperv1.AWSRolesRef{
							IngressARN:              "ingress-arn",
							ImageRegistryARN:        "image-registry-arn",
							StorageARN:              "storage-arn",
							NetworkARN:              "network-arn",
							KubeCloudControllerARN:  " kube-cloud-controller-arn",
							NodePoolManagementARN:   "node-pool-management-arn",
							ControlPlaneOperatorARN: "control-plane-operator-arn",
						},
						Region: "us-east-1",
					},
				},
				Release: hyperv1.Release{
					Image: releaseImage.PullSpec,
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "none",
				Namespace: "test",
			},
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.NonePlatform,
				},
				Release: hyperv1.Release{
					Image: releaseImage.PullSpec,
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ibm",
				Namespace: "test",
			},
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{
					Type:     hyperv1.IBMCloudPlatform,
					IBMCloud: &hyperv1.IBMCloudPlatformSpec{},
				},
				Release: hyperv1.Release{
					Image: releaseImage.PullSpec,
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kubevirt",
				Namespace: "test",
				Annotations: map[string]string{
					hyperv1.AllowUnsupportedKubeVirtRHCOSVariantsAnnotation: "true",
				},
			},
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.KubevirtPlatform,
					Kubevirt: &hyperv1.KubevirtPlatformSpec{
						GenerateID: "123456789",
						Credentials: &hyperv1.KubevirtPlatformCredentials{
							InfraNamespace: "kubevirt-kubevirt",
							InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{
								Name: "secret",
								Key:  "key",
							},
						},
					},
				},
				SecretEncryption: &hyperv1.SecretEncryptionSpec{
					Type: hyperv1.AESCBC,
					AESCBC: &hyperv1.AESCBCSpec{
						ActiveKey: corev1.LocalObjectReference{
							Name: "kubevirt" + etcdEncKeyPostfix,
						},
					},
				},
				Release: hyperv1.Release{
					Image: releaseImage.PullSpec,
				},
			},
		},
	}

	objects := []crclient.Object{
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "secret",
				Namespace: "test",
			},
			Data: map[string][]byte{
				"credentials":       []byte("creds"),
				".dockerconfigjson": []byte("{}"),
			},
		},
		&configv1.Network{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cluster",
			},
			Spec: configv1.NetworkSpec{
				NetworkType: "OVNKubernetes",
			},
		},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "agent-namespace"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "agent"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aws"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "none"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ibm"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kubevirt"}},
		&corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Name: "kubernetes", Namespace: "default"}},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "kubevirt" + etcdEncKeyPostfix, Namespace: "test"},
			Data: map[string][]byte{
				hyperv1.AESCBCKeySecretKey: {
					0, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
					17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31,
				},
			},
		},
		&configv1.Infrastructure{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cluster",
			},
			Status: configv1.InfrastructureStatus{
				PlatformStatus: &configv1.PlatformStatus{
					Type: configv1.AWSPlatformType,
				},
			},
		},
	}
	for _, cluster := range hostedClusters {
		cluster.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
			{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
			{Service: hyperv1.Konnectivity, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}},
			{Service: hyperv1.OAuthServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}},
			{Service: hyperv1.Ignition, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}},
		}
		cluster.Spec.PullSecret = corev1.LocalObjectReference{Name: "secret"}
		cluster.Spec.InfraID = "infra-id"
		cluster.Spec.Networking.ClusterNetwork = []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.1.0/24")}}
		cluster.Spec.Networking.MachineNetwork = []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}}
		cluster.Spec.Networking.ServiceNetwork = []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.0.0/24")}}
		objects = append(objects, cluster)
	}

	mockedProviderWithOpenShiftImageRegistryOverrides := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(mockCtrl)
	mockedProviderWithOpenShiftImageRegistryOverrides.EXPECT().
		Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(testutils.InitReleaseImageOrDie("4.15.0"), nil).AnyTimes()
	mockedProviderWithOpenShiftImageRegistryOverrides.EXPECT().
		GetOpenShiftImageRegistryOverrides().
		Return(nil).AnyTimes()
	mockedProviderWithOpenShiftImageRegistryOverrides.EXPECT().
		GetRegistryOverrides().
		Return(nil).AnyTimes()
	client := &createTypeTrackingClient{Client: fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).WithStatusSubresource(&hyperv1.HostedCluster{}).Build()}
	r := &HostedClusterReconciler{
		Client:            client,
		Clock:             clock.RealClock{},
		CertRotationScale: 24 * time.Hour,
		ManagementClusterCapabilities: fakecapabilities.NewSupportAllExcept(
			capabilities.CapabilityInfrastructure,
			capabilities.CapabilityIngress,
			capabilities.CapabilityProxy,
		),
		createOrUpdate: func(reconcile.Request) upsert.CreateOrUpdateFN { return ctrl.CreateOrUpdate },
		ReconcileMetadataProviders: func(ctx context.Context, imgOverrides map[string]string) (releaseinfo.ProviderWithOpenShiftImageRegistryOverrides, hyperutil.ImageMetadataProvider, error) {
			return mockedProviderWithOpenShiftImageRegistryOverrides,
				&fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
					MediaType: ManifestListMediaType,
					Result:    &dockerv1client.DockerImageConfig{},
					Manifest:  fakeimagemetadataprovider.FakeManifest{},
				}, nil
		},
		EnableEtcdRecovery: true,
		now:                metav1.Now,
	}

	r.KubevirtInfraClients = kvinfra.NewMockKubevirtInfraClientMap(&createTypeTrackingClient{Client: fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build()},
		"v1.2.0",
		"1.28.0")

	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))

	for _, hc := range hostedClusters {
		t.Run(hc.Name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), registryclient.DeserializeFuncName, deserializeFunc)
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: hc.Namespace, Name: hc.Name}})
			if err != nil {
				t.Fatalf("Reconcile failed: %v", err)
			}
		})
	}
	watchedResources := sets.New[string]()
	for _, resource := range r.managedResources() {
		resourceType := fmt.Sprintf("%T", resource)
		switch resourceType {
		case "*v1.Endpoints", "*v1.Job", "*v1.StatefulSet", "*v1beta1.NodePool", "*v1beta1.AWSEndpointService":
			// We watch Endpoints for changes to the kubernetes Endpoint in the default namespace
			// but never create an Endpoints resource

			// We only create a Job when etcd recovery is needed

			// We don't create a StatefulSet but we watch them for etcd health check and recovery

			// We watch NodePools but don't create them

			// We watch AWSEndpointServices to propagate conditions to the HostedCluster
			continue
		}
		watchedResources.Insert(resourceType)
	}
	watchedResourcesSlice := watchedResources.UnsortedList()
	sort.Strings(watchedResourcesSlice)
	clientCreatedTypesSlice := client.createdTypes.UnsortedList()
	sort.Strings(clientCreatedTypesSlice)
	if diff := cmp.Diff(clientCreatedTypesSlice, watchedResourcesSlice); diff != "" {
		t.Errorf("the set of resources that are being created differs from the one that is being watched: %s", diff)
	}
}

func TestReconcileCLISecrets(t *testing.T) {
	const (
		infraID = "infraId"
		ns      = "myns"
	)

	labels := map[string]string{
		util.DeleteWithClusterLabelName: "true",
		util.AutoInfraLabelName:         infraID,
	}
	testCase := []struct {
		name            string
		secrets         []crclient.Object
		expectedWithRef int
	}{
		{
			name: "secret with both labels and with no ownerRef",
			secrets: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "secret1",
						Namespace: ns,
						Labels:    labels,
					},
				},
			},
			expectedWithRef: 1,
		},
		{
			name: "multiple secret with both labels and with no ownerRef",
			secrets: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "secret1",
						Namespace: ns,
						Labels:    labels,
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "secret2",
						Namespace: ns,
						Labels:    labels,
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "secret3",
						Namespace: ns,
						Labels:    labels,
					},
				},
			},
			expectedWithRef: 3,
		},
		{
			name: "mix cases",
			secrets: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "valid-1",
						Namespace: ns,
						Labels:    labels,
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "other-namespace",
						Namespace: "other",
						Labels:    labels,
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "valid-2",
						Namespace: ns,
						Labels:    labels,
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "no-labels",
						Namespace: ns,
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "only-auto-created-for-infra-label",
						Namespace: ns,
						Labels: map[string]string{
							util.AutoInfraLabelName: infraID,
						},
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "only-safe-to-delete-with-cluster-label",
						Namespace: ns,
						Labels: map[string]string{
							util.DeleteWithClusterLabelName: "true",
						},
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "other-infra-id",
						Namespace: ns,
						Labels: map[string]string{
							util.AutoInfraLabelName:         "other",
							util.DeleteWithClusterLabelName: "true",
						},
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "valid-3",
						Namespace: ns,
						Labels:    labels,
					},
				},
			},
			expectedWithRef: 3,
		},
	}

	for _, tc := range testCase {
		hc := &hyperv1.HostedCluster{
			TypeMeta: metav1.TypeMeta{
				APIVersion: hyperv1.GroupVersion.Version,
				Kind:       "HostedCluster",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-cluster",
				Namespace: ns,
				UID:       types.UID("my-cluster-uid"),
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: hyperv1.GroupVersion.Version,
						Kind:       "HostedCluster",
						Name:       "hc1",
						UID:        types.UID("hclusterUID"),
					},
				},
			},
			Spec: hyperv1.HostedClusterSpec{
				InfraID: infraID,
			},
		}

		reference := *(config.OwnerRefFrom(hc).Reference)
		createOrUpdate := upsert.New(false).CreateOrUpdate
		t.Run(tc.name, func(tt *testing.T) {
			cli := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.secrets...).Build()
			r := &HostedClusterReconciler{Client: cli}
			ctx := context.Background()
			err := r.reconcileCLISecrets(ctx, createOrUpdate, hc)
			if err != nil {
				tt.Fatalf("should not return error but returned %q", err.Error())
			}

			count := 0
			secrets := &corev1.SecretList{}
			err = cli.List(ctx, secrets) // reading secrets with no filter, because the function under test uses filters. checking manually below
			if err != nil {
				tt.Fatalf("failed to read secrets: %s", err.Error())
			}

			for _, secret := range secrets.Items {
				found := len(secret.OwnerReferences) == 1 && reflect.DeepEqual(secret.OwnerReferences[0], reference)
				if found {
					count++
				} else {
					shouldNotBeFound := false
					if secret.Namespace != ns {
						shouldNotBeFound = true
					} else if v, ok := secret.Labels[util.DeleteWithClusterLabelName]; !ok || v != "true" {
						shouldNotBeFound = true
					} else if v, ok = secret.Labels[util.AutoInfraLabelName]; !ok || v != infraID {
						shouldNotBeFound = true
					}

					if !shouldNotBeFound {
						tt.Errorf("owner reference wasn't found in secret. secret name: %s", secret.Name)
					}
				}
			}

			if count != tc.expectedWithRef {
				tt.Errorf("wrong number of affected secrets. Extcted %d but found %d", tc.expectedWithRef, count)
			}
		})
	}
}

type createTypeTrackingClient struct {
	crclient.Client
	createdTypes sets.Set[string]
}

func (c *createTypeTrackingClient) Create(ctx context.Context, obj crclient.Object, opts ...crclient.CreateOption) error {
	if c.createdTypes == nil {
		c.createdTypes = sets.New[string]()
	}
	c.createdTypes.Insert(fmt.Sprintf("%T", obj))
	return c.Client.Create(ctx, obj, opts...)
}

func TestValidateConfigAndClusterCapabilities(t *testing.T) {

	// For network test below.
	clusterNet := make([]hyperv1.ClusterNetworkEntry, 2)
	cidr, _ := ipnet.ParseCIDR("192.168.1.0/24")
	clusterNet[0].CIDR = *cidr
	machineNet := make([]hyperv1.MachineNetworkEntry, 2)
	cidr, _ = ipnet.ParseCIDR("172.16.0.0/24")
	machineNet[0].CIDR = *cidr
	cidr, _ = ipnet.ParseCIDR("172.16.1.0/24")
	machineNet[1].CIDR = *cidr
	serviceNet := make([]hyperv1.ServiceNetworkEntry, 2)
	cidr, _ = ipnet.ParseCIDR("172.16.1.252/32")
	serviceNet[0].CIDR = *cidr
	cidr, _ = ipnet.ParseCIDR("172.16.3.0/24")
	serviceNet[1].CIDR = *cidr

	testCases := []struct {
		name                          string
		hostedCluster                 *hyperv1.HostedCluster
		other                         []crclient.Object
		managementClusterCapabilities capabilities.CapabiltyChecker
		expectedResult                error
		infraKubeVirtVersion          string
		infraK8sVersion               string
	}{
		{
			name: "Cluster uses route but not supported, error",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster",
				},
				Spec: hyperv1.HostedClusterSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type: hyperv1.Route,
						}},
					},
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: clusterNet,
					},
				}},
			managementClusterCapabilities: &fakecapabilities.FakeSupportNoCapabilities{},
			expectedResult:                errors.New(`cluster does not support Routes, but service "" is exposed via a Route`),
		},
		{
			name: "Cluster uses routes and supported, success",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster",
				},
				Spec: hyperv1.HostedClusterSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type: hyperv1.Route,
						}},
					},
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: clusterNet,
					},
				}},
			managementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
		},
		{
			name: "invalid cluster uuid",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster",
				},
				Spec: hyperv1.HostedClusterSpec{
					ClusterID: "foobar",
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: clusterNet,
					},
				}},
			expectedResult: errors.New(`cannot parse cluster ID "foobar": invalid UUID length: 6`),
		},
		{
			name: "Setting Service network CIDR and NodePort IP overlapping, not allowed",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster",
				},
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						MachineNetwork: machineNet,
						ClusterNetwork: clusterNet,
						ServiceNetwork: serviceNet,
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.NodePort,
								NodePort: &hyperv1.NodePortPublishingStrategy{
									Address: "172.16.3.3",
									Port:    4433,
								},
							},
						},
					},
				},
			},
			expectedResult: errors.New(`[spec.networking.MachineNetwork: Invalid value: "172.16.1.0/24": spec.networking.MachineNetwork and spec.networking.ServiceNetwork overlap: 172.16.1.0/24 and 172.16.1.252/32, spec.networking.ServiceNetwork: Invalid value: "172.16.3.0/24": Nodeport IP is within the service network range: 172.16.3.3 is within 172.16.3.0/24]`),
		},
		{
			name: "Setting network CIDRs overlapped, not allowed",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster",
				},
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						ServiceNetwork: serviceNet,
						MachineNetwork: machineNet,
						ClusterNetwork: clusterNet,
					},
				},
			},
			expectedResult: errors.New(`spec.networking.MachineNetwork: Invalid value: "172.16.1.0/24": spec.networking.MachineNetwork and spec.networking.ServiceNetwork overlap: 172.16.1.0/24 and 172.16.1.252/32`),
		},
		{
			name: "multiple published services use the same hostname, error",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster",
				},
				Spec: hyperv1.HostedClusterSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type:  hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{Hostname: "api.example.com"},
							},
						},
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type:  hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{Hostname: "api.example.com"},
							},
						},
					},
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: clusterNet,
					},
				}},
			expectedResult:                errors.New(`service type OAuthServer can't be published with the same hostname api.example.com as service type APIServer`),
			managementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
		},
		{
			name: "KubeVirt cluster meeting min infra cluster versions should succeed",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
					},
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: clusterNet,
					},
				}},
			other: []crclient.Object{
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "creds"}},
			},
			infraKubeVirtVersion: "v1.0.0",
			infraK8sVersion:      "v1.27.0",
		},
		{
			name: "KubeVirt cluster not meeting min infra cluster versions should fail",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
					},
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: clusterNet,
					},
				}},
			other: []crclient.Object{
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "creds"}},
			},
			infraKubeVirtVersion: "v0.99.0",
			infraK8sVersion:      "v1.26.99",

			expectedResult: errors.New(`[infrastructure kubevirt version is [0.99.0], hypershift kubevirt platform requires kubevirt version [1.0.0] or greater, infrastructure Kubernetes version is [1.26.99], hypershift kubevirt platform requires Kubernetes version [1.27.0] or greater]`),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := &HostedClusterReconciler{
				CertRotationScale:             24 * time.Hour,
				Client:                        fake.NewClientBuilder().WithObjects(tc.other...).Build(),
				ManagementClusterCapabilities: tc.managementClusterCapabilities,
			}

			r.KubevirtInfraClients = kvinfra.NewMockKubevirtInfraClientMap(r.Client, tc.infraKubeVirtVersion, tc.infraK8sVersion)

			ctx := context.Background()
			actual := r.validateConfigAndClusterCapabilities(ctx, tc.hostedCluster)
			if diff := cmp.Diff(actual, tc.expectedResult, equateErrorMessage); diff != "" {
				t.Errorf("actual validation result differs from expected: %s", diff)
			}
		})
	}
}

func TestValidateReleaseImage(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	testCases := []struct {
		name                  string
		other                 []crclient.Object
		hostedCluster         *hyperv1.HostedCluster
		releaseImageLoookup   func(_ context.Context, image string, _ []byte) (*releaseinfo.ReleaseImage, error)
		expectedResult        error
		expectedNotFoundError bool
	}{
		{
			name: "no pull secret, error",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						NetworkType: hyperv1.OVNKubernetes,
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
			},
			releaseImageLoookup: func(_ context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
				return testutils.InitReleaseImageOrDie("4.15.0"), nil
			},
			expectedResult:        errors.New("failed to get pull secret: secrets \"pull-secret\" not found"),
			expectedNotFoundError: true,
		},
		{
			name: "invalid pull secret, error",
			other: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "pull-secret"},
					Data:       map[string][]byte{},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						NetworkType: hyperv1.OVNKubernetes,
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
			},
			releaseImageLoookup: func(_ context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
				return testutils.InitReleaseImageOrDie("4.15.0"), nil
			},
			expectedResult: errors.New("expected .dockerconfigjson key in pull secret"),
		},
		{
			name: "unable to pull release image, error",
			other: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "pull-secret"},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: nil,
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						NetworkType: hyperv1.OVNKubernetes,
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: hyperv1.Release{
						Image: "image-4.nope.0",
					},
				},
			},
			releaseImageLoookup: func(_ context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
				return nil, errors.New("unable to lookup release image")
			},
			expectedResult: errors.New("failed to lookup release image: unable to lookup release image"),
		},
		{
			name: "unsupported release, error",
			other: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "pull-secret"},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: nil,
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						NetworkType: hyperv1.OVNKubernetes,
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: hyperv1.Release{
						Image: "image-4.7.0",
					},
				},
			},
			releaseImageLoookup: func(_ context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
				return testutils.InitReleaseImageOrDie("4.7.0"), nil
			},
			expectedResult: errors.New(`releases before 4.8 are not supported. Attempting to use: "4.7.0"`),
		},
		{
			name: "unsupported y-stream downgrade, error",
			other: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "pull-secret"},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: nil,
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						NetworkType: hyperv1.OVNKubernetes,
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: hyperv1.Release{
						Image: "image-4.15.0",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{
							Image: "image-4.16.0",
						},
					},
				},
			},
			releaseImageLoookup: func(_ context.Context, image string, _ []byte) (*releaseinfo.ReleaseImage, error) {
				return testutils.InitReleaseImageOrDie(strings.ReplaceAll(image, "image-", "")), nil
			},
			expectedResult: errors.New(`y-stream downgrade from "4.16.0" to "4.15.0" is not supported`),
		},
		{
			name: "unsupported y-stream upgrade, error",
			other: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "pull-secret"},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: nil,
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						NetworkType: hyperv1.OpenShiftSDN,
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: hyperv1.Release{
						Image: "image-4.15.0",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{
							Image: "image-4.12.0",
						},
					},
				},
			},
			releaseImageLoookup: func(_ context.Context, image string, _ []byte) (*releaseinfo.ReleaseImage, error) {
				return testutils.InitReleaseImageOrDie(strings.ReplaceAll(image, "image-", "")), nil
			},
			expectedResult: errors.New(`y-stream upgrade from "4.12.0" to "4.15.0" is not for OpenShiftSDN`),
		},
		{
			name: "supported y-stream upgrade, success",
			other: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "pull-secret"},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: nil,
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						NetworkType: hyperv1.OVNKubernetes,
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: hyperv1.Release{
						Image: "image-4.15.0",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{
							Image: "image-4.12.0",
						},
					},
				},
			},
			releaseImageLoookup: func(_ context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
				return testutils.InitReleaseImageOrDie("4.15.0"), nil
			},
			expectedResult: nil,
		},
		{
			name: "valid create, success",
			other: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "pull-secret"},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: nil,
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						NetworkType: hyperv1.OVNKubernetes,
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: hyperv1.Release{
						Image: "image-4.15.0",
					},
				},
			},
			releaseImageLoookup: func(_ context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
				return testutils.InitReleaseImageOrDie("4.15.0"), nil
			},
			expectedResult: nil,
		},
		{
			name: "no-op, success",
			other: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "pull-secret"},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: nil,
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						NetworkType: hyperv1.OVNKubernetes,
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: hyperv1.Release{
						Image: "image-4.15.0",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{
							Image: "image-4.15.0",
						},
					},
				},
			},
			expectedResult: nil,
			releaseImageLoookup: func(_ context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
				return testutils.InitReleaseImageOrDie("4.15.0"), nil
			},
		},
		{
			name: "z-stream upgrade, success",
			other: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "pull-secret"},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: nil,
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						NetworkType: hyperv1.OVNKubernetes,
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: hyperv1.Release{
						Image: "image-4.15.0",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{
							Image: "image-4.12.1",
						},
					},
				},
			},
			expectedResult: nil,
			releaseImageLoookup: func(_ context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
				return testutils.InitReleaseImageOrDie("4.15.0"), nil
			},
		},
		{
			name: "y-stream upgrade, success",
			other: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "pull-secret"},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: nil,
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						NetworkType: hyperv1.OVNKubernetes,
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: hyperv1.Release{
						Image: "image-4.15.0",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{
							Image: "image-4.15.0",
						},
					},
				},
			},
			releaseImageLoookup: func(_ context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
				return testutils.InitReleaseImageOrDie("4.15.0"), nil
			},
			expectedResult: nil,
		},
		{
			name: "skip release image validation with annotation, success",
			other: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "pull-secret"},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: nil,
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.SkipReleaseImageValidation: "true",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						NetworkType: hyperv1.OVNKubernetes,
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: hyperv1.Release{
						Image: "image-4.12.0",
					},
				},
			},
			releaseImageLoookup: nil, //won't be called
			expectedResult:      nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			mockedProviderWithOpenShiftImageRegistryOverrides := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(mockCtrl)
			mockedProviderWithOpenShiftImageRegistryOverrides.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(tc.releaseImageLoookup).AnyTimes()
			r := &HostedClusterReconciler{
				CertRotationScale: 24 * time.Hour,
				Client:            fake.NewClientBuilder().WithObjects(tc.other...).Build(),
				ReconcileMetadataProviders: func(ctx context.Context, imgOverrides map[string]string) (releaseinfo.ProviderWithOpenShiftImageRegistryOverrides,
					hyperutil.ImageMetadataProvider, error) {
					return mockedProviderWithOpenShiftImageRegistryOverrides,
						&fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
							Result: &dockerv1client.DockerImageConfig{},
						},
						nil
				},
			}

			ctx := context.Background()
			releaseProvider, _, err := r.ReconcileMetadataProviders(ctx, nil)
			g.Expect(err).ToNot(HaveOccurred())
			actual := r.validateReleaseImage(ctx, tc.hostedCluster, releaseProvider)
			if diff := cmp.Diff(actual, tc.expectedResult, equateErrorMessage); diff != "" {
				t.Errorf("actual validation result differs from expected: %s", diff)
			}
			if tc.expectedNotFoundError {
				g.Expect(errors2.IsNotFound(actual)).To(BeTrue())
			}
		})
	}
}

var equateErrorMessage = cmp.FilterValues(func(x, y interface{}) bool {
	_, ok1 := x.(error)
	_, ok2 := y.(error)
	return ok1 && ok2
}, cmp.Comparer(func(x, y interface{}) bool {
	xe := x.(error)
	ye := y.(error)
	if xe == nil || ye == nil {
		return xe == nil && ye == nil
	}
	return xe.Error() == ye.Error()
}))

func TestPauseHostedControlPlane(t *testing.T) {
	fakePauseAnnotationValue := "true"
	fakeHCPName := "cluster1"
	fakeHCPNamespace := "master-cluster1"
	testsCases := []struct {
		name                             string
		inputObjects                     []crclient.Object
		inputHostedControlPlane          *hyperv1.HostedControlPlane
		expectedHostedControlPlaneObject *hyperv1.HostedControlPlane
	}{
		{
			name:                    "if a hostedControlPlane exists then the pauseReconciliation annotation is added to it",
			inputHostedControlPlane: manifests.HostedControlPlane(fakeHCPNamespace, fakeHCPName),
			inputObjects: []crclient.Object{
				&hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: fakeHCPNamespace,
						Name:      fakeHCPName,
					},
				},
			},
			expectedHostedControlPlaneObject: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: fakeHCPNamespace,
					Name:      fakeHCPName,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					PausedUntil: &fakePauseAnnotationValue,
				},
			},
		},
		{
			name:                             "if a hostedControlPlane does not exist it is not created",
			inputHostedControlPlane:          manifests.HostedControlPlane(fakeHCPNamespace, fakeHCPName),
			inputObjects:                     []crclient.Object{},
			expectedHostedControlPlaneObject: nil,
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.inputObjects...).Build()
			err := pauseHostedControlPlane(context.Background(), c, tc.inputHostedControlPlane, &fakePauseAnnotationValue)
			g.Expect(err).ToNot(HaveOccurred())
			finalHCP := manifests.HostedControlPlane(fakeHCPNamespace, fakeHCPName)
			err = c.Get(context.Background(), crclient.ObjectKeyFromObject(finalHCP), finalHCP)
			if tc.expectedHostedControlPlaneObject != nil {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(finalHCP.Annotations).To(BeEquivalentTo(tc.expectedHostedControlPlaneObject.Annotations))
			} else {
				g.Expect(errors2.IsNotFound(err)).To(BeTrue())
			}
		})
	}
}

func TestDefaultClusterIDsIfNeeded(t *testing.T) {
	testHC := func(infraID, clusterID string) *hyperv1.HostedCluster {
		return &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "fake-cluster",
				Namespace: "fake-namespace",
			},
			Spec: hyperv1.HostedClusterSpec{
				InfraID:   infraID,
				ClusterID: clusterID,
			},
		}
	}
	tests := []struct {
		name string
		hc   *hyperv1.HostedCluster
	}{
		{
			name: "generate both",
			hc:   testHC("", ""),
		},
		{
			name: "generate clusterid",
			hc:   testHC("fake-infra", ""),
		},
		{
			name: "generate infra-id",
			hc:   testHC("", "fake-uuid"),
		},
		{
			name: "generate none",
			hc:   testHC("fake-infra", "fake-uuid"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := &HostedClusterReconciler{
				CertRotationScale: 24 * time.Hour,
				Client:            fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(test.hc).Build(),
			}
			g := NewGomegaWithT(t)
			previousInfraID := test.hc.Spec.InfraID
			previousClusterID := test.hc.Spec.ClusterID
			err := r.defaultClusterIDsIfNeeded(context.Background(), test.hc)
			g.Expect(err).ToNot(HaveOccurred())
			resultHC := &hyperv1.HostedCluster{}
			err = r.Client.Get(context.Background(), crclient.ObjectKeyFromObject(test.hc), resultHC)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resultHC.Spec.ClusterID).NotTo(BeEmpty())
			g.Expect(resultHC.Spec.InfraID).NotTo(BeEmpty())
			if len(previousClusterID) > 0 {
				g.Expect(resultHC.Spec.ClusterID).To(BeIdenticalTo(previousClusterID))
			}
			if len(previousInfraID) > 0 {
				g.Expect(resultHC.Spec.InfraID).To(BeIdenticalTo(previousInfraID))
			}
		})
	}
}

func TestIsUpgradeable(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	releaseImageFrom := "image-4.13"
	releaseImageTo := "image-4.14"
	tests := []struct {
		name                string
		hc                  *hyperv1.HostedCluster
		releaseImageLoookup func(_ context.Context, image string, _ []byte) (*releaseinfo.ReleaseImage, error)
		upgrading           bool
		err                 bool
	}{
		{
			name: "version not reported yet",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: releaseImageFrom,
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: nil,
				},
			},
			releaseImageLoookup: func(_ context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
				return testutils.InitReleaseImageOrDie("4.15.0"), nil
			},
			upgrading: false,
			err:       false,
		},
		{
			name: "not upgrading",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: releaseImageFrom,
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{
							Image:   releaseImageFrom,
							Version: "4.13.0",
						},
					},
				},
			},
			releaseImageLoookup: func(_ context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
				return testutils.InitReleaseImageOrDie("4.15.0"), nil
			},
			upgrading: false,
			err:       false,
		},
		{
			name: "not upgradeable, no force annotation",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: releaseImageTo,
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{
							Image:   releaseImageFrom,
							Version: "4.13.0",
						},
					},
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.ClusterVersionUpgradeable),
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			releaseImageLoookup: func(_ context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
				return testutils.InitReleaseImageOrDie("4.15.0"), nil
			},
			upgrading: true,
			err:       true,
		},
		{
			name: "not upgradeable, old force annotation",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.ForceUpgradeToAnnotation: releaseImageFrom,
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: releaseImageTo,
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{
							Image:   releaseImageFrom,
							Version: "4.13.0",
						},
					},
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.ClusterVersionUpgradeable),
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			releaseImageLoookup: func(_ context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
				return testutils.InitReleaseImageOrDie("4.15.0"), nil
			},
			upgrading: true,
			err:       true,
		},
		{
			name: "not upgradeable, force annotation",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.ForceUpgradeToAnnotation: releaseImageTo,
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: releaseImageTo,
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{},
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.ClusterVersionUpgradeable),
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			releaseImageLoookup: func(_ context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
				return testutils.InitReleaseImageOrDie("4.15.0"), nil
			},
			upgrading: true,
			err:       false,
		},
		{
			name: "not upgradeable but z-stream upgrade allowed",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: "image-4.13.1",
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{
							Image:   releaseImageFrom,
							Version: "4.13.0",
						},
					},
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.ClusterVersionUpgradeable),
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			releaseImageLoookup: func(ctx context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
				releaseImage := testutils.InitReleaseImageOrDie("4.15.0")
				releaseImage.ObjectMeta.Name = "4.13.1" // patch with z-stream
				return releaseImage, nil
			},
			upgrading: true,
			err:       false,
		},
	}
	for _, test := range tests {
		mockedProviderWithOpenShiftImageRegistryOverrides := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(mockCtrl)
		mockedProviderWithOpenShiftImageRegistryOverrides.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(test.releaseImageLoookup).AnyTimes()

		objs := []crclient.Object{
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "pull-secret"},
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: nil,
				},
			},
		}
		r := &HostedClusterReconciler{
			CertRotationScale: 24 * time.Hour,
			Client:            fake.NewClientBuilder().WithObjects(objs...).Build(),
			ReconcileMetadataProviders: func(ctx context.Context, imgOverrides map[string]string) (releaseinfo.ProviderWithOpenShiftImageRegistryOverrides, hyperutil.ImageMetadataProvider, error) {
				return mockedProviderWithOpenShiftImageRegistryOverrides,
					&fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
						Result: &dockerv1client.DockerImageConfig{},
					},
					nil
			},
		}

		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			g := NewGomegaWithT(t)
			releaseProvider, _, err := r.ReconcileMetadataProviders(ctx, nil)
			g.Expect(err).ToNot(HaveOccurred())
			releaseImage, err := r.lookupReleaseImage(context.TODO(), test.hc, releaseProvider)
			if err != nil {
				t.Errorf("isUpgrading() internal err = %v", err)
			}
			upgrading, _, err := isUpgrading(test.hc, releaseImage)
			if upgrading != test.upgrading {
				t.Errorf("isUpgrading() upgrading = %v, want %v", upgrading, test.upgrading)
			}
			if (err == nil) == test.err {
				t.Errorf("isUpgrading() err = %v, want %v", err, test.err)
				return
			}
		})
	}
}

func TestReconciliationSuccessConditionSetting(t *testing.T) {

	// Serialization seems to round to seconds, so we have to do the
	// same to be able to compare.
	now := metav1.Time{Time: time.Now().Round(time.Second)}
	reconcilerNow := metav1.Time{Time: now.Add(time.Second)}

	testCases := []struct {
		name               string
		reconcileResult    error
		existingConditions []metav1.Condition
		expectedConditions []metav1.Condition
	}{
		{
			name: "Success, success condition gets set",
			expectedConditions: []metav1.Condition{{
				Type:               string(hyperv1.ReconciliationSucceeded),
				Status:             metav1.ConditionTrue,
				Reason:             "ReconciliatonSucceeded",
				Message:            "Reconciliation completed successfully",
				LastTransitionTime: reconcilerNow,
			}},
		},
		{
			name: "Success, existing success condition transition timestamp stays",
			existingConditions: []metav1.Condition{{
				Type:               string(hyperv1.ReconciliationSucceeded),
				Status:             metav1.ConditionTrue,
				Message:            "Reconciliation completed successfully",
				Reason:             "ReconciliatonSucceeded",
				LastTransitionTime: now,
			}},
			expectedConditions: []metav1.Condition{{
				Type:               string(hyperv1.ReconciliationSucceeded),
				Status:             metav1.ConditionTrue,
				Reason:             "ReconciliatonSucceeded",
				Message:            "Reconciliation completed successfully",
				LastTransitionTime: now,
			}},
		},
		{
			name: "Success, error condition gets cleared",
			existingConditions: []metav1.Condition{{
				Type:               string(hyperv1.ReconciliationSucceeded),
				Status:             metav1.ConditionFalse,
				Message:            "Some error",
				LastTransitionTime: now,
			}},
			expectedConditions: []metav1.Condition{{
				Type:               string(hyperv1.ReconciliationSucceeded),
				Status:             metav1.ConditionTrue,
				Reason:             "ReconciliatonSucceeded",
				Message:            "Reconciliation completed successfully",
				LastTransitionTime: reconcilerNow,
			}},
		},
		{
			name:            "Error, error gets set",
			reconcileResult: errors.New("things went sideways"),
			expectedConditions: []metav1.Condition{{
				Type:               string(hyperv1.ReconciliationSucceeded),
				Status:             metav1.ConditionFalse,
				Reason:             "ReconciliationError",
				Message:            "things went sideways",
				LastTransitionTime: reconcilerNow,
			}},
		},
		{
			name:            "Error, errors gets updated",
			reconcileResult: errors.New("things went sideways"),
			existingConditions: []metav1.Condition{{
				Type:               string(hyperv1.ReconciliationSucceeded),
				Status:             metav1.ConditionFalse,
				Reason:             "ReconciliationError",
				Message:            "some old error",
				LastTransitionTime: reconcilerNow,
			}},
			expectedConditions: []metav1.Condition{{
				Type:               string(hyperv1.ReconciliationSucceeded),
				Status:             metav1.ConditionFalse,
				Reason:             "ReconciliationError",
				Message:            "things went sideways",
				LastTransitionTime: reconcilerNow,
			}},
		},
		{
			name:            "Error, success condition gets cleaned up",
			reconcileResult: errors.New("things went sideways"),
			existingConditions: []metav1.Condition{{
				Type:               string(hyperv1.ReconciliationSucceeded),
				Status:             metav1.ConditionTrue,
				Reason:             "ReconciliatonSucceeded",
				LastTransitionTime: now,
			}},
			expectedConditions: []metav1.Condition{{
				Type:               string(hyperv1.ReconciliationSucceeded),
				Status:             metav1.ConditionFalse,
				Reason:             "ReconciliationError",
				Message:            "things went sideways",
				LastTransitionTime: reconcilerNow,
			}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "hcluster"},
				Status: hyperv1.HostedClusterStatus{
					Conditions: tc.existingConditions,
				},
			}

			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcluster).WithStatusSubresource(hcluster).Build()
			r := &HostedClusterReconciler{
				CertRotationScale: 24 * time.Hour,
				Client:            c,
				overwriteReconcile: func(ctx context.Context, req ctrl.Request, log logr.Logger, hcluster *hyperv1.HostedCluster) (ctrl.Result, error) {
					return ctrl.Result{}, tc.reconcileResult
				},
				now: func() metav1.Time { return reconcilerNow },
			}

			ctx := context.Background()

			var actualErrString string
			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: crclient.ObjectKeyFromObject(hcluster)}); err != nil {
				actualErrString = err.Error()
			}
			var expectedErrString string
			if tc.reconcileResult != nil {
				expectedErrString = tc.reconcileResult.Error()
			}
			if actualErrString != expectedErrString {
				t.Errorf("actual error %s doesn't match expected %s", actualErrString, expectedErrString)
			}

			if err := c.Get(ctx, crclient.ObjectKeyFromObject(hcluster), hcluster); err != nil {
				t.Fatalf("failed to get hcluster after reconciliation: %v", err)
			}

			if diff := cmp.Diff(hcluster.Status.Conditions, tc.expectedConditions); diff != "" {
				t.Errorf("actual conditions differ from expected: %s", diff)
			}
		})
	}
}

func TestIsProgressing(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	tests := []struct {
		name       string
		hc         *hyperv1.HostedCluster
		withDigest string
		want       bool
		wantErr    bool
	}{
		{
			name: "stable at release",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: "release-1.2",
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{
							Image:   "release-1.2",
							Version: "1.2.0",
						},
					},
				},
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "stable at release with digest",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: "release-1.2",
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{
							Image:   "release-1.2@sha12345",
							Version: "1.2.0",
						},
					},
				},
			},
			withDigest: "release-1.2@sha12345",
			want:       false,
			wantErr:    false,
		},
		{
			name: "cluster is rolling out",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: "release-1.2",
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "cluster is upgrading with digest",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: "release-1.3",
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{
							Image:   "release-1.2@sha12345",
							Version: "1.2.0",
						},
					},
				},
			},
			withDigest: "release-1.3@sha67890",
			want:       true,
			wantErr:    false,
		},
		{
			name: "cluster is upgrading",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: "release-1.3",
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{
							Image:   "release-1.2",
							Version: "1.2.0",
						},
					},
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "cluster update is blocked by condition",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: "release-1.3",
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{
							Image:   "release-1.2",
							Version: "1.2.0",
						},
					},
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.ValidHostedClusterConfiguration),
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			want:    false,
			wantErr: true,
		},
		{
			name: "cluster upgrade is blocked by ClusterVersionUpgradeable",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: "release-1.3",
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{
							Image:   "release-1.2",
							Version: "1.2.0",
						},
					},
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.ClusterVersionUpgradeable),
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			want:    false,
			wantErr: true,
		},
		{
			name: "cluster upgrade is forced",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.ForceUpgradeToAnnotation: "release-1.3",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: "release-1.3",
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{
							Image:   "release-1.2",
							Version: "1.2.0",
						},
					},
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.ClusterVersionUpgradeable),
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			want:    true,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		objs := []crclient.Object{
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "pull-secret"},
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: nil,
				},
			},
		}
		mockedProviderWithOpenShiftImageRegistryOverrides := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(mockCtrl)
		mockedProviderWithOpenShiftImageRegistryOverrides.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).Return(testutils.InitReleaseImageOrDie("4.15.0"), nil).AnyTimes()
		r := &HostedClusterReconciler{
			CertRotationScale: 24 * time.Hour,
			Client:            fake.NewClientBuilder().WithObjects(objs...).Build(),
			ReconcileMetadataProviders: func(ctx context.Context, imgOverrides map[string]string) (releaseinfo.ProviderWithOpenShiftImageRegistryOverrides, hyperutil.ImageMetadataProvider, error) {
				return mockedProviderWithOpenShiftImageRegistryOverrides,
					&fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
						Result: &dockerv1client.DockerImageConfig{},
					},
					nil
			},
		}

		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			g := NewGomegaWithT(t)
			releaseProvider, _, err := r.ReconcileMetadataProviders(ctx, nil)
			g.Expect(err).ToNot(HaveOccurred())
			releaseImage, err := r.lookupReleaseImage(context.TODO(), tt.hc, releaseProvider)
			if err != nil {
				t.Errorf("isProgressing() internal err = %v", err)
			}
			got, err := isProgressing(tt.hc, releaseImage, func() (string, error) {
				return tt.withDigest, nil
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("isProgressing() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("isProgressing() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestComputeAWSEndpointServiceCondition(t *testing.T) {
	tests := []struct {
		name                string
		endpointAConditions []metav1.Condition
		endpointBConditions []metav1.Condition
		expected            metav1.Condition
	}{
		{
			name: "Both endpoints condition is true",
			endpointAConditions: []metav1.Condition{
				{
					Type:    string(hyperv1.AWSEndpointAvailable),
					Status:  metav1.ConditionTrue,
					Reason:  hyperv1.AWSSuccessReason,
					Message: hyperv1.AllIsWellMessage,
				},
			},
			endpointBConditions: []metav1.Condition{
				{
					Type:    string(hyperv1.AWSEndpointAvailable),
					Status:  metav1.ConditionTrue,
					Reason:  hyperv1.AWSSuccessReason,
					Message: hyperv1.AllIsWellMessage,
				},
			},
			expected: metav1.Condition{
				Type:    string(hyperv1.AWSEndpointAvailable),
				Status:  metav1.ConditionTrue,
				Reason:  hyperv1.AWSSuccessReason,
				Message: hyperv1.AllIsWellMessage,
			},
		},
		{
			name: "endpointA condition true, endpointB condition false",
			endpointAConditions: []metav1.Condition{
				{
					Type:    string(hyperv1.AWSEndpointAvailable),
					Status:  metav1.ConditionTrue,
					Reason:  hyperv1.AWSSuccessReason,
					Message: hyperv1.AllIsWellMessage,
				},
			},
			endpointBConditions: []metav1.Condition{
				{
					Type:    string(hyperv1.AWSEndpointAvailable),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.AWSErrorReason,
					Message: "error message B",
				},
			},
			expected: metav1.Condition{
				Type:    string(hyperv1.AWSEndpointAvailable),
				Status:  metav1.ConditionFalse,
				Reason:  hyperv1.AWSErrorReason,
				Message: "error message B",
			},
		},
		{
			name: "endpointA condition false, endpointB condition true",
			endpointAConditions: []metav1.Condition{
				{
					Type:    string(hyperv1.AWSEndpointAvailable),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.AWSErrorReason,
					Message: "error message A",
				},
			},
			endpointBConditions: []metav1.Condition{
				{
					Type:    string(hyperv1.AWSEndpointAvailable),
					Status:  metav1.ConditionTrue,
					Reason:  hyperv1.AWSSuccessReason,
					Message: hyperv1.AllIsWellMessage,
				},
			},
			expected: metav1.Condition{
				Type:    string(hyperv1.AWSEndpointAvailable),
				Status:  metav1.ConditionFalse,
				Reason:  hyperv1.AWSErrorReason,
				Message: "error message A",
			},
		},
		{
			name: "Both endpoints condition is false",
			endpointAConditions: []metav1.Condition{
				{
					Type:    string(hyperv1.AWSEndpointAvailable),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.AWSErrorReason,
					Message: "error message A",
				},
			},
			endpointBConditions: []metav1.Condition{
				{
					Type:    string(hyperv1.AWSEndpointAvailable),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.AWSErrorReason,
					Message: "error message B",
				},
			},
			expected: metav1.Condition{
				Type:    string(hyperv1.AWSEndpointAvailable),
				Status:  metav1.ConditionFalse,
				Reason:  hyperv1.AWSErrorReason,
				Message: "error message A; error message B",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			awsEndpointServiceList := hyperv1.AWSEndpointServiceList{
				Items: []hyperv1.AWSEndpointService{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "endpointA",
						},
						Status: hyperv1.AWSEndpointServiceStatus{
							Conditions: tc.endpointAConditions,
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "endpointB",
						},
						Status: hyperv1.AWSEndpointServiceStatus{
							Conditions: tc.endpointBConditions,
						},
					},
				},
			}
			condition := computeAWSEndpointServiceCondition(awsEndpointServiceList, hyperv1.AWSEndpointAvailable)
			if condition != tc.expected {
				t.Errorf("error, expected %v\nbut got %v", tc.expected, condition)
			}
		})
	}
}

func TestValidateSliceNetworkCIDRs(t *testing.T) {
	tests := []struct {
		name    string
		mn      []hyperv1.MachineNetworkEntry
		cn      []hyperv1.ClusterNetworkEntry
		sn      []hyperv1.ServiceNetworkEntry
		wantErr bool
	}{
		{
			name:    "given a conflicting IPv6 clusterNetwork overlapped with machineNetwork, it should fail",
			mn:      []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd02::/48")}},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd02::/64")}},
			sn:      []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("2620:52:0:1306::1/64")}},
			wantErr: true,
		},
		{
			name:    "given different IPv6 network CIDRs, it should success",
			mn:      []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd02::/48")}},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd01::/64")}},
			sn:      []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("2620:52:0:1306::1/64")}},
			wantErr: false,
		},
		{
			name:    "given a conflicting IPv4 clusterNetwork overlapped with serviceNetwork, it should fail",
			mn:      []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.0.0/16")}},
			sn:      []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.0.0/24")}},
			wantErr: true,
		},
		{
			name:    "given different IPv4 network CIDRs, it should success",
			mn:      []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.1.0/24")}},
			sn:      []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.0.0/24")}},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "any",
				},
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						MachineNetwork: tt.mn,
						ClusterNetwork: tt.cn,
						ServiceNetwork: tt.sn,
					},
				},
			}
			err := validateSliceNetworkCIDRs(hc)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSliceNetworkCIDRs() wantErr %v, err %v", tt.wantErr, err)
			}
		})
	}
}

func TestCheckAdvertiseAddressOverlapping(t *testing.T) {
	tests := []struct {
		name    string
		mn      []hyperv1.MachineNetworkEntry
		cn      []hyperv1.ClusterNetworkEntry
		sn      []hyperv1.ServiceNetworkEntry
		aa      *hyperv1.APIServerNetworking
		wantErr bool
	}{
		{
			name:    "given an IPv6 defined AdvertiseAddress overlapped with ClusterNetwork, it should fail",
			aa:      &hyperv1.APIServerNetworking{AdvertiseAddress: ptr.To("fd03::1")},
			mn:      []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd02::/48")}},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd03::/64")}},
			sn:      []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("2620:52:0:1306::1/64")}},
			wantErr: true,
		},
		{
			name:    "given not overlapped IPv6 networks CIDRs and not defined AdvertiseAddress, it should success",
			mn:      []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd02::/48")}},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd01::/64")}},
			sn:      []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("2620:52:0:1306::1/64")}},
			wantErr: false,
		},
		{
			name:    "given an IPv4 defined AdvertiseAddress overlapped with MachineNetwork, it should fail",
			aa:      &hyperv1.APIServerNetworking{AdvertiseAddress: ptr.To("192.168.1.1")},
			mn:      []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.0.0/16")}},
			sn:      []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.0.0/24")}},
			wantErr: true,
		},
		{
			name:    "given not overlapped IPv4 networks CIDRs and not defined AdvertiseAddress, it should success",
			mn:      []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.1.0/24")}},
			sn:      []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.0.0/24")}},
			wantErr: false,
		},
		{
			name:    "given a not valid AdvertiseAddress, it should fail",
			aa:      &hyperv1.APIServerNetworking{AdvertiseAddress: ptr.To("192.168.2.1.2")},
			mn:      []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.1.0/24")}},
			sn:      []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.0.0/24")}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "any",
				},
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						MachineNetwork: tt.mn,
						ClusterNetwork: tt.cn,
						ServiceNetwork: tt.sn,
						APIServer:      tt.aa,
					},
				},
			}
			g := NewGomegaWithT(t)
			err := checkAdvertiseAddressOverlapping(hc)
			g.Expect((err != nil)).To(Equal(tt.wantErr))
		})
	}
}

func TestFindAdvertiseAddress(t *testing.T) {
	tests := []struct {
		name             string
		aa               *hyperv1.APIServerNetworking
		cn               []hyperv1.ClusterNetworkEntry
		resultAdvAddress string
		wantErr          bool
	}{
		{
			name:             "given a defined AdvertiseAddress, should be the result and IPv4",
			aa:               &hyperv1.APIServerNetworking{AdvertiseAddress: ptr.To("192.168.1.1")},
			cn:               []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.1.0/24")}},
			resultAdvAddress: "192.168.1.1",
		},
		{
			name:             "given a hc without AdvertiseAddress, it should return the default IPv4 address",
			cn:               []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.1.0/24")}},
			resultAdvAddress: config.DefaultAdvertiseIPv4Address,
		},
		{
			name:             "given an IPv6 hc with defined AdvertiseAddress, it should return that address",
			aa:               &hyperv1.APIServerNetworking{AdvertiseAddress: ptr.To("fd03::1")},
			cn:               []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd01::/64")}},
			resultAdvAddress: "fd03::1",
		},
		{
			name:             "given an IPv6 hc without AdvertiseAddress, it return IPv6 default address",
			cn:               []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd01::/64")}},
			resultAdvAddress: config.DefaultAdvertiseIPv6Address,
		},
		{
			name:    "given an invalid IPv4 AdvertiseAddress, it should fail",
			aa:      &hyperv1.APIServerNetworking{AdvertiseAddress: ptr.To("192.168.1.1222")},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.1.0/24")}},
			wantErr: true,
		},
		{
			name:    "given an invalid IPv6 AdvertiseAddress, it should fail",
			aa:      &hyperv1.APIServerNetworking{AdvertiseAddress: ptr.To("fd03::4444444")},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd01::/64")}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "any",
				},
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: tt.cn,
						APIServer:      tt.aa,
					},
				},
			}
			g := NewGomegaWithT(t)
			avdAddress, err := findAdvertiseAddress(hc)
			if tt.wantErr {
				g.Expect(err).To(Not(BeNil()))
				g.Expect(avdAddress).To(BeZero())
			} else {
				g.Expect(avdAddress.String()).To(Equal(tt.resultAdvAddress))
			}
		})
	}
}

func TestValidateNetworkStackAddresses(t *testing.T) {
	tests := []struct {
		name    string
		cn      []hyperv1.ClusterNetworkEntry
		mn      []hyperv1.MachineNetworkEntry
		sn      []hyperv1.ServiceNetworkEntry
		aa      *hyperv1.APIServerNetworking
		wantErr bool
	}{
		{
			name:    "given an IPv6 clusterNetwork and an IPv4 ServiceNetwork, it should fail",
			aa:      &hyperv1.APIServerNetworking{AdvertiseAddress: ptr.To("fd03::1")},
			mn:      []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd02::/48")}},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd03::/64")}},
			sn:      []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.0.0/24")}},
			wantErr: true,
		},
		{
			name:    "on IPv6 and IPv4 Advertise Address, it should fail",
			aa:      &hyperv1.APIServerNetworking{AdvertiseAddress: ptr.To("192.168.1.1")},
			mn:      []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd02::/48")}},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd01::/64")}},
			sn:      []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("2620:52:0:1306::1/64")}},
			wantErr: true,
		},
		{
			name:    "on IPv6 and defining Advertise Address, it should success",
			aa:      &hyperv1.APIServerNetworking{AdvertiseAddress: ptr.To("fd03::1")},
			mn:      []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd02::/48")}},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd01::/64")}},
			sn:      []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("2620:52:0:1306::1/64")}},
			wantErr: false,
		},
		{
			name:    "given an IPv4 clusterNetwork and an IPv6 ServiceNetwork, it should fail",
			aa:      &hyperv1.APIServerNetworking{AdvertiseAddress: ptr.To("192.168.1.1")},
			mn:      []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.0.0/16")}},
			sn:      []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("2620:52:0:1306::1/64")}},
			wantErr: true,
		},
		{
			name:    "on IPv4 and defining IPv6 Advertise Address, it should fail",
			aa:      &hyperv1.APIServerNetworking{AdvertiseAddress: ptr.To("fd03::1")},
			mn:      []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.1.0/24")}},
			sn:      []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.0.0/24")}},
			wantErr: true,
		},
		{
			name:    "on IPv4 and defining Advertise Address, it should success",
			aa:      &hyperv1.APIServerNetworking{AdvertiseAddress: ptr.To("192.168.1.1")},
			mn:      []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.0.0/24")}},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.1.0/24")}},
			sn:      []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.0.0/24")}},
			wantErr: false,
		},
		{
			name:    "on IPv4, it should success",
			mn:      []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.1.0/24")}},
			sn:      []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.0.0/24")}},
			wantErr: false,
		},
		{
			name:    "on IPv6, it should success",
			mn:      []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd02::/48")}},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd01::/64")}},
			sn:      []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("2620:52:0:1306::1/64")}},
			wantErr: false,
		},
		{
			name:    "given an IPv4 invalid advertise address, it should fail",
			aa:      &hyperv1.APIServerNetworking{AdvertiseAddress: ptr.To("192.168.1.1.2")},
			mn:      []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.0.0/24")}},
			sn:      []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.1.0/24")}},
			wantErr: true,
		},
		{
			name:    "given an IPv6 invalid advertise address, it should fail",
			aa:      &hyperv1.APIServerNetworking{AdvertiseAddress: ptr.To("fd03::1::32")},
			mn:      []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd02::/48")}},
			cn:      []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd03::/64")}},
			sn:      []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("2620:52:0:1306::1/64")}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "any",
				},
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: tt.cn,
						ServiceNetwork: tt.sn,
						MachineNetwork: tt.mn,
						APIServer:      tt.aa,
					},
				},
			}
			err := validateNetworkStackAddresses(hc)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateNetworkStackAddresses() wantErr %v, err %v", tt.wantErr, err)
			}
		})
	}
}

func TestReconcileCAPIProviderDeployment(t *testing.T) {
	testCases := []struct {
		name       string
		deployment *appsv1.Deployment
		expected   *metav1.LabelSelector
	}{
		{
			name: "When has selector it should keep it",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterapi.CAPIProviderDeployment("test").Name,
					Namespace: clusterapi.CAPIProviderDeployment("test").Namespace,
					Annotations: map[string]string{
						hcmetrics.HasBeenAvailableAnnotation: "true",
					},
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"keep": "it",
						},
					},
				},
			},
			expected: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"keep": "it",
				},
			},
		},
		{
			name: "When it doesn't have selector it should add a new one",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterapi.CAPIProviderDeployment("test").Name,
					Namespace: clusterapi.CAPIProviderDeployment("test").Namespace,
					Annotations: map[string]string{
						hcmetrics.HasBeenAvailableAnnotation: "true",
					},
				},
				Spec: appsv1.DeploymentSpec{},
			},
			expected: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"control-plane": "capi-provider-controller-manager",
					"app":           "capi-provider-controller-manager",
					"hypershift.openshift.io/control-plane-component": "capi-provider-controller-manager",
				},
			},
		},
	}

	g := NewGomegaWithT(t)
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.deployment).Build()
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test",
				},
				Spec: hyperv1.HostedControlPlaneSpec{},
			}
			createOrUpdate := upsert.New(false)
			deployment := clusterapi.CAPIProviderDeployment("test")
			capiProviderServiceAccount := clusterapi.CAPIProviderServiceAccount("test")
			_, err := createOrUpdate.CreateOrUpdate(context.Background(), client, deployment, func() error {
				return reconcileCAPIProviderDeployment(deployment, &deployment.Spec, hcp, capiProviderServiceAccount, false)
			})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(deployment.Spec.Selector).To(BeEquivalentTo(tc.expected))
		})
	}
}

func TestKubevirtETCDEncKey(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		hc             *hyperv1.HostedCluster
		secretName     string
		secretExpected bool
		objects        []crclient.Object
	}{
		{
			name: "secret encryption already defined",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kubevirt",
					Namespace: "test",
					Annotations: map[string]string{
						hyperv1.AllowUnsupportedKubeVirtRHCOSVariantsAnnotation: "true",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							GenerateID: "123456789",
							Credentials: &hyperv1.KubevirtPlatformCredentials{
								InfraNamespace: "kubevirt-kubevirt",
								InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{
									Name: "secret",
									Key:  "key",
								},
							},
						},
					},
					SecretEncryption: &hyperv1.SecretEncryptionSpec{
						Type: hyperv1.AESCBC,
						AESCBC: &hyperv1.AESCBCSpec{
							ActiveKey: corev1.LocalObjectReference{
								Name: "kubevirt" + etcdEncKeyPostfix,
							},
						},
					},
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{
							AdvertiseAddress: ptr.To("1.2.3.4"),
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.LoadBalancer,
							},
						},
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "kubevirt" + etcdEncKeyPostfix,
					},
				},
			},
			secretName:     "kubevirt" + etcdEncKeyPostfix,
			secretExpected: true,
			objects: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "kubevirt" + etcdEncKeyPostfix, Namespace: "test"},
					Data: map[string][]byte{
						hyperv1.AESCBCKeySecretKey: {1, 2, 3, 4, 5, 6, 7, 8, 9, 0},
						".dockerconfigjson":        []byte("{}"),
					},
				},
			},
		},
		{
			name: "secret encryption not defined",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kubevirt",
					Namespace: "test",
					Annotations: map[string]string{
						hyperv1.AllowUnsupportedKubeVirtRHCOSVariantsAnnotation: "true",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							GenerateID: "123456789",
							Credentials: &hyperv1.KubevirtPlatformCredentials{
								InfraNamespace: "kubevirt-kubevirt",
								InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{
									Name: "secret",
									Key:  "key",
								},
							},
						},
					},
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{
							AdvertiseAddress: ptr.To("1.2.3.4"),
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.LoadBalancer,
							},
						},
					},
				},
			},
			secretName:     "kubevirt" + etcdEncKeyPostfix,
			secretExpected: true,
		},
		{
			name: "secret encryption with no type",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kubevirt",
					Namespace: "test",
					Annotations: map[string]string{
						hyperv1.AllowUnsupportedKubeVirtRHCOSVariantsAnnotation: "true",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							GenerateID: "123456789",
							Credentials: &hyperv1.KubevirtPlatformCredentials{
								InfraNamespace: "kubevirt-kubevirt",
								InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{
									Name: "secret",
									Key:  "key",
								},
							},
						},
					},
					SecretEncryption: &hyperv1.SecretEncryptionSpec{},
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{
							AdvertiseAddress: ptr.To("1.2.3.4"),
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.LoadBalancer,
							},
						},
					},
				},
			},
			secretName:     "kubevirt" + etcdEncKeyPostfix,
			secretExpected: true,
		},
		{
			name: "secret encryption with no details",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kubevirt",
					Namespace: "test",
					Annotations: map[string]string{
						hyperv1.AllowUnsupportedKubeVirtRHCOSVariantsAnnotation: "true",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							GenerateID: "123456789",
							Credentials: &hyperv1.KubevirtPlatformCredentials{
								InfraNamespace: "kubevirt-kubevirt",
								InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{
									Name: "secret",
									Key:  "key",
								},
							},
						},
					},
					SecretEncryption: &hyperv1.SecretEncryptionSpec{
						Type: hyperv1.AESCBC,
					},
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{
							AdvertiseAddress: ptr.To("1.2.3.4"),
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.LoadBalancer,
							},
						},
					},
				},
			},
			secretName:     "kubevirt" + etcdEncKeyPostfix,
			secretExpected: true,
		},
		{
			name: "secret encryption with no name",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kubevirt",
					Namespace: "test",
					Annotations: map[string]string{
						hyperv1.AllowUnsupportedKubeVirtRHCOSVariantsAnnotation: "true",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							GenerateID: "123456789",
							Credentials: &hyperv1.KubevirtPlatformCredentials{
								InfraNamespace: "kubevirt-kubevirt",
								InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{
									Name: "secret",
									Key:  "key",
								},
							},
						},
					},
					SecretEncryption: &hyperv1.SecretEncryptionSpec{
						Type:   hyperv1.AESCBC,
						AESCBC: &hyperv1.AESCBCSpec{},
					},
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{
							AdvertiseAddress: ptr.To("1.2.3.4"),
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.LoadBalancer,
							},
						},
					},
				},
			},
			secretName:     "kubevirt" + etcdEncKeyPostfix,
			secretExpected: true,
		},
		{
			name: "secret encryption with custom name",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kubevirt",
					Namespace: "test",
					Annotations: map[string]string{
						hyperv1.AllowUnsupportedKubeVirtRHCOSVariantsAnnotation: "true",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							GenerateID: "123456789",
							Credentials: &hyperv1.KubevirtPlatformCredentials{
								InfraNamespace: "kubevirt-kubevirt",
								InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{
									Name: "secret",
									Key:  "key",
								},
							},
						},
					},
					SecretEncryption: &hyperv1.SecretEncryptionSpec{
						Type: hyperv1.AESCBC,
						AESCBC: &hyperv1.AESCBCSpec{
							ActiveKey: corev1.LocalObjectReference{
								Name: "custom-name",
							},
						},
					},
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{
							AdvertiseAddress: ptr.To("1.2.3.4"),
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.LoadBalancer,
							},
						},
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "custom-name",
					},
				},
			},
			secretName:     "custom-name",
			secretExpected: false,
			objects: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "custom-name", Namespace: "test"},
					Data: map[string][]byte{
						hyperv1.AESCBCKeySecretKey: {1, 2, 3, 4, 5, 6, 7, 8, 9, 0},
						".dockerconfigjson":        []byte("{}"),
					},
				},
			},
		},
		{
			name: "secret encryption not defined and secret exists with no key",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kubevirt",
					Namespace: "test",
					Annotations: map[string]string{
						hyperv1.AllowUnsupportedKubeVirtRHCOSVariantsAnnotation: "true",
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							GenerateID: "123456789",
							Credentials: &hyperv1.KubevirtPlatformCredentials{
								InfraNamespace: "kubevirt-kubevirt",
								InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{
									Name: "secret",
									Key:  "key",
								},
							},
						},
					},
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{
							AdvertiseAddress: ptr.To("1.2.3.4"),
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.LoadBalancer,
							},
						},
					},
				},
			},
			secretName:     "kubevirt" + etcdEncKeyPostfix,
			secretExpected: true,
			objects: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "kubevirt" + etcdEncKeyPostfix, Namespace: "test"},
				},
			},
		},
	} {
		t.Run(testCase.name, func(tt *testing.T) {
			mockCtrl := gomock.NewController(t)
			testCase.objects = append(testCase.objects, testCase.hc)
			infra := &configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: configv1.InfrastructureStatus{
					PlatformStatus: &configv1.PlatformStatus{
						Type: configv1.KubevirtPlatformType,
					},
				},
			}
			testCase.objects = append(testCase.objects, infra)
			client := &createTypeTrackingClient{Client: fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(testCase.objects...).
				WithStatusSubresource(&hyperv1.HostedCluster{}).
				Build()}
			mockedProviderWithOpenShiftImageRegistryOverrides := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(mockCtrl)
			mockedProviderWithOpenShiftImageRegistryOverrides.EXPECT().
				Lookup(context.Background(), gomock.Any(), gomock.Any()).Return(testutils.InitReleaseImageOrDie("4.15.0"), nil).AnyTimes()
			r := &HostedClusterReconciler{
				Client:            client,
				Clock:             clock.RealClock{},
				CertRotationScale: 24 * time.Hour,
				ManagementClusterCapabilities: fakecapabilities.NewSupportAllExcept(
					capabilities.CapabilityInfrastructure,
					capabilities.CapabilityIngress,
					capabilities.CapabilityProxy,
				),
				createOrUpdate: func(reconcile.Request) upsert.CreateOrUpdateFN { return ctrl.CreateOrUpdate },
				ReconcileMetadataProviders: func(ctx context.Context, imgOverrides map[string]string) (releaseinfo.ProviderWithOpenShiftImageRegistryOverrides, hyperutil.ImageMetadataProvider, error) {
					return mockedProviderWithOpenShiftImageRegistryOverrides,
						&fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{}}, nil
				},
				now: metav1.Now,
			}

			if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: testCase.hc.Namespace, Name: testCase.hc.Name}}); err != nil {
				tt.Fatalf("Reconcile failed: %v", err)
			}

			if testCase.secretExpected {
				secList := &corev1.SecretList{}
				err := client.List(context.Background(), secList)
				if err != nil {
					tt.Fatalf("should create etcd encryptiuon key secret, but no secret found")
				}

				if numSec := len(secList.Items); numSec != 1 {
					tt.Fatalf("should create 1 secret, but found %d", numSec)
				}

				sec := secList.Items[0]
				if sec.Name != testCase.secretName {
					tt.Errorf("secret should be with name of %q, but it's %q", testCase.secretName, secList.Items[0].Name)
				}

				if _, keyExist := sec.Data[hyperv1.AESCBCKeySecretKey]; !keyExist {
					tt.Errorf("the secret should contain the %q key", hyperv1.AESCBCKeySecretKey)
				}
			}

			hcFromTest := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testCase.hc.Name,
					Namespace: testCase.hc.Namespace,
				},
			}

			err := client.Get(context.Background(), crclient.ObjectKeyFromObject(hcFromTest), hcFromTest)
			if err != nil {
				tt.Fatalf("should read the hosted cluster but got error; %v", err)
			}

			if hcFromTest.Spec.SecretEncryption == nil ||
				hcFromTest.Spec.SecretEncryption.Type != hyperv1.AESCBC ||
				hcFromTest.Spec.SecretEncryption.AESCBC == nil ||
				hcFromTest.Spec.SecretEncryption.AESCBC.ActiveKey.Name != testCase.secretName {

				tt.Errorf("wrong SecretEncryption %#v", hcFromTest.Spec.SecretEncryption)
			}
		},
		)
	}
}

func TestReconcileCAPIManagerDeployment(t *testing.T) {
	testCases := []struct {
		name              string
		version           string
		expectFeatureGate bool
	}{
		{
			name:              "version >= 4.19 should have --feature-gates=MachineSetPreflightChecks=true",
			version:           "4.19.0",
			expectFeatureGate: true,
		},
		{
			name:              "version <= 4.19 should not have --feature-gates=MachineSetPreflightChecks=true",
			version:           "4.18.0",
			expectFeatureGate: false,
		},
	}

	hcNamespace := "namespace"
	cpNamespace := "cp-namespace"
	image := "image"

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: hcNamespace,
				},
			}

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: cpNamespace,
				},
			}

			sa := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "capi-manager",
					Namespace: cpNamespace,
				},
			}

			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "capi-manager",
					Namespace: cpNamespace,
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{},
					},
				},
			}

			semVersion, err := semver.Parse(tc.version)
			g.Expect(err).ToNot(HaveOccurred())

			err = reconcileCAPIManagerDeployment(deployment, hc, hcp, sa, image, true, &semVersion)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
			// Check for feature gate argument
			gotFeatureGate := false
			for _, arg := range deployment.Spec.Template.Spec.Containers[0].Args {
				if arg == "--feature-gates=MachineSetPreflightChecks=false" {
					gotFeatureGate = true
					break
				}
			}

			g.Expect(gotFeatureGate).To(Equal(tc.expectFeatureGate), "Feature gate presence doesn't match expectation")

			// Verify other expected configurations
			g.Expect(deployment.Spec.Template.Spec.ServiceAccountName).To(Equal(sa.Name))
			g.Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal(image))
		})
	}
}
