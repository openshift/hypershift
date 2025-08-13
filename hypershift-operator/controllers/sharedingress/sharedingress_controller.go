package sharedingress

import (
	"context"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/cmd/install/assets"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/support/capabilities"
	supportconfig "github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type SharedIngressReconciler struct {
	Client         client.Client
	Namespace      string
	createOrUpdate upsert.CreateOrUpdateFN

	// ManagementClusterCapabilities can be asked for support of optional management cluster capabilities
	ManagementClusterCapabilities capabilities.CapabiltyChecker
	HypershiftOperatorImage       string
}

func (r *SharedIngressReconciler) SetupWithManager(mgr ctrl.Manager, createOrUpdateProvider upsert.CreateOrUpdateProvider) error {
	r.createOrUpdate = createOrUpdateProvider.CreateOrUpdate
	r.Client = mgr.GetClient()

	err := mgr.GetCache().IndexField(context.Background(), &corev1.Service{}, "metadata.name", func(o client.Object) []string {
		return []string{o.GetName()}
	})
	if err != nil {
		return err
	}

	// A channel is used to generate an initial sync event.
	// Afterwards, the controller syncs on HostedClusters.
	initialSync := make(chan event.GenericEvent)

	go func() {
		initialSync <- event.GenericEvent{Object: &hyperv1.HostedCluster{}}
	}()

	return ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedCluster{}).
		WatchesRawSource(source.Channel(initialSync, &handler.EnqueueRequestForObject{})).
		Watches(
			&routev1.Route{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
				if _, hasHCPLabel := obj.GetLabels()[util.HCPRouteLabel]; !hasHCPLabel {
					return nil
				}
				return []ctrl.Request{{NamespacedName: client.ObjectKey{
					Name:      obj.GetName(),
					Namespace: obj.GetNamespace(),
				}}}
			}),
		).
		Named("SharedIngressController").
		Complete(r)
}

func (r *SharedIngressReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: RouterNamespace}}
	if _, err := r.createOrUpdate(ctx, r.Client, namespace, func() error {
		return nil
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile namespace: %w", err)
	}

	pullSecretPresent := false
	src := &corev1.Secret{}
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: assets.PullSecretName}, src); err != nil {
		if errors.IsNotFound(err) {
			log.Log.Info(fmt.Sprintf("pull secret was not found in %s namespace, will not create pullsecret for sharedingress", r.Namespace))
		} else {
			return ctrl.Result{}, fmt.Errorf("failed to get pull secret %s: %w", src, err)
		}
	} else {
		pullSecretPresent = true
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

	if err := r.reconcileRouter(ctx, pullSecretPresent); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *SharedIngressReconciler) reconcileRouter(ctx context.Context, pullSecretPresent bool) error {
	if err := r.reconcileDefaultServiceAccount(ctx, pullSecretPresent); err != nil {
		return fmt.Errorf("failed to reconcile default service account: %w", err)
	}

	if err := r.reconcileConfigGeneratorControllerRBAC(ctx, pullSecretPresent); err != nil {
		return fmt.Errorf("failed to reconcile config generator RBAC: %w", err)
	}

	deployment := RouterDeployment()
	if _, err := r.createOrUpdate(ctx, r.Client, deployment, func() error {
		return ReconcileRouterDeployment(deployment, r.HypershiftOperatorImage)
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

	routeList := &routev1.RouteList{}
	// If the hypershift.openshift.io/hosted-control-plane label is not present,
	// then it means the route should be fulfilled by the management cluster's router.
	if err := r.Client.List(ctx, routeList, client.HasLabels{util.HCPRouteLabel}); err != nil {
		return fmt.Errorf("failed to list routes: %w", err)
	}

	// "Admit" routes that we manage so that other code depending on routes continues
	// to work as before.
	for i := range routeList.Items {
		route := routeList.Items[i]
		originalRoute := route.DeepCopy()
		ReconcileRouteStatus(&route, canonicalHostname)
		if !equality.Semantic.DeepEqual(originalRoute.Status, route.Status) {
			if err := r.Client.Status().Patch(ctx, &route, client.MergeFrom(originalRoute)); err != nil {
				return fmt.Errorf("failed to update route %s status: %w", route.Name, err)
			}
		}
	}

	routerDeploymentOwnerRef := supportconfig.OwnerRefFrom(deployment)

	pdb := RouterPodDisruptionBudget()
	if result, err := r.createOrUpdate(ctx, r.Client, pdb, func() error {
		ReconcileRouterPodDisruptionBudget(pdb, routerDeploymentOwnerRef)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd pdb: %w", err)
	} else {
		log.Log.Info("reconciled etcd pdb", "result", result)
	}

	// Reconcile KAS Network Policy
	var managementClusterNetwork *configv1.Network
	if r.ManagementClusterCapabilities.Has(capabilities.CapabilityNetworks) {
		managementClusterNetwork = &configv1.Network{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(managementClusterNetwork), managementClusterNetwork); err != nil {
			return fmt.Errorf("failed to get management cluster network config: %w", err)
		}
	}

	networkPolicy := RouterNetworkPolicy()
	if result, err := r.createOrUpdate(ctx, r.Client, networkPolicy, func() error {
		ReconcileRouterNetworkPolicy(networkPolicy, r.ManagementClusterCapabilities.Has(capabilities.CapabilityDNS), managementClusterNetwork)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile router network policy: %w", err)
	} else {
		log.Log.Info("reconciled router network policy", "result", result)
	}

	return nil
}

func (r *SharedIngressReconciler) reconcileDefaultServiceAccount(ctx context.Context, pullSecretPresent bool) error {
	defaultSA := common.DefaultServiceAccount(RouterNamespace)
	if _, err := r.createOrUpdate(ctx, r.Client, defaultSA, func() error {
		if pullSecretPresent {
			util.EnsurePullSecret(defaultSA, PullSecret().Name)
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (r *SharedIngressReconciler) reconcileConfigGeneratorControllerRBAC(ctx context.Context, pullSecretPresent bool) error {
	sa := RouterServiceAccount()
	if _, err := r.createOrUpdate(ctx, r.Client, sa, func() error {
		if pullSecretPresent {
			util.EnsurePullSecret(sa, PullSecret().Name)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile router ServiceAccount: %w", err)
	}

	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sharedingress-config-generator",
		},
	}
	if _, err := r.createOrUpdate(ctx, r.Client, cr, func() error {
		cr.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{"hypershift.openshift.io"},
				Resources: []string{"hostedclusters"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"route.openshift.io"},
				Resources: []string{"routes"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"services"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"events"},
				Verbs:     []string{"create", "patch", "update"},
			},
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile ClusterRole: %w", err)
	}

	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sharedingress-config-generator",
		},
	}
	if _, err := r.createOrUpdate(ctx, r.Client, crb, func() error {
		crb.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     cr.Name,
		}
		crb.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      sa.Name,
				Namespace: sa.Namespace,
			},
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile ClusterRoleBinding: %w", err)
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
