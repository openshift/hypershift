package etcd

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileEtcdShards_SingleShard(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = policyv1.AddToScheme(scheme)
	_ = hyperv1.AddToScheme(scheme)

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			ClusterID:                    "test-cluster-id",
			ControllerAvailabilityPolicy: hyperv1.SingleReplica,
			Etcd: hyperv1.EtcdSpec{
				ManagementType: hyperv1.Managed,
				Managed: &hyperv1.ManagedEtcdSpec{
					Storage: hyperv1.ManagedEtcdStorageSpec{
						Type: hyperv1.PersistentVolumeEtcdStorage,
					},
				},
			},
			Networking: hyperv1.ClusterNetworking{
				ClusterNetwork: []hyperv1.ClusterNetworkEntry{
					{CIDR: *ipnet.MustParseCIDR("10.132.0.0/14")},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	params := &ShardParams{
		OwnerRef: metav1.OwnerReference{
			APIVersion: "hypershift.openshift.io/v1beta1",
			Kind:       "HostedControlPlane",
			Name:       hcp.Name,
			UID:        hcp.UID,
		},
		EtcdImage:                 "quay.io/openshift/etcd:latest",
		ControlPlaneOperatorImage: "quay.io/openshift/hypershift:latest",
		ClusterName:               hcp.Name,
		Namespace:                 hcp.Namespace,
		IPv4:                      true,
		AvailabilityPolicy:        hcp.Spec.ControllerAvailabilityPolicy,
		DefaultStorageSize:        "8Gi",
		ClusterID:                 hcp.Spec.ClusterID,
	}

	createOrUpdate := upsert.New(false).CreateOrUpdate

	err := ReconcileEtcdShards(ctx, hcp, params, fakeClient, createOrUpdate, metrics.MetricsSetAll)
	if err != nil {
		t.Fatalf("ReconcileEtcdShards failed: %v", err)
	}

	// Verify StatefulSet was created
	sts := manifests.EtcdStatefulSetForShard(hcp.Namespace, "default")
	err = fakeClient.Get(ctx, client.ObjectKeyFromObject(sts), sts)
	if err != nil {
		t.Errorf("Expected StatefulSet to be created, got error: %v", err)
	}
}

func TestReconcileEtcdShards_MultipleShards(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = policyv1.AddToScheme(scheme)
	_ = hyperv1.AddToScheme(scheme)

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			ClusterID:                    "test-cluster-id",
			ControllerAvailabilityPolicy: hyperv1.HighlyAvailable,
			Etcd: hyperv1.EtcdSpec{
				ManagementType: hyperv1.Managed,
				Managed: &hyperv1.ManagedEtcdSpec{
					Storage: hyperv1.ManagedEtcdStorageSpec{
						Type: hyperv1.PersistentVolumeEtcdStorage,
					},
					Shards: []hyperv1.ManagedEtcdShardSpec{
						{
							Name:     "default",
							Priority: hyperv1.EtcdShardPriorityCritical,
							Replicas: ptr.To(int32(3)),
						},
						{
							Name:     "events",
							Priority: hyperv1.EtcdShardPriorityLow,
							Replicas: ptr.To(int32(3)),
						},
					},
				},
			},
			Networking: hyperv1.ClusterNetworking{
				ClusterNetwork: []hyperv1.ClusterNetworkEntry{
					{CIDR: *ipnet.MustParseCIDR("10.132.0.0/14")},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	params := &ShardParams{
		OwnerRef: metav1.OwnerReference{
			APIVersion: "hypershift.openshift.io/v1beta1",
			Kind:       "HostedControlPlane",
			Name:       hcp.Name,
			UID:        hcp.UID,
		},
		EtcdImage:                 "quay.io/openshift/etcd:latest",
		ControlPlaneOperatorImage: "quay.io/openshift/hypershift:latest",
		ClusterName:               hcp.Name,
		Namespace:                 hcp.Namespace,
		IPv4:                      true,
		AvailabilityPolicy:        hcp.Spec.ControllerAvailabilityPolicy,
		DefaultStorageSize:        "8Gi",
		ClusterID:                 hcp.Spec.ClusterID,
	}

	createOrUpdate := upsert.New(false).CreateOrUpdate

	err := ReconcileEtcdShards(ctx, hcp, params, fakeClient, createOrUpdate, metrics.MetricsSetAll)
	if err != nil {
		t.Fatalf("ReconcileEtcdShards failed: %v", err)
	}

	// Verify default shard StatefulSet
	defaultSts := manifests.EtcdStatefulSetForShard(hcp.Namespace, "default")
	err = fakeClient.Get(ctx, client.ObjectKeyFromObject(defaultSts), defaultSts)
	if err != nil {
		t.Errorf("Expected default shard StatefulSet to be created, got error: %v", err)
	}
	if defaultSts.Name != "etcd" {
		t.Errorf("Expected default shard StatefulSet name to be 'etcd', got: %s", defaultSts.Name)
	}

	// Verify events shard StatefulSet
	eventsSts := manifests.EtcdStatefulSetForShard(hcp.Namespace, "events")
	err = fakeClient.Get(ctx, client.ObjectKeyFromObject(eventsSts), eventsSts)
	if err != nil {
		t.Errorf("Expected events shard StatefulSet to be created, got error: %v", err)
	}
	if eventsSts.Name != "etcd-events" {
		t.Errorf("Expected events shard StatefulSet name to be 'etcd-events', got: %s", eventsSts.Name)
	}
}

func TestAggregateShardStatus_AllHealthy(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = hyperv1.AddToScheme(scheme)

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
	}

	shards := []hyperv1.ManagedEtcdShardSpec{
		{
			Name:     "default",
			Priority: hyperv1.EtcdShardPriorityCritical,
		},
	}

	// Create a healthy StatefulSet
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd",
			Namespace: hcp.Namespace,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr.To(int32(3)),
		},
		Status: appsv1.StatefulSetStatus{
			ReadyReplicas: 3,
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sts).Build()

	condition, err := AggregateShardStatus(ctx, hcp, shards, fakeClient)
	if err != nil {
		t.Fatalf("AggregateShardStatus failed: %v", err)
	}

	if condition.Status != metav1.ConditionTrue {
		t.Errorf("Expected condition status to be True, got: %s", condition.Status)
	}
	if condition.Reason != hyperv1.EtcdQuorumAvailableReason {
		t.Errorf("Expected reason to be %s, got: %s", hyperv1.EtcdQuorumAvailableReason, condition.Reason)
	}
}

func TestAggregateShardStatus_CriticalUnhealthy(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = hyperv1.AddToScheme(scheme)

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
	}

	shards := []hyperv1.ManagedEtcdShardSpec{
		{
			Name:     "default",
			Priority: hyperv1.EtcdShardPriorityCritical,
		},
	}

	// Create an unhealthy StatefulSet (no quorum)
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd",
			Namespace: hcp.Namespace,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr.To(int32(3)),
		},
		Status: appsv1.StatefulSetStatus{
			ReadyReplicas: 1,
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sts).Build()

	condition, err := AggregateShardStatus(ctx, hcp, shards, fakeClient)
	if err != nil {
		t.Fatalf("AggregateShardStatus failed: %v", err)
	}

	if condition.Status != metav1.ConditionFalse {
		t.Errorf("Expected condition status to be False, got: %s", condition.Status)
	}
	if condition.Reason != hyperv1.EtcdWaitingForQuorumReason {
		t.Errorf("Expected reason to be %s, got: %s", hyperv1.EtcdWaitingForQuorumReason, condition.Reason)
	}
}

func TestCleanupOrphanedShards(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = hyperv1.AddToScheme(scheme)

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
	}

	activeShards := []hyperv1.ManagedEtcdShardSpec{
		{Name: "default"},
	}

	// Create an orphaned shard StatefulSet
	orphanedSts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-events",
			Namespace: hcp.Namespace,
			Labels: map[string]string{
				"app": "etcd",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(orphanedSts).Build()

	err := CleanupOrphanedShards(ctx, hcp, activeShards, fakeClient)
	if err != nil {
		t.Fatalf("CleanupOrphanedShards failed: %v", err)
	}

	// Verify orphaned StatefulSet was deleted
	err = fakeClient.Get(ctx, client.ObjectKeyFromObject(orphanedSts), orphanedSts)
	if err == nil {
		t.Error("Expected orphaned StatefulSet to be deleted")
	}
}

func TestBackwardCompatibility_DefaultShard(t *testing.T) {
	// Verify that default shard uses "etcd" name without suffix
	name := resourceNameForShard("etcd", "default")
	if name != "etcd" {
		t.Errorf("Expected default shard name to be 'etcd', got: %s", name)
	}

	// Verify that named shards use prefix
	name = resourceNameForShard("etcd", "events")
	if name != "etcd-events" {
		t.Errorf("Expected events shard name to be 'etcd-events', got: %s", name)
	}
}
