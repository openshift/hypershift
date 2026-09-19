package core

import (
	"fmt"
	"strconv"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/utils/ptr"
)

// etcdShardFlagHelp documents the --etcd-shard flag. Kept as a package level
// constant so the CLI help text and the parser stay in sync.
const etcdShardFlagHelp = `Configure an additional managed etcd shard that serves a subset of resource types. ` +
	`Requires the EtcdSharding feature gate on the management cluster. ` +
	`Format: "name=<shard>,resources=<apiGroup>/<resource>[;<apiGroup>/<resource>...][,replicas=1|3][,storage=PersistentVolume|EmptyDir][,storageClassName=<class>]". ` +
	`Use an empty apiGroup for the core group, e.g. "/events". ` +
	`Example: --etcd-shard "name=events,resources=/events;events.k8s.io/events,replicas=3,storage=EmptyDir". ` +
	`Can be specified multiple times to create multiple shards.`

// parseEtcdShards converts the raw --etcd-shard flag values into shard specs.
// Every entry is a comma separated list of key=value pairs; the resources key
// takes a semicolon separated list of "<apiGroup>/<resource>" pairs. It returns
// an error when a key is unknown, a required key is missing, a value fails to
// parse, or two shards share a name or a resource.
func parseEtcdShards(items []string) ([]hyperv1.ManagedEtcdShardSpec, error) {
	if len(items) == 0 {
		return nil, nil
	}

	shards := make([]hyperv1.ManagedEtcdShardSpec, 0, len(items))
	seenNames := map[string]struct{}{}
	seenResources := map[string]string{}

	for _, item := range items {
		shard, err := parseEtcdShard(item)
		if err != nil {
			return nil, err
		}
		if _, ok := seenNames[shard.Name]; ok {
			return nil, fmt.Errorf("invalid etcd shard %q: duplicate shard name %q", item, shard.Name)
		}
		seenNames[shard.Name] = struct{}{}

		for _, resource := range shard.Resources {
			key := fmt.Sprintf("%s/%s", ptr.Deref(resource.APIGroup, ""), resource.Resource)
			if other, ok := seenResources[key]; ok {
				return nil, fmt.Errorf("invalid etcd shard %q: resource %q is already routed to shard %q; resources must not overlap across shards", item, key, other)
			}
			seenResources[key] = shard.Name
		}

		shards = append(shards, shard)
	}

	return shards, nil
}

// parseEtcdShard parses a single --etcd-shard value. Defaults are replicas=3
// and storage inherited from the parent spec.etcd.managed.storage.
func parseEtcdShard(item string) (hyperv1.ManagedEtcdShardSpec, error) {
	shard := hyperv1.ManagedEtcdShardSpec{
		Replicas: 3,
	}

	var storageClassName string
	for _, field := range strings.Split(item, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		key, value, found := strings.Cut(field, "=")
		if !found {
			return shard, fmt.Errorf("invalid etcd shard %q: field %q is not in key=value form", item, field)
		}
		switch key {
		case "name":
			shard.Name = value
		case "resources":
			resources, err := parseEtcdShardResources(value)
			if err != nil {
				return shard, fmt.Errorf("invalid etcd shard %q: %w", item, err)
			}
			shard.Resources = resources
		case "replicas":
			replicas, err := strconv.ParseInt(value, 10, 32)
			if err != nil {
				return shard, fmt.Errorf("invalid etcd shard %q: replicas %q is not a number: %w", item, value, err)
			}
			if replicas != 1 && replicas != 3 {
				return shard, fmt.Errorf("invalid etcd shard %q: replicas must be 1 or 3, got %d", item, replicas)
			}
			shard.Replicas = int32(replicas)
		case "storage":
			switch hyperv1.ManagedEtcdShardStorageType(value) {
			case hyperv1.PersistentVolumeEtcdShardStorage, hyperv1.EmptyDirEtcdShardStorage:
				shard.Storage.Type = hyperv1.ManagedEtcdShardStorageType(value)
			default:
				return shard, fmt.Errorf("invalid etcd shard %q: storage must be %s or %s, got %q", item,
					hyperv1.PersistentVolumeEtcdShardStorage, hyperv1.EmptyDirEtcdShardStorage, value)
			}
		case "storageClassName":
			storageClassName = value
		default:
			return shard, fmt.Errorf("invalid etcd shard %q: unknown field %q (supported: name, resources, replicas, storage, storageClassName)", item, key)
		}
	}

	if shard.Name == "" {
		return shard, fmt.Errorf("invalid etcd shard %q: name is required", item)
	}
	if len(shard.Resources) == 0 {
		return shard, fmt.Errorf("invalid etcd shard %q: resources is required", item)
	}
	if storageClassName != "" {
		if shard.Storage.Type == "" {
			shard.Storage.Type = hyperv1.PersistentVolumeEtcdShardStorage
		}
		if shard.Storage.Type != hyperv1.PersistentVolumeEtcdShardStorage {
			return shard, fmt.Errorf("invalid etcd shard %q: storageClassName is only valid when storage is %s", item, hyperv1.PersistentVolumeEtcdShardStorage)
		}
		shard.Storage.PersistentVolume.StorageClassName = storageClassName
	}

	return shard, nil
}

// parseEtcdShardResources parses a semicolon separated list of
// "<apiGroup>/<resource>" entries. A leading slash (or an empty apiGroup)
// designates the core API group.
func parseEtcdShardResources(value string) ([]hyperv1.EtcdShardResource, error) {
	var resources []hyperv1.EtcdShardResource
	for _, entry := range strings.Split(value, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		apiGroup, resource, found := strings.Cut(entry, "/")
		if !found {
			return nil, fmt.Errorf("resource %q must be in <apiGroup>/<resource> form; use a leading slash for the core group, e.g. /events", entry)
		}
		if resource == "" {
			return nil, fmt.Errorf("resource %q must specify a resource name after the slash", entry)
		}
		if strings.Contains(resource, "/") {
			return nil, fmt.Errorf("resource %q must contain exactly one slash", entry)
		}
		resources = append(resources, hyperv1.EtcdShardResource{
			APIGroup: ptr.To(apiGroup),
			Resource: resource,
		})
	}
	if len(resources) == 0 {
		return nil, fmt.Errorf("resources must not be empty")
	}
	return resources, nil
}
