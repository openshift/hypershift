package v1beta1

import (
	"k8s.io/utils/ptr"
)

// EffectiveShards returns the effective shard configuration for managed etcd.
// If shards are not explicitly configured, returns a default single shard.
func (m *ManagedEtcdSpec) EffectiveShards(hcp *HostedControlPlane) []ManagedEtcdShardSpec {
	if len(m.Shards) > 0 {
		return m.Shards
	}

	// Default: single shard accepting all prefixes
	replicas := int32(1)
	if hcp.Spec.ControllerAvailabilityPolicy == HighlyAvailable {
		replicas = 3
	}

	return []ManagedEtcdShardSpec{
		{
			Name:             "default",
			ResourcePrefixes: []string{"/"},
			Priority:         EtcdShardPriorityCritical,
			Storage:          nil, // inherits from m.Storage
			Replicas:         &replicas,
			BackupSchedule:   ptr.To("*/30 * * * *"),
		},
	}
}

// EffectiveShards returns the effective shard configuration for unmanaged etcd.
// If shards are not explicitly configured, returns a default single shard using
// the legacy endpoint and tls fields.
func (u *UnmanagedEtcdSpec) EffectiveShards() []UnmanagedEtcdShardSpec {
	if len(u.Shards) > 0 {
		return u.Shards
	}

	// Default: single shard accepting all prefixes, using legacy endpoint/tls
	tls := EtcdTLSConfig{}
	if u.TLS != nil {
		tls = *u.TLS
	}

	return []UnmanagedEtcdShardSpec{
		{
			Name:             "default",
			ResourcePrefixes: []string{"/"},
			Priority:         EtcdShardPriorityCritical,
			Endpoint:         u.Endpoint,
			TLS:              tls,
		},
	}
}
