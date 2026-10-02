package ntostatus

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
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

	errorClass := classifyListError(ctx, requestCtx, err)

	configMaps := make([]*corev1.ConfigMap, 0, len(list.Items))
	const maxLoggedNames = 5
	names := make([]string, 0, min(len(list.Items), maxLoggedNames))
	for index := range list.Items {
		configMap := &list.Items[index]
		configMaps = append(configMaps, configMap)
		if len(names) < maxLoggedNames {
			names = append(names, configMap.Name)
		}
	}
	logf("NTO status ConfigMap list start=%s end=%s duration=%s namespace=%q selector=%q requestTimeout=%s count=%d names=%q omittedNames=%d listRV=%q errorClass=%s",
		started.Format(time.RFC3339Nano), ended.Format(time.RFC3339Nano), ended.Sub(started), namespace, selector.String(), requestTimeout, len(configMaps), names, len(configMaps)-len(names), list.ResourceVersion, errorClass)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("NTO status ConfigMap list: %s: %w", errorClass, ctx.Err())
		}
		if requestCtx.Err() != nil {
			return nil, fmt.Errorf("NTO status ConfigMap list: %s: %w", errorClass, requestCtx.Err())
		}
		if errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("NTO status ConfigMap list: %s: %w", errorClass, context.Canceled)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("NTO status ConfigMap list: %s: %w", errorClass, context.DeadlineExceeded)
		}
		return nil, fmt.Errorf("NTO status ConfigMap list: %s", errorClass)
	}
	return configMaps, nil
}

func classifyListError(ctx, requestCtx context.Context, err error) string {
	if err == nil {
		return "none"
	}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return "caller_canceled"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "caller_deadline"
	case errors.Is(requestCtx.Err(), context.DeadlineExceeded):
		return "request_deadline"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case apierrors.IsUnauthorized(err):
		return "unauthorized"
	case apierrors.IsForbidden(err):
		return "forbidden"
	case apierrors.IsTimeout(err), apierrors.IsServerTimeout(err):
		return "api_timeout"
	case apierrors.IsTooManyRequests(err):
		return "throttled"
	}
	var status apierrors.APIStatus
	if errors.As(err, &status) && status.Status().Code > 0 {
		return fmt.Sprintf("http_%d", status.Status().Code)
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return "dns_error"
	}
	var tlsError *tls.CertificateVerificationError
	var certificateError x509.UnknownAuthorityError
	var invalidCertificate x509.CertificateInvalidError
	var recordHeaderError tls.RecordHeaderError
	if errors.As(err, &tlsError) || errors.As(err, &certificateError) || errors.As(err, &invalidCertificate) || errors.As(err, &recordHeaderError) {
		return "tls_error"
	}
	var networkError *net.OpError
	if errors.As(err, &networkError) {
		return "network_error"
	}
	return "api_error"
}
