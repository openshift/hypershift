package resources

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/openstack"
	kubevirtcsi "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/csi/kubevirt"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cvo"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	cpoauth "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/oauth"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ocm"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	alerts "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/alerts"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/cco"
	ccm "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/cloudcontrollermanager/azure"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/crd"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/ingress"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/kas"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/konnectivity"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/kubeadminpassword"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/monitoring"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/namespaces"
	networkoperator "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/network"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/oapi"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/oauth"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/olm"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/rbac"
	dr "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/recovery"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/registry"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/storage"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	"github.com/openshift/api/annotations"
	configv1 "github.com/openshift/api/config/v1"
	imageregistryv1 "github.com/openshift/api/imageregistry/v1"
	openshiftcpv1 "github.com/openshift/api/openshiftcontrolplane/v1"
	operatorv1 "github.com/openshift/api/operator/v1"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"k8s.io/utils/ptr"
	"k8s.io/utils/set"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/blang/semver"
	"github.com/go-logr/logr"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

const (
	ControllerName         = "resources"
	ConfigNamespace        = "openshift-config"
	ConfigManagedNamespace = "openshift-config-managed"
	CloudProviderCMName    = "cloud-provider-config"
	awsCredentialsTemplate = `[default]
role_arn = %s
web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token
sts_regional_endpoints = regional
region = %s
`
)

var (
	// deleteDNSOperatorDeploymentOnce ensures that the reconciler tries
	// to delete any existing DNS operator deployment on the hosted cluster
	// only once.
	deleteDNSOperatorDeploymentOnce sync.Once
	deleteCVORemovedResourcesOnce   sync.Once
)

const azureCCMScript = `
#!/bin/bash
set -o allexport
if [[ -f /etc/kubernetes/apiserver-url.env ]]; then
  source /etc/kubernetes/apiserver-url.env
fi
exec /bin/azure-cloud-node-manager \
  --node-name=${NODE_NAME} \
  --enable-deprecated-beta-topology-labels \
  --wait-routes=false
`

type reconciler struct {
	client         client.Client
	uncachedClient client.Client
	upsert.CreateOrUpdateProvider
	platformType              hyperv1.PlatformType
	rootCA                    string
	clusterSignerCA           string
	cpClient                  client.Client
	kubevirtInfraClient       client.Client
	hcpName                   string
	hcpNamespace              string
	releaseProvider           releaseinfo.Provider
	konnectivityServerAddress string
	konnectivityServerPort    int32
	oauthAddress              string
	oauthPort                 int32
	versions                  map[string]string
	operateOnReleaseImage     string
	ImageMetaDataProvider     util.ImageMetadataProvider
}

// eventHandler is the handler used throughout. As this controller reconciles all kind of different resources
// it uses an empty request but always reconciles everything.
func eventHandler() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(
		func(context.Context, client.Object) []reconcile.Request {
			return []reconcile.Request{{}}
		})
}

func Setup(ctx context.Context, opts *operator.HostedClusterConfigOperatorConfig) error {
	if err := imageregistryv1.Install(opts.Manager.GetScheme()); err != nil {
		return fmt.Errorf("failed to add to scheme: %w", err)
	}

	uncachedClientRestConfig := opts.Manager.GetConfig()
	uncachedClientRestConfig.WarningHandler = rest.NoWarnings{}
	uncachedClient, err := client.New(uncachedClientRestConfig, client.Options{
		Scheme: opts.Manager.GetScheme(),
		Mapper: opts.Manager.GetRESTMapper(),
	})
	if err != nil {
		return fmt.Errorf("failed to create uncached client: %w", err)
	}

	// if kubevirt infra config is not used, it is being set the same as the mgmt config
	kubevirtInfraClientRestConfig := opts.KubevirtInfraConfig
	kubevirtInfraClientRestConfig.WarningHandler = rest.NoWarnings{}
	kubevirtInfraClient, err := client.New(kubevirtInfraClientRestConfig, client.Options{
		Scheme: opts.Manager.GetScheme(),
		Mapper: opts.Manager.GetRESTMapper(),
	})
	if err != nil {
		return fmt.Errorf("failed to create kubevirt infra uncached client: %w", err)
	}

	c, err := controller.New(ControllerName, opts.Manager, controller.Options{Reconciler: &reconciler{
		client:                    opts.Manager.GetClient(),
		uncachedClient:            uncachedClient,
		CreateOrUpdateProvider:    opts.TargetCreateOrUpdateProvider,
		platformType:              opts.PlatformType,
		rootCA:                    opts.InitialCA,
		clusterSignerCA:           opts.ClusterSignerCA,
		cpClient:                  opts.CPCluster.GetClient(),
		kubevirtInfraClient:       kubevirtInfraClient,
		hcpName:                   opts.HCPName,
		hcpNamespace:              opts.Namespace,
		releaseProvider:           opts.ReleaseProvider,
		konnectivityServerAddress: opts.KonnectivityAddress,
		konnectivityServerPort:    opts.KonnectivityPort,
		oauthAddress:              opts.OAuthAddress,
		oauthPort:                 opts.OAuthPort,
		versions:                  opts.Versions,
		operateOnReleaseImage:     opts.OperateOnReleaseImage,
		ImageMetaDataProvider:     opts.ImageMetaDataProvider,
	}})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	ct, err := client.New(opts.CPCluster.GetConfig(), client.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	hcp := manifests.HostedControlPlane(opts.Namespace, opts.HCPName)
	if err = ct.Get(ctx, client.ObjectKeyFromObject(hcp), hcp); err != nil {
		return fmt.Errorf("failed to get HCP: %w", err)
	}

	resourcesToWatch := []client.Object{
		&imageregistryv1.Config{},
		&corev1.ConfigMap{},
		&corev1.Namespace{},
		&corev1.Secret{},
		&corev1.Service{},
		&corev1.Endpoints{},
		&corev1.PersistentVolumeClaim{},
		&corev1.PersistentVolume{},
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
		&configv1.ClusterOperator{},
		&configv1.OperatorHub{},
		&appsv1.DaemonSet{},
		&configv1.ClusterOperator{},
		&configv1.ClusterVersion{},
		&apiregistrationv1.APIService{},
		&operatorv1.Network{},
		&admissionregistrationv1.MutatingWebhookConfiguration{},
		&admissionregistrationv1.ValidatingWebhookConfiguration{},
		&prometheusoperatorv1.PrometheusRule{},
		&discoveryv1.EndpointSlice{},
	}

	if capabilities.IsIngressCapabilityEnabled(hcp.Spec.Capabilities) {
		resourcesToWatch = append(resourcesToWatch, &operatorv1.IngressController{})
	}

	for _, r := range resourcesToWatch {
		if err := c.Watch(source.Kind[client.Object](opts.Manager.GetCache(), r, eventHandler())); err != nil {
			return fmt.Errorf("failed to watch %T: %w", r, err)
		}
	}

	if err := c.Watch(source.Kind[client.Object](opts.CPCluster.GetCache(), &hyperv1.HostedControlPlane{}, eventHandler())); err != nil {
		return fmt.Errorf("failed to watch HostedControlPlane: %w", err)
	}

	// HCCO needs to watch for KubeletConfig ConfigMaps on the Control plane cluster (MNG cluster)
	// and mirrors them to the hosted cluster so the operators on the hosted cluster
	// could access the data in the mirrored ConfigMaps.
	// This is driven by Telco customers that would like to use the NUMAResource-operator
	// https://github.com/openshift-kni/numaresources-operator/tree/main
	// on their hosted clusters.
	// NUMAResource-operator needs access to the KubeletConfig
	//  so it could run properly on the cluster.
	p := predicate.NewPredicateFuncs(func(o client.Object) bool {
		cm := o.(*corev1.ConfigMap)
		if _, ok := cm.Labels[nodepool.KubeletConfigConfigMapLabel]; ok {
			return true
		}
		return false
	})
	if err := c.Watch(source.Kind[client.Object](opts.CPCluster.GetCache(), &corev1.ConfigMap{}, eventHandler(), p)); err != nil {
		return fmt.Errorf("failed to watch ConfigMap: %w", err)
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

	if !hcp.DeletionTimestamp.IsZero() {
		if shouldCleanupCloudResources(hcp) {
			log.Info("Cleaning up hosted cluster cloud resources")
			return r.destroyCloudResources(ctx, hcp)
		}
		return ctrl.Result{}, nil
	}

	if isPaused, duration := util.IsReconciliationPaused(log, hcp.Spec.PausedUntil); isPaused {
		log.Info("Reconciliation paused", "pausedUntil", *hcp.Spec.PausedUntil)
		return ctrl.Result{RequeueAfter: duration}, nil
	}
	if r.operateOnReleaseImage != "" && r.operateOnReleaseImage != hcp.Spec.ReleaseImage {
		log.Info("releaseImage is " + hcp.Spec.ReleaseImage + ", but this operator is configured for " + r.operateOnReleaseImage + ", skipping reconciliation")
		return ctrl.Result{}, nil
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

	// Clusters with "none" as their Kubernetes API server endpoint reconciliation
	// type must manually manage the Kubernetes endpoints and endpointslice resources.
	// Due to recent Kubernetes changes, we need to reconcile these resources to avoid
	// problems such as [1].
	// [1] https://github.com/kubernetes/kubernetes/issues/118777
	log.Info("reconciling kubernetes.default endpoints and endpointslice")
	if err := r.reconcileKASEndpoints(ctx, hcp); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile kubernetes.default endpoints and endpointslice: %w", err))
	}

	releaseImageVersion, err := semver.Parse(releaseImage.Version())
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to parse release image version: %w", err)
	}

	// The exception for IBMCloudPlatform is due to the fact that the IBM will include new certificates for HCCO from 4.17 version
	if !(hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform && (releaseImageVersion.Major == 4 && releaseImageVersion.Minor < 17)) {
		// Apply new ValidatingAdmissionPolicy to restrict the modification/deletion of certain
		// objects from the DataPlane which are managed by the HCCO.
		if err := kas.ReconcileKASValidatingAdmissionPolicies(ctx, hcp, r.client, r.CreateOrUpdate); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile validating admission policies: %w", err))
		}
	}

	log.Info("reconciling install configmap")
	if err := r.reconcileInstallConfigMap(ctx, releaseImage); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile install configmap: %w", err))
	}

	log.Info("reconciling guest cluster alert rules")
	if err := r.reconcileGuestClusterAlertRules(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile guest cluster alert rules: %w", err))
	}

	log.Info("reconciling clusterversion")
	if err := r.reconcileClusterVersion(ctx, hcp); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile clusterversion: %w", err))
	}

	log.Info("reconciling clusterOperators")
	if err := r.reconcileClusterOperators(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile clusterOperators: %w", err))
	}

	log.Info("reconciling guest cluster global configuration")
	if err := r.reconcileConfig(ctx, hcp); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile global configuration: %w", err))
	}

	log.Info("reconciling guest cluster namespaces")
	if err := r.reconcileNamespaces(ctx, hcp); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile namespaces: %w", err))
	}

	log.Info("reconciling guest cluster rbac")
	if err := r.reconcileRBAC(ctx, hcp); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile rbac: %w", err))
	}

	registryConfig := manifests.Registry()
	var registryConfigExists bool
	// Check if the registry config exists
	if err := r.client.Get(ctx, client.ObjectKeyFromObject(registryConfig), registryConfig); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to get registry config: %w", err)
		}
	} else {
		registryConfigExists = true
	}

	// For platforms where cluster-image-registry-operator (CIRO) needs a PVC to be created, bootstrap needs to happen
	// in CIRO before the registry config is created. For now, this is the case for the OpenStack platform.
	// If the object exist, we reconcile the registry config for other fields as it should be fine since the PVC would
	// exist at this point.
	if capabilities.IsImageRegistryCapabilityEnabled(hcp.Spec.Capabilities) {
		if imageRegistryPlatformWithPVC(hcp.Spec.Platform.Type) && (!registryConfigExists || registryConfig == nil) {
			log.Info("skipping registry config to let CIRO bootstrap")
		} else {
			log.Info("reconciling image registry validating admission policy")
			if r.platformType == hyperv1.AzurePlatform {
				if err := registry.ReconcileRegistryConfigValidatingAdmissionPolicies(ctx, hcp, r.client, r.CreateOrUpdate); err != nil {
					errs = append(errs, fmt.Errorf("failed to reconcile image registry validating admission policy: %w", err))
				}
			}
			log.Info("reconciling registry config")
			if _, err := r.CreateOrUpdate(ctx, r.client, registryConfig, func() error {
				err = registry.ReconcileRegistryConfig(registryConfig, r.platformType, hcp.Spec.InfrastructureAvailabilityPolicy)
				if err != nil {
					return err
				}
				return nil
			}); err != nil {
				errs = append(errs, fmt.Errorf("failed to reconcile imageregistry config: %w", err))
			}

			// TODO: remove this when ROSA HCP stops setting the managementState to Removed to disable the Image Registry
			if registryConfig.Spec.ManagementState == operatorv1.Removed && r.platformType != hyperv1.IBMCloudPlatform && r.platformType != hyperv1.AzurePlatform {
				log.Info("imageregistry operator managementstate is removed, disabling openshift-controller-manager controllers and cleaning up resources")
				ocmConfigMap := cpomanifests.OpenShiftControllerManagerConfig(r.hcpNamespace)
				if _, err := r.CreateOrUpdate(ctx, r.cpClient, ocmConfigMap, func() error {
					if ocmConfigMap.Data == nil {
						// CPO has not created the configmap yet, wait for create
						// This should not happen as we are started by the CPO after the configmap should be created
						return nil
					}
					config := &openshiftcpv1.OpenShiftControllerManagerConfig{}
					if configStr, exists := ocmConfigMap.Data[ocm.ConfigKey]; exists && len(configStr) > 0 {
						err := util.DeserializeResource(configStr, config, api.Scheme)
						if err != nil {
							return fmt.Errorf("unable to decode existing openshift controller manager configuration: %w", err)
						}
					}
					config.Controllers = []string{"*", fmt.Sprintf("-%s", openshiftcpv1.OpenShiftServiceAccountPullSecretsController)}
					configStr, err := util.SerializeResource(config, api.Scheme)
					if err != nil {
						return fmt.Errorf("failed to serialize openshift controller manager configuration: %w", err)
					}
					ocmConfigMap.Data[ocm.ConfigKey] = configStr
					return nil
				}); err != nil {
					errs = append(errs, fmt.Errorf("failed to reconcile openshift-controller-manager config: %w", err))
				}
			}
		}
	}

	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.Image != nil && hcp.Spec.Configuration.Image.AdditionalTrustedCA.Name != "" {
		additionalTrustedCAName := hcp.Spec.Configuration.Image.AdditionalTrustedCA.Name
		src := &corev1.ConfigMap{}
		err := r.cpClient.Get(ctx, client.ObjectKey{Namespace: hcp.Namespace, Name: additionalTrustedCAName}, src)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to get image registry additional trusted CA configmap %s: %w", additionalTrustedCAName, err))
		} else {
			dst := manifests.ImageRegistryAdditionalTrustedCAConfigMap(additionalTrustedCAName)
			if _, err := r.CreateOrUpdate(ctx, r.client, dst, func() error {
				dst.Data = src.Data
				return nil
			}); err != nil {
				errs = append(errs, fmt.Errorf("failed to reconcile image registry additional trusted CA configmap %s: %w", additionalTrustedCAName, err))
			}
		}
	}
	// Reconcile the IngressController resource only if the ingress capability is enabled.
	// Skip this step if the user explicitly disabled ingress.
	if capabilities.IsIngressCapabilityEnabled(hcp.Spec.Capabilities) {
		log.Info("reconciling ingress controller")
		if err := r.reconcileIngressController(ctx, hcp); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile ingress controller: %w", err))
		}
	}

	log.Info("reconciling oauth client secrets")
	if err := r.reconcileAuthOIDC(ctx, hcp); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile oauth client secrets: %w", err))
	}

	log.Info("reconciling kube control plane signer secret")
	kubeControlPlaneSignerSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-kube-apiserver-operator",
			Name:      "kube-control-plane-signer",
			Annotations: map[string]string{
				annotations.OpenShiftComponent: "kube-apiserver",
			},
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
			Namespace: ConfigManagedNamespace,
			Name:      "kubelet-serving-ca",
			Annotations: map[string]string{
				annotations.OpenShiftComponent: "kube-controller-manager",
			},
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

	if util.HCPOAuthEnabled(hcp) {
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

		log.Info("reconciling kubeadmin password hash secret")
		if err := r.reconcileKubeadminPasswordHashSecret(ctx, hcp); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile kubeadmin password hash secret: %w", err))
		}
	}

	log.Info("reconciling kube apiserver service monitor")
	kasServiceMonitor := manifests.KubeAPIServerServiceMonitor()
	if _, err := r.CreateOrUpdate(ctx, r.client, kasServiceMonitor, func() error {
		return monitoring.ReconcileKubeAPIServerServiceMonitor(kasServiceMonitor)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile the kube apiserver service monitor: %w", err))
	}

	log.Info("reconciling network operator")
	networkOperator := networkoperator.NetworkOperator()
	var ovnConfig *hyperv1.OVNKubernetesConfig
	if hcp.Spec.OperatorConfiguration != nil && hcp.Spec.OperatorConfiguration.ClusterNetworkOperator != nil {
		ovnConfig = hcp.Spec.OperatorConfiguration.ClusterNetworkOperator.OVNKubernetesConfig
	}
	if _, err := r.CreateOrUpdate(ctx, r.client, networkOperator, func() error {
		networkoperator.ReconcileNetworkOperator(networkOperator, hcp.Spec.Networking.NetworkType, hcp.Spec.Platform.Type, util.IsDisableMultiNetwork(hcp), ovnConfig)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile network operator: %w", err))
	}
	// Detect suboptimal MTU size on kubevirt hosted cluster with ovn-k and raise a condition in such a case
	if err := networkoperator.DetectSuboptimalMTU(ctx, r.cpClient, networkOperator, hcp); err != nil {
		errs = append(errs, err)
	}
	// this allows users to disable data collection in sensitive environments
	// solves https://issues.redhat.com/browse/OCPBUGS-12208
	ensureExistsReconciliationStrategy := false
	if _, exists := hcp.Annotations[hyperv1.EnsureExistsPullSecretReconciliation]; exists {
		ensureExistsReconciliationStrategy = true
	}
	log.Info("reconciling pull secret")
	for _, ns := range manifests.PullSecretTargetNamespaces() {
		secret := manifests.PullSecret(ns)
		if _, err := r.CreateOrUpdate(ctx, r.client, secret, func() error {
			if !ensureExistsReconciliationStrategy || len(secret.Data) == 0 {
				secret.Data = pullSecret.Data
				secret.Type = pullSecret.Type
			}
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile pull secret at namespace %s: %w", ns, err))
		}
	}

	log.Info("reconciling user cert CA bundle")
	if err := r.reconcileUserCertCABundle(ctx, hcp); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile user cert CA bundle: %w", err))
	}

	log.Info("reconciling proxy CA bundle")
	if err := r.reconcileProxyCABundle(ctx, hcp); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile proxy CA bundle: %w", err))
	}

	if util.HCPOAuthEnabled(hcp) {
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

		log.Info("reconciling oauth cli client")
		oauthCLIClient := manifests.OAuthServerCLIClient()
		if _, err := r.CreateOrUpdate(ctx, r.client, oauthCLIClient, func() error {
			return oauth.ReconcileCLIClient(oauthCLIClient, r.oauthAddress, r.oauthPort)
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile oauth cli client: %w", err))
		}

		log.Info("reconciling oauth serving cert rbac")
		oauthServingCertRole := manifests.OAuthServingCertRole()
		if _, err := r.CreateOrUpdate(ctx, r.client, oauthServingCertRole, func() error {
			return oauth.ReconcileOauthServingCertRole(oauthServingCertRole)
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile oauth serving cert role: %w", err))
		}

		oauthServingCertRoleBinding := manifests.OAuthServingCertRoleBinding()
		if _, err := r.CreateOrUpdate(ctx, r.client, oauthServingCertRoleBinding, func() error {
			return oauth.ReconcileOauthServingCertRoleBinding(oauthServingCertRoleBinding)
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile oauth serving cert rolebinding: %w", err))
		}
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
	errs = append(errs, r.reconcileOLM(ctx, hcp, pullSecret)...)

	log.Info("reconciling kubelet configs")
	if err := r.reconcileKubeletConfig(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile kubelet config: %w", err))
	}

	if hostedcontrolplane.IsStorageAndCSIManaged(hcp) {
		log.Info("reconciling storage resources")
		errs = append(errs, r.reconcileStorage(ctx, hcp)...)

		log.Info("reconciling node level csi configuration")
		if err := r.reconcileCSIDriver(ctx, hcp, releaseImage); err != nil {
			errs = append(errs, err)
		}
	}

	recyclerServiceAccount := manifests.RecyclerServiceAccount()
	if _, err := r.CreateOrUpdate(ctx, r.client, recyclerServiceAccount, func() error {
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile pv recycler service account: %w", err))
	}

	log.Info("reconciling observed configuration")
	errs = append(errs, r.reconcileObservedConfiguration(ctx, hcp)...)

	errs = append(errs, r.ensureGuestAdmissionWebhooksAreValid(ctx))

	// Delete the DNS operator deployment in the hosted cluster, if it is
	// present there.  A separate DNS operator deployment runs as part of
	// the hosted control-plane, but an upgraded cluster might still have
	// an old DNS operator deployment in the hosted cluster.  The caching
	// client has a label selector that doesn't match the deployment,
	// so we must use the uncached client for this delete call.  To avoid
	// excessive API calls using the uncached client, the delete call is
	// guarded using a sync.Once.
	if r.isClusterVersionUpdated(ctx, releaseImage.Version()) {
		deleteDNSOperatorDeploymentOnce.Do(func() {
			dnsOperatorDeployment := manifests.DNSOperatorDeployment()
			log.Info("removing any existing DNS operator deployment")
			if err := r.uncachedClient.Delete(ctx, dnsOperatorDeployment); err != nil && !apierrors.IsNotFound(err) {
				errs = append(errs, err)
			}
		})
		deleteCVORemovedResourcesOnce.Do(func() {
			resources := cvo.ResourcesToRemove(hcp.Spec.Platform.Type)
			for _, resource := range resources {
				log.Info("removing existing resources", "resource", resource)
				if err := r.uncachedClient.Delete(ctx, resource); err != nil && !apierrors.IsNotFound(err) {
					errs = append(errs, err)
				}
			}
		})
	}

	// Reconcile platform specific resources
	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		log.Info("reconciling AWS specific resources")
		errs = append(errs, r.reconcileAWSIdentityWebhook(ctx)...)
	case hyperv1.AzurePlatform:
		log.Info("reconciling Azure specific resources")
		errs = append(errs, r.reconcileAzureCloudNodeManager(ctx, releaseImage.ComponentImages()["azure-cloud-node-manager"])...)
	}

	// Reconcile hostedCluster recovery if the hosted cluster was restored from backup
	if _, exists := hcp.Annotations[hyperv1.HostedClusterRestoredFromBackupAnnotation]; exists {
		condition := &metav1.Condition{
			Type:   string(hyperv1.HostedClusterRestoredFromBackup),
			Reason: hyperv1.RecoveryFinishedReason,
		}

		if err := r.reconcileRestoredCluster(ctx, hcp); err != nil {
			log.Info("hosted cluster recovery not finished yet")
			condition.Status = metav1.ConditionFalse
			condition.Message = fmt.Sprintf("Hosted cluster recovery not finished: %v", err)

			meta.SetStatusCondition(&hcp.Status.Conditions, *condition)
			if _, err := r.CreateOrUpdate(ctx, r.client, hcp, func() error {
				return nil
			}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update status on hcp for hosted cluster recovery: %w. Condition error message: %v", err, condition.Message)
			}

			return ctrl.Result{RequeueAfter: 120 * time.Second}, errors.NewAggregate(errs)
		}

		log.Info("hosted cluster recovery finished")
		condition.Status = metav1.ConditionTrue
		condition.Message = "Hosted cluster recovery finished"
		meta.SetStatusCondition(&hcp.Status.Conditions, *condition)
		if _, err := r.CreateOrUpdate(ctx, r.client, hcp, func() error {
			return nil
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status on hcp for hosted cluster recovery: %w. Condition error message: %v", err, condition.Message)
		}
	}

	return ctrl.Result{}, errors.NewAggregate(errs)
}

func (r *reconciler) reconcileCSIDriver(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage) error {
	switch hcp.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		// Most csi drivers should be laid down by the Cluster Storage Operator (CSO) instead of
		// the hcco operator. Only KubeVirt is unique at the moment.
		err := kubevirtcsi.ReconcileTenant(r.client, hcp, ctx, r.CreateOrUpdate, releaseImage.ComponentImages())
		if err != nil {
			return err
		}
	}

	return nil
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

func (r *reconciler) reconcileConfig(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
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

	image := globalconfig.ImageConfig()
	if _, err := r.CreateOrUpdate(ctx, r.client, image, func() error {
		globalconfig.ReconcileImageConfig(image, hcp)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile image config: %w", err))
	}

	ingress := globalconfig.IngressConfig()
	if _, err := r.CreateOrUpdate(ctx, r.client, ingress, func() error {
		globalconfig.ReconcileIngressConfig(ingress, hcp)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile ingress config: %w", err))
	}

	networkConfig := globalconfig.NetworkConfig()
	if _, err := r.CreateOrUpdate(ctx, r.client, networkConfig, func() error {
		if err := globalconfig.ReconcileNetworkConfig(networkConfig, hcp); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile network config: %w", err))
		}
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to create network config: %w", err))
	}

	// Copy proxy trustedCA to guest cluster.
	if err := r.reconcileProxyTrustedCAConfigMap(ctx, hcp); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile proxy TrustedCA configmap: %w", err))
	}

	proxy := globalconfig.ProxyConfig()
	if _, err := r.CreateOrUpdate(ctx, r.client, proxy, func() error {
		globalconfig.ReconcileInClusterProxyConfig(proxy, hcp.Spec.Configuration)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile proxy config: %w", err))
	}

	err := r.reconcileImageContentPolicyType(ctx, hcp)
	if err != nil {
		errs = append(errs, err)
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

	cloudCredentialConfig := manifests.CloudCredential()
	if _, err := r.CreateOrUpdate(ctx, r.client, cloudCredentialConfig, func() error {
		cco.ReconcileCloudCredentialConfig(cloudCredentialConfig)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile cloud credential config: %w", err))
	}

	authenticationConfig := globalconfig.AuthenticationConfiguration()
	if _, err := r.CreateOrUpdate(ctx, r.client, authenticationConfig, func() error {
		return globalconfig.ReconcileAuthenticationConfiguration(authenticationConfig, hcp.Spec.Configuration, hcp.Spec.IssuerURL)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile authentication config: %w", err))
	}

	apiServerConfig := globalconfig.APIServerConfiguration()
	if _, err := r.CreateOrUpdate(ctx, r.client, apiServerConfig, func() error {
		return globalconfig.ReconcileAPIServerConfiguration(apiServerConfig, hcp.Spec.Configuration)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile apiserver config: %w", err))
	}

	return errors.NewAggregate(errs)
}

func (r *reconciler) reconcileProxyTrustedCAConfigMap(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	log := ctrl.LoggerFrom(ctx)

	configMapRef := ""
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.Proxy != nil {
		configMapRef = hcp.Spec.Configuration.Proxy.TrustedCA.Name
	}

	proxy := globalconfig.ProxyConfig()
	if err := r.client.Get(ctx, client.ObjectKeyFromObject(proxy), proxy); err != nil {
		return err
	}

	currentConfigMapRef := proxy.Spec.TrustedCA.Name
	if currentConfigMapRef != "" && currentConfigMapRef != configMapRef {
		// cleanup old configMaps
		cm := &corev1.ConfigMap{}
		cm.Name = currentConfigMapRef

		// log and ignore deletion errors, should not disrupt normal workflow
		cm.Namespace = hcp.Namespace
		if err := r.cpClient.Delete(ctx, cm); err != nil {
			log.Error(err, "failed to delete configmap", "name", cm.Name, "namespace", cm.Namespace)
		}

		cm.Namespace = manifests.ProxyTrustedCAConfigMap("").Namespace
		if err := r.client.Delete(ctx, cm); err != nil {
			log.Error(err, "failed to delete configmap in hosted cluster", "name", cm.Name, "namespace", cm.Namespace)
		}
	}

	if configMapRef == "" {
		return nil
	}

	sourceCM := &corev1.ConfigMap{}
	if err := r.cpClient.Get(ctx, client.ObjectKey{Namespace: hcp.Namespace, Name: configMapRef}, sourceCM); err != nil {
		return fmt.Errorf("failed to get referenced TrustedCA configmap %s/%s: %w", hcp.Namespace, configMapRef, err)
	}

	destCM := manifests.ProxyTrustedCAConfigMap(sourceCM.Name)
	if _, err := r.CreateOrUpdate(ctx, r.client, destCM, func() error {
		destCM.Data = sourceCM.Data
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile referenced TrustedCA config map %s/%s: %w", destCM.Namespace, destCM.Name, err)
	}

	return nil
}

func (r *reconciler) reconcileNamespaces(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	namespaceManifests := []struct {
		manifest  func() *corev1.Namespace
		reconcile func(*corev1.Namespace) error
	}{
		{manifest: manifests.NamespaceOpenShiftAPIServer},
		{manifest: manifests.NamespaceOpenShiftInfra, reconcile: namespaces.ReconcileOpenShiftInfraNamespace},
		{manifest: manifests.NamespaceOpenshiftCloudControllerManager},
		{manifest: manifests.NamespaceOpenShiftControllerManager},
		{manifest: manifests.NamespaceKubeAPIServer, reconcile: namespaces.ReconcileKubeAPIServerNamespace},
		{manifest: manifests.NamespaceKubeControllerManager},
		{manifest: manifests.NamespaceKubeScheduler},
		{manifest: manifests.NamespaceEtcd},
		{manifest: manifests.NamespaceIngress, reconcile: namespaces.ReconcileOpenShiftIngressNamespace},
		{manifest: manifests.NamespaceAuthentication},
		{manifest: manifests.NamespaceRouteControllerManager},
	}

	var errs []error
	for _, m := range namespaceManifests {
		ns := m.manifest()
		if ns.Name == "openshift-ingress" && !capabilities.IsIngressCapabilityEnabled(hcp.Spec.Capabilities) {
			continue
		}
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

type manifestAndReconcile[o client.Object] struct {
	manifest  func() o
	reconcile func(o) error
}

func (m manifestAndReconcile[o]) upsert(ctx context.Context, client client.Client, createOrUpdate upsert.CreateOrUpdateFN) error {
	obj := m.manifest()
	if _, err := createOrUpdate(ctx, client, obj, func() error {
		return m.reconcile(obj)
	}); err != nil {
		return fmt.Errorf("failed to reconcile %T %s: %w", obj, obj.GetName(), err)
	}

	return nil
}

// getKey returns a unique identifier string for the manifest object,
// combining Kind, Name, and optionally Namespace (if the object is namespaced).
// This is useful for mapping capabilities to specific manifests while
// avoiding conflicts between objects with the same name in different scopes
// or of different kinds (e.g., Role vs RoleBinding).
//
// - For namespaced objects: "<namespace>/<name>/<kind>"
// - For cluster-scoped objects: "<name>/<kind>"
func (m manifestAndReconcile[o]) getKey() string {
	obj := m.manifest()
	gvk := obj.GetObjectKind().GroupVersionKind()
	ns := obj.GetNamespace()
	name := obj.GetName()
	if ns != "" {
		return fmt.Sprintf("%s/%s/%s", ns, name, gvk.Kind)
	}
	return fmt.Sprintf("%s/%s", name, gvk.Kind) // cluster-scoped
}

type manifestReconciler interface {
	upsert(ctx context.Context, client client.Client, createOrUpdate upsert.CreateOrUpdateFN) error
	getKey() string
}

func (r *reconciler) reconcileRBAC(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	rbacReconciler := []manifestReconciler{
		manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.CSRApproverClusterRole, reconcile: rbac.ReconcileCSRApproverClusterRole},
		manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.IngressToRouteControllerClusterRole, reconcile: rbac.ReconcileIngressToRouteControllerClusterRole},
		manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.NamespaceSecurityAllocationControllerClusterRole, reconcile: rbac.ReconcileNamespaceSecurityAllocationControllerClusterRole},

		manifestAndReconcile[*rbacv1.Role]{manifest: manifests.IngressToRouteControllerRole, reconcile: rbac.ReconcileReconcileIngressToRouteControllerRole},

		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.CSRApproverClusterRoleBinding, reconcile: rbac.ReconcileCSRApproverClusterRoleBinding},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.IngressToRouteControllerClusterRoleBinding, reconcile: rbac.ReconcileIngressToRouteControllerClusterRoleBinding},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.NamespaceSecurityAllocationControllerClusterRoleBinding, reconcile: rbac.ReconcileNamespaceSecurityAllocationControllerClusterRoleBinding},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.NodeBootstrapperClusterRoleBinding, reconcile: rbac.ReconcileNodeBootstrapperClusterRoleBinding},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.CSRRenewalClusterRoleBinding, reconcile: rbac.ReconcileCSRRenewalClusterRoleBinding},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.MetricsClientClusterRoleBinding, reconcile: rbac.ReconcileGenericMetricsClusterRoleBinding("system:serviceaccount:hypershift:prometheus")},

		manifestAndReconcile[*rbacv1.RoleBinding]{manifest: manifests.IngressToRouteControllerRoleBinding, reconcile: rbac.ReconcileIngressToRouteControllerRoleBinding},

		manifestAndReconcile[*rbacv1.RoleBinding]{manifest: manifests.AuthenticatedReaderForAuthenticatedUserRolebinding, reconcile: rbac.ReconcileAuthenticatedReaderForAuthenticatedUserRolebinding},

		manifestAndReconcile[*rbacv1.Role]{manifest: manifests.KCMLeaderElectionRole, reconcile: rbac.ReconcileKCMLeaderElectionRole},
		manifestAndReconcile[*rbacv1.RoleBinding]{manifest: manifests.KCMLeaderElectionRoleBinding, reconcile: rbac.ReconcileKCMLeaderElectionRoleBinding},

		manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.ImageTriggerControllerClusterRole, reconcile: rbac.ReconcileImageTriggerControllerClusterRole},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.ImageTriggerControllerClusterRoleBinding, reconcile: rbac.ReconcileImageTriggerControllerClusterRoleBinding},

		manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.PodSecurityAdmissionLabelSyncerControllerClusterRole, reconcile: rbac.ReconcilePodSecurityAdmissionLabelSyncerControllerClusterRole},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.PodSecurityAdmissionLabelSyncerControllerRoleBinding, reconcile: rbac.ReconcilePodSecurityAdmissionLabelSyncerControllerRoleBinding},

		manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.PriviligedNamespacesPSALabelSyncerClusterRole, reconcile: rbac.ReconcilePriviligedNamespacesPSALabelSyncerClusterRole},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.PriviligedNamespacesPSALabelSyncerClusterRoleBinding, reconcile: rbac.ReconcilePriviligedNamespacesPSALabelSyncerClusterRoleBinding},

		manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.DeployerClusterRole, reconcile: rbac.ReconcileDeployerClusterRole},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.DeployerClusterRoleBinding, reconcile: rbac.ReconcileDeployerClusterRoleBinding},

		// ClusterRole and ClusterRoleBinding for useroauthaccesstokens referenced from https://github.com/openshift/cluster-authentication-operator/tree/bebf0fd3932be12594227b415fecd5d664611bc0/bindata/oauth-apiserver/RBAC
		// Let this go by for now
		manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.UserOAuthClusterRole, reconcile: rbac.ReconcileUserOAuthClusterRole},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.UserOAuthClusterRoleBinding, reconcile: rbac.ReconcileUserOAuthClusterRoleBinding},
	}

	if azureutil.IsAroHCP() {
		rbacReconciler = append(rbacReconciler,
			manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.AzureDiskCSIDriverNodeServiceAccountRole, reconcile: rbac.ReconcileAzureDiskCSIDriverNodeServiceAccountClusterRole},
			manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.AzureDiskCSIDriverNodeServiceAccountRoleBinding, reconcile: rbac.ReconcileAzureDiskCSIDriverNodeServiceAccountClusterRoleBinding},

			manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.AzureFileCSIDriverNodeServiceAccountRole, reconcile: rbac.ReconcileAzureFileCSIDriverNodeServiceAccountClusterRole},
			manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.AzureFileCSIDriverNodeServiceAccountRoleBinding, reconcile: rbac.ReconcileAzureFileCSIDriverNodeServiceAccountClusterRoleBinding},

			manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.CloudNetworkConfigControllerServiceAccountRole, reconcile: rbac.ReconcileCloudNetworkConfigControllerServiceAccountClusterRole},
			manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.CloudNetworkConfigControllerServiceAccountRoleBinding, reconcile: rbac.ReconcileCloudNetworkConfigControllerServiceAccountClusterRoleBinding},
		)
	}

	var errs []error
	for _, m := range rbacReconciler {
		mKey := m.getKey()
		capability, found := manifests.RbacCapabilityMap[mKey]
		if found && capability == hyperv1.IngressCapability && !capabilities.IsIngressCapabilityEnabled(hcp.Spec.Capabilities) {
			continue
		}
		if err := m.upsert(ctx, r.client, r.CreateOrUpdate); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.NewAggregate(errs)
}

func (r *reconciler) reconcileIngressController(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	var errs []error
	p := ingress.NewIngressParams(hcp)
	ingressController := manifests.IngressDefaultIngressController()
	if _, err := r.CreateOrUpdate(ctx, r.client, ingressController, func() error {
		return ingress.ReconcileDefaultIngressController(ingressController, p.IngressSubdomain, p.PlatformType, p.Replicas, p.IBMCloudUPI, p.IsPrivate, p.AWSNLB, p.LoadBalancerScope, p.LoadBalancerIP)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile default ingress controller: %w", err))
	}

	sourceCert := cpomanifests.IngressCert(hcp.Namespace)
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

	// Default Ingress is passed through as a subdomain of the infra/mgmt cluster
	// for KubeVirt when the base domain passthrough feature is in use.
	if hcp.Spec.Platform.Type == hyperv1.KubevirtPlatform &&
		hcp.Spec.Platform.Kubevirt != nil &&
		hcp.Spec.Platform.Kubevirt.BaseDomainPassthrough != nil &&
		*hcp.Spec.Platform.Kubevirt.BaseDomainPassthrough {

		// Here we are creating a route and service in the hosted control plane namespace
		// while in the HCCO (which typically only works on the guest client, not the mgmt client).
		//
		// This is being done in the HCCO because we have to get the NodePort's port used by the
		// default ingress services in the guest cluster in order to create the service in the
		// mgmt/infra cluster. basically, the mgmt cluster service has to point the backend
		// "somewhere", and that somewhere is the nodeport's port of the routers in the guest cluster.
		// The component that can get that information about the nodeport's port is the HCCO, so
		// that's why we're reconciling a service and route within hosted control plane in the HCCO

		defaultIngressNodePortService := manifests.IngressDefaultIngressNodePortService()
		err := r.client.Get(ctx, client.ObjectKeyFromObject(defaultIngressNodePortService), defaultIngressNodePortService)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to retrieve guest cluster ingress NodePort: %w", err))
		}

		var namespace string
		if hcp.Spec.Platform.Kubevirt.Credentials != nil {
			namespace = hcp.Spec.Platform.Kubevirt.Credentials.InfraNamespace
		} else {
			namespace = hcp.Namespace
		}

		// Manifests for infra/mgmt cluster passthrough service
		cpService := manifests.IngressDefaultIngressPassthroughService(namespace)

		cpService.Name = fmt.Sprintf("%s-%s",
			manifests.IngressDefaultIngressPassthroughServiceName,
			hcp.Spec.Platform.Kubevirt.GenerateID)

		// Manifests for infra/mgmt cluster passthrough routes
		cpPassthroughRoute := manifests.IngressDefaultIngressPassthroughRoute(namespace)

		cpPassthroughRoute.Name = fmt.Sprintf("%s-%s",
			manifests.IngressDefaultIngressPassthroughRouteName,
			hcp.Spec.Platform.Kubevirt.GenerateID)

		if _, err := r.CreateOrUpdate(ctx, r.kubevirtInfraClient, cpService, func() error {
			return ingress.ReconcileDefaultIngressPassthroughService(cpService, defaultIngressNodePortService, hcp)
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile kubevirt ingress passthrough service: %w", err))
		}

		if _, err := r.CreateOrUpdate(ctx, r.kubevirtInfraClient, cpPassthroughRoute, func() error {
			return ingress.ReconcileDefaultIngressPassthroughRoute(cpPassthroughRoute, cpService, hcp)
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile kubevirt ingress passthrough route: %w", err))
		}
	}

	return errors.NewAggregate(errs)
}

func (r *reconciler) reconcileAuthOIDC(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	var errs []error
	if !util.HCPOAuthEnabled(hcp) &&
		len(hcp.Spec.Configuration.Authentication.OIDCProviders) != 0 {

		// Copy issuer CA configmap into openshift-config namespace
		provider := hcp.Spec.Configuration.Authentication.OIDCProviders[0]
		if provider.Issuer.CertificateAuthority.Name != "" {
			name := provider.Issuer.CertificateAuthority.Name
			var src corev1.ConfigMap
			err := r.cpClient.Get(ctx, client.ObjectKey{Namespace: hcp.Namespace, Name: name}, &src)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to get issuer CA configmap %s: %w", name, err))
			} else {
				dest := corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: ConfigNamespace,
					},
				}
				_, err = r.CreateOrUpdate(ctx, r.client, &dest, func() error {
					if dest.Data == nil {
						dest.Data = map[string]string{}
					}
					dest.Data["ca-bundle.crt"] = src.Data["ca-bundle.crt"]
					return nil
				})
				if err != nil {
					errs = append(errs, fmt.Errorf("failed to reconcile issuer CA configmap %s: %w", dest.Name, err))
				}
			}
		}

		// Copy OIDCClient Secrets into openshift-config namespace
		if len(hcp.Spec.Configuration.Authentication.OIDCProviders[0].OIDCClients) > 0 {
			for _, oidcClient := range hcp.Spec.Configuration.Authentication.OIDCProviders[0].OIDCClients {
				if oidcClient.ClientSecret.Name != "" {
					var src corev1.Secret
					err := r.cpClient.Get(ctx, client.ObjectKey{Namespace: hcp.Namespace, Name: oidcClient.ClientSecret.Name}, &src)
					if err != nil {
						errs = append(errs, fmt.Errorf("failed to get OIDCClient secret %s: %w", oidcClient.ClientSecret.Name, err))
						continue
					}
					if azureutil.IsAroHCP() && util.HasAnnotationWithValue(&src, hyperv1.HostedClusterSourcedAnnotation, "true") {
						// This is a day-2 secret. We shouldn't copy it, instead it'll be provided by the end-user on the hosted cluster.
						continue
					}
					dest := corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      oidcClient.ClientSecret.Name,
							Namespace: ConfigNamespace,
						},
					}
					_, err = r.CreateOrUpdate(ctx, r.client, &dest, func() error {
						if dest.Data == nil {
							dest.Data = map[string][]byte{}
						}
						dest.Data["clientSecret"] = src.Data["clientSecret"]
						return nil
					})
					if err != nil {
						errs = append(errs, fmt.Errorf("failed to reconcile OIDCClient secret %s: %w", dest.Name, err))

					}
				}
			}
		}
	}
	return errors.NewAggregate(errs)
}

func (r *reconciler) reconcileKonnectivityAgent(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage) error {
	var errs []error

	// Set pod security labels on kube-system to avoid API warnings
	kubeSystemNamespace := manifests.NamespaceKubeSystem()
	if _, err := r.CreateOrUpdate(ctx, r.client, kubeSystemNamespace, func() error {
		if kubeSystemNamespace.Labels == nil {
			kubeSystemNamespace.Labels = map[string]string{}
		}
		kubeSystemNamespace.Labels["security.openshift.io/scc.podSecurityLabelSync"] = "false"
		kubeSystemNamespace.Labels["pod-security.kubernetes.io/enforce"] = "privileged"
		kubeSystemNamespace.Labels["pod-security.kubernetes.io/audit"] = "privileged"
		kubeSystemNamespace.Labels["pod-security.kubernetes.io/warn"] = "privileged"
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile kube-system namespace: %w", err)
	}

	p := konnectivity.NewKonnectivityParams(hcp, releaseImage.ComponentImages(), r.konnectivityServerAddress, r.konnectivityServerPort)

	controlPlaneKonnectivityCA := manifests.KonnectivityControlPlaneCAConfigMap(hcp.Namespace)
	if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(controlPlaneKonnectivityCA), controlPlaneKonnectivityCA); err != nil {
		errs = append(errs, fmt.Errorf("failed to get control plane konnectivity agent CA config map: %w", err))
	} else {
		hostedKonnectivityCA := manifests.KonnectivityHostedCAConfigMap()
		if _, err := r.CreateOrUpdate(ctx, r.client, hostedKonnectivityCA, func() error {
			util.CopyConfigMap(hostedKonnectivityCA, controlPlaneKonnectivityCA)
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile konnectivity CA config map: %w", err))
		}
	}

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

	proxy := &configv1.Proxy{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
	if err := r.client.Get(ctx, client.ObjectKeyFromObject(proxy), proxy); err != nil {
		// If the cluster doesn't use a proxy this is irrelevant, so we don't return.
		errs = append(errs, fmt.Errorf("failed to get proxy config: %w", err))
	}

	agentDaemonset := manifests.KonnectivityAgentDaemonSet()
	if _, err := r.CreateOrUpdate(ctx, r.client, agentDaemonset, func() error {
		konnectivity.ReconcileAgentDaemonSet(agentDaemonset, p, hcp.Spec.Platform, proxy.Status)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile konnectivity agent daemonset: %w", err))
	}

	return errors.NewAggregate(errs)
}

func (r *reconciler) reconcileClusterVersion(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	clusterVersion := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}}
	if _, err := r.CreateOrUpdate(ctx, r.client, clusterVersion, func() error {
		clusterVersion.Spec.ClusterID = configv1.ClusterID(hcp.Spec.ClusterID)
		clusterVersion.Spec.Capabilities = &configv1.ClusterVersionCapabilitiesSpec{
			BaselineCapabilitySet:         configv1.ClusterVersionCapabilitySetNone,
			AdditionalEnabledCapabilities: capabilities.CalculateEnabledCapabilities(hcp.Spec.Capabilities),
		}
		clusterVersion.Spec.Upstream = hcp.Spec.UpdateService
		clusterVersion.Spec.Channel = hcp.Spec.Channel
		clusterVersion.Spec.DesiredUpdate = nil
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile clusterVersion: %w", err)
	}

	return nil
}

func (r *reconciler) reconcileOpenshiftAPIServerAPIServices(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	rootCA := cpomanifests.RootCASecret(hcp.Namespace)
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
	rootCA := cpomanifests.RootCASecret(hcp.Namespace)
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

func (r *reconciler) reconcileKASEndpoints(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	var errs []error

	kasAdvertiseAddress := util.GetAdvertiseAddress(hcp, config.DefaultAdvertiseIPv4Address, config.DefaultAdvertiseIPv6Address)
	kasEndpointsPort := util.KASPodPort(hcp)

	// We only keep reconciling the endpoint for existing clusters that are relying on this for nodes haproxy to work.
	// Otherwise, changing the haproxy config to !=443 would result in a NodePool rollout which want to avoid for existing clusters.
	// Existing clusters are given the *hcp.Spec.Networking.APIServer.Port == 443 semantic as we were enforcing this default previously,
	// and it's a now a forbidden operation.
	if hcp.Spec.Networking.APIServer != nil && hcp.Spec.Networking.APIServer.Port != nil &&
		*hcp.Spec.Networking.APIServer.Port == 443 {
		kasEndpointsPort = 443
	}

	kasEndpoints := manifests.KASEndpoints()
	if _, err := r.CreateOrUpdate(ctx, r.client, kasEndpoints, func() error {
		kas.ReconcileKASEndpoints(kasEndpoints, kasAdvertiseAddress, kasEndpointsPort)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile kubernetes.default endpoints: %w", err))
	}
	kasEndpointSlice := manifests.KASEndpointSlice()
	if _, err := r.CreateOrUpdate(ctx, r.client, kasEndpointSlice, func() error {
		kas.ReconcileKASEndpointSlice(kasEndpointSlice, kasAdvertiseAddress, kasEndpointsPort)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile kubernetes.default endpoint slice: %w", err))
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
			// kubeAdminPasswordHash should not exist when a user specifies an explicit oauth config
			// delete kubeAdminPasswordHash if it exist
			return r.deleteKubeadminPasswordHashSecret(ctx)
		}
		return fmt.Errorf("failed to get kubeadmin password secret: %w", err)
	}

	kubeadminPasswordHashSecret := manifests.KubeadminPasswordHashSecret()
	if _, err := r.CreateOrUpdate(ctx, r.client, kubeadminPasswordHashSecret, func() error {
		return kubeadminpassword.ReconcileKubeadminPasswordHashSecret(kubeadminPasswordHashSecret, kubeadminPasswordSecret)
	}); err != nil {
		return err
	}

	if _, err := r.CreateOrUpdate(ctx, r.cpClient, kubeadminPasswordSecret, func() error {
		if kubeadminPasswordSecret.Annotations == nil {
			kubeadminPasswordSecret.Annotations = map[string]string{}
		}
		kubeadminPasswordSecret.Annotations[cpoauth.KubeadminSecretHashAnnotation] = string(kubeadminPasswordHashSecret.Data["kubeadmin"])
		return nil
	}); err != nil {
		return fmt.Errorf("failed to annotate kubeadmin-password secret in hcp namespace: %v", err)
	}

	return nil
}

func (r *reconciler) deleteKubeadminPasswordHashSecret(ctx context.Context) error {
	kubeadminPasswordHashSecret := manifests.KubeadminPasswordHashSecret()
	if err := r.client.Get(ctx, client.ObjectKeyFromObject(kubeadminPasswordHashSecret), kubeadminPasswordHashSecret); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
	} else {
		if err := r.client.Delete(ctx, kubeadminPasswordHashSecret); err != nil {
			return err
		}
	}

	return nil
}

func (r *reconciler) reconcileOAuthServingCertCABundle(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	sourceBundle := cpomanifests.OpenShiftOAuthMasterCABundle(hcp.Namespace)
	if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(sourceBundle), sourceBundle); err != nil {
		return fmt.Errorf("cannot get oauth master ca bundle: %w", err)
	}
	caBundle := manifests.OAuthCABundle()
	if _, err := r.CreateOrUpdate(ctx, r.client, caBundle, func() error {
		return oauth.ReconcileOAuthServerCertCABundle(caBundle, sourceBundle)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth server cert ca bundle: %w", err)
	}
	return nil
}

func (r *reconciler) reconcileUserCertCABundle(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	if hcp.Spec.AdditionalTrustBundle != nil {
		cpUserCAConfigMap := cpomanifests.UserCAConfigMap(hcp.Namespace)
		if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(cpUserCAConfigMap), cpUserCAConfigMap); err != nil {
			return fmt.Errorf("cannot get AdditionalTrustBundle ConfigMap: %w", err)
		}
		userCAConfigMap := manifests.UserCABundle()
		if _, err := r.CreateOrUpdate(ctx, r.client, userCAConfigMap, func() error {
			userCAConfigMap.Data = cpUserCAConfigMap.Data
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile the %s ConfigMap: %w", client.ObjectKeyFromObject(userCAConfigMap), err)
		}
	}
	return nil
}

func (r *reconciler) reconcileProxyCABundle(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	proxyCADestination := manifests.OpenShiftUserCABundle()
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.Proxy != nil && hcp.Spec.Configuration.Proxy.TrustedCA.Name != "" {
		cpProxyCA := &corev1.ConfigMap{}
		cpProxyCA.Namespace = hcp.Namespace
		cpProxyCA.Name = hcp.Spec.Configuration.Proxy.TrustedCA.Name
		if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(cpProxyCA), cpProxyCA); err != nil {
			return fmt.Errorf("cannot get proxy CA bundle ConfigMap: %w", err)
		}
		if _, err := r.CreateOrUpdate(ctx, r.client, proxyCADestination, func() error {
			proxyCADestination.Data = cpProxyCA.Data
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile the proxy CA bundle ConfigMap: %w", err)
		}
	} else {
		if _, err := util.DeleteIfNeeded(ctx, r.client, proxyCADestination); err != nil {
			return err
		}
	}
	return nil
}

func buildAWSWebIdentityCredentials(roleArn, region string) (string, error) {
	if roleArn == "" {
		return "", fmt.Errorf("role arn cannot be empty in AssumeRole credentials")
	}
	if region == "" {
		return "", fmt.Errorf("a region must be specified for cross-partition compatibility in AssumeRole credentials")
	}
	return fmt.Sprintf(awsCredentialsTemplate, roleArn, region), nil
}

func (r *reconciler) reconcileCloudCredentialSecrets(ctx context.Context, hcp *hyperv1.HostedControlPlane, log logr.Logger) []error {
	var errs []error
	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		var region string
		if hcp.Spec.Platform.AWS != nil {
			region = hcp.Spec.Platform.AWS.Region
		}
		syncSecret := func(secret *corev1.Secret, arn string) error {
			ns := &corev1.Namespace{}
			err := r.client.Get(ctx, client.ObjectKey{Name: secret.Namespace}, ns)
			if err != nil {
				if apierrors.IsNotFound(err) {
					log.Info("WARNING: cannot sync cloud credential secret because namespace does not exist", "secret", client.ObjectKeyFromObject(secret))
					return nil
				}
				return fmt.Errorf("failed to get secret namespace %s: %w", secret.Namespace, err)
			}
			credentials, err := buildAWSWebIdentityCredentials(arn, region)
			if err != nil {
				return fmt.Errorf("failed to build cloud credentials secret %s/%s: %w", secret.Namespace, secret.Name, err)
			}
			if _, err := r.CreateOrUpdate(ctx, r.client, secret, func() error {
				secret.Data = map[string][]byte{"credentials": []byte(credentials)}
				secret.Type = corev1.SecretTypeOpaque
				return nil
			}); err != nil {
				return fmt.Errorf("failed to reconcile aws cloud credential secret %s/%s: %w", secret.Namespace, secret.Name, err)
			}
			return nil
		}
		roleMap := map[string]*corev1.Secret{}

		roleMap[hcp.Spec.Platform.AWS.RolesRef.StorageARN] = manifests.AWSStorageCloudCredsSecret()

		if capabilities.IsIngressCapabilityEnabled(hcp.Spec.Capabilities) {
			roleMap[hcp.Spec.Platform.AWS.RolesRef.IngressARN] = manifests.AWSIngressCloudCredsSecret()
		}

		for arn, secret := range roleMap {
			if err := syncSecret(secret, arn); err != nil {
				errs = append(errs, err)
			}
		}

		if capabilities.IsImageRegistryCapabilityEnabled(hcp.Spec.Capabilities) {
			err := syncSecret(
				manifests.AWSImageRegistryCloudCredsSecret(),
				hcp.Spec.Platform.AWS.RolesRef.ImageRegistryARN,
			)
			if err != nil {
				errs = append(errs, err)
			}
		}
	case hyperv1.AzurePlatform:
		secretData := map[string][]byte{
			"azure_federated_token_file": []byte("/var/run/secrets/openshift/serviceaccount/token"),
			"azure_region":               []byte(hcp.Spec.Platform.Azure.Location),
			"azure_resource_prefix":      []byte(hcp.Name + "-" + hcp.Spec.InfraID),
			"azure_resourcegroup":        []byte(hcp.Spec.Platform.Azure.ResourceGroupName),
			"azure_subscription_id":      []byte(hcp.Spec.Platform.Azure.SubscriptionID),
			"azure_tenant_id":            []byte(hcp.Spec.Platform.Azure.TenantID),
		}

		// The ingress controller fails if this secret is not provided. The controller runs on the control plane side. In managed azure, we are
		// overriding the Azure credentials authentication method to always use client certificate authentication. This secret is just created
		// so that the ingress controller does not fail. The data in the secret is never used by the ingress controller due to the aforementioned
		// override to use client certificate authentication.
		//
		// Skip this step if the user explicitly disabled ingress.
		if capabilities.IsIngressCapabilityEnabled(hcp.Spec.Capabilities) {
			ingressCredentialSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-ingress-operator", Name: "cloud-credentials"}}
			if _, err := r.CreateOrUpdate(ctx, r.client, ingressCredentialSecret, func() error {
				secretData["azure_client_id"] = []byte("fakeClientID")
				ingressCredentialSecret.Data = secretData
				return nil
			}); err != nil {
				errs = append(errs, fmt.Errorf("failed to reconcile guest cluster ingress operator secret: %w", err))
			}
		}

		azureDiskCSISecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-cluster-csi-drivers", Name: "azure-disk-credentials"}}
		if _, err := r.CreateOrUpdate(ctx, r.client, azureDiskCSISecret, func() error {
			secretData["azure_client_id"] = []byte(hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.DataPlane.DiskMSIClientID)
			azureDiskCSISecret.Data = secretData
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile guest cluster CSI secret: %w", err))
		}

		if capabilities.IsImageRegistryCapabilityEnabled(hcp.Spec.Capabilities) {
			imageRegistrySecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-image-registry", Name: "installer-cloud-credentials"}}
			if _, err := r.CreateOrUpdate(ctx, r.client, imageRegistrySecret, func() error {
				secretData["azure_client_id"] = []byte(hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.DataPlane.ImageRegistryMSIClientID)
				imageRegistrySecret.Data = secretData
				return nil
			}); err != nil {
				errs = append(errs, fmt.Errorf("failed to reconcile guest cluster image-registry secret: %w", err))
			}
		}

		azureFileCSISecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-cluster-csi-drivers", Name: "azure-file-credentials"}}
		if _, err := r.CreateOrUpdate(ctx, r.client, azureFileCSISecret, func() error {
			secretData["azure_client_id"] = []byte(hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.DataPlane.FileMSIClientID)
			azureFileCSISecret.Data = secretData
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile csi driver secret: %w", err))
		}
	case hyperv1.OpenStackPlatform:
		credentialsSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: hcp.Spec.Platform.OpenStack.IdentityRef.Name}}
		if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(credentialsSecret), credentialsSecret); err != nil {
			return []error{fmt.Errorf("failed to get cloud credentials secret in hcp namespace: %w", err)}
		}
		caCertData := openstack.GetCACertFromCredentialsSecret(credentialsSecret)
		errs = append(errs,
			r.reconcileOpenStackCredentialsSecret(ctx, hcp.Spec.Platform.OpenStack, "openshift-cluster-csi-drivers", "openstack-cloud-credentials", credentialsSecret, caCertData, hcp.Spec.Networking.MachineNetwork),
			r.reconcileOpenStackCredentialsSecret(ctx, hcp.Spec.Platform.OpenStack, "openshift-cluster-csi-drivers", "manila-cloud-credentials", credentialsSecret, caCertData, hcp.Spec.Networking.MachineNetwork),
			r.reconcileOpenStackCredentialsSecret(ctx, hcp.Spec.Platform.OpenStack, "openshift-image-registry", "installer-cloud-credentials", credentialsSecret, caCertData, hcp.Spec.Networking.MachineNetwork),
			r.reconcileOpenStackCredentialsSecret(ctx, hcp.Spec.Platform.OpenStack, "openshift-cloud-network-config-controller", "cloud-credentials", credentialsSecret, caCertData, hcp.Spec.Networking.MachineNetwork),
		)
	case hyperv1.PowerVSPlatform:
		createPowerVSSecret := func(srcSecret, destSecret *corev1.Secret) error {
			_, err := r.CreateOrUpdate(ctx, r.client, destSecret, func() error {
				credData, credHasData := srcSecret.Data["ibmcloud_api_key"]
				if !credHasData {
					return fmt.Errorf("secret %q is missing credentials key", destSecret.Name)
				}
				destSecret.Type = corev1.SecretTypeOpaque
				if destSecret.Data == nil {
					destSecret.Data = map[string][]byte{}
				}
				destSecret.Data["ibmcloud_api_key"] = credData
				return nil
			})
			return err
		}

		// fetch the user-supplied ingress cloud credentials Secret,
		// transform and apply it as the "cloud-credentials" Secret in the openshift-ingress-operator namespace,
		// which is required by the ingress operator in the guest cluster.
		// Skip this step if the user explicitly disabled ingress.
		if capabilities.IsIngressCapabilityEnabled(hcp.Spec.Capabilities) {
			var ingressCredentials corev1.Secret
			err := r.cpClient.Get(ctx, client.ObjectKey{Namespace: hcp.Namespace, Name: hcp.Spec.Platform.PowerVS.IngressOperatorCloudCreds.Name}, &ingressCredentials)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to get ingress operator cloud credentials secret %s from hcp namespace : %w", hcp.Spec.Platform.PowerVS.IngressOperatorCloudCreds.Name, err))
				return errs
			}

			cloudCredentials := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "openshift-ingress-operator",
					Name:      "cloud-credentials",
				},
			}
			err = createPowerVSSecret(&ingressCredentials, cloudCredentials)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to reconcile powervs ingress cloud credentials secret %w", err))
			}
		}
		var storageCredentials corev1.Secret
		err := r.cpClient.Get(ctx, client.ObjectKey{Namespace: hcp.Namespace, Name: hcp.Spec.Platform.PowerVS.StorageOperatorCloudCreds.Name}, &storageCredentials)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to get storage operator cloud credentials secret %s from hcp namespace : %w", hcp.Spec.Platform.PowerVS.StorageOperatorCloudCreds.Name, err))
			return errs
		}

		ibmPowerVSCloudCredentials := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "openshift-cluster-csi-drivers",
				Name:      "ibm-powervs-cloud-credentials",
			},
		}
		err = createPowerVSSecret(&storageCredentials, ibmPowerVSCloudCredentials)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile powervs storage cloud credentials secret %w", err))
		}

		if capabilities.IsImageRegistryCapabilityEnabled(hcp.Spec.Capabilities) {
			var imageRegistryCredentials corev1.Secret
			err = r.cpClient.Get(ctx, client.ObjectKey{Namespace: hcp.Namespace, Name: hcp.Spec.Platform.PowerVS.ImageRegistryOperatorCloudCreds.Name}, &imageRegistryCredentials)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to get image registry operator cloud credentials secret %s from hcp namespace : %w", hcp.Spec.Platform.PowerVS.ImageRegistryOperatorCloudCreds.Name, err))
				return errs
			}

			imageRegistryInstallerCloudCredentials := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "openshift-image-registry",
					Name:      "installer-cloud-credentials",
				},
			}
			err = createPowerVSSecret(&imageRegistryCredentials, imageRegistryInstallerCloudCredentials)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to reconcile powervs image registry cloud credentials secret %w", err))
			}
		}
	}
	return errs
}

// reconcileOpenStackCredentialsSecret is a wrapper used to reconcile the OpenStack credentials secrets.
func (r *reconciler) reconcileOpenStackCredentialsSecret(ctx context.Context, platformSpec *hyperv1.OpenStackPlatformSpec, namespace, name string, credentialsSecret *corev1.Secret, caCertData []byte, machineNetwork []hyperv1.MachineNetworkEntry) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: credentialsSecret.Data,
	}
	if _, err := r.CreateOrUpdate(ctx, r.client, secret, func() error {
		return openstack.ReconcileCloudConfigSecret(platformSpec, secret, credentialsSecret, caCertData, machineNetwork)
	}); err != nil {
		return fmt.Errorf("failed to reconcile secret %s/%s: %w", secret.Namespace, secret.Name, err)
	}
	return nil
}

// reconcileOperatorHub gets the OperatorHubConfig from the HCP, for now the controller only reconcile over the DisableAllDefaultSources field and only once.
// After that the HCCO checks the OperatorHub object in the HC to manage the OLM resources.
// TODO (jparrill): Include in the reconciliation the OperatorHub.Sources to disable only the selected sources.
func (r *reconciler) reconcileOperatorHub(ctx context.Context, operatorHub *configv1.OperatorHub, hcp *hyperv1.HostedControlPlane) []error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling HCP OperatorHub config")
	if operatorHub.ResourceVersion == "" {
		if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.OperatorHub != nil {
			operatorHub.Spec.DisableAllDefaultSources = hcp.Spec.Configuration.OperatorHub.DisableAllDefaultSources
		}
	}

	return nil
}

func (r *reconciler) reconcileOLM(ctx context.Context, hcp *hyperv1.HostedControlPlane, pullSecret *corev1.Secret) []error {
	var errs []error

	operatorHub := manifests.OperatorHub()

	if hcp.Spec.OLMCatalogPlacement == hyperv1.ManagementOLMCatalogPlacement {
		// Management OLM Placement
		if _, err := r.CreateOrUpdate(ctx, r.client, operatorHub, func() error {
			if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.OperatorHub != nil {
				// if spec.Configuration.OperatorHub is set, we need to sync it to the guest cluster
				operatorHub.Spec.DisableAllDefaultSources = hcp.Spec.Configuration.OperatorHub.DisableAllDefaultSources
			} else {
				// If the spec.Configuration is nil or the spec.Configuration.OperatorHub is nil, then we need to set the OperatorHub.Spec to an empty struct
				operatorHub.Spec = configv1.OperatorHubSpec{}
			}
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile OperatorHub configuration: %w", err))
		}
	} else {
		// Guest OLM Placement
		if _, err := r.CreateOrUpdate(ctx, r.client, operatorHub, func() error {
			r.reconcileOperatorHub(ctx, operatorHub, hcp)
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile OperatorHub configuration: %w", err))
		}
	}

	p, err := olm.NewOperatorLifecycleManagerParams(ctx, hcp, pullSecret, r.ImageMetaDataProvider)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to create OperatorLifecycleManagerParams: %w", err))
	}

	// Check if the defaultSources are disabled
	if err := r.client.Get(ctx, client.ObjectKeyFromObject(operatorHub), operatorHub); err != nil {
		if !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("failed to get OperatorHub %s: %w", client.ObjectKeyFromObject(operatorHub).String(), err))
		}
	}

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
		if operatorHub.Spec.DisableAllDefaultSources {
			if _, err := util.DeleteIfNeeded(ctx, r.client, cs); err != nil {
				if !apierrors.IsNotFound(err) {
					errs = append(errs, fmt.Errorf("failed to delete catalogSource %s/%s: %w", cs.Namespace, cs.Name, err))
				}
			}
		} else {
			if _, err := r.CreateOrUpdate(ctx, r.client, cs, func() error {
				if p != nil {
					catalog.reconcile(cs, p)
					return nil
				} else {
					return fmt.Errorf("failed to get OperatorLifecycleManagerParams")
				}
			}); err != nil {
				errs = append(errs, fmt.Errorf("failed to reconcile catalog source %s/%s: %w", cs.Namespace, cs.Name, err))
			}
		}
	}

	rootCA := cpomanifests.RootCASecret(hcp.Namespace)
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

	switch hcp.Spec.Platform.Type {
	case hyperv1.AzurePlatform:
		// This is needed for the e2e tests and only for Azure: https://github.com/openshift/origin/blob/625733dd1ce7ebf40c3dd0abd693f7bb54f2d580/test/extended/util/cluster/cluster.go#L186
		reference := cpomanifests.AzureProviderConfig(hcp.Namespace)
		if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(reference), reference); err != nil {
			return fmt.Errorf("failed to fetch %s/%s configmap from management cluster: %w", reference.Namespace, reference.Name, err)
		}

		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: ConfigNamespace, Name: CloudProviderCMName}}
		if _, err := r.CreateOrUpdate(ctx, r.client, cm, func() error {
			if cm.Data == nil {
				cm.Data = map[string]string{}
			}
			cm.Data["config"] = reference.Data[azure.CloudConfigKey]
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile the %s/%s configmap: %w", cm.Namespace, cm.Name, err)
		}
	case hyperv1.OpenStackPlatform:
		reference := cpomanifests.OpenStackProviderConfig(hcp.Namespace)
		if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(reference), reference); err != nil {
			return fmt.Errorf("failed to fetch %s/%s configmap from management cluster: %w", reference.Namespace, reference.Name, err)
		}

		cmCPC := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: ConfigNamespace, Name: CloudProviderCMName}}
		if _, err := r.CreateOrUpdate(ctx, r.client, cmCPC, func() error {
			if cmCPC.Data == nil {
				cmCPC.Data = map[string]string{}
			}
			cmCPC.Data[openstack.CloudConfigKey] = reference.Data[openstack.CloudConfigKey]
			if reference.Data[openstack.CABundleKey] != "" {
				cmCPC.Data[openstack.CABundleKey] = reference.Data[openstack.CABundleKey]
			}
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile the %s/%s configmap: %w", cmCPC.Namespace, cmCPC.Name, err)
		}

		// This ConfigMap is normally created by the cloud controller manager operator
		// in the config-sync-controllers but this isn't yet deployed in the control-plane.
		// We need to handle this now so the ConfigMap can be used by Cluster Network Operator
		// to create the kube-cloud-config ConfigMap for cloud-network-config-controller.
		// This is particular to OpenStack because in the case of a cloud using a self-signed
		// CA, the CA bundle is needed to be passed to the cloud-network-config-controller.
		cmKCC := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: ConfigManagedNamespace, Name: "kube-cloud-config"}}
		if _, err := r.CreateOrUpdate(ctx, r.client, cmKCC, func() error {
			if cmKCC.Data == nil {
				cmKCC.Data = map[string]string{}
			}
			cmKCC.Data[openstack.CloudConfigKey] = reference.Data[openstack.CloudConfigKey]
			if reference.Data[openstack.CABundleKey] != "" {
				cmKCC.Data[openstack.CABundleKey] = reference.Data[openstack.CABundleKey]
			}
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile the %s/%s configmap: %w", cmKCC.Namespace, cmKCC.Name, err)
		}
	default:
		return nil
	}

	return nil
}

func (r *reconciler) reconcileGuestClusterAlertRules(ctx context.Context) error {
	var errs []error
	apiUsageRule := manifests.ApiUsageRule()
	if _, err := r.CreateOrUpdate(ctx, r.client, apiUsageRule, func() error {
		return alerts.ReconcileApiUsageRule(apiUsageRule)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile guest cluster api usage rule: %w", err))
	}
	podSecurityViolationRule := manifests.PodSecurityViolationRule()
	if _, err := r.CreateOrUpdate(ctx, r.client, podSecurityViolationRule, func() error {
		return alerts.ReconcilePodSecurityViolationRule(podSecurityViolationRule)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile guest cluster pod security violation rule: %w", err))
	}

	return errors.NewAggregate(errs)
}

func (r *reconciler) reconcileAWSIdentityWebhook(ctx context.Context) []error {
	var errs []error
	clusterRole := manifests.AWSPodIdentityWebhookClusterRole()
	if _, err := r.CreateOrUpdate(ctx, r.client, clusterRole, func() error {
		clusterRole.Rules = []rbacv1.PolicyRule{{
			APIGroups: []string{""},
			Resources: []string{"serviceaccounts"},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		}}
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile %T %s: %w", clusterRole, clusterRole.Name, err))
	}

	clusterRoleBinding := manifests.AWSPodIdentityWebhookClusterRoleBinding()
	if _, err := r.CreateOrUpdate(ctx, r.client, clusterRoleBinding, func() error {
		clusterRoleBinding.RoleRef.APIGroup = "rbac.authorization.k8s.io"
		clusterRoleBinding.RoleRef.Kind = "ClusterRole"
		clusterRoleBinding.RoleRef.Name = clusterRole.Name
		clusterRoleBinding.Subjects = []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      "aws-pod-identity-webhook",
			Namespace: "openshift-authentication",
		}}
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile %T %s: %w", clusterRoleBinding, clusterRoleBinding.Name, err))
	}

	ignoreFailurePolicy := admissionregistrationv1.Ignore
	sideEffectsNone := admissionregistrationv1.SideEffectClassNone
	webhook := manifests.AWSPodIdentityWebhook()
	if _, err := r.CreateOrUpdate(ctx, r.client, webhook, func() error {
		webhook.Webhooks = []admissionregistrationv1.MutatingWebhook{{
			AdmissionReviewVersions: []string{"v1beta1"},
			Name:                    "pod-identity-webhook.amazonaws.com",
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				CABundle: []byte(r.rootCA),
				URL:      ptr.To("https://127.0.0.1:4443/mutate"),
			},
			FailurePolicy: &ignoreFailurePolicy,
			Rules: []admissionregistrationv1.RuleWithOperations{{
				Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
				Rule: admissionregistrationv1.Rule{
					APIGroups:   []string{""},
					APIVersions: []string{"v1"},
					Resources:   []string{"pods"},
				},
			}},
			SideEffects: &sideEffectsNone,
		}}
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile %T %s: %w", webhook, webhook.Name, err))
	}

	return errs
}

func (r *reconciler) destroyCloudResources(ctx context.Context, hcp *hyperv1.HostedControlPlane) (ctrl.Result, error) {
	remaining, err := r.ensureCloudResourcesDestroyed(ctx, hcp)

	var status metav1.ConditionStatus
	var reason, message string

	if err != nil {
		reason = "ErrorOccurred"
		status = metav1.ConditionFalse
		message = fmt.Sprintf("Error: %v", err)
	} else {
		if remaining.Len() == 0 {
			reason = "CloudResourcesDestroyed"
			status = metav1.ConditionTrue
			message = "All guest resources destroyed"
		} else {
			reason = "RemainingCloudResources"
			status = metav1.ConditionFalse
			message = fmt.Sprintf("Remaining resources: %s", strings.Join(remaining.UnsortedList(), ","))
		}
	}
	resourcesDestroyedCond := &metav1.Condition{
		Type:    string(hyperv1.CloudResourcesDestroyed),
		Status:  status,
		Reason:  reason,
		Message: message,
	}

	originalHCP := hcp.DeepCopy()
	meta.SetStatusCondition(&hcp.Status.Conditions, *resourcesDestroyedCond)

	if !equality.Semantic.DeepEqual(hcp, originalHCP) {
		if err := r.cpClient.Status().Update(ctx, hcp); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set resources destroyed condition: %w", err)
		}
	}

	if remaining.Len() > 0 {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	} else {
		return ctrl.Result{}, nil
	}
}

func (r *reconciler) ensureCloudResourcesDestroyed(ctx context.Context, hcp *hyperv1.HostedControlPlane) (sets.Set[string], error) {
	log := ctrl.LoggerFrom(ctx)
	remaining := sets.New[string]()
	log.Info("Ensuring resource creation is blocked in cluster")
	if err := r.ensureResourceCreationIsBlocked(ctx, hcp); err != nil {
		return remaining, err
	}
	var errs []error
	if capabilities.IsImageRegistryCapabilityEnabled(hcp.Spec.Capabilities) {
		log.Info("Ensuring image registry storage is removed")
		removed, err := r.ensureImageRegistryStorageRemoved(ctx)
		if err != nil {
			errs = append(errs, err)
		}
		if !removed {
			remaining.Insert("image-registry")
		} else {
			log.Info("Image registry is removed")
		}
	}
	if capabilities.IsIngressCapabilityEnabled(hcp.Spec.Capabilities) {
		log.Info("Ensuring ingress controllers are removed")
		removed, err := r.ensureIngressControllersRemoved(ctx, hcp)
		if err != nil {
			errs = append(errs, err)
		}
		if !removed {
			remaining.Insert("ingress-controllers")
		} else {
			log.Info("Ingress controllers are removed")
		}
	}
	log.Info("Ensuring load balancers are removed")
	removed, err := r.ensureServiceLoadBalancersRemoved(ctx)
	if err != nil {
		errs = append(errs, err)
	}
	if !removed {
		remaining.Insert("loadbalancers")
	} else {
		log.Info("Load balancers are removed")
	}

	log.Info("Ensuring persistent volumes are removed")
	removed, err = r.ensurePersistentVolumesRemoved(ctx)
	if err != nil {
		errs = append(errs, err)
	}
	if !removed {
		remaining.Insert("persistent-volumes")
	} else {
		log.Info("Persistent volumes are removed")
	}

	log.Info("Ensuring volume snapshots are removed")
	removed, err = r.ensureVolumeSnapshotsRemoved(ctx)
	if err != nil {
		errs = append(errs, err)
	}
	if !removed {
		remaining.Insert("volume-snapshots")
	} else {
		log.Info("Volume snapshots are removed")
	}

	return remaining, errors.NewAggregate(errs)
}

func (r *reconciler) reconcileRestoredCluster(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	var errs []error

	log := ctrl.LoggerFrom(ctx)
	log.Info("Ensuring monitoring stack is properly working after hosted cluster restoration")
	if err := dr.RecoverMonitoringStack(ctx, hcp, r.uncachedClient); err != nil {
		errs = append(errs, err)
	}

	return errors.NewAggregate(errs)
}

func (r *reconciler) ensureGuestAdmissionWebhooksAreValid(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)

	cpServices := &corev1.ServiceList{}
	if err := r.cpClient.List(ctx, cpServices, client.InNamespace(r.hcpNamespace)); err != nil {
		return fmt.Errorf("failed to list control plane services: %w", err)
	}

	// disallow all urls targeting services in the hcp namespace by default unless 'hypershift.openshift.io/allow-guest-webhooks' label is present.
	disallowedUrls := make([]string, 0)
	for _, svc := range cpServices.Items {
		if _, exist := svc.Labels[hyperv1.AllowGuestWebhooksServiceLabel]; exist {
			continue
		}

		disallowedUrls = append(disallowedUrls, fmt.Sprintf("https://%s", svc.Name))
		disallowedUrls = append(disallowedUrls, fmt.Sprintf("https://%s.%s.svc", svc.Name, svc.Namespace))
		disallowedUrls = append(disallowedUrls, fmt.Sprintf("https://%s.%s.svc.cluster.local", svc.Name, svc.Namespace))
	}

	validatingWebhookConfigurations := &admissionregistrationv1.ValidatingWebhookConfigurationList{}
	if err := r.client.List(ctx, validatingWebhookConfigurations); err != nil {
		return fmt.Errorf("failed to list validatingWebhookConfigurations: %w", err)
	}

	errs := make([]error, 0)
	for _, configuration := range validatingWebhookConfigurations.Items {
		for _, webhook := range configuration.Webhooks {
			if webhook.ClientConfig.URL != nil && !isAllowedWebhookUrl(disallowedUrls, *webhook.ClientConfig.URL) {
				log.Info("deleting validating webhook configuration with a disallowed url", "webhook_name", configuration.Name, "disallowed_url", *webhook.ClientConfig.URL)
				errs = append(errs, r.client.Delete(ctx, &configuration))
				break
			}
		}
	}

	mutatingWebhookConfigurations := &admissionregistrationv1.MutatingWebhookConfigurationList{}
	if err := r.client.List(ctx, mutatingWebhookConfigurations); err != nil {
		errs = append(errs, fmt.Errorf("failed to list mutatingWebhookConfigurations: %w", err))
		return errors.NewAggregate(errs)
	}

	for _, configuration := range mutatingWebhookConfigurations.Items {
		for _, webhook := range configuration.Webhooks {
			if webhook.ClientConfig.URL != nil && !isAllowedWebhookUrl(disallowedUrls, *webhook.ClientConfig.URL) {
				log.Info("deleting mutating webhook configuration with a disallowed url", "webhook_name", configuration.Name, "disallowed_url", *webhook.ClientConfig.URL)
				errs = append(errs, r.client.Delete(ctx, &configuration))
				break
			}
		}
	}

	return errors.NewAggregate(errs)
}

// reconcileKubeletConfig Lists the KubeletConfig ConfigMaps from the controlPlane cluster
// and copies them to the hosted cluster.
// In addition, it deletes KubeletConfig ConfigMaps from the hosted cluster which are no longer relevant.
// I.e., has been deleted from the controlPlane cluster.
// IOW, it makes sure to synchronize the KubeletConfig ConfigMaps to be the same between the controlPlane cluster
// and the hosted-cluster.
func (r *reconciler) reconcileKubeletConfig(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)

	wantCMList := &corev1.ConfigMapList{}
	if err := r.cpClient.List(ctx, wantCMList, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{nodepool.KubeletConfigConfigMapLabel: "true"}),
		Namespace:     r.hcpNamespace,
	}); err != nil {
		return fmt.Errorf("failed to list KubeletConfig ConfigMaps from controlplane namespace %s: %w", r.hcpNamespace, err)
	}
	want := set.Set[string]{}
	for _, cm := range wantCMList.Items {
		want.Insert(cm.Name)
	}
	for _, cm := range wantCMList.Items {
		hostedClusterCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cm.Name,
				Namespace: ConfigManagedNamespace,
			},
		}
		if result, err := r.CreateOrUpdate(ctx, r.client, hostedClusterCM, func() error {
			return mutateKubeletConfig(&cm, hostedClusterCM)
		}); err != nil {
			return fmt.Errorf("failed to reconciled KubeletConfig %s ConfigMap: %w", client.ObjectKeyFromObject(hostedClusterCM).String(), err)
		} else {
			log.Info("reconciled ConfigMap", "result", result)
		}
	}

	haveCMList := &corev1.ConfigMapList{}
	if err := r.client.List(ctx, haveCMList, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{nodepool.KubeletConfigConfigMapLabel: "true"}),
		Namespace:     ConfigManagedNamespace,
	}); err != nil {
		return fmt.Errorf("failed to list KubeletConfig ConfigMaps from hostedcluster namespace %s: %w", ConfigManagedNamespace, err)
	}
	for i := range haveCMList.Items {
		cm := &haveCMList.Items[i]
		if want.Has(cm.Name) {
			continue
		}
		log.Info("delete mirror config ConfigMap", "config", client.ObjectKeyFromObject(cm).String())
		if _, err := util.DeleteIfNeeded(ctx, r.client, cm); err != nil {
			return fmt.Errorf("failed to delete ConfigMap %s: %w", client.ObjectKeyFromObject(cm).String(), err)
		}
	}
	return nil
}

func mutateKubeletConfig(controlPlaneConfigMap, hostedClusterConfigMap *corev1.ConfigMap) error {
	hostedClusterConfigMap.Immutable = ptr.To(true)
	hostedClusterConfigMap.Labels = labels.Merge(hostedClusterConfigMap.Labels, map[string]string{
		nodepool.KubeletConfigConfigMapLabel: "true",
		hyperv1.NodePoolLabel:                controlPlaneConfigMap.Labels[hyperv1.NodePoolLabel],
		nodepool.NTOMirroredConfigLabel:      "true",
	})
	hostedClusterConfigMap.Data = controlPlaneConfigMap.Data
	return nil
}

func isAllowedWebhookUrl(disallowedUrls []string, url string) bool {
	for i := range disallowedUrls {
		if strings.Contains(url, disallowedUrls[i]) {
			return false
		}
	}

	return true
}

func (r *reconciler) ensureResourceCreationIsBlocked(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	wh := manifests.ResourceCreationBlockerWebhook()
	if _, err := r.CreateOrUpdate(ctx, r.client, wh, func() error {
		reconcileCreationBlockerWebhook(wh, hcp)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile resource cleanup webhook: %w", err)
	}
	return nil
}

func reconcileCreationBlockerWebhook(wh *admissionregistrationv1.ValidatingWebhookConfiguration, hcp *hyperv1.HostedControlPlane) {
	failurePolicy := admissionregistrationv1.Fail
	sideEffectClass := admissionregistrationv1.SideEffectClassNone
	allScopes := admissionregistrationv1.AllScopes
	equivalentMatch := admissionregistrationv1.Equivalent

	// Base rules
	rules := []admissionregistrationv1.RuleWithOperations{
		{
			Operations: []admissionregistrationv1.OperationType{
				admissionregistrationv1.Create,
			},
			Rule: admissionregistrationv1.Rule{
				APIGroups:   []string{""},
				APIVersions: []string{"v1"},
				Resources: []string{
					"pods", "persistentvolumeclaims", "persistentvolumes", "services",
				},
				Scope: &allScopes,
			},
		},
	}

	// Only add the ingresscontrollers blocking rule if ingress capability is enabled.
	// This rule prevents re-creation of ingresscontrollers during cleanup,
	// but if ingress was never enabled, no need to explicitly block it.
	if capabilities.IsIngressCapabilityEnabled(hcp.Spec.Capabilities) {
		rules = append(rules, admissionregistrationv1.RuleWithOperations{
			Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
			Rule: admissionregistrationv1.Rule{
				APIGroups:   []string{"operator.openshift.io"},
				APIVersions: []string{"v1"},
				Resources:   []string{"ingresscontrollers"},
				Scope:       &allScopes,
			},
		})
	}

	wh.Webhooks = []admissionregistrationv1.ValidatingWebhook{
		{
			AdmissionReviewVersions: []string{"v1"},
			Name:                    "block-resources.hypershift.openshift.io",
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				Service: &admissionregistrationv1.ServiceReference{
					Namespace: "default",
					Name:      "xxx-invalid-service-xxx",
					Path:      ptr.To("/validate"),
					Port:      ptr.To[int32](443),
				},
			},
			FailurePolicy:     &failurePolicy,
			Rules:             rules,
			MatchPolicy:       &equivalentMatch,
			SideEffects:       &sideEffectClass,
			TimeoutSeconds:    ptr.To[int32](30),
			NamespaceSelector: &metav1.LabelSelector{},
			ObjectSelector:    &metav1.LabelSelector{},
		},
	}
}

func (r *reconciler) ensureIngressControllersRemoved(ctx context.Context, hcp *hyperv1.HostedControlPlane) (bool, error) {
	log := ctrl.LoggerFrom(ctx)
	ingressControllers := &operatorv1.IngressControllerList{}
	if err := r.client.List(ctx, ingressControllers); err != nil {
		return false, fmt.Errorf("failed to list ingress controllers: %w", err)
	}
	if len(ingressControllers.Items) == 0 {
		log.Info("There are no ingresscontrollers, nothing to do")
		return true, nil
	}
	var errs []error
	for i := range ingressControllers.Items {
		ic := &ingressControllers.Items[i]
		if ic.DeletionTimestamp.IsZero() {
			log.Info("Deleting ingresscontroller", "name", client.ObjectKeyFromObject(ic))
			if err := r.client.Delete(ctx, ic); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete %s", client.ObjectKeyFromObject(ic).String()))
			}
		}
	}
	if len(errs) > 0 {
		return false, fmt.Errorf("failed to delete ingress controllers: %w", errors.NewAggregate(errs))
	}

	// Force deleting pods under openshift-ingress to unblock ingress-controller deletion.
	// Ingress-operator is dependent on these pods deletion on removing the finalizers of ingress-controller.
	routerPods := &corev1.PodList{}
	if err := r.uncachedClient.List(ctx, routerPods, &client.ListOptions{Namespace: "openshift-ingress"}); err != nil {
		return false, fmt.Errorf("failed to list pods under openshift-ingress namespace: %w", err)
	}

	for i := range routerPods.Items {
		rp := &routerPods.Items[i]
		log.Info("Force deleting", "pod", client.ObjectKeyFromObject(rp).String())
		if err := r.client.Delete(ctx, rp, &client.DeleteOptions{GracePeriodSeconds: ptr.To[int64](0)}); err != nil {
			errs = append(errs, fmt.Errorf("failed to force delete %s", client.ObjectKeyFromObject(rp).String()))
		}
	}

	if len(errs) > 0 {
		return false, fmt.Errorf("failed to force delete pods under openshift-ingress namespace: %w", errors.NewAggregate(errs))
	}

	// Remove ingress service and route that were created by HCCO in case of basedomain passthrough feature is enabled
	if hcp.Spec.Platform.Type == hyperv1.KubevirtPlatform &&
		hcp.Spec.Platform.Kubevirt != nil &&
		hcp.Spec.Platform.Kubevirt.BaseDomainPassthrough != nil &&
		*hcp.Spec.Platform.Kubevirt.BaseDomainPassthrough {
		{
			var namespace string
			if hcp.Spec.Platform.Kubevirt.Credentials != nil {
				namespace = hcp.Spec.Platform.Kubevirt.Credentials.InfraNamespace
			} else {
				namespace = hcp.Namespace
			}
			cpService := manifests.IngressDefaultIngressPassthroughService(namespace)
			cpService.Name = fmt.Sprintf("%s-%s",
				manifests.IngressDefaultIngressPassthroughServiceName,
				hcp.Spec.Platform.Kubevirt.GenerateID)

			cpPassthroughRoute := manifests.IngressDefaultIngressPassthroughRoute(namespace)
			cpPassthroughRoute.Name = fmt.Sprintf("%s-%s",
				manifests.IngressDefaultIngressPassthroughRouteName,
				hcp.Spec.Platform.Kubevirt.GenerateID)

			err := r.kubevirtInfraClient.Delete(ctx, cpService)
			if err != nil && !apierrors.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to delete %s: %w", client.ObjectKeyFromObject(cpService).String(), err))
			}

			err = r.kubevirtInfraClient.Delete(ctx, cpPassthroughRoute)
			if err != nil && !apierrors.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to delete %s: %w", client.ObjectKeyFromObject(cpPassthroughRoute).String(), err))
			}
		}
	}

	if len(errs) > 0 {
		return false, fmt.Errorf("failed to delete ingress resources on infra cluster: %w", errors.NewAggregate(errs))
	}

	return false, nil
}

func (r *reconciler) ensureImageRegistryStorageRemoved(ctx context.Context) (bool, error) {
	log := ctrl.LoggerFrom(ctx)
	registryConfig := manifests.Registry()
	if err := r.client.Get(ctx, client.ObjectKeyFromObject(registryConfig), registryConfig); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("registry operator config does not exist, nothing to do")
			return true, nil
		}
		return false, fmt.Errorf("failed to get registry operator configuration: %w", err)
	}
	// If storage has already been removed, nothing to do
	// When the registry operator has been removed, management state in status is currently cleared.
	if registryConfig.Status.Storage.ManagementState == "" || registryConfig.Status.Storage.ManagementState == "Removed" {
		log.Info("Registry operator management state is blank or removed, done cleaning up")
		return true, nil
	}
	log.Info("Setting management state for registry operator to removed")
	if _, err := r.CreateOrUpdate(ctx, r.client, registryConfig, func() error {
		registryConfig.Spec.ManagementState = operatorv1.Removed
		return nil
	}); err != nil {
		return false, fmt.Errorf("failed to update image registry management state: %w", err)
	}
	return false, nil
}

func (r *reconciler) ensureServiceLoadBalancersRemoved(ctx context.Context) (bool, error) {
	_, err := cleanupResources(ctx, r.client, &corev1.ServiceList{}, func(obj client.Object) bool {
		svc := obj.(*corev1.Service)
		if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			return false
		}
		if _, hasAnnotation := svc.Annotations["ingresscontroller.operator.openshift.io/owning-ingresscontroller"]; hasAnnotation {
			return false
		}
		// The router-default from openshift-ingress namespace it has the same but as a label
		if _, hasLabel := svc.Labels["ingresscontroller.operator.openshift.io/owning-ingresscontroller"]; hasLabel {
			return false
		}
		return true
	}, false)
	if err != nil {
		return false, fmt.Errorf("failed to remove load balancer services: %w", err)
	}

	removed, err := allLoadBalancersRemoved(ctx, r.client)
	if err != nil {
		return false, fmt.Errorf("error checking load balancer services: %w", err)
	}

	return removed, nil
}

func (r *reconciler) ensureVolumeSnapshotsRemoved(ctx context.Context) (bool, error) {
	log := ctrl.LoggerFrom(ctx)
	vss := &snapshotv1.VolumeSnapshotList{}
	if err := r.client.List(ctx, vss); err != nil {
		return false, fmt.Errorf("cannot list volume snapshots: %w", err)
	}
	if len(vss.Items) == 0 {
		log.Info("There are no more volume snapshots. Nothing to cleanup.")
		return true, nil
	}
	if _, err := cleanupResources(ctx, r.client, &snapshotv1.VolumeSnapshotList{}, nil, false); err != nil {
		return false, fmt.Errorf("failed to remove volume snapshots: %w", err)
	}
	return false, nil
}

func (r *reconciler) ensurePersistentVolumesRemoved(ctx context.Context) (bool, error) {
	log := ctrl.LoggerFrom(ctx)
	pvs := &corev1.PersistentVolumeList{}
	if err := r.client.List(ctx, pvs); err != nil {
		return false, fmt.Errorf("cannot list persistent volumes: %w", err)
	}
	if len(pvs.Items) == 0 {
		log.Info("There are no more persistent volumes. Nothing to cleanup.")
		return true, nil
	}
	if _, err := cleanupResources(ctx, r.client, &corev1.PersistentVolumeClaimList{}, nil, false); err != nil {
		return false, fmt.Errorf("failed to remove persistent volume claims: %w", err)
	}
	if _, err := cleanupResources(ctx, r.uncachedClient, &corev1.PodList{}, func(obj client.Object) bool {
		pod := obj.(*corev1.Pod)
		return hasAttachedPVC(pod)
	}, true); err != nil {
		return false, fmt.Errorf("failed to remove pods: %w", err)
	}
	return false, nil
}

func (r *reconciler) reconcileInstallConfigMap(ctx context.Context, releaseImage *releaseinfo.ReleaseImage) error {
	cm := manifests.InstallConfigMap()
	if _, err := r.CreateOrUpdate(ctx, r.client, cm, func() error {
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data["invoker"] = "hypershift"
		// Only set 'version' if unset. This is meant to preserve the version with
		// which the cluster was installed and not followup upgrade versions.
		if _, hasVersion := cm.Data["version"]; !hasVersion {
			componentVersions, err := releaseImage.ComponentVersions()
			if err != nil {
				return fmt.Errorf("failed to look up component versions: %w", err)
			}
			cm.Data["version"] = componentVersions["release"]
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile install configmap: %w", err)
	}
	return nil
}

func hasAttachedPVC(pod *corev1.Pod) bool {
	for _, v := range pod.Spec.Volumes {
		if v.PersistentVolumeClaim != nil {
			return true
		}
	}
	return false
}

func shouldCleanupCloudResources(hcp *hyperv1.HostedControlPlane) bool {
	return hcp.Annotations[hyperv1.CleanupCloudResourcesAnnotation] == "true" &&
		meta.IsStatusConditionTrue(hcp.Status.Conditions, string(hyperv1.CVOScaledDown))
}

// cleanupResources generically deletes resources of a given type using an optional filter
// function. The result is a boolean indicating whether resources were found that match
// the filter and an error if one occurred.
func cleanupResources(ctx context.Context, c client.Client, list client.ObjectList, filter func(client.Object) bool, force bool) (bool, error) {
	log := ctrl.LoggerFrom(ctx)
	if err := c.List(ctx, list); err != nil {
		return false, fmt.Errorf("cannot list %T: %w", list, err)
	}

	var errs []error
	foundResource := false
	a := listAccessor(list)
	for i := 0; i < a.len(); i++ {
		obj := a.item(i)
		if filter == nil || filter(obj) {
			foundResource = true
			if obj.GetDeletionTimestamp().IsZero() {
				log.Info("Deleting resource", "type", fmt.Sprintf("%T", obj), "name", client.ObjectKeyFromObject(obj).String())
				var deleteErr error
				if force {
					deleteErr = c.Delete(ctx, obj, &client.DeleteOptions{GracePeriodSeconds: ptr.To[int64](0)})
				} else {
					deleteErr = c.Delete(ctx, obj)
				}
				if deleteErr != nil {
					errs = append(errs, deleteErr)
				}
			}
		}
	}
	return foundResource, errors.NewAggregate(errs)
}

type genericListAccessor struct {
	items reflect.Value
}

func listAccessor(list client.ObjectList) *genericListAccessor {
	return &genericListAccessor{
		items: reflect.ValueOf(list).Elem().FieldByName("Items"),
	}
}

func (a *genericListAccessor) len() int {
	return a.items.Len()
}

func (a *genericListAccessor) item(i int) client.Object {
	return (a.items.Index(i).Addr().Interface()).(client.Object)
}

func (r *reconciler) isClusterVersionUpdated(ctx context.Context, version string) bool {
	log := ctrl.LoggerFrom(ctx)
	var clusterVersion configv1.ClusterVersion
	err := r.client.Get(ctx, types.NamespacedName{Name: "version"}, &clusterVersion)
	if err != nil {
		log.Error(err, "unable to retrieve cluster version resource")
		return false
	}
	if clusterVersion.Status.Desired.Version != version {
		log.Info(fmt.Sprintf("cluster version not yet updated to %s", version))
		return false
	}
	return true
}

func (r *reconciler) reconcileStorage(ctx context.Context, hcp *hyperv1.HostedControlPlane) []error {
	var errs []error

	snapshotController := manifests.CSISnapshotController()
	if _, err := r.CreateOrUpdate(ctx, r.client, snapshotController, func() error {
		storage.ReconcileCSISnapshotController(snapshotController)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile CSISnapshotController : %w", err))
	}

	storageCR := manifests.Storage()
	if _, err := r.CreateOrUpdate(ctx, r.client, storageCR, func() error {
		storage.ReconcileStorage(storageCR)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile Storage : %w", err))
	}

	var driverNames []operatorv1.CSIDriverName
	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		driverNames = []operatorv1.CSIDriverName{operatorv1.AWSEBSCSIDriver}
	case hyperv1.OpenStackPlatform:
		driverNames = []operatorv1.CSIDriverName{
			operatorv1.CinderCSIDriver,
			operatorv1.ManilaCSIDriver,
		}
	}
	for _, driverName := range driverNames {
		driver := manifests.ClusterCSIDriver(driverName)
		if _, err := r.CreateOrUpdate(ctx, r.client, driver, func() error {
			storage.ReconcileClusterCSIDriver(driver)
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile ClusterCSIDriver %s: %w", driver.Name, err))
		}
	}
	return errs
}

// reconcileImageContentPolicyType deletes any existing ICSP since IDMS should be used for release versions >= 4.13,
// then reconciles the ImageContentSources into an IDMS instance.
func (r *reconciler) reconcileImageContentPolicyType(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	icsp := globalconfig.ImageContentSourcePolicy()

	// Delete any current ICSP
	_, err := util.DeleteIfNeeded(ctx, r.client, icsp)
	if err != nil {
		return fmt.Errorf("failed to delete image content source policy configuration configmap: %w", err)
	}

	// Next, reconcile the ImageDigestMirrorSet
	idms := globalconfig.ImageDigestMirrorSet()
	if _, err = r.CreateOrUpdate(ctx, r.client, idms, func() error {
		return globalconfig.ReconcileImageDigestMirrors(idms, hcp)
	}); err != nil {
		return fmt.Errorf("failed to reconcile image digest mirror set: %w", err)
	}

	return nil
}

// allLoadBalancersRemoved checks any service of type corev1.ServiceTypeLoadBalancer exists.
// If any one service of type corev1.ServiceTypeLoadBalancer exists, will return false or else will return true.
func allLoadBalancersRemoved(ctx context.Context, c client.Client) (bool, error) {
	log := ctrl.LoggerFrom(ctx)
	list := &corev1.ServiceList{}
	if err := c.List(ctx, list); err != nil {
		return false, fmt.Errorf("cannot list %T: %w", list, err)
	}

	for _, svc := range list.Items {
		if svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
			log.Info("Waiting on service of type LoadBalancer to be deleted", "name", svc.Name, "namespace", svc.Namespace)
			return false, nil
		}
	}

	return true, nil
}

func (r *reconciler) reconcileAzureCloudNodeManager(ctx context.Context, image string) []error {
	var errs []error

	serviceAccount := ccm.CloudNodeManagerServiceAccount()
	if _, err := r.CreateOrUpdate(ctx, r.client, serviceAccount, func() error { return nil }); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile %T %s: %w", serviceAccount, serviceAccount.Name, err))
	}

	clusterRole := ccm.CloudNodeManagerClusterRole()
	if _, err := r.CreateOrUpdate(ctx, r.client, clusterRole, func() error {
		// TODO explore scoping down rbac to the running Node
		clusterRole.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"nodes"},
				Verbs: []string{
					"get",
					"list",
					"patch",
					"update",
					"watch",
				},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"nodes/status"},
				Verbs: []string{
					"patch",
				},
			},
		}
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile %T %s: %w", clusterRole, clusterRole.Name, err))
	}

	clusterRoleBinding := ccm.CloudNodeManagerClusterRoleBinding()
	if _, err := r.CreateOrUpdate(ctx, r.client, clusterRoleBinding, func() error {
		clusterRoleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     clusterRole.Name,
		}
		clusterRoleBinding.Subjects = []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Namespace: serviceAccount.Namespace,
				Name:      serviceAccount.Name,
			},
		}
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile %T %s: %w", clusterRole, clusterRole.Name, err))
	}

	cloudNodeManagerDaemonSet := ccm.CloudNodeManagerDaemonSet()
	if _, err := r.CreateOrUpdate(ctx, r.client, cloudNodeManagerDaemonSet, func() error {
		cloudNodeManagerDaemonSet.Spec = appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"k8s-app": ccm.CloudNodeManagerName},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      map[string]string{"k8s-app": ccm.CloudNodeManagerName},
					Annotations: map[string]string{"cluster-autoscaler.kubernetes.io/daemonset-pod": "true"},
				},
				Spec: corev1.PodSpec{
					PriorityClassName:  "system-node-critical",
					ServiceAccountName: ccm.CloudNodeManagerName,
					HostNetwork:        true,
					// https://github.com/openshift/cluster-cloud-controller-manager-operator/blob/release-4.15/pkg/cloud/azure/assets/cloud-node-manager-daemonset.yaml#L34
					Tolerations: []corev1.Toleration{
						{
							Operator: corev1.TolerationOpExists,
							Effect:   corev1.TaintEffectNoSchedule,
						},
						{
							Key:      "node.kubernetes.io/unreachable",
							Operator: corev1.TolerationOpExists,
							Effect:   corev1.TaintEffectNoExecute,
						},
						{
							Key:      "node.kubernetes.io/not-ready",
							Operator: corev1.TolerationOpExists,
							Effect:   corev1.TaintEffectNoExecute,
						},
					},
					Containers: []corev1.Container{
						{
							Name:    ccm.CloudNodeManagerName,
							Image:   image,
							Command: []string{"/bin/bash"},
							Args:    []string{"-c", azureCCMScript},
							Env: []corev1.EnvVar{
								{
									Name: "NODE_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "spec.nodeName",
										},
									},
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("50Mi"),
									corev1.ResourceCPU:    resource.MustParse("50m"),
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "host-etc-kube",
									ReadOnly:  true,
									MountPath: "/etc/kubernetes",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "host-etc-kube",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/etc/kubernetes",
									Type: ptr.To(corev1.HostPathUnset),
								},
							},
						},
					},
				},
			},
		}
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile %T %s: %w", cloudNodeManagerDaemonSet, cloudNodeManagerDaemonSet.Name, err))
	}

	return errs
}

// imageRegistryPlatformWithPVC returns true if the platform requires a PVC for the image registry.
func imageRegistryPlatformWithPVC(platform hyperv1.PlatformType) bool {
	switch platform {
	case hyperv1.OpenStackPlatform:
		return true
	default:
		return false
	}
}
