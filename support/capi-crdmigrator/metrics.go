package capicrdmigrator

import (
	"context"
	"encoding/json"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	capiMigrationTotalCRDsDesc = prometheus.NewDesc(
		"hypershift_capi_migration_total_crds",
		"Total number of CAPI CRDs configured for storage version migration.",
		nil, nil,
	)
	capiMigrationMigratedCRDsDesc = prometheus.NewDesc(
		"hypershift_capi_migration_migrated_crds",
		"Number of CAPI CRDs that have completed storage version migration.",
		nil, nil,
	)
	capiMigrationCollectorSuccessDesc = prometheus.NewDesc(
		"hypershift_capi_migration_collector_success",
		"Whether the CAPI migration metrics collector was able to collect metrics successfully. 1 for success, 0 for failure.",
		nil, nil,
	)
)

type migrationMetricsCollector struct {
	client    client.Reader
	namespace string
}

func RegisterMigrationMetrics(c client.Reader, namespace string) {
	metrics.Registry.MustRegister(&migrationMetricsCollector{
		client:    c,
		namespace: namespace,
	})
}

func (c *migrationMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- capiMigrationTotalCRDsDesc
	ch <- capiMigrationMigratedCRDsDesc
	ch <- capiMigrationCollectorSuccessDesc
}

func (c *migrationMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	log := ctrllog.Log

	cm := &corev1.ConfigMap{}
	if err := c.client.Get(context.Background(), client.ObjectKey{
		Namespace: c.namespace,
		Name:      StatusConfigMapName,
	}, cm); err != nil {
		log.V(4).Info("Failed to get migration status ConfigMap for metrics", "error", err)
		ch <- prometheus.MustNewConstMetric(capiMigrationCollectorSuccessDesc, prometheus.GaugeValue, 0)
		return
	}

	data, ok := cm.Data[statusDataKey]
	if !ok {
		ch <- prometheus.MustNewConstMetric(capiMigrationCollectorSuccessDesc, prometheus.GaugeValue, 0)
		return
	}

	var status CAPIMigrationStatus
	if err := json.Unmarshal([]byte(data), &status); err != nil {
		log.Error(err, "Failed to unmarshal migration status for metrics")
		ch <- prometheus.MustNewConstMetric(capiMigrationCollectorSuccessDesc, prometheus.GaugeValue, 0)
		return
	}

	ch <- prometheus.MustNewConstMetric(capiMigrationTotalCRDsDesc, prometheus.GaugeValue, float64(status.TotalCRDs))
	ch <- prometheus.MustNewConstMetric(capiMigrationMigratedCRDsDesc, prometheus.GaugeValue, float64(status.MigratedCRDs))
	ch <- prometheus.MustNewConstMetric(capiMigrationCollectorSuccessDesc, prometheus.GaugeValue, 1)
}
