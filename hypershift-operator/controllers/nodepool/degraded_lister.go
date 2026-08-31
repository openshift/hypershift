package nodepool

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	toolscache "k8s.io/client-go/tools/cache"
)

// NewDegradedModeInformerFactory creates a custom informer factory that skips bad NodePools at startup.
// It scans all NodePools once for invalid duration fields and excludes them from the cache.
func NewDegradedModeInformerFactory(
	ctx context.Context,
	badNodePools map[string]struct{},
	log logr.Logger,
) func(toolscache.ListerWatcher, runtime.Object, time.Duration, toolscache.Indexers) toolscache.SharedIndexInformer {
	return func(lw toolscache.ListerWatcher, obj runtime.Object, resync time.Duration, indexers toolscache.Indexers) toolscache.SharedIndexInformer {
		// Only wrap NodePool ListerWatchers
		if _, ok := obj.(*hyperv1.NodePool); !ok {
			return toolscache.NewSharedIndexInformer(lw, obj, resync, indexers)
		}

		// Wrap the ListerWatcher to skip bad NodePools
		wrappedLW := &degradedNodePoolListerWatcher{
			delegate:   lw,
			badKeys:    badNodePools,
			log:        log,
		}
		return toolscache.NewSharedIndexInformer(wrappedLW, obj, resync, indexers)
	}
}

type degradedNodePoolListerWatcher struct {
	delegate toolscache.ListerWatcher
	badKeys  map[string]struct{} // "namespace/name" → struct{}
	log      logr.Logger
}

// List calls the delegate LIST and filters out bad NodePools.
func (d *degradedNodePoolListerWatcher) List(opts metav1.ListOptions) (runtime.Object, error) {
	list, err := d.delegate.List(opts)
	if err != nil {
		return list, err
	}

	npList, ok := list.(*hyperv1.NodePoolList)
	if !ok {
		return list, nil
	}

	filtered := npList.DeepCopy()
	filtered.Items = make([]hyperv1.NodePool, 0, len(npList.Items))

	for _, item := range npList.Items {
		key := fmt.Sprintf("%s/%s", item.Namespace, item.Name)
		if _, isBad := d.badKeys[key]; !isBad {
			filtered.Items = append(filtered.Items, item)
		}
	}

	return filtered, nil
}

// Watch delegates to the wrapped ListerWatcher.
func (d *degradedNodePoolListerWatcher) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	return d.delegate.Watch(opts)
}

// ScanAndIdentifyBadNodePools scans all NodePools at startup and identifies those with invalid duration fields.
// Returns a map of bad NodePool keys ("namespace/name") and logs details about each.
func ScanAndIdentifyBadNodePools(ctx context.Context, dynClient dynamic.Interface, log logr.Logger) (map[string]struct{}, error) {
	badKeys := make(map[string]struct{})

	// NodePool GVR
	npGVR := schema.GroupVersionResource{
		Group:    "hypershift.openshift.io",
		Version:  "v1beta1",
		Resource: "nodepools",
	}

	list, err := dynClient.Resource(npGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return badKeys, fmt.Errorf("list NodePools: %w", err)
	}

	for _, item := range list.Items {
		badFields := findBadDurationFields(&item)
		if len(badFields) == 0 {
			continue
		}

		key := fmt.Sprintf("%s/%s", item.GetNamespace(), item.GetName())
		badKeys[key] = struct{}{}

		// Log ERROR loudly (without customer data)
		log.Error(
			fmt.Errorf("invalid duration field(s)"),
			"NodePool has invalid duration field(s) and will not be reconciled; see logs for details",
			"fields", strings.Join(sortedKeys(badFields), ","),
		)
	}

	return badKeys, nil
}

// findBadDurationFields returns a map of field names to their bad values.
func findBadDurationFields(item *unstructured.Unstructured) map[string]string {
	badFields := make(map[string]string)

	spec, found, _ := unstructured.NestedMap(item.Object, "spec")
	if !found {
		return badFields
	}

	for _, fieldName := range []string{"nodeDrainTimeout", "nodeVolumeDetachTimeout"} {
		val, ok := spec[fieldName]
		if !ok {
			continue
		}

		strVal, ok := val.(string)
		if !ok {
			continue
		}

		if _, err := time.ParseDuration(strVal); err != nil {
			badFields[fieldName] = strVal
		}
	}

	return badFields
}

// formatBadFields formats bad fields for logging.
func formatBadFields(badFields map[string]string) string {
	var parts []string
	for fieldName, badValue := range badFields {
		parts = append(parts, fmt.Sprintf("%s=%q", fieldName, badValue))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// sortedKeys returns sorted keys from a map for deterministic output.
func sortedKeys(m map[string]string) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
