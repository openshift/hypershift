package hostedcluster

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/kubevirt"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/autoscaler"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/capabilities"
	fakecapabilities "github.com/openshift/hypershift/support/capabilities/fake"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/upsert"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
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
			actualStatus := computeClusterVersionStatus(clock.NewFakeClock(Now.Time), &test.Cluster, &test.ControlPlane)
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
		"missing kubeconfig should cause unavailability": {
			Cluster: hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Etcd: hyperv1.EtcdSpec{ManagementType: hyperv1.Managed},
				},
				Status: hyperv1.HostedClusterStatus{},
			},
			ControlPlane: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{Type: string(hyperv1.HostedControlPlaneAvailable), Status: metav1.ConditionTrue},
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
				Status: hyperv1.HostedClusterStatus{
					KubeConfig: &corev1.LocalObjectReference{Name: "foo"},
				},
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
			err := reconcileAutoScalerDeployment(deployment, hc, sa, secret, test.AutoscalerOptions, imageClusterAutoscaler, "availability-prober:latest", false)
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

func TestReconcileIgnitionServerServiceNodePortFreshInitialization(t *testing.T) {
	tests := []struct {
		name                           string
		inputIgnitionServerService     *corev1.Service
		inputServicePublishingStrategy *hyperv1.ServicePublishingStrategy
	}{
		{
			name:                       "fresh service initialization",
			inputIgnitionServerService: ignitionserver.Service("default"),
			inputServicePublishingStrategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.NodePort,
			},
		},
		{
			name:                       "fresh service with node port specified",
			inputIgnitionServerService: ignitionserver.Service("default"),
			inputServicePublishingStrategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.NodePort,
				NodePort: &hyperv1.NodePortPublishingStrategy{
					Port: int32(30000),
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reconcileIgnitionServerService(test.inputIgnitionServerService, test.inputServicePublishingStrategy)
			g := NewGomegaWithT(t)
			g.Expect(len(test.inputIgnitionServerService.Spec.Ports)).To(Equal(1))
			g.Expect(test.inputIgnitionServerService.Spec.Type).To(Equal(corev1.ServiceTypeNodePort))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].TargetPort).To(Equal(intstr.FromInt(9090)))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Port).To(Equal(int32(443)))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Name).To(Equal("https"))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))
			if test.inputServicePublishingStrategy.NodePort != nil && test.inputServicePublishingStrategy.NodePort.Port > 0 {
				g.Expect(test.inputIgnitionServerService.Spec.Ports[0].NodePort).To(Equal(test.inputServicePublishingStrategy.NodePort.Port))
			}
		})
	}
}

func TestReconcileIgnitionServerServiceNodePortExistingService(t *testing.T) {
	tests := []struct {
		name                           string
		inputIgnitionServerService     *corev1.Service
		inputServicePublishingStrategy *hyperv1.ServicePublishingStrategy
	}{
		{
			name: "existing service keeps nodeport",
			inputIgnitionServerService: &corev1.Service{
				ObjectMeta: ignitionserver.Service("default").ObjectMeta,
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "https",
							Port:       443,
							TargetPort: intstr.FromInt(9090),
							Protocol:   corev1.ProtocolTCP,
							NodePort:   int32(30000),
						},
					},
				},
			},
			inputServicePublishingStrategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.NodePort,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initialNodePort := test.inputIgnitionServerService.Spec.Ports[0].NodePort
			reconcileIgnitionServerService(test.inputIgnitionServerService, test.inputServicePublishingStrategy)
			g := NewGomegaWithT(t)
			g.Expect(len(test.inputIgnitionServerService.Spec.Ports)).To(Equal(1))
			g.Expect(test.inputIgnitionServerService.Spec.Type).To(Equal(corev1.ServiceTypeNodePort))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].TargetPort).To(Equal(intstr.FromInt(9090)))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Port).To(Equal(int32(443)))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Name).To(Equal("https"))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].NodePort).To(Equal(initialNodePort))
		})
	}
}

func TestReconcileIgnitionServerServiceRoute(t *testing.T) {
	tests := []struct {
		name                           string
		inputIgnitionServerService     *corev1.Service
		inputServicePublishingStrategy *hyperv1.ServicePublishingStrategy
	}{
		{
			name:                       "fresh service initialization",
			inputIgnitionServerService: ignitionserver.Service("default"),
			inputServicePublishingStrategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
			},
		},
		{
			name: "existing service",
			inputIgnitionServerService: &corev1.Service{
				ObjectMeta: ignitionserver.Service("default").ObjectMeta,
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "https",
							Port:       443,
							TargetPort: intstr.FromInt(9090),
							Protocol:   corev1.ProtocolTCP,
						},
					},
				},
			},
			inputServicePublishingStrategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reconcileIgnitionServerService(test.inputIgnitionServerService, test.inputServicePublishingStrategy)
			g := NewGomegaWithT(t)
			g.Expect(len(test.inputIgnitionServerService.Spec.Ports)).To(Equal(1))
			g.Expect(test.inputIgnitionServerService.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].TargetPort).To(Equal(intstr.FromInt(9090)))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Port).To(Equal(int32(443)))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Name).To(Equal("https"))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))
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
						hostedClusterAnnotation: "master/cluster1",
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
						hostedClusterAnnotation: "master/cluster1",
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
					Agent: &hyperv1.AgentPlatformSpec{},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "aws"},
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AWSPlatform,
					AWS: &hyperv1.AWSPlatformSpec{
						KubeCloudControllerCreds:  corev1.LocalObjectReference{Name: "secret"},
						NodePoolManagementCreds:   corev1.LocalObjectReference{Name: "secret"},
						ControlPlaneOperatorCreds: corev1.LocalObjectReference{Name: "secret"},
						EndpointAccess:            hyperv1.Public,
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
		ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
		createOrUpdate:                func(reconcile.Request) upsert.CreateOrUpdateFN { return ctrl.CreateOrUpdate },
		ReleaseProvider:               &fakereleaseprovider.FakeReleaseProvider{},
	}

	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))

	for _, hc := range hostedClusters {
		if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: hc.Namespace, Name: hc.Name}}); err != nil {
			t.Fatalf("Reconcile failed: %v", err)
		}
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
			name: "Azurecluser with incomplete credentials secret, error",
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
