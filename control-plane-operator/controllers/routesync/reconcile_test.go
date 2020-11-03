package routesync

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	routev1 "github.com/openshift/api/route/v1"
	routeclient "github.com/openshift/client-go/route/clientset/versioned"
	"github.com/openshift/client-go/route/clientset/versioned/fake"
	routelister "github.com/openshift/client-go/route/listers/route/v1"
)

const (
	testNamespace = "testnamespace"
)

func TestRouteSyncReconcile(t *testing.T) {

	tests := []struct {
		name               string
		targetRoutes       []*routev1.Route
		hostRoutes         []*routev1.Route
		expectedHostRoutes []*routev1.Route
	}{
		{
			name: "create a single route",
			targetRoutes: []*routev1.Route{
				createTestTargetRoute("testroute", "targetnamespace", "test.example.com", nil),
			},
			expectedHostRoutes: []*routev1.Route{
				createTestHostRoute(generateRouteName("targetnamespace-testroute", testNamespace), testNamespace, "test.example.com", httpRouteTarget, nil),
			},
		},
		{
			name: "update existing route",
			targetRoutes: []*routev1.Route{
				createTestTargetRoute("testroute", "targetnamespace", "test.updated-example.com", nil),
			},
			hostRoutes: []*routev1.Route{
				createTestHostRoute(generateRouteName("targetnamespace-testroute", testNamespace), testNamespace, "test.example.com", httpRouteTarget, nil),
			},
			expectedHostRoutes: []*routev1.Route{
				createTestHostRoute(generateRouteName("targetnamespace-testroute", testNamespace), testNamespace, "test.updated-example.com", httpRouteTarget, nil),
			},
		},
		{
			name: "delete extra route",
			hostRoutes: []*routev1.Route{
				createTestHostRoute(generateRouteName("oldnamespace-oldroute", testNamespace), testNamespace, "old.route.com", httpRouteTarget, nil),
			},
			expectedHostRoutes: []*routev1.Route{},
		},
		{
			name: "ignore other host routes",
			targetRoutes: []*routev1.Route{
				createTestTargetRoute("testroute", "targetnamespace", "test.example.com", nil),
			},
			hostRoutes: []*routev1.Route{
				createTestHostRoute(generateRouteName("targetnamespace-testroute", testNamespace), testNamespace, "test.example.com", httpRouteTarget, nil),
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "ignoreme",
						Labels: map[string]string{},
					},
				},
			},
			expectedHostRoutes: []*routev1.Route{
				createTestHostRoute(generateRouteName("targetnamespace-testroute", testNamespace), testNamespace, "test.example.com", httpRouteTarget, nil),
			},
		},
		{
			name: "create https route",
			targetRoutes: []*routev1.Route{
				createTestTargetRoute("testroute", "targetnamespace", "test.example.com", &routev1.TLSConfig{
					Termination:                   routev1.TLSTerminationReencrypt,
					InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyAllow,
				}),
			},
			expectedHostRoutes: []*routev1.Route{
				createTestHostRoute(generateRouteName("targetnamespace-testroute", testNamespace), testNamespace, "test.example.com", httpsRouteTarget, &routev1.TLSConfig{
					InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyAllow,
				}),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hostClient := fake.NewSimpleClientset(routeListToRuntimeList(test.hostRoutes)...)

			targetLister := &fakeRouteLister{
				routes: test.targetRoutes,
			}

			hostLister := &fakeRouteLister{
				routes: test.hostRoutes,
			}

			rsReconciler := &RouteSyncReconciler{
				HostClient:   hostClient,
				Namespace:    testNamespace,
				Log:          log.Log.WithName("testroutesync"),
				TargetLister: targetLister,
				HostLister:   hostLister,
			}

			_, err := rsReconciler.Reconcile(context.TODO(), ctrl.Request{})

			assert.NoError(t, err, "unexpected error during Reconcile()")

			validateRoutes(t, hostClient, test.expectedHostRoutes)
		})

	}
	assert.True(t, true)
}

func validateRoutes(t *testing.T, client routeclient.Interface, expectedRoutes []*routev1.Route) {

	routes, err := client.RouteV1().Routes(testNamespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", routeLabel, testNamespace),
	})
	assert.NoError(t, err, "unexpected error listing routes from fake client")
	assert.Equal(t, len(expectedRoutes), len(routes.Items), "unexpected number of host routes found")

	for _, expectedRoute := range expectedRoutes {
		route, err := client.RouteV1().Routes(testNamespace).Get(context.TODO(), expectedRoute.Name, metav1.GetOptions{})
		assert.NoError(t, err, "unexpected error fetching route from fake client")
		val := routeEqual(route, expectedRoute)
		assert.True(t, val, "expected and actual route do not match")
	}
}

func createTestTargetRoute(name, namespace, host string, tlsConfig *routev1.TLSConfig) *routev1.Route {
	target := routev1.RouteTargetReference{
		Kind: "Service",
		Name: "testservice",
	}

	return createTestHostRoute(name, namespace, host, target, tlsConfig)
}

func createTestHostRoute(name, namespace, host string, specTo routev1.RouteTargetReference, tlsConfig *routev1.TLSConfig) *routev1.Route {
	route := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				routeLabel: testNamespace,
			},
		},
		Spec: routev1.RouteSpec{
			Host: host,
			TLS:  tlsConfig,
			To:   specTo,
		},
	}

	if tlsConfig != nil {
		route.Spec.TLS = &routev1.TLSConfig{
			Termination:                   routev1.TLSTerminationPassthrough,
			InsecureEdgeTerminationPolicy: tlsConfig.InsecureEdgeTerminationPolicy,
		}
	}

	return route
}

func routeListToRuntimeList(routeList []*routev1.Route) []runtime.Object {
	runtimeList := []runtime.Object{}

	for i := range routeList {
		runtimeList = append(runtimeList, routeList[i])
	}
	return runtimeList
}

// implement a minimal route lister for the controller
type fakeRouteLister struct {
	routes []*routev1.Route
}

func (f *fakeRouteLister) List(labels.Selector) ([]*routev1.Route, error) {
	return f.routes, nil
}

func (f *fakeRouteLister) Routes(string) routelister.RouteNamespaceLister {
	panic("Routes() not implemented")
}
