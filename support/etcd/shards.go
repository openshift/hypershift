package etcd

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// ManagedEffectiveShard represents a resolved etcd shard with all configuration needed
// to create the corresponding component.
type ManagedEffectiveShard struct {
	Name             string
	ResourcePrefixes []string
	Storage          hyperv1.ManagedEtcdShardStorageSpec
	Replicas         int32 // 0 means "use framework default" (for the default shard)
	Scheduling       hyperv1.EtcdShardSchedulingSpec
	IsDefault        bool
}

// EffectiveShards returns the full list of etcd shards for a managed etcd configuration.
// It always synthesizes a default shard from the top-level Storage field that handles
// all resources ("/"), then appends any explicitly configured non-default shards.
// When no shards are configured, the returned list contains only the default shard,
// which is identical to today's single-etcd behavior.
func EffectiveShards(managed *hyperv1.ManagedEtcdSpec) []ManagedEffectiveShard {
	if managed == nil {
		return nil
	}

	defaultStorageType := hyperv1.PersistentVolumeEtcdShardStorage
	var defaultPV hyperv1.ManagedEtcdShardPersistentVolumeSpec
	if managed.Storage.PersistentVolume != nil && managed.Storage.PersistentVolume.StorageClassName != nil {
		defaultPV = hyperv1.ManagedEtcdShardPersistentVolumeSpec{
			StorageClassName: *managed.Storage.PersistentVolume.StorageClassName,
		}
	}

	shards := []ManagedEffectiveShard{
		{
			Name:             "etcd",
			ResourcePrefixes: []string{"/"},
			Storage: hyperv1.ManagedEtcdShardStorageSpec{
				Type:             defaultStorageType,
				PersistentVolume: defaultPV,
			},
			Scheduling: managed.Scheduling,
			IsDefault:  true,
		},
	}

	for _, s := range managed.Shards {
		var prefixes []string
		for _, r := range s.Resources {
			prefixes = append(prefixes, resourcePrefix(r))
		}
		shards = append(shards, ManagedEffectiveShard{
			Name:             fmt.Sprintf("etcd-%s", s.Name),
			ResourcePrefixes: prefixes,
			Storage:          s.Storage,
			Replicas:         s.Replicas,
			Scheduling:       s.Scheduling,
			IsDefault:        false,
		})
	}

	return shards
}

// UnmanagedEffectiveShard represents a resolved etcd shard for unmanaged etcd.
type UnmanagedEffectiveShard struct {
	Name             string
	ResourcePrefixes []string
	Endpoint         string
	IsDefault        bool
}

// UnmanagedEffectiveShards returns the full list of etcd shards for unmanaged etcd.
func UnmanagedEffectiveShards(unmanaged *hyperv1.UnmanagedEtcdSpec) []UnmanagedEffectiveShard {
	if unmanaged == nil {
		return nil
	}

	shards := []UnmanagedEffectiveShard{
		{
			Name:             "etcd",
			ResourcePrefixes: []string{"/"},
			Endpoint:         unmanaged.Endpoint,
			IsDefault:        true,
		},
	}

	for _, s := range unmanaged.Shards {
		var prefixes []string
		for _, r := range s.Resources {
			prefixes = append(prefixes, resourcePrefix(r))
		}
		shards = append(shards, UnmanagedEffectiveShard{
			Name:             fmt.Sprintf("etcd-%s", s.Name),
			ResourcePrefixes: prefixes,
			Endpoint:         s.Endpoint,
			IsDefault:        false,
		})
	}

	return shards
}

// resourcePrefix converts an EtcdShardResource to the format expected by
// --etcd-servers-overrides: "group/resource".
func resourcePrefix(r hyperv1.EtcdShardResource) string {
	if r.APIGroup == nil || *r.APIGroup == "" {
		return fmt.Sprintf("/%s", r.Resource)
	}
	return fmt.Sprintf("%s/%s", *r.APIGroup, r.Resource)
}

// ClientServiceName returns the client service name for a shard.
// shardName must be "etcd" (the default shard) or "etcd-<suffix>" (a named shard).
func ClientServiceName(shardName string) string {
	if shardName == "etcd" {
		return "etcd-client"
	}
	suffix, ok := strings.CutPrefix(shardName, "etcd-")
	if !ok {
		panic(fmt.Sprintf("ClientServiceName: shardName %q must start with 'etcd-'", shardName))
	}
	return fmt.Sprintf("etcd-client-%s", suffix)
}

// DiscoveryServiceName returns the discovery service name for a shard.
// shardName must be "etcd" (the default shard) or "etcd-<suffix>" (a named shard).
func DiscoveryServiceName(shardName string) string {
	if shardName == "etcd" {
		return "etcd-discovery"
	}
	suffix, ok := strings.CutPrefix(shardName, "etcd-")
	if !ok {
		panic(fmt.Sprintf("DiscoveryServiceName: shardName %q must start with 'etcd-'", shardName))
	}
	return fmt.Sprintf("etcd-discovery-%s", suffix)
}
