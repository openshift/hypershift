package common

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	UnremovalAWSObjectsName            = "hypershift_cluster_skipped_object_deletion"
	HostedClusterSkippedObjectDeletion = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Help: "Times the controller tried to delete a AWS Cloud resource without luck",
		Name: UnremovalAWSObjectsName,
	}, []string{"name", "id", "hostedcluster_name", "hostedcluster_namespace", "deleted"})
)
