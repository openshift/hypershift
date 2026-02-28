package logcontext

import (
	"strings"

	"github.com/go-logr/logr"
)

// AddAnnotationContext takes annotations, extracts context desired by the service provider, and adds that context to the logger.
// This is useful when the service provider has keys like resourceGroupName, resourceName, hcpClusterName, clusterServiceID
// and wants to be able to select all log lines that contain those keys.
// We use annotations because they can hold more values and are applicable to all resource types.
func AddAnnotationContext(log logr.Logger, annotations map[string]string) logr.Logger {
	for k, v := range annotations {
		if !strings.HasPrefix(k, "context.serviceprovider.hypershift.openshift.io/") {
			continue
		}
		logKey, _ := strings.CutPrefix(k, "context.serviceprovider.hypershift.openshift.io/")

		log = log.WithValues(logKey, v)
	}
	return log
}

// AddServiceProviderAnnotations adds service provider annotations from the hosted cluster (or other authoritative annotations)
// and sets them on the target if they are not already set.  This is an easy way to take serviceprovider annotations used for log
// and metric labeling during aggregated ingestion and placing them on namespaces where they can be accessed.
func AddServiceProviderAnnotations(targetAnnotations map[string]string, hostedClusterAnnotations map[string]string) {
	for k, v := range hostedClusterAnnotations {
		if !strings.HasPrefix(k, "context.serviceprovider.hypershift.openshift.io/") {
			continue
		}
		if _, exists := targetAnnotations[k]; exists {
			continue
		}
		targetAnnotations[k] = v
	}
}
