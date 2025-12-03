package etcd

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestReconcileStatefulSet_DefaultShard(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			ControllerAvailabilityPolicy: hyperv1.SingleReplica,
			Etcd: hyperv1.EtcdSpec{
				ManagementType: hyperv1.Managed,
				Managed: &hyperv1.ManagedEtcdSpec{
					Storage: hyperv1.ManagedEtcdStorageSpec{
						Type: hyperv1.PersistentVolumeEtcdStorage,
					},
				},
			},
		},
	}

	shard := hyperv1.ManagedEtcdShardSpec{
		Name:     "default",
		Priority: hyperv1.EtcdShardPriorityCritical,
	}

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
		ClusterID:                 "test-cluster-id",
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd",
			Namespace: hcp.Namespace,
		},
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "etcd"},
					},
				},
			},
		},
	}

	err := ReconcileStatefulSet(sts, hcp, shard, params)
	if err != nil {
		t.Fatalf("ReconcileStatefulSet failed: %v", err)
	}

	// Verify StatefulSet name is "etcd" for backward compatibility
	if sts.Name != "etcd" {
		t.Errorf("Expected StatefulSet name to be 'etcd', got: %s", sts.Name)
	}

	// Verify ServiceName is "etcd-discovery"
	if sts.Spec.ServiceName != "etcd-discovery" {
		t.Errorf("Expected ServiceName to be 'etcd-discovery', got: %s", sts.Spec.ServiceName)
	}

	// Verify shard label
	if sts.Spec.Template.Labels["hypershift.openshift.io/etcd-shard"] != "default" {
		t.Errorf("Expected shard label to be 'default', got: %s", sts.Spec.Template.Labels["hypershift.openshift.io/etcd-shard"])
	}

	// Verify priority label
	if sts.Spec.Template.Labels["hypershift.openshift.io/etcd-priority"] != string(hyperv1.EtcdShardPriorityCritical) {
		t.Errorf("Expected priority label to be '%s', got: %s", hyperv1.EtcdShardPriorityCritical, sts.Spec.Template.Labels["hypershift.openshift.io/etcd-priority"])
	}
}

func TestReconcileStatefulSet_NamedShard(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			ControllerAvailabilityPolicy: hyperv1.HighlyAvailable,
			Etcd: hyperv1.EtcdSpec{
				ManagementType: hyperv1.Managed,
				Managed: &hyperv1.ManagedEtcdSpec{
					Storage: hyperv1.ManagedEtcdStorageSpec{
						Type: hyperv1.PersistentVolumeEtcdStorage,
					},
				},
			},
		},
	}

	shard := hyperv1.ManagedEtcdShardSpec{
		Name:     "events",
		Priority: hyperv1.EtcdShardPriorityLow,
		Replicas: ptr.To(int32(3)),
	}

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
		ClusterID:                 "test-cluster-id",
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-events",
			Namespace: hcp.Namespace,
		},
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "etcd"},
					},
				},
			},
		},
	}

	err := ReconcileStatefulSet(sts, hcp, shard, params)
	if err != nil {
		t.Fatalf("ReconcileStatefulSet failed: %v", err)
	}

	// Verify StatefulSet name includes shard name
	if sts.Name != "etcd-events" {
		t.Errorf("Expected StatefulSet name to be 'etcd-events', got: %s", sts.Name)
	}

	// Verify ServiceName includes shard name
	if sts.Spec.ServiceName != "etcd-events-discovery" {
		t.Errorf("Expected ServiceName to be 'etcd-events-discovery', got: %s", sts.Spec.ServiceName)
	}

	// Verify replicas from shard spec
	if *sts.Spec.Replicas != 3 {
		t.Errorf("Expected replicas to be 3, got: %d", *sts.Spec.Replicas)
	}

	// Verify shard label
	if sts.Spec.Template.Labels["hypershift.openshift.io/etcd-shard"] != "events" {
		t.Errorf("Expected shard label to be 'events', got: %s", sts.Spec.Template.Labels["hypershift.openshift.io/etcd-shard"])
	}

	// Verify priority label
	if sts.Spec.Template.Labels["hypershift.openshift.io/etcd-priority"] != string(hyperv1.EtcdShardPriorityLow) {
		t.Errorf("Expected priority label to be '%s', got: %s", hyperv1.EtcdShardPriorityLow, sts.Spec.Template.Labels["hypershift.openshift.io/etcd-priority"])
	}
}

func TestReconcileStatefulSet_ShardConfiguration(t *testing.T) {
	tests := []struct {
		name               string
		availabilityPolicy hyperv1.AvailabilityPolicy
		shardReplicas      *int32
		expectedReplicas   int32
	}{
		{
			name:               "SingleReplica default",
			availabilityPolicy: hyperv1.SingleReplica,
			shardReplicas:      nil,
			expectedReplicas:   1,
		},
		{
			name:               "HighlyAvailable default",
			availabilityPolicy: hyperv1.HighlyAvailable,
			shardReplicas:      nil,
			expectedReplicas:   3,
		},
		{
			name:               "Custom replicas override",
			availabilityPolicy: hyperv1.HighlyAvailable,
			shardReplicas:      ptr.To(int32(5)),
			expectedReplicas:   5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ControllerAvailabilityPolicy: tt.availabilityPolicy,
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
						Managed: &hyperv1.ManagedEtcdSpec{
							Storage: hyperv1.ManagedEtcdStorageSpec{
								Type: hyperv1.PersistentVolumeEtcdStorage,
							},
						},
					},
				},
			}

			shard := hyperv1.ManagedEtcdShardSpec{
				Name:     "test",
				Priority: hyperv1.EtcdShardPriorityCritical,
				Replicas: tt.shardReplicas,
			}

			params := &ShardParams{
				AvailabilityPolicy: tt.availabilityPolicy,
				Namespace:          hcp.Namespace,
			}

			sts := &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd-test",
					Namespace: hcp.Namespace,
				},
				Spec: appsv1.StatefulSetSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{Name: "etcd"},
							},
						},
					},
				},
			}

			err := ReconcileStatefulSet(sts, hcp, shard, params)
			if err != nil {
				t.Fatalf("ReconcileStatefulSet failed: %v", err)
			}

			if *sts.Spec.Replicas != tt.expectedReplicas {
				t.Errorf("Expected replicas to be %d, got: %d", tt.expectedReplicas, *sts.Spec.Replicas)
			}
		})
	}
}
