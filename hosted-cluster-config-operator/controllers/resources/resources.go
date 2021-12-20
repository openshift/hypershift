package resources

import (
	"context"
	"fmt"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/registry"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	configv1 "github.com/openshift/api/config/v1"
	imageregistryv1 "github.com/openshift/api/imageregistry/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/crd"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/ingress"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/konnectivity"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/manifests"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/monitoring"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/namespaces"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/rbac"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/operator"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/upsert"
)

const ControllerName = "resources"

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
	versions                  map[string]string
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
		versions:                  opts.Versions,
	}})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}
	resourcesToWatch := []client.Object{
		&imageregistryv1.Config{},
		&corev1.ConfigMap{},
		&corev1.Namespace{},
		&corev1.Secret{},
		&rbacv1.ClusterRole{},
		&rbacv1.ClusterRoleBinding{},
		&configv1.Infrastructure{},
		&configv1.DNS{},
		&configv1.Ingress{},
		&configv1.Network{},
		&configv1.Proxy{},
		&appsv1.DaemonSet{},
		&configv1.ClusterOperator{},
		&configv1.ClusterVersion{},
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
	return ctrl.Result{}, r.reconcile(ctx)
}

func (r *reconciler) reconcile(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling")

	hcp := manifests.HostedControlPlane(r.hcpNamespace, r.hcpName)
	if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(hcp), hcp); err != nil {
		return fmt.Errorf("failed to get hosted control plane %s/%s: %w", r.hcpNamespace, r.hcpName, err)
	}

	globalConfig, err := globalconfig.ParseGlobalConfig(ctx, hcp.Spec.Configuration)
	if err != nil {
		return fmt.Errorf("failed to parse global config for control plane %s/%s: %w", r.hcpNamespace, r.hcpName, err)
	}

	pullSecret := manifests.PullSecret(hcp.Namespace)
	if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
		return fmt.Errorf("failed to get pull secret: %w", err)
	}

	releaseImage, err := r.releaseProvider.Lookup(ctx, hcp.Spec.ReleaseImage, pullSecret.Data[corev1.DockerConfigJsonKey])
	if err != nil {
		return fmt.Errorf("failed to get lookup release image %s: %w", hcp.Spec.ReleaseImage, err)
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
			registry.ReconcileRegistryConfig(registryConfig, r.platformType == hyperv1.NonePlatform)
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile imageregistry config: %w", err))
		}
	}

	log.Info("reconciling ingress controller")
	if err := r.reconcileIngressController(ctx, hcp); err != nil {
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

	log.Info("reconciling kube apiserver service monitor")
	kasServiceMonitor := manifests.KubeAPIServerServiceMonitor()
	if _, err := r.CreateOrUpdate(ctx, r.client, kasServiceMonitor, func() error {
		return monitoring.ReconcileKubeAPIServerServiceMonitor(kasServiceMonitor)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile the kube apiserver service monitor: %w", err))
	}

	log.Info("reconciling monitoring configuration")
	monitoringConfig := manifests.MonitoringConfig()
	if _, err := r.CreateOrUpdate(ctx, r.client, monitoringConfig, func() error {
		return monitoring.ReconcileMonitoringConfig(monitoringConfig)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile monitoring config: %w", err))
	}

	return errors.NewAggregate(errs)
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

func (r *reconciler) reconcileIngressController(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	var errs []error
	p := ingress.NewIngressParams(hcp)
	ingressController := manifests.IngressDefaultIngressController()
	if _, err := r.CreateOrUpdate(ctx, r.client, ingressController, func() error {
		return ingress.ReconcileDefaultIngressController(ingressController, p.IngressSubdomain, p.PlatformType, p.Replicas)
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
		konnectivity.ReconcileAgentDaemonSet(agentDaemonset, p.DeploymentConfig, p.Image, p.ExternalAddress, p.ExternalPort)
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
