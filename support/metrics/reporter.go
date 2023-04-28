package metrics

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

type ObjectDeletionStatus struct {
	ResourceName      string
	ResourceID        string
	HostedClusterName string
	HostedClusterNS   string
	DeletionRetries   *uint64
	Deleted           bool
}

func NewObjectToMarkAsDeleted(resourceName, resourceID, hcName, hcNamespace string) *ObjectDeletionStatus {
	var deletionRetries uint64
	return &ObjectDeletionStatus{
		ResourceName:      resourceName,
		ResourceID:        resourceID,
		HostedClusterName: hcName,
		HostedClusterNS:   hcNamespace,
		Deleted:           false,
		DeletionRetries:   &deletionRetries,
	}
}

func (o *ObjectDeletionStatus) RegisterDeletionTry(reportMetric *prometheus.GaugeVec) {
	*o.DeletionRetries++
	reportMetric.WithLabelValues(
		o.ResourceName,
		o.ResourceID,
		o.HostedClusterName,
		o.HostedClusterNS,
		strconv.FormatBool(o.Deleted),
	).Set(float64(*o.DeletionRetries))

}
