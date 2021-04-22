package hostedcontrolplane

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	rbacv1 "k8s.io/api/rbac/v1"
	"math/big"
	"math/rand"
	"reflect"
	"strings"
	"time"

	"github.com/blang/semver"
	"github.com/go-logr/logr"
	operatorv1 "github.com/openshift/api/operator/v1"
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
	"k8s.io/apimachinery/pkg/util/intstr"
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
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/etcd"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/render"
	renderpki "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/render/pki"
	pkiutil "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/render/pki/util"
	hcputil "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
	"github.com/openshift/hypershift/control-plane-operator/releaseinfo"
	etcdv1 "github.com/openshift/hypershift/thirdparty/etcd/v1beta2"
)

const (
	finalizer                  = "hypershift.openshift.io/finalizer"
	controlPlaneAnnotation     = "hypershift.openshift.io/hosted-control-plane"
	APIServerPort              = 6443
	DefaultAdminKubeconfigName = "admin-kubeconfig"
	DefaultAdminKubeconfigKey  = "kubeconfig"
	kubeAPIServerServiceName   = "kube-apiserver"
	vpnServiceName             = "openvpn-server"
	oauthServiceName           = "oauth-openshift"
	pullSecretName             = "pull-secret"
	vpnServiceAccountName      = "vpn"
	ingressOperatorNamespace   = "openshift-ingress-operator"
	hypershiftRouteLabel       = "hypershift.openshift.io/cluster"
	oauthBrandingManifest      = "v4-0-config-system-branding.yaml"
	DefaultAPIServerIPAddress  = "172.20.0.1"
	externalOauthPort          = 443
	vpnServicePort             = 1194

	etcdOperatorImage          = "quay.io/coreos/etcd-operator:v0.9.4"
	etcdVersion                = "3.4.9"
	etcdClusterSize            = 1
	etcdDeleteCheckInterval    = 10 * time.Second
	etcdAvailableCheckInterval = 10 * time.Second
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
	APIAddress            string
	APIPort               int32
	OAuthAddress          string
	OAuthPort             int32
	VPNAddress            string
	VPNPort               int32
	OpenShiftAPIAddress   string
	OauthAPIServerAddress string
}

func (s InfrastructureStatus) IsReady() bool {
	return len(s.APIAddress) > 0 &&
		len(s.OAuthAddress) > 0 &&
		len(s.VPNAddress) > 0 &&
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

func getConditionByType(conditions []hyperv1.HostedControlPlaneCondition, conditionType hyperv1.ConditionType) *hyperv1.HostedControlPlaneCondition {
	for k, v := range conditions {
		if v.Type == conditionType {
			return &conditions[k]
		}
	}
	return nil
}

func setConditionByType(conditions *[]hyperv1.HostedControlPlaneCondition, conditionType hyperv1.ConditionType, status hyperv1.ConditionStatus, reason, message string) {
	existingCondition := getConditionByType(*conditions, conditionType)
	if existingCondition == nil {
		newCondition := hyperv1.HostedControlPlaneCondition{
			Type:    conditionType,
			Status:  status,
			Reason:  reason,
			Message: message,
		}
		*conditions = append(*conditions, newCondition)
	} else {
		existingCondition.Status = status
		existingCondition.Reason = reason
		existingCondition.Message = message
	}
}

func (r *HostedControlPlaneReconciler) setAvailableCondition(ctx context.Context, hostedControlPlane *hyperv1.HostedControlPlane, oldStatus *hyperv1.HostedControlPlaneStatus,
	status hyperv1.ConditionStatus, reason, message string, result ctrl.Result, err error) (ctrl.Result, error) {
	conditions := &hostedControlPlane.Status.Conditions

	setConditionByType(conditions, hyperv1.Available, status, reason, message)

	// Sync status.ready with Available condition
	condition := getConditionByType(*conditions, hyperv1.Available)
	if condition != nil && condition.Status == hyperv1.ConditionTrue {
		hostedControlPlane.Status.Ready = true
	} else {
		hostedControlPlane.Status.Ready = false
	}

	if reflect.DeepEqual(oldStatus, hostedControlPlane.Status) {
		// No change to status, nothing to sync
		return result, err
	}

	// Check for changed conditions to update LastTransitionTime
	for k, condition := range *conditions {
		oldCondition := getConditionByType(oldStatus.Conditions, condition.Type)
		if oldCondition == nil || *oldCondition != condition {
			(*conditions)[k].LastTransitionTime = metav1.Now()
		}
	}

	if updateErr := r.Status().Update(ctx, hostedControlPlane); updateErr != nil {
		r.Log.Error(updateErr, "failed to update status")
		result.Requeue = true
	}

	return result, err
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

	var result ctrl.Result
	// TODO (alberto):
	// May be eventually just run a deployment with a CVO running a hostedControlPlane profile
	// passing the hostedControlPlane.spec.version through?

	r.Log.Info("Creating API services")
	infraStatus, err := r.ensureInfrastructure(ctx, hostedControlPlane)
	if err != nil {
		r.Log.Error(err, "failed to ensure infrastructure")
		return r.setAvailableCondition(ctx, hostedControlPlane, oldStatus, hyperv1.ConditionFalse, "InfrastructureEnsureFailed", err.Error(), result, fmt.Errorf("failed to ensure infrastructure: %w", err))
	}

	// Wait for things like LB services to become available
	if !infraStatus.IsReady() {
		result.RequeueAfter = 5 * time.Second
		r.Log.Info("Cluster infrastructure is still provisioning, will try again later")
		return r.setAvailableCondition(ctx, hostedControlPlane, oldStatus, hyperv1.ConditionFalse, "WaitingOnInfrastructureReady", "Cluster infrastructure is still provisioning", result, nil)
	}
	hostedControlPlane.Status.ControlPlaneEndpoint = hyperv1.APIEndpoint{
		Host: infraStatus.APIAddress,
		Port: APIServerPort,
	}

	releaseImage, err := r.ReleaseProvider.Lookup(ctx, hostedControlPlane.Spec.ReleaseImage)
	if err != nil {
		return r.setAvailableCondition(ctx, hostedControlPlane, oldStatus, hyperv1.ConditionFalse, "ReleaseInfoLookupFailed", err.Error(), ctrl.Result{}, fmt.Errorf("failed to look up release info: %w", err))
	}
	componentVersions, err := releaseImage.ComponentVersions()
	if err != nil {
		return r.setAvailableCondition(ctx, hostedControlPlane, oldStatus, hyperv1.ConditionFalse, "InvalidComponentVersion", err.Error(), ctrl.Result{}, fmt.Errorf("invalid component versions found in release info: %w", err))
	}
	r.Log.Info("found release info for image", "releaseImage", hostedControlPlane.Spec.ReleaseImage, "info", releaseImage, "componentImages", releaseImage.ComponentImages(), "componentVersions", componentVersions)

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
		etcdCluster := etcd.Cluster(hostedControlPlane.Namespace)
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
		err = etcd.ReconcileEtcdClusterStatus(ctx, r.Client, hostedControlPlane, etcdCluster)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	// Reconcile root CA
	rootCASecret := pki.RootCASecret(hostedControlPlane.Namespace)
	if _, err = controllerutil.CreateOrUpdate(ctx, r, rootCASecret, func() error {
		rootCASecret.OwnerReferences = ensureHCPOwnerRef(hostedControlPlane, rootCASecret.OwnerReferences)
		return pki.ReconcileRootCA(rootCASecret)
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile root CA: %w", err)
	}

	// Reconcile etcd
	r.Log.Info("Reconciling Etcd")
	if err = r.reconcileEtcd(ctx, hostedControlPlane, releaseImage); err != nil {
		r.Log.Error(err, "failed to reconcile etcd")
		return ctrl.Result{}, err
	}
	{
		etcdAvailable := getConditionByType(hostedControlPlane.Status.Conditions, hyperv1.EtcdAvailable)
		if etcdAvailable == nil || etcdAvailable.Status != hyperv1.ConditionTrue {
			r.Log.Info("etcd is not yet available")
			return ctrl.Result{RequeueAfter: etcdAvailableCheckInterval}, nil
		}
	}

	// Install the control plane into the infrastructure
	r.Log.Info("Creating hosted control plane")
	err = r.ensureControlPlane(ctx, hostedControlPlane, infraStatus, releaseImage)
	if err != nil {
		r.Log.Error(err, "failed to ensure control plane")
		return r.setAvailableCondition(ctx, hostedControlPlane, oldStatus, hyperv1.ConditionFalse, "ControlPlaneEnsureFailed", err.Error(), result, fmt.Errorf("failed to ensure control plane: %w", err))
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

func (r *HostedControlPlaneReconciler) ensureInfrastructure(ctx context.Context, hcp *hyperv1.HostedControlPlane) (InfrastructureStatus, error) {
	status := InfrastructureStatus{}

	targetNamespace := hcp.GetNamespace()
	// Ensure that we can run privileged pods
	if err := ensureVPNSCC(r, hcp, targetNamespace); err != nil {
		return status, fmt.Errorf("failed to ensure privileged SCC for the new namespace: %w", err)
	}
	if hcp.Spec.Services == nil {
		return status, fmt.Errorf("service publishing strategy undefined")
	}
	for _, serviceItr := range hcp.Spec.Services {
		targetNamespace := hcp.GetNamespace()
		switch serviceItr.Service {
		case hyperv1.APIServer:
			switch serviceItr.ServicePublishingStrategy.Type {
			case hyperv1.NodePort:
				if serviceItr.ServicePublishingStrategy.NodePort == nil {
					return status, fmt.Errorf("nodeport metadata not defined for servicetype: %s", serviceItr.Service)
				}
				r.Log.Info("Creating nodePort Kube API service")
				apiService, err := r.reconcileKubeAPIServerServiceNodePort(ctx, hcp, targetNamespace, *serviceItr.ServicePublishingStrategy.NodePort)
				if err != nil {
					return status, fmt.Errorf("failed to reconcile Kube API service: %w", err)
				}
				r.Log.Info("Fetched Kube API service nodePort", "nodePort", apiService.Spec.Ports[0].NodePort)
				status.APIAddress = serviceItr.ServicePublishingStrategy.NodePort.Address
				status.APIPort = apiService.Spec.Ports[0].NodePort
			case hyperv1.LoadBalancer:
				r.Log.Info("Creating LoadBalancer Kube API service")
				apiService, err := createKubeAPIServerService(r, hcp, targetNamespace)
				if err != nil {
					return status, fmt.Errorf("failed to create Kube API service: %w", err)
				}

				r.Log.Info("Fetching Kube API service information")
				apiAddress, err := getLoadBalancerServiceAddress(r, ctx, client.ObjectKeyFromObject(apiService))
				if err != nil {
					return status, fmt.Errorf("error getting api service info: %w", err)
				}
				status.APIAddress = apiAddress
				status.APIPort = APIServerPort
			default:
				return status, fmt.Errorf("unsupported servicetype %s for service: %s", serviceItr.ServicePublishingStrategy.Type, serviceItr.Service)
			}
		case hyperv1.VPN:
			switch serviceItr.ServicePublishingStrategy.Type {
			case hyperv1.NodePort:
				if serviceItr.ServicePublishingStrategy.NodePort == nil {
					return status, fmt.Errorf("nodeport metadata not defined for servicetype: %s", serviceItr.Service)
				}
				r.Log.Info("reconciling nodePort VPN service")
				vpnService, err := r.reconcileVPNServerServiceNodePort(ctx, hcp, targetNamespace, *serviceItr.ServicePublishingStrategy.NodePort)
				if err != nil {
					return status, fmt.Errorf("error reconciling service for vpn: %w", err)
				}
				r.Log.Info("Fetched VPN service nodePort", "nodePort", vpnService.Spec.Ports[0].NodePort)
				status.VPNAddress = serviceItr.ServicePublishingStrategy.NodePort.Address
				status.VPNPort = vpnService.Spec.Ports[0].NodePort
			case hyperv1.LoadBalancer:
				r.Log.Info("Creating VPN service")
				vpnService, err := createVPNServerService(r, hcp, targetNamespace)
				if err != nil {
					return status, fmt.Errorf("error creating service for vpn: %w", err)
				}
				r.Log.Info("Fetching VPN service info")
				vpnAddress, err := getLoadBalancerServiceAddress(r, ctx, client.ObjectKeyFromObject(vpnService))
				if err != nil {
					return status, fmt.Errorf("error getting vpn service nodeport: %w", err)
				}
				status.VPNAddress = vpnAddress
				status.VPNPort = vpnServicePort
			default:
				return status, fmt.Errorf("unsupported servicetype %s for service: %s", serviceItr.ServicePublishingStrategy.Type, serviceItr.Service)
			}
		case hyperv1.OAuthServer:
			switch serviceItr.ServicePublishingStrategy.Type {
			case hyperv1.NodePort:
				if serviceItr.ServicePublishingStrategy.NodePort == nil {
					return status, fmt.Errorf("nodeport metadata not defined for servicetype: %s", serviceItr.Service)
				}
				r.Log.Info("Reconciling nodePort OAuth service")
				oauthService, err := r.reconcileOauthServiceNodePort(ctx, hcp, targetNamespace, *serviceItr.ServicePublishingStrategy.NodePort)
				if err != nil {
					return status, fmt.Errorf("error reconciling service for oauth: %w", err)
				}
				r.Log.Info("Fetched OAuth nodePort", "nodePort", oauthService.Spec.Ports[0].NodePort)
				status.OAuthAddress = serviceItr.ServicePublishingStrategy.NodePort.Address
				status.OAuthPort = oauthService.Spec.Ports[0].NodePort
			case hyperv1.Route:
				r.Log.Info("Creating OAuth service")
				_, err := createOauthService(r, hcp, targetNamespace)
				if err != nil {
					return status, fmt.Errorf("error creating service for oauth: %w", err)
				}
				r.Log.Info("Creating OAuth server route")
				oauthRoute := createOauthServerRoute(targetNamespace)
				oauthRoute.OwnerReferences = ensureHCPOwnerRef(hcp, oauthRoute.OwnerReferences)
				if err := r.Create(ctx, oauthRoute); err != nil && !apierrors.IsAlreadyExists(err) {
					return status, fmt.Errorf("failed to create oauth server route: %w", err)
				}
				r.Log.Info("Fetching OAuth server route information")
				oauthAddress, err := getRouteAddress(r, ctx, client.ObjectKeyFromObject(oauthRoute))
				if err != nil {
					return status, fmt.Errorf("failed get get route address: %w", err)
				}
				status.OAuthAddress = oauthAddress
				status.OAuthPort = 443
			default:
				return status, fmt.Errorf("unsupported servicetype %s for service: %s", serviceItr.ServicePublishingStrategy.Type, serviceItr.Service)
			}
		default:
			return status, fmt.Errorf("unknown service specified: %s", serviceItr.Service)
		}
	}

	r.Log.Info("Creating Openshift API service")
	openshiftAPIService, err := createOpenshiftService(r, hcp, targetNamespace)
	if err != nil {
		return status, fmt.Errorf("failed to create openshift server service: %w", err)
	}
	r.Log.Info("Created Openshift API service")

	r.Log.Info("Creating Openshift OAuth API service")
	oauthAPIService, err := createOauthAPIService(r, hcp, targetNamespace)
	if err != nil {
		return status, fmt.Errorf("failed to create openshift oauth api service: %w", err)
	}
	r.Log.Info("Created Openshift Oauth API service")

	status.OpenShiftAPIAddress = openshiftAPIService.Spec.ClusterIP
	status.OauthAPIServerAddress = oauthAPIService.Spec.ClusterIP

	return status, nil
}

func (r *HostedControlPlaneReconciler) ensureControlPlane(ctx context.Context, hcp *hyperv1.HostedControlPlane, infraStatus InfrastructureStatus, releaseImage *releaseinfo.ReleaseImage) error {
	r.Log.Info("ensuring control plane for cluster", "cluster", hcp.Name)

	targetNamespace := hcp.GetNamespace()

	// Create the configmap with the pull secret for the guest cluster
	var pullSecret corev1.Secret
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: targetNamespace, Name: pullSecretName}, &pullSecret); err != nil {
		return fmt.Errorf("failed to get pull secret %s: %w", pullSecretName, err)
	}
	pullSecretData, hasPullSecretData := pullSecret.Data[".dockerconfigjson"]
	if !hasPullSecretData {
		return fmt.Errorf("pull secret %s is missing the .dockerconfigjson key", pullSecretName)
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

	pkiSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      "pki",
		},
		Data: map[string][]byte{},
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(pkiSecret), pkiSecret); err != nil {
		return fmt.Errorf("failed to get pki secret: %w", err)
	}

	kubeconfigSecret, err := generateKubeconfigSecret(hcp.GetNamespace(), hcp.Spec.KubeConfig, pkiSecret.Data["admin.kubeconfig"])
	if err != nil {
		return fmt.Errorf("failed to create kubeconfig secret manifest for management cluster: %w", err)
	}
	kubeconfigSecret.OwnerReferences = ensureHCPOwnerRef(hcp, kubeconfigSecret.OwnerReferences)
	if err := r.Create(ctx, kubeconfigSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to generate kubeconfigSecret: %w", err)
	}

	baseDomain, err := clusterBaseDomain(r.Client, ctx, hcp)
	if err != nil {
		return fmt.Errorf("couldn't determine cluster base domain  name: %w", err)
	}
	r.Log.Info(fmt.Sprintf("Cluster API URL: %s", fmt.Sprintf("https://%s:%d", infraStatus.APIAddress, APIServerPort)))
	r.Log.Info(fmt.Sprintf("Kubeconfig is available in secret admin-kubeconfig in the %s namespace", hcp.GetNamespace()))
	r.Log.Info(fmt.Sprintf("Console URL:  %s", fmt.Sprintf("https://console-openshift-console.%s", fmt.Sprintf("apps.%s", baseDomain))))
	r.Log.Info(fmt.Sprintf("kubeadmin password is available in secret %q in the %s namespace", "kubeadmin-password", targetNamespace))

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileEtcd(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage) error {
	rootCASecret := pki.RootCASecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(rootCASecret), rootCASecret); err != nil {
		return fmt.Errorf("cannot get root CA secret: %w", err)
	}

	// Etcd client secret
	clientSecret := etcd.ClientSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, clientSecret, func() error {
		clientSecret.OwnerReferences = ensureHCPOwnerRef(hcp, clientSecret.OwnerReferences)
		return etcd.ReconcileClientSecret(clientSecret, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd client secret: %w", err)
	}

	// Etcd server secret
	serverSecret := etcd.ServerSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, serverSecret, func() error {
		serverSecret.OwnerReferences = ensureHCPOwnerRef(hcp, serverSecret.OwnerReferences)
		return etcd.ReconcileServerSecret(serverSecret, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd server secret: %w", err)
	}

	// Etcd peer secret
	peerSecret := etcd.PeerSecret(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, peerSecret, func() error {
		peerSecret.OwnerReferences = ensureHCPOwnerRef(hcp, peerSecret.OwnerReferences)
		return etcd.ReconcilePeerSecret(peerSecret, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd peer secret: %w", err)
	}

	// Etcd Operator ServiceAccount
	operatorServiceAccount := etcd.OperatorServiceAccount(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, operatorServiceAccount, func() error {
		operatorServiceAccount.OwnerReferences = ensureHCPOwnerRef(hcp, operatorServiceAccount.OwnerReferences)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd operator service account: %w", err)
	}

	// Etcd operator role
	operatorRole := etcd.OperatorRole(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, operatorRole, func() error {
		operatorRole.OwnerReferences = ensureHCPOwnerRef(hcp, operatorRole.OwnerReferences)
		return etcd.ReconcileOperatorRole(operatorRole)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd operator role: %w", err)
	}

	// Etcd operator rolebinding
	operatorRoleBinding := etcd.OperatorRoleBinding(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, operatorRoleBinding, func() error {
		operatorRoleBinding.OwnerReferences = ensureHCPOwnerRef(hcp, operatorRoleBinding.OwnerReferences)
		return etcd.ReconcileOperatorRoleBinding(operatorRoleBinding)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd operator role binding: %w", err)
	}

	// Etcd operator deployment
	operatorDeployment := etcd.OperatorDeployment(hcp.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r, operatorDeployment, func() error {
		operatorDeployment.OwnerReferences = ensureHCPOwnerRef(hcp, operatorDeployment.OwnerReferences)
		return etcd.ReconcileOperatorDeployment(operatorDeployment, etcdOperatorImage)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd operator deployment: %w", err)
	}

	// Etcd cluster
	etcdCluster := etcd.Cluster(hcp.Namespace)
	etcdAvailableCond := hcputil.GetConditionByType(hcp.Status.Conditions, hyperv1.EtcdAvailable)
	if etcdAvailableCond != nil && etcdAvailableCond.Status == hyperv1.ConditionFalse && etcdAvailableCond.Reason == etcd.EtcdReasonFailed {
		if err := r.Delete(ctx, etcdCluster); err != nil {
			return fmt.Errorf("failed to delete etcd cluster: %w", err)
		}
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r, etcdCluster, func() error {
		etcdCluster.OwnerReferences = ensureHCPOwnerRef(hcp, etcdCluster.OwnerReferences)
		return etcd.ReconcileCluster(etcdCluster, etcdClusterSize, etcdVersion)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd cluster: %w", err)
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
	params.ExternalAPIDNSName = infraStatus.APIAddress
	params.ExternalAPIPort = uint(infraStatus.APIPort)
	params.ExternalAPIAddress = DefaultAPIServerIPAddress
	params.ExternalOpenVPNAddress = infraStatus.VPNAddress
	params.ExternalOpenVPNPort = uint(infraStatus.VPNPort)
	params.ExternalOauthDNSName = infraStatus.OAuthAddress
	params.ExternalOauthPort = uint(infraStatus.OAuthPort)
	params.ServiceCIDR = hcp.Spec.ServiceCIDR
	params.PodCIDR = hcp.Spec.PodCIDR
	params.MachineCIDR = hcp.Spec.MachineCIDR
	params.ReleaseImage = hcp.Spec.ReleaseImage
	params.IngressSubdomain = fmt.Sprintf("apps.%s", baseDomain)
	params.OpenShiftAPIClusterIP = infraStatus.OpenShiftAPIAddress
	params.OauthAPIClusterIP = infraStatus.OauthAPIServerAddress
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

	params.InternalAPIPort = APIServerPort
	params.IssuerURL = hcp.Spec.IssuerURL
	params.EtcdClientName = "etcd-client"
	params.NetworkType = "OpenShiftSDN"
	params.ImageRegistryHTTPSecret = generateImageRegistrySecret()
	params.APIAvailabilityPolicy = render.SingleReplica
	params.ControllerAvailabilityPolicy = render.SingleReplica
	params.SSHKey = string(sshKeyData)

	// Generate PKI data just once and store it in a secret. PKI generation isn't
	// deterministic and shouldn't be performed with every reconcile, otherwise
	// we're effectively doing an uncontrolled cert rotation each generation.
	pkiSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      "pki",
		},
		Data: map[string][]byte{},
	}
	needsPkiSecret := false
	if err := r.Get(ctx, client.ObjectKeyFromObject(pkiSecret), pkiSecret); err != nil {
		if apierrors.IsNotFound(err) {
			needsPkiSecret = true
		} else {
			return nil, fmt.Errorf("failed to get pki secret: %w", err)
		}
	} else {
		r.Log.Info("using existing pki secret")
	}
	if needsPkiSecret {
		rootCA := pki.RootCASecret(hcp.Namespace)
		if err := r.Get(ctx, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
			return nil, fmt.Errorf("failed to read Root CA secret: %w", err)
		}
		pkiParams := &render.PKIParams{
			ExternalAPIAddress:         infraStatus.APIAddress,
			NodeInternalAPIServerIP:    params.ExternalAPIAddress,
			ExternalAPIPort:            params.ExternalAPIPort,
			InternalAPIPort:            params.InternalAPIPort,
			ServiceCIDR:                hcp.Spec.ServiceCIDR,
			ExternalOauthAddress:       infraStatus.OAuthAddress,
			MachineConfigServerAddress: infraStatus.APIAddress,
			IngressSubdomain:           "apps." + baseDomain,
			ExternalOpenVPNAddress:     infraStatus.VPNAddress,
			Namespace:                  targetNamespace,
			RootCACert:                 rootCA.Data[pki.CASignerCertMapKey],
			RootCAKey:                  rootCA.Data[pki.CASignerKeyMapKey],
		}
		r.Log.Info("Checking if node port domain should be added to machine config server certs")
		// TODO: note this assumes that all NodePort services share a common address which is virtually always the case in
		// actual environments. Ideally this would be fetched from the MachineConfigServer CRD itself but there is a race
		// condition between the creation of the MachineConfigServer CRD and the initial PKI creation that would result in the
		// Machine Config Server not functioning in the node port deployment model.
		if hcp.Spec.Services != nil {
			for _, serviceItr := range hcp.Spec.Services {
				if serviceItr.ServicePublishingStrategy.Type == hyperv1.NodePort && serviceItr.ServicePublishingStrategy.NodePort != nil {
					r.Log.Info("Using node port address found in node port service", "serviceType", serviceItr.Service, "address", serviceItr.ServicePublishingStrategy.NodePort.Address)
					pkiParams.MachineConfigServerAddress = serviceItr.ServicePublishingStrategy.NodePort.Address
					break
				}
			}
		}
		r.Log.Info("generating PKI secret data")
		data, err := renderpki.GeneratePKI(pkiParams)
		if err != nil {
			return nil, fmt.Errorf("failed to generate PKI data: %w", err)
		}
		pkiSecret.Data = data
		if err := r.Create(ctx, pkiSecret); err != nil {
			return nil, fmt.Errorf("failed to create pki secret: %w", err)
		}
		r.Log.Info("created pki secret")
	}

	caBytes, hasData := pkiSecret.Data["combined-ca.crt"]
	if !hasData {
		return nil, fmt.Errorf("pki secret %q is missing combined-ca.crt key", pkiSecret.Name)
	}
	params.OpenshiftAPIServerCABundle = base64.StdEncoding.EncodeToString(caBytes)
	params.OauthAPIServerCABundle = params.OpenshiftAPIServerCABundle

	var pullSecret corev1.Secret
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcp.GetNamespace(), Name: hcp.Spec.PullSecret.Name}, &pullSecret); err != nil {
		return nil, fmt.Errorf("failed to get pull secret %s: %w", hcp.Spec.PullSecret.Name, err)
	}
	pullSecretData, hasPullSecretData := pullSecret.Data[".dockerconfigjson"]
	if !hasPullSecretData {
		return nil, fmt.Errorf("pull secret %s is missing the .dockerconfigjson key", hcp.Spec.PullSecret.Name)
	}

	var signingKeySecret corev1.Secret
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcp.GetNamespace(), Name: hcp.Spec.SigningKey.Name}, &signingKeySecret); err != nil {
		return nil, fmt.Errorf("failed to get signing key %s: %w", hcp.Spec.SigningKey.Name, err)
	}
	signingKeySecretData, hasSigningKeySecretData := signingKeySecret.Data["key"]
	if !hasSigningKeySecretData {
		return nil, fmt.Errorf("signing key secret %s is missing the key key", hcp.Spec.SigningKey.Name)
	}
	privKey, err := pkiutil.PemToPrivateKey(signingKeySecretData)
	if err != nil {
		return nil, fmt.Errorf("failed to PEM decode private key %s: %w", hcp.Spec.SigningKey.Name, err)
	}
	pubPEMKey, err := pkiutil.PublicKeyToPem(&privKey.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to PEM encode public key %s: %w", hcp.Spec.SigningKey.Name, err)
	}
	pkiSecret.Data["service-account.key"] = signingKeySecretData
	pkiSecret.Data["service-account.pub"] = pubPEMKey

	manifests, err := render.RenderClusterManifests(params, releaseImage, pullSecretData, pkiSecret.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to render hypershift manifests for cluster: %w", err)
	}

	kubeAPIServerParams := &render.KubeAPIServerParams{
		PodCIDR:               params.PodCIDR,
		ServiceCIDR:           params.ServiceCIDR,
		ExternalAPIAddress:    params.ExternalAPIAddress,
		APIServerAuditEnabled: params.APIServerAuditEnabled,
		CloudProvider:         params.CloudProvider,
		EtcdClientName:        params.EtcdClientName,
		DefaultFeatureGates:   params.DefaultFeatureGates,
		ExtraFeatureGates:     params.ExtraFeatureGates,
		IngressSubdomain:      params.IngressSubdomain,
		InternalAPIPort:       params.InternalAPIPort,
		IssuerURL:             params.IssuerURL,
		NamedCerts:            params.NamedCerts,
		PKI:                   pkiSecret.Data,
		APIAvailabilityPolicy: render.KubeAPIServerParamsAvailabilityPolicy(params.APIAvailabilityPolicy),
		ClusterID:             params.ClusterID,
		Images:                releaseImage.ComponentImages(),
		ApiserverLivenessPath: params.ApiserverLivenessPath,
		APINodePort:           params.APINodePort,
		ExternalOauthPort:     params.ExternalOauthPort,
		ExternalOauthDNSName:  params.ExternalOauthDNSName,
		InfraID:               hcp.Spec.InfraID,
	}
	if hcp.Spec.Platform.AWS != nil {
		kubeAPIServerParams.AWSRegion = hcp.Spec.Platform.AWS.Region
		kubeAPIServerParams.AWSVPCID = hcp.Spec.Platform.AWS.VPC
		if hcp.Spec.Platform.AWS.NodePoolDefaults != nil {
			kubeAPIServerParams.AWSZone = hcp.Spec.Platform.AWS.NodePoolDefaults.Zone
			if hcp.Spec.Platform.AWS.NodePoolDefaults.Subnet.ID != nil {
				kubeAPIServerParams.AWSSubnetID = *hcp.Spec.Platform.AWS.NodePoolDefaults.Subnet.ID
			}
		}
	}
	kubeAPIServerContext := render.NewKubeAPIServerManifestContext(kubeAPIServerParams)
	kubeAPIServerManifests, err := kubeAPIServerContext.Render()
	if err != nil {
		return nil, fmt.Errorf("failed to render kube apiserver manifests: %w", err)
	}
	for k := range kubeAPIServerManifests {
		manifests[k] = kubeAPIServerManifests[k]
	}

	return manifests, nil
}

func createKubeAPIServerService(client client.Client, hcp *hyperv1.HostedControlPlane, namespace string) (*corev1.Service, error) {
	svc := &corev1.Service{}
	svc.Namespace = namespace
	svc.Name = kubeAPIServerServiceName
	svc.Spec.Selector = map[string]string{"app": "kube-apiserver"}
	svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Port:       6443,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(6443),
		},
	}
	svc.OwnerReferences = ensureHCPOwnerRef(hcp, svc.OwnerReferences)
	if err := client.Create(context.TODO(), svc); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("failed to create api server service: %w", err)
		}
	}
	return svc, nil
}

func (r *HostedControlPlaneReconciler) reconcileKubeAPIServerServiceNodePort(ctx context.Context, hcp *hyperv1.HostedControlPlane, namespace string, nodePortMetadata hyperv1.NodePortPublishingStrategy) (*corev1.Service, error) {
	svc := &corev1.Service{}
	svc.Namespace = namespace
	svc.Name = kubeAPIServerServiceName
	var nodePort int32 = 0
	if nodePortMetadata.Port > 0 {
		nodePort = nodePortMetadata.Port
	}
	var kubeAPIServerServiceData corev1.Service
	r.Log.Info("Checking for existing service", "serviceName", svc.Name, "namespace", svc.Namespace)
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: svc.Namespace, Name: svc.Name}, &kubeAPIServerServiceData); err != nil && !apierrors.IsNotFound(err) {
		return svc, err
	}
	if len(kubeAPIServerServiceData.Spec.Ports) > 0 && kubeAPIServerServiceData.Spec.Ports[0].NodePort > 0 {
		r.Log.Info("Preserving existing nodePort for service", "nodePort", kubeAPIServerServiceData.Spec.Ports[0].NodePort)
		nodePort = kubeAPIServerServiceData.Spec.Ports[0].NodePort
	}
	r.Log.Info("Updating KubeAPI service")
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Spec.Ports = []corev1.ServicePort{
			{
				Port:       6443,
				Protocol:   corev1.ProtocolTCP,
				TargetPort: intstr.FromInt(6443),
			},
		}
		if nodePort > 0 {
			svc.Spec.Ports[0].NodePort = nodePort
		}
		svc.Spec.Selector = map[string]string{"app": "kube-apiserver"}
		svc.Spec.Type = corev1.ServiceTypeNodePort
		svc.OwnerReferences = ensureHCPOwnerRef(hcp, svc.OwnerReferences)
		return nil
	})
	if err != nil {
		return svc, err
	}
	r.Log.Info("Retrieving KubeAPI service to get nodePort value")
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: svc.Namespace, Name: svc.Name}, svc); err != nil {
		return svc, err
	}
	if !(svc.Spec.Ports[0].NodePort > 0) {
		return svc, fmt.Errorf("nodePort not populated")
	}
	return svc, err
}

func createVPNServerService(client client.Client, hcp *hyperv1.HostedControlPlane, namespace string) (*corev1.Service, error) {
	svc := &corev1.Service{}
	svc.Namespace = namespace
	svc.Name = vpnServiceName
	svc.Spec.Selector = map[string]string{"app": "openvpn-server"}
	svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Port:       vpnServicePort,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(vpnServicePort),
		},
	}
	svc.OwnerReferences = ensureHCPOwnerRef(hcp, svc.OwnerReferences)
	if err := client.Create(context.TODO(), svc); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("failed to create vpn server service: %w", err)
		}
	}
	return svc, nil
}

func (r *HostedControlPlaneReconciler) reconcileVPNServerServiceNodePort(ctx context.Context, hcp *hyperv1.HostedControlPlane, namespace string, nodePortMetadata hyperv1.NodePortPublishingStrategy) (*corev1.Service, error) {
	svc := &corev1.Service{}
	svc.Namespace = namespace
	svc.Name = vpnServiceName
	var nodePort int32 = 0
	if nodePortMetadata.Port > 0 {
		nodePort = nodePortMetadata.Port
	}
	var openVPNServerServiceData corev1.Service
	r.Log.Info("Checking for existing service", "serviceName", svc.Name, "namespace", svc.Namespace)
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: svc.Namespace, Name: svc.Name}, &openVPNServerServiceData); err != nil && !apierrors.IsNotFound(err) {
		return svc, err
	}
	if len(openVPNServerServiceData.Spec.Ports) > 0 && openVPNServerServiceData.Spec.Ports[0].NodePort > 0 {
		r.Log.Info("Preserving existing nodePort for service", "nodePort", openVPNServerServiceData.Spec.Ports[0].NodePort)
		nodePort = openVPNServerServiceData.Spec.Ports[0].NodePort
	}
	r.Log.Info("Updating openVPN service")
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Spec.Ports = []corev1.ServicePort{
			{
				Port:       vpnServicePort,
				Protocol:   corev1.ProtocolTCP,
				TargetPort: intstr.FromInt(vpnServicePort),
			},
		}
		if nodePort > 0 {
			svc.Spec.Ports[0].NodePort = nodePort
		}
		svc.Spec.Selector = map[string]string{"app": "openvpn-server"}
		svc.Spec.Type = corev1.ServiceTypeNodePort
		svc.OwnerReferences = ensureHCPOwnerRef(hcp, svc.OwnerReferences)
		return nil
	})
	if err != nil {
		return svc, err
	}
	r.Log.Info("Retrieving openVPN service to get nodePort value")
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: svc.Namespace, Name: svc.Name}, svc); err != nil {
		return svc, err
	}
	if !(svc.Spec.Ports[0].NodePort > 0) {
		return svc, fmt.Errorf("node port not populated")
	}
	return svc, err
}

func createOpenshiftService(c client.Client, hcp *hyperv1.HostedControlPlane, namespace string) (*corev1.Service, error) {
	svc := &corev1.Service{}
	svc.Namespace = namespace
	svc.Name = "openshift-apiserver"
	svc.Spec.Selector = map[string]string{"app": "openshift-apiserver"}
	svc.Spec.Type = corev1.ServiceTypeClusterIP
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Name:       "https",
			Port:       443,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(8443),
		},
	}
	svc.OwnerReferences = ensureHCPOwnerRef(hcp, svc.OwnerReferences)
	if err := c.Create(context.TODO(), svc); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return svc, c.Get(context.TODO(), client.ObjectKeyFromObject(svc), svc)
		} else {
			return nil, fmt.Errorf("failed to create openshift service: %w", err)
		}
	}
	return svc, nil
}

func createOauthAPIService(c client.Client, hcp *hyperv1.HostedControlPlane, namespace string) (*corev1.Service, error) {
	svc := &corev1.Service{}
	svc.Namespace = namespace
	svc.Name = "openshift-oauth-apiserver"
	svc.Spec.Selector = map[string]string{"app": "openshift-oauth-apiserver"}
	svc.Spec.Type = corev1.ServiceTypeClusterIP
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Name:       "https",
			Port:       443,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(8443),
		},
	}
	svc.OwnerReferences = ensureHCPOwnerRef(hcp, svc.OwnerReferences)
	if err := c.Create(context.TODO(), svc); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return svc, c.Get(context.TODO(), client.ObjectKeyFromObject(svc), svc)
		} else {
			return nil, fmt.Errorf("failed to create openshift service: %w", err)
		}
	}
	return svc, nil
}

func createOauthService(client client.Client, hcp *hyperv1.HostedControlPlane, namespace string) (*corev1.Service, error) {
	svc := &corev1.Service{}
	svc.Namespace = namespace
	svc.Name = oauthServiceName
	svc.Spec.Selector = map[string]string{"app": "oauth-openshift"}
	svc.Spec.Type = corev1.ServiceTypeClusterIP
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Name:       "https",
			Port:       443,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(6443),
		},
	}
	svc.OwnerReferences = ensureHCPOwnerRef(hcp, svc.OwnerReferences)
	err := client.Create(context.TODO(), svc)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("failed to create oauth service: %w", err)
	}
	return svc, nil
}

func (r *HostedControlPlaneReconciler) reconcileOauthServiceNodePort(ctx context.Context, hcp *hyperv1.HostedControlPlane, namespace string, nodePortMetadata hyperv1.NodePortPublishingStrategy) (*corev1.Service, error) {
	svc := &corev1.Service{}
	svc.Namespace = namespace
	svc.Name = oauthServiceName
	var nodePort int32 = 0
	if nodePortMetadata.Port > 0 {
		nodePort = nodePortMetadata.Port
	}
	var oauthServerServiceData corev1.Service
	r.Log.Info("Checking for existing service", "serviceName", svc.Name, "namespace", svc.Namespace)
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: svc.Namespace, Name: svc.Name}, &oauthServerServiceData); err != nil && !apierrors.IsNotFound(err) {
		return svc, err
	}
	if len(oauthServerServiceData.Spec.Ports) > 0 && oauthServerServiceData.Spec.Ports[0].NodePort > 0 {
		r.Log.Info("Preserving existing nodePort for service", "nodePort", oauthServerServiceData.Spec.Ports[0].NodePort)
		nodePort = oauthServerServiceData.Spec.Ports[0].NodePort
	}
	r.Log.Info("Updating oauth service")
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Spec.Ports = []corev1.ServicePort{
			{
				Name:       "https",
				Port:       443,
				Protocol:   corev1.ProtocolTCP,
				TargetPort: intstr.FromInt(6443),
			},
		}
		if nodePort > 0 {
			svc.Spec.Ports[0].NodePort = nodePort
		}
		svc.Spec.Selector = map[string]string{"app": "oauth-openshift"}
		svc.Spec.Type = corev1.ServiceTypeNodePort
		svc.OwnerReferences = ensureHCPOwnerRef(hcp, svc.OwnerReferences)
		return nil
	})
	if err != nil {
		return svc, err
	}
	r.Log.Info("Retrieving oauth service to get nodePort value")
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: svc.Namespace, Name: svc.Name}, svc); err != nil {
		return svc, err
	}
	if !(svc.Spec.Ports[0].NodePort > 0) {
		return svc, fmt.Errorf("node port not populated")
	}
	return svc, err
}

func ensureVPNSCC(c client.Client, hcp *hyperv1.HostedControlPlane, namespace string) error {
	sccBinding := &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("system:serviceaccount:%s:%s:scc:privileged", namespace, vpnServiceAccountName),
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
				Name:      vpnServiceAccountName,
				Namespace: namespace,
			},
		}
		sccBinding.OwnerReferences = ensureHCPOwnerRef(hcp, sccBinding.OwnerReferences)
		return nil
	})
	return err
}

func ensureDefaultIngressControllerSelector(c client.Client) error {
	defaultIC := &operatorv1.IngressController{}
	if err := c.Get(context.TODO(), client.ObjectKey{Namespace: ingressOperatorNamespace, Name: "default"}, defaultIC); err != nil {
		return fmt.Errorf("failed to fetch default ingress controller: %w", err)
	}
	routeSelector := defaultIC.Spec.RouteSelector
	if routeSelector == nil {
		routeSelector = &metav1.LabelSelector{}
	}
	found := false
	for _, exp := range routeSelector.MatchExpressions {
		if exp.Key == hypershiftRouteLabel && exp.Operator == metav1.LabelSelectorOpDoesNotExist {
			found = true
			break
		}
	}
	if !found {
		routeSelector.MatchExpressions = append(routeSelector.MatchExpressions, metav1.LabelSelectorRequirement{
			Key:      hypershiftRouteLabel,
			Operator: metav1.LabelSelectorOpDoesNotExist,
		})
		defaultIC.Spec.RouteSelector = routeSelector
		if err := c.Update(context.TODO(), defaultIC); err != nil {
			return fmt.Errorf("failed to update default ingress controller: %w", err)
		}
	}
	return nil
}

func createOauthServerRoute(namespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      "oauth",
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: "oauth-openshift",
			},
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationPassthrough,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect,
			},
		},
	}
}

func getLoadBalancerServiceAddress(c client.Client, ctx context.Context, key client.ObjectKey) (string, error) {
	svc := &corev1.Service{}
	if err := c.Get(ctx, key, svc); err != nil {
		return "", fmt.Errorf("failed to get service: %w", err)
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
	return addr, nil
}

func getRouteAddress(c client.Client, ctx context.Context, key client.ObjectKey) (string, error) {
	route := &routev1.Route{}
	if err := c.Get(ctx, key, route); err != nil {
		return "", fmt.Errorf("failed to get route: %w", err)
	}
	var addr string
	if len(route.Spec.Host) > 0 {
		addr = route.Spec.Host
	}
	return addr, nil
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
	default:
		return "None"
	}
}

func cloudProvider(hcp *hyperv1.HostedControlPlane) string {
	switch {
	case hcp.Spec.Platform.AWS != nil:
		return "aws"
	default:
		return ""
	}
}
