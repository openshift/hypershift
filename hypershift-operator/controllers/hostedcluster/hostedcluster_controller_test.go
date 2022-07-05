package hostedcluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	platformaws "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/aws"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/kubevirt"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/autoscaler"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
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
	"k8s.io/apimachinery/pkg/runtime"
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

// TestClusterAutoscalerArgs checks to make sure that fields specified in a ClusterAutoscaling spec
// become arguments to the autoscaler.
func TestClusterAutoscalerArgs(t *testing.T) {
	tests := map[string]struct {
		AutoscalerOptions   hyperv1.ClusterAutoscaling
		ExpectedArgs        []string
		ExpectedMissingArgs []string
	}{
		"contains only default arguments": {
			AutoscalerOptions: hyperv1.ClusterAutoscaling{},
			ExpectedArgs: []string{
				"--cloud-provider=clusterapi",
				"--node-group-auto-discovery=clusterapi:namespace=$(MY_NAMESPACE)",
				"--kubeconfig=/mnt/kubeconfig/target-kubeconfig",
				"--clusterapi-cloud-config-authoritative",
				"--skip-nodes-with-local-storage=false",
				"--alsologtostderr",
				"--v=4",
			},
			ExpectedMissingArgs: []string{
				"--max-nodes-total",
				"--max-graceful-termination-sec",
				"--max-node-provision-time",
				"--expendable-pods-priority-cutoff",
			},
		},
		"contains all optional parameters": {
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				MaxNodesTotal:        pointer.Int32Ptr(100),
				MaxPodGracePeriod:    pointer.Int32Ptr(300),
				MaxNodeProvisionTime: "20m",
				PodPriorityThreshold: pointer.Int32Ptr(-5),
			},
			ExpectedArgs: []string{
				"--cloud-provider=clusterapi",
				"--node-group-auto-discovery=clusterapi:namespace=$(MY_NAMESPACE)",
				"--kubeconfig=/mnt/kubeconfig/target-kubeconfig",
				"--clusterapi-cloud-config-authoritative",
				"--skip-nodes-with-local-storage=false",
				"--alsologtostderr",
				"--v=4",
				"--max-nodes-total=100",
				"--max-graceful-termination-sec=300",
				"--max-node-provision-time=20m",
				"--expendable-pods-priority-cutoff=-5",
			},
			ExpectedMissingArgs: []string{},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			deployment := autoscaler.AutoScalerDeployment("test-ns")
			sa := autoscaler.AutoScalerServiceAccount("test-ns")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "test-secret",
				},
			}
			hc := &hyperv1.HostedCluster{}
			hc.Name = "name"
			hc.Namespace = "namespace"
			err := reconcileAutoScalerDeployment(deployment, hc, sa, secret, test.AutoscalerOptions, "clusterAutoscalerImage", "availabilityProberImage", false)
			if err != nil {
				t.Error(err)
			}

			observedArgs := sets.NewString(deployment.Spec.Template.Spec.Containers[0].Args...)
			for _, arg := range test.ExpectedArgs {
				if !observedArgs.Has(arg) {
					t.Errorf("Expected to find \"%s\" in observed arguments: %v", arg, observedArgs)
				}
			}

			for _, arg := range test.ExpectedMissingArgs {
				if observedArgs.Has(arg) {
					t.Errorf("Did not expect to find \"%s\" in observed arguments", arg)
				}
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
				g.Expect(hostedControlPlane.Spec.APIPort).To(Equal(test.expectedAPIPort))
				g.Expect(hostedControlPlane.Spec.APIAdvertiseAddress).To(Equal(test.expectedAPIAdvertiseAddress))
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
		Client:                        client,
		Clock:                         clock.RealClock{},
		ManagementClusterCapabilities: fakecapabilities.NewSupportAllExcept(capabilities.CapabilityConfigOpenshiftIO),
		createOrUpdate:                func(reconcile.Request) upsert.CreateOrUpdateFN { return ctrl.CreateOrUpdate },
		ReleaseProvider:               &fakereleaseprovider.FakeReleaseProvider{},
		ImageMetadataProvider:         &fakeimagemetadataprovider.FakeImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{}},
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
	config := &hyperv1.ClusterConfiguration{Proxy: &configv1.ProxySpec{HTTPProxy: "http://10.0.136.57:3128", HTTPSProxy: "http://10.0.136.57:3128"}}
	result, err := configurationFieldsToRawExtensions(config)
	if err != nil {
		t.Fatalf("configurationFieldsToRawExtensions: %v", err)
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

	var proxy configv1.Proxy
	if err := json.Unmarshal(result[0].Raw, &proxy); err != nil {
		t.Fatalf("failed to unmarshal raw data: %v", err)
	}
	if proxy.APIVersion == "" || proxy.Kind == "" {
		t.Errorf("rawObject has no apiVersion or kind set: %+v", proxy.ObjectMeta)
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
			name: "not upgradable, no force annotation",
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
			name: "not upgradable, old force annotation",
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
			name: "not upgradable, force annotation",
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
				ARN:       imageRegistryARN,
				Namespace: "cloud-network-config-controller",
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
