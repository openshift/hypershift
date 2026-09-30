package ntostatus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/openshift/hypershift/pkg/nodepool"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ListPerformanceProfileConfigMaps records one status ConfigMap list attempt without logging API error contents.
func ListPerformanceProfileConfigMaps(ctx context.Context, managementClient client.Client, namespace string, requestTimeout time.Duration, logf func(string, ...any)) ([]*corev1.ConfigMap, error) {
	selector := labels.SelectorFromSet(labels.Set{nodepool.NodeTuningGeneratedPerformanceProfileStatusLabel: "true"})
	started := time.Now().UTC()
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	list := &corev1.ConfigMapList{}
	err := managementClient.List(requestCtx, list, client.InNamespace(namespace), client.MatchingLabelsSelector{Selector: selector})
	ended := time.Now().UTC()

	errorClass := "none"
	if err != nil {
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			errorClass = "caller_canceled"
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			errorClass = "caller_deadline"
		case errors.Is(requestCtx.Err(), context.DeadlineExceeded):
			errorClass = "request_deadline"
		case apierrors.IsUnauthorized(err):
			errorClass = "unauthorized"
		case apierrors.IsForbidden(err):
			errorClass = "forbidden"
		case apierrors.IsTimeout(err), apierrors.IsServerTimeout(err):
			errorClass = "api_timeout"
		case apierrors.IsTooManyRequests(err):
			errorClass = "throttled"
		default:
			errorClass = "api_error"
		}
	}

	configMaps := make([]*corev1.ConfigMap, 0, len(list.Items))
	names := make([]string, 0, len(list.Items))
	for index := range list.Items {
		configMap := &list.Items[index]
		configMaps = append(configMaps, configMap)
		names = append(names, configMap.Name)
	}
	logf("NTO status ConfigMap list start=%s end=%s duration=%s namespace=%q selector=%q requestTimeout=%s count=%d names=%q listRV=%q errorClass=%s",
		started.Format(time.RFC3339Nano), ended.Format(time.RFC3339Nano), ended.Sub(started), namespace, selector.String(), requestTimeout, len(configMaps), names, list.ResourceVersion, errorClass)
	if err != nil {
		return nil, fmt.Errorf("NTO status ConfigMap list: %s", errorClass)
	}
	return configMaps, nil
}
