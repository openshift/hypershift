package v1beta1

import (
	"reflect"
	"testing"
)

func TestMissingPersistentShards(t *testing.T) {
	tests := []struct {
		name    string
		mapping []HCPEtcdShardSnapshot
		managed *ManagedEtcdSpec
		want    []string
	}{
		{
			name: "complete mapping covers default and named persistent shards",
			mapping: []HCPEtcdShardSnapshot{
				{Name: "default", SnapshotURL: "s3://b/default.db"},
				{Name: "events", SnapshotURL: "s3://b/events.db"},
			},
			managed: &ManagedEtcdSpec{
				Shards: []ManagedEtcdShardSpec{
					{Name: "events", Storage: ManagedEtcdShardStorageSpec{Type: PersistentVolumeEtcdShardStorage}},
					{Name: "logs", Storage: ManagedEtcdShardStorageSpec{Type: EmptyDirEtcdShardStorage}},
				},
			},
			want: nil,
		},
		{
			name:    "empty mapping reports the default shard missing",
			mapping: nil,
			managed: nil,
			want:    []string{"default"},
		},
		{
			name: "missing named persistent shard is reported",
			mapping: []HCPEtcdShardSnapshot{
				{Name: "default", SnapshotURL: "s3://b/default.db"},
			},
			managed: &ManagedEtcdSpec{
				Shards: []ManagedEtcdShardSpec{
					{Name: "events", Storage: ManagedEtcdShardStorageSpec{Type: PersistentVolumeEtcdShardStorage}},
				},
			},
			want: []string{"events"},
		},
		{
			name: "EmptyDir shards are not required",
			mapping: []HCPEtcdShardSnapshot{
				{Name: "default", SnapshotURL: "s3://b/default.db"},
			},
			managed: &ManagedEtcdSpec{
				Shards: []ManagedEtcdShardSpec{
					{Name: "logs", Storage: ManagedEtcdShardStorageSpec{Type: EmptyDirEtcdShardStorage}},
				},
			},
			want: nil,
		},
		{
			name: "missing default and missing named shard are both reported",
			mapping: []HCPEtcdShardSnapshot{
				{Name: "events", SnapshotURL: "s3://b/events.db"},
			},
			managed: &ManagedEtcdSpec{
				Shards: []ManagedEtcdShardSpec{
					{Name: "events", Storage: ManagedEtcdShardStorageSpec{Type: PersistentVolumeEtcdShardStorage}},
					{Name: "leases", Storage: ManagedEtcdShardStorageSpec{Type: PersistentVolumeEtcdShardStorage}},
				},
			},
			want: []string{"default", "leases"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MissingPersistentShards(tt.mapping, tt.managed)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MissingPersistentShards() = %v, want %v", got, tt.want)
			}
		})
	}
}
