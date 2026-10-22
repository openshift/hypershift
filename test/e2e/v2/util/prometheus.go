//go:build e2ev2

package util

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"time"

	routeclient "github.com/openshift/client-go/route/clientset/versioned"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/transport"

	prometheusapi "github.com/prometheus/client_golang/api"
	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

type sloQuerier interface {
	Query(context.Context, string, time.Time, ...prometheusv1.Option) (model.Value, prometheusv1.Warnings, error)
}

func querySLOAlerts(ctx context.Context, client sloQuerier) (model.Vector, error) {
	result, _, err := client.Query(ctx, `sort_desc(count_over_time(ALERTS{alertstate="firing",severity="slo",alertname!~"HypershiftSLO"}[10000s:1s])) > 0`, time.Now())
	if err != nil {
		return nil, err
	}
	vector, ok := result.(model.Vector)
	if !ok {
		return nil, fmt.Errorf("SLO alert query returned %T instead of a vector", result)
	}
	return vector, nil
}

// ManagementSLOAlerts queries recently firing management-cluster SLO alerts.
// It returns errors instead of panicking for missing monitoring resources.
func ManagementSLOAlerts(ctx context.Context) (model.Vector, error) {
	config, err := GetConfig()
	if err != nil {
		return nil, err
	}
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	routes, err := routeclient.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	client, err := newManagementPrometheusClient(ctx, kubeClient, routes)
	if err != nil {
		return nil, err
	}
	return querySLOAlerts(ctx, client)
}

func newManagementPrometheusClient(ctx context.Context, kubeClient kubernetes.Interface, routes routeclient.Interface) (prometheusv1.API, error) {
	route, err := routes.RouteV1().Routes("openshift-monitoring").Get(ctx, "thanos-querier", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if len(route.Status.Ingress) == 0 || route.Status.Ingress[0].Host == "" {
		return nil, fmt.Errorf("management Thanos route has no admitted hostname")
	}
	host := route.Status.Ingress[0].Host
	expiration := int64(time.Hour / time.Second)
	token, err := kubeClient.CoreV1().ServiceAccounts("openshift-monitoring").CreateToken(ctx, "prometheus-k8s", &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: &expiration},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	if token.Status.Token == "" {
		return nil, fmt.Errorf("management Prometheus service account token is empty")
	}
	ca, err := kubeClient.CoreV1().ConfigMaps("openshift-config-managed").Get(ctx, "default-ingress-cert", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(ca.Data["ca-bundle.crt"])) {
		return nil, fmt.Errorf("management ingress certificate bundle is invalid")
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: host, MinVersion: tls.VersionTLS12}
	base.ResponseHeaderTimeout = 30 * time.Second
	client, err := prometheusapi.NewClient(prometheusapi.Config{
		Address:      "https://" + host,
		RoundTripper: transport.NewBearerAuthRoundTripper(token.Status.Token, base),
	})
	if err != nil {
		return nil, err
	}
	return prometheusv1.NewAPI(client), nil
}
