package primaryudn

import (
	"context"
	"fmt"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type reconciler struct {
	guestClient client.Client
	mgmtClient  client.Client
	namespace   string
	hcpName     string
}

func (r *reconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("controller", ControllerName)

	ingressDomain, err := guestIngressDomain(ctx, r.guestClient)
	if err != nil {
		log.Info("waiting for guest ingress domain")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	routerIP, err := guestRouterInternalClusterIP(ctx, r.guestClient)
	if err != nil {
		log.Info("waiting for guest router-internal ClusterIP")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	dnsImage, err := guestDNSImage(ctx, r.guestClient)
	if err != nil {
		log.Info("waiting for guest dns-default DaemonSet")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	if err := ensureInternalAppsDNSBase(ctx, r.guestClient, dnsImage); err != nil {
		return ctrl.Result{}, err
	}

	internalUpstream, ready, err := internalAppsDNSUpstream(ctx, r.guestClient)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ready {
		log.Info("waiting for internal-apps-dns endpoints")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	consoleHost := fmt.Sprintf("console-openshift-console.%s", ingressDomain)
	canaryHost := fmt.Sprintf("canary-openshift-ingress-canary.%s", ingressDomain)
	downloadsHost := fmt.Sprintf("downloads-openshift-console.%s", ingressDomain)

	hosts := map[string]string{
		consoleHost:   routerIP,
		canaryHost:    routerIP,
		downloadsHost: routerIP,
	}
	if err := ensureInternalAppsDNSCorefile(ctx, r.guestClient, hosts); err != nil {
		return ctrl.Result{}, err
	}
	if err := ensureDNSOperatorForwarding(ctx, r.guestClient, []string{ingressDomain}, internalUpstream); err != nil {
		return ctrl.Result{}, err
	}

	oauthHost, err := mgmtOAuthRouteHost(ctx, r.mgmtClient, r.namespace)
	if err != nil {
		log.Info("waiting for OAuth route host")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	oauthUDNIP, err := mgmtOAuthPrimaryUDNIP(ctx, r.mgmtClient, r.namespace)
	if err != nil {
		log.Info("waiting for OAuth pod primary UDN IP")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	oauthBridgeSvcIP, err := ensureGuestOAuthBridge(ctx, r.guestClient, oauthUDNIP)
	if err != nil {
		return ctrl.Result{}, err
	}
	if oauthBridgeSvcIP == "" {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	hosts[oauthHost] = oauthBridgeSvcIP
	if err := ensureInternalAppsDNSCorefile(ctx, r.guestClient, hosts); err != nil {
		return ctrl.Result{}, err
	}

	zones := uniqueStrings([]string{ingressDomain, dnsZoneFromHostname(oauthHost)})
	if err := ensureDNSOperatorForwarding(ctx, r.guestClient, zones, internalUpstream); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func isOAuthPodInNamespace(namespace string) predicate.Funcs {
	match := func(obj client.Object) bool {
		if obj.GetNamespace() != namespace {
			return false
		}
		if labels := obj.GetLabels(); labels != nil {
			return labels["app"] == oauthPodLabelValue
		}
		return false
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return match(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return match(e.ObjectNew) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return match(e.Object) },
		GenericFunc: func(e event.GenericEvent) bool { return match(e.Object) },
	}
}

func isOAuthRouteInNamespace(namespace string) predicate.Funcs {
	match := func(obj client.Object) bool {
		return obj.GetNamespace() == namespace && obj.GetName() == oauthRouteName
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return match(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return match(e.ObjectNew) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return match(e.Object) },
		GenericFunc: func(e event.GenericEvent) bool { return match(e.Object) },
	}
}
