package routesync

import (
	"context"
	"fmt"
	"reflect"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	errorsutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/pointer"

	ctrl "sigs.k8s.io/controller-runtime"

	routev1 "github.com/openshift/api/route/v1"
	routeclient "github.com/openshift/client-go/route/clientset/versioned"
	routelister "github.com/openshift/client-go/route/listers/route/v1"
)

const (
	httpServiceName  = "router-http"
	httpsServiceName = "router-https"

	routeLabel = "hypershift.openshift.io/cluster"
)

var (
	httpRouteTarget = routev1.RouteTargetReference{
		Kind:   "Service",
		Name:   httpServiceName,
		Weight: pointer.Int32Ptr(100),
	}
	httpsRouteTarget = routev1.RouteTargetReference{
		Kind:   "Service",
		Name:   httpsServiceName,
		Weight: pointer.Int32Ptr(100),
	}
)

type processRoute string

const (
	skipRoute   processRoute = "skip"
	updateRoute processRoute = "update"
	createRoute processRoute = "create"
)

// RouteSyncReconciler holds the fields necessary to run the Route Sync reconciliation
type RouteSyncReconciler struct {
	HostClient   routeclient.Interface
	Namespace    string // Note: the target cluster name and the namespace it resides in are the same
	TargetLister routelister.RouteLister
	HostLister   routelister.RouteLister
	Log          logr.Logger
}

// Reconcile will react to any Route change and reconcile all the remote target routes
// against the local host list of routes. It will create local host routes and point them
// to the appropriate proxy service for delivery into the target cluster's routers.
func (r *RouteSyncReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Log.Info("Reconciling target routes")

	targetRoutes, err := r.TargetLister.List(labels.Everything())
	if err != nil {
		r.Log.Error(err, "failed to list target cluster routes")
		return ctrl.Result{}, err
	}

	currentHostRoutes, err := r.HostLister.List(labels.Everything())
	if err != nil {
		r.Log.Error(err, "failed to list host routes")
		return ctrl.Result{}, err
	}

	routesToCreate := []*routev1.Route{}
	routesToUpdate := []*routev1.Route{}
	routesToSkip := []*routev1.Route{}
	routesToDelete := []*routev1.Route{}

	// Walk through each remote target route, and identify which needs
	// creation, update, or skipping.
	for _, targetRoute := range targetRoutes {
		syncRoute, action := r.createSyncRouteFromTarget(currentHostRoutes, targetRoute)

		switch action {
		case createRoute:
			routesToCreate = append(routesToCreate, syncRoute)
		case updateRoute:
			routesToUpdate = append(routesToUpdate, syncRoute)
		case skipRoute:
			routesToSkip = append(routesToSkip, syncRoute)
		}
	}

	allProposedHostRoutes := listOfAllRoutes(routesToCreate, routesToUpdate, routesToSkip)
	// Walk through each local host route and identify any that need to be removed.
	for i, hostRoute := range currentHostRoutes {
		if hostRoute.Labels[routeLabel] != r.Namespace {
			// ignore routes that this controller isn't responsible for
			continue
		}
		if hostRouteNeedsDeletion(allProposedHostRoutes, hostRoute) {
			routesToDelete = append(routesToDelete, currentHostRoutes[i])
		}
	}

	errorList := []error{}
	for _, route := range routesToCreate {
		r.Log.Info("Will create route", "route", route.Name)
		_, err := r.HostClient.RouteV1().Routes(r.Namespace).Create(ctx, route, metav1.CreateOptions{})
		// ignore if it already exists error to not clog up the logs
		// when starting the controller
		if err != nil && !errors.IsAlreadyExists(err) {
			r.Log.Error(err, "failed to create route")
			errorList = append(errorList, err)
		} else if errors.IsAlreadyExists(err) {
			r.Log.Info("route already exists", "route", route.Name)
		}
	}
	for _, route := range routesToUpdate {
		r.Log.Info("Will update route", "route", route.Name)
		_, err := r.HostClient.RouteV1().Routes(r.Namespace).Update(ctx, route, metav1.UpdateOptions{})
		if err != nil {
			r.Log.Error(err, "failed to update route")
			errorList = append(errorList, err)
		}
	}
	for _, route := range routesToDelete {
		r.Log.Info("Will delete route", "route", route.Name)
		err := r.HostClient.RouteV1().Routes(r.Namespace).Delete(ctx, route.Name, metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			r.Log.Error(err, "failed to delete route")
			errorList = append(errorList, err)
		}
	}

	return ctrl.Result{}, errorsutil.NewAggregate(errorList)
}

func hostRouteNeedsDeletion(proposedHostRoutes []*routev1.Route, hostRoute *routev1.Route) bool {
	for _, route := range proposedHostRoutes {
		if route.Name == hostRoute.Name {
			return false
		}
	}
	return true
}

func (r *RouteSyncReconciler) createSyncRouteFromTarget(currentRoutes []*routev1.Route, tRoute *routev1.Route) (*routev1.Route, processRoute) {
	targetRouteNamespacedName := fmt.Sprintf("%s-%s", tRoute.Namespace, tRoute.Name)
	syncedRouteName := generateRouteName(targetRouteNamespacedName, r.Namespace)

	var existingRoute *routev1.Route

	for i, cRoute := range currentRoutes {
		if cRoute.Name == syncedRouteName {
			existingRoute = currentRoutes[i].DeepCopy()
			break
		}
	}

	sRoute := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      syncedRouteName,
			Namespace: r.Namespace,
			Labels: map[string]string{
				routeLabel: r.Namespace,
			},
		},
	}

	sRoute.Spec = routev1.RouteSpec{
		Host: tRoute.Spec.Host,
	}

	if tRoute.Spec.TLS == nil {
		sRoute.Spec.To = httpRouteTarget
	} else {
		sRoute.Spec.To = httpsRouteTarget
		sRoute.Spec.TLS = &routev1.TLSConfig{
			// Always pass through to the target cluster's route and allow the target
			// cluster to terminate as if traffic had been directed to it.
			Termination: routev1.TLSTerminationPassthrough,
			// Use the same edge termination policy as the target route
			InsecureEdgeTerminationPolicy: tRoute.Spec.TLS.InsecureEdgeTerminationPolicy,
		}
	}

	if existingRoute == nil {
		// No existing route, so we need to create a new one.
		return sRoute, createRoute
	}

	if routeEqual(existingRoute, sRoute) {
		// Already up to date, so skip.
		return existingRoute, skipRoute
	}

	existingRoute.Spec = sRoute.Spec
	return existingRoute, updateRoute
}

func generateRouteName(routeName, clusterName string) string {
	suffix := fmt.Sprintf("%s-%s", clusterName, routeName)
	return GetResourceName("childroute", suffix)
}

// routeEqual is a custom route equality comparison that only looks at the fields that this
// controller sets.
func routeEqual(route1, route2 *routev1.Route) bool {

	if route1.Labels[routeLabel] != route2.Labels[routeLabel] {
		return false
	}

	if route1.Spec.Host != route2.Spec.Host {
		return false
	}

	if !reflect.DeepEqual(route1.Spec.To, route2.Spec.To) {
		return false
	}

	if !reflect.DeepEqual(route1.Spec.TLS, route2.Spec.TLS) {
		return false
	}

	return true
}

func listOfAllRoutes(routesToCreate, routesToUpdate, routesToSkip []*routev1.Route) []*routev1.Route {
	allRoutes := make([]*routev1.Route, len(routesToCreate)+len(routesToUpdate)+len(routesToSkip))
	for i := range routesToCreate {
		allRoutes[i] = routesToCreate[i]
	}
	curLength := len(routesToCreate)
	for i := range routesToUpdate {
		allRoutes[curLength+i] = routesToUpdate[i]
	}
	curLength = curLength + len(routesToUpdate)
	for i := range routesToSkip {
		allRoutes[curLength+i] = routesToSkip[i]
	}

	return allRoutes
}
