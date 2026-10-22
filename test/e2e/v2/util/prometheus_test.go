//go:build e2ev2

package util

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	routev1 "github.com/openshift/api/route/v1"
	routeclient "github.com/openshift/client-go/route/clientset/versioned"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	kubetesting "k8s.io/client-go/testing"

	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

type sloQueryFixture struct {
	value model.Value
	err   error
	query string
}

func TestNewManagementPrometheusClient(t *testing.T) {
	server := httptest.NewTLSServer(nil)
	defer server.Close()
	certificate := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	for _, test := range []struct {
		name      string
		ingress   []routev1.RouteIngress
		token     string
		bundle    string
		wantError bool
	}{
		{name: "When Thanos has no route ingress, it should return an error instead of panicking", wantError: true},
		{name: "When the route hostname is empty, it should reject the unready route", ingress: []routev1.RouteIngress{{}}, wantError: true},
		{name: "When the service account token is empty, it should reject authentication", ingress: []routev1.RouteIngress{{Host: "thanos.apps.hypershift.example.com"}}, wantError: true},
		{name: "When the ingress trust bundle is invalid, it should reject TLS configuration", ingress: []routev1.RouteIngress{{Host: "thanos.apps.hypershift.example.com"}}, token: "unit-test-placeholder", bundle: "invalid", wantError: true},
		{name: "When monitoring authentication and trust are configured, it should build a native client", ingress: []routev1.RouteIngress{{Host: "thanos.apps.hypershift.example.com"}}, token: "unit-test-placeholder", bundle: certificate},
	} {
		t.Run(test.name, func(t *testing.T) {
			routeServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodGet || request.URL.Path != "/apis/route.openshift.io/v1/namespaces/openshift-monitoring/routes/thanos-querier" {
					http.Error(writer, "unexpected route request", http.StatusNotFound)
					return
				}
				writer.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(writer).Encode(&routev1.Route{
					TypeMeta:   metav1.TypeMeta{APIVersion: "route.openshift.io/v1", Kind: "Route"},
					ObjectMeta: metav1.ObjectMeta{Name: "thanos-querier", Namespace: "openshift-monitoring"},
					Status:     routev1.RouteStatus{Ingress: test.ingress},
				}); err != nil {
					t.Errorf("encoding local route fixture: %v", err)
				}
			}))
			defer routeServer.Close()
			routes, err := routeclient.NewForConfig(&rest.Config{Host: routeServer.URL})
			if err != nil {
				t.Fatal(err)
			}
			kubeClient := kubefake.NewSimpleClientset(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "default-ingress-cert", Namespace: "openshift-config-managed"}, Data: map[string]string{"ca-bundle.crt": test.bundle}})
			kubeClient.PrependReactor("create", "serviceaccounts", func(action kubetesting.Action) (bool, runtime.Object, error) {
				if action.GetSubresource() != "token" {
					t.Fatal("unexpected service account operation")
				}
				return true, &authenticationv1.TokenRequest{Status: authenticationv1.TokenRequestStatus{Token: test.token}}, nil
			})
			client, err := newManagementPrometheusClient(t.Context(), kubeClient, routes)
			if (err != nil) != test.wantError || !test.wantError && client == nil {
				t.Fatalf("unexpected monitoring client result: %v", err)
			}
		})
	}
}

func (fixture *sloQueryFixture) Query(_ context.Context, query string, _ time.Time, _ ...prometheusv1.Option) (model.Value, prometheusv1.Warnings, error) {
	fixture.query = query
	return fixture.value, nil, fixture.err
}

func TestQuerySLOAlerts(t *testing.T) {
	for _, test := range []struct {
		name      string
		value     model.Value
		err       error
		wantError bool
	}{
		{name: "When alerts fire, it should return their observed vector", value: model.Vector{&model.Sample{Metric: model.Metric{"alertname": "KubeAPIDown"}, Value: 60}}},
		{name: "When no alerts fire, it should return an empty vector", value: model.Vector{}},
		{name: "When the server returns an unexpected result type, it should report the error", value: &model.Scalar{}, wantError: true},
		{name: "When the query fails, it should return its error", err: errors.New("query unavailable"), wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := &sloQueryFixture{value: test.value, err: test.err}
			alerts, err := querySLOAlerts(t.Context(), fixture)
			if (err != nil) != test.wantError {
				t.Fatalf("unexpected query error: %v", err)
			}
			if test.err != nil && !errors.Is(err, test.err) {
				t.Fatalf("query error was lost: %v", err)
			}
			if !strings.Contains(fixture.query, `severity="slo"`) || !strings.Contains(fixture.query, `alertname!~"HypershiftSLO"`) || !strings.Contains(fixture.query, "[10000s:1s]") {
				t.Fatalf("unexpected SLO scope: %s", fixture.query)
			}
			if !test.wantError && len(alerts) != len(test.value.(model.Vector)) {
				t.Fatal("query results were lost")
			}
		})
	}
}

func TestManagementSLOAlerts(t *testing.T) {
	t.Run("When management configuration is malformed, it should return an error without querying", func(t *testing.T) {
		t.Setenv("KUBECONFIG", t.TempDir())
		if _, err := ManagementSLOAlerts(t.Context()); err == nil {
			t.Fatal("expected configuration error")
		}
	})
}
