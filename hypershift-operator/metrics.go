package main

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

type hypershiftMetrics struct {
	// clusterCreationTime is the time it takes between cluster creation until the first
	// version got successfully rolled out. Technically this is a const, but using a gauge
	// means we do not have to track what we already reported and can just call Set
	// repeatedly with the same value.
	clusterCreationTime *prometheus.GaugeVec

	client crclient.Client

	log logr.Logger
}

func (m *hypershiftMetrics) Start(ctx context.Context) error {
	ticker := time.NewTicker(30 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := m.collect(ctx); err != nil {
				m.log.Error(err, "failed to collect metrics")
			}
		}
	}
}

func (m *hypershiftMetrics) collect(ctx context.Context) error {
	var clusters hyperv1.HostedClusterList
	if err := m.client.List(ctx, &clusters); err != nil {
		return fmt.Errorf("failed to list hostedclusters: %w", err)
	}

	for _, cluster := range clusters.Items {
		if cluster.Status.Version == nil || len(cluster.Status.Version.History) == 0 {
			continue
		}
		completionTime := cluster.Status.Version.History[len(cluster.Status.Version.History)-1].CompletionTime
		if completionTime == nil {
			continue
		}
		m.clusterCreationTime.WithLabelValues(cluster.Namespace + "/" + cluster.Name).Set(completionTime.Sub(cluster.CreationTimestamp.Time).Seconds())
	}

	return nil
}

func newMetrics(client crclient.Client, log logr.Logger) *hypershiftMetrics {
	return &hypershiftMetrics{
		clusterCreationTime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hypershift_cluster_initial_rollout_duration_seconds",
		}, []string{"name"}),
		client: client,
		log:    log,
	}
}

func setupMetrics(mgr manager.Manager) error {
	metrics := newMetrics(mgr.GetClient(), mgr.GetLogger().WithName("metrics"))
	if err := crmetrics.Registry.Register(metrics.clusterCreationTime); err != nil {
		return fmt.Errorf("failed to to register clusterCreationTime metric: %w", err)
	}

	if err := mgr.Add(metrics); err != nil {
		return fmt.Errorf("failed to add metrics runnable to manager: %w", err)
	}

	return nil
}
