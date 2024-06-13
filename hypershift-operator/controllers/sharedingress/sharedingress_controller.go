package sharedingress

import (
	"context"
	"fmt"
	"os"

	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/cmd/install/assets"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type SharedIngressReconciler struct {
	Client         client.Client
	Namespace      string
	createOrUpdate upsert.CreateOrUpdateFN
}

func (r *SharedIngressReconciler) SetupWithManager(mgr ctrl.Manager, createOrUpdateProvider upsert.CreateOrUpdateProvider) error {
	r.createOrUpdate = createOrUpdateProvider.CreateOrUpdate
	r.Client = mgr.GetClient()

	mgr.GetCache().IndexField(context.Background(), &corev1.Service{}, "metadata.name", func(o client.Object) []string {
		return []string{o.GetName()}
	})
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedCluster{}).
		Named("SharedIngressController")
	return builder.Complete(r)
}

func (r *SharedIngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: RouterNamespace}}
	if _, err := r.createOrUpdate(ctx, r.Client, namespace, func() error {
		return nil
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile namespace: %w", err)
	}

	{
		src := &corev1.Secret{}
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: assets.PullSecretName}, src); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get pull secret %s: %w", src, err)
		}
		dst := PullSecret()
		_, err := r.createOrUpdate(ctx, r.Client, dst, func() error {
			srcData, srcHasData := src.Data[".dockerconfigjson"]
			if !srcHasData {
				return fmt.Errorf("pull secret %q must have a .dockerconfigjson key", src.Name)
			}
			dst.Type = corev1.SecretTypeDockerConfigJson
			if dst.Data == nil {
				dst.Data = map[string][]byte{}
			}
			dst.Data[".dockerconfigjson"] = srcData
			return nil
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile pull secret: %w", err)
		}
	}

	if err := r.reconcileRouter(ctx); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *SharedIngressReconciler) generateConfig(ctx context.Context) (string, []routev1.Route, error) {
	hcList := &hyperv1.HostedClusterList{}
	if err := r.Client.List(ctx, hcList); err != nil {
		return "", nil, fmt.Errorf("failed to list HCs: %w", err)
	}
	namespaces := make([]string, 0, len(hcList.Items))
	for _, hc := range hcList.Items {
		namespaces = append(namespaces, hc.Namespace+"-"+hc.Name)
	}

	// This enables traffic from through external DNS.
	routes := make([]routev1.Route, 0, len(namespaces))
	for _, ns := range namespaces {
		routeList := &routev1.RouteList{}
		if err := r.Client.List(ctx, routeList, client.InNamespace(ns)); err != nil {
			return "", nil, fmt.Errorf("failed to list routes: %w", err)
		}
		routes = append(routes, routeList.Items...)
	}
	svcsNameToIP := make(map[string]string)
	for _, route := range routes {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      route.Spec.To.Name,
				Namespace: route.Namespace,
			},
		}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
			return "", nil, fmt.Errorf("failed to get service %s: %w", svc.Name, err)
		}
		svcsNameToIP[route.Namespace+route.Spec.To.Name] = svc.Spec.ClusterIP
	}

	// This enables traffic from the data plane via kuberntes.svc.
	svcList := &corev1.ServiceList{}
	fieldSelector := fields.SelectorFromSet(fields.Set{"metadata.name": "kube-apiserver"})
	listOptions := &client.ListOptions{
		FieldSelector: fieldSelector,
	}
	if err := r.Client.List(ctx, svcList, listOptions); err != nil {
		return "", nil, err
	}

	config, err := generateRouterConfig(svcList, routes, svcsNameToIP)
	if err != nil {
		return "", nil, fmt.Errorf("failed to generate router config: %w", err)
	}

	return config, routes, nil
}

func (r *SharedIngressReconciler) reconcileRouter(ctx context.Context) error {
	if err := r.reconcileDefaultServiceAccount(ctx); err != nil {
		return fmt.Errorf("failed to reconcile default service account: %w", err)
	}

	config, routes, err := r.generateConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to generate router config: %w", err)
	}

	routerConfig := RouterConfigurationConfigMap()
	if _, err := r.createOrUpdate(ctx, r.Client, routerConfig, func() error {
		return ReconcileRouterConfiguration(routerConfig, config)
	}); err != nil {
		return fmt.Errorf("failed to reconcile router configuration: %w", err)
	}

	deployment := RouterDeployment()
	if _, err := r.createOrUpdate(ctx, r.Client, deployment, func() error {
		return ReconcileRouterDeployment(deployment,
			routerConfig,
		)
	}); err != nil {
		return fmt.Errorf("failed to reconcile router deployment: %w", err)
	}

	svc := RouterPublicService()
	if _, err := r.createOrUpdate(ctx, r.Client, svc, func() error {
		return ReconcileRouterService(svc)
	}); err != nil {
		return fmt.Errorf("failed to reconcile private router service: %w", err)
	}

	// Get the sharedIngress LB hostname to populate the route status.
	// External DNS uses this to generate the DNS records for each route.
	var canonicalHostname string
	if len(svc.Status.LoadBalancer.Ingress) > 0 {
		canonicalHostname = svc.Status.LoadBalancer.Ingress[0].Hostname
		if svc.Status.LoadBalancer.Ingress[0].Hostname == "" {
			canonicalHostname = svc.Status.LoadBalancer.Ingress[0].IP
		}
	}
	// "Admit" routes that we manage so that other code depending on routes continues
	// to work as before.
	for i := range routes {
		route := routes[i]
		if _, hasHCPLabel := route.Labels[util.HCPRouteLabel]; !hasHCPLabel {
			// If the hypershift.openshift.io/hosted-control-plane label is not present,
			// then it means the route should be fulfilled by the management cluster's router.
			continue
		}
		originalRoute := route.DeepCopy()
		ReconcileRouteStatus(&route, canonicalHostname)
		if !equality.Semantic.DeepEqual(originalRoute.Status, route.Status) {
			if err := r.Client.Status().Patch(ctx, &route, client.MergeFrom(originalRoute)); err != nil {
				return fmt.Errorf("failed to update route %s status: %w", route.Name, err)
			}
		}
	}

	// TODO(alberto): set PDBs.
	// TODO(alberto): set Network policies.

	return nil
}

func (r *SharedIngressReconciler) reconcileDefaultServiceAccount(ctx context.Context) error {
	defaultSA := common.DefaultServiceAccount(RouterNamespace)
	if _, err := r.createOrUpdate(ctx, r.Client, defaultSA, func() error {
		util.EnsurePullSecret(defaultSA, PullSecret().Name)
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func ReconcileRouteStatus(route *routev1.Route, canonicalHostname string) {
	// Skip reconciliation if ingress status.ingress has already been populated and canonical hostname is the same
	if len(route.Status.Ingress) > 0 && route.Status.Ingress[0].RouterCanonicalHostname == canonicalHostname {
		return
	}

	ingress := routev1.RouteIngress{
		Host:                    route.Spec.Host,
		RouterName:              "router",
		WildcardPolicy:          routev1.WildcardPolicyNone,
		RouterCanonicalHostname: canonicalHostname,
	}

	if len(route.Status.Ingress) > 0 && len(route.Status.Ingress[0].Conditions) > 0 {
		ingress.Conditions = route.Status.Ingress[0].Conditions
	} else {
		now := metav1.Now()
		ingress.Conditions = []routev1.RouteIngressCondition{
			{
				Type:               routev1.RouteAdmitted,
				LastTransitionTime: &now,
				Status:             corev1.ConditionTrue,
			},
		}
	}
	route.Status.Ingress = []routev1.RouteIngress{ingress}
}

func UseSharedIngress() bool {
	managedService, _ := os.LookupEnv("MANAGED_SERVICE")
	return managedService == hyperv1.AroHCP
}

func Hostname(hcp *hyperv1.HostedControlPlane) string {
	kasPublishStrategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.APIServer)
	if kasPublishStrategy.Route == nil {
		return ""
	}
	return kasPublishStrategy.Route.Hostname
}
