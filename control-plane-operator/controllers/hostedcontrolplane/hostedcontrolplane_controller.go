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
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/blang/semver"
	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"
	securityv1 "github.com/openshift/api/security/v1"

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

	"github.com/openshift/hypershift/thirdparty/clusterapi/util"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/render"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/render/pki"
	"github.com/openshift/hypershift/control-plane-operator/releaseinfo"
)

const (
	finalizer                 = "hypershift.openshift.io/finalizer"
	APIServerPort             = 6443
	kubeAPIServerServiceName  = "kube-apiserver"
	vpnServiceName            = "openvpn-server"
	oauthServiceName          = "oauth-openshift"
	pullSecretName            = "pull-secret"
	vpnServiceAccountName     = "vpn"
	ingressOperatorNamespace  = "openshift-ingress-operator"
	hypershiftRouteLabel      = "hypershift.openshift.io/cluster"
	oauthBrandingManifest     = "v4-0-config-system-branding.yaml"
	DefaultAPIServerIPAddress = "172.20.0.1"
	externalOauthPort         = 443
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

type InfrastructureStatus struct {
	APIAddress              string
	APIPort                 string
	OAuthAddress            string
	VPNAddress              string
	VPNPort                 string
	OpenShiftAPIAddress     string
	OauthAPIServerAddress   string
	IgnitionProviderAddress string
}

func (s InfrastructureStatus) IsReady() bool {
	return len(s.APIAddress) > 0 &&
		len(s.OAuthAddress) > 0 &&
		len(s.VPNAddress) > 0 &&
		len(s.IgnitionProviderAddress) > 0
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
		r.Log.Error(err, "failed to update status")
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
	switch hostedControlPlane.Spec.ServiceType {
	case "NodePort":
		externalAPIPort, err := strconv.ParseInt(infraStatus.APIPort, 10, 32)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to parse APIPort: %w", err)
		}
		hostedControlPlane.Status.ControlPlaneEndpoint = hyperv1.APIEndpoint{
			Host: infraStatus.APIAddress,
			Port: int32(externalAPIPort),
		}
	default:
		hostedControlPlane.Status.ControlPlaneEndpoint = hyperv1.APIEndpoint{
			Host: infraStatus.APIAddress,
			Port: APIServerPort,
		}
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
			if bootstrapPod.Spec.Containers[0].Image != hostedControlPlane.Spec.ReleaseImage {
				err := r.Client.Delete(ctx, &bootstrapPod)
				if err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to delete manifests bootstrapper pod: %w", err)
				}
				r.Log.Info("deleted manifests bootstrapper pod as part of an image rollout", "pod", bootstrapPod.Name)
			}
		}
	}

	// Install the control plane into the infrastructure
	r.Log.Info("Creating hosted control plane")
	err = r.ensureControlPlane(ctx, hostedControlPlane, infraStatus, releaseImage)
	if err != nil {
		r.Log.Error(err, "failed to ensure control plane")
		return r.setAvailableCondition(ctx, hostedControlPlane, oldStatus, hyperv1.ConditionFalse, "ControlPlaneEnsureFailed", err.Error(), result, fmt.Errorf("failed to ensure control plane: %w", err))
	}

	hostedControlPlane.Status.KubeConfig = &corev1.LocalObjectReference{
		Name: fmt.Sprintf("%v-kubeconfig", hostedControlPlane.Name),
	}

	hostedControlPlane.Status.ReleaseImage = hostedControlPlane.Spec.ReleaseImage

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
	if err := deleteManifests(ctx, r, r.Log, hcp.GetName(), manifests); err != nil {
		return err
	}
	return nil
}

func (r *HostedControlPlaneReconciler) ensureInfrastructure(ctx context.Context, hcp *hyperv1.HostedControlPlane) (InfrastructureStatus, error) {
	status := InfrastructureStatus{}

	targetNamespace := hcp.GetName()
	// Ensure that we can run privileged pods
	if err := ensureVPNSCC(r, hcp, targetNamespace); err != nil {
		return status, fmt.Errorf("failed to ensure privileged SCC for the new namespace: %w", err)
	}

	baseDomain, err := clusterBaseDomain(r.Client, ctx, hcp.Name)
	if err != nil {
		return status, fmt.Errorf("couldn't determine cluster base domain  name: %w", err)
	}

	// Create Kube APIServer service
	r.Log.Info("Creating Kube API service")
	apiService, err := createKubeAPIServerService(r, hcp, targetNamespace)
	if err != nil {
		return status, fmt.Errorf("failed to create Kube API service: %w", err)
	}
	r.Log.Info("Created Kube API service")

	r.Log.Info("Creating VPN service")
	vpnService, err := createVPNServerService(r, hcp, targetNamespace)
	if err != nil {
		return status, fmt.Errorf("failed to create vpn server service: %w", err)
	}
	r.Log.Info("Created VPN service")

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

	r.Log.Info("Creating OAuth service")
	_, err = createOauthService(r, hcp, targetNamespace)
	if err != nil {
		return status, fmt.Errorf("error creating service for oauth: %w", err)
	}

	r.Log.Info("Creating router shard")
	if err := createIngressController(r, hcp, targetNamespace, baseDomain); err != nil {
		return status, fmt.Errorf("cannot create router shard: %w", err)
	}

	r.Log.Info("Creating ignition provider route")
	ignitionRoute := createIgnitionServerRoute(targetNamespace)
	ignitionRoute.OwnerReferences = ensureHCPOwnerRef(hcp, ignitionRoute.OwnerReferences)
	if err := r.Create(ctx, ignitionRoute); err != nil && !apierrors.IsAlreadyExists(err) {
		return status, fmt.Errorf("failed to create ignition route: %w", err)
	}

	r.Log.Info("Creating oauth server route")
	oauthRoute := createOauthServerRoute(targetNamespace)
	oauthRoute.OwnerReferences = ensureHCPOwnerRef(hcp, oauthRoute.OwnerReferences)
	if err := r.Create(ctx, oauthRoute); err != nil && !apierrors.IsAlreadyExists(err) {
		return status, fmt.Errorf("failed to create oauth server route: %w", err)
	}

	apiAddress, err := getLoadBalancerServiceAddress(r, ctx, client.ObjectKeyFromObject(apiService))
	if err != nil {
		return status, fmt.Errorf("failed to get service: %w", err)
	}

	oauthAddress, err := getRouteAddress(r, ctx, client.ObjectKeyFromObject(oauthRoute))
	if err != nil {
		return status, fmt.Errorf("failed get get route address: %w", err)
	}
	status.OAuthAddress = oauthAddress

	vpnAddress, err := getLoadBalancerServiceAddress(r, ctx, client.ObjectKeyFromObject(vpnService))
	if err != nil {
		return status, fmt.Errorf("failed to get service: %w", err)
	}

	ignitionAddress, err := getRouteAddress(r, ctx, client.ObjectKeyFromObject(ignitionRoute))
	if err != nil {
		return status, fmt.Errorf("failed get get route address: %w", err)
	}
	status.IgnitionProviderAddress = ignitionAddress

	switch hcp.Spec.ServiceType {
	case "NodePort":
		status.APIAddress = hcp.Spec.ServiceAddress
		status.APIPort = apiAddress
		status.VPNAddress = hcp.Spec.ServiceAddress
		status.VPNPort = vpnAddress
	default:
		status.APIAddress = apiAddress
		status.APIPort = fmt.Sprint(APIServerPort)
		status.VPNAddress = vpnAddress
		status.VPNPort = "1194"
	}
	status.OpenShiftAPIAddress = openshiftAPIService.Spec.ClusterIP
	status.OauthAPIServerAddress = oauthAPIService.Spec.ClusterIP

	return status, nil
}

func (r *HostedControlPlaneReconciler) ensureControlPlane(ctx context.Context, hcp *hyperv1.HostedControlPlane, infraStatus InfrastructureStatus, releaseImage *releaseinfo.ReleaseImage) error {
	r.Log.Info("ensuring control plane for cluster", "cluster", hcp.Name)

	targetNamespace := hcp.GetName()
	version, err := semver.Parse(releaseImage.Version())
	if err != nil {
		return fmt.Errorf("cannot parse release version (%s): %v", releaseImage.Version(), err)
	}

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

	userDataSecret := generateUserDataSecret(hcp.GetName(), hcp.GetNamespace(), infraStatus.IgnitionProviderAddress, version)
	if err := r.Create(ctx, userDataSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to generate user data secret: %w", err)
	}
	userDataSecret.OwnerReferences = ensureHCPOwnerRef(hcp, userDataSecret.OwnerReferences)

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

	kubeconfigSecret, err := generateKubeconfigSecret(hcp.GetName(), hcp.GetNamespace(), pkiSecret.Data["admin.kubeconfig"])
	if err != nil {
		return fmt.Errorf("failed to create kubeconfig secret manifest for management cluster: %w", err)
	}
	kubeconfigSecret.OwnerReferences = ensureHCPOwnerRef(hcp, kubeconfigSecret.OwnerReferences)
	if err := r.Create(ctx, kubeconfigSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to generate kubeconfigSecret: %w", err)
	}

	baseDomain, err := clusterBaseDomain(r.Client, ctx, hcp.Name)
	if err != nil {
		return fmt.Errorf("couldn't determine cluster base domain  name: %w", err)
	}
	r.Log.Info(fmt.Sprintf("Cluster API URL: %s", fmt.Sprintf("https://%s:%d", infraStatus.APIAddress, APIServerPort)))
	r.Log.Info(fmt.Sprintf("Kubeconfig is available in secret admin-kubeconfig in the %s namespace", hcp.GetNamespace()))
	r.Log.Info(fmt.Sprintf("Console URL:  %s", fmt.Sprintf("https://console-openshift-console.%s", fmt.Sprintf("apps.%s", baseDomain))))
	r.Log.Info(fmt.Sprintf("kubeadmin password is available in secret %q in the %s namespace", "kubeadmin-password", targetNamespace))

	return nil
}

func (r *HostedControlPlaneReconciler) generateControlPlaneManifests(ctx context.Context, hcp *hyperv1.HostedControlPlane, infraStatus InfrastructureStatus, releaseImage *releaseinfo.ReleaseImage) (map[string][]byte, error) {
	targetNamespace := hcp.GetName()

	var sshKeySecret corev1.Secret
	err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcp.Namespace, Name: hcp.Spec.SSHKey.Name}, &sshKeySecret)
	if err != nil {
		return nil, fmt.Errorf("failed to get SSH key secret %s: %w", hcp.Spec.SSHKey.Name, err)
	}
	sshKeyData, hasSSHKeyData := sshKeySecret.Data["id_rsa.pub"]
	if !hasSSHKeyData {
		return nil, fmt.Errorf("SSH key secret secret %s is missing the id_rsa.pub key", hcp.Spec.SSHKey.Name)
	}
	baseDomain, err := clusterBaseDomain(r.Client, ctx, hcp.Name)
	if err != nil {
		return nil, fmt.Errorf("couldn't determine cluster base domain  name: %w", err)
	}
	var cloudCreds corev1.Secret
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: hcp.Namespace, Name: hcp.Spec.ProviderCreds.Name}, &cloudCreds)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider credentials secret %s: %w", hcp.Spec.ProviderCreds.Name, err)
	}

	params := render.NewClusterParams()
	params.Namespace = targetNamespace
	params.ExternalAPIDNSName = infraStatus.APIAddress
	params.ExternalAPIAddress = DefaultAPIServerIPAddress
	externalAPIPort, err := strconv.ParseUint(infraStatus.APIPort, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse API Port: %w", err)
	}
	params.ExternalAPIPort = uint(externalAPIPort)
	externalOpenVPNPort, err := strconv.ParseUint(infraStatus.VPNPort, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse VPN Port: %w", err)
	}
	params.ExternalOpenVPNPort = uint(externalOpenVPNPort)
	params.ExternalOpenVPNAddress = infraStatus.VPNAddress
	params.ExternalOauthDNSName = infraStatus.OAuthAddress
	params.ExternalOauthPort = externalOauthPort
	params.ServiceCIDR = hcp.Spec.ServiceCIDR
	params.PodCIDR = hcp.Spec.PodCIDR
	params.MachineCIDR = hcp.Spec.MachineCIDR
	params.ReleaseImage = hcp.Spec.ReleaseImage
	params.IngressSubdomain = fmt.Sprintf("apps.%s", baseDomain)
	params.OpenShiftAPIClusterIP = infraStatus.OpenShiftAPIAddress
	params.OauthAPIClusterIP = infraStatus.OauthAPIServerAddress
	params.BaseDomain = baseDomain
	params.MachineConfigServerAddress = infraStatus.IgnitionProviderAddress
	params.CloudProvider = cloudProvider(hcp)
	params.PlatformType = platformType(hcp)
	params.InfraID = hcp.Spec.InfraID
	if hcp.Spec.Platform.AWS != nil {
		params.AWSRegion = hcp.Spec.Platform.AWS.Region
		params.AWSVPCID = hcp.Spec.Platform.AWS.VPC
		if hcp.Spec.Platform.AWS.NodePoolDefaults != nil {
			params.AWSZone = hcp.Spec.Platform.AWS.NodePoolDefaults.Zone
			if hcp.Spec.Platform.AWS.NodePoolDefaults.Subnet.ID != nil {
				params.AWSSubnetID = *hcp.Spec.Platform.AWS.NodePoolDefaults.Subnet.ID
			}
		}
	}
	params.CloudCredentials = string(cloudCreds.Data["credentials"])
	params.ProviderCredsSecretName = hcp.Spec.ProviderCreds.Name
	params.InternalAPIPort = APIServerPort
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
		pkiParams := &render.PKIParams{
			ExternalAPIAddress:         infraStatus.APIAddress,
			NodeInternalAPIServerIP:    DefaultAPIServerIPAddress,
			ExternalAPIPort:            params.ExternalAPIPort,
			InternalAPIPort:            APIServerPort,
			ServiceCIDR:                hcp.Spec.ServiceCIDR,
			ExternalOauthAddress:       infraStatus.OAuthAddress,
			IngressSubdomain:           "apps." + baseDomain,
			MachineConfigServerAddress: infraStatus.IgnitionProviderAddress,
			ExternalOpenVPNAddress:     infraStatus.VPNAddress,
			Namespace:                  targetNamespace,
		}
		r.Log.Info("generating PKI secret data")
		data, err := pki.GeneratePKI(pkiParams)
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
	manifests, err := render.RenderClusterManifests(params, releaseImage, pullSecretData, pkiSecret.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to render hypershift manifests for cluster: %w", err)
	}

	kubeAPIServerParams := &render.KubeAPIServerParams{
		PodCIDR:                 params.PodCIDR,
		ServiceCIDR:             params.ServiceCIDR,
		ExternalAPIAddress:      params.ExternalAPIAddress,
		APIServerAuditEnabled:   params.APIServerAuditEnabled,
		CloudProvider:           params.CloudProvider,
		EtcdClientName:          params.EtcdClientName,
		DefaultFeatureGates:     params.DefaultFeatureGates,
		ExtraFeatureGates:       params.ExtraFeatureGates,
		IngressSubdomain:        params.IngressSubdomain,
		InternalAPIPort:         params.InternalAPIPort,
		NamedCerts:              params.NamedCerts,
		PKI:                     pkiSecret.Data,
		APIAvailabilityPolicy:   render.KubeAPIServerParamsAvailabilityPolicy(params.APIAvailabilityPolicy),
		ClusterID:               params.ClusterID,
		Images:                  releaseImage.ComponentImages(),
		ApiserverLivenessPath:   params.ApiserverLivenessPath,
		APINodePort:             params.APINodePort,
		ExternalOauthPort:       params.ExternalOauthPort,
		ExternalOauthDNSName:    params.ExternalOauthDNSName,
		ProviderCredsSecretName: hcp.Spec.ProviderCreds.Name,
		InfraID:                 hcp.Spec.InfraID,
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
	switch hcp.Spec.ServiceType {
	case "NodePort":
		svc.Spec.Type = corev1.ServiceTypeNodePort
	default:
		svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	}
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

func createVPNServerService(client client.Client, hcp *hyperv1.HostedControlPlane, namespace string) (*corev1.Service, error) {
	svc := &corev1.Service{}
	svc.Namespace = namespace
	svc.Name = vpnServiceName
	svc.Spec.Selector = map[string]string{"app": "openvpn-server"}
	switch hcp.Spec.ServiceType {
	case "NodePort":
		svc.Spec.Type = corev1.ServiceTypeNodePort
	default:
		svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	}
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Port:       1194,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(1194),
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

func ensureVPNSCC(c client.Client, hcp *hyperv1.HostedControlPlane, namespace string) error {
	scc := &securityv1.SecurityContextConstraints{}
	if err := c.Get(context.TODO(), client.ObjectKey{Name: "privileged"}, scc); err != nil {
		return fmt.Errorf("failed to get privileged scc: %w", err)
	}
	userSet := sets.NewString(scc.Users...)
	svcAccount := fmt.Sprintf("system:serviceaccount:%s:%s", namespace, vpnServiceAccountName)
	if userSet.Has(svcAccount) {
		return nil
	}
	userSet.Insert(svcAccount)
	scc.Users = userSet.List()
	scc.OwnerReferences = ensureHCPOwnerRef(hcp, scc.OwnerReferences)
	if err := c.Update(context.TODO(), scc); err != nil {
		return fmt.Errorf("failed to update privileged scc: %w", err)
	}
	return nil
}

func createIngressController(c client.Client, hcp *hyperv1.HostedControlPlane, name string, parentDomain string) error {
	// First ensure that the default ingress controller doesn't use routes generated for hypershift clusters
	err := ensureDefaultIngressControllerSelector(c)
	if err != nil {
		return err
	}
	ic := &operatorv1.IngressController{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ingressOperatorNamespace,
		},
		Spec: operatorv1.IngressControllerSpec{
			Domain: fmt.Sprintf("apps.%s", parentDomain),
			RouteSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					hypershiftRouteLabel: name,
				},
			},
		},
	}
	if err := c.Create(context.TODO(), ic); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create ingress controller for %s: %w", name, err)
	}
	return nil
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

func createIgnitionServerRoute(namespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      "ignition-provider",
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: "machine-config-server",
			},
		},
	}
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
	} else if svc.Spec.Ports[0].NodePort > 0 {
		addr = fmt.Sprint(svc.Spec.Ports[0].NodePort)
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

func clusterBaseDomain(c client.Client, ctx context.Context, clusterName string) (string, error) {
	var dnsConfig configv1.DNS
	err := c.Get(ctx, client.ObjectKey{Name: "cluster"}, &dnsConfig)
	if err != nil {
		return "", fmt.Errorf("failed to get cluster dns config: %w", err)
	}
	return fmt.Sprintf("%s.%s", clusterName, dnsConfig.Spec.BaseDomain), nil
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

func generateUserDataSecret(name, namespace string, ignitionProviderAddr string, version semver.Version) *corev1.Secret {
	secret := &corev1.Secret{}
	secret.Name = fmt.Sprintf("%s-user-data", name)
	secret.Namespace = namespace

	disableTemplatingValue := []byte(base64.StdEncoding.EncodeToString([]byte("true")))
	var userDataValue []byte

	// Clear any version modifiers for this comparison
	version.Pre = nil
	version.Build = nil
	if version.GTE(version46) {
		userDataValue = []byte(fmt.Sprintf(`{"ignition":{"config":{"merge":[{"source":"http://%s/config/master","verification":{}}]},"security":{},"timeouts":{},"version":"3.1.0"},"networkd":{},"passwd":{},"storage":{},"systemd":{}}`, ignitionProviderAddr))
	} else {
		userDataValue = []byte(fmt.Sprintf(`{"ignition":{"config":{"append":[{"source":"http://%s/config/master","verification":{}}]},"security":{},"timeouts":{},"version":"2.2.0"},"networkd":{},"passwd":{},"storage":{},"systemd":{}}`, ignitionProviderAddr))
	}

	secret.Data = map[string][]byte{
		"disableTemplating": disableTemplatingValue,
		"value":             userDataValue,
	}
	return secret
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

func generateKubeconfigSecret(name, namespace string, kubeconfigBytes []byte) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	secret.Namespace = namespace
	secret.Name = fmt.Sprintf("%v-kubeconfig", name)
	secret.Data = map[string][]byte{"value": kubeconfigBytes}
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
