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
	"time"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/konnectivity"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"

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
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/etcd"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ingress"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kcm"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/oapi"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/oauth"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/render"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/scheduler"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/vpn"
	"github.com/openshift/hypershift/control-plane-operator/releaseinfo"
	etcdv1 "github.com/openshift/hypershift/thirdparty/etcd/v1beta2"
)

const (
	finalizer                  = "hypershift.openshift.io/finalizer"
	DefaultAdminKubeconfigName = "admin-kubeconfig"
	DefaultAdminKubeconfigKey  = "kubeconfig"
	oauthBrandingManifest      = "v4-0-config-system-branding.yaml"
	DefaultAPIServerIPAddress  = "172.20.0.1"
)

var (
	excludeManifests = sets.NewString(
		"openshift-apiserver-service.yaml",
		"v4-0-config-system-branding.yaml",
		"oauth-server-service.yaml",
		"kube-apiserver-service.yaml",
	)
)

type InfrastructureStatus struct {
	APIHost                 string
	APIPort                 int32
	OAuthHost               string
	OAuthPort               int32
	VPNHost                 string
	VPNPort                 int32
	KonnectivityHost        string
	KonnectivityPort        int32
	OpenShiftAPIHost        string
	OauthAPIServerHost      string
	PackageServerAPIAddress string
}

func (s InfrastructureStatus) IsReady() bool {
	return len(s.APIHost) > 0 &&
		len(s.OAuthHost) > 0 &&
		len(s.VPNHost) > 0 &&
		len(s.KonnectivityHost) > 0 &&
		s.APIPort > 0 &&
		s.OAuthPort > 0 &&
		s.VPNPort > 0 &&
		s.KonnectivityPort > 0
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
		Watches(&source.Kind{Type: &corev1.Service{}}, &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}).
		Watches(&source.Kind{Type: &appsv1.Deployment{}}, &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}).
		Build(r)
	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager %w", err)
	}

	r.recorder = mgr.GetEventRecorderFor("hosted-control-plane-controller")

	return nil
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

	// Ensure the hostedControlPlane has a finalizer for cleanup
	if !controllerutil.ContainsFinalizer(hostedControlPlane, finalizer) {
		controllerutil.AddFinalizer(hostedControlPlane, finalizer)
		if err := r.Update(ctx, hostedControlPlane); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to hostedControlPlane: %w", err)
		}
	}

	// Reconcile etcd cluster status
	{
		newCondition := metav1.Condition{
			Type:   string(hyperv1.EtcdAvailable),
			Status: metav1.ConditionUnknown,
			Reason: "EtcdStatusUnknown",
		}
		switch hostedControlPlane.Spec.Etcd.ManagementType {
		case hyperv1.Managed:
			r.Log.Info("Reconciling etcd cluster status for managed strategy")
			etcdCluster := manifests.EtcdCluster(hostedControlPlane.Namespace)
			if err := r.Get(ctx, types.NamespacedName{Namespace: etcdCluster.Namespace, Name: etcdCluster.Name}, etcdCluster); err != nil {
				if apierrors.IsNotFound(err) {
					newCondition = metav1.Condition{
						Type:   string(hyperv1.EtcdAvailable),
						Status: metav1.ConditionFalse,
						Reason: "EtcdClusterNotFound",
					}
				} else {
					return ctrl.Result{}, fmt.Errorf("failed to fetch etcd cluster %s/%s: %w", etcdCluster.Namespace, etcdCluster.Name, err)
				}
			} else {
				r.Log.Info("Computing proper etcd cluster status based on current state of etcd cluster")
				cond, err := etcd.ComputeEtcdClusterStatus(ctx, r.Client, etcdCluster)
				if err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to compute etcd cluster status: %w", err)
				}
				newCondition = cond
			}
		case hyperv1.Unmanaged:
			r.Log.Info("Assuming Etcd cluster is running in unmanaged etcd strategy")
			newCondition = metav1.Condition{
				Type:    string(hyperv1.EtcdAvailable),
				Status:  metav1.ConditionTrue,
				Reason:  etcd.EtcdReasonRunning,
				Message: "Etcd cluster is assumed to be running in unmanaged state",
			}
		}
		newCondition.ObservedGeneration = hostedControlPlane.Generation
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, newCondition)
	}

	// Reconcile Kube APIServer status
	{
		newCondition := metav1.Condition{
			Type:   string(hyperv1.KubeAPIServerAvailable),
			Status: metav1.ConditionUnknown,
			Reason: "StatusUnknown",
		}
		deployment := manifests.KASDeployment(hostedControlPlane.Namespace)
		if err := r.Get(ctx, client.ObjectKeyFromObject(deployment), deployment); err != nil {
			if apierrors.IsNotFound(err) {
				newCondition = metav1.Condition{
					Type:   string(hyperv1.KubeAPIServerAvailable),
					Status: metav1.ConditionFalse,
					Reason: "DeploymentNotFound",
				}
			} else {
				return ctrl.Result{}, fmt.Errorf("failed to fetch Kube APIServer deployment %s/%s: %w", deployment.Namespace, deployment.Name, err)
			}
		} else {
			// Assume the deployment is unavailable until proven otherwise.
			newCondition = metav1.Condition{
				Type:   string(hyperv1.KubeAPIServerAvailable),
				Status: metav1.ConditionFalse,
				Reason: "DeploymentStatusUnknown",
			}
			for _, cond := range deployment.Status.Conditions {
				if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
					newCondition = metav1.Condition{
						Type:   string(hyperv1.KubeAPIServerAvailable),
						Status: metav1.ConditionTrue,
						Reason: "AsExpected",
					}
					break
				}
			}
		}
		newCondition.ObservedGeneration = hostedControlPlane.Generation
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, newCondition)
	}

	// Reconcile hostedcontrolplane availability and Ready flag
	{
		newCondition := metav1.Condition{
			Type:   string(hyperv1.HostedControlPlaneAvailable),
			Status: metav1.ConditionUnknown,
			Reason: "StatusUnknown",
		}
		if meta.IsStatusConditionPresentAndEqual(hostedControlPlane.Status.Conditions, string(hyperv1.KubeAPIServerAvailable), metav1.ConditionTrue) &&
			meta.IsStatusConditionPresentAndEqual(hostedControlPlane.Status.Conditions, string(hyperv1.EtcdAvailable), metav1.ConditionTrue) {
			hostedControlPlane.Status.Ready = true
			newCondition = metav1.Condition{
				Type:   string(hyperv1.HostedControlPlaneAvailable),
				Status: metav1.ConditionTrue,
				Reason: "AsExpected",
			}
		} else {
			hostedControlPlane.Status.Ready = false
			newCondition = metav1.Condition{
				Type:    string(hyperv1.HostedControlPlaneAvailable),
				Status:  metav1.ConditionFalse,
				Reason:  "ComponentsUnavailable",
				Message: "Not all dependent components are available yet",
			}
		}
		newCondition.ObservedGeneration = hostedControlPlane.Generation
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, newCondition)
	}

	{
		r.Log.Info("Reconciling infrastructure status")
		newCondition := metav1.Condition{
			Type:   string(hyperv1.InfrastructureReady),
			Status: metav1.ConditionUnknown,
			Reason: "StatusUnknown",
		}
		infraStatus, err := r.reconcileInfrastructureStatus(ctx, hostedControlPlane)
		if err != nil {
			newCondition = metav1.Condition{
				Type:    string(hyperv1.InfrastructureReady),
				Status:  metav1.ConditionUnknown,
				Reason:  "InfraStatusFailure",
				Message: err.Error(),
			}
			r.Log.Error(err, "failed to determine infrastructure status")
		} else {
			if infraStatus.IsReady() {
				hostedControlPlane.Status.ControlPlaneEndpoint = hyperv1.APIEndpoint{
					Host: infraStatus.APIHost,
					Port: infraStatus.APIPort,
				}
				newCondition = metav1.Condition{
					Type:   string(hyperv1.InfrastructureReady),
					Status: metav1.ConditionTrue,
					Reason: "AsExpected",
				}
			} else {
				newCondition = metav1.Condition{
					Type:    string(hyperv1.InfrastructureReady),
					Status:  metav1.ConditionFalse,
					Reason:  "WaitingOnInfrastructureReady",
					Message: "Cluster infrastructure is still provisioning",
				}
				r.Log.Info("Infrastructure is not yet ready")
			}
		}
		newCondition.ObservedGeneration = hostedControlPlane.Generation
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, newCondition)
	}

	if hostedControlPlane.Status.Version == "" {
		lookupCtx, lookupCancel := context.WithTimeout(ctx, 2*time.Minute)
		defer lookupCancel()
		releaseImage, err := r.ReleaseProvider.Lookup(lookupCtx, hostedControlPlane.Spec.ReleaseImage)
		if err != nil {
			r.Log.Error(err, "failed to look up release image metadata")
		} else {
			hostedControlPlane.Status.Version = releaseImage.Version()
		}
	}

	if hostedControlPlane.Spec.KubeConfig != nil {
		hostedControlPlane.Status.KubeConfig = hostedControlPlane.Spec.KubeConfig
	} else {
		hostedControlPlane.Status.KubeConfig = &hyperv1.KubeconfigSecretRef{
			Name: DefaultAdminKubeconfigName,
			Key:  DefaultAdminKubeconfigKey,
		}
	}
	hostedControlPlane.Status.Initialized = true

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

	// Always update status based on the current state of the world.
	if err := r.Client.Status().Update(ctx, hostedControlPlane); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	// Perform the hosted control plane reconciliation
	if err := r.update(ctx, hostedControlPlane); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update control plane: %w", err)
	}

	r.Log.Info("Successfully reconciled")
	return ctrl.Result{}, nil
}

func (r *HostedControlPlaneReconciler) update(ctx context.Context, hostedControlPlane *hyperv1.HostedControlPlane) error {
	// If the cluster is marked paused, don't do any reconciliation work at all.
	if cluster, err := util.GetOwnerCluster(ctx, r.Client, hostedControlPlane.ObjectMeta); err != nil {
		return fmt.Errorf("failed to get owner cluster: %w", err)
	} else {
		if cluster == nil {
			r.Log.Info("Cluster Controller has not yet set OwnerRef")
			return nil
		}
		if util.IsPaused(cluster, hostedControlPlane) {
			r.Log.Info("HostedControlPlane or linked Cluster is marked as paused. Won't reconcile")
			return nil
		}
	}

	r.Log.Info("Looking up release image metadata", "image", hostedControlPlane.Spec.ReleaseImage)
	lookupCtx, lookupCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer lookupCancel()
	releaseImage, err := r.ReleaseProvider.Lookup(lookupCtx, hostedControlPlane.Spec.ReleaseImage)
	if err != nil {
		return fmt.Errorf("failed to look up release image metadata: %w", err)
	}
	componentVersions, err := releaseImage.ComponentVersions()
	if err != nil {
		return fmt.Errorf("invalid component versions found in release info: %w", err)
	}
	r.Log.Info("Found release info for image", "releaseImage", hostedControlPlane.Spec.ReleaseImage, "info", releaseImage, "componentImages", len(releaseImage.ComponentImages()), "componentVersions", componentVersions)

	// During an upgrade, if there's an old bootstrapper pod referring to the old
	// image, delete the pod to make way for the new one to be rendered. This is
	// a hack to avoid the refactoring of moving this pod into the hosted cluster
	// config operator.
	if hostedControlPlane.Spec.ReleaseImage != hostedControlPlane.Status.ReleaseImage {
		var bootstrapPod corev1.Pod
		err := r.Client.Get(ctx, types.NamespacedName{Namespace: hostedControlPlane.Namespace, Name: "manifests-bootstrapper"}, &bootstrapPod)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to get manifests bootstrapper pod: %w", err)
			}
		} else {
			currentImage := bootstrapPod.Spec.Containers[0].Image
			latestImage, latestImageFound := releaseImage.ComponentImages()["cli"]
			if latestImageFound && currentImage != latestImage {
				err := r.Client.Delete(ctx, &bootstrapPod)
				if err != nil {
					return fmt.Errorf("failed to delete manifests bootstrapper pod: %w", err)
				}
				r.Log.Info("deleted manifests bootstrapper pod as part of an image rollout", "pod", bootstrapPod.Name, "from", currentImage, "to", latestImage)
			}
		}
	}

	r.Log.Info("Reconciling infrastructure services")
	if err := r.reconcileInfrastructure(ctx, hostedControlPlane); err != nil {
		return fmt.Errorf("failed to ensure infrastructure: %w", err)
	}
	// Block here until infra status reports readiness
	// TODO(dmace): This seems a bit heavy handed vs. making more granular bits no-op if
	// they don't have the specific required inputs
	infraStatus, err := r.reconcileInfrastructureStatus(ctx, hostedControlPlane)
	if err != nil {
		return fmt.Errorf("failed to look up infra status: %w", err)
	}
	if !infraStatus.IsReady() {
		r.Log.Info("Waiting for infrastructure to be ready before proceeding")
		return nil
	}

	// Reconcile PKI
	if _, exists := hostedControlPlane.Annotations[hyperv1.DisablePKIReconciliationAnnotation]; !exists {
		r.Log.Info("Reconciling PKI")
		if err := r.reconcilePKI(ctx, hostedControlPlane, infraStatus); err != nil {
			return fmt.Errorf("failed to reconcile PKI: %w", err)
		}
	}

	// Reconcile Cloud Provider Config
	r.Log.Info("Reconciling cloud provider config")
	if err := r.reconcileCloudProviderConfig(ctx, hostedControlPlane); err != nil {
		return fmt.Errorf("failed to reconcile cloud provider config: %w", err)
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
				return fmt.Errorf("failed to reconcile OIDC route: %w", err)
			}
		case hyperv1.None:
			r.Log.Info("OIDC Route is disabled")
		}
	}

	// Reconcile etcd
	r.Log.Info("Reconciling Etcd")

	switch hostedControlPlane.Spec.Etcd.ManagementType {
	case hyperv1.Managed:
		if err := r.reconcileManagedEtcd(ctx, hostedControlPlane, releaseImage); err != nil {
			return fmt.Errorf("failed to reconcile etcd: %w", err)
		}
	case hyperv1.Unmanaged:
		if err := r.reconcileUnmanagedEtcd(ctx, hostedControlPlane); err != nil {
			return fmt.Errorf("failed to reconcile etcd: %w", err)
		}
	default:
		return fmt.Errorf("unrecognized etcd management type: %s", hostedControlPlane.Spec.Etcd.ManagementType)
	}

	// Reconcile VPN
	r.Log.Info("Reconciling VPN")
	if err := r.reconcileVPN(ctx, hostedControlPlane, releaseImage, infraStatus.VPNHost, infraStatus.VPNPort); err != nil {
		return fmt.Errorf("failed to reconcile vpn: %w", err)
	}

	// Reconcile Konnectivity
	r.Log.Info("Reconciling Konnectivity")
	if err := r.reconcileKonnectivity(ctx, hostedControlPlane, releaseImage, infraStatus); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity: %w", err)
	}

	// Reconcile kube apiserver
	r.Log.Info("Reconciling Kube API Server")
	if err := r.reconcileKubeAPIServer(ctx, hostedControlPlane, releaseImage, infraStatus.OAuthHost, infraStatus.OAuthPort); err != nil {
		return fmt.Errorf("failed to reconcile kube apiserver: %w", err)
	}

	// Reconcile kube controller manager
	r.Log.Info("Reconciling Kube Controller Manager")
	if err := r.reconcileKubeControllerManager(ctx, hostedControlPlane, releaseImage); err != nil {
		return fmt.Errorf("failed to reconcile kube controller manager: %w", err)
	}

	// Reconcile kube scheduler
	r.Log.Info("Reconciling Kube Scheduler")
	if err := r.reconcileKubeScheduler(ctx, hostedControlPlane, releaseImage); err != nil {
		return fmt.Errorf("failed to reconcile kube controller manager: %w", err)
	}

	// Reconcile openshift apiserver
	r.Log.Info("Reconciling OpenShift API Server")
	if err := r.reconcileOpenShiftAPIServer(ctx, hostedControlPlane, releaseImage, infraStatus.OpenShiftAPIHost); err != nil {
		return fmt.Errorf("failed to reconcile openshift apiserver: %w", err)
	}

	// Reconcile openshift oauth apiserver
	r.Log.Info("Reconciling OpenShift OAuth API Server")
	if err := r.reconcileOpenShiftOAuthAPIServer(ctx, hostedControlPlane, releaseImage, infraStatus.OauthAPIServerHost); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver: %w", err)
	}

	r.Log.Info("Reconciling default ingress controller")
	if err = r.reconcileDefaultIngressController(ctx, hostedControlPlane); err != nil {
		return fmt.Errorf("failed to reconcile default ingress controller: %w", err)
	}

	// Reconcile oauth server
	r.Log.Info("Reconciling OAuth Server")
	if err = r.reconcileOAuthServer(ctx, hostedControlPlane, releaseImage, infraStatus.OAuthHost, infraStatus.OAuthPort); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver: %w", err)
	}

	// Install the control plane into the infrastructure
	r.Log.Info("Reconciling hosted control plane")
	if err := r.ensureControlPlane(ctx, hostedControlPlane, infraStatus, releaseImage); err != nil {
		return fmt.Errorf("failed to ensure control plane: %w", err)
	}
	return nil
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
		return vpn.ReconcileService(vpnServerService, p.OwnerRef, serviceStrategy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile VPN service: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileKonnectivityServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	p := konnectivity.NewKonnectivityServiceParams(hcp)
	serviceStrategy := servicePublishingStrategyByType(hcp, hyperv1.Konnectivity)
	if serviceStrategy == nil {
		return fmt.Errorf("Konnectivity service strategy not specified")
	}
	konnectivityServerService := manifests.KonnectivityServerService(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, konnectivityServerService, func() error {
		return konnectivity.ReconcileServerService(konnectivityServerService, p.OwnerRef, serviceStrategy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile Konnectivity service: %w", err)
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
		return oauth.ReconcileService(oauthServerService, p.OwnerRef, serviceStrategy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile OAuth service: %w", err)
	}
	if serviceStrategy.Type != hyperv1.Route {
		return nil
	}
	oauthRoute := manifests.OauthServerRoute(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, oauthRoute, func() error {
		return oauth.ReconcileRoute(oauthRoute, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile OAuth route: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOpenshiftAPIServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	svc := manifests.OpenshiftAPIServerService(hcp.Namespace)
	p := oapi.NewOpenShiftAPIServerServiceParams(hcp)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		return oapi.ReconcileOpenShiftAPIService(svc, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile OpenShift API server service: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOAuthAPIServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	svc := manifests.OauthAPIServerService(hcp.Namespace)
	p := oapi.NewOpenShiftAPIServerServiceParams(hcp)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		return oapi.ReconcileOAuthAPIService(svc, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile OAuth API server service: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOLMPackageServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	svc := manifests.OLMPackageServerService(hcp.Namespace)
	p := oapi.NewOpenShiftAPIServerServiceParams(hcp)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		return oapi.ReconcileOLMPackageServerService(svc, p.OwnerRef)
	})
	if err != nil {
		return err
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
	if err := r.reconcileKonnectivityServerService(ctx, hcp); err != nil {
		return fmt.Errorf("failed to reconcile Konnectivity servier service: %w", err)
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
	if err := r.reconcileOLMPackageServerService(ctx, hcp); err != nil {
		return fmt.Errorf("failed to reconcile OLM PackageServer service: %w", err)
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
	if infraStatus.KonnectivityHost, infraStatus.KonnectivityPort, err = r.reconcileKonnectivityServiceStatus(ctx, hcp); err != nil {
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
	if infraStatus.PackageServerAPIAddress, err = r.reconcileOLMPackageServerServiceStatus(ctx, hcp); err != nil {
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
	return vpn.ReconcileServiceStatus(svc, serviceStrategy)
}

func (r *HostedControlPlaneReconciler) reconcileKonnectivityServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (host string, port int32, err error) {
	serviceStrategy := servicePublishingStrategyByType(hcp, hyperv1.Konnectivity)
	if serviceStrategy == nil {
		err = fmt.Errorf("Konnectivity service strategy not specified")
		return
	}
	svc := manifests.KonnectivityServerService(hcp.Namespace)
	if err = r.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		if apierrors.IsNotFound(err) {
			err = nil
			return
		}
		err = fmt.Errorf("failed to get konnectivity service: %w", err)
		return
	}
	return konnectivity.ReconcileServerServiceStatus(svc, serviceStrategy)
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
	return oauth.ReconcileServiceStatus(svc, route, serviceStrategy)
}

func (r *HostedControlPlaneReconciler) reconcileOpenShiftAPIServerServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (string, error) {
	svc := manifests.OpenshiftAPIServerService(hcp.Namespace)
	return r.reconcileClusterIPServiceStatus(ctx, svc)
}

func (r *HostedControlPlaneReconciler) reconcileOAuthAPIServerServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (string, error) {
	svc := manifests.OauthAPIServerService(hcp.Namespace)
	return r.reconcileClusterIPServiceStatus(ctx, svc)
}

func (r *HostedControlPlaneReconciler) reconcileOLMPackageServerServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (string, error) {
	svc := manifests.OLMPackageServerService(hcp.Namespace)
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
	p := pki.NewPKIParams(hcp, infraStatus.APIHost, infraStatus.OAuthHost, infraStatus.VPNHost, infraStatus.KonnectivityHost)

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

	// Konnectivity Server Cert
	konnectivityServerSecret := manifests.KonnectivityServerSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, konnectivityServerSecret, func() error {
		return p.ReconcileKonnectivityServerSecret(konnectivityServerSecret, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity server cert: %w", err)
	}

	// Konnectivity Cluster Cert
	konnectivityClusterSecret := manifests.KonnectivityClusterSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, konnectivityClusterSecret, func() error {
		return p.ReconcileKonnectivityClusterSecret(konnectivityClusterSecret, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity cluster cert: %w", err)
	}

	// Konnectivity Client Cert
	konnectivityClientSecret := manifests.KonnectivityClientSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, konnectivityClientSecret, func() error {
		return p.ReconcileKonnectivityClientSecret(konnectivityClientSecret, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity client cert: %w", err)
	}

	// Konnectivity Agent Cert
	konnectivityAgentSecret := manifests.KonnectivityAgentSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, konnectivityAgentSecret, func() error {
		return p.ReconcileKonnectivityAgentSecret(konnectivityAgentSecret, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity agent cert: %w", err)
	}

	// Konnectivity Worker Agent Cert
	konnectivityWorkerAgentSecret := manifests.KonnectivityWorkerAgentSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, konnectivityWorkerAgentSecret, func() error {
		return p.ReconcileKonnectivityWorkerAgentSecret(konnectivityWorkerAgentSecret, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity worker agent cert: %w", err)
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

	// OLM PackageServer Cert
	packageServerCertSecret := manifests.OLMPackageServerCertSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, packageServerCertSecret, func() error {
		return p.ReconcileOLMPackageServerCertSecret(packageServerCertSecret, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile packageserver cert: %w", err)
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

func (r *HostedControlPlaneReconciler) reconcileManagedEtcd(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage) error {
	p := etcd.NewEtcdParams(hcp, releaseImage.ComponentImages())

	// Etcd Operator ServiceAccount
	operatorServiceAccount := manifests.EtcdOperatorServiceAccount(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, operatorServiceAccount, func() error {
		return etcd.ReconcileOperatorServiceAccount(operatorServiceAccount, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd operator service account: %w", err)
	}

	// Etcd operator role
	operatorRole := manifests.EtcdOperatorRole(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, operatorRole, func() error {
		operatorRole.OwnerReferences = ensureHCPOwnerRef(hcp, operatorRole.OwnerReferences)
		return etcd.ReconcileOperatorRole(operatorRole, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd operator role: %w", err)
	}

	// Etcd operator rolebinding
	operatorRoleBinding := manifests.EtcdOperatorRoleBinding(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, operatorRoleBinding, func() error {
		operatorRoleBinding.OwnerReferences = ensureHCPOwnerRef(hcp, operatorRoleBinding.OwnerReferences)
		return etcd.ReconcileOperatorRoleBinding(operatorRoleBinding, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd operator role binding: %w", err)
	}

	// Etcd operator deployment
	operatorDeployment := manifests.EtcdOperatorDeployment(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, operatorDeployment, func() error {
		operatorDeployment.OwnerReferences = ensureHCPOwnerRef(hcp, operatorDeployment.OwnerReferences)
		return etcd.ReconcileOperatorDeployment(operatorDeployment, p.OwnerRef, p.OperatorDeploymentConfig, p.EtcdOperatorImage)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd operator deployment: %w", err)
	}

	// Etcd cluster
	etcdCluster := manifests.EtcdCluster(hcp.Namespace)

	// The EtcdCluster can currently enter a permanently failed state, so when
	// that's detected, delete the EtcdCluster and start over.
	// TODO(dmace): Fix this in the etcd operator and delete this code
	shouldDeleteFailedEtcd := false
	for _, cond := range hcp.Status.Conditions {
		if cond.Type == string(hyperv1.EtcdAvailable) && cond.Status == metav1.ConditionFalse && cond.Reason == etcd.EtcdReasonFailed {
			shouldDeleteFailedEtcd = true
			break
		}
	}
	if shouldDeleteFailedEtcd {
		if err := r.Delete(ctx, etcdCluster); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete etcd cluster: %w", err)
			}
		}
	}

	if _, err := controllerutil.CreateOrUpdate(ctx, r, etcdCluster, func() error {
		etcdCluster.OwnerReferences = ensureHCPOwnerRef(hcp, etcdCluster.OwnerReferences)
		return etcd.ReconcileCluster(etcdCluster, p.OwnerRef, p.EtcdDeploymentConfig, p.ClusterVersion, p.PVCClaim)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd cluster: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileUnmanagedEtcd(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	//reconcile client secret over
	if hcp.Spec.Etcd.Unmanaged == nil || len(hcp.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name) == 0 || len(hcp.Spec.Etcd.Unmanaged.Endpoint) == 0 {
		return fmt.Errorf("etcd metadata not specified for unmanaged deployment")
	}
	r.Log.Info("Retrieving tls secret", "name", hcp.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name)
	var src corev1.Secret
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcp.GetNamespace(), Name: hcp.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name}, &src); err != nil {
		return fmt.Errorf("failed to get etcd client cert %s: %w", hcp.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name, err)
	}
	if _, ok := src.Data["etcd-client.crt"]; !ok {
		return fmt.Errorf("etcd secret %s does not have client cert", hcp.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name)
	}
	if _, ok := src.Data["etcd-client.key"]; !ok {
		return fmt.Errorf("etcd secret %s does not have client key", hcp.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name)
	}
	if _, ok := src.Data["etcd-client-ca.crt"]; !ok {
		return fmt.Errorf("etcd secret %s does not have client ca", hcp.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name)
	}
	kubeComponentEtcdClientSecret := manifests.EtcdClientSecret(hcp.GetNamespace())
	r.Log.Info("Reconciling openshift control plane etcd client tls secret", "name", kubeComponentEtcdClientSecret.Name)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, kubeComponentEtcdClientSecret, func() error {
		if kubeComponentEtcdClientSecret.Data == nil {
			kubeComponentEtcdClientSecret.Data = map[string][]byte{}
		}
		kubeComponentEtcdClientSecret.Data = src.Data
		kubeComponentEtcdClientSecret.Type = corev1.SecretTypeOpaque
		return nil
	})
	return err
}

func (r *HostedControlPlaneReconciler) reconcileVPN(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, address string, port int32) error {
	r.Log.Info("Reconciling VPN")
	p := vpn.NewVPNParams(hcp, releaseImage.ComponentImages(), address, port)
	serviceAccount := manifests.VPNServiceAccount(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, serviceAccount, func() error {
		return vpn.ReconcileVPNServiceAccount(serviceAccount, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile vpn service account: %w", err)
	}
	serverConfig := manifests.VPNServerConfig(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, serverConfig, func() error {
		return vpn.ReconcileVPNServerConfig(serverConfig, p.OwnerRef, config.ClusterCIDR(&p.Network), config.ServiceCIDR(&p.Network), p.MachineCIDR)
	}); err != nil {
		return fmt.Errorf("failed to reconcile vpn server config: %w", err)
	}
	serverClientConfig := manifests.VPNServerClientConfig(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, serverClientConfig, func() error {
		return vpn.ReconcileVPNServerClientConfig(serverClientConfig, p.OwnerRef, config.ClusterCIDR(&p.Network), config.ServiceCIDR(&p.Network), p.MachineCIDR)
	}); err != nil {
		return fmt.Errorf("failed to reconcile vpn server client config: %w", err)
	}
	kubeAPIServerConfig := manifests.VPNKubeAPIServerClientConfig(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, kubeAPIServerConfig, func() error {
		return vpn.ReconcileKubeAPIServerClientConfig(kubeAPIServerConfig, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile vpn kas client config: %w", err)
	}
	clientConfig := manifests.VPNWorkerClientConfig(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, clientConfig, func() error {
		return vpn.ReconcileWorkerClientConfig(clientConfig, p.OwnerRef, p.ExternalAddress, p.ExternalPort)
	}); err != nil {
		return fmt.Errorf("failed to reconcile vpn worker client config: %w", err)
	}
	serverDeployment := manifests.VPNServerDeployment(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, serverDeployment, func() error {
		return vpn.ReconcileServerDeployment(serverDeployment, p.OwnerRef, p.ServerDeploymentConfig, p.VPNImage)
	}); err != nil {
		return fmt.Errorf("failed to reconcile vpn server deployment: %w", err)
	}
	clientDeployment := manifests.VPNWorkerClientDeployment(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, clientDeployment, func() error {
		return vpn.ReconcileWorkerClientDeployment(clientDeployment, p.OwnerRef, p.WorkerClientDeploymentConfig, p.VPNImage)
	}); err != nil {
		return fmt.Errorf("failed to reconcile vpn client deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileKonnectivity(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, infraStatus InfrastructureStatus) error {
	r.Log.Info("Reconciling Konnectivity")
	p := konnectivity.NewKonnectivityParams(hcp, releaseImage.ComponentImages(), infraStatus.KonnectivityHost, infraStatus.KonnectivityPort)
	serverDeployment := manifests.KonnectivityServerDeployment(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, serverDeployment, func() error {
		return konnectivity.ReconcileServerDeployment(serverDeployment, p.OwnerRef, p.ServerDeploymentConfig, p.KonnectivityServerImage)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity server deployment: %w", err)
	}
	serverLocalService := manifests.KonnectivityServerLocalService(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, serverLocalService, func() error {
		return konnectivity.ReconcileServerLocalService(serverLocalService, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity server local service: %w", err)
	}
	agentDeployment := manifests.KonnectivityAgentDeployment(hcp.Namespace)
	ips := []string{
		infraStatus.OpenShiftAPIHost,
		infraStatus.OauthAPIServerHost,
		infraStatus.PackageServerAPIAddress,
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r, agentDeployment, func() error {
		return konnectivity.ReconcileAgentDeployment(agentDeployment, p.OwnerRef, p.AgentDeploymentConfig, p.KonnectivityAgentImage, ips)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity agent deployment: %w", err)
	}
	agentDaemonSet := manifests.KonnectivityWorkerAgentDaemonSet(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, agentDaemonSet, func() error {
		return konnectivity.ReconcileWorkerAgentDaemonSet(agentDaemonSet, p.OwnerRef, p.AgentDeamonSetConfig, p.KonnectivityAgentImage, p.ExternalAddress, p.ExternalPort)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity agent daemonset: %w", err)
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

	kubeAPIServerEgressSelectorConfig := manifests.KASEgressSelectorConfig(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, kubeAPIServerEgressSelectorConfig, func() error {
		return kas.ReconcileEgressSelectorConfig(kubeAPIServerEgressSelectorConfig, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server egress selector config: %w", err)
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
			p.AuditWebhookRef,
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
		return kcm.ReconcileKCMServiceServingCA(serviceServingCA, combinedCA, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kcm serving ca: %w", err)
	}

	kcmConfig := manifests.KCMConfig(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, kcmConfig, func() error {
		return kcm.ReconcileConfig(kcmConfig, serviceServingCA, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kcm config: %w", err)
	}

	kcmDeployment := manifests.KCMDeployment(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, kcmDeployment, func() error {
		return kcm.ReconcileDeployment(kcmDeployment, serviceServingCA, p)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kcm deployment: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileKubeScheduler(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage) error {
	p := scheduler.NewKubeSchedulerParams(hcp, releaseImage.ComponentImages())

	schedulerConfig := manifests.SchedulerConfig(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, schedulerConfig, func() error {
		return scheduler.ReconcileConfig(schedulerConfig, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile scheduler config: %w", err)
	}

	schedulerDeployment := manifests.SchedulerDeployment(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, schedulerDeployment, func() error {
		return scheduler.ReconcileDeployment(schedulerDeployment, p.OwnerRef, p.DeploymentConfig, p.HyperkubeImage, p.FeatureGates())
	}); err != nil {
		return fmt.Errorf("failed to reconcile scheduler deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOpenShiftAPIServer(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, serviceClusterIP string) error {
	p := oapi.NewOpenShiftAPIServerParams(hcp, releaseImage.ComponentImages())

	oapicfg := manifests.OpenShiftAPIServerConfig(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, oapicfg, func() error {
		return oapi.ReconcileConfig(oapicfg, p.OwnerRef, p.EtcdURL, p.IngressDomain(), p.MinTLSVersion(), p.CipherSuites())
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift apiserver config: %w", err)
	}

	auditCfg := manifests.OpenShiftAPIServerAuditConfig(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, auditCfg, func() error {
		return oapi.ReconcileAuditConfig(auditCfg, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift apiserver audit config: %w", err)
	}

	deployment := manifests.OpenShiftAPIServerDeployment(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, deployment, func() error {
		return oapi.ReconcileDeployment(deployment, p.OwnerRef, p.OpenShiftAPIServerDeploymentConfig, p.OpenShiftAPIServerImage)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift apiserver deployment: %w", err)
	}

	workerEndpoints := manifests.OpenShiftAPIServerWorkerEndpoints(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, workerEndpoints, func() error {
		return oapi.ReconcileWorkerEndpoints(workerEndpoints, p.OwnerRef, manifests.OpenShiftAPIServerClusterEndpoints(), serviceClusterIP)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift apiserver endpoints: %w", err)
	}

	workerService := manifests.OpenShiftAPIServerWorkerService(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, workerService, func() error {
		return oapi.ReconcileWorkerService(workerService, p.OwnerRef, manifests.OpenShiftAPIServerClusterService())
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift apiserver worker service: %w", err)
	}

	rootCA := manifests.RootCASecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return fmt.Errorf("failed to get root ca cert secret: %w", err)
	}
	for _, apiSvcGroup := range manifests.OpenShiftAPIServerAPIServiceGroups() {
		workerAPISvc := manifests.OpenShiftAPIServerWorkerAPIService(apiSvcGroup, hcp.Namespace)
		if _, err := controllerutil.CreateOrUpdate(ctx, r, workerAPISvc, func() error {
			return oapi.ReconcileWorkerAPIService(workerAPISvc, p.OwnerRef, manifests.OpenShiftAPIServerClusterService(), rootCA, apiSvcGroup)
		}); err != nil {
			return fmt.Errorf("failed to reconcile openshift apiserver worker apiservice (%s): %w", apiSvcGroup, err)
		}
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOpenShiftOAuthAPIServer(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, serviceClusterIP string) error {
	p := oapi.NewOpenShiftAPIServerParams(hcp, releaseImage.ComponentImages())

	auditCfg := manifests.OpenShiftOAuthAPIServerAuditConfig(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, auditCfg, func() error {
		return oapi.ReconcileAuditConfig(auditCfg, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver audit config: %w", err)
	}

	deployment := manifests.OpenShiftOAuthAPIServerDeployment(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, deployment, func() error {
		return oapi.ReconcileOAuthAPIServerDeployment(deployment, p.OwnerRef, p.OAuthAPIServerDeploymentParams())
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver deployment: %w", err)
	}

	workerEndpoints := manifests.OpenShiftOAuthAPIServerWorkerEndpoints(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, workerEndpoints, func() error {
		return oapi.ReconcileWorkerEndpoints(workerEndpoints, p.OwnerRef, manifests.OpenShiftOAuthAPIServerClusterEndpoints(), serviceClusterIP)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver endpoints: %w", err)
	}

	workerService := manifests.OpenShiftOAuthAPIServerWorkerService(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, workerService, func() error {
		return oapi.ReconcileWorkerService(workerService, p.OwnerRef, manifests.OpenShiftOAuthAPIServerClusterService())
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver worker service: %w", err)
	}

	rootCA := manifests.RootCASecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return fmt.Errorf("failed to get root ca cert secret: %w", err)
	}
	for _, apiSvcGroup := range manifests.OpenShiftOAuthAPIServerAPIServiceGroups() {
		workerAPISvc := manifests.OpenShiftAPIServerWorkerAPIService(apiSvcGroup, hcp.Namespace)
		if _, err := controllerutil.CreateOrUpdate(ctx, r, workerAPISvc, func() error {
			return oapi.ReconcileWorkerAPIService(workerAPISvc, p.OwnerRef, manifests.OpenShiftOAuthAPIServerClusterService(), rootCA, apiSvcGroup)
		}); err != nil {
			return fmt.Errorf("failed to reconcile openshift oauth apiserver worker apiservice (%s): %w", apiSvcGroup, err)
		}
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileDefaultIngressController(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	ingressControllerManifest := manifests.IngressDefaultIngressControllerWorkerManifest(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, ingressControllerManifest, func() error {
		return ingress.ReconcileDefaultIngressControllerWorkerManifest(ingressControllerManifest, config.OwnerRefFrom(hcp), config.IngressSubdomain(hcp), hcp.Spec.Platform.Type)
	}); err != nil {
		return fmt.Errorf("failed to reconcile default ingress controller worker manifest: %w", err)
	}

	ingressServingCert := manifests.IngressCert(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(ingressServingCert), ingressServingCert); err != nil {
		return fmt.Errorf("cannot get ingress serving cert: %w", err)
	}
	ingressControllerCertManifest := manifests.IngressDefaultIngressControllerCertWorkerManifest(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, ingressControllerCertManifest, func() error {
		return ingress.ReconcileDefaultIngressControllerCertWorkerManifest(ingressControllerCertManifest, config.OwnerRefFrom(hcp), ingressServingCert)
	}); err != nil {
		return fmt.Errorf("failed to reconcile default ingress controller cert worker manifest: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOAuthServer(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, oauthHost string, oauthPort int32) error {
	p := oauth.NewOAuthServerParams(hcp, releaseImage.ComponentImages(), oauthHost, oauthPort)

	sessionSecret := manifests.OAuthServerServiceSessionSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, sessionSecret, func() error {
		return oauth.ReconcileSessionSecret(sessionSecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth session secret: %w", err)
	}

	loginTemplate := manifests.OAuthServerDefaultLoginTemplateSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, loginTemplate, func() error {
		return oauth.ReconcileLoginTemplateSecret(loginTemplate, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile login template secret: %w", err)
	}

	providersTemplate := manifests.OAuthServerDefaultProviderSelectionTemplateSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, providersTemplate, func() error {
		return oauth.ReconcileProviderSelectionTemplateSecret(providersTemplate, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile provider selection template secret: %w", err)
	}

	errorTemplate := manifests.OAuthServerDefaultErrorTemplateSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, errorTemplate, func() error {
		return oauth.ReconcileErrorTemplateSecret(errorTemplate, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile error template secret: %w", err)
	}

	ingressServingCert := manifests.IngressCert(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(ingressServingCert), ingressServingCert); err != nil {
		return fmt.Errorf("cannot get ingress serving cert: %w", err)
	}
	oauthConfig := manifests.OAuthServerConfig(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, oauthConfig, func() error {
		return oauth.ReconcileOAuthServerConfig(ctx, oauthConfig, p.OwnerRef, r.Client, p.ConfigParams(ingressServingCert))
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth server config: %w", err)
	}

	deployment := manifests.OAuthServerDeployment(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, deployment, func() error {
		return oauth.ReconcileDeployment(deployment, p.OwnerRef, p.OAuthServerImage, p.DeploymentConfig)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth deployment: %w", err)
	}

	oauthBrowserClient := manifests.OAuthServerBrowserClientManifest(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, oauthBrowserClient, func() error {
		return oauth.ReconcileBrowserClientWorkerManifest(oauthBrowserClient, p.OwnerRef, p.ExternalHost, p.ExternalPort)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth browser client manifest: %w", err)
	}

	oauthChallengingClient := manifests.OAuthServerChallengingClientManifest(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, oauthChallengingClient, func() error {
		return oauth.ReconcileChallengingClientWorkerManifest(oauthChallengingClient, p.OwnerRef, p.ExternalHost, p.ExternalPort)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth challenging client manifest: %w", err)
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
	params.PackageServerAPIClusterIP = infraStatus.PackageServerAPIAddress
	params.BaseDomain = baseDomain
	params.PublicZoneID = hcp.Spec.DNS.PublicZoneID
	params.PrivateZoneID = hcp.Spec.DNS.PrivateZoneID
	params.CloudProvider = cloudProvider(hcp)
	params.PlatformType = platformType(hcp)
	params.InfraID = hcp.Spec.InfraID

	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		params.AWSRegion = hcp.Spec.Platform.AWS.Region
	}

	params.InternalAPIPort = defaultAPIServerPort
	params.IssuerURL = hcp.Spec.IssuerURL

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
	params.PackageServerCABundle = params.OpenshiftAPIServerCABundle

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
