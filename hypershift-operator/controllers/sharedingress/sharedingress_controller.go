package sharedingress

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"

	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/cmd/install/assets"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"
)

const (
	privateLinkServiceName = "pls-shared-ingress"
)

type SharedIngressReconciler struct {
	client.Client
	Namespace       string
	PrivatePlatform string
	createOrUpdate  upsert.CreateOrUpdateFN
}

func (r *SharedIngressReconciler) SetupWithManager(mgr ctrl.Manager, createOrUpdateProvider upsert.CreateOrUpdateProvider) error {
	r.createOrUpdate = createOrUpdateProvider.CreateOrUpdate
	r.Client = mgr.GetClient()

	mgr.GetCache().IndexField(context.Background(), &corev1.Service{}, "metadata.name", func(o client.Object) []string {
		return []string{o.GetName()}
	})

	// A channel is used to generate an initial sync event.
	// Afterwards, the controller syncs on HostedClusters.
	initialSync := make(chan event.GenericEvent)

	builder := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedCluster{}).
		WatchesRawSource(source.Channel(initialSync, &handler.EnqueueRequestForObject{})).
		Named("SharedIngressController")

	go func() {
		initialSync <- event.GenericEvent{Object: &hyperv1.HostedCluster{}}
	}()

	return builder.Complete(r)
}

func (r *SharedIngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: RouterNamespace}}
	if _, err := r.createOrUpdate(ctx, r.Client, namespace, func() error {
		return nil
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile namespace: %w", err)
	}

	pullSecretPresent := false
	src := &corev1.Secret{}
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: assets.PullSecretName}, src); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info(fmt.Sprintf("pull secret was not found in %s namespace, will not create pullsecret for sharedingress", r.Namespace))
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

	if hyperv1.PlatformType(r.PrivatePlatform) == hyperv1.AzurePlatform {
		hcluster := &hyperv1.HostedCluster{}
		if err := r.Get(ctx, req.NamespacedName, hcluster); err != nil {
			if apierrors.IsNotFound(err) {
				log.Info("hostedcluster not found, aborting reconcile", "name", req.NamespacedName)
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to get hosted cluster %q: %w", req.NamespacedName, err)
		}

		if hcluster.DeletionTimestamp.IsZero() {
			if err := r.reconcileAzurePrivateEndpoint(ctx, hcluster); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to reconcile HostedCluster %q azure private endpoint: %w", req.NamespacedName, err)
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *SharedIngressReconciler) generateConfig(ctx context.Context, hcList *hyperv1.HostedClusterList, svcsNamespaceToLinkID map[string]string) (string, []routev1.Route, error) {
	namespaces := make([]string, 0, len(hcList.Items))
	svcsNamespaceToClusterID := make(map[string]string)
	for _, hc := range hcList.Items {
		hcpNamespace := hc.Namespace + "-" + hc.Name
		namespaces = append(namespaces, hcpNamespace)
		svcsNamespaceToClusterID[hcpNamespace] = hc.Spec.ClusterID
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

	config, err := generateRouterConfig(svcList, svcsNamespaceToClusterID, routes, svcsNameToIP, svcsNamespaceToLinkID)
	if err != nil {
		return "", nil, fmt.Errorf("failed to generate router config: %w", err)
	}

	return config, routes, nil
}

func (r *SharedIngressReconciler) reconcileRouter(ctx context.Context, pullSecretPresent bool) error {
	if err := r.reconcileDefaultServiceAccount(ctx, pullSecretPresent); err != nil {
		return fmt.Errorf("failed to reconcile default service account: %w", err)
	}

	hcList := &hyperv1.HostedClusterList{}
	if err := r.Client.List(ctx, hcList); err != nil {
		return fmt.Errorf("failed to list HCs: %w", err)
	}

	var svcsNamespaceToLinkID map[string]string
	// reconcile the private service first, so that the private Link Service is created before generating config which requires the service to exist.
	if hyperv1.PlatformType(r.PrivatePlatform) == hyperv1.AzurePlatform {
		privateSvc := RouterPrivateService()
		if _, err := r.createOrUpdate(ctx, r.Client, privateSvc, func() error {
			return ReconcileRouterPrivateService(privateSvc, hcList)
		}); err != nil {
			return fmt.Errorf("failed to reconcile private router service: %w", err)
		}

		var err error
		svcsNamespaceToLinkID, err = r.getLinkIDMapping(ctx, hcList)
		if err != nil {
			return err
		}
	}

	config, routes, err := r.generateConfig(ctx, hcList, svcsNamespaceToLinkID)
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
		return fmt.Errorf("failed to reconcile public router service: %w", err)
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

func (r *SharedIngressReconciler) getLinkIDMapping(ctx context.Context, hcList *hyperv1.HostedClusterList) (map[string]string, error) {
	privateClusters := make([]hyperv1.HostedCluster, 0)
	for _, hc := range hcList.Items {
		if hc.Spec.Platform.Azure == nil || hc.Spec.Platform.Azure.EndpointAccess == hyperv1.AzureEndpointAccessTypePublic {
			continue
		}
		privateClusters = append(privateClusters, hc)
	}
	if len(privateClusters) == 0 {
		return nil, nil
	}

	response, err := getPrivateLink(ctx)
	if err != nil {
		return nil, err
	}

	privateEndpointIDToLinkID := make(map[string]string)
	for _, connection := range response.Properties.PrivateEndpointConnections {
		privateEndpointIDToLinkID[*connection.Properties.PrivateEndpoint.ID] = *connection.Properties.LinkIdentifier
	}

	hcpNamespaceToLinkID := make(map[string]string)
	for _, hc := range privateClusters {
		privateEndpoint := &hyperv1.AzurePrivateEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hc.Name,
				Namespace: hc.Namespace + "-" + hc.Name,
			},
		}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(privateEndpoint), privateEndpoint); err != nil {
			// skip and continue, shared-ingress should continue serving functional clusters.
			continue
		}

		if len(privateEndpoint.Status.EndpointID) != 0 {
			linkIDHex, err := linkIDToLittleEndianHex(privateEndpointIDToLinkID[privateEndpoint.Status.EndpointID])
			if err != nil {
				continue
			}
			hcpNamespaceToLinkID[privateEndpoint.Namespace] = linkIDHex
		}
	}

	return hcpNamespaceToLinkID, nil
}

func linkIDToLittleEndianHex(linkID string) (string, error) {
	// LinkID is represented as UINT32(4 bytes) and encoded in little endian format
	// see: https://learn.microsoft.com/en-us/azure/private-link/private-link-service-overview#getting-connection-information-using-tcp-proxy-v2
	uint32LinkID, err := strconv.ParseUint(linkID, 10, 32)
	if err != nil {
		return "", err
	}
	b := [4]byte{}
	binary.LittleEndian.PutUint32(b[:], uint32(uint32LinkID))
	return fmt.Sprintf("%x", b), nil
}

func (r *SharedIngressReconciler) reconcileAzurePrivateEndpoint(ctx context.Context, hc *hyperv1.HostedCluster) error {
	if hc.Spec.Platform.Type != hyperv1.AzurePlatform ||
		hc.Spec.Platform.Azure == nil ||
		hc.Spec.Platform.Azure.EndpointAccess != hyperv1.AzureEndpointAccessTypePrivate {
		return nil
	}

	response, err := getPrivateLink(ctx)
	if err != nil {
		return err
	}
	plsID := response.ID

	privateEndpoint := &hyperv1.AzurePrivateEndpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hc.Name,
			Namespace: hc.Namespace + "-" + hc.Name,
		},
	}
	if _, err := r.createOrUpdate(ctx, r.Client, privateEndpoint, func() error {
		privateEndpoint.Spec = hyperv1.AzurePrivateEndpointSpec{
			PrivateLinkServiceID: *plsID,
			ResourceGroupName:    hc.Spec.Platform.Azure.ResourceGroupName,
			SubnetID:             hc.Spec.Platform.Azure.SubnetID,
			Location:             hc.Spec.Platform.Azure.Location,
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile azure private endpoint: %w", err)
	}

	return nil
}

func getPrivateLink(ctx context.Context) (*armnetwork.PrivateLinkServicesClientGetResponse, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}

	subID := os.Getenv("AZURE_SUBSCRIPTION_ID")
	client, err := armnetwork.NewPrivateLinkServicesClient(subID, cred, nil)
	if err != nil {
		return nil, err
	}

	rg := os.Getenv("RESOURCE_GROUP")
	response, err := client.Get(ctx, rg, privateLinkServiceName, nil)
	if err != nil {
		return nil, err
	}

	return &response, nil
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

func APIServerInternalAddress(dnsSpec hyperv1.DNSSpec) string {
	privateDNSZoneResource, _ := arm.ParseResourceID(dnsSpec.PrivateZoneID)
	return fmt.Sprintf("api.%s", privateDNSZoneResource.Name)
}
