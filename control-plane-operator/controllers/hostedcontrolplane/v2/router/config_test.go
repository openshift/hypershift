package router

import (
	"context"
	"fmt"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/netutil"
	"github.com/openshift/hypershift/support/testutil"

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGenerateRouterConfig(t *testing.T) {
	const testNS = "test-ns"

	namedRoute := func(r *routev1.Route, mods ...func(*routev1.Route)) *routev1.Route {
		r.Labels = map[string]string{
			netutil.HCPRouteLabel: "test-ns-clustername",
		}
		for _, m := range mods {
			m(r)
		}
		return r
	}
	route := func(name string, mods ...func(*routev1.Route)) *routev1.Route {
		r := &routev1.Route{}
		r.Name = name
		r.Namespace = testNS
		return namedRoute(r, mods...)
	}
	withHost := func(host string) func(*routev1.Route) {
		return func(r *routev1.Route) {
			r.Spec.Host = host
		}
	}
	withSvc := func(svc string) func(*routev1.Route) {
		return func(r *routev1.Route) {
			r.Spec.To.Name = svc
			r.Spec.To.Kind = "Service"
		}
	}
	withPort := func(value int) func(*routev1.Route) {
		return func(r *routev1.Route) {
			r.Spec.Port = &routev1.RoutePort{
				TargetPort: intstr.FromInt(value),
			}
		}
	}

	buildRouteList := func() *routev1.RouteList {
		ignition := route(ignitionserver.Route("").Name, withHost("ignition-server.example.com"), withSvc("ignition-server-proxy"))
		konnectivity := namedRoute(manifests.KonnectivityServerRoute(testNS), withHost("konnectivity.example.com"), withSvc("konnectivity-server"))
		oauthInternal := namedRoute(manifests.OauthServerInternalRoute(testNS), withHost("oauth-internal.example.com"), withSvc("openshift-oauth"))
		oauthExternalPrivate := namedRoute(manifests.OauthServerExternalPrivateRoute(testNS), withHost("oauth-private.example.com"), withSvc("openshift-oauth"))
		oauthExternalPublic := namedRoute(manifests.OauthServerExternalPublicRoute(testNS), withHost("oauth-public.example.com"), withSvc("openshift-oauth"))
		metricsForwarder := route(manifests.MetricsForwarderRoute("").Name, withHost("metrics-forwarder.example.com"), withSvc("metrics-forwarder"), withPort(4000))
		kasPublic := namedRoute(manifests.KubeAPIServerExternalPublicRoute(testNS), withHost("kube-apiserver-public.example.com"), withSvc("kube-apiserver"))
		kasPrivate := namedRoute(manifests.KubeAPIServerExternalPrivateRoute(testNS), withSvc("kube-apiserver-private.example.com"), withSvc("kube-apiserver"))

		return &routev1.RouteList{
			Items: []routev1.Route{*ignition, *konnectivity, *oauthInternal, *oauthExternalPrivate, *oauthExternalPublic, *metricsForwarder, *kasPublic, *kasPrivate},
		}
	}

	buildSvcsNameToIP := func(routeList *routev1.RouteList) map[string]string {
		svcsNameToIP := make(map[string]string)
		i := 0
		for _, r := range routeList.Items {
			svcsNameToIP[r.Spec.To.Name] = fmt.Sprintf("0.0.0.%v", i)
			i++
		}
		return svcsNameToIP
	}

	t.Run("When using default config it should use port 8443", func(t *testing.T) {
		routeList := buildRouteList()
		svcsNameToIP := buildSvcsNameToIP(routeList)

		cfg, err := generateRouterConfig(routeList, svcsNameToIP, "")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		testutil.CompareWithFixture(t, cfg)
	})

	t.Run("When Azure KMS is configured it should include keyvault backend", func(t *testing.T) {
		routeList := buildRouteList()
		svcsNameToIP := buildSvcsNameToIP(routeList)

		cfg, err := generateRouterConfig(routeList, svcsNameToIP, "my-keyvault.vault.azure.net")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		testutil.CompareWithFixture(t, cfg)
	})
}

// TestAdaptConfigSkipsRoutesWithMissingService asserts that a Route whose backing
// Service does not exist yet is skipped instead of failing the whole reconcile.
func TestAdaptConfigSkipsRoutesWithMissingService(t *testing.T) {
	const testNS = "test-ns"

	scheme := runtime.NewScheme()
	if err := routev1.Install(scheme); err != nil {
		t.Fatalf("install Route scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("install core scheme: %v", err)
	}

	route := func(name, svcName string) *routev1.Route {
		return &routev1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNS,
				Labels:    map[string]string{netutil.HCPRouteLabel: "test-ns-clustername"},
			},
			Spec: routev1.RouteSpec{
				Host: name + ".example.com",
				To:   routev1.RouteTargetReference{Kind: "Service", Name: svcName},
			},
		}
	}

	objects := []runtime.Object{
		// Backed by an existing Service.
		route(manifests.KonnectivityServerRoute(testNS).Name, "konnectivity-server"),
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "konnectivity-server", Namespace: testNS},
			Spec:       corev1.ServiceSpec{ClusterIP: "172.30.0.1"},
		},
		// Deliberately has no backing Service yet.
		route(manifests.ConsoleRoute(testNS).Name, "console"),
	}

	cpContext := component.WorkloadContext{
		Context: context.Background(),
		Client:  fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build(),
		HCP: &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Name: "clustername", Namespace: testNS},
		},
	}

	cm := &corev1.ConfigMap{Data: map[string]string{}}
	if err := adaptConfig(cpContext, cm); err != nil {
		t.Fatalf("expected missing Service to be tolerated, got error: %v", err)
	}

	cfg := cm.Data[routerConfigKey]
	if !strings.Contains(cfg, "172.30.0.1") {
		t.Errorf("expected config to route the konnectivity backend to its ClusterIP, got:\n%s", cfg)
	}
}
