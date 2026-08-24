package tracing

import (
	"strings"

	"go.opentelemetry.io/otel/attribute"
)

// Span names for top-level controller operations.
const (
	// SpanHostedClusterReconcile is the root span for each HostedCluster reconcile loop.
	SpanHostedClusterReconcile = "HostedCluster.Reconcile"
	// SpanHostedClusterDelete is the root span for HostedCluster deletion handling.
	SpanHostedClusterDelete = "HostedCluster.Delete"
	// SpanNodePoolReconcile is the root span for each NodePool reconcile loop.
	SpanNodePoolReconcile = "NodePool.Reconcile"
)

// ReconcileSubSpan returns a span name for a named sub-operation within a reconcile loop
// (e.g. "HostedCluster.Reconcile.EnsureKubeconfig").
func ReconcileSubSpan(name string) string {
	return SpanHostedClusterReconcile + "." + name
}

// Span attribute keys for HyperShift tracing. Shared across controllers
// and consumed by Grafana dashboards and TraceQL queries.
const (
	// AttrHostedClusterName identifies the HostedCluster resource name.
	AttrHostedClusterName = attribute.Key("hostedcluster.name")
	// AttrHostedClusterNamespace identifies the HostedCluster resource namespace.
	AttrHostedClusterNamespace = attribute.Key("hostedcluster.namespace")
	// AttrHostedClusterPlatform identifies the infrastructure platform type (AWS, Azure, etc.).
	AttrHostedClusterPlatform = attribute.Key("hostedcluster.platform")
	// AttrHostedClusterInfraID is the globally unique infrastructure identifier for the cluster (spec.infraID).
	AttrHostedClusterInfraID = attribute.Key("hostedcluster.infraID")
	// AttrHostedClusterClusterID is the immutable RFC4122 UUID for the cluster (spec.clusterID).
	AttrHostedClusterClusterID = attribute.Key("hostedcluster.clusterID")
	// AttrHostedClusterDeleting indicates the HostedCluster has a deletion timestamp set.
	AttrHostedClusterDeleting = attribute.Key("hostedcluster.deleting")

	// AttrNodePoolName identifies the NodePool resource name.
	AttrNodePoolName = attribute.Key("nodepool.name")
	// AttrNodePoolNamespace identifies the NodePool resource namespace.
	AttrNodePoolNamespace = attribute.Key("nodepool.namespace")
	// AttrNodePoolClusterName is the HostedCluster name this NodePool belongs to.
	AttrNodePoolClusterName = attribute.Key("nodepool.clusterName")
	// AttrNodePoolReleaseImage is the OCP release image the NodePool targets.
	AttrNodePoolReleaseImage = attribute.Key("nodepool.releaseImage")
	// AttrNodePoolDeleting indicates the NodePool has a deletion timestamp set.
	AttrNodePoolDeleting = attribute.Key("nodepool.deleting")

	// AttrReconcileOperation names the sub-operation within a reconcile loop.
	AttrReconcileOperation = attribute.Key("reconcile.operation")
	// AttrReconcileCritical indicates the operation is critical and blocks downstream work on failure.
	AttrReconcileCritical = attribute.Key("reconcile.critical")
	// AttrReconcileBlocked indicates the operation was skipped due to a prior critical failure.
	AttrReconcileBlocked = attribute.Key("reconcile.blocked")
)

// correlationAttrKeys holds configurable span attribute keys for cross-service
// correlation. Set via Config.CorrelationAttrs during InitProvider.
var correlationAttrKeys []attribute.Key

// SetCorrelationAttrs configures span attribute keys for cross-service
// correlation from a comma-separated list. Called by InitProvider.
func SetCorrelationAttrs(csv string) {
	correlationAttrKeys = nil
	for _, name := range strings.Split(csv, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			correlationAttrKeys = append(correlationAttrKeys, attribute.Key(name))
		}
	}
}

// CorrelationAttrs returns span attributes for cross-service correlation,
// one per configured key, all set to the given value. Returns nil when no
// keys are configured.
func CorrelationAttrs(value string) []attribute.KeyValue {
	if len(correlationAttrKeys) == 0 {
		return nil
	}
	attrs := make([]attribute.KeyValue, len(correlationAttrKeys))
	for i, key := range correlationAttrKeys {
		attrs[i] = key.String(value)
	}
	return attrs
}
