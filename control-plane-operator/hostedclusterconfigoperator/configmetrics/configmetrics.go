package configmetrics

/*
package configmetrics is mostly a straight copy of https://github.com/openshift/cluster-kube-apiserver-operator/blob/5435d65b520191ba74fb2cd8c84a4f5bdf604178/pkg/operator/configmetrics/configmetrics.go
with some slight adjustments to use a controller-runtime cache rather than a standard informer and to use the controller-runtime metrics registry rather than the client-go legacyregistry.
*/

import (
	"context"

	configv1 "github.com/openshift/api/config/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/blang/semver"
	"github.com/prometheus/client_golang/prometheus"
)

// Register exposes core platform metrics that relate to the configuration
// of Kubernetes.
// TODO: in the future this may move to cluster-config-operator.
func Register(cache crclient.Reader) {
	metrics.Registry.MustRegister(&configMetrics{
		cloudProvider: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_infrastructure_provider",
			Help: "Reports whether the cluster is configured with an infrastructure provider. type is unset if no cloud provider is recognized or set to the constant used by the Infrastructure config. region is set when the cluster clearly identifies a region within the provider. The value is 1 if a cloud provider is set or 0 if it is unset.",
		}, []string{"type", "region"}),
		featureSet: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_feature_set",
			Help: "Reports the feature set the cluster is configured to expose. name corresponds to the featureSet field of the cluster. The value is 1 if a cloud provider is supported.",
		}, []string{"name"}),
		proxyEnablement: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_proxy_enabled",
			Help: "Reports whether the cluster has been configured to use a proxy. type is which type of proxy configuration has been set - http for an http proxy, https for an https proxy, and trusted_ca if a custom CA was specified.",
		}, []string{"type"}),
		cache: cache,
	})
}

// configMetrics implements metrics gathering for this component.
type configMetrics struct {
	cloudProvider   *prometheus.GaugeVec
	featureSet      *prometheus.GaugeVec
	proxyEnablement *prometheus.GaugeVec
	cache           crclient.Reader
}

func (m *configMetrics) Create(version *semver.Version) bool {
	return true
}

// Describe reports the metadata for metrics to the prometheus collector.
func (m *configMetrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.cloudProvider.WithLabelValues("", "").Desc()
	ch <- m.featureSet.WithLabelValues("").Desc()
	ch <- m.proxyEnablement.WithLabelValues("").Desc()
}

// Collect calculates metrics from the cached config and reports them to the prometheus collector.
func (m *configMetrics) Collect(ch chan<- prometheus.Metric) {
	infra := &configv1.Infrastructure{}
	if err := m.cache.Get(context.Background(), crclient.ObjectKey{Name: "cluster"}, infra); err == nil {
		if status := infra.Status.PlatformStatus; status != nil {
			var g prometheus.Gauge
			var value float64 = 1
			switch {
			// it is illegal to set type to empty string, so let the default case handle
			// empty string (so we can detect it) while preserving the constant None here
			case status.Type == configv1.NonePlatformType:
				g = m.cloudProvider.WithLabelValues(string(status.Type), "")
				value = 0
			case status.AWS != nil:
				g = m.cloudProvider.WithLabelValues(string(status.Type), status.AWS.Region)
			case status.GCP != nil:
				g = m.cloudProvider.WithLabelValues(string(status.Type), status.GCP.Region)
			default:
				g = m.cloudProvider.WithLabelValues(string(status.Type), "")
			}
			g.Set(value)
			ch <- g
		}
	}
	features := &configv1.FeatureGate{}
	if err := m.cache.Get(context.Background(), crclient.ObjectKey{Name: "cluster"}, features); err == nil {
		ch <- booleanGaugeValue(
			m.featureSet.WithLabelValues(string(features.Spec.FeatureSet)),
			features.Spec.FeatureSet == configv1.Default,
		)
	}
	proxy := &configv1.Proxy{}
	if err := m.cache.Get(context.Background(), crclient.ObjectKey{Name: "cluster"}, proxy); err == nil {
		ch <- booleanGaugeValue(m.proxyEnablement.WithLabelValues("http"), len(proxy.Spec.HTTPProxy) > 0)
		ch <- booleanGaugeValue(m.proxyEnablement.WithLabelValues("https"), len(proxy.Spec.HTTPSProxy) > 0)
		ch <- booleanGaugeValue(m.proxyEnablement.WithLabelValues("trusted_ca"), len(proxy.Spec.TrustedCA.Name) > 0)
	}
}

func booleanGaugeValue(g prometheus.Gauge, value bool) prometheus.Gauge {
	if value {
		g.Set(1)
	} else {
		g.Set(0)
	}
	return g
}

func (m *configMetrics) ClearState() {}

func (m *configMetrics) FQName() string {
	return "cluster_kube_apiserver_operator"
}
