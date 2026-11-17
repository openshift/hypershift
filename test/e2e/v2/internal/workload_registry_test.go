//go:build e2ev2

package internal

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/utils/ptr"
)

func TestEtcdShardWorkloads(t *testing.T) {
	managedWithShards := func(names ...string) *hyperv1.HostedCluster {
		shards := make([]hyperv1.ManagedEtcdShardSpec, 0, len(names))
		for _, name := range names {
			shards = append(shards, hyperv1.ManagedEtcdShardSpec{
				Name:      name,
				Resources: []hyperv1.EtcdShardResource{{APIGroup: ptr.To(""), Resource: name}},
			})
		}
		return &hyperv1.HostedCluster{
			Spec: hyperv1.HostedClusterSpec{
				Etcd: hyperv1.EtcdSpec{
					ManagementType: hyperv1.Managed,
					Managed:        &hyperv1.ManagedEtcdSpec{Shards: shards},
				},
			},
		}
	}

	tests := []struct {
		name          string
		hostedCluster *hyperv1.HostedCluster
		want          []WorkloadSpec
	}{
		{
			name:          "When the hosted cluster is nil, it should return no workloads",
			hostedCluster: nil,
		},
		{
			name: "When etcd is unmanaged, it should return no workloads",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Unmanaged,
						Unmanaged: &hyperv1.UnmanagedEtcdSpec{
							Shards: []hyperv1.UnmanagedEtcdShardSpec{{Name: "events"}},
						},
					},
				},
			},
		},
		{
			name:          "When managed etcd has no shards, it should return no workloads for the default shard",
			hostedCluster: managedWithShards(),
		},
		{
			name:          "When managed etcd has shards, it should return a StatefulSet workload per non-default shard",
			hostedCluster: managedWithShards("events", "leases", "podtemplates"),
			want: []WorkloadSpec{
				{Type: "StatefulSet", Name: "etcd-events", PodSelector: map[string]string{"app": "etcd-events"}},
				{Type: "StatefulSet", Name: "etcd-leases", PodSelector: map[string]string{"app": "etcd-leases"}},
				{Type: "StatefulSet", Name: "etcd-podtemplates", PodSelector: map[string]string{"app": "etcd-podtemplates"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(EtcdShardWorkloads(tt.hostedCluster)).To(Equal(tt.want))
		})
	}
}
