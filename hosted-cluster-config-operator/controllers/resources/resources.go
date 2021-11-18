package resources

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	configv1 "github.com/openshift/api/config/v1"
	imageregistryv1 "github.com/openshift/api/imageregistry/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/crd"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/manifests"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/monitoring"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/namespaces"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/rbac"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/registry"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/operator"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/upsert"
)

const ControllerName = "resources"

type reconciler struct {
	client crclient.Client
	upsert.CreateOrUpdateProvider
	platformType    hyperv1.PlatformType
	clusterSignerCA string
	cpClient        crclient.Client
	hcpName         string
	hcpNamespace    string
}

// eventHandler is the handler used throughout. As this controller reconciles all kind of different resources
// it uses an empty request but always reconciles everything.
func eventHandler() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(
		func(crclient.Object) []reconcile.Request {
			return []reconcile.Request{{}}
		})
}

func Setup(opts *operator.HostedClusterConfigOperatorConfig) error {
	c, err := controller.New(ControllerName, opts.Manager(), controller.Options{Reconciler: &reconciler{
		client:                 opts.Manager().GetClient(),
		CreateOrUpdateProvider: opts.TargetCreateOrUpdateProvider,
		platformType:           opts.PlatformType,
		clusterSignerCA:        opts.ClusterSignerCA(),
		cpClient:               opts.CPCluster.GetClient(),
		hcpName:                opts.HCPName(),
		hcpNamespace:           opts.Namespace(),
	}})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}
	resourcesToWatch := []crclient.Object{
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
		&operatorv1.OpenShiftControllerManager{},
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
	if err := r.cpClient.Get(ctx, crclient.ObjectKeyFromObject(hcp), hcp); err != nil {
		return fmt.Errorf("failed to get hosted control plane %s/%s: %w", r.hcpNamespace, r.hcpName, err)
	}

	globalConfig, err := globalconfig.ParseGlobalConfig(ctx, hcp.Spec.Configuration)
	if err != nil {
		return fmt.Errorf("failed to parse global config for control plane %s/%s: %w", r.hcpNamespace, r.hcpName, err)
	}

	var errs []error
	log.Info("reconciling guest cluster crds")
	if err := r.reconcileCRDs(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile crds: %w", err))
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

	log.Info("reconciling registry config")
	registryConfig := manifests.Registry()
	if _, err := r.CreateOrUpdate(ctx, r.client, registryConfig, func() error {
		registry.ReconcileRegistryConfig(registryConfig, r.platformType == hyperv1.NonePlatform)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile imageregistry config: %w", err))
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
		errs = append(errs, fmt.Errorf("failed to reconcile the %s Secret: %w", crclient.ObjectKeyFromObject(kubeControlPlaneSignerSecret), err))
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
		errs = append(errs, fmt.Errorf("failed to reconcile the %s ConfigMap: %w", crclient.ObjectKeyFromObject(kubeletServingCAConfigMap), err))
	}

	log.Info("reconciling kube apiserver service monitor")
	kasServiceMonitor := manifests.KubeAPIServerServiceMonitor()
	if _, err := r.CreateOrUpdate(ctx, r.client, kasServiceMonitor, func() error {
		return monitoring.ReconcileKubeAPIServerServiceMonitor(kasServiceMonitor)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile the kube apiserver service monitor: %w", err))
	}

	log.Info("Reconciling OpenShiftControllerManager")
	if err := r.reconcileOpenshiftControllerManager(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile OpenShiftControllerManager: %w", err))
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
		{manifest: manifests.ServiceAccountIssuerDiscoveryClusterRoleBinding, reconcile: rbac.ReconcileServiceAccountIssuerDiscoveryClusterRoleBinding},
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

// Conformance tests expect this to be set: https://github.com/openshift/origin/blob/be5b2284418341b8b0e228e9fdfa9d074d667b46/test/extended/util/framework.go#L146-L152
const ocmObservedConfigValue = `{"dockerPullSecret": {"internalRegistryHostname": "image-registry.openshift-image-registry.svc:5000"}}`

func (r *reconciler) reconcileOpenshiftControllerManager(ctx context.Context) error {
	openshiftControllerManager := &operatorv1.OpenShiftControllerManager{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
	}
	if _, err := r.CreateOrUpdate(ctx, r.client, openshiftControllerManager, func() error {
		openshiftControllerManager.Spec.ObservedConfig = runtime.RawExtension{Raw: []byte(ocmObservedConfigValue)}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile %T %s: %w", openshiftControllerManager, crclient.ObjectKeyFromObject(openshiftControllerManager), err)
	}

	return nil
}
