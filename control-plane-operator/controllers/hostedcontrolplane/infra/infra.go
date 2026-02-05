package infra

import (
	"context"
	"errors"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ingress"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/konnectivity"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/oapi"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/oauth"
	sharedingress "github.com/openshift/hypershift/hypershift-operator/controllers/sharedingress"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/events"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type InfrastructureStatus struct {
	APIHost                 string
	APIPort                 int32
	OAuthEnabled            bool
	OAuthHost               string
	OAuthPort               int32
	KonnectivityHost        string
	KonnectivityPort        int32
	OpenShiftAPIHost        string
	OauthAPIServerHost      string
	PackageServerAPIAddress string
	Message                 string
	InternalHCPRouterHost   string
	NeedInternalRouter      bool
	ExternalHCPRouterHost   string
	NeedExternalRouter      bool
}

func (s InfrastructureStatus) IsReady() bool {
	isReady := len(s.APIHost) > 0 &&
		len(s.KonnectivityHost) > 0 &&
		s.APIPort > 0 &&
		s.KonnectivityPort > 0

	if s.OAuthEnabled {
		isReady = isReady && len(s.OAuthHost) > 0 && s.OAuthPort > 0
	}
	if s.NeedInternalRouter {
		isReady = isReady && len(s.InternalHCPRouterHost) > 0
	}
	if s.NeedExternalRouter {
		isReady = isReady && len(s.ExternalHCPRouterHost) > 0
	}
	return isReady
}

type Reconciler struct {
	Client               client.Client
	DefaultIngressDomain string
}

func NewReconciler(c client.Client, defaultIngressDomain string) *Reconciler {
	return &Reconciler{
		Client:               c,
		DefaultIngressDomain: defaultIngressDomain,
	}
}

func (r *Reconciler) ReconcileInfrastructure(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	if hcp.Spec.Services == nil {
		return fmt.Errorf("service publishing strategy undefined")
	}
	if err := r.reconcileAPIServerService(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile API server service: %w", err)
	}
	if err := r.reconcileKonnectivityServerService(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile Konnectivity server service: %w", err)
	}
	if util.HCPOAuthEnabled(hcp) {
		if err := r.reconcileOAuthServerService(ctx, hcp, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile OAuth server service: %w", err)
		}
		if err := r.reconcileOAuthAPIServerService(ctx, hcp, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile OpenShift OAuth api service: %w", err)
		}
	}
	if err := r.reconcileOpenshiftAPIServerService(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile OpenShift api service: %w", err)
	}
	if err := r.reconcileOLMPackageServerService(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile OLM PackageServer service: %w", err)
	}
	if err := r.reconcileHCPRouterServices(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile HCP router services: %w", err)
	}

	return nil
}

func (r *Reconciler) ReconcileInfrastructureStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (InfrastructureStatus, error) {
	var (
		infraStatus InfrastructureStatus
		errs        []error
		err         error
		msg         string
		messages    []string
	)
	if infraStatus.APIHost, infraStatus.APIPort, msg, err = r.reconcileAPIServerServiceStatus(ctx, hcp); err != nil {
		errs = append(errs, err)
	}
	if len(msg) > 0 {
		messages = append(messages, msg)
	}
	if infraStatus.KonnectivityHost, infraStatus.KonnectivityPort, msg, err = r.reconcileKonnectivityServiceStatus(ctx, hcp); err != nil {
		errs = append(errs, err)
	}
	if len(msg) > 0 {
		messages = append(messages, msg)
	}
	if util.HCPOAuthEnabled(hcp) {
		infraStatus.OAuthEnabled = true
		if infraStatus.OAuthHost, infraStatus.OAuthPort, msg, err = r.reconcileOAuthServiceStatus(ctx, hcp); err != nil {
			errs = append(errs, err)
		}
		if len(msg) > 0 {
			messages = append(messages, msg)
		}
		if infraStatus.OauthAPIServerHost, err = r.reconcileOAuthAPIServerServiceStatus(ctx, hcp); err != nil {
			errs = append(errs, err)
		}
	} else {
		infraStatus.OAuthEnabled = false
	}
	if infraStatus.OpenShiftAPIHost, err = r.reconcileOpenShiftAPIServerServiceStatus(ctx, hcp); err != nil {
		errs = append(errs, err)
	}
	if infraStatus.PackageServerAPIAddress, err = r.reconcileOLMPackageServerServiceStatus(ctx, hcp); err != nil {
		errs = append(errs, err)
	}
	if infraStatus.InternalHCPRouterHost, infraStatus.NeedInternalRouter, msg, err = r.reconcileInternalRouterServiceStatus(ctx, hcp); err != nil {
		errs = append(errs, err)
	}
	if len(msg) > 0 {
		messages = append(messages, msg)
	}
	if infraStatus.ExternalHCPRouterHost, infraStatus.NeedExternalRouter, msg, err = r.reconcileExternalRouterServiceStatus(ctx, hcp); err != nil {
		errs = append(errs, err)
	}
	if len(msg) > 0 {
		messages = append(messages, msg)
	}
	if len(messages) > 0 {
		infraStatus.Message = strings.Join(messages, "; ")
	}

	return infraStatus, utilerrors.NewAggregate(errs)
}

func (r *Reconciler) AdmitHCPManagedRoutes(ctx context.Context, hcp *hyperv1.HostedControlPlane, privateRouterHost, externalRouterHost string) error {
	routeList := &routev1.RouteList{}
	if err := r.Client.List(ctx, routeList, client.InNamespace(hcp.Namespace)); err != nil {
		return fmt.Errorf("failed to list routes: %w", err)
	}

	// "Admit" routes that we manage so that other code depending on routes continues
	// to work as before.
	for i := range routeList.Items {
		route := &routeList.Items[i]
		if _, hasHCPLabel := route.Labels[util.HCPRouteLabel]; !hasHCPLabel {
			// If the hypershift.openshift.io/hosted-control-plane label is not present,
			// then it means the route should be fulfilled by the management cluster's router.
			continue
		}
		originalRoute := route.DeepCopy()
		ingress.ReconcileRouteStatus(route, externalRouterHost, privateRouterHost)
		if !equality.Semantic.DeepEqual(originalRoute.Status, route.Status) {
			if err := r.Client.Status().Patch(ctx, route, client.MergeFrom(originalRoute)); err != nil {
				return fmt.Errorf("failed to update route %s status: %w", route.Name, err)
			}
		}
	}

	return nil
}

func (r *Reconciler) reconcileAPIServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	serviceStrategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.APIServer)
	if serviceStrategy == nil {
		return errors.New("APIServer service strategy not specified")
	}
	p := kas.NewKubeAPIServerServiceParams(hcp)
	apiServerService := manifests.KubeAPIServerService(hcp.Namespace)
	kasSVCPort := config.KASSVCPort
	if hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		kasSVCPort = config.KASSVCIBMCloudPort
	}
	if serviceStrategy.Type == hyperv1.LoadBalancer && (hcp.Spec.Platform.Type == hyperv1.AzurePlatform ||
		hcp.Annotations[hyperv1.ManagementPlatformAnnotation] == string(hyperv1.AzurePlatform)) {
		// For Azure or Kubevirt on Azure we currently hardcode 7443 for the SVC LB as 6443 collides with public LB rule for the management cluster.
		// https://bugzilla.redhat.com/show_bug.cgi?id=2060650
		// TODO(alberto): explore exposing multiple Azure frontend IPs on the load balancer.
		kasSVCPort = config.KASSVCLBAzurePort
		apiServerService = manifests.KubeAPIServerServiceAzureLB(hcp.Namespace)
	}
	if _, err := createOrUpdate(ctx, r.Client, apiServerService, func() error {
		return kas.ReconcileService(apiServerService, serviceStrategy, p.OwnerReference, kasSVCPort, p.AllowedCIDRBlocks, hcp)
	}); err != nil {
		return fmt.Errorf("failed to reconcile API server service: %w", err)
	}

	if serviceStrategy.Type == hyperv1.LoadBalancer && (hcp.Spec.Platform.Type == hyperv1.AzurePlatform ||
		hcp.Spec.Platform.Type == hyperv1.KubevirtPlatform && hcp.Annotations[hyperv1.ManagementPlatformAnnotation] == string(hyperv1.AzurePlatform)) {
		// Create the svc clusterIP for Azure on config.KASSVCPort as expected by internal consumers.
		kasSVC := manifests.KubeAPIServerService(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r.Client, kasSVC, func() error {
			return kas.ReconcileServiceClusterIP(kasSVC, p.OwnerReference)
		}); err != nil {
			return fmt.Errorf("failed to reconcile KAS SVC clusterIP: %w", err)
		}
	}

	if serviceStrategy.Type == hyperv1.Route {
		externalPublicRoute := manifests.KubeAPIServerExternalPublicRoute(hcp.Namespace)
		externalPrivateRoute := manifests.KubeAPIServerExternalPrivateRoute(hcp.Namespace)
		if util.IsPublicHCP(hcp) {
			// Remove the external private route if it exists
			err := r.Client.Get(ctx, client.ObjectKeyFromObject(externalPrivateRoute), externalPrivateRoute)
			if err != nil {
				if !apierrors.IsNotFound(err) {
					return fmt.Errorf("failed to check whether apiserver external private route exists: %w", err)
				}
			} else {
				if err := r.Client.Delete(ctx, externalPrivateRoute); err != nil {
					return fmt.Errorf("failed to delete apiserver external private route: %w", err)
				}
			}
			// Reconcile the external public route
			if _, err := createOrUpdate(ctx, r.Client, externalPublicRoute, func() error {
				hostname := ""
				if serviceStrategy.Route != nil {
					hostname = serviceStrategy.Route.Hostname
				}
				return kas.ReconcileExternalPublicRoute(externalPublicRoute, p.OwnerReference, hostname)
			}); err != nil {
				return fmt.Errorf("failed to reconcile apiserver external public route %s: %w", externalPublicRoute.Name, err)
			}
		} else {
			// Remove the external public route if it exists
			err := r.Client.Get(ctx, client.ObjectKeyFromObject(externalPublicRoute), externalPublicRoute)
			if err != nil {
				if !apierrors.IsNotFound(err) {
					return fmt.Errorf("failed to check whether apiserver external public route exists: %w", err)
				}
			} else {
				if err := r.Client.Delete(ctx, externalPublicRoute); err != nil {
					return fmt.Errorf("failed to delete apiserver external public route: %w", err)
				}
			}
			// Reconcile the external private route
			if _, err := createOrUpdate(ctx, r.Client, externalPrivateRoute, func() error {
				hostname := ""
				if serviceStrategy.Route != nil {
					hostname = serviceStrategy.Route.Hostname
				}
				return kas.ReconcileExternalPrivateRoute(externalPrivateRoute, p.OwnerReference, hostname)
			}); err != nil {
				return fmt.Errorf("failed to reconcile apiserver external private route %s: %w", externalPrivateRoute.Name, err)
			}
		}
		// The private KAS route is always present as it is the default
		// destination for the HCP router
		internalRoute := manifests.KubeAPIServerInternalRoute(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r.Client, internalRoute, func() error {
			return kas.ReconcileInternalRoute(internalRoute, p.OwnerReference)
		}); err != nil {
			return fmt.Errorf("failed to reconcile apiserver internal route %s: %w", internalRoute.Name, err)
		}
	} else if serviceStrategy.Type == hyperv1.LoadBalancer && util.IsPrivateHCP(hcp) {
		apiServerPrivateService := manifests.KubeAPIServerPrivateService(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r.Client, apiServerPrivateService, func() error {
			return kas.ReconcilePrivateService(apiServerPrivateService, hcp, p.OwnerReference)
		}); err != nil {
			return fmt.Errorf("failed to reconcile API server private service: %w", err)
		}
	}

	return nil
}

func (r *Reconciler) reconcileKonnectivityServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := konnectivity.NewKonnectivityServiceParams(hcp)
	serviceStrategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.Konnectivity)
	if serviceStrategy == nil {
		//lint:ignore ST1005 Konnectivity is proper name
		return fmt.Errorf("Konnectivity service strategy not specified")
	}
	konnectivityServerService := manifests.KonnectivityServerService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, konnectivityServerService, func() error {
		return kas.ReconcileKonnectivityServerService(konnectivityServerService, p.OwnerRef, serviceStrategy, hcp)
	}); err != nil {
		return fmt.Errorf("failed to reconcile Konnectivity service: %w", err)
	}
	if serviceStrategy.Type != hyperv1.Route {
		return nil
	}
	konnectivityRoute := manifests.KonnectivityServerRoute(hcp.Namespace)
	if util.IsPrivateHCP(hcp) {
		if _, err := createOrUpdate(ctx, r.Client, konnectivityRoute, func() error {
			return kas.ReconcileKonnectivityInternalRoute(konnectivityRoute, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile Konnectivity server internal route: %w", err)
		}
	} else {
		if _, err := createOrUpdate(ctx, r.Client, konnectivityRoute, func() error {
			hostname := ""
			if serviceStrategy.Route != nil {
				hostname = serviceStrategy.Route.Hostname
			}
			return kas.ReconcileKonnectivityExternalRoute(konnectivityRoute, p.OwnerRef, hostname, r.DefaultIngressDomain, util.LabelHCPRoutes(hcp))
		}); err != nil {
			return fmt.Errorf("failed to reconcile Konnectivity server external route: %w", err)
		}
	}
	return nil
}

func (r *Reconciler) reconcileOAuthServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	serviceStrategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.OAuthServer)
	if serviceStrategy == nil {
		return fmt.Errorf("OAuthServer service strategy not specified")
	}
	p := oauth.NewOAuthServiceParams(hcp)
	oauthServerService := manifests.OauthServerService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, oauthServerService, func() error {
		return oauth.ReconcileService(oauthServerService, p.OwnerRef, serviceStrategy, hcp.Spec.Platform.Type)
	}); err != nil {
		return fmt.Errorf("failed to reconcile OAuth service: %w", err)
	}
	if serviceStrategy.Type != hyperv1.Route {
		return nil
	}
	oauthExternalPublicRoute := manifests.OauthServerExternalPublicRoute(hcp.Namespace)
	oauthExternalPrivateRoute := manifests.OauthServerExternalPrivateRoute(hcp.Namespace)
	if util.IsPublicHCP(hcp) {
		// Remove the external private route if it exists
		_, err := util.DeleteIfNeeded(ctx, r.Client, oauthExternalPrivateRoute)
		if err != nil {
			return fmt.Errorf("failed to delete OAuth external private route: %w", err)
		}

		// Reconcile the external public route
		if _, err := createOrUpdate(ctx, r.Client, oauthExternalPublicRoute, func() error {
			hostname := ""
			if serviceStrategy.Route != nil {
				hostname = serviceStrategy.Route.Hostname
			}
			return oauth.ReconcileExternalPublicRoute(oauthExternalPublicRoute, p.OwnerRef, hostname, r.DefaultIngressDomain, util.LabelHCPRoutes(hcp))
		}); err != nil {
			return fmt.Errorf("failed to reconcile OAuth external public route: %w", err)
		}
	} else {
		// Remove the external public route if it exists
		_, err := util.DeleteIfNeeded(ctx, r.Client, oauthExternalPublicRoute)
		if err != nil {
			return fmt.Errorf("failed to delete OAuth external public route: %w", err)
		}

		// Reconcile the external private route if a hostname is specified
		if serviceStrategy.Route != nil && serviceStrategy.Route.Hostname != "" {
			if _, err := createOrUpdate(ctx, r.Client, oauthExternalPrivateRoute, func() error {
				return oauth.ReconcileExternalPrivateRoute(oauthExternalPrivateRoute, p.OwnerRef, serviceStrategy.Route.Hostname, r.DefaultIngressDomain, util.LabelHCPRoutes(hcp))
			}); err != nil {
				return fmt.Errorf("failed to reconcile OAuth external private route: %w", err)
			}
		} else {
			// Remove the external private route if it exists when hostname is not specified
			_, err := util.DeleteIfNeeded(ctx, r.Client, oauthExternalPrivateRoute)
			if err != nil {
				return fmt.Errorf("failed to delete OAuth external private route: %w", err)
			}
		}
	}
	if util.IsPrivateHCP(hcp) {
		oauthInternalRoute := manifests.OauthServerInternalRoute(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r.Client, oauthInternalRoute, func() error {
			return oauth.ReconcileInternalRoute(oauthInternalRoute, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile OAuth internal route: %w", err)
		}
	}
	return nil
}

func (r *Reconciler) reconcileOpenshiftAPIServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	svc := manifests.OpenshiftAPIServerService(hcp.Namespace)
	p := oapi.NewOpenShiftAPIServerServiceParams(hcp)
	if _, err := createOrUpdate(ctx, r.Client, svc, func() error {
		return oapi.ReconcileOpenShiftAPIService(svc, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile OpenShift API server service: %w", err)
	}
	return nil
}

func (r *Reconciler) reconcileOAuthAPIServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	svc := manifests.OauthAPIServerService(hcp.Namespace)
	p := oapi.NewOpenShiftAPIServerServiceParams(hcp)
	if _, err := createOrUpdate(ctx, r.Client, svc, func() error {
		return oapi.ReconcileOAuthAPIService(svc, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile OAuth API server service: %w", err)
	}
	return nil
}

func (r *Reconciler) reconcileOLMPackageServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	svc := manifests.OLMPackageServerService(hcp.Namespace)
	p := oapi.NewOpenShiftAPIServerServiceParams(hcp)
	_, err := createOrUpdate(ctx, r.Client, svc, func() error {
		return oapi.ReconcileOLMPackageServerService(svc, p.OwnerRef)
	})
	if err != nil {
		return err
	}
	return nil
}

func (r *Reconciler) reconcileHCPRouterServices(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	pubSvc := manifests.RouterPublicService(hcp.Namespace)
	privSvc := manifests.PrivateRouterService(hcp.Namespace)
	if sharedingress.UseSharedIngress() || hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform || (!util.IsPrivateHCP(hcp) && !util.LabelHCPRoutes(hcp)) {
		if _, err := util.DeleteIfNeeded(ctx, r.Client, pubSvc); err != nil {
			return fmt.Errorf("failed to delete public router service: %w", err)
		}
		if _, err := util.DeleteIfNeeded(ctx, r.Client, privSvc); err != nil {
			return fmt.Errorf("failed to delete private router service: %w", err)
		}
		return nil
	}

	// Create the Service type LB internal for private endpoints.
	if util.IsPrivateHCP(hcp) {
		if _, err := createOrUpdate(ctx, r.Client, privSvc, func() error {
			return ingress.ReconcileRouterService(privSvc, true, true, hcp)
		}); err != nil {
			return fmt.Errorf("failed to reconcile private router service: %w", err)
		}
	} else {
		if _, err := util.DeleteIfNeeded(ctx, r.Client, privSvc); err != nil {
			return fmt.Errorf("failed to delete private router service: %w", err)
		}
	}

	// When Public access endpoint AND routes use HCP router, create a Service type LB external.
	// This ensures we only create public router infrastructure when routes are labeled for it.
	if util.IsPublicHCP(hcp) && util.LabelHCPRoutes(hcp) {
		if _, err := createOrUpdate(ctx, r.Client, pubSvc, func() error {
			return ingress.ReconcileRouterService(pubSvc, false, util.IsPrivateHCP(hcp), hcp)
		}); err != nil {
			return fmt.Errorf("failed to reconcile router service: %w", err)
		}
	} else {
		if _, err := util.DeleteIfNeeded(ctx, r.Client, pubSvc); err != nil {
			return fmt.Errorf("failed to delete public router service: %w", err)
		}
	}
	return nil
}

func (r *Reconciler) reconcileAPIServerServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (host string, port int32, message string, err error) {
	serviceStrategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.APIServer)
	if serviceStrategy == nil {
		return "", 0, "", errors.New("APIServer service strategy not specified")
	}

	if sharedingress.UseSharedIngress() || (hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform && serviceStrategy.Type == hyperv1.Route) {
		return sharedingress.Hostname(hcp), sharedingress.ExternalDNSLBPort, "", nil
	}

	var svc *corev1.Service
	if serviceStrategy.Type == hyperv1.Route {
		if util.IsPublicHCP(hcp) {
			svc = manifests.RouterPublicService(hcp.Namespace)
		} else {
			svc = manifests.PrivateRouterService(hcp.Namespace)
		}
	} else {
		if util.IsPublicHCP(hcp) {
			svc = manifests.KubeAPIServerService(hcp.Namespace)
		} else {
			svc = manifests.KubeAPIServerPrivateService(hcp.Namespace)
		}
	}

	kasSVCLBPort := config.KASSVCPort
	if hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		kasSVCLBPort = config.KASSVCIBMCloudPort
	}
	if serviceStrategy.Type == hyperv1.LoadBalancer && (hcp.Spec.Platform.Type == hyperv1.AzurePlatform ||
		hcp.Annotations[hyperv1.ManagementPlatformAnnotation] == string(hyperv1.AzurePlatform)) {
		// If Azure or Kubevirt on Azure we get the SVC handling the LB.
		// TODO(alberto): remove this hack when having proper traffic management for Azure.
		kasSVCLBPort = config.KASSVCLBAzurePort
		svc = manifests.KubeAPIServerServiceAzureLB(hcp.Namespace)
	}

	if err = r.Client.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		if apierrors.IsNotFound(err) {
			err = nil
			return
		}
		err = fmt.Errorf("failed to get kube apiserver service: %w", err)
		return
	}

	return kas.ReconcileServiceStatus(svc, serviceStrategy, kasSVCLBPort, events.NewMessageCollector(ctx, r.Client))
}

func (r *Reconciler) reconcileKonnectivityServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (host string, port int32, message string, err error) {
	serviceStrategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.Konnectivity)
	if serviceStrategy == nil {
		err = fmt.Errorf("konnectivity service strategy not specified")
		return
	}
	svc := manifests.KonnectivityServerService(hcp.Namespace)
	if err = r.Client.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		if apierrors.IsNotFound(err) {
			err = nil
			return
		}
		err = fmt.Errorf("failed to get konnectivity service: %w", err)
		return
	}
	var route *routev1.Route
	if serviceStrategy.Type == hyperv1.Route {
		route = manifests.KonnectivityServerRoute(hcp.Namespace)
		if err = r.Client.Get(ctx, client.ObjectKeyFromObject(route), route); err != nil {
			if apierrors.IsNotFound(err) {
				err = nil
				return
			}
			err = fmt.Errorf("failed to get konnectivity route: %w", err)
			return
		}
	}
	return kas.ReconcileKonnectivityServerServiceStatus(svc, route, serviceStrategy, events.NewMessageCollector(ctx, r.Client))
}

func (r *Reconciler) reconcileOAuthServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (host string, port int32, message string, err error) {
	serviceStrategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.OAuthServer)
	if serviceStrategy == nil {
		err = fmt.Errorf("OAuth strategy not specified")
		return
	}
	var route *routev1.Route
	svc := manifests.OauthServerService(hcp.Namespace)
	if err = r.Client.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		if apierrors.IsNotFound(err) {
			err = nil
			return
		}
		err = fmt.Errorf("failed to get oauth service: %w", err)
		return
	}
	if serviceStrategy.Type == hyperv1.Route {
		if util.IsPublicHCP(hcp) {
			route = manifests.OauthServerExternalPublicRoute(hcp.Namespace)
			if err = r.Client.Get(ctx, client.ObjectKeyFromObject(route), route); err != nil {
				if apierrors.IsNotFound(err) {
					err = nil
					return
				}
				err = fmt.Errorf("failed to get oauth external route: %w", err)
				return
			}
		} else if serviceStrategy.Route != nil && serviceStrategy.Route.Hostname != "" {
			route = manifests.OauthServerExternalPrivateRoute(hcp.Namespace)
			if err = r.Client.Get(ctx, client.ObjectKeyFromObject(route), route); err != nil {
				if apierrors.IsNotFound(err) {
					err = nil
					return
				}
				err = fmt.Errorf("failed to get oauth internal route: %w", err)
				return
			}
		} else {
			route = manifests.OauthServerInternalRoute(hcp.Namespace)
			if err = r.Client.Get(ctx, client.ObjectKeyFromObject(route), route); err != nil {
				if apierrors.IsNotFound(err) {
					err = nil
					return
				}
				err = fmt.Errorf("failed to get oauth internal route: %w", err)
				return
			}
		}
	}
	return oauth.ReconcileServiceStatus(svc, route, serviceStrategy)
}

func (r *Reconciler) reconcileOpenShiftAPIServerServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (string, error) {
	svc := manifests.OpenshiftAPIServerService(hcp.Namespace)
	return r.reconcileClusterIPServiceStatus(ctx, svc)
}

func (r *Reconciler) reconcileOAuthAPIServerServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (string, error) {
	svc := manifests.OauthAPIServerService(hcp.Namespace)
	return r.reconcileClusterIPServiceStatus(ctx, svc)
}

func (r *Reconciler) reconcileOLMPackageServerServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (string, error) {
	svc := manifests.OLMPackageServerService(hcp.Namespace)
	return r.reconcileClusterIPServiceStatus(ctx, svc)
}

func (r *Reconciler) reconcileClusterIPServiceStatus(ctx context.Context, svc *corev1.Service) (string, error) {
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to get cluster ip service %s/%s: %w", svc.Namespace, svc.Name, err)
	}
	return svc.Spec.ClusterIP, nil
}

func (r *Reconciler) reconcileInternalRouterServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (host string, needed bool, message string, err error) {
	if !util.IsPrivateHCP(hcp) || hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		return
	}
	return r.reconcileRouterServiceStatus(ctx, manifests.PrivateRouterService(hcp.Namespace), events.NewMessageCollector(ctx, r.Client))
}

func (r *Reconciler) reconcileExternalRouterServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (host string, needed bool, message string, err error) {
	if !util.IsPublicHCP(hcp) || !util.LabelHCPRoutes(hcp) || sharedingress.UseSharedIngress() || hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		return
	}
	return r.reconcileRouterServiceStatus(ctx, manifests.RouterPublicService(hcp.Namespace), events.NewMessageCollector(ctx, r.Client))
}

func (r *Reconciler) reconcileRouterServiceStatus(ctx context.Context, svc *corev1.Service, messageCollector events.MessageCollector) (host string, needed bool, message string, err error) {
	needed = true
	if err = r.Client.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		if apierrors.IsNotFound(err) {
			err = nil
			return
		}
		err = fmt.Errorf("failed to get router service (%s): %w", svc.Name, err)
		return
	}
	if message, err = util.CollectLBMessageIfNotProvisioned(svc, messageCollector); err != nil || message != "" {
		return
	}
	switch {
	case svc.Status.LoadBalancer.Ingress[0].Hostname != "":
		host = svc.Status.LoadBalancer.Ingress[0].Hostname
	case svc.Status.LoadBalancer.Ingress[0].IP != "":
		host = svc.Status.LoadBalancer.Ingress[0].IP
	}
	return
}
