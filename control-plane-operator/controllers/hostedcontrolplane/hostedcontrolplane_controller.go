package hostedcontrolplane

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"math/rand"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	rbacv1 "k8s.io/api/rbac/v1"

	"github.com/blang/semver"
	"github.com/go-logr/logr"
	routev1 "github.com/openshift/api/route/v1"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/hypershift/thirdparty/clusterapi/util"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/aws"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/etcd"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kcm"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/oapi"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/oauth"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/render"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/scheduler"
	hcputil "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/vpn"
	"github.com/openshift/hypershift/control-plane-operator/releaseinfo"
	etcdv1 "github.com/openshift/hypershift/thirdparty/etcd/v1beta2"
)

const (
	finalizer                   = "hypershift.openshift.io/finalizer"
	controlPlaneAnnotation      = "hypershift.openshift.io/hosted-control-plane"
	DefaultAdminKubeconfigName  = "admin-kubeconfig"
	DefaultAdminKubeconfigKey   = "kubeconfig"
	ingressOperatorNamespace    = "openshift-ingress-operator"
	hypershiftRouteLabel        = "hypershift.openshift.io/cluster"
	oauthBrandingManifest       = "v4-0-config-system-branding.yaml"
	DefaultAPIServerIPAddress   = "172.20.0.1"
	etcdClusterSize             = 1
	infrastructureCheckInterval = 5 * time.Second
	etcdDeleteCheckInterval     = 10 * time.Second
	etcdAvailableCheckInterval  = 10 * time.Second
	kasAvailableCheckInterval   = 10 * time.Second

	kubeAPIServerPort = 6443
)

var (
	excludeManifests = sets.NewString(
		"openshift-apiserver-service.yaml",
		"v4-0-config-system-branding.yaml",
		"oauth-server-service.yaml",
		"kube-apiserver-service.yaml",
	)

	version46 = semver.MustParse("4.6.0")
)

// NoopReconcile is just a default mutation function that does nothing.
var NoopReconcile controllerutil.MutateFn = func() error { return nil }

type InfrastructureStatus struct {
	APIHost            string
	APIPort            int32
	OAuthHost          string
	OAuthPort          int32
	VPNHost            string
	VPNPort            int32
	OpenShiftAPIHost   string
	OauthAPIServerHost string
}

func (s InfrastructureStatus) IsReady() bool {
	return len(s.APIHost) > 0 &&
		len(s.OAuthHost) > 0 &&
		len(s.VPNHost) > 0 &&
		s.APIPort > 0 &&
		s.OAuthPort > 0 &&
		s.VPNPort > 0
}

type HostedControlPlaneReconciler struct {
	client.Client

	Log             logr.Logger
	ReleaseProvider releaseinfo.Provider

	recorder record.EventRecorder
}

func (r *HostedControlPlaneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedControlPlane{}).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
		}).
		Watches(&source.Kind{Type: &etcdv1.EtcdCluster{}}, &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}).
		Build(r)
	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager %w", err)
	}

	r.recorder = mgr.GetEventRecorderFor("hosted-control-plane-controller")

	return nil
}

func (r *HostedControlPlaneReconciler) setAvailableCondition(ctx context.Context, hostedControlPlane *hyperv1.HostedControlPlane, oldStatus *hyperv1.HostedControlPlaneStatus,
	status hyperv1.ConditionStatus, reason, message string, result ctrl.Result, err error) (ctrl.Result, error) {
	conditions := &hostedControlPlane.Status.Conditions

	hcputil.SetConditionByType(conditions, hyperv1.Available, status, reason, message)

	// Sync status.ready with Available condition
	condition := hcputil.GetConditionByType(*conditions, hyperv1.Available)
	if condition != nil && condition.Status == hyperv1.ConditionTrue {
		hostedControlPlane.Status.Ready = true
	} else {
		hostedControlPlane.Status.Ready = false
	}
	if updateErr := r.updateStatus(ctx, hostedControlPlane, oldStatus); updateErr != nil {
		r.Log.Error(updateErr, "failed to update status")
		result.Requeue = true
	}
	return result, err
}

func (r *HostedControlPlaneReconciler) updateStatus(ctx context.Context, hostedControlPlane *hyperv1.HostedControlPlane, oldStatus *hyperv1.HostedControlPlaneStatus) error {
	conditions := &hostedControlPlane.Status.Conditions
	if reflect.DeepEqual(oldStatus, hostedControlPlane.Status) {
		// No change to status, nothing to sync
		return nil
	}

	// Check for changed conditions to update LastTransitionTime
	for k, condition := range *conditions {
		oldCondition := hcputil.GetConditionByType(oldStatus.Conditions, condition.Type)
		if oldCondition == nil || *oldCondition != condition {
			(*conditions)[k].LastTransitionTime = metav1.Now()
		}
	}
	return r.Status().Update(ctx, hostedControlPlane)
}

func (r *HostedControlPlaneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Log = ctrl.LoggerFrom(ctx)
	r.Log.Info("Reconciling")

	// Fetch the hostedControlPlane instance
	hostedControlPlane := &hyperv1.HostedControlPlane{}
	err := r.Client.Get(ctx, req.NamespacedName, hostedControlPlane)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Return early if deleted
	if !hostedControlPlane.DeletionTimestamp.IsZero() {
		if err := r.delete(ctx, hostedControlPlane); err != nil {
			r.Log.Error(err, "failed to delete cluster")
			return ctrl.Result{}, err
		}
		if controllerutil.ContainsFinalizer(hostedControlPlane, finalizer) {
			controllerutil.RemoveFinalizer(hostedControlPlane, finalizer)
			if err := r.Update(ctx, hostedControlPlane); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from cluster: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	oldStatus := hostedControlPlane.Status.DeepCopy()

	// Fetch the Cluster.
	cluster, err := util.GetOwnerCluster(ctx, r.Client, hostedControlPlane.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}

	if cluster == nil {
		r.Log.Info("Cluster Controller has not yet set OwnerRef")
		return ctrl.Result{}, nil
	}

	if util.IsPaused(cluster, hostedControlPlane) {
		r.Log.Info("HostedControlPlane or linked Cluster is marked as paused. Won't reconcile")
		return ctrl.Result{}, nil
	}

	// Ensure the hostedControlPlane has a finalizer for cleanup
	if !controllerutil.ContainsFinalizer(hostedControlPlane, finalizer) {
		controllerutil.AddFinalizer(hostedControlPlane, finalizer)
		if err := r.Update(ctx, hostedControlPlane); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to hostedControlPlane: %w", err)
		}
	}

	r.Log = r.Log.WithValues("cluster", cluster.Name)
	var infraStatus InfrastructureStatus
	{
		r.Log.Info("Reconciling infrastructure status")
		infraStatus, err = r.reconcileInfrastructureStatus(ctx, hostedControlPlane)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile infrastructure status: %w", err)
		}
		if infraStatus.IsReady() {
			hostedControlPlane.Status.ControlPlaneEndpoint = hyperv1.APIEndpoint{
				Host: infraStatus.APIHost,
				Port: infraStatus.APIPort,
			}
		} else {
			r.Log.Info("Infrastructure is not yet ready")
		}
	}

	r.Log.Info("Looking up release image metadata", "image", hostedControlPlane.Spec.ReleaseImage)
	lookupCtx, lookupCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer lookupCancel()
	releaseImage, err := r.ReleaseProvider.Lookup(lookupCtx, hostedControlPlane.Spec.ReleaseImage)
	if err != nil {
		return r.setAvailableCondition(ctx, hostedControlPlane, oldStatus, hyperv1.ConditionFalse, "ReleaseInfoLookupFailed", err.Error(), ctrl.Result{}, fmt.Errorf("failed to look up release info: %w", err))
	}
	componentVersions, err := releaseImage.ComponentVersions()
	if err != nil {
		return r.setAvailableCondition(ctx, hostedControlPlane, oldStatus, hyperv1.ConditionFalse, "InvalidComponentVersion", err.Error(), ctrl.Result{}, fmt.Errorf("invalid component versions found in release info: %w", err))
	}
	r.Log.Info("Found release info for image", "releaseImage", hostedControlPlane.Spec.ReleaseImage, "info", releaseImage, "componentImages", len(releaseImage.ComponentImages()), "componentVersions", componentVersions)

	if hostedControlPlane.Status.Version == "" {
		hostedControlPlane.Status.Version = releaseImage.Version()
	}

	// During an upgrade, if there's an old bootstrapper pod referring to the old
	// image, delete the pod to make way for the new one to be rendered. This is
	// a hack to avoid the refactoring of moving this pod into the hosted cluster
	// config operator.
	if hostedControlPlane.Spec.ReleaseImage != hostedControlPlane.Status.ReleaseImage {
		var bootstrapPod corev1.Pod
		err := r.Client.Get(ctx, types.NamespacedName{Namespace: hostedControlPlane.Namespace, Name: "manifests-bootstrapper"}, &bootstrapPod)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to get manifests bootstrapper pod: %w", err)
			}
		} else {
			currentImage := bootstrapPod.Spec.Containers[0].Image
			latestImage, latestImageFound := releaseImage.ComponentImages()["cli"]
			if latestImageFound && currentImage != latestImage {
				err := r.Client.Delete(ctx, &bootstrapPod)
				if err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to delete manifests bootstrapper pod: %w", err)
				}
				r.Log.Info("deleted manifests bootstrapper pod as part of an image rollout", "pod", bootstrapPod.Name, "from", currentImage, "to", latestImage)
			}
		}
	}

	// Reconcile etcd cluster status
	{
		etcdCluster := manifests.EtcdCluster(hostedControlPlane.Namespace)
		var err error
		if err = r.Get(ctx, types.NamespacedName{Namespace: etcdCluster.Namespace, Name: etcdCluster.Name}, etcdCluster); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to fetch etcd cluster %s/%s: %w", etcdCluster.Namespace, etcdCluster.Name, err)
		}
		if apierrors.IsNotFound(err) {
			etcdCluster = nil
		} else if !etcdCluster.DeletionTimestamp.IsZero() {
			// Wait til etcd cluster is gone in case it's being deleted
			return ctrl.Result{RequeueAfter: etcdDeleteCheckInterval}, nil
		}
		if err = etcd.ReconcileEtcdClusterStatus(ctx, r.Client, &hostedControlPlane.Status, etcdCluster); err != nil {
			return ctrl.Result{}, err
		}
		if err = r.updateStatus(ctx, hostedControlPlane, oldStatus); err != nil {
			return ctrl.Result{}, err
		} else {
			oldStatus = hostedControlPlane.Status.DeepCopy()
		}
	}
	// Reconcile Kube APIServer status
	{
		kubeAPIServerDeployment := manifests.KASDeployment(hostedControlPlane.Namespace)
		var err error
		if err = r.Get(ctx, client.ObjectKeyFromObject(kubeAPIServerDeployment), kubeAPIServerDeployment); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to fetch Kube APIServer deployment %s/%s: %w", kubeAPIServerDeployment.Namespace, kubeAPIServerDeployment.Name, err)
		}
		if apierrors.IsNotFound(err) {
			kubeAPIServerDeployment = nil
		}
		kas.ReconcileKubeAPIServerDeploymentStatus(ctx, &hostedControlPlane.Status, kubeAPIServerDeployment)
		if err = r.updateStatus(ctx, hostedControlPlane, oldStatus); err != nil {
			return ctrl.Result{}, err
		} else {
			oldStatus = hostedControlPlane.Status.DeepCopy()
		}
	}

	r.Log.Info("Reconciling infrastructure services")
	if err := r.reconcileInfrastructure(ctx, hostedControlPlane); err != nil {
		r.Log.Error(err, "failed to ensure infrastructure")
		return r.setAvailableCondition(ctx, hostedControlPlane, oldStatus, hyperv1.ConditionFalse, "InfrastructureEnsureFailed", err.Error(), ctrl.Result{}, fmt.Errorf("failed to ensure infrastructure: %w", err))
	}
	if !infraStatus.IsReady() {
		r.Log.Info("Cluster infrastructure is still provisioning, will try again later")
		return r.setAvailableCondition(ctx, hostedControlPlane, oldStatus, hyperv1.ConditionFalse, "WaitingOnInfrastructureReady", "Cluster infrastructure is still provisioning", ctrl.Result{RequeueAfter: infrastructureCheckInterval}, nil)
	}

	// Reconcile PKI
	r.Log.Info("Reconciling PKI")
	if err = r.reconcilePKI(ctx, hostedControlPlane, infraStatus); err != nil {
		r.Log.Error(err, "failed to reconcile PKI")
	}

	// Reconcile Cloud Provider Config
	r.Log.Info("Reconciling cloud provider config")
	if err = r.reconcileCloudProviderConfig(ctx, hostedControlPlane); err != nil {
		r.Log.Error(err, "failed to reconcile cloud provider config")
	}

	// Reconcile OIDC Route
	for _, service := range hostedControlPlane.Spec.Services {
		if service.Service != hyperv1.OIDC {
			continue
		}
		switch service.Type {
		case hyperv1.Route:
			r.Log.Info("Reconciling OIDC Route servicetype resources")
			if err := r.reconcileOIDCRouteResources(ctx, hostedControlPlane); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to reconcile OIDC route: %w", err)
			}
		case hyperv1.None:
			r.Log.Info("OIDC Route is disabled")
		}
	}

	// Reconcile etcd
	r.Log.Info("Reconciling Etcd")
	if err = r.reconcileEtcd(ctx, hostedControlPlane, releaseImage); err != nil {
		r.Log.Error(err, "failed to reconcile etcd")
		return ctrl.Result{}, err
	}
	{
		etcdAvailable := hcputil.GetConditionByType(hostedControlPlane.Status.Conditions, hyperv1.EtcdAvailable)
		if etcdAvailable == nil || etcdAvailable.Status != hyperv1.ConditionTrue {
			r.Log.Info("etcd is not yet available")
			msg := "Etcd service not yet available"
			if etcdAvailable != nil {
				msg = etcdAvailable.Message
			}
			return r.setAvailableCondition(ctx, hostedControlPlane, oldStatus, hyperv1.ConditionFalse, "EtcdNotAvailable", msg, ctrl.Result{RequeueAfter: etcdAvailableCheckInterval}, nil)
		}
	}
	// Reconcile VPN
	r.Log.Info("Reconciling VPN")
	if err = r.reconcileVPN(ctx, hostedControlPlane, releaseImage, infraStatus.VPNHost, infraStatus.VPNPort); err != nil {
		r.Log.Error(err, "failed to reconcile vpn")
		return ctrl.Result{}, err
	}

	// Reconcile kube apiserver
	r.Log.Info("Reconciling Kube API Server")
	if err = r.reconcileKubeAPIServer(ctx, hostedControlPlane, releaseImage, infraStatus.OAuthHost, infraStatus.OAuthPort); err != nil {
		r.Log.Error(err, "failed to reconcile kube apiserver")
		return ctrl.Result{}, err
	}
	{
		kasAvailable := hcputil.GetConditionByType(hostedControlPlane.Status.Conditions, hyperv1.KubeAPIServerAvailable)
		if kasAvailable == nil || kasAvailable.Status != hyperv1.ConditionTrue {
			r.Log.Info("Kube API server is not yet available")
			msg := "Kube API server not yet available"
			if kasAvailable != nil {
				msg = kasAvailable.Message
			}
			return r.setAvailableCondition(ctx, hostedControlPlane, oldStatus, hyperv1.ConditionFalse, "KubeAPIServerNotAvailable", msg, ctrl.Result{RequeueAfter: kasAvailableCheckInterval}, nil)
		}
	}
	// Reconcile kube controller manager
	r.Log.Info("Reconciling Kube Controller Manager")
	if err = r.reconcileKubeControllerManager(ctx, hostedControlPlane, releaseImage); err != nil {
		r.Log.Error(err, "failed to reconcile kube controller manager")
		return ctrl.Result{}, err
	}

	// Reconcile kube scheduler
	if err = r.reconcileKubeScheduler(ctx, hostedControlPlane, releaseImage); err != nil {
		r.Log.Error(err, "failed to reconcile kube controller manager")
		return ctrl.Result{}, err
	}

	// Install the control plane into the infrastructure
	r.Log.Info("Reconciling hosted control plane")
	err = r.ensureControlPlane(ctx, hostedControlPlane, infraStatus, releaseImage)
	if err != nil {
		r.Log.Error(err, "failed to ensure control plane")
		return r.setAvailableCondition(ctx, hostedControlPlane, oldStatus, hyperv1.ConditionFalse, "ControlPlaneEnsureFailed", err.Error(), ctrl.Result{}, fmt.Errorf("failed to ensure control plane: %w", err))
	}

	if hostedControlPlane.Spec.KubeConfig != nil {
		hostedControlPlane.Status.KubeConfig = hostedControlPlane.Spec.KubeConfig
	} else {
		hostedControlPlane.Status.KubeConfig = &hyperv1.KubeconfigSecretRef{
			Name: DefaultAdminKubeconfigName,
			Key:  DefaultAdminKubeconfigKey,
		}
	}

	// At this point the latest image is considered to be rolled out. If we're transitioning
	// from one image to another, record that on status and note the time.
	// TODO: This is an extremely weak check and doesn't take into account the actual
	// state of any of the managed components. It's basically a placeholder to prove
	// the orchestration of upgrades works at all.
	if hostedControlPlane.Status.ReleaseImage != hostedControlPlane.Spec.ReleaseImage {
		hostedControlPlane.Status.ReleaseImage = hostedControlPlane.Spec.ReleaseImage
		now := metav1.NewTime(time.Now())
		hostedControlPlane.Status.LastReleaseImageTransitionTime = &now
	}

	r.Log.Info("Successfully reconciled")
	return r.setAvailableCondition(ctx, hostedControlPlane, oldStatus, hyperv1.ConditionTrue, "AsExpected", "HostedControlPlane is ready", ctrl.Result{}, nil)
}

func (r *HostedControlPlaneReconciler) delete(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	releaseImage, err := r.ReleaseProvider.Lookup(ctx, hcp.Spec.ReleaseImage)
	if err != nil {
		return fmt.Errorf("failed to look up release info: %w", err)
	}
	manifests, err := r.generateControlPlaneManifests(ctx, hcp, InfrastructureStatus{}, releaseImage)
	if err != nil {
		return nil
	}
	if err := deleteManifests(ctx, r, r.Log, hcp.GetNamespace(), manifests); err != nil {
		return err
	}
	return nil
}

func servicePublishingStrategyByType(hcp *hyperv1.HostedControlPlane, svcType hyperv1.ServiceType) *hyperv1.ServicePublishingStrategy {
	for _, mapping := range hcp.Spec.Services {
		if mapping.Service == svcType {
			return &mapping.ServicePublishingStrategy
		}
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileAPIServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	serviceStrategy := servicePublishingStrategyByType(hcp, hyperv1.APIServer)
	if serviceStrategy == nil {
		return fmt.Errorf("APIServer service strategy not specified")
	}
	p := kas.NewKubeAPIServerServiceParams(hcp)
	apiServerService := manifests.KubeAPIServerService(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, apiServerService, func() error {
		return kas.ReconcileService(apiServerService, serviceStrategy, p.OwnerReference, p.APIServerPort)
	}); err != nil {
		return fmt.Errorf("failed to reconcile API server service: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileVPNServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	serviceStrategy := servicePublishingStrategyByType(hcp, hyperv1.VPN)
	if serviceStrategy == nil {
		return fmt.Errorf("VPN service strategy not specified")
	}
	p := vpn.NewVPNServiceParams(hcp)
	vpnServerService := manifests.VPNServerService(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, vpnServerService, func() error {
		return p.ReconcileService(vpnServerService, serviceStrategy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile VPN service: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOAuthServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	serviceStrategy := servicePublishingStrategyByType(hcp, hyperv1.OAuthServer)
	if serviceStrategy == nil {
		return fmt.Errorf("VPN service strategy not specified")
	}
	p := oauth.NewOAuthServiceParams(hcp)
	oauthServerService := manifests.OauthServerService(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, oauthServerService, func() error {
		return p.ReconcileService(oauthServerService, serviceStrategy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile OAuth service: %w", err)
	}
	if serviceStrategy.Type != hyperv1.Route {
		return nil
	}
	oauthRoute := manifests.OauthServerRoute(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, oauthRoute, func() error {
		return p.ReconcileRoute(oauthRoute)
	}); err != nil {
		return fmt.Errorf("failed to reconcile OAuth route: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOpenshiftAPIServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	svc := manifests.OpenshiftAPIServerService(hcp.Namespace)
	p := oapi.NewOpenShiftAPIServerServiceParams(hcp)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		return p.ReconcileOpenShiftAPIService(svc)
	}); err != nil {
		return fmt.Errorf("failed to reconcile OpenShift API server service: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOAuthAPIServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	svc := manifests.OauthAPIServerService(hcp.Namespace)
	p := oapi.NewOpenShiftAPIServerServiceParams(hcp)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		return p.ReconcileOAuthAPIService(svc)
	}); err != nil {
		return fmt.Errorf("failed to reconcile OAuth API server service: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileInfrastructure(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	// Ensure that we can run privileged pods
	if err := ensureVPNSCC(r, hcp, hcp.Namespace); err != nil {
		return fmt.Errorf("failed to ensure privileged SCC for the new namespace: %w", err)
	}
	if hcp.Spec.Services == nil {
		return fmt.Errorf("service publishing strategy undefined")
	}
	if err := r.reconcileAPIServerService(ctx, hcp); err != nil {
		return fmt.Errorf("failed to reconcile API server service: %w", err)
	}
	if err := r.reconcileVPNServerService(ctx, hcp); err != nil {
		return fmt.Errorf("failed to reconcile VPN server service: %w", err)
	}
	if err := r.reconcileOAuthServerService(ctx, hcp); err != nil {
		return fmt.Errorf("failed to reconcile OAuth server service: %w", err)
	}
	if err := r.reconcileOpenshiftAPIServerService(ctx, hcp); err != nil {
		return fmt.Errorf("failed to reconcile OpenShift api service: %w", err)
	}
	if err := r.reconcileOAuthAPIServerService(ctx, hcp); err != nil {
		return fmt.Errorf("failed to reconcile OpenShift OAuth api service: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileInfrastructureStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (InfrastructureStatus, error) {
	var infraStatus InfrastructureStatus
	var err error
	if infraStatus.APIHost, infraStatus.APIPort, err = r.reconcileAPIServerServiceStatus(ctx, hcp); err != nil {
		return infraStatus, err
	}
	if infraStatus.VPNHost, infraStatus.VPNPort, err = r.reconcileVPNServerServiceStatus(ctx, hcp); err != nil {
		return infraStatus, err
	}
	if infraStatus.OAuthHost, infraStatus.OAuthPort, err = r.reconcileOAuthServiceStatus(ctx, hcp); err != nil {
		return infraStatus, err
	}
	if infraStatus.OpenShiftAPIHost, err = r.reconcileOpenShiftAPIServerServiceStatus(ctx, hcp); err != nil {
		return infraStatus, err
	}
	if infraStatus.OauthAPIServerHost, err = r.reconcileOAuthAPIServerServiceStatus(ctx, hcp); err != nil {
		return infraStatus, err
	}
	return infraStatus, nil
}

func (r *HostedControlPlaneReconciler) reconcileAPIServerServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (host string, port int32, err error) {
	serviceStrategy := servicePublishingStrategyByType(hcp, hyperv1.APIServer)
	if serviceStrategy == nil {
		err = fmt.Errorf("APIServer service strategy not specified")
		return
	}
	svc := manifests.KubeAPIServerService(hcp.Namespace)
	if err = r.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		if apierrors.IsNotFound(err) {
			err = nil
			return
		}
		err = fmt.Errorf("failed to get kube apiserver service: %w", err)
		return
	}
	p := kas.NewKubeAPIServerServiceParams(hcp)
	return kas.ReconcileServiceStatus(svc, serviceStrategy, p.APIServerPort)
}

func (r *HostedControlPlaneReconciler) reconcileVPNServerServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (host string, port int32, err error) {
	serviceStrategy := servicePublishingStrategyByType(hcp, hyperv1.VPN)
	if serviceStrategy == nil {
		err = fmt.Errorf("VPN service strategy not specified")
		return
	}
	svc := manifests.VPNServerService(hcp.Namespace)
	if err = r.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		if apierrors.IsNotFound(err) {
			err = nil
			return
		}
		err = fmt.Errorf("failed to get vpn service: %w", err)
		return
	}
	p := vpn.NewVPNServiceParams(hcp)
	return p.ReconcileServiceStatus(svc, serviceStrategy)
}

func (r *HostedControlPlaneReconciler) reconcileOAuthServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (host string, port int32, err error) {
	serviceStrategy := servicePublishingStrategyByType(hcp, hyperv1.OAuthServer)
	if serviceStrategy == nil {
		err = fmt.Errorf("OAuth strategy not specified")
		return
	}
	var route *routev1.Route
	svc := manifests.OauthServerService(hcp.Namespace)
	if err = r.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		if apierrors.IsNotFound(err) {
			err = nil
			return
		}
		err = fmt.Errorf("failed to get oauth service: %w", err)
		return
	}
	if serviceStrategy.Type == hyperv1.Route {
		route = manifests.OauthServerRoute(hcp.Namespace)
		if err = r.Get(ctx, client.ObjectKeyFromObject(route), route); err != nil {
			if apierrors.IsNotFound(err) {
				err = nil
				return
			}
			err = fmt.Errorf("failed to get oauth route: %w", err)
			return
		}
	}
	p := oauth.NewOAuthServiceParams(hcp)
	return p.ReconcileServiceStatus(svc, route, serviceStrategy)
}

func (r *HostedControlPlaneReconciler) reconcileOpenShiftAPIServerServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (string, error) {
	svc := manifests.OpenshiftAPIServerService(hcp.Namespace)
	return r.reconcileClusterIPServiceStatus(ctx, svc)
}

func (r *HostedControlPlaneReconciler) reconcileOAuthAPIServerServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (string, error) {
	svc := manifests.OauthAPIServerService(hcp.Namespace)
	return r.reconcileClusterIPServiceStatus(ctx, svc)
}

func (r *HostedControlPlaneReconciler) reconcileClusterIPServiceStatus(ctx context.Context, svc *corev1.Service) (string, error) {
	if err := r.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to get cluster ip service %s/%s: %w", svc.Namespace, svc.Name, err)
	}
	return svc.Spec.ClusterIP, nil
}

func (r *HostedControlPlaneReconciler) ensureControlPlane(ctx context.Context, hcp *hyperv1.HostedControlPlane, infraStatus InfrastructureStatus, releaseImage *releaseinfo.ReleaseImage) error {
	r.Log.Info("ensuring control plane for cluster", "cluster", hcp.Name)

	targetNamespace := hcp.GetNamespace()

	// Create the configmap with the pull secret for the guest cluster
	pullSecret := common.PullSecret(targetNamespace)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
		return fmt.Errorf("failed to get pull secret %s: %w", pullSecret.Name, err)
	}
	pullSecretData, hasPullSecretData := pullSecret.Data[".dockerconfigjson"]
	if !hasPullSecretData {
		return fmt.Errorf("pull secret %s is missing the .dockerconfigjson key", pullSecret.Data)
	}
	targetPullSecret, err := generateTargetPullSecret(r.Scheme(), pullSecretData, targetNamespace)
	if err != nil {
		return fmt.Errorf("failed to create pull secret manifest for target cluster: %w", err)
	}
	if err := r.Create(ctx, targetPullSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to generate targetPullSecret: %v", err)
	}

	if hcp.Spec.Platform.AWS != nil {
		for _, role := range hcp.Spec.Platform.AWS.Roles {
			targetCredentialsSecret, err := generateTargetCredentialsSecret(r.Scheme(), role, targetNamespace)
			if err != nil {
				return fmt.Errorf("failed to create credentials secret manifest for target cluster: %w", err)
			}
			if err := r.Create(ctx, targetCredentialsSecret); err != nil && !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("failed to generate roleSecret: %v", err)
			}
		}
	}

	manifests, err := r.generateControlPlaneManifests(ctx, hcp, infraStatus, releaseImage)
	if err != nil {
		return err
	}

	// Create oauth branding manifest because it cannot be applied
	manifestBytes := manifests[oauthBrandingManifest]
	manifestObj := &unstructured.Unstructured{}
	if err := yaml.NewYAMLOrJSONDecoder(strings.NewReader(string(manifestBytes)), 100).Decode(manifestObj); err != nil {
		return fmt.Errorf("failed to decode manifest %s: %w", oauthBrandingManifest, err)
	}
	manifestObj.SetNamespace(targetNamespace)
	if err = r.Create(context.TODO(), manifestObj); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to apply manifest %s: %w", oauthBrandingManifest, err)
		}
	}

	if err := applyManifests(ctx, r, r.Log, targetNamespace, manifests); err != nil {
		return err
	}
	r.Log.Info("successfully applied all manifests")

	kubeadminPassword, err := generateKubeadminPassword()
	if err != nil {
		return fmt.Errorf("failed to generate kubeadmin password: %w", err)
	}

	kubeadminPasswordTargetSecret, err := generateKubeadminPasswordTargetSecret(r.Scheme(), kubeadminPassword, targetNamespace)
	if err != nil {
		return fmt.Errorf("failed to create kubeadmin secret manifest for target cluster: %w", err)
	}
	kubeadminPasswordTargetSecret.OwnerReferences = ensureHCPOwnerRef(hcp, kubeadminPasswordTargetSecret.OwnerReferences)
	if err := r.Create(ctx, kubeadminPasswordTargetSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to generate kubeadminPasswordTargetSecret: %w", err)
	}

	kubeadminPasswordSecret := generateKubeadminPasswordSecret(targetNamespace, kubeadminPassword)
	kubeadminPasswordSecret.OwnerReferences = ensureHCPOwnerRef(hcp, kubeadminPasswordSecret.OwnerReferences)
	if err := r.Create(ctx, kubeadminPasswordSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to generate kubeadminPasswordSecret: %w", err)
	}

	baseDomain, err := clusterBaseDomain(r.Client, ctx, hcp)
	if err != nil {
		return fmt.Errorf("couldn't determine cluster base domain  name: %w", err)
	}
	r.Log.Info(fmt.Sprintf("Cluster API URL: %s", fmt.Sprintf("https://%s:%d", infraStatus.APIHost, infraStatus.APIPort)))
	r.Log.Info(fmt.Sprintf("Kubeconfig is available in secret admin-kubeconfig in the %s namespace", hcp.GetNamespace()))
	r.Log.Info(fmt.Sprintf("Console URL:  %s", fmt.Sprintf("https://console-openshift-console.%s", fmt.Sprintf("apps.%s", baseDomain))))
	r.Log.Info(fmt.Sprintf("kubeadmin password is available in secret %q in the %s namespace", "kubeadmin-password", targetNamespace))

	return nil
}

func (r *HostedControlPlaneReconciler) reconcilePKI(ctx context.Context, hcp *hyperv1.HostedControlPlane, infraStatus InfrastructureStatus) error {
	p := pki.NewPKIParams(hcp, infraStatus.APIHost, infraStatus.OAuthHost, infraStatus.VPNHost)

	// Root CA
	rootCASecret := manifests.RootCASecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, rootCASecret, func() error {
		return p.ReconcileRootCA(rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile root CA: %w", err)
	}
	// Signer CA
	signerCASecret := manifests.ClusterSignerCASecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, signerCASecret, func() error {
		return p.ReconcileClusterSignerCA(signerCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile signer CA: %w", err)
	}
	// Combined CA
	combinedCA := manifests.CombinedCAConfigMap(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, combinedCA, func() error {
		return p.ReconcileCombinedCA(combinedCA, rootCASecret, signerCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile combined CA: %w", err)
	}

	// Etcd client secret
	etcdClientSecret := manifests.EtcdClientSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, etcdClientSecret, func() error {
		return p.ReconcileEtcdClientSecret(etcdClientSecret, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd client secret: %w", err)
	}

	// Etcd server secret
	etcdServerSecret := manifests.EtcdServerSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, etcdServerSecret, func() error {
		return p.ReconcileEtcdServerSecret(etcdServerSecret, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd server secret: %w", err)
	}

	// Etcd peer secret
	etcdPeerSecret := manifests.EtcdPeerSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, etcdPeerSecret, func() error {
		return p.ReconcileEtcdPeerSecret(etcdPeerSecret, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd peer secret: %w", err)
	}

	// VPN CA
	vpnCASecret := manifests.VPNSignerCASecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, vpnCASecret, func() error {
		return p.ReconcileVPNSignerCA(vpnCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile vpn CA: %w", err)
	}
	// VPN server cert
	vpnServerCert := manifests.VPNServerCertSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, vpnServerCert, func() error {
		return p.ReconcileVPNServerCertSecret(vpnServerCert, vpnCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile vpn server cert: %w", err)
	}
	// VPN KAS client cert
	vpnKASClientCert := manifests.VPNKubeAPIServerClientSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, vpnKASClientCert, func() error {
		return p.ReconcileVPNKubeAPIServerClientSecret(vpnKASClientCert, vpnCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile vpn kas client cert: %w", err)
	}
	// VPN worker client cert
	vpnWorkerClientCert := manifests.VPNWorkerClientSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, vpnWorkerClientCert, func() error {
		return p.ReconcileVPNWorkerClientSecret(vpnWorkerClientCert, vpnCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile vpn worker client secret: %w", err)
	}

	// KAS server secret
	kasServerSecret := manifests.KASServerCertSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, kasServerSecret, func() error {
		return p.ReconcileKASServerCertSecret(kasServerSecret, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kas server secret: %w", err)
	}

	// KAS kubelet client secret
	kasKubeletClientSecret := manifests.KASKubeletClientCertSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, kasKubeletClientSecret, func() error {
		return p.ReconcileKASKubeletClientCertSecret(kasKubeletClientSecret, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kas kubelet client secret: %w", err)
	}

	// KAS aggregator cert secret
	kasAggregatorCertSecret := manifests.KASAggregatorCertSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, kasAggregatorCertSecret, func() error {
		return p.ReconcileKASAggregatorCertSecret(kasAggregatorCertSecret, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kas aggregator secret: %w", err)
	}

	// KAS admin client cert secret
	kasAdminClientCertSecret := manifests.KASAdminClientCertSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, kasAdminClientCertSecret, func() error {
		return p.ReconcileKASAdminClientCertSecret(kasAdminClientCertSecret, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kas admin client secret: %w", err)
	}

	// KAS bootstrap client cert secret
	kasBootstrapClientCertSecret := manifests.KASMachineBootstrapClientCertSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, kasBootstrapClientCertSecret, func() error {
		return p.ReconcileKASMachineBootstrapClientCertSecret(kasBootstrapClientCertSecret, signerCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kas bootstrap client secret: %w", err)
	}

	// Service account signing key secret
	serviceAccountSigningKeySecret := manifests.ServiceAccountSigningKeySecret(hcp.Namespace)
	var signingKeySecret *corev1.Secret
	if len(hcp.Spec.SigningKey.Name) > 0 {
		signingKeySecret = &corev1.Secret{}
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcp.GetNamespace(), Name: hcp.Spec.SigningKey.Name}, signingKeySecret); err != nil {
			return fmt.Errorf("failed to get signing key %s: %w", hcp.Spec.SigningKey.Name, err)
		}
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r, serviceAccountSigningKeySecret, func() error {
		return p.ReconcileServiceAccountSigningKeySecret(serviceAccountSigningKeySecret, signingKeySecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server service account key secret: %w", err)
	}

	// OpenShift APIServer
	openshiftAPIServerCertSecret := manifests.OpenShiftAPIServerCertSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, openshiftAPIServerCertSecret, func() error {
		return p.ReconcileOpenShiftAPIServerCertSecret(openshiftAPIServerCertSecret, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kas admin client secret: %w", err)
	}

	// OpenShift OAuth APIServer
	openshiftOAuthAPIServerCertSecret := manifests.OpenShiftOAuthAPIServerCertSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, openshiftOAuthAPIServerCertSecret, func() error {
		return p.ReconcileOpenShiftOAuthAPIServerCertSecret(openshiftOAuthAPIServerCertSecret, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver cert: %w", err)
	}

	// OpenShift ControllerManager Cert
	openshiftControllerManagerCertSecret := manifests.OpenShiftControllerManagerCertSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, openshiftControllerManagerCertSecret, func() error {
		return p.ReconcileOpenShiftControllerManagerCertSecret(openshiftControllerManagerCertSecret, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift controller manager cert: %w", err)
	}

	// Ingress Cert
	ingressCert := manifests.IngressCert(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, ingressCert, func() error {
		return p.ReconcileIngressCert(ingressCert, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile ingress cert secret: %w", err)
	}

	// MCS Cert
	machineConfigServerCert := manifests.MachineConfigServerCert(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, machineConfigServerCert, func() error {
		return p.ReconcileMachineConfigServerCert(machineConfigServerCert, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile machine config server cert secret: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileCloudProviderConfig(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		p := aws.NewAWSParams(hcp)
		awsProviderConfig := manifests.AWSProviderConfig(hcp.Namespace)
		if _, err := controllerutil.CreateOrUpdate(ctx, r, awsProviderConfig, func() error {
			return p.ReconcileCloudConfig(awsProviderConfig)
		}); err != nil {
			return fmt.Errorf("failed to reconcile aws provider config: %w", err)
		}
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileEtcd(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage) error {

	p := etcd.NewEtcdParams(hcp, releaseImage.ComponentImages())

	// Etcd Operator ServiceAccount
	operatorServiceAccount := manifests.EtcdOperatorServiceAccount(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, operatorServiceAccount, func() error {
		return p.ReconcileOperatorServiceAccount(operatorServiceAccount)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd operator service account: %w", err)
	}

	// Etcd operator role
	operatorRole := manifests.EtcdOperatorRole(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, operatorRole, func() error {
		operatorRole.OwnerReferences = ensureHCPOwnerRef(hcp, operatorRole.OwnerReferences)
		return p.ReconcileOperatorRole(operatorRole)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd operator role: %w", err)
	}

	// Etcd operator rolebinding
	operatorRoleBinding := manifests.EtcdOperatorRoleBinding(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, operatorRoleBinding, func() error {
		operatorRoleBinding.OwnerReferences = ensureHCPOwnerRef(hcp, operatorRoleBinding.OwnerReferences)
		return p.ReconcileOperatorRoleBinding(operatorRoleBinding)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd operator role binding: %w", err)
	}

	// Etcd operator deployment
	operatorDeployment := manifests.EtcdOperatorDeployment(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, operatorDeployment, func() error {
		operatorDeployment.OwnerReferences = ensureHCPOwnerRef(hcp, operatorDeployment.OwnerReferences)
		return p.ReconcileOperatorDeployment(operatorDeployment)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd operator deployment: %w", err)
	}

	// Etcd cluster
	etcdCluster := manifests.EtcdCluster(hcp.Namespace)
	etcdAvailableCond := hcputil.GetConditionByType(hcp.Status.Conditions, hyperv1.EtcdAvailable)
	if etcdAvailableCond != nil && etcdAvailableCond.Status == hyperv1.ConditionFalse && etcdAvailableCond.Reason == etcd.EtcdReasonFailed {
		if err := r.Delete(ctx, etcdCluster); err != nil {
			return fmt.Errorf("failed to delete etcd cluster: %w", err)
		}
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r, etcdCluster, func() error {
		etcdCluster.OwnerReferences = ensureHCPOwnerRef(hcp, etcdCluster.OwnerReferences)
		return p.ReconcileCluster(etcdCluster)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd cluster: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileVPN(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, address string, port int32) error {
	r.Log.Info("Reconciling VPN")
	p := vpn.NewVPNParams(hcp, releaseImage.ComponentImages(), address, port)
	serviceAccount := manifests.VPNServiceAccount(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, serviceAccount, func() error {
		return p.ReconcileVPNServiceAccount(serviceAccount)
	}); err != nil {
		return fmt.Errorf("failed to reconcile vpn service account: %w", err)
	}
	serverConfig := manifests.VPNServerConfig(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, serverConfig, func() error {
		return p.ReconcileVPNServerConfig(serverConfig)
	}); err != nil {
		return fmt.Errorf("failed to reconcile vpn server config: %w", err)
	}
	serverClientConfig := manifests.VPNServerClientConfig(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, serverClientConfig, func() error {
		return p.ReconcileVPNServerClientConfig(serverClientConfig)
	}); err != nil {
		return fmt.Errorf("failed to reconcile vpn server client config: %w", err)
	}
	kubeAPIServerConfig := manifests.VPNKubeAPIServerClientConfig(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, kubeAPIServerConfig, func() error {
		return p.ReconcileKubeAPIServerClientConfig(kubeAPIServerConfig)
	}); err != nil {
		return fmt.Errorf("failed to reconcile vpn kas client config: %w", err)
	}
	clientConfig := manifests.VPNWorkerClientConfig(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, clientConfig, func() error {
		return p.ReconcileWorkerClientConfig(clientConfig)
	}); err != nil {
		return fmt.Errorf("failed to reconcile vpn worker client config: %w", err)
	}
	serverDeployment := manifests.VPNServerDeployment(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, serverDeployment, func() error {
		return p.ReconcileServerDeployment(serverDeployment)
	}); err != nil {
		return fmt.Errorf("failed to reconcile vpn server deployment: %w", err)
	}
	clientDeployment := manifests.VPNWorkerClientDeployment(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, clientDeployment, func() error {
		return p.ReconcileWorkerClientDeployment(clientDeployment)
	}); err != nil {
		return fmt.Errorf("failed to reconcile vpn client deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileKubeAPIServer(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, oauthAddress string, oauthPort int32) error {
	p := kas.NewKubeAPIServerParams(hcp, releaseImage.ComponentImages(), oauthAddress, oauthPort)

	rootCA := manifests.RootCASecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return fmt.Errorf("failed to get root ca cert secret: %w", err)
	}

	clientCertSecret := manifests.KASAdminClientCertSecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(clientCertSecret), clientCertSecret); err != nil {
		return fmt.Errorf("failed to get admin client cert secret: %w", err)
	}
	bootstrapClientCertSecret := manifests.KASMachineBootstrapClientCertSecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(bootstrapClientCertSecret), bootstrapClientCertSecret); err != nil {
		return fmt.Errorf("failed to get bootstrap client cert secret: %w", err)
	}

	serviceKubeconfigSecret := manifests.KASServiceKubeconfigSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, serviceKubeconfigSecret, func() error {
		return kas.ReconcileServiceKubeconfigSecret(serviceKubeconfigSecret, clientCertSecret, rootCA, p.OwnerRef, p.APIServerPort)
	}); err != nil {
		return fmt.Errorf("failed to reconcile service admin kubeconfig secret: %w", err)
	}

	// The client used by CAPI machine controller expects the kubeconfig to follow this naming and key convention
	// https://github.com/kubernetes-sigs/cluster-api/blob/5c85a0a01ee44ecf7c8a3c3fdc867a88af87d73c/util/secret/secret.go#L29-L33
	capiKubeconfigSecret := manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, capiKubeconfigSecret, func() error {
		// TODO(alberto): This secret is currently using the cluster-admin kubeconfig for the guest cluster.
		// We should create a separate kubeconfig with a tight set of permissions for it to use.
		return kas.ReconcileServiceCAPIKubeconfigSecret(capiKubeconfigSecret, clientCertSecret, rootCA, p.OwnerRef, p.APIServerPort)
	}); err != nil {
		return fmt.Errorf("failed to reconcile CAPI service admin kubeconfig secret: %w", err)
	}

	localhostKubeconfigSecret := manifests.KASLocalhostKubeconfigSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, localhostKubeconfigSecret, func() error {
		return kas.ReconcileLocalhostKubeconfigSecret(localhostKubeconfigSecret, clientCertSecret, rootCA, p.OwnerRef, p.APIServerPort)
	}); err != nil {
		return fmt.Errorf("failed to reconcile localhost kubeconfig secret: %w", err)
	}

	externalKubeconfigSecret := manifests.KASExternalKubeconfigSecret(hcp.Namespace, hcp.Spec.KubeConfig)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, externalKubeconfigSecret, func() error {
		return kas.ReconcileExternalKubeconfigSecret(externalKubeconfigSecret, clientCertSecret, rootCA, p.OwnerRef, p.ExternalURL(), p.ExternalKubeconfigKey())
	}); err != nil {
		return fmt.Errorf("failed to reconcile external kubeconfig secret: %w", err)
	}

	bootstrapKubeconfigSecret := manifests.KASBootstrapKubeconfigSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, bootstrapKubeconfigSecret, func() error {
		return kas.ReconcileBootstrapKubeconfigSecret(bootstrapKubeconfigSecret, bootstrapClientCertSecret, rootCA, p.OwnerRef, p.ExternalURL())
	}); err != nil {
		return fmt.Errorf("failed to reconcile bootstrap kubeconfig secret: %w", err)
	}

	kubeAPIServerAuditConfig := manifests.KASAuditConfig(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, kubeAPIServerAuditConfig, func() error {
		return kas.ReconcileAuditConfig(kubeAPIServerAuditConfig, p.OwnerRef, p.AuditPolicyProfile())
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server audit config: %w", err)
	}

	kubeAPIServerConfig := manifests.KASConfig(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, kubeAPIServerConfig, func() error {
		return kas.ReconcileConfig(kubeAPIServerConfig,
			p.OwnerRef,
			p.ConfigParams())
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server config: %w", err)
	}

	oauthMetadata := manifests.KASOAuthMetadata(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, oauthMetadata, func() error {
		return kas.ReconcileOauthMetadata(oauthMetadata, p.OwnerRef, p.ExternalOAuthAddress, p.ExternalOAuthPort)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth metadata: %w", err)
	}

	kubeAPIServerDeployment := manifests.KASDeployment(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, kubeAPIServerDeployment, func() error {
		return kas.ReconcileKubeAPIServerDeployment(kubeAPIServerDeployment,
			p.OwnerRef,
			p.DeploymentConfig,
			p.NamedCertificates(),
			p.CloudProviderConfig,
			p.Images,
		)
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileKubeControllerManager(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage) error {
	p := kcm.NewKubeControllerManagerParams(hcp, releaseImage.ComponentImages())

	combinedCA := manifests.CombinedCAConfigMap(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(combinedCA), combinedCA); err != nil {
		return fmt.Errorf("failed to fetch combined ca configmap: %w", err)
	}
	serviceServingCA := manifests.KCMServiceServingCA(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, serviceServingCA, func() error {
		return p.ReconcileKCMServiceServingCA(serviceServingCA, combinedCA)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kcm serving ca: %w", err)
	}

	kcmConfig := manifests.KCMConfig(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, kcmConfig, func() error {
		return p.ReconcileConfig(kcmConfig, serviceServingCA)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kcm config: %w", err)
	}

	kcmDeployment := manifests.KCMDeployment(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, kcmDeployment, func() error {
		return p.ReconcileDeployment(kcmDeployment, serviceServingCA)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kcm deployment: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileKubeScheduler(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage) error {
	p := scheduler.NewKubeSchedulerParams(hcp, releaseImage.ComponentImages())

	schedulerConfig := manifests.SchedulerConfig(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, schedulerConfig, func() error {
		return p.ReconcileConfig(schedulerConfig)
	}); err != nil {
		return fmt.Errorf("failed to reconcile scheduler config: %w", err)
	}

	schedulerDeployment := manifests.SchedulerDeployment(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, schedulerDeployment, func() error {
		return p.ReconcileDeployment(schedulerDeployment)
	}); err != nil {
		return fmt.Errorf("failed to reconcile scheduler deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) generateControlPlaneManifests(ctx context.Context, hcp *hyperv1.HostedControlPlane, infraStatus InfrastructureStatus, releaseImage *releaseinfo.ReleaseImage) (map[string][]byte, error) {
	targetNamespace := hcp.GetNamespace()

	var sshKeyData []byte
	if len(hcp.Spec.SSHKey.Name) > 0 {
		var sshKeySecret corev1.Secret
		err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcp.Namespace, Name: hcp.Spec.SSHKey.Name}, &sshKeySecret)
		if err != nil {
			return nil, fmt.Errorf("failed to get SSH key secret %s: %w", hcp.Spec.SSHKey.Name, err)
		}
		data, hasSSHKeyData := sshKeySecret.Data["id_rsa.pub"]
		if !hasSSHKeyData {
			return nil, fmt.Errorf("SSH key secret secret %s is missing the id_rsa.pub key", hcp.Spec.SSHKey.Name)
		}
		sshKeyData = data
	}

	baseDomain, err := clusterBaseDomain(r.Client, ctx, hcp)
	if err != nil {
		return nil, fmt.Errorf("couldn't determine cluster base domain  name: %w", err)
	}

	params := render.NewClusterParams()
	params.Namespace = targetNamespace
	params.ExternalAPIDNSName = infraStatus.APIHost
	params.ExternalAPIPort = uint(infraStatus.APIPort)
	params.ExternalAPIAddress = DefaultAPIServerIPAddress
	params.ExternalOpenVPNAddress = infraStatus.VPNHost
	params.ExternalOpenVPNPort = uint(infraStatus.VPNPort)
	params.ExternalOauthDNSName = infraStatus.OAuthHost
	params.ExternalOauthPort = uint(infraStatus.OAuthPort)
	params.ServiceCIDR = hcp.Spec.ServiceCIDR
	params.PodCIDR = hcp.Spec.PodCIDR
	params.MachineCIDR = hcp.Spec.MachineCIDR
	params.ReleaseImage = hcp.Spec.ReleaseImage
	params.IngressSubdomain = fmt.Sprintf("apps.%s", baseDomain)
	params.OpenShiftAPIClusterIP = infraStatus.OpenShiftAPIHost
	params.OauthAPIClusterIP = infraStatus.OauthAPIServerHost
	params.BaseDomain = baseDomain
	params.PublicZoneID = hcp.Spec.DNS.PublicZoneID
	params.PrivateZoneID = hcp.Spec.DNS.PrivateZoneID
	params.CloudProvider = cloudProvider(hcp)
	params.PlatformType = platformType(hcp)
	params.InfraID = hcp.Spec.InfraID

	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		params.AWSRegion = hcp.Spec.Platform.AWS.Region
		params.AWSVPCID = hcp.Spec.Platform.AWS.VPC
		params.ProviderCredsSecretName = hcp.Spec.Platform.AWS.KubeCloudControllerCreds.Name
		if hcp.Spec.Platform.AWS.NodePoolDefaults != nil {
			params.AWSZone = hcp.Spec.Platform.AWS.NodePoolDefaults.Zone
			if hcp.Spec.Platform.AWS.NodePoolDefaults.Subnet.ID != nil {
				params.AWSSubnetID = *hcp.Spec.Platform.AWS.NodePoolDefaults.Subnet.ID
			}
		}
	}

	params.InternalAPIPort = defaultAPIServerPort
	params.IssuerURL = hcp.Spec.IssuerURL
	params.EtcdClientName = "etcd-client"
	params.NetworkType = "OpenShiftSDN"
	params.ImageRegistryHTTPSecret = generateImageRegistrySecret()
	params.APIAvailabilityPolicy = render.SingleReplica
	params.ControllerAvailabilityPolicy = render.SingleReplica
	params.SSHKey = string(sshKeyData)

	combinedCA := manifests.CombinedCAConfigMap(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(combinedCA), combinedCA); err != nil {
		return nil, fmt.Errorf("cannot get combined ca secret: %w", err)
	}
	caBytes, hasData := combinedCA.Data[pki.CASignerCertMapKey]
	if !hasData {
		return nil, fmt.Errorf("pki secret %q is missing a %s key", combinedCA.Name, pki.CASignerCertMapKey)
	}
	params.OpenshiftAPIServerCABundle = base64.StdEncoding.EncodeToString([]byte(caBytes))
	params.OauthAPIServerCABundle = params.OpenshiftAPIServerCABundle

	var pullSecret corev1.Secret
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcp.GetNamespace(), Name: hcp.Spec.PullSecret.Name}, &pullSecret); err != nil {
		return nil, fmt.Errorf("failed to get pull secret %s: %w", hcp.Spec.PullSecret.Name, err)
	}
	pullSecretData, hasPullSecretData := pullSecret.Data[".dockerconfigjson"]
	if !hasPullSecretData {
		return nil, fmt.Errorf("pull secret %s is missing the .dockerconfigjson key", hcp.Spec.PullSecret.Name)
	}

	secrets := &corev1.SecretList{}
	if err = r.List(ctx, secrets, client.InNamespace(hcp.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list secrets in current namespace: %w", err)
	}

	configMaps := &corev1.ConfigMapList{}
	if err = r.List(ctx, configMaps, client.InNamespace(hcp.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list configmaps in current namespace: %w", err)
	}

	manifests, err := render.RenderClusterManifests(params, releaseImage, pullSecretData, secrets, configMaps)
	if err != nil {
		return nil, fmt.Errorf("failed to render hypershift manifests for cluster: %w", err)
	}
	return manifests, nil
}

func (r *HostedControlPlaneReconciler) reconcileKubeAPIServerServiceNodePortResources(ctx context.Context, hcp *hyperv1.HostedControlPlane, namespace string, nodePortMetadata hyperv1.NodePortPublishingStrategy) error {
	svc := manifests.KubeAPIServerService(namespace)
	var nodePort int32 = 0
	if nodePortMetadata.Port > 0 {
		nodePort = nodePortMetadata.Port
	}
	var kubeAPIServerServiceData corev1.Service
	r.Log.Info("Checking for existing service", "serviceName", svc.Name, "namespace", svc.Namespace)
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: svc.Namespace, Name: svc.Name}, &kubeAPIServerServiceData); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if len(kubeAPIServerServiceData.Spec.Ports) > 0 && kubeAPIServerServiceData.Spec.Ports[0].NodePort > 0 {
		r.Log.Info("Preserving existing nodePort for service", "nodePort", kubeAPIServerServiceData.Spec.Ports[0].NodePort)
		nodePort = kubeAPIServerServiceData.Spec.Ports[0].NodePort
	}
	r.Log.Info("Updating KubeAPI service")
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.OwnerReferences = ensureHCPOwnerRef(hcp, svc.OwnerReferences)
		return reconcileKubeAPIServerServiceNodePort(svc, nodePort)
	})
	return err
}

func (r *HostedControlPlaneReconciler) updateStatusKubeAPIServerServiceNodePort(ctx context.Context, namespace string, servicePublishingStrategyMapping hyperv1.ServicePublishingStrategyMapping, status *InfrastructureStatus) error {
	r.Log.Info("Retrieving KubeAPI service to get nodePort value")
	svc := manifests.KubeAPIServerService(namespace)
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: svc.Namespace, Name: svc.Name}, svc); err != nil {
		return err
	}
	if !(svc.Spec.Ports[0].NodePort > 0) {
		return fmt.Errorf("nodePort not populated")
	}
	r.Log.Info("Fetched Kube API service nodePort", "nodePort", svc.Spec.Ports[0].NodePort)
	status.APIHost = servicePublishingStrategyMapping.NodePort.Address
	status.APIPort = svc.Spec.Ports[0].NodePort
	return nil
}

func reconcileKubeAPIServerServiceNodePort(svc *corev1.Service, nodePort int32) error {
	svc.Spec.Ports = KubeAPIServerServicePorts(defaultAPIServerPort)
	if nodePort > 0 {
		svc.Spec.Ports[0].NodePort = nodePort
	}
	svc.Spec.Selector = KubeAPIServerServiceSelector()
	svc.Spec.Type = corev1.ServiceTypeNodePort
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileKubeAPIServerServiceLoadBalancerResources(ctx context.Context, hcp *hyperv1.HostedControlPlane, namespace string, status *InfrastructureStatus) error {
	svc := manifests.KubeAPIServerService(namespace)
	var kubeAPIServerServiceData corev1.Service
	var existingNodePort int32 = 0
	r.Log.Info("Checking for existing service", "serviceName", svc.Name, "namespace", svc.Namespace)
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: svc.Namespace, Name: svc.Name}, &kubeAPIServerServiceData); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if len(kubeAPIServerServiceData.Spec.Ports) > 0 && kubeAPIServerServiceData.Spec.Ports[0].NodePort > 0 {
		r.Log.Info("Preserving existing nodePort for service", "nodePort", kubeAPIServerServiceData.Spec.Ports[0].NodePort)
		existingNodePort = kubeAPIServerServiceData.Spec.Ports[0].NodePort
	}
	r.Log.Info("Updating KubeAPI service")
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.OwnerReferences = ensureHCPOwnerRef(hcp, svc.OwnerReferences)
		return reconcileKubeAPIServerServiceLoadBalancer(svc, existingNodePort)
	})
	return err
}

func (r *HostedControlPlaneReconciler) updateStatusKubeAPIServerServiceLoadBalancer(ctx context.Context, namespace string, status *InfrastructureStatus) error {
	r.Log.Info("Retrieving KubeAPI service to get load balancer info")
	svc := manifests.KubeAPIServerService(namespace)
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: svc.Namespace, Name: svc.Name}, svc); err != nil {
		return err
	}
	var addr string
	if len(svc.Status.LoadBalancer.Ingress) > 0 {
		switch {
		case svc.Status.LoadBalancer.Ingress[0].Hostname != "":
			addr = svc.Status.LoadBalancer.Ingress[0].Hostname
		case svc.Status.LoadBalancer.Ingress[0].IP != "":
			addr = svc.Status.LoadBalancer.Ingress[0].IP
		}
	}
	r.Log.Info("Retrieved load balancer info", "serviceName", svc.Name, "address", addr)
	status.APIHost = addr
	status.APIPort = defaultAPIServerPort
	return nil
}

func reconcileKubeAPIServerServiceLoadBalancer(svc *corev1.Service, existingNodePort int32) error {
	svc.Spec.Ports = KubeAPIServerServicePorts(defaultAPIServerPort)
	if existingNodePort > 0 {
		svc.Spec.Ports[0].NodePort = existingNodePort
	}
	svc.Spec.Selector = KubeAPIServerServiceSelector()
	svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	return nil
}

func (r *HostedControlPlaneReconciler) updateStatusOauthServerServiceRoute(ctx context.Context, namespace string, status *InfrastructureStatus) error {
	r.Log.Info("Gathering route metadata")
	routeInstance := manifests.OauthServerRoute(namespace)
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: routeInstance.Namespace, Name: routeInstance.Name}, routeInstance); err != nil {
		return err
	}
	var addr = routeInstance.Spec.Host
	r.Log.Info("Retrieved route  info", "routeName", routeInstance.Name, "address", addr)
	status.OAuthHost = addr
	status.OAuthPort = 443
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOauthServerServiceRouteResources(ctx context.Context, hcp *hyperv1.HostedControlPlane, namespace string) error {
	svc := manifests.OauthServerService(namespace)
	r.Log.Info("Updating oauth service")
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.OwnerReferences = ensureHCPOwnerRef(hcp, svc.OwnerReferences)
		return r.reconcileOauthServiceClusterIP(svc)
	})
	if err != nil {
		return err
	}
	r.Log.Info("Updated oauth service. Proceeding to update oauth route")
	routeInstance := manifests.OauthServerRoute(namespace)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, routeInstance, func() error {
		routeInstance.OwnerReferences = ensureHCPOwnerRef(hcp, svc.OwnerReferences)
		return reconcileOauthServerRoute(routeInstance)
	})
	return err
}

func (r *HostedControlPlaneReconciler) reconcileOauthServiceClusterIP(svc *corev1.Service) error {
	svc.Spec.Ports = OauthServerServicePorts()
	svc.Spec.Selector = OauthServerServiceSelector()
	svc.Spec.Type = corev1.ServiceTypeClusterIP
	return nil
}

func reconcileOauthServerRoute(routeInstance *routev1.Route) error {
	routeInstance.Spec = routev1.RouteSpec{
		To: routev1.RouteTargetReference{
			Kind: "Service",
			Name: manifests.OauthServiceName,
		},
		TLS: &routev1.TLSConfig{
			Termination:                   routev1.TLSTerminationPassthrough,
			InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect,
		},
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOIDCRouteResources(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	route := manifests.OIDCRoute(hcp.GetNamespace())
	r.Log.Info("Updating OIDC route")
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, route, func() error {
		rootCASecret := manifests.RootCASecret(hcp.Namespace)
		if err := r.Get(ctx, client.ObjectKeyFromObject(rootCASecret), rootCASecret); err != nil {
			return err
		}
		u, err := url.Parse(hcp.Spec.IssuerURL)
		if err != nil {
			return fmt.Errorf("Unable to parse issuer URL: %s", hcp.Spec.IssuerURL)
		}
		route.OwnerReferences = ensureHCPOwnerRef(hcp, route.OwnerReferences)
		route.Spec = routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: manifests.KubeAPIServerServiceName,
			},
			Host: u.Host,
			TLS: &routev1.TLSConfig{
				// Reencrypt is used here because we need to probe for the
				// CA thumbprint before the KAS on the HCP is running
				Termination:                   routev1.TLSTerminationReencrypt,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
				DestinationCACertificate:      string(rootCASecret.Data["ca.crt"]),
			},
		}
		return nil
	})
	return err
}

func ensureVPNSCC(c client.Client, hcp *hyperv1.HostedControlPlane, namespace string) error {
	sccBinding := &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("system:serviceaccount:%s:%s:scc:privileged", namespace, manifests.VPNServiceAccount(namespace).Name),
		},
	}
	_, err := controllerutil.CreateOrUpdate(context.TODO(), c, sccBinding, func() error {
		sccBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "system:openshift:scc:privileged",
		}
		sccBinding.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      manifests.VPNServiceAccount(namespace).Name,
				Namespace: namespace,
			},
		}
		sccBinding.OwnerReferences = ensureHCPOwnerRef(hcp, sccBinding.OwnerReferences)
		return nil
	})
	return err
}

func deleteManifests(ctx context.Context, c client.Client, log logr.Logger, namespace string, manifests map[string][]byte) error {
	// Use server side apply for manifestss
	applyErrors := []error{}
	for manifestName, manifestBytes := range manifests {
		if excludeManifests.Has(manifestName) {
			continue
		}
		obj := &unstructured.Unstructured{}
		if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifestBytes), 100).Decode(obj); err != nil {
			applyErrors = append(applyErrors, fmt.Errorf("failed to decode manifest %s: %w", manifestName, err))
		}
		obj.SetNamespace(namespace)
		err := c.Delete(ctx, obj)
		if err != nil && !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err) {
			applyErrors = append(applyErrors, fmt.Errorf("failed to delete manifest %s: %w", manifestName, err))
		} else {
			log.Info("deleted manifest", "manifest", manifestName)
		}
	}
	if errs := errors.NewAggregate(applyErrors); errs != nil {
		return fmt.Errorf("failed to delete some manifests: %w", errs)
	}
	return nil
}

func clusterBaseDomain(c client.Client, ctx context.Context, hcp *hyperv1.HostedControlPlane) (string, error) {
	return fmt.Sprintf("%s.%s", hcp.Name, hcp.Spec.DNS.BaseDomain), nil
}

func ensureHCPOwnerRef(hcp *hyperv1.HostedControlPlane, ownerReferences []metav1.OwnerReference) []metav1.OwnerReference {
	return util.EnsureOwnerRef(ownerReferences, metav1.OwnerReference{
		APIVersion: hyperv1.GroupVersion.String(),
		Kind:       "HostedControlPlane",
		Name:       hcp.GetName(),
		UID:        hcp.UID,
	})
}

func generateTargetPullSecret(scheme *runtime.Scheme, data []byte, namespace string) (*corev1.ConfigMap, error) {
	secret := &corev1.Secret{}
	secret.Name = "pull-secret"
	secret.Namespace = "openshift-config"
	secret.Data = map[string][]byte{".dockerconfigjson": data}
	secret.Type = corev1.SecretTypeDockerConfigJson
	secretBytes, err := runtime.Encode(serializer.NewCodecFactory(scheme).LegacyCodec(corev1.SchemeGroupVersion), secret)
	if err != nil {
		return nil, err
	}
	configMap := &corev1.ConfigMap{}
	configMap.Namespace = namespace
	configMap.Name = "user-manifest-pullsecret"
	configMap.Data = map[string]string{"data": string(secretBytes)}
	return configMap, nil
}

const awsCredentialsTemplate = `[default]
role_arn = %s
web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token
`

func generateTargetCredentialsSecret(scheme *runtime.Scheme, creds hyperv1.AWSRoleCredentials, namespace string) (*corev1.ConfigMap, error) {
	secret := &corev1.Secret{}
	secret.Name = creds.Name
	secret.Namespace = creds.Namespace
	credentials := fmt.Sprintf(awsCredentialsTemplate, creds.ARN)
	secret.Data = map[string][]byte{"credentials": []byte(credentials)}
	secret.Type = corev1.SecretTypeOpaque
	secretBytes, err := runtime.Encode(serializer.NewCodecFactory(scheme).LegacyCodec(corev1.SchemeGroupVersion), secret)
	if err != nil {
		return nil, err
	}
	configMap := &corev1.ConfigMap{}
	configMap.Namespace = namespace
	configMap.Name = fmt.Sprintf("user-manifest-%s-%s", creds.Namespace, creds.Name)
	configMap.Data = map[string]string{"data": string(secretBytes)}
	return configMap, nil
}

func applyManifests(ctx context.Context, c client.Client, log logr.Logger, namespace string, manifests map[string][]byte) error {
	// Use server side apply for manifestss
	applyErrors := []error{}
	for manifestName, manifestBytes := range manifests {
		if excludeManifests.Has(manifestName) {
			continue
		}
		obj := &unstructured.Unstructured{}
		if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifestBytes), 100).Decode(obj); err != nil {
			applyErrors = append(applyErrors, fmt.Errorf("failed to decode manifest %s: %w", manifestName, err))
		}
		obj.SetNamespace(namespace)
		err := c.Patch(ctx, obj, client.RawPatch(types.ApplyPatchType, manifestBytes), client.ForceOwnership, client.FieldOwner("control-plane-operator"))
		if err != nil {
			applyErrors = append(applyErrors, fmt.Errorf("failed to apply manifest %s: %w", manifestName, err))
		} else {
			log.Info("applied manifest", "manifest", manifestName)
		}
	}
	if errs := errors.NewAggregate(applyErrors); errs != nil {
		return fmt.Errorf("failed to apply some manifests: %w", errs)
	}
	return nil
}

func generateKubeadminPassword() (string, error) {
	const (
		lowerLetters = "abcdefghijkmnopqrstuvwxyz"
		upperLetters = "ABCDEFGHIJKLMNPQRSTUVWXYZ"
		digits       = "23456789"
		all          = lowerLetters + upperLetters + digits
		length       = 23
	)
	var password string
	for i := 0; i < length; i++ {
		n, err := crand.Int(crand.Reader, big.NewInt(int64(len(all))))
		if err != nil {
			return "", err
		}
		newchar := string(all[n.Int64()])
		if password == "" {
			password = newchar
		}
		if i < length-1 {
			n, err = crand.Int(crand.Reader, big.NewInt(int64(len(password)+1)))
			if err != nil {
				return "", err
			}
			j := n.Int64()
			password = password[0:j] + newchar + password[j:]
		}
	}
	pw := []rune(password)
	for _, replace := range []int{5, 11, 17} {
		pw[replace] = '-'
	}
	return string(pw), nil
}

func generateKubeadminPasswordTargetSecret(scheme *runtime.Scheme, password string, namespace string) (*corev1.ConfigMap, error) {
	secret := &corev1.Secret{}
	secret.APIVersion = "v1"
	secret.Kind = "Secret"
	secret.Name = "kubeadmin"
	secret.Namespace = "kube-system"
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	secret.Data = map[string][]byte{"kubeadmin": passwordHash}

	secretBytes, err := runtime.Encode(serializer.NewCodecFactory(scheme).LegacyCodec(corev1.SchemeGroupVersion), secret)
	if err != nil {
		return nil, err
	}
	configMap := &corev1.ConfigMap{}
	configMap.Namespace = namespace
	configMap.Name = "user-manifest-kubeadmin-password"
	configMap.Data = map[string]string{"data": string(secretBytes)}
	return configMap, nil
}

func generateKubeadminPasswordSecret(namespace, password string) *corev1.Secret {
	secret := &corev1.Secret{}
	secret.Namespace = namespace
	secret.Name = "kubeadmin-password"
	secret.Data = map[string][]byte{"password": []byte(password)}
	return secret
}

func generateKubeconfigSecret(namespace string, ref *hyperv1.KubeconfigSecretRef, kubeconfigBytes []byte) (*corev1.Secret, error) {
	var name, key string
	if ref != nil {
		name = ref.Name
		key = ref.Key
	} else {
		name = DefaultAdminKubeconfigName
		key = DefaultAdminKubeconfigKey
	}
	secret := &corev1.Secret{}
	secret.Namespace = namespace
	secret.Name = name
	secret.Data = map[string][]byte{key: kubeconfigBytes}
	return secret, nil
}

func generateImageRegistrySecret() string {
	num := make([]byte, 64)
	rand.Read(num)
	return hex.EncodeToString(num)
}

func platformType(hcp *hyperv1.HostedControlPlane) string {
	switch {
	case hcp.Spec.Platform.AWS != nil:
		return "AWS"
	case hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform:
		return "IBMCloud"
	default:
		return "None"
	}
}

func cloudProvider(hcp *hyperv1.HostedControlPlane) string {
	switch {
	case hcp.Spec.Platform.AWS != nil:
		return "aws"
	case hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform:
		return "external"
	default:
		return ""
	}
}
