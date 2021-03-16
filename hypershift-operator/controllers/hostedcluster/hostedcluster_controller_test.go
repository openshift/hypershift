package hostedcluster

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

var Now = metav1.NewTime(time.Now())
var Later = metav1.NewTime(Now.Add(5 * time.Minute))

type fixedClock struct{}

func (_ fixedClock) Now() time.Time { return Now.Time }

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
				Spec:       hyperv1.HostedClusterSpec{Release: hyperv1.Release{Image: "a"}},
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
		"hostedcontrolplane rollout is deferred due to existing rollout": {
			Cluster: hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: Now},
				Spec:       hyperv1.HostedClusterSpec{Release: hyperv1.Release{Image: "b"}},
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
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: Now},
				Spec:       hyperv1.HostedControlPlaneSpec{ReleaseImage: "a"},
				Status:     hyperv1.HostedControlPlaneStatus{},
			},
			ExpectedImage: "a",
		},
		"hostedcontrolplane rollout happens after existing rollout is complete": {
			Cluster: hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: Now},
				Spec:       hyperv1.HostedClusterSpec{Release: hyperv1.Release{Image: "b"}},
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
			actualStatus := computeClusterVersionStatus(fixedClock{}, &test.Cluster, &test.ControlPlane)
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
				Spec:   hyperv1.HostedClusterSpec{},
				Status: hyperv1.HostedClusterStatus{},
			},
			ControlPlane: nil,
			ExpectedCondition: metav1.Condition{
				Type:   string(hyperv1.Available),
				Status: metav1.ConditionFalse,
			},
		},
		"missing kubeconfig should cause unavailability": {
			Cluster: hyperv1.HostedCluster{
				Spec:   hyperv1.HostedClusterSpec{},
				Status: hyperv1.HostedClusterStatus{},
			},
			ControlPlane: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []hyperv1.HostedControlPlaneCondition{
						{Type: hyperv1.Available, Status: hyperv1.ConditionTrue},
					},
				},
			},
			ExpectedCondition: metav1.Condition{
				Type:   string(hyperv1.Available),
				Status: metav1.ConditionFalse,
			},
		},
		"should be available": {
			Cluster: hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{},
				Status: hyperv1.HostedClusterStatus{
					KubeConfig: &corev1.LocalObjectReference{Name: "foo"},
				},
			},
			ControlPlane: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{ReleaseImage: "a"},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []hyperv1.HostedControlPlaneCondition{
						{Type: hyperv1.Available, Status: hyperv1.ConditionTrue},
					},
				},
			},
			ExpectedCondition: metav1.Condition{
				Type:   string(hyperv1.Available),
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
