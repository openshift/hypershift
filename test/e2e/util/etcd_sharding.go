package util

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	etcdutil "github.com/openshift/hypershift/support/etcd"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Shard names and resources used by the etcd sharding e2e coverage. The
// "etcd-sharded" cluster variant creates them through the `--etcd-shard` CLI
// flag and the specs in test/e2e/v2/tests/etcd_sharding_test.go assert against
// them.
const (
	// EtcdShardEventsName is the shard holding core and events.k8s.io events.
	// It is backed by EmptyDir because event data is expendable.
	EtcdShardEventsName = "events"
	// EtcdShardLeasesName is the shard holding coordination.k8s.io leases. It
	// is backed by a PersistentVolume so the PVC code path is also covered.
	EtcdShardLeasesName = "leases"

	// KASEtcdPrefix is the value kube-apiserver is configured with via
	// --etcd-prefix. Keys in etcd are therefore "/<KASEtcdPrefix>/<resource>/...".
	KASEtcdPrefix = "kubernetes.io"

	// EtcdContainerName is the name of the etcd container in a shard's pod.
	EtcdContainerName = "etcd"
)

// EtcdShardingTestShards returns the shard specs exercised by the etcd
// sharding e2e tests. When storageClassName is empty the leases shard inherits
// the StorageClass from spec.etcd.managed.storage.
func EtcdShardingTestShards(storageClassName string) []hyperv1.ManagedEtcdShardSpec {
	leasesStorage := hyperv1.ManagedEtcdShardStorageSpec{
		Type: hyperv1.PersistentVolumeEtcdShardStorage,
	}
	if storageClassName != "" {
		leasesStorage.PersistentVolume.StorageClassName = storageClassName
	}
	return []hyperv1.ManagedEtcdShardSpec{
		{
			Name:     EtcdShardEventsName,
			Replicas: 3,
			Resources: []hyperv1.EtcdShardResource{
				{APIGroup: ptr.To(""), Resource: "events"},
				{APIGroup: ptr.To("events.k8s.io"), Resource: "events"},
			},
			Storage: hyperv1.ManagedEtcdShardStorageSpec{
				Type: hyperv1.EmptyDirEtcdShardStorage,
			},
		},
		{
			Name:     EtcdShardLeasesName,
			Replicas: 3,
			Resources: []hyperv1.EtcdShardResource{
				{APIGroup: ptr.To("coordination.k8s.io"), Resource: "leases"},
			},
			Storage: leasesStorage,
		},
	}
}

// EtcdShardingCreateArgs renders EtcdShardingTestShards as `--etcd-shard`
// arguments for `hypershift create cluster`. Keeping this next to the spec
// builder lets the specs assert against EtcdShardingTestShards directly.
func EtcdShardingCreateArgs(storageClassName string) []string {
	var args []string
	for _, shard := range EtcdShardingTestShards(storageClassName) {
		resources := make([]string, 0, len(shard.Resources))
		for _, r := range shard.Resources {
			resources = append(resources, fmt.Sprintf("%s/%s", ptr.Deref(r.APIGroup, ""), r.Resource))
		}
		value := fmt.Sprintf("name=%s,resources=%s,replicas=%d", shard.Name, strings.Join(resources, ";"), shard.Replicas)
		if shard.Storage.Type != "" {
			value += fmt.Sprintf(",storage=%s", shard.Storage.Type)
		}
		if shard.Storage.PersistentVolume.StorageClassName != "" {
			value += fmt.Sprintf(",storageClassName=%s", shard.Storage.PersistentVolume.StorageClassName)
		}
		args = append(args, "--etcd-shard="+value)
	}
	return args
}

// ExpectedEtcdServersOverrides returns the --etcd-servers-overrides entries
// kube-apiserver should be configured with for the given shards, in the order
// the control plane operator generates them.
func ExpectedEtcdServersOverrides(shards []hyperv1.ManagedEtcdShardSpec, controlPlaneNamespace string) []string {
	managed := &hyperv1.ManagedEtcdSpec{Shards: shards}
	var overrides []string
	for _, shard := range etcdutil.EffectiveShards(managed) {
		if shard.IsDefault {
			continue
		}
		endpoint := fmt.Sprintf("https://%s.%s.svc:2379", etcdutil.ClientServiceName(shard.Name), controlPlaneNamespace)
		for _, prefix := range shard.ResourcePrefixes {
			overrides = append(overrides, fmt.Sprintf("%s#%s", prefix, endpoint))
		}
	}
	return overrides
}

// KASEtcdServersOverrides reads the generated kube-apiserver config from the
// kas-config ConfigMap in the control plane namespace and returns the value of
// the etcd-servers-overrides argument. An empty slice is returned when the
// argument is not set.
func KASEtcdServersOverrides(ctx context.Context, client crclient.Client, controlPlaneNamespace string) ([]string, error) {
	cm := &corev1.ConfigMap{}
	if err := client.Get(ctx, types.NamespacedName{Namespace: controlPlaneNamespace, Name: "kas-config"}, cm); err != nil {
		return nil, fmt.Errorf("failed to get kas-config ConfigMap: %w", err)
	}
	raw, ok := cm.Data["config.json"]
	if !ok {
		return nil, fmt.Errorf("kas-config ConfigMap in %s has no config.json key", controlPlaneNamespace)
	}
	var config struct {
		APIServerArguments map[string][]string `json:"apiServerArguments"`
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return nil, fmt.Errorf("failed to parse kube-apiserver config: %w", err)
	}
	return config.APIServerArguments["etcd-servers-overrides"], nil
}

// EtcdShardStatefulSetName returns the StatefulSet name for a shard. It also
// doubles as the "app" pod label value and the component name.
func EtcdShardStatefulSetName(shardName string) string {
	return fmt.Sprintf("etcd-%s", shardName)
}

// EtcdKeysWithPrefix execs etcdctl inside the given etcd StatefulSet's first
// pod and returns the sorted keys under keyPrefix. stsName is the StatefulSet
// name, which equals the value of the "app" pod label ("etcd" for the default
// shard, "etcd-<name>" for a shard). It returns an error if the exec fails.
func EtcdKeysWithPrefix(ctx context.Context, client crclient.Client, controlPlaneNamespace, stsName, keyPrefix string) ([]string, error) {
	endpoint := fmt.Sprintf("https://%s.%s.svc:2379", etcdutil.ClientServiceName(stsName), controlPlaneNamespace)
	command := []string{
		"/usr/bin/etcdctl",
		"--cacert=/etc/etcd/tls/etcd-ca/ca.crt",
		"--cert=/etc/etcd/tls/server/server.crt",
		"--key=/etc/etcd/tls/server/server.key",
		"--endpoints=" + endpoint,
		"get", keyPrefix,
		"--prefix",
		"--keys-only",
		"--limit=200",
	}
	out, err := RunCommandInPod(ctx, client, stsName, controlPlaneNamespace, command, EtcdContainerName, 2*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("failed to list etcd keys with prefix %q in %s/%s: %w (output: %s)", keyPrefix, controlPlaneNamespace, stsName, err, out)
	}
	var keys []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		keys = append(keys, line)
	}
	sort.Strings(keys)
	return keys, nil
}

// EtcdKeyCount execs etcdctl inside the given etcd StatefulSet's first pod and
// returns the number of keys under keyPrefix. Unlike EtcdKeysWithPrefix it is
// safe to use on prefixes holding a large number of keys. It returns an error
// if the exec fails or the etcdctl output cannot be parsed.
func EtcdKeyCount(ctx context.Context, client crclient.Client, controlPlaneNamespace, stsName, keyPrefix string) (int, error) {
	endpoint := fmt.Sprintf("https://%s.%s.svc:2379", etcdutil.ClientServiceName(stsName), controlPlaneNamespace)
	command := []string{
		"/usr/bin/etcdctl",
		"--cacert=/etc/etcd/tls/etcd-ca/ca.crt",
		"--cert=/etc/etcd/tls/server/server.crt",
		"--key=/etc/etcd/tls/server/server.key",
		"--endpoints=" + endpoint,
		"get", keyPrefix,
		"--prefix",
		"--count-only",
		"--write-out=fields",
	}
	out, err := RunCommandInPod(ctx, client, stsName, controlPlaneNamespace, command, EtcdContainerName, 2*time.Minute)
	if err != nil {
		return 0, fmt.Errorf("failed to count etcd keys with prefix %q in %s/%s: %w (output: %s)", keyPrefix, controlPlaneNamespace, stsName, err, out)
	}
	// etcdctl -w fields emits lines of the form `"Count" : 42`.
	for _, line := range strings.Split(out, "\n") {
		_, value, found := strings.Cut(line, `"Count" :`)
		if !found {
			continue
		}
		count, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, fmt.Errorf("failed to parse etcdctl count %q: %w", line, err)
		}
		return count, nil
	}
	return 0, fmt.Errorf("etcdctl output for prefix %q in %s/%s has no Count field: %s", keyPrefix, controlPlaneNamespace, stsName, out)
}

// EtcdResourceKeyPrefix returns the etcd key prefix kube-apiserver writes a
// resource under, accounting for the --etcd-prefix setting. Core group
// resources are stored as "/kubernetes.io/<resource>/", non-core resources as
// "/kubernetes.io/<apiGroup>/<resource>/".
func EtcdResourceKeyPrefix(apiGroup, resource string) string {
	if apiGroup == "" {
		return fmt.Sprintf("/%s/%s/", KASEtcdPrefix, resource)
	}
	return fmt.Sprintf("/%s/%s/%s/", KASEtcdPrefix, apiGroup, resource)
}
