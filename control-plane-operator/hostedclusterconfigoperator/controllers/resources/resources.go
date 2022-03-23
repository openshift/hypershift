package resources

import (
	"context"
	"crypto/md5"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/errors"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	imageregistryv1 "github.com/openshift/api/imageregistry/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/crd"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/ingress"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/konnectivity"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/kubeadminpassword"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/monitoring"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/namespaces"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/oapi"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/oauth"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/olm"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/rbac"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/registry"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

const (
	ControllerName       = "resources"
	SecretHashAnnotation = "hypershift.openshift.io/kubeadmin-secret-hash"
	observedConfigKey    = "config"
)

type reconciler struct {
	client client.Client
	upsert.CreateOrUpdateProvider
	platformType              hyperv1.PlatformType
	clusterSignerCA           string
	cpClient                  client.Client
	hcpName                   string
	hcpNamespace              string
	releaseProvider           releaseinfo.Provider
	konnectivityServerAddress string
	konnectivityServerPort    int32
	oauthAddress              string
	oauthPort                 int32
	versions                  map[string]string
	operateOnReleaseImage     string
}

// eventHandler is the handler used throughout. As this controller reconciles all kind of different resources
// it uses an empty request but always reconciles everything.
func eventHandler() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(
		func(client.Object) []reconcile.Request {
			return []reconcile.Request{{}}
		})
}

func Setup(opts *operator.HostedClusterConfigOperatorConfig) error {
	if err := imageregistryv1.AddToScheme(opts.Manager.GetScheme()); err != nil {
		return fmt.Errorf("failed to add to scheme: %w", err)
	}
	c, err := controller.New(ControllerName, opts.Manager, controller.Options{Reconciler: &reconciler{
		client:                    opts.Manager.GetClient(),
		CreateOrUpdateProvider:    opts.TargetCreateOrUpdateProvider,
		platformType:              opts.PlatformType,
		clusterSignerCA:           opts.ClusterSignerCA,
		cpClient:                  opts.CPCluster.GetClient(),
		hcpName:                   opts.HCPName,
		hcpNamespace:              opts.Namespace,
		releaseProvider:           opts.ReleaseProvider,
		konnectivityServerAddress: opts.KonnectivityAddress,
		konnectivityServerPort:    opts.KonnectivityPort,
		oauthAddress:              opts.OAuthAddress,
		oauthPort:                 opts.OAuthPort,
		versions:                  opts.Versions,
		operateOnReleaseImage:     opts.OperateOnReleaseImage,
	}})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}
	resourcesToWatch := []client.Object{
		&imageregistryv1.Config{},
		&corev1.ConfigMap{},
		&corev1.Namespace{},
		&corev1.Secret{},
		&corev1.Service{},
		&corev1.Endpoints{},
		&rbacv1.ClusterRole{},
		&rbacv1.ClusterRoleBinding{},
		&configv1.Infrastructure{},
		&configv1.DNS{},
		&configv1.Ingress{},
		&configv1.Network{},
		&configv1.Proxy{},
		&configv1.Build{},
		&configv1.Image{},
		&configv1.Project{},
		&appsv1.DaemonSet{},
		&configv1.ClusterOperator{},
		&configv1.ClusterVersion{},
		&apiregistrationv1.APIService{},
	}
	for _, r := range resourcesToWatch {
		if err := c.Watch(&source.Kind{Type: r}, eventHandler()); err != nil {
			return fmt.Errorf("failed to watch %T: %w", r, err)
		}
	}
	if err := c.Watch(source.NewKindWithCache(&hyperv1.HostedControlPlane{}, opts.CPCluster.GetCache()), eventHandler()); err != nil {
		return fmt.Errorf("failed to watch HostedControlPlane: %w", err)
	}
	return nil
}

func (r *reconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling")

	hcp := manifests.HostedControlPlane(r.hcpNamespace, r.hcpName)
	if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(hcp), hcp); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get hosted control plane %s/%s: %w", r.hcpNamespace, r.hcpName, err)
	}

	if isPaused, duration := util.IsReconciliationPaused(log, hcp.Spec.PausedUntil); isPaused {
		log.Info("Reconciliation paused", "pausedUntil", *hcp.Spec.PausedUntil)
		return ctrl.Result{RequeueAfter: duration}, nil
	}
	if r.operateOnReleaseImage != "" && r.operateOnReleaseImage != hcp.Spec.ReleaseImage {
		log.Info("releaseImage is %s, but this operator is configured for %s, skipping reconciliation", hcp.Spec.ReleaseImage, r.operateOnReleaseImage)
		return ctrl.Result{}, nil
	}

	globalConfig, err := globalconfig.ParseGlobalConfig(ctx, hcp.Spec.Configuration)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to parse global config for control plane %s/%s: %w", r.hcpNamespace, r.hcpName, err)
	}

	pullSecret := manifests.PullSecret(hcp.Namespace)
	if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get pull secret: %w", err)
	}

	releaseImage, err := r.releaseProvider.Lookup(ctx, hcp.Spec.ReleaseImage, pullSecret.Data[corev1.DockerConfigJsonKey])
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get lookup release image %s: %w", hcp.Spec.ReleaseImage, err)
	}

	var errs []error
	log.Info("reconciling guest cluster crds")
	if err := r.reconcileCRDs(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile crds: %w", err))
	}

	log.Info("reconciling clusterversion")
	if err := r.reconcileClusterVersion(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile clusterversion: %w", err))
	}

	log.Info("reconciling clusterOperators")
	if err := r.reconcileClusterOperators(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile clusterOperators: %w", err))
	}

	log.Info("reconciling guest cluster global configuration")
	if err := r.reconcileConfig(ctx, hcp, globalConfig); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile global configuration: %w", err))
	}

	log.Info("reconciling guest cluster namespaces")
	if err := r.reconcileNamespaces(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile namespaces: %w", err))
	}

	log.Info("reconciling guest cluster rbac")
	if err := r.reconcileRBAC(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile rbac: %w", err))
	}

	// IBMCloud platform should not be constantly reconciled as we allow customers to change initial deployment.
	// Initial deployment handled by managed service wrapping hypershift
	if r.platformType != hyperv1.IBMCloudPlatform {
		log.Info("reconciling registry config")
		registryConfig := manifests.Registry()
		if _, err := r.CreateOrUpdate(ctx, r.client, registryConfig, func() error {
			registry.ReconcileRegistryConfig(registryConfig, r.platformType)
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile imageregistry config: %w", err))
		}
	}

	log.Info("reconciling ingress controller")
	if err := r.reconcileIngressController(ctx, hcp, globalConfig); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile ingress controller: %w", err))
	}

	log.Info("reconciling kube control plane signer secret")
	kubeControlPlaneSignerSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-kube-apiserver-operator",
			Name:      "kube-control-plane-signer",
		},
	}
	if _, err := r.CreateOrUpdate(ctx, r.client, kubeControlPlaneSignerSecret, func() error {
		kubeControlPlaneSignerSecret.Data = map[string][]byte{corev1.TLSCertKey: []byte(r.clusterSignerCA)}
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile the %s Secret: %w", client.ObjectKeyFromObject(kubeControlPlaneSignerSecret), err))
	}

	log.Info("reconciling kubelet serving CA configmap")
	kubeletServingCAConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-config-managed",
			Name:      "kubelet-serving-ca",
		},
	}
	if _, err := r.CreateOrUpdate(ctx, r.client, kubeletServingCAConfigMap, func() error {
		kubeletServingCAConfigMap.Data = map[string]string{"ca-bundle.crt": r.clusterSignerCA}
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile the %s ConfigMap: %w", client.ObjectKeyFromObject(kubeletServingCAConfigMap), err))
	}

	log.Info("reconciling konnectivity agent")
	if err := r.reconcileKonnectivityAgent(ctx, hcp, releaseImage); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile konnectivity agent: %w", err))
	}

	log.Info("reconciling openshift apiserver apiservices")
	if err := r.reconcileOpenshiftAPIServerAPIServices(ctx, hcp); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile openshift apiserver service: %w", err))
	}

	log.Info("reconciling openshift apiserver service")
	openshiftAPIServerService := manifests.OpenShiftAPIServerClusterService()
	if _, err := r.CreateOrUpdate(ctx, r.client, openshiftAPIServerService, func() error {
		oapi.ReconcileClusterService(openshiftAPIServerService)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile openshift apiserver service: %w", err))
	}

	log.Info("reconciling openshift apiserver endpoints")
	if err := r.reconcileOpenshiftAPIServerEndpoints(ctx, hcp); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile openshift apiserver endpoints: %w", err))
	}

	log.Info("reconciling openshift oauth apiserver apiservices")
	if err := r.reconcileOpenshiftOAuthAPIServerAPIServices(ctx, hcp); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile openshift apiserver service: %w", err))
	}

	log.Info("reconciling openshift oauth apiserver service")
	openshiftOAuthAPIServerService := manifests.OpenShiftOAuthAPIServerClusterService()
	if _, err := r.CreateOrUpdate(ctx, r.client, openshiftOAuthAPIServerService, func() error {
		oapi.ReconcileClusterService(openshiftOAuthAPIServerService)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile openshift oauth apiserver service: %w", err))
	}

	log.Info("reconciling openshift oauth apiserver endpoints")
	if err := r.reconcileOpenshiftOAuthAPIServerEndpoints(ctx, hcp); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile openshift apiserver endpoints: %w", err))
	}

	log.Info("reconciling kube apiserver service monitor")
	kasServiceMonitor := manifests.KubeAPIServerServiceMonitor()
	if _, err := r.CreateOrUpdate(ctx, r.client, kasServiceMonitor, func() error {
		return monitoring.ReconcileKubeAPIServerServiceMonitor(kasServiceMonitor)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile the kube apiserver service monitor: %w", err))
	}

	log.Info("reconciling kubeadmin password hash secret")
	if err := r.reconcileKubeadminPasswordHashSecret(ctx, hcp); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile kubeadmin password hash secret: %w", err))
	}

	log.Info("reconciling monitoring configuration")
	monitoringConfig := manifests.MonitoringConfig()
	if _, err := r.CreateOrUpdate(ctx, r.client, monitoringConfig, func() error {
		return monitoring.ReconcileMonitoringConfig(monitoringConfig)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile monitoring config: %w", err))
	}

	log.Info("reconciling pull secret")
	for _, ns := range manifests.PullSecretTargetNamespaces() {
		secret := manifests.PullSecret(ns)
		if _, err := r.CreateOrUpdate(ctx, r.client, secret, func() error {
			secret.Data = pullSecret.Data
			secret.Type = pullSecret.Type
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile pull secret at namespace %s: %w", ns, err))
		}
	}

	log.Info("reconciling oauth serving cert ca bundle")
	if err := r.reconcileOAuthServingCertCABundle(ctx, hcp); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile oauth serving cert CA bundle: %w", err))
	}

	log.Info("reconciling oauth browser client")
	oauthBrowserClient := manifests.OAuthServerBrowserClient()
	if _, err := r.CreateOrUpdate(ctx, r.client, oauthBrowserClient, func() error {
		return oauth.ReconcileBrowserClient(oauthBrowserClient, r.oauthAddress, r.oauthPort)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile oauth browser client: %w", err))
	}

	log.Info("reconciling oauth challenging client")
	oauthChallengingClient := manifests.OAuthServerChallengingClient()
	if _, err := r.CreateOrUpdate(ctx, r.client, oauthChallengingClient, func() error {
		return oauth.ReconcileChallengingClient(oauthChallengingClient, r.oauthAddress, r.oauthPort)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile oauth challenging client: %w", err))
	}

	log.Info("reconciling cloud credential secrets")
	errs = append(errs, r.reconcileCloudCredentialSecrets(ctx, hcp, log)...)

	log.Info("reconciling in-cluster cloud config")
	if err := r.reconcileCloudConfig(ctx, hcp); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile the cloud config: %w", err))
	}

	log.Info("reconciling openshift controller manager service ca bundle")
	ocmServiceCA := manifests.OpenShiftControllerManagerServiceCA()
	if _, err := r.CreateOrUpdate(ctx, r.client, ocmServiceCA, func() error {
		if ocmServiceCA.Annotations == nil {
			ocmServiceCA.Annotations = map[string]string{}
		}
		ocmServiceCA.Annotations["service.beta.openshift.io/inject-cabundle"] = "true"
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile openshift controller manager service ca bundle: %w", err))
	}

	log.Info("reconciling olm resources")
	errs = append(errs, r.reconcileOLM(ctx, hcp, releaseImage)...)

	log.Info("reconciling observed configuration")
	errs = append(errs, r.reconcileObservedConfiguration(ctx, hcp)...)

	return ctrl.Result{}, errors.NewAggregate(errs)
}

func (r *reconciler) reconcileCRDs(ctx context.Context) error {
	var errs []error

	requestCount := manifests.RequestCountCRD()
	if _, err := r.CreateOrUpdate(ctx, r.client, requestCount, func() error {
		return crd.ReconcileRequestCountCRD(requestCount)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile request count crd: %w", err))
	}

	return errors.NewAggregate(errs)
}

func (r *reconciler) reconcileConfig(ctx context.Context, hcp *hyperv1.HostedControlPlane, globalConfig globalconfig.GlobalConfig) error {
	var errs []error

	apiServerAddress := hcp.Status.ControlPlaneEndpoint.Host

	if len(apiServerAddress) == 0 {
		return fmt.Errorf("hosted control plane does not have an APIServer endpoint address")
	}

	// Infrastructure is first reconciled for its spec
	infra := globalconfig.InfrastructureConfig()
	var currentInfra *configv1.Infrastructure
	if _, err := r.CreateOrUpdate(ctx, r.client, infra, func() error {
		currentInfra = infra.DeepCopy()
		globalconfig.ReconcileInfrastructure(infra, hcp)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile infrastructure config spec: %w", err))
	} else {
		// It is reconciled a second time to update its status
		globalconfig.ReconcileInfrastructure(infra, hcp)
		if !equality.Semantic.DeepEqual(infra.Status, currentInfra.Status) {
			if err := r.client.Status().Update(ctx, infra); err != nil {
				errs = append(errs, fmt.Errorf("failed to update infrastructure status: %w", err))
			}
		}
	}

	dns := globalconfig.DNSConfig()
	if _, err := r.CreateOrUpdate(ctx, r.client, dns, func() error {
		globalconfig.ReconcileDNSConfig(dns, hcp)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile dns config: %w", err))
	}

	ingress := globalconfig.IngressConfig()
	if _, err := r.CreateOrUpdate(ctx, r.client, ingress, func() error {
		globalconfig.ReconcileIngressConfig(ingress, hcp, globalConfig)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile ingress config: %w", err))
	}

	network := globalconfig.NetworkConfig()
	if _, err := r.CreateOrUpdate(ctx, r.client, network, func() error {
		globalconfig.ReconcileNetworkConfig(network, hcp, globalConfig)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile network config: %w", err))
	}

	proxy := globalconfig.ProxyConfig()
	if _, err := r.CreateOrUpdate(ctx, r.client, proxy, func() error {
		globalconfig.ReconcileProxyConfig(proxy, hcp, globalConfig)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile proxy config: %w", err))
	}

	icsp := globalconfig.ImageContentSourcePolicy()
	if _, err := r.CreateOrUpdate(ctx, r.client, icsp, func() error {
		return globalconfig.ReconcileImageContentSourcePolicy(icsp, hcp)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile image content source policy: %w", err))
	}

	installConfigCM := manifests.InstallConfigConfigMap()
	if _, err := r.CreateOrUpdate(ctx, r.client, installConfigCM, func() error {
		installConfigCM.Data = map[string]string{
			"install-config": globalconfig.NewInstallConfig(hcp).String(),
		}
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile dns config: %w", err))
	}

	return errors.NewAggregate(errs)
}

func (r *reconciler) reconcileNamespaces(ctx context.Context) error {
	namespaceManifests := []struct {
		manifest  func() *corev1.Namespace
		reconcile func(*corev1.Namespace) error
	}{
		{manifest: manifests.NamespaceOpenShiftAPIServer},
		{manifest: manifests.NamespaceOpenShiftControllerManager},
		{manifest: manifests.NamespaceKubeAPIServer, reconcile: namespaces.ReconcileKubeAPIServerNamespace},
		{manifest: manifests.NamespaceKubeControllerManager},
		{manifest: manifests.NamespaceKubeScheduler},
		{manifest: manifests.NamespaceEtcd},
		{manifest: manifests.NamespaceIngress, reconcile: namespaces.ReconcileOpenShiftIngressNamespace},
		{manifest: manifests.NamespaceAuthentication},
	}

	var errs []error
	for _, m := range namespaceManifests {
		ns := m.manifest()
		if _, err := r.CreateOrUpdate(ctx, r.client, ns, func() error {
			if m.reconcile != nil {
				return m.reconcile(ns)
			}
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile namespace %s: %w", ns.Name, err))
		}
	}

	return errors.NewAggregate(errs)
}

func (r *reconciler) reconcileRBAC(ctx context.Context) error {
	roles := []struct {
		manifest  func() *rbacv1.ClusterRole
		reconcile func(*rbacv1.ClusterRole) error
	}{
		{manifest: manifests.CSRApproverClusterRole, reconcile: rbac.ReconcileCSRApproverClusterRole},
		{manifest: manifests.IngressToRouteControllerClusterRole, reconcile: rbac.ReconcileIngressToRouteControllerClusterRole},
		{manifest: manifests.NamespaceSecurityAllocationControllerClusterRole, reconcile: rbac.ReconcileNamespaceSecurityAllocationControllerClusterRole},
	}

	roleBindings := []struct {
		manifest  func() *rbacv1.ClusterRoleBinding
		reconcile func(*rbacv1.ClusterRoleBinding) error
	}{
		{manifest: manifests.CSRApproverClusterRoleBinding, reconcile: rbac.ReconcileCSRApproverClusterRoleBinding},
		{manifest: manifests.IngressToRouteControllerClusterRoleBinding, reconcile: rbac.ReconcileIngressToRouteControllerClusterRoleBinding},
		{manifest: manifests.NamespaceSecurityAllocationControllerClusterRoleBinding, reconcile: rbac.ReconcileNamespaceSecurityAllocationControllerClusterRoleBinding},
		{manifest: manifests.NodeBootstrapperClusterRoleBinding, reconcile: rbac.ReconcileNodeBootstrapperClusterRoleBinding},
		{manifest: manifests.CSRRenewalClusterRoleBinding, reconcile: rbac.ReconcileCSRRenewalClusterRoleBinding},
		{manifest: manifests.MetricsClientClusterRoleBinding, reconcile: rbac.ReconcileGenericMetricsClusterRoleBinding("system:serviceaccount:hypershift:prometheus")},
	}

	var errs []error
	for _, m := range roles {
		role := m.manifest()
		if _, err := r.CreateOrUpdate(ctx, r.client, role, func() error {
			return m.reconcile(role)
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile role %s: %w", role.Name, err))
		}
	}

	for _, m := range roleBindings {
		rb := m.manifest()
		if _, err := r.CreateOrUpdate(ctx, r.client, rb, func() error {
			return m.reconcile(rb)
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile role binding %s: %w", rb.Name, err))
		}
	}

	return errors.NewAggregate(errs)
}

func (r *reconciler) reconcileIngressController(ctx context.Context, hcp *hyperv1.HostedControlPlane, globalConfig globalconfig.GlobalConfig) error {
	var errs []error
	p := ingress.NewIngressParams(hcp, globalConfig)
	ingressController := manifests.IngressDefaultIngressController()
	if _, err := r.CreateOrUpdate(ctx, r.client, ingressController, func() error {
		return ingress.ReconcileDefaultIngressController(ingressController, p.IngressSubdomain, p.PlatformType, p.Replicas, (hcp.Spec.Platform.IBMCloud != nil && hcp.Spec.Platform.IBMCloud.ProviderType == configv1.IBMCloudProviderTypeUPI))
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile default ingress controller: %w", err))
	}

	sourceCert := manifests.IngressCert(hcp.Namespace)
	if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(sourceCert), sourceCert); err != nil {
		errs = append(errs, fmt.Errorf("failed to get ingress cert (%s/%s) from control plane: %w", sourceCert.Namespace, sourceCert.Name, err))
	} else {
		ingressControllerCert := manifests.IngressDefaultIngressControllerCert()
		if _, err := r.CreateOrUpdate(ctx, r.client, ingressControllerCert, func() error {
			return ingress.ReconcileDefaultIngressControllerCertSecret(ingressControllerCert, sourceCert)
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile default ingress controller cert: %w", err))
		}
	}
	return errors.NewAggregate(errs)
}

func (r *reconciler) reconcileKonnectivityAgent(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage) error {
	var errs []error

	p := konnectivity.NewKonnectivityParams(hcp, releaseImage.ComponentImages(), r.konnectivityServerAddress, r.konnectivityServerPort)

	controlPlaneAgentSecret := manifests.KonnectivityControlPlaneAgentSecret(hcp.Namespace)
	if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(controlPlaneAgentSecret), controlPlaneAgentSecret); err != nil {
		errs = append(errs, fmt.Errorf("failed to get control plane konnectivity agent secret: %w", err))
	} else {
		agentSecret := manifests.KonnectivityAgentSecret()
		if _, err := r.CreateOrUpdate(ctx, r.client, agentSecret, func() error {
			konnectivity.ReconcileKonnectivityAgentSecret(agentSecret, controlPlaneAgentSecret)
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile konnectivity agent secret: %w", err))
		}
	}

	agentDaemonset := manifests.KonnectivityAgentDaemonSet()
	if _, err := r.CreateOrUpdate(ctx, r.client, agentDaemonset, func() error {
		konnectivity.ReconcileAgentDaemonSet(agentDaemonset, p.DeploymentConfig, p.Image, p.ExternalAddress, p.ExternalPort, hcp.Spec.Platform.Type)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile konnectivity agent daemonset: %w", err))
	}

	return errors.NewAggregate(errs)
}

func (r *reconciler) reconcileClusterVersion(ctx context.Context) error {
	clusterVersion := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}}
	if _, err := r.CreateOrUpdate(ctx, r.client, clusterVersion, func() error {
		clusterVersion.Spec.Upstream = ""
		clusterVersion.Spec.Channel = ""
		clusterVersion.Spec.DesiredUpdate = nil
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile clusterVersion: %w", err)
	}

	return nil
}

func (r *reconciler) reconcileOpenshiftAPIServerAPIServices(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	rootCA := manifests.RootCASecret(hcp.Namespace)
	if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return fmt.Errorf("failed to get root ca from control plane: %w", err)
	}
	var errs []error
	for _, apiSvcGroup := range manifests.OpenShiftAPIServerAPIServiceGroups() {
		apiSvc := manifests.OpenShiftAPIServerAPIService(apiSvcGroup)
		if _, err := r.CreateOrUpdate(ctx, r.client, apiSvc, func() error {
			oapi.ReconcileAPIService(apiSvc, manifests.OpenShiftAPIServerClusterService(), rootCA, apiSvcGroup)
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile openshift apiserver apiservice (%s): %w", apiSvcGroup, err))
		}
	}
	return errors.NewAggregate(errs)
}

func (r *reconciler) reconcileOpenshiftOAuthAPIServerAPIServices(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	rootCA := manifests.RootCASecret(hcp.Namespace)
	if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return fmt.Errorf("failed to get root ca from control plane: %w", err)
	}
	var errs []error
	for _, apiSvcGroup := range manifests.OpenShiftOAuthAPIServerAPIServiceGroups() {
		apiSvc := manifests.OpenShiftOAuthAPIServerAPIService(apiSvcGroup)
		if _, err := r.CreateOrUpdate(ctx, r.client, apiSvc, func() error {
			oapi.ReconcileAPIService(apiSvc, manifests.OpenShiftOAuthAPIServerClusterService(), rootCA, apiSvcGroup)
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile openshift oauth apiserver apiservice (%s): %w", apiSvcGroup, err))
		}
	}
	return errors.NewAggregate(errs)
}

func (r *reconciler) reconcileOpenshiftAPIServerEndpoints(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	cpService := manifests.OpenShiftAPIServerService(hcp.Namespace)
	if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(cpService), cpService); err != nil {
		return fmt.Errorf("failed to get openshift apiserver service from control plane: %w", err)
	}
	if len(cpService.Spec.ClusterIP) == 0 {
		return fmt.Errorf("openshift apiserver service in control plane does not yet have a cluster IP")
	}
	openshiftAPIServerEndpoints := manifests.OpenShiftAPIServerClusterEndpoints()
	_, err := r.CreateOrUpdate(ctx, r.client, openshiftAPIServerEndpoints, func() error {
		oapi.ReconcileEndpoints(openshiftAPIServerEndpoints, cpService.Spec.ClusterIP)
		return nil
	})
	return err
}

func (r *reconciler) reconcileOpenshiftOAuthAPIServerEndpoints(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	cpService := manifests.OpenShiftOAuthAPIServerService(hcp.Namespace)
	if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(cpService), cpService); err != nil {
		return fmt.Errorf("failed to get openshift oauth apiserver service from control plane: %w", err)
	}
	if len(cpService.Spec.ClusterIP) == 0 {
		return fmt.Errorf("openshift oauth apiserver service in control plane does not yet have a cluster IP")
	}
	openshiftOAuthAPIServerEndpoints := manifests.OpenShiftOAuthAPIServerClusterEndpoints()
	_, err := r.CreateOrUpdate(ctx, r.client, openshiftOAuthAPIServerEndpoints, func() error {
		oapi.ReconcileEndpoints(openshiftOAuthAPIServerEndpoints, cpService.Spec.ClusterIP)
		return nil
	})
	return err
}

func (r *reconciler) reconcileKubeadminPasswordHashSecret(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	kubeadminPasswordSecret := manifests.KubeadminPasswordSecret(hcp.Namespace)
	if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(kubeadminPasswordSecret), kubeadminPasswordSecret); err != nil {
		if apierrors.IsNotFound(err) {
			// kubeAdminPasswordHash will not exist when a user specifies an explicit oauth config
			return nil
		}
		return fmt.Errorf("failed to get kubeadmin password secret: %w", err)
	}
	kubeadminPasswordHashSecret := manifests.KubeadminPasswordHashSecret()
	if _, err := r.CreateOrUpdate(ctx, r.client, kubeadminPasswordHashSecret, func() error {
		return kubeadminpassword.ReconcileKubeadminPasswordHashSecret(kubeadminPasswordHashSecret, kubeadminPasswordSecret)
	}); err != nil {
		return err
	}
	oauthDeployment := manifests.OAuthDeployment(hcp.Namespace)
	if _, err := r.CreateOrUpdate(ctx, r.cpClient, oauthDeployment, func() error {
		if oauthDeployment.Spec.Template.ObjectMeta.Annotations == nil {
			oauthDeployment.Spec.Template.ObjectMeta.Annotations = map[string]string{}
		}
		oauthDeployment.Spec.Template.ObjectMeta.Annotations[SecretHashAnnotation] = secretHash(kubeadminPasswordHashSecret.Data["kubeadmin"])
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func secretHash(data []byte) string {
	return fmt.Sprintf("%x", md5.Sum(data))
}

func (r *reconciler) reconcileOAuthServingCertCABundle(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	oauthServingCert := manifests.OpenShiftOAuthServerCert(hcp.Namespace)
	if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(oauthServingCert), oauthServingCert); err != nil {
		return fmt.Errorf("cannot get oauth serving cert: %w", err)
	}
	caBundle := manifests.OAuthCABundle()
	if _, err := r.CreateOrUpdate(ctx, r.client, caBundle, func() error {
		return oauth.ReconcileOAuthServerCertCABundle(caBundle, oauthServingCert)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth server cert ca bundle: %w", err)
	}
	return nil
}

const awsCredentialsTemplate = `[default]
role_arn = %s
web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token
`

func (r *reconciler) reconcileCloudCredentialSecrets(ctx context.Context, hcp *hyperv1.HostedControlPlane, log logr.Logger) []error {
	var errs []error
	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		for _, role := range hcp.Spec.Platform.AWS.Roles {
			secret := manifests.AWSCloudCredsSecret(role)
			ns := &corev1.Namespace{}
			err := r.client.Get(ctx, client.ObjectKey{Name: secret.Namespace}, ns)
			if err != nil {
				if apierrors.IsNotFound(err) {
					log.Info("WARNING: cannot sync cloud credential secret because namespace does not exist", "secret", client.ObjectKeyFromObject(secret))
				} else {
					errs = append(errs, err)
				}
				continue
			}
			if _, err := r.CreateOrUpdate(ctx, r.client, secret, func() error {
				credentials := fmt.Sprintf(awsCredentialsTemplate, role.ARN)
				secret.Data = map[string][]byte{"credentials": []byte(credentials)}
				secret.Type = corev1.SecretTypeOpaque
				return nil
			}); err != nil {
				errs = append(errs, fmt.Errorf("failed to reconcile aws cloud credential secret %s/%s: %w", secret.Namespace, secret.Name, err))
			}
		}
	case hyperv1.AzurePlatform:
		referenceCredentialsSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: hcp.Spec.Platform.Azure.Credentials.Name}}
		if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(referenceCredentialsSecret), referenceCredentialsSecret); err != nil {
			return []error{fmt.Errorf("failed to get cloud credentials secret in hcp namespace: %w", err)}
		}

		secretData := map[string][]byte{
			"azure_client_id":       referenceCredentialsSecret.Data["AZURE_CLIENT_ID"],
			"azure_client_secret":   referenceCredentialsSecret.Data["AZURE_CLIENT_SECRET"],
			"azure_region":          []byte(hcp.Spec.Platform.Azure.Location),
			"azure_resource_prefix": []byte(hcp.Name + "-" + hcp.Spec.InfraID),
			"azure_resourcegroup":   []byte(hcp.Spec.Platform.Azure.ResourceGroupName),
			"azure_subscription_id": referenceCredentialsSecret.Data["AZURE_SUBSCRIPTION_ID"],
			"azure_tenant_id":       referenceCredentialsSecret.Data["AZURE_TENANT_ID"],
		}

		ingressCredentialSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-ingress-operator", Name: "cloud-credentials"}}
		if _, err := r.CreateOrUpdate(ctx, r.client, ingressCredentialSecret, func() error {
			ingressCredentialSecret.Data = secretData
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed tom reconcile guest cluster ingress operator secret: %w", err))
		}

		csiCredentialSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-cluster-csi-drivers", Name: "azure-disk-credentials"}}
		if _, err := r.CreateOrUpdate(ctx, r.client, csiCredentialSecret, func() error {
			csiCredentialSecret.Data = secretData
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile guest cluster CSI secret: %w", err))
		}

		imageRegistrySecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-image-registry", Name: "installer-cloud-credentials"}}
		if _, err := r.CreateOrUpdate(ctx, r.client, imageRegistrySecret, func() error {
			imageRegistrySecret.Data = secretData
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile guest cluster image-registry secret: %w", err))
		}

		cloudNetworkConfigControllerSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-cloud-network-config-controller", Name: "cloud-credentials"}}
		if _, err := r.CreateOrUpdate(ctx, r.client, cloudNetworkConfigControllerSecret, func() error {
			cloudNetworkConfigControllerSecret.Data = secretData
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile guest cluster cloud-network-config-controller secret: %w", err))
		}
	}
	return errs
}

func (r *reconciler) reconcileOLM(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage) []error {
	var errs []error

	p := olm.NewOperatorLifecycleManagerParams(hcp, releaseImage.Version())

	catalogs := []struct {
		manifest  func() *operatorsv1alpha1.CatalogSource
		reconcile func(*operatorsv1alpha1.CatalogSource, *olm.OperatorLifecycleManagerParams)
	}{
		{manifest: manifests.CertifiedOperatorsCatalogSource, reconcile: olm.ReconcileCertifiedOperatorsCatalogSource},
		{manifest: manifests.CommunityOperatorsCatalogSource, reconcile: olm.ReconcileCommunityOperatorsCatalogSource},
		{manifest: manifests.RedHatMarketplaceCatalogSource, reconcile: olm.ReconcileRedHatMarketplaceCatalogSource},
		{manifest: manifests.RedHatOperatorsCatalogSource, reconcile: olm.ReconcileRedHatOperatorsCatalogSource},
	}

	for _, catalog := range catalogs {
		cs := catalog.manifest()
		if _, err := r.CreateOrUpdate(ctx, r.client, cs, func() error {
			catalog.reconcile(cs, p)
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile catalog source %s/%s: %w", cs.Namespace, cs.Name, err))
		}
	}

	olmAlertRules := manifests.OLMAlertRules()
	if _, err := r.CreateOrUpdate(ctx, r.client, olmAlertRules, func() error {
		olm.ReconcileOLMAlertRules(olmAlertRules)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile olm alert rules: %w", err))
	}

	rootCA := manifests.RootCASecret(hcp.Namespace)
	if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		errs = append(errs, fmt.Errorf("failed to get root ca cert from control plane namespace: %w", err))
	} else {
		packageServerAPIService := manifests.OLMPackageServerAPIService()
		if _, err := r.CreateOrUpdate(ctx, r.client, packageServerAPIService, func() error {
			olm.ReconcilePackageServerAPIService(packageServerAPIService, rootCA)
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile OLM packageserver API service: %w", err))
		}
	}

	packageServerService := manifests.OLMPackageServerService()
	if _, err := r.CreateOrUpdate(ctx, r.client, packageServerService, func() error {
		olm.ReconcilePackageServerService(packageServerService)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile OLM packageserver service: %w", err))
	}

	cpService := manifests.OLMPackageServerControlPlaneService(hcp.Namespace)
	if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(cpService), cpService); err != nil {
		errs = append(errs, fmt.Errorf("failed to get packageserver service from control plane namespace: %w", err))
	} else {
		if len(cpService.Spec.ClusterIP) == 0 {
			errs = append(errs, fmt.Errorf("packageserver service does not yet have a cluster IP"))
		} else {
			packageServerEndpoints := manifests.OLMPackageServerEndpoints()
			if _, err := r.CreateOrUpdate(ctx, r.client, packageServerEndpoints, func() error {
				olm.ReconcilePackageServerEndpoints(packageServerEndpoints, cpService.Spec.ClusterIP)
				return nil
			}); err != nil {
				errs = append(errs, fmt.Errorf("failed to reconcile OLM packageserver service: %w", err))
			}
		}
	}

	return errs
}

func (r *reconciler) reconcileObservedConfiguration(ctx context.Context, hcp *hyperv1.HostedControlPlane) []error {
	var errs []error
	configs := []struct {
		name       string
		source     client.Object
		observedCM *corev1.ConfigMap
	}{
		{
			source:     globalconfig.ImageConfig(),
			observedCM: globalconfig.ObservedImageConfig(hcp.Namespace),
		},
		{
			source:     globalconfig.BuildConfig(),
			observedCM: globalconfig.ObservedBuildConfig(hcp.Namespace),
		},
		{
			source:     globalconfig.ProjectConfig(),
			observedCM: globalconfig.ObservedProjectConfig(hcp.Namespace),
		},
	}

	ownerRef := config.OwnerRefFrom(hcp)
	for _, cfg := range configs {
		err := func() error {
			sourceConfig := cfg.source
			if err := r.client.Get(ctx, client.ObjectKeyFromObject(sourceConfig), sourceConfig); err != nil {
				if apierrors.IsNotFound(err) {
					sourceConfig = nil
				} else {
					return fmt.Errorf("cannot get config (%s): %w", sourceConfig.GetName(), err)
				}
			}
			observedConfig := cfg.observedCM
			if sourceConfig == nil {
				if err := r.cpClient.Delete(ctx, observedConfig); err != nil && !apierrors.IsNotFound(err) {
					return fmt.Errorf("cannot delete observed config: %w", err)
				}
				return nil
			}
			if _, err := r.CreateOrUpdate(ctx, r.cpClient, observedConfig, func() error {
				ownerRef.ApplyTo(observedConfig)
				return globalconfig.ReconcileObservedConfig(observedConfig, sourceConfig)
			}); err != nil {
				return err
			}
			return nil
		}()
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs

}

func (r *reconciler) reconcileCloudConfig(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	// This is needed for the e2e tests and only for Azure: https://github.com/openshift/origin/blob/625733dd1ce7ebf40c3dd0abd693f7bb54f2d580/test/extended/util/cluster/cluster.go#L186
	if hcp.Spec.Platform.Type != hyperv1.AzurePlatform {
		return nil
	}

	reference := cpomanifests.AzureProviderConfig(hcp.Namespace)
	if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(reference), reference); err != nil {
		return fmt.Errorf("failed to fetch %s/%s configmap from management cluster: %w", reference.Namespace, reference.Name, err)
	}

	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-config", Name: "cloud-provider-config"}}
	if _, err := r.CreateOrUpdate(ctx, r.client, cm, func() error {
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data["config"] = reference.Data[azure.CloudConfigKey]
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile the %s/%s configmap: %w", cm.Namespace, cm.Name, err)
	}

	return nil
}
