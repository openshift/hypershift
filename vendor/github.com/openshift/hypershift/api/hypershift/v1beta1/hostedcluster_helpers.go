package v1beta1

// EffectiveShards returns the effective shard configuration for managed etcd.
// If shards are not explicitly configured, returns a default single shard.
func (m *ManagedEtcdSpec) EffectiveShards(hcp *HostedControlPlane) []ManagedEtcdShardSpec {
	if len(m.Shards) > 0 {
		return m.Shards
	}

	replicas := int32(1)
	if hcp.Spec.ControllerAvailabilityPolicy == HighlyAvailable {
		replicas = 3
	}

	return []ManagedEtcdShardSpec{
		{
			Name:             "default",
			ResourcePrefixes: []string{"/"},
			Priority:         EtcdShardPriorityCritical,
			Replicas:         &replicas,
			BackupSchedule:   "*/30 * * * *",
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

	return []UnmanagedEtcdShardSpec{
		{
			Name:             "default",
			ResourcePrefixes: []string{"/"},
			Priority:         EtcdShardPriorityCritical,
			Endpoint:         u.Endpoint,
			TLS:              u.TLS,
		},
	}
}
