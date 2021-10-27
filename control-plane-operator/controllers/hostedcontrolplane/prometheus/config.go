package prometheus

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

const prometheusConfigFileName = "prometheus.yml"

const dummyConfig = `# example config
global:
  scrape_interval:     15s # By default, scrape targets every 15 seconds.

  # Attach these labels to any time series or alerts when communicating with
  # external systems (federation, remote storage, Alertmanager).
  external_labels:
    monitor: 'codelab-monitor'

# A scrape configuration containing exactly one endpoint to scrape:
# Here it's Prometheus itself.
scrape_configs:
  # The job name is added as a label 'job=<job_name>' to any timeseries scraped from this config.
  - job_name: 'prometheus'

    # Override the global default and scrape targets from this job every 5 seconds.
    scrape_interval: 5s

    static_configs:
      - targets: ['localhost:9090']
`

func ReconcileConfiguration(cm *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[prometheusConfigFileName] = dummyConfig
	ownerRef.ApplyTo(cm)
	return nil
}
