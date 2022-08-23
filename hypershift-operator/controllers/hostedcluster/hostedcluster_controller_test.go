package hostedcluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/blang/semver"
	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	platformaws "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/aws"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/kubevirt"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/capabilities"
	fakecapabilities "github.com/openshift/hypershift/support/capabilities/fake"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	errors2 "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	serializerjson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/pointer"
	capiawsv1 "sigs.k8s.io/cluster-api-provider-aws/api/v1beta1"
	capibmv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta1"
	"sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var Now = metav1.NewTime(time.Now())
var Later = metav1.NewTime(Now.Add(5 * time.Minute))

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
						Desired: hyperv1.Release{Image: "a"},
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
						Desired: hyperv1.Release{Image: "a"},
						History: []configv1.UpdateHistory{
							{Image: "a", State: configv1.CompletedUpdate},
						},
					},
				},
			},
			ControlPlane: hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: Now},
				Spec:       hyperv1.HostedControlPlaneSpec{ReleaseImage: "a"},
				Status:     hyperv1.HostedControlPlaneStatus{ReleaseImage: "a"},
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
						Desired: hyperv1.Release{Image: "b"},
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
				Status:     hyperv1.HostedControlPlaneStatus{ReleaseImage: "a"},
			},
			ExpectedImage: "b",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			updated := test.ControlPlane.DeepCopy()
			err := reconcileHostedControlPlane(updated, &test.Cluster)
			if err != nil {
				t.Error(err)
			}
			actualImage := updated.Spec.ReleaseImage
			if !equality.Semantic.DeepEqual(test.ExpectedImage, actualImage) {
				t.Errorf(cmp.Diff(test.ExpectedImage, actualImage))
			}
		})
	}
}

func TestComputeClusterVersionStatus(t *testing.T) {
	tests := map[string]struct {
		// TODO: incorporate conditions?
		Cluster        hyperv1.HostedCluster
		ControlPlane   hyperv1.HostedControlPlane
		ExpectedStatus hyperv1.ClusterVersionStatus
	}{
		"missing history causes new rollout": {
			Cluster: hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{Release: hyperv1.Release{Image: "a"}},
			},
			ControlPlane: hyperv1.HostedControlPlane{
				Spec:   hyperv1.HostedControlPlaneSpec{ReleaseImage: "a"},
				Status: hyperv1.HostedControlPlaneStatus{},
			},
			ExpectedStatus: hyperv1.ClusterVersionStatus{
				Desired: hyperv1.Release{Image: "a"},
				History: []configv1.UpdateHistory{
					{Image: "a", State: configv1.PartialUpdate, StartedTime: Now},
				},
			},
		},
		"hosted cluster spec is newer than completed control plane spec should not cause update to be completed": {
			Cluster: hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{Release: hyperv1.Release{Image: "b"}},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: hyperv1.Release{Image: "b"},
						History: []configv1.UpdateHistory{
							{Image: "b", Version: "", State: configv1.PartialUpdate, StartedTime: Now},
							{Image: "a", Version: "1.0.0", State: configv1.CompletedUpdate, StartedTime: Now, CompletionTime: &Later},
						},
					},
				},
			},
			ControlPlane: hyperv1.HostedControlPlane{
				Spec:   hyperv1.HostedControlPlaneSpec{ReleaseImage: "a"},
				Status: hyperv1.HostedControlPlaneStatus{ReleaseImage: "a", Version: "1.0.0", LastReleaseImageTransitionTime: &Now},
			},
			ExpectedStatus: hyperv1.ClusterVersionStatus{
				Desired: hyperv1.Release{Image: "b"},
				History: []configv1.UpdateHistory{
					{Image: "b", Version: "", State: configv1.PartialUpdate, StartedTime: Now},
					{Image: "a", Version: "1.0.0", State: configv1.CompletedUpdate, StartedTime: Now, CompletionTime: &Later},
				},
			},
		},
		"completed rollout updates history": {
			Cluster: hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{Release: hyperv1.Release{Image: "a"}},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: hyperv1.Release{Image: "a"},
						History: []configv1.UpdateHistory{
							{Image: "a", State: configv1.PartialUpdate, StartedTime: Now},
						},
					},
				},
			},
			ControlPlane: hyperv1.HostedControlPlane{
				Spec:   hyperv1.HostedControlPlaneSpec{ReleaseImage: "a"},
				Status: hyperv1.HostedControlPlaneStatus{ReleaseImage: "a", Version: "1.0.0", LastReleaseImageTransitionTime: &Later},
			},
			ExpectedStatus: hyperv1.ClusterVersionStatus{
				Desired: hyperv1.Release{Image: "a"},
				History: []configv1.UpdateHistory{
					{Image: "a", Version: "1.0.0", State: configv1.CompletedUpdate, StartedTime: Now, CompletionTime: &Later},
				},
			},
		},
		"new rollout happens after existing rollout completes": {
			Cluster: hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{Release: hyperv1.Release{Image: "b"}},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: hyperv1.Release{Image: "a"},
						History: []configv1.UpdateHistory{
							{Image: "a", State: configv1.CompletedUpdate, StartedTime: Now, CompletionTime: &Later},
						},
					},
				},
			},
			ControlPlane: hyperv1.HostedControlPlane{
				Spec:   hyperv1.HostedControlPlaneSpec{ReleaseImage: "a"},
				Status: hyperv1.HostedControlPlaneStatus{ReleaseImage: "a", Version: "1.0.0", LastReleaseImageTransitionTime: &Later},
			},
			ExpectedStatus: hyperv1.ClusterVersionStatus{
				Desired: hyperv1.Release{Image: "b"},
				History: []configv1.UpdateHistory{
					{Image: "b", State: configv1.PartialUpdate, StartedTime: Now},
					{Image: "a", Version: "1.0.0", State: configv1.CompletedUpdate, StartedTime: Now, CompletionTime: &Later},
				},
			},
		},
		"new rollout is deferred until existing rollout completes": {
			Cluster: hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{Release: hyperv1.Release{Image: "b"}},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: hyperv1.Release{Image: "a"},
						History: []configv1.UpdateHistory{
							{Image: "a", State: configv1.PartialUpdate, StartedTime: Now},
						},
					},
				},
			},
			ControlPlane: hyperv1.HostedControlPlane{
				Spec:   hyperv1.HostedControlPlaneSpec{ReleaseImage: "a"},
				Status: hyperv1.HostedControlPlaneStatus{},
			},
			ExpectedStatus: hyperv1.ClusterVersionStatus{
				Desired: hyperv1.Release{Image: "a"},
				History: []configv1.UpdateHistory{
					{Image: "a", State: configv1.PartialUpdate, StartedTime: Now},
				},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			actualStatus := computeClusterVersionStatus(clocktesting.NewFakeClock(Now.Time), &test.Cluster, &test.ControlPlane)
			if !equality.Semantic.DeepEqual(&test.ExpectedStatus, actualStatus) {
				t.Errorf(cmp.Diff(&test.ExpectedStatus, actualStatus))
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
						{Type: string(hyperv1.HostedControlPlaneAvailable), Status: metav1.ConditionFalse},
					},
				},
			},
			ExpectedCondition: metav1.Condition{
				Type:   string(hyperv1.HostedClusterAvailable),
				Status: metav1.ConditionFalse,
			},
		},
		"should be available": {
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
						{Type: string(hyperv1.HostedControlPlaneAvailable), Status: metav1.ConditionTrue},
					},
				},
			},
			ExpectedCondition: metav1.Condition{
				Type:   string(hyperv1.HostedClusterAvailable),
				Status: metav1.ConditionTrue,
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			actualCondition := computeHostedClusterAvailability(&test.Cluster, test.ControlPlane)
			// Clear fields irrelevant for diffing
			actualCondition.ObservedGeneration = 0
			actualCondition.Reason = ""
			actualCondition.Message = ""
			if !equality.Semantic.DeepEqual(test.ExpectedCondition, actualCondition) {
				t.Errorf(cmp.Diff(test.ExpectedCondition, actualCondition))
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
				AdvertiseAddress: pointer.StringPtr("1.2.3.4"),
			},
			expectedAPIAdvertiseAddress: pointer.StringPtr("1.2.3.4"),
		},
		{
			name: "port specified",
			networking: &hyperv1.APIServerNetworking{
				Port: pointer.Int32Ptr(1234),
			},
			expectedAPIPort: pointer.Int32Ptr(1234),
		},
		{
			name: "both specified",
			networking: &hyperv1.APIServerNetworking{
				Port:             pointer.Int32Ptr(6789),
				AdvertiseAddress: pointer.StringPtr("9.8.7.6"),
			},
			expectedAPIPort:             pointer.Int32Ptr(6789),
			expectedAPIAdvertiseAddress: pointer.StringPtr("9.8.7.6"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hostedCluster := &hyperv1.HostedCluster{}
			hostedCluster.Spec.Networking.APIServer = test.networking
			hostedControlPlane := &hyperv1.HostedControlPlane{}
			err := reconcileHostedControlPlane(hostedControlPlane, hostedCluster)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			g := NewGomegaWithT(t)
			if test.networking != nil {
				// deprecated values should still be populated
				g.Expect(hostedControlPlane.Spec.APIPort).To(Equal(test.expectedAPIPort))
				g.Expect(hostedControlPlane.Spec.APIAdvertiseAddress).To(Equal(test.expectedAPIAdvertiseAddress))

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
						HostedClusterAnnotation: "master/cluster1",
					},
					Namespace: "master-cluster1",
					Name:      "cluster1",
				},
				Spec: v1beta1.ClusterSpec{
					ControlPlaneEndpoint: v1beta1.APIEndpoint{},
					ControlPlaneRef: &corev1.ObjectReference{
						APIVersion: "hypershift.openshift.io/v1alpha1",
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
			infraCR: &capiawsv1.AWSCluster{
				TypeMeta: metav1.TypeMeta{
					Kind:       "AWSCluster",
					APIVersion: capiawsv1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster1",
					Namespace: "master-cluster1",
				},
			},
			expectedCAPICluster: &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						HostedClusterAnnotation: "master/cluster1",
					},
					Namespace: "master-cluster1",
					Name:      "cluster1",
				},
				Spec: v1beta1.ClusterSpec{
					ControlPlaneEndpoint: v1beta1.APIEndpoint{},
					ControlPlaneRef: &corev1.ObjectReference{
						APIVersion: "hypershift.openshift.io/v1alpha1",
						Kind:       "HostedControlPlane",
						Namespace:  "master-cluster1",
						Name:       "cluster1",
					},
					InfrastructureRef: &corev1.ObjectReference{
						APIVersion: capiawsv1.GroupVersion.String(),
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
				t.Errorf("reconciled CAPI cluster differs from expcted CAPI cluster: %s", diff)
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
				Client: client,
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

	hostedClusters := []*hyperv1.HostedCluster{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "agent"},
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{
					Type:  hyperv1.AgentPlatform,
					Agent: &hyperv1.AgentPlatformSpec{AgentNamespace: "agent-namespace"},
				},
			},
			Status: hyperv1.HostedClusterStatus{
				IgnitionEndpoint: "ign",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "aws"},
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
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "none"},
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.NonePlatform,
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "ibm"},
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{
					Type:     hyperv1.IBMCloudPlatform,
					IBMCloud: &hyperv1.IBMCloudPlatformSpec{},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "kubevirt"},
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.KubevirtPlatform,
				},
			},
		},
	}

	objects := []crclient.Object{
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "secret",
			},
			Data: map[string][]byte{
				"credentials":       []byte("creds"),
				".dockerconfigjson": []byte("{}"),
			},
		},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "agent-namespace"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "agent"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aws"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "none"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ibm"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kubevirt"}},
	}
	for _, cluster := range hostedClusters {
		cluster.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
			{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
			{Service: hyperv1.Konnectivity, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}},
			{Service: hyperv1.OAuthServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}},
			{Service: hyperv1.OIDC, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.None}},
			{Service: hyperv1.Ignition, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}},
		}
		cluster.Spec.PullSecret = corev1.LocalObjectReference{Name: "secret"}
		cluster.Spec.InfraID = "infra-id"
		objects = append(objects, cluster)
	}

	client := &createTypeTrackingClient{Client: fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build()}
	r := &HostedClusterReconciler{
		Client: client,
		Clock:  clock.RealClock{},
		ManagementClusterCapabilities: fakecapabilities.NewSupportAllExcept(
			capabilities.CapabilityInfrastructure,
			capabilities.CapabilityIngress,
			capabilities.CapabilityProxy,
		),
		createOrUpdate:        func(reconcile.Request) upsert.CreateOrUpdateFN { return ctrl.CreateOrUpdate },
		ReleaseProvider:       &fakereleaseprovider.FakeReleaseProvider{},
		ImageMetadataProvider: &fakeimagemetadataprovider.FakeImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{}},
		now:                   metav1.Now,
	}

	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))

	for _, hc := range hostedClusters {
		t.Run(hc.Name, func(t *testing.T) {
			if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: hc.Namespace, Name: hc.Name}}); err != nil {
				t.Fatalf("Reconcile failed: %v", err)
			}
		})
	}
	watchedResources := sets.String{}
	for _, resource := range r.managedResources() {
		watchedResources.Insert(fmt.Sprintf("%T", resource))
	}
	if diff := cmp.Diff(client.createdTypes.List(), watchedResources.List()); diff != "" {
		t.Errorf("the set of resources that are being created differs from the one that is being watched: %s", diff)
	}
}

type createTypeTrackingClient struct {
	crclient.Client
	createdTypes sets.String
}

func (c *createTypeTrackingClient) Create(ctx context.Context, obj crclient.Object, opts ...crclient.CreateOption) error {
	if c.createdTypes == nil {
		c.createdTypes = sets.String{}
	}
	c.createdTypes.Insert(fmt.Sprintf("%T", obj))
	return c.Client.Create(ctx, obj, opts...)
}

func TestReconcileAWSSubnets(t *testing.T) {
	g := NewGomegaWithT(t)
	hcNamespace := "test"
	hcName := "test"
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: hcNamespace,
		},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: hcName,
			Platform: hyperv1.NodePoolPlatform{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSNodePoolPlatform{
					Subnet: &hyperv1.AWSResourceReference{
						ID: pointer.StringPtr("1"),
					},
				},
			},
		},
	}

	nodePool2 := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test2",
			Namespace: hcNamespace,
		},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: hcName,
			Platform: hyperv1.NodePoolPlatform{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSNodePoolPlatform{
					Subnet: &hyperv1.AWSResourceReference{
						ID: pointer.StringPtr("2"),
					},
				},
			},
		},
	}

	infraCRName := "test"
	hcpNamespace := "hcp"
	infraCR := &capiawsv1.AWSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      infraCRName,
			Namespace: hcpNamespace,
		},
		Spec: capiawsv1.AWSClusterSpec{},
	}

	client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(infraCR, nodePool, nodePool2).Build()
	r := &HostedClusterReconciler{
		Client:         client,
		createOrUpdate: func(reconcile.Request) upsert.CreateOrUpdateFN { return ctrl.CreateOrUpdate },
	}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: hcNamespace, Name: hcName}}
	createOrUpdate := r.createOrUpdate(req)

	err := r.reconcileAWSSubnets(context.Background(), createOrUpdate, infraCR, req.Namespace, req.Name, hcpNamespace)
	g.Expect(err).ToNot(HaveOccurred())

	freshInfraCR := &capiawsv1.AWSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      infraCRName,
			Namespace: hcpNamespace,
		}}
	err = client.Get(context.Background(), crclient.ObjectKeyFromObject(freshInfraCR), freshInfraCR)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(freshInfraCR.Spec.NetworkSpec.Subnets).To(BeEquivalentTo([]capiawsv1.SubnetSpec{
		{
			ID: "1",
		},
		{
			ID: "2",
		},
	}))
}

func TestValidateConfigAndClusterCapabilities(t *testing.T) {
	testCases := []struct {
		name                          string
		hostedCluster                 *hyperv1.HostedCluster
		other                         []crclient.Object
		managementClusterCapabilities capabilities.CapabiltyChecker
		expectedResult                error
	}{
		{
			name: "Cluster uses route but not supported, error",
			hostedCluster: &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{
				Services: []hyperv1.ServicePublishingStrategyMapping{
					{ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
						Type: hyperv1.Route,
					}},
				},
			}},
			managementClusterCapabilities: &fakecapabilities.FakeSupportNoCapabilities{},
			expectedResult:                errors.New(`cluster does not support Routes, but service "" is exposed via a Route`),
		},
		{
			name: "Cluster uses routes and supported, success",
			hostedCluster: &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{
				Services: []hyperv1.ServicePublishingStrategyMapping{
					{ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
						Type: hyperv1.Route,
					}},
				},
			}},
			managementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
		},
		{
			name: "Azurecluster with incomplete credentials secret, error",
			hostedCluster: &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AzurePlatform,
				Azure: &hyperv1.AzurePlatformSpec{
					Credentials: corev1.LocalObjectReference{Name: "creds"},
				},
			}}},
			other: []crclient.Object{
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "creds"}},
			},
			expectedResult: errors.New(`[credentials secret for cluster doesn't have required key AZURE_CLIENT_ID, credentials secret for cluster doesn't have required key AZURE_CLIENT_SECRET, credentials secret for cluster doesn't have required key AZURE_SUBSCRIPTION_ID, credentials secret for cluster doesn't have required key AZURE_TENANT_ID]`),
		},
		{
			name: "Azurecluster with complete credentials secret, success",
			hostedCluster: &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AzurePlatform,
				Azure: &hyperv1.AzurePlatformSpec{
					Credentials: corev1.LocalObjectReference{Name: "creds"},
				},
			}}},
			other: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "creds"},
					Data: map[string][]byte{
						"AZURE_CLIENT_ID":       nil,
						"AZURE_CLIENT_SECRET":   nil,
						"AZURE_SUBSCRIPTION_ID": nil,
						"AZURE_TENANT_ID":       nil,
					},
				},
			},
		},
		{
			name: "invalid cluster uuid",
			hostedCluster: &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{
				ClusterID: "foobar",
			}},
			expectedResult: errors.New(`cannot parse cluster ID "foobar": invalid UUID length: 6`),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := &HostedClusterReconciler{
				Client:                        fake.NewClientBuilder().WithObjects(tc.other...).Build(),
				ManagementClusterCapabilities: tc.managementClusterCapabilities,
			}

			ctx := context.Background()
			actual := r.validateConfigAndClusterCapabilities(ctx, tc.hostedCluster)
			if diff := cmp.Diff(actual, tc.expectedResult, equateErrorMessage); diff != "" {
				t.Errorf("actual validation result differs from expected: %s", diff)
			}
		})
	}
}

func TestValidateReleaseImage(t *testing.T) {
	testCases := []struct {
		name           string
		other          []crclient.Object
		hostedCluster  *hyperv1.HostedCluster
		expectedResult error
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
			expectedResult: errors.New("failed to get pull secret: secrets \"pull-secret\" not found"),
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
			expectedResult: errors.New("releases before 4.8 are not supported"),
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
						Image: "image-4.10.0",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: hyperv1.Release{
							Image: "image-4.11.0",
						},
					},
				},
			},
			expectedResult: errors.New("y-stream downgrade is not supported"),
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
						Image: "image-4.11.0",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: hyperv1.Release{
							Image: "image-4.10.0",
						},
					},
				},
			},
			expectedResult: errors.New("y-stream upgrade is not for OpenShiftSDN"),
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
						Image: "image-4.11.0",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: hyperv1.Release{
							Image: "image-4.10.0",
						},
					},
				},
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
						NetworkType: hyperv1.OpenShiftSDN,
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: hyperv1.Release{
						Image: "image-4.10.0",
					},
				},
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
						NetworkType: hyperv1.OpenShiftSDN,
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: hyperv1.Release{
						Image: "image-4.10.0",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: hyperv1.Release{
							Image: "image-4.10.0",
						},
					},
				},
			},
			expectedResult: nil,
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
						Image: "image-4.10.0",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: hyperv1.Release{
							Image: "image-4.10.1",
						},
					},
				},
			},
			expectedResult: nil,
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
						Image: "image-4.12.0",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: hyperv1.Release{
							Image: "image-4.11.0",
						},
					},
				},
			},
			expectedResult: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := &HostedClusterReconciler{
				Client: fake.NewClientBuilder().WithObjects(tc.other...).Build(),
				ReleaseProvider: &fakereleaseprovider.FakeReleaseProvider{
					ImageVersion: map[string]string{
						"image-4.7.0":  "4.7.0",
						"image-4.9.0":  "4.9.0",
						"image-4.10.0": "4.10.0",
						"image-4.10.1": "4.10.1",
						"image-4.11.0": "4.11.0",
						"image-4.12.0": "4.12.0",
					},
				},
			}

			ctx := context.Background()
			actual := r.validateReleaseImage(ctx, tc.hostedCluster)
			if diff := cmp.Diff(actual, tc.expectedResult, equateErrorMessage); diff != "" {
				t.Errorf("actual validation result differs from expected: %s", diff)
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
				Client: fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(test.hc).Build(),
			}
			g := NewGomegaWithT(t)
			previousInfraID := test.hc.Spec.InfraID
			previousClusterID := test.hc.Spec.ClusterID
			err := r.defaultClusterIDsIfNeeded(context.Background(), test.hc)
			g.Expect(err).ToNot(HaveOccurred())
			resultHC := &hyperv1.HostedCluster{}
			r.Client.Get(context.Background(), crclient.ObjectKeyFromObject(test.hc), resultHC)
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

func TestConfigurationFieldsToRawExtensions(t *testing.T) {
	config := &hyperv1.ClusterConfiguration{
		Ingress: &configv1.IngressSpec{Domain: "example.com"},
		Proxy:   &configv1.ProxySpec{HTTPProxy: "http://10.0.136.57:3128", HTTPSProxy: "http://10.0.136.57:3128"},
	}
	result, err := configurationFieldsToRawExtensions(config)
	if err != nil {
		t.Fatalf("configurationFieldsToRawExtensions: %v", err)
	}

	// Check that serialized resources do not contain a status section
	for i, rawExt := range result {
		unstructuredObj := &unstructured.Unstructured{}
		_, _, err := unstructured.UnstructuredJSONScheme.Decode(rawExt.Raw, nil, unstructuredObj)
		if err != nil {
			t.Fatalf("unexpected decode error: %v", err)
		}
		_, exists, err := unstructured.NestedFieldNoCopy(unstructuredObj.Object, "status")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exists {
			t.Errorf("status field exists for resource %d", i)
		}
	}

	serialized, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var roundtripped []runtime.RawExtension
	if err := json.Unmarshal(serialized, &roundtripped); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	// CreateOrUpdate does a naive DeepEqual which can not deal with custom unmarshallers, so make
	// sure the output matches a roundtripped result.
	if diff := cmp.Diff(result, roundtripped); diff != "" {
		t.Errorf("output does not match a json-roundtripped version: %s", diff)
	}

	var ingress configv1.Ingress
	if err := json.Unmarshal(result[0].Raw, &ingress); err != nil {
		t.Fatalf("failed to unmarshal raw data: %v", err)
	}
	if ingress.APIVersion == "" || ingress.Kind == "" {
		t.Errorf("rawObject has no apiVersion or kind set: %+v", ingress.ObjectMeta)
	}
	if ingress.Spec.Domain != "example.com" {
		t.Errorf("ingress does not have expected domain: %q", ingress.Spec.Domain)
	}

	var proxy configv1.Proxy
	if err := json.Unmarshal(result[1].Raw, &proxy); err != nil {
		t.Fatalf("failed to unmarshal raw data: %v", err)
	}
	if proxy.APIVersion == "" || proxy.Kind == "" {
		t.Errorf("rawObject has no apiVersion or kind set: %+v", proxy.ObjectMeta)
	}
	if proxy.Spec.HTTPProxy != "http://10.0.136.57:3128" {
		t.Errorf("proxy does not have expected HTTPProxy: %q", proxy.Spec.HTTPProxy)
	}

}

func TestIsUpgradeable(t *testing.T) {
	releaseImageFrom := "image:1.2"
	releaseImageTo := "image:1.3"
	tests := []struct {
		name      string
		hc        *hyperv1.HostedCluster
		upgrading bool
		err       bool
	}{
		{
			name: "version not reported yet",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: releaseImageFrom,
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: nil,
				},
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
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: hyperv1.Release{
							Image: releaseImageFrom,
						},
					},
				},
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
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: hyperv1.Release{
							Image: releaseImageFrom,
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
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: hyperv1.Release{
							Image: releaseImageFrom,
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
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: hyperv1.Release{
							Image: releaseImageFrom,
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
			upgrading: true,
			err:       false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upgrading, _, err := isUpgradeable(test.hc)
			if upgrading != test.upgrading {
				t.Errorf("isUpgradeable() upgrading = %v, want %v", upgrading, test.upgrading)
			}
			if (err == nil) == test.err {
				t.Errorf("isUpgradeable() err = %v, want %v", (err == nil), test.err)
				return
			}
		})
	}
}

func TestReconcileDeprecatedAWSRoles(t *testing.T) {
	testNamespace := "test"

	// Emulate user input secrets pre-created by the CLI.
	kubeCloudControllerARN := "kubeCloudControllerARN"
	kubeCloudControllerSecretName := "kubeCloudControllerCreds"
	kubeCloudControllerSecret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      kubeCloudControllerSecretName,
		},
		Data: map[string][]byte{
			"credentials": []byte(fmt.Sprintf(`[default]
role_arn = %s
web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token
`, kubeCloudControllerARN)),
		},
	}

	nodePoolManagementARN := "nodePoolManagementARN"
	nodePoolManagementSecretName := "nodePoolManagementCreds"
	nodePoolManagementSecret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      nodePoolManagementSecretName,
		},
		Data: map[string][]byte{
			"credentials": []byte(fmt.Sprintf(`[default]
role_arn = %s
web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token
`, nodePoolManagementARN)),
		},
	}

	controlPlaneOperatorARN := "controlPlaneOperatorARN"
	controlPlaneOperatorSecretName := "controlPlaneOperatorCreds"
	controlPlaneOperatorSecret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      controlPlaneOperatorSecretName,
		},
		Data: map[string][]byte{
			"credentials": []byte(fmt.Sprintf(`[default]
role_arn = %s
web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token
`, controlPlaneOperatorARN)),
		},
	}

	// Emulate user input.
	ingressARN := "ingressARN"
	imageRegistryARN := "registryARN"
	storageARN := "ebsARN"
	networkARN := "networkARN"

	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "",
			Namespace: testNamespace,
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					RolesRef: hyperv1.AWSRolesRef{
						IngressARN:              "",
						ImageRegistryARN:        "",
						StorageARN:              "",
						NetworkARN:              "",
						KubeCloudControllerARN:  "",
						NodePoolManagementARN:   "",
						ControlPlaneOperatorARN: "",
					},
					Roles: []hyperv1.AWSRoleCredentials{
						{
							ARN:       ingressARN,
							Namespace: "openshift-ingress-operator",
							Name:      "cloud-credentials",
						},
						{
							ARN:       imageRegistryARN,
							Namespace: "openshift-image-registry",
							Name:      "installer-cloud-credentials",
						},
						{
							ARN:       storageARN,
							Namespace: "openshift-cluster-csi-drivers",
							Name:      "ebs-cloud-credentials",
						},
						{
							ARN:       networkARN,
							Namespace: "openshift-cloud-network-config-controller",
							Name:      "cloud-credentials",
						},
					},
					KubeCloudControllerCreds:  corev1.LocalObjectReference{Name: kubeCloudControllerSecretName},
					NodePoolManagementCreds:   corev1.LocalObjectReference{Name: nodePoolManagementSecretName},
					ControlPlaneOperatorCreds: corev1.LocalObjectReference{Name: controlPlaneOperatorSecretName},
				},
			},
		},
		Status: hyperv1.HostedClusterStatus{},
	}

	// Expect old fields to be migrated.
	expectedAWSPlatformSpec := &hyperv1.AWSPlatformSpec{
		Region:              "",
		CloudProviderConfig: nil,
		ServiceEndpoints:    nil,
		RolesRef: hyperv1.AWSRolesRef{
			IngressARN:              ingressARN,
			ImageRegistryARN:        imageRegistryARN,
			StorageARN:              storageARN,
			NetworkARN:              networkARN,
			KubeCloudControllerARN:  kubeCloudControllerARN,
			NodePoolManagementARN:   nodePoolManagementARN,
			ControlPlaneOperatorARN: controlPlaneOperatorARN,
		},
		Roles:                     nil,
		KubeCloudControllerCreds:  corev1.LocalObjectReference{},
		NodePoolManagementCreds:   corev1.LocalObjectReference{},
		ControlPlaneOperatorCreds: corev1.LocalObjectReference{},
		ResourceTags:              nil,
		EndpointAccess:            "",
	}

	client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(controlPlaneOperatorSecret, nodePoolManagementSecret, kubeCloudControllerSecret).Build()
	r := &HostedClusterReconciler{Client: client}

	g := NewGomegaWithT(t)
	err := r.reconcileDeprecatedAWSRoles(context.Background(), hc)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(hc.Spec.Platform.AWS).To(BeEquivalentTo(expectedAWSPlatformSpec))
}

func TestEnsureHCPAWSRolesBackwardCompatibility(t *testing.T) {
	ingressARN := "ingressARN"
	imageRegistryARN := "imageRegistryARN"
	storageARN := "storageARN"
	networkARN := "networkARN"

	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "",
			Namespace: "",
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					RolesRef: hyperv1.AWSRolesRef{
						IngressARN:              ingressARN,
						ImageRegistryARN:        imageRegistryARN,
						StorageARN:              storageARN,
						NetworkARN:              networkARN,
						KubeCloudControllerARN:  "anything",
						NodePoolManagementARN:   "anything",
						ControlPlaneOperatorARN: "anything",
					},
				},
			},
		},
	}

	expectedAWSPlatformSpec := &hyperv1.AWSPlatformSpec{
		Region:              "",
		CloudProviderConfig: nil,
		ServiceEndpoints:    nil,
		RolesRef: hyperv1.AWSRolesRef{
			IngressARN:              ingressARN,
			ImageRegistryARN:        imageRegistryARN,
			StorageARN:              storageARN,
			NetworkARN:              networkARN,
			KubeCloudControllerARN:  "anything",
			NodePoolManagementARN:   "anything",
			ControlPlaneOperatorARN: "anything",
		},
		Roles: []hyperv1.AWSRoleCredentials{
			{
				ARN:       ingressARN,
				Namespace: "openshift-ingress-operator",
				Name:      "cloud-credentials",
			},
			{
				ARN:       imageRegistryARN,
				Namespace: "openshift-image-registry",
				Name:      "installer-cloud-credentials",
			},
			{
				ARN:       storageARN,
				Namespace: "openshift-cluster-csi-drivers",
				Name:      "ebs-cloud-credentials",
			},
			{
				ARN:       networkARN,
				Namespace: "openshift-cloud-network-config-controller",
				Name:      "cloud-credentials",
			},
		},
		KubeCloudControllerCreds:  corev1.LocalObjectReference{Name: platformaws.KubeCloudControllerCredsSecret("").Name},
		NodePoolManagementCreds:   corev1.LocalObjectReference{},
		ControlPlaneOperatorCreds: corev1.LocalObjectReference{},
		ResourceTags:              nil,
		EndpointAccess:            "",
	}

	g := NewGomegaWithT(t)
	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					Region:                    "",
					CloudProviderConfig:       nil,
					ServiceEndpoints:          nil,
					RolesRef:                  hyperv1.AWSRolesRef{},
					Roles:                     nil,
					KubeCloudControllerCreds:  corev1.LocalObjectReference{},
					NodePoolManagementCreds:   corev1.LocalObjectReference{},
					ControlPlaneOperatorCreds: corev1.LocalObjectReference{},
					ResourceTags:              nil,
					EndpointAccess:            "",
				},
			},
		},
	}
	ensureHCPAWSRolesBackwardCompatibility(hc, hcp)
	g.Expect(hcp.Spec.Platform.AWS).To(BeEquivalentTo(expectedAWSPlatformSpec))
}

func TestReconcileDeprecatedGlobalConfig(t *testing.T) {
	hc := &hyperv1.HostedCluster{}
	hc.Name = "fake-name"
	hc.Namespace = "fake-namespace"

	apiServer := &configv1.APIServer{
		TypeMeta: metav1.TypeMeta{
			APIVersion: configv1.SchemeGroupVersion.String(),
			Kind:       "APIServer",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			Audit: configv1.Audit{
				// Populate kubebuilder default for comparison
				// https://github.com/openshift/api/blob/f120778bee805ad1a7a4f05a6430332cf5811813/config/v1/types_apiserver.go#L57
				Profile: configv1.DefaultAuditProfileType,
			},
			ClientCA: configv1.ConfigMapNameReference{
				Name: "fake-ca",
			},
		},
	}

	jsonSerializer := serializerjson.NewSerializerWithOptions(
		serializerjson.DefaultMetaFactory, hyperapi.Scheme, hyperapi.Scheme,
		serializerjson.SerializerOptions{Yaml: false, Pretty: true, Strict: false},
	)

	serializedAPIServer := &bytes.Buffer{}
	err := jsonSerializer.Encode(apiServer, serializedAPIServer)
	if err != nil {
		t.Fatalf("failed to serialize apiserver: %v", err)
	}

	hc.Spec.Configuration = &hyperv1.ClusterConfiguration{
		Items: []runtime.RawExtension{
			{
				Raw: serializedAPIServer.Bytes(),
			},
		},
		ConfigMapRefs: []corev1.LocalObjectReference{
			{
				Name: "fake-ca",
			},
		},
		SecretRefs: []corev1.LocalObjectReference{
			{
				Name: "fake-creds",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(hyperapi.Scheme).
		WithObjects(hc).
		Build()
	reconciler := &HostedClusterReconciler{
		Client: fakeClient,
	}

	originalSpec := hc.Spec.DeepCopy()
	if err := reconciler.reconcileDeprecatedGlobalConfig(context.Background(), hc); err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}

	// Update fields if required.
	if !equality.Semantic.DeepEqual(&hc.Spec, originalSpec) {
		err := reconciler.Client.Update(context.Background(), hc)
		if err != nil {
			t.Fatalf("unexpected update error: %v", err)
		}
	}

	updatedHc := &hyperv1.HostedCluster{}
	if err := fakeClient.Get(context.Background(), crclient.ObjectKeyFromObject(hc), updatedHc); err != nil {
		t.Fatalf("unexpected get error: %v", err)
	}
	if updatedHc.Spec.Configuration == nil {
		t.Fatalf("unexpected nil configuration")
	}
	if len(updatedHc.Spec.Configuration.Items) == 0 {
		t.Errorf("empty deprecated configuration")
	}
	if len(updatedHc.Spec.Configuration.ConfigMapRefs) == 0 {
		t.Errorf("empty configmap refs")
	}
	if len(updatedHc.Spec.Configuration.SecretRefs) == 0 {
		t.Errorf("emtpy secret refs")
	}
	if !equality.Semantic.DeepEqual(&apiServer.Spec, updatedHc.Spec.Configuration.APIServer) {
		t.Errorf("unexpected apiserver spec: %#v", updatedHc.Spec.Configuration.APIServer)
	}

	// Update deprecated field, remove test when field is unsupported
	apiServer.Spec.ClientCA.Name = "updated-ca"
	serializedAPIServer.Reset()
	err = jsonSerializer.Encode(apiServer, serializedAPIServer)
	if err != nil {
		t.Fatalf("failed to serialize apiserver: %v", err)
	}
	updatedHc.Spec.Configuration.Items = []runtime.RawExtension{{Raw: serializedAPIServer.Bytes()}}
	if err := reconciler.reconcileDeprecatedGlobalConfig(context.Background(), updatedHc); err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}
	err = reconciler.Client.Update(context.Background(), updatedHc)
	if err != nil {
		t.Fatalf("unexpected update error: %v", err)
	}
	updatedHcAgain := &hyperv1.HostedCluster{}
	if err := fakeClient.Get(context.Background(), crclient.ObjectKeyFromObject(updatedHc), updatedHcAgain); err != nil {
		t.Fatalf("unexpected get error: %v", err)
	}
	if !equality.Semantic.DeepEqual(&apiServer.Spec, updatedHcAgain.Spec.Configuration.APIServer) {
		t.Errorf("unexpected apiserver spec on update: %#v", updatedHcAgain.Spec.Configuration.APIServer)
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
				Message:            "Reconciliation completed succesfully",
				LastTransitionTime: reconcilerNow,
			}},
		},
		{
			name: "Succcess, existing success condition transition timestamp stays",
			existingConditions: []metav1.Condition{{
				Type:               string(hyperv1.ReconciliationSucceeded),
				Status:             metav1.ConditionTrue,
				Message:            "Reconciliation completed succesfully",
				Reason:             "ReconciliatonSucceeded",
				LastTransitionTime: now,
			}},
			expectedConditions: []metav1.Condition{{
				Type:               string(hyperv1.ReconciliationSucceeded),
				Status:             metav1.ConditionTrue,
				Reason:             "ReconciliatonSucceeded",
				Message:            "Reconciliation completed succesfully",
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
				Message:            "Reconciliation completed succesfully",
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

			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcluster).Build()
			r := &HostedClusterReconciler{
				Client: c,
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
	tests := []struct {
		name    string
		hc      *hyperv1.HostedCluster
		want    bool
		wantErr bool
	}{
		{
			name: "stable at relase",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: "release-1.2",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: hyperv1.Release{
							Image: "release-1.2",
						},
					},
				},
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "cluster is rolling out",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: "release-1.2",
					},
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "cluster is upgrading",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: "release-1.3",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: hyperv1.Release{
							Image: "release-1.2",
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
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: hyperv1.Release{
							Image: "release-1.2",
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
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: hyperv1.Release{
							Image: "release-1.2",
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
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: hyperv1.Release{
							Image: "release-1.2",
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
		t.Run(tt.name, func(t *testing.T) {
			got, err := isProgressing(context.TODO(), tt.hc)
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

func TestIsValidReleaseVersion(t *testing.T) {
	v := func(str string) *semver.Version {
		result := semver.MustParse(str)
		return &result
	}
	testCases := []struct {
		name                   string
		currentVersion         *semver.Version
		nextVersion            *semver.Version
		latestVersionSupported *semver.Version
		minVersionSupported    *semver.Version
		networkType            hyperv1.NetworkType
		expectError            bool
		platform               hyperv1.PlatformType
	}{
		{
			name:                   "Releases before 4.8 are not supported",
			currentVersion:         v("4.8.0"),
			nextVersion:            v("4.7.0"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			expectError:            true,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "y-stream downgrade is not supported",
			currentVersion:         v("4.10.0"),
			nextVersion:            v("4.9.0"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			expectError:            true,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "y-stream upgrade is not for OpenShiftSDN",
			currentVersion:         v("4.10.0"),
			nextVersion:            v("4.11.0"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			networkType:            hyperv1.OpenShiftSDN,
			expectError:            true,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "the latest HostedCluster version supported by this Operator is 4.12.0",
			currentVersion:         v("4.12.0"),
			nextVersion:            v("4.13.0"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			expectError:            true,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "the minimum HostedCluster version supported by this Operator is 4.10.0",
			currentVersion:         v("4.9.0"),
			nextVersion:            v("4.9.0"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			expectError:            true,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "Valid",
			currentVersion:         v("4.11.0"),
			nextVersion:            v("4.11.1"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			expectError:            false,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "When going to minimum should be valid",
			currentVersion:         v("4.9.0"),
			nextVersion:            v("4.10.0"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			expectError:            false,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "Valid when going to minimum with a dev tag",
			currentVersion:         v("4.9.0"),
			nextVersion:            v("4.10.0-nightly-something"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			expectError:            false,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "Invalid when installing with OpenShiftSDN and version > 4.10",
			currentVersion:         nil,
			nextVersion:            v("4.11.5"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			networkType:            hyperv1.OpenShiftSDN,
			expectError:            true,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "Valid when installing with OpenShift SDN and version <= 4.10",
			currentVersion:         nil,
			nextVersion:            v("4.10.3"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			networkType:            hyperv1.OpenShiftSDN,
			expectError:            false,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "Invalid when isntalling with OVNKubernetes and version < 4.11",
			currentVersion:         nil,
			nextVersion:            v("4.10.5"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			networkType:            hyperv1.OVNKubernetes,
			expectError:            true,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "Valid when isntalling with OVNKubernetes and version >= 4.11",
			currentVersion:         nil,
			nextVersion:            v("4.11.1"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			networkType:            hyperv1.OVNKubernetes,
			expectError:            false,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "Valid when installing with OpenShift SDN and version >= 4.11 with PowerVS platform",
			currentVersion:         nil,
			nextVersion:            v("4.11.0"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			networkType:            hyperv1.OpenShiftSDN,
			expectError:            false,
			platform:               hyperv1.PowerVSPlatform,
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := isValidReleaseVersion(test.nextVersion, test.currentVersion, test.latestVersionSupported, test.minVersionSupported, test.networkType, test.platform)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
		})
	}

}
