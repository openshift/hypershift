package router

import (
	"fmt"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"

	routev1 "github.com/openshift/api/route/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestGenerateRouterConfig(t *testing.T) {
	const testNS = "test-ns"

	namedRoute := func(r *routev1.Route, mods ...func(*routev1.Route)) *routev1.Route {
		r.Labels = map[string]string{
			util.HCPRouteLabel: "test-ns-clustername",
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

	testCases := []struct {
		name     string
		setupEnv func(t *testing.T)
		hcp      *hyperv1.HostedControlPlane
	}{
		{
			name: "When using default config it should use port 8443",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNS,
				},
			},
		},
		{
			name: "When ARO Swift is enabled it should use port 443",
			setupEnv: func(t *testing.T) {
				azureutil.SetAsAroHCPTest(t)
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNS,
					Annotations: map[string]string{
						hyperv1.SwiftPodNetworkInstanceAnnotation: "test-swift-instance",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setupEnv != nil {
				tc.setupEnv(t)
			}

			routeList := buildRouteList()
			svcsNameToIP := buildSvcsNameToIP(routeList)

			cfg, err := generateRouterConfig(routeList, svcsNameToIP, tc.hcp)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			testutil.CompareWithFixture(t, cfg)
		})
	}
}
