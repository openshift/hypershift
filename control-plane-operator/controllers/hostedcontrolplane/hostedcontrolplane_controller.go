package hostedcontrolplane

import (
	"context"
	crand "crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/kms"
	"github.com/blang/semver"
	"github.com/go-logr/logr"
	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/autoscaler"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/aws"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/kubevirt"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/powervs"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/clusterpolicy"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cno"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/configoperator"
	kubevirtcsi "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/csi/kubevirt"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cvo"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/dnsoperator"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/etcd"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ignition"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ignitionserver"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ingress"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ingressoperator"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kcm"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/konnectivity"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/machineapprover"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/mcs"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/nto"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/oapi"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/oauth"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ocm"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/olm"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/registryoperator"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/routecm"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/scheduler"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/snapshotcontroller"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/storage"
	supportawsutil "github.com/openshift/hypershift/support/awsutil"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/events"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/duration"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	awssdk "github.com/aws/aws-sdk-go/aws"
)

const (
	finalizer                              = "hypershift.openshift.io/finalizer"
	DefaultAdminKubeconfigKey              = "kubeconfig"
	ImageStreamAutoscalerImage             = "cluster-autoscaler"
	ImageStreamClusterMachineApproverImage = "cluster-machine-approver"

	resourceDeletionTimeout = 2 * time.Minute
)

var NoopReconcile controllerutil.MutateFn = func() error { return nil }

type InfrastructureStatus struct {
	APIHost                 string
	APIPort                 int32
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
		len(s.OAuthHost) > 0 &&
		len(s.KonnectivityHost) > 0 &&
		s.APIPort > 0 &&
		s.OAuthPort > 0 &&
		s.KonnectivityPort > 0

	if s.NeedInternalRouter {
		isReady = isReady && len(s.InternalHCPRouterHost) > 0
	}
	if s.NeedExternalRouter {
		isReady = isReady && len(s.ExternalHCPRouterHost) > 0
	}
	return isReady
}

type HostedControlPlaneReconciler struct {
	client.Client

	// ManagementClusterCapabilities can be asked for support of optional management cluster capabilities
	ManagementClusterCapabilities capabilities.CapabiltyChecker

	// SetDefaultSecurityContext is used to configure Security Context for containers
	SetDefaultSecurityContext bool

	Log                           logr.Logger
	ReleaseProvider               releaseinfo.ProviderWithRegistryOverrides
	createOrUpdate                func(hcp *hyperv1.HostedControlPlane) upsert.CreateOrUpdateFN
	EnableCIDebugOutput           bool
	OperateOnReleaseImage         string
	DefaultIngressDomain          string
	MetricsSet                    metrics.MetricsSet
	ec2Client                     ec2iface.EC2API
	awsSession                    *session.Session
	reconcileInfrastructureStatus func(ctx context.Context, hcp *hyperv1.HostedControlPlane) (InfrastructureStatus, error)
}

func (r *HostedControlPlaneReconciler) SetupWithManager(mgr ctrl.Manager, createOrUpdate upsert.CreateOrUpdateFN) error {
	r.setup(createOrUpdate)
	b := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedControlPlane{}).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
		})
	for _, handler := range r.eventHandlers() {
		b.Watches(&source.Kind{Type: handler.obj}, handler.handler)
	}
	if _, err := b.Build(r); err != nil {
		return fmt.Errorf("failed setting up with a controller manager %w", err)
	}

	// Set based on SCC capability
	// When SCC is available (OpenShift), the container's security context and UID range is automatically set
	// When SCC is not available (Kubernetes), we want to explicitly set a default (non-root) security context
	r.SetDefaultSecurityContext = !r.ManagementClusterCapabilities.Has(capabilities.CapabilitySecurityContextConstraint)

	r.reconcileInfrastructureStatus = r.defaultReconcileInfrastructureStatus

	r.ec2Client, r.awsSession = getEC2Client()

	return nil
}

func getEC2Client() (ec2iface.EC2API, *session.Session) {
	// AWS_SHARED_CREDENTIALS_FILE and AWS_REGION envvar should be set in operator deployment
	// when reconciling an AWS hosted control plane
	if os.Getenv("AWS_SHARED_CREDENTIALS_FILE") != "" {
		awsSession := awsutil.NewSession("control-plane-operator", "", "", "", "")
		awsConfig := awssdk.NewConfig()
		ec2Client := ec2.New(awsSession, awsConfig)
		return ec2Client, awsSession
	}
	return nil, nil
}

func isScrapeConfig(obj client.Object) bool {
	switch obj.(type) {
	case *prometheusoperatorv1.ServiceMonitor:
		return true
	case *prometheusoperatorv1.PodMonitor:
		return true
	}

	return false
}

func isClusterVersionAvailable(hcp *hyperv1.HostedControlPlane) bool {
	condition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionAvailable))

	return condition != nil && condition.Status == metav1.ConditionTrue
}

func createOrUpdateWithDelayForScrapeConfigs(hcp *hyperv1.HostedControlPlane, upstreamCreateOrUpdate upsert.CreateOrUpdateFN) upsert.CreateOrUpdateFN {
	return func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
		// Skipping creation / update of scrape configs (servicemonitor and podmonitor resources) till condition ClusterVersionAvailable is met.
		// Meeting this condition is equivalent to reach 'Complete' progress on the corresponding hosted cluster
		if isScrapeConfig(obj) && !isClusterVersionAvailable(hcp) {
			log := ctrl.LoggerFrom(ctx)
			log.Info("Skipping creation/update of scrape config as "+string(hyperv1.ClusterVersionAvailable)+" condition is not yet met", "scrapeConfig", obj)

			return controllerutil.OperationResultNone, nil
		}

		return upstreamCreateOrUpdate(ctx, c, obj, f)
	}
}

func (r *HostedControlPlaneReconciler) setup(upstreamCreateOrUpdate upsert.CreateOrUpdateFN) {
	createOrUpdateFactory := createOrUpdateWithOwnerRefFactory(upstreamCreateOrUpdate)

	r.createOrUpdate = func(hcp *hyperv1.HostedControlPlane) upsert.CreateOrUpdateFN {
		return createOrUpdateWithDelayForScrapeConfigs(hcp, createOrUpdateFactory(hcp))
	}
}

type eventHandler struct {
	obj     client.Object
	handler handler.EventHandler
}

func (r *HostedControlPlaneReconciler) eventHandlers() []eventHandler {
	handlers := []eventHandler{
		{obj: &corev1.Event{}, handler: handler.EnqueueRequestsFromMapFunc(r.hostedControlPlaneInNamespace)},
		{obj: &corev1.Service{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &appsv1.Deployment{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &appsv1.StatefulSet{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &corev1.Secret{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &corev1.ConfigMap{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &corev1.ServiceAccount{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &policyv1.PodDisruptionBudget{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &prometheusoperatorv1.PodMonitor{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &prometheusoperatorv1.ServiceMonitor{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &prometheusoperatorv1.PrometheusRule{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &rbacv1.Role{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &rbacv1.RoleBinding{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
	}
	if r.ManagementClusterCapabilities.Has(capabilities.CapabilityRoute) {
		handlers = append(handlers, eventHandler{obj: &routev1.Route{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}})
	}

	return handlers
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

	originalHostedControlPlane := hostedControlPlane.DeepCopy()
	// This is a best effort ping to the identity provider
	// that enables access from the operator to the cloud provider resources.
	healthCheckIdentityProvider(ctx, hostedControlPlane)
	// We want to ensure the healthCheckIdentityProvider condition is in status before we go through the deletion timestamp path.
	if err := r.Client.Status().Patch(ctx, hostedControlPlane, client.MergeFromWithOptions(originalHostedControlPlane, client.MergeFromWithOptimisticLock{})); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}
	originalHostedControlPlane = hostedControlPlane.DeepCopy()

	// Return early if deleted
	if !hostedControlPlane.DeletionTimestamp.IsZero() {

		if shouldCleanupCloudResources(r.Log, hostedControlPlane) {
			if err := r.destroyAWSDefaultSecurityGroup(ctx, hostedControlPlane); err != nil {
				if awsErrorCode(err) == "UnauthorizedOperation" {
					r.Log.Info("Skipping AWS default security group deletion because the operator is not authorized to delete it.")
				} else {
					return ctrl.Result{}, fmt.Errorf("failed to delete AWS default security group: %w", err)
				}
			}
			done, err := r.removeCloudResources(ctx, hostedControlPlane)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to ensure cloud resources are removed: %w", err)
			}
			if !done {
				return ctrl.Result{RequeueAfter: time.Minute}, nil
			}
		}

		if controllerutil.ContainsFinalizer(hostedControlPlane, finalizer) {
			originalHCP := hostedControlPlane.DeepCopy()
			controllerutil.RemoveFinalizer(hostedControlPlane, finalizer)
			if err := r.Patch(ctx, hostedControlPlane, client.MergeFromWithOptions(originalHCP, client.MergeFromWithOptimisticLock{})); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from cluster: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure the hostedControlPlane has a finalizer for cleanup
	if !controllerutil.ContainsFinalizer(hostedControlPlane, finalizer) {
		originalHCP := hostedControlPlane.DeepCopy()
		controllerutil.AddFinalizer(hostedControlPlane, finalizer)
		if err := r.Patch(ctx, hostedControlPlane, client.MergeFromWithOptions(originalHCP, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to hostedControlPlane: %w", err)
		}
	}

	if r.OperateOnReleaseImage != "" && r.OperateOnReleaseImage != hostedControlPlane.Spec.ReleaseImage {
		r.Log.Info("releaseImage is %s, but this operator is configured for %s, skipping reconciliation", hostedControlPlane.Spec.ReleaseImage, r.OperateOnReleaseImage)
		return ctrl.Result{}, nil
	}

	// Reconcile global configuration validation status
	{
		condition := metav1.Condition{
			Type:               string(hyperv1.ValidHostedControlPlaneConfiguration),
			ObservedGeneration: hostedControlPlane.Generation,
		}
		if err := r.validateConfigAndClusterCapabilities(hostedControlPlane); err != nil {
			condition.Status = metav1.ConditionFalse
			condition.Message = err.Error()
			condition.Reason = hyperv1.InsufficientClusterCapabilitiesReason
		} else {
			condition.Status = metav1.ConditionTrue
			condition.Message = "Configuration passes validation"
			condition.Reason = hyperv1.AsExpectedReason
		}
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, condition)
	}

	// Reconcile etcd cluster status
	{
		newCondition := metav1.Condition{
			Type:   string(hyperv1.EtcdAvailable),
			Status: metav1.ConditionUnknown,
			Reason: hyperv1.StatusUnknownReason,
		}
		switch hostedControlPlane.Spec.Etcd.ManagementType {
		case hyperv1.Managed:
			r.Log.Info("Reconciling etcd cluster status for managed strategy")
			sts := manifests.EtcdStatefulSet(hostedControlPlane.Namespace)
			if err := r.Get(ctx, client.ObjectKeyFromObject(sts), sts); err != nil {
				if apierrors.IsNotFound(err) {
					newCondition = metav1.Condition{
						Type:   string(hyperv1.EtcdAvailable),
						Status: metav1.ConditionFalse,
						Reason: hyperv1.EtcdStatefulSetNotFoundReason,
					}
				} else {
					return ctrl.Result{}, fmt.Errorf("failed to fetch etcd statefulset %s/%s: %w", sts.Namespace, sts.Name, err)
				}
			} else {
				conditionPtr, err := r.etcdStatefulSetCondition(ctx, sts)
				if err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to get etcd statefulset status: %w", err)
				}
				newCondition = *conditionPtr
			}
		case hyperv1.Unmanaged:
			r.Log.Info("Assuming Etcd cluster is running in unmanaged etcd strategy")
			newCondition = metav1.Condition{
				Type:    string(hyperv1.EtcdAvailable),
				Status:  metav1.ConditionTrue,
				Reason:  "EtcdRunning",
				Message: "Etcd cluster is assumed to be running in unmanaged state",
			}
		}
		newCondition.ObservedGeneration = hostedControlPlane.Generation
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, newCondition)
	}

	// Reconcile etcd restore status
	if hostedControlPlane.Spec.Etcd.ManagementType == hyperv1.Managed &&
		hostedControlPlane.Spec.Etcd.Managed != nil && len(hostedControlPlane.Spec.Etcd.Managed.Storage.RestoreSnapshotURL) > 0 {
		restoreCondition := meta.FindStatusCondition(hostedControlPlane.Status.Conditions, string(hyperv1.EtcdSnapshotRestored))
		if restoreCondition == nil {
			r.Log.Info("Reconciling etcd cluster restore status")
			sts := manifests.EtcdStatefulSet(hostedControlPlane.Namespace)
			if err := r.Get(ctx, client.ObjectKeyFromObject(sts), sts); err == nil {
				newCondition := metav1.Condition{}
				conditionPtr := r.etcdRestoredCondition(ctx, sts)
				if conditionPtr != nil {
					newCondition = *conditionPtr
					newCondition.ObservedGeneration = hostedControlPlane.Generation
					meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, newCondition)
				}
			}
		}
	}

	// Validate AWS KMS config
	r.validateAWSKMSConfig(ctx, hostedControlPlane)

	// Reconcile Kube APIServer status
	{
		newCondition := metav1.Condition{
			Type:   string(hyperv1.KubeAPIServerAvailable),
			Status: metav1.ConditionUnknown,
			Reason: hyperv1.StatusUnknownReason,
		}
		deployment := manifests.KASDeployment(hostedControlPlane.Namespace)
		if err := r.Get(ctx, client.ObjectKeyFromObject(deployment), deployment); err != nil {
			if apierrors.IsNotFound(err) {
				newCondition = metav1.Condition{
					Type:    string(hyperv1.KubeAPIServerAvailable),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.NotFoundReason,
					Message: "Kube APIServer deployment not found",
				}
			} else {
				return ctrl.Result{}, fmt.Errorf("failed to fetch Kube APIServer deployment %s/%s: %w", deployment.Namespace, deployment.Name, err)
			}
		} else {
			// Assume the deployment is unavailable until proven otherwise.
			newCondition = metav1.Condition{
				Type:   string(hyperv1.KubeAPIServerAvailable),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.StatusUnknownReason,
			}
			for _, cond := range deployment.Status.Conditions {
				if cond.Type == appsv1.DeploymentAvailable {
					if cond.Status == corev1.ConditionTrue {
						newCondition = metav1.Condition{
							Type:    string(hyperv1.KubeAPIServerAvailable),
							Status:  metav1.ConditionTrue,
							Reason:  hyperv1.AsExpectedReason,
							Message: "Kube APIServer deployment is available",
						}
					} else {
						newCondition = metav1.Condition{
							Type:    string(hyperv1.KubeAPIServerAvailable),
							Status:  metav1.ConditionFalse,
							Reason:  hyperv1.WaitingForAvailableReason,
							Message: "Waiting for Kube APIServer deployment to become available",
						}
					}
					break
				}
			}
		}
		newCondition.ObservedGeneration = hostedControlPlane.Generation
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, newCondition)
	}

	// Reconcile Degraded status
	{
		condition := metav1.Condition{
			Type:               string(hyperv1.HostedControlPlaneDegraded),
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.AsExpectedReason,
			ObservedGeneration: hostedControlPlane.Generation,
		}
		cpoManagedDeploymentList := &appsv1.DeploymentList{}
		if err := r.List(ctx, cpoManagedDeploymentList, client.MatchingLabels{
			config.ManagedByLabel: "control-plane-operator",
		}, client.InNamespace(hostedControlPlane.Namespace)); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to list managed deployments in namespace %s: %w", hostedControlPlane.Namespace, err)
			}
		}
		var errs []error
		sort.SliceStable(cpoManagedDeploymentList.Items, func(i, j int) bool {
			return cpoManagedDeploymentList.Items[i].Name < cpoManagedDeploymentList.Items[j].Name
		})
		for _, deployment := range cpoManagedDeploymentList.Items {
			if deployment.Status.UnavailableReplicas > 0 {
				errs = append(errs, fmt.Errorf("%s deployment has %d unavailable replicas", deployment.Name, deployment.Status.UnavailableReplicas))
			}
		}
		err := utilerrors.NewAggregate(errs)
		if err != nil {
			condition.Status = metav1.ConditionTrue
			condition.Reason = "UnavailableReplicas"
			condition.Message = err.Error()
		}
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, condition)
	}

	// Reconcile infrastructure status
	{
		r.Log.Info("Reconciling infrastructure status")
		newCondition := metav1.Condition{
			Type:   string(hyperv1.InfrastructureReady),
			Status: metav1.ConditionUnknown,
			Reason: hyperv1.StatusUnknownReason,
		}
		infraStatus, err := r.reconcileInfrastructureStatus(ctx, hostedControlPlane)
		if err != nil {
			newCondition = metav1.Condition{
				Type:    string(hyperv1.InfrastructureReady),
				Status:  metav1.ConditionUnknown,
				Reason:  hyperv1.InfraStatusFailureReason,
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
					Type:    string(hyperv1.InfrastructureReady),
					Status:  metav1.ConditionTrue,
					Message: hyperv1.AllIsWellMessage,
					Reason:  hyperv1.AsExpectedReason,
				}
				hostedControlPlane.Status.OAuthCallbackURLTemplate = fmt.Sprintf("https://%s:%d/oauthcallback/[identity-provider-name]", infraStatus.OAuthHost, infraStatus.OAuthPort)
			} else {
				message := "Cluster infrastructure is still provisioning"
				if len(infraStatus.Message) > 0 {
					message = infraStatus.Message
				}
				newCondition = metav1.Condition{
					Type:    string(hyperv1.InfrastructureReady),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.WaitingOnInfrastructureReadyReason,
					Message: message,
				}
				r.Log.Info("Infrastructure is not yet ready")
			}
		}
		newCondition.ObservedGeneration = hostedControlPlane.Generation
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, newCondition)
	}

	// Reconcile external DNS status
	{
		newCondition := metav1.Condition{
			Type:   string(hyperv1.ExternalDNSReachable),
			Status: metav1.ConditionUnknown,
			Reason: hyperv1.StatusUnknownReason,
		}

		kasExternalHostname := util.ServiceExternalDNSHostname(hostedControlPlane, hyperv1.APIServer)
		if kasExternalHostname != "" {
			if err := util.ResolveDNSHostname(ctx, kasExternalHostname); err != nil {
				newCondition = metav1.Condition{
					Type:    string(hyperv1.ExternalDNSReachable),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.ExternalDNSHostNotReachableReason,
					Message: err.Error(),
				}
			} else {
				newCondition = metav1.Condition{
					Type:    string(hyperv1.ExternalDNSReachable),
					Status:  metav1.ConditionTrue,
					Message: hyperv1.AllIsWellMessage,
					Reason:  hyperv1.AsExpectedReason,
				}
			}
		} else {
			newCondition.Message = "External DNS is not configured"
		}

		newCondition.ObservedGeneration = hostedControlPlane.Generation
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, newCondition)
	}

	// Reconcile hostedcontrolplane availability and Ready flag
	{
		infrastructureCondition := meta.FindStatusCondition(hostedControlPlane.Status.Conditions, string(hyperv1.InfrastructureReady))
		kubeConfigAvailable := hostedControlPlane.Status.KubeConfig != nil
		etcdCondition := meta.FindStatusCondition(hostedControlPlane.Status.Conditions, string(hyperv1.EtcdAvailable))
		kubeAPIServerCondition := meta.FindStatusCondition(hostedControlPlane.Status.Conditions, string(hyperv1.KubeAPIServerAvailable))

		status := metav1.ConditionFalse
		var reason, message string
		switch {
		case infrastructureCondition == nil && etcdCondition == nil && kubeAPIServerCondition == nil:
			reason = hyperv1.StatusUnknownReason
			message = ""
		case infrastructureCondition != nil && infrastructureCondition.Status == metav1.ConditionFalse:
			reason = infrastructureCondition.Reason
			message = infrastructureCondition.Message
		case !kubeConfigAvailable:
			reason = hyperv1.KubeconfigWaitingForCreateReason
			message = "Waiting for hosted control plane kubeconfig to be created"
		case etcdCondition != nil && etcdCondition.Status == metav1.ConditionFalse:
			reason = etcdCondition.Reason
			message = etcdCondition.Message
		case kubeAPIServerCondition != nil && kubeAPIServerCondition.Status == metav1.ConditionFalse:
			reason = kubeAPIServerCondition.Reason
			message = kubeAPIServerCondition.Message
		default:
			reason = hyperv1.AsExpectedReason
			message = ""
			status = metav1.ConditionTrue
		}
		hostedControlPlane.Status.Ready = (status == metav1.ConditionTrue)
		condition := metav1.Condition{
			Type:               string(hyperv1.HostedControlPlaneAvailable),
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: hostedControlPlane.Generation,
		}
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, condition)
	}

	kubeconfig := manifests.KASExternalKubeconfigSecret(hostedControlPlane.Namespace, hostedControlPlane.Spec.KubeConfig)
	if err := r.Get(ctx, client.ObjectKeyFromObject(kubeconfig), kubeconfig); err != nil {
		if !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}
	} else {
		hostedControlPlane.Status.KubeConfig = &hyperv1.KubeconfigSecretRef{
			Name: kubeconfig.Name,
			Key:  DefaultAdminKubeconfigKey,
		}
		if hostedControlPlane.Spec.KubeConfig != nil {
			hostedControlPlane.Status.KubeConfig.Key = hostedControlPlane.Spec.KubeConfig.Key
		}
	}

	kubeadminPassword := common.KubeadminPasswordSecret(hostedControlPlane.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(kubeadminPassword), kubeadminPassword); err != nil {
		if !apierrors.IsNotFound(err) {
			return reconcile.Result{}, fmt.Errorf("failed to get kubeadmin password: %w", err)
		}
	} else {
		hostedControlPlane.Status.KubeadminPassword = &corev1.LocalObjectReference{
			Name: kubeadminPassword.Name,
		}
	}

	hostedControlPlane.Status.Initialized = true

	meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, util.GenerateReconciliationActiveCondition(hostedControlPlane.Spec.PausedUntil, hostedControlPlane.Generation))
	// Always update status based on the current state of the world.
	if err := r.Client.Status().Patch(ctx, hostedControlPlane, client.MergeFromWithOptions(originalHostedControlPlane, client.MergeFromWithOptimisticLock{})); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}
	if isPaused, duration := util.IsReconciliationPaused(r.Log, hostedControlPlane.Spec.PausedUntil); isPaused {
		r.Log.Info("Reconciliation paused", "pausedUntil", *hostedControlPlane.Spec.PausedUntil)
		return ctrl.Result{
			RequeueAfter: duration,
		}, nil
	}

	// Perform the hosted control plane reconciliation
	result, err := r.update(ctx, hostedControlPlane)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update control plane: %w", err)
	}

	r.Log.Info("Successfully reconciled")
	return result, nil
}

func (r *HostedControlPlaneReconciler) validateConfigAndClusterCapabilities(hc *hyperv1.HostedControlPlane) error {
	for _, svc := range hc.Spec.Services {
		if svc.Type == hyperv1.Route && !r.ManagementClusterCapabilities.Has(capabilities.CapabilityRoute) {
			return fmt.Errorf("cluster does not support Routes, but service %q is exposed via a Route", svc.Service)
		}
	}
	return nil
}

func (r *HostedControlPlaneReconciler) LookupReleaseImage(ctx context.Context, hcp *hyperv1.HostedControlPlane) (*releaseinfo.ReleaseImage, error) {
	pullSecret := common.PullSecret(hcp.Namespace)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
		return nil, err
	}
	lookupCtx, lookupCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer lookupCancel()
	return r.ReleaseProvider.Lookup(lookupCtx, hcp.Spec.ReleaseImage, pullSecret.Data[corev1.DockerConfigJsonKey])
}

func (r *HostedControlPlaneReconciler) update(ctx context.Context, hostedControlPlane *hyperv1.HostedControlPlane) (reconcile.Result, error) {

	// Block here if the cluster configuration does not pass validation
	{
		validConfig := meta.FindStatusCondition(hostedControlPlane.Status.Conditions, string(hyperv1.ValidHostedControlPlaneConfiguration))
		if validConfig != nil && validConfig.Status == metav1.ConditionFalse {
			r.Log.Info("Configuration is invalid, reconciliation is blocked")
			return reconcile.Result{}, nil
		}
	}

	r.Log.Info("Looking up release image metadata", "image", hostedControlPlane.Spec.ReleaseImage)
	releaseImage, err := r.LookupReleaseImage(ctx, hostedControlPlane)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to look up release image metadata: %w", err)
	}
	componentVersions, err := releaseImage.ComponentVersions()
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("invalid component versions found in release info: %w", err)
	}
	r.Log.Info("Found release info for image", "releaseImage", hostedControlPlane.Spec.ReleaseImage, "info", releaseImage, "componentImages", len(releaseImage.ComponentImages()), "componentVersions", componentVersions)

	createOrUpdate := r.createOrUpdate(hostedControlPlane)

	if util.IsPrivateHCP(hostedControlPlane) {
		r.Log.Info("Removing private IngressController")
		// Ensure that if an ingress controller exists from a previous version, it is removed
		if err = r.reconcilePrivateIngressController(ctx, hostedControlPlane); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to reconcile private ingresscontroller: %w", err)
		}
	}

	r.Log.Info("Reconciling infrastructure services")
	if err := r.reconcileInfrastructure(ctx, hostedControlPlane, createOrUpdate); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to ensure infrastructure: %w", err)
	}

	// Block here until infra status reports readiness
	// TODO(dmace): This seems a bit heavy handed vs. making more granular bits no-op if
	// they don't have the specific required inputs
	infraStatus, err := r.reconcileInfrastructureStatus(ctx, hostedControlPlane)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to look up infra status: %w", err)
	}
	if !infraStatus.IsReady() {
		r.Log.Info("Waiting for infrastructure to be ready before proceeding")
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}

	var errs []error

	if err := r.reconcile(ctx, hostedControlPlane, createOrUpdate, releaseImage, infraStatus); err != nil {
		errs = append(errs, err)
	}

	r.Log.Info("Reconciling autoscaler")
	if err := r.reconcileAutoscaler(ctx, hostedControlPlane, releaseImage.ComponentImages(), createOrUpdate); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile autoscaler: %w", err))
	}

	r.Log.Info("Reconciling machine approver")
	if err := r.reconcileMachineApprover(ctx, hostedControlPlane, releaseImage.ComponentImages(), createOrUpdate); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile machine approver: %w", err))
	}

	r.Log.Info("Reconciling default security group")
	if err := r.reconcileDefaultSecurityGroup(ctx, hostedControlPlane); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile default security group"))
	}

	return ctrl.Result{}, utilerrors.NewAggregate(errs)
}

// useHCPRouter returns true if a dedicated common router is created for a HCP to handle ingress for the managed endpoints.
// This is true when the API input specifies intent for the following:
// 1 - AWS endpointAccess is private somehow (i.e. publicAndPrivate or private) or is public and configured with external DNS.
// 2 - When 1 is true, we recommend (and automate via CLI) ServicePublishingStrategy to be "Route" for all endpoints but the KAS
// which needs a dedicated Service type LB external to be exposed if no external DNS is supported.
// Otherwise, the Routes use the management cluster Domain and resolve through the default ingress controller.
func useHCPRouter(hostedControlPlane *hyperv1.HostedControlPlane) bool {
	return util.IsPrivateHCP(hostedControlPlane) || util.IsPublicKASWithDNS(hostedControlPlane)
}

func IsStorageAndCSIManaged(hostedControlPlane *hyperv1.HostedControlPlane) bool {
	if hostedControlPlane.Spec.Platform.Type == hyperv1.IBMCloudPlatform || hostedControlPlane.Spec.Platform.Type == hyperv1.PowerVSPlatform {
		return false
	}
	return true
}

func (r *HostedControlPlaneReconciler) reconcile(ctx context.Context, hostedControlPlane *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN, releaseImage *releaseinfo.ReleaseImage, infraStatus InfrastructureStatus) error {
	if useHCPRouter(hostedControlPlane) {
		r.Log.Info("Reconciling router")
		if err := r.reconcileRouter(ctx, hostedControlPlane, releaseImage, createOrUpdate, util.IsRouteKAS(hostedControlPlane), infraStatus.InternalHCPRouterHost, infraStatus.ExternalHCPRouterHost); err != nil {
			return fmt.Errorf("failed to reconcile router: %w", err)
		}
	}

	r.Log.Info("Reconciling ignition server")
	if err := ignitionserver.ReconcileIgnitionServer(ctx,
		r.Client,
		createOrUpdate,
		releaseImage.ComponentImages()[util.CPOImageName],
		hostedControlPlane,
		r.DefaultIngressDomain,
		// The healthz handler was added before the CPO started to mange te ignition server and its the same binary,
		// so we know it always exists here.
		true,
		r.ReleaseProvider.GetRegistryOverrides(),
		r.ManagementClusterCapabilities.Has(capabilities.CapabilitySecurityContextConstraint),
		config.OwnerRefFrom(hostedControlPlane),
	); err != nil {
		return fmt.Errorf("failed to reconcile ignition server: %w", err)
	}

	// Reconcile default service account
	r.Log.Info("Reconciling default service account")
	if err := r.reconcileDefaultServiceAccount(ctx, hostedControlPlane, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile default service account: %w", err)
	}

	// Reconcile PKI
	if _, exists := hostedControlPlane.Annotations[hyperv1.DisablePKIReconciliationAnnotation]; !exists {
		r.Log.Info("Reconciling PKI")
		if err := r.reconcilePKI(ctx, hostedControlPlane, infraStatus, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile PKI: %w", err)
		}
	}

	// Reconcile Cloud Provider Config
	r.Log.Info("Reconciling cloud provider config")
	if err := r.reconcileCloudProviderConfig(ctx, hostedControlPlane, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile cloud provider config: %w", err)
	}

	r.Log.Info("Looking up observed configuration")
	observedConfig := &globalconfig.ObservedConfig{}
	if err := globalconfig.ReadObservedConfig(ctx, r.Client, observedConfig, hostedControlPlane.Namespace); err != nil {
		return fmt.Errorf("failed to read observed global config: %w", err)
	}

	// Reconcile etcd
	r.Log.Info("Reconciling Etcd")

	switch hostedControlPlane.Spec.Etcd.ManagementType {
	case hyperv1.Managed:
		if err := r.reconcileManagedEtcd(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile etcd: %w", err)
		}
	case hyperv1.Unmanaged:
		if err := r.reconcileUnmanagedEtcd(ctx, hostedControlPlane, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile etcd: %w", err)
		}
	default:
		return fmt.Errorf("unrecognized etcd management type: %s", hostedControlPlane.Spec.Etcd.ManagementType)
	}

	// Reconcile Konnectivity
	r.Log.Info("Reconciling Konnectivity")
	if err := r.reconcileKonnectivity(ctx, hostedControlPlane, releaseImage, infraStatus, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity: %w", err)
	}

	// Reconcile kube apiserver
	r.Log.Info("Reconciling Kube API Server")
	if err := r.reconcileKubeAPIServer(ctx, hostedControlPlane, releaseImage, infraStatus.APIHost, infraStatus.APIPort, infraStatus.OAuthHost, infraStatus.OAuthPort, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile kube apiserver: %w", err)
	}

	// Block until kube apiserver is fully ready to enforce upgrade order of version skew policy
	// https://kubernetes.io/releases/version-skew-policy/#supported-component-upgrade-order
	ready, err := util.IsDeploymentReady(ctx, r, manifests.KASDeployment(hostedControlPlane.Namespace))
	if err != nil {
		return fmt.Errorf("failed to check kube apiserver availability: %w", err)
	}
	if !ready {
		r.Log.Info("Waiting for kube apiserver deployment to become ready")
		return nil
	}

	// Reconcile kube controller manager
	r.Log.Info("Reconciling Kube Controller Manager")
	if err := r.reconcileKubeControllerManager(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile kube controller manager: %w", err)
	}

	// Reconcile kube scheduler
	r.Log.Info("Reconciling Kube Scheduler")
	if err := r.reconcileKubeScheduler(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile kube controller manager: %w", err)
	}

	// Reconcile openshift apiserver
	r.Log.Info("Reconciling OpenShift API Server")
	if err := r.reconcileOpenShiftAPIServer(ctx, hostedControlPlane, observedConfig, releaseImage, infraStatus.OpenShiftAPIHost, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile openshift apiserver: %w", err)
	}

	// Block until openshift apiserver is fully ready to enforce upgrade order of version skew policy
	// https://github.com/openshift/enhancements/blob/master/enhancements/update/eus-upgrades-mvp.md
	ready, err = util.IsDeploymentReady(ctx, r, manifests.OpenShiftAPIServerDeployment(hostedControlPlane.Namespace))
	if err != nil {
		return fmt.Errorf("failed to check openshift apiserver availability: %w", err)
	}
	if !ready {
		r.Log.Info("Waiting for openshift apiserver deployment to become ready")
		return nil
	}

	// Reconcile openshift oauth apiserver
	r.Log.Info("Reconciling OpenShift OAuth API Server")
	if err := r.reconcileOpenShiftOAuthAPIServer(ctx, hostedControlPlane, observedConfig, releaseImage, infraStatus.OauthAPIServerHost, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver: %w", err)
	}

	// Reconcile oauth server
	r.Log.Info("Reconciling OAuth Server")
	if err = r.reconcileOAuthServer(ctx, hostedControlPlane, releaseImage, infraStatus.OAuthHost, infraStatus.OAuthPort, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver: %w", err)
	}

	// Reconcile openshift controller manager
	r.Log.Info("Reconciling OpenShift Controller Manager")
	if err = r.reconcileOpenShiftControllerManager(ctx, hostedControlPlane, observedConfig, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile openshift controller manager: %w", err)
	}

	// Reconcile openshift route controller manager
	r.Log.Info("Reconciling OpenShift Route Controller Manager")
	if err = r.reconcileOpenShiftRouteControllerManager(ctx, hostedControlPlane, observedConfig, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile openshift route controller manager: %w", err)
	}

	// Reconcile cluster policy controller
	r.Log.Info("Reconciling Cluster Policy Controller")
	if err = r.reconcileClusterPolicyController(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile cluster policy controller: %w", err)
	}

	// Reconcile cluster version operator
	r.Log.Info("Reconciling Cluster Version Operator")
	if err = r.reconcileClusterVersionOperator(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile cluster version operator: %w", err)
	}

	r.Log.Info("Reconciling ClusterNetworkOperator")
	if err := r.reconcileClusterNetworkOperator(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile cluster network operator: %w", err)
	}

	r.Log.Info("Reconciling Cluster Node Tuning Operator")
	if err := r.reconcileClusterNodeTuningOperator(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile cluster node tuning operator: %w", err)
	}

	r.Log.Info("Reconciling DNSOperator")
	if err := r.reconcileDNSOperator(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile DNS operator: %w", err)
	}

	r.Log.Info("Reconciling IngressOperator")
	if err := r.reconcileIngressOperator(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile ingress operator: %w", err)
	}

	// Reconcile hosted cluster config operator
	r.Log.Info("Reconciling Hosted Cluster Config Operator")
	if err = r.reconcileHostedClusterConfigOperator(ctx, hostedControlPlane, releaseImage, infraStatus, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile hosted cluster config operator: %w", err)
	}

	// Reconcile cloud controller manager
	r.Log.Info("Reconciling Cloud Controller Manager")
	if err := r.reconcileCloudControllerManager(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile cloud controller manager: %w", err)
	}

	// Reconcile OLM
	r.Log.Info("Reconciling OLM")
	if err = r.reconcileOperatorLifecycleManager(ctx, hostedControlPlane, releaseImage, infraStatus.PackageServerAPIAddress, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile olm: %w", err)
	}

	// Reconcile image registry operator
	r.Log.Info("Reconciling Image Registry Operator")
	if err = r.reconcileImageRegistryOperator(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile image registry operator: %w", err)
	}

	if IsStorageAndCSIManaged(hostedControlPlane) {
		// Reconcile cluster storage operator
		r.Log.Info("Reconciling cluster storage operator")
		if err = r.reconcileClusterStorageOperator(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile cluster storage operator: %w", err)
		}
	}

	// Reconcile Ignition
	r.Log.Info("Reconciling core machine configs")
	if err = r.reconcileCoreIgnitionConfig(ctx, hostedControlPlane, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile ignition: %w", err)
	}

	// Reconcle machine config server config
	r.Log.Info("Reconciling machine config server config")
	if err = r.reconcileMachineConfigServerConfig(ctx, hostedControlPlane, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile mcs config: %w", err)
	}

	// Reconcile kubeadmin password
	r.Log.Info("Reconciling kubeadmin password secret")
	explicitOauthConfig := hostedControlPlane.Spec.Configuration != nil && hostedControlPlane.Spec.Configuration.OAuth != nil
	if err := r.reconcileKubeadminPassword(ctx, hostedControlPlane, explicitOauthConfig, createOrUpdate); err != nil {
		return fmt.Errorf("failed to ensure control plane: %w", err)
	}

	if IsStorageAndCSIManaged(hostedControlPlane) {
		// Reconcile cloud csi driver
		r.Log.Info("Reconciling CSI Driver")
		if err := r.reconcileCSIDriver(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile csi driver: %w", err)
		}

		// Reconcile CSI snapshot controller operator
		r.Log.Info("Reconciling CSI snapshot controller operator")
		if err := r.reconcileCSISnapshotControllerOperator(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile CSI snapshot controller operator: %w", err)
		}
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileDefaultServiceAccount(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	defaultSA := common.DefaultServiceAccount(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, defaultSA, func() error {
		util.EnsurePullSecret(defaultSA, common.PullSecret(hcp.Namespace).Name)
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileAPIServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	serviceStrategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.APIServer)
	if serviceStrategy == nil {
		return errors.New("APIServer service strategy not specified")
	}
	p := kas.NewKubeAPIServerServiceParams(hcp)
	apiServerService := manifests.KubeAPIServerService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, apiServerService, func() error {
		return kas.ReconcileService(apiServerService, serviceStrategy, p.OwnerReference, p.APIServerPort, p.APIServerListenPort, p.AllowedCIDRBlocks, util.IsPublicHCP(hcp), util.IsPrivateHCP(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile API server service: %w", err)
	}

	if serviceStrategy.Type == hyperv1.Route {
		externalPublicRoute := manifests.KubeAPIServerExternalPublicRoute(hcp.Namespace)
		externalPrivateRoute := manifests.KubeAPIServerExternalPrivateRoute(hcp.Namespace)
		if util.IsPublicHCP(hcp) {
			// Remove the external private route if it exists
			err := r.Get(ctx, client.ObjectKeyFromObject(externalPrivateRoute), externalPrivateRoute)
			if err != nil {
				if !apierrors.IsNotFound(err) {
					return fmt.Errorf("failed to check whether apiserver external private route exists: %w", err)
				}
			} else {
				if err := r.Delete(ctx, externalPrivateRoute); err != nil {
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
			err := r.Get(ctx, client.ObjectKeyFromObject(externalPublicRoute), externalPublicRoute)
			if err != nil {
				if !apierrors.IsNotFound(err) {
					return fmt.Errorf("failed to check whether apiserver external public route exists: %w", err)
				}
			} else {
				if err := r.Delete(ctx, externalPublicRoute); err != nil {
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

func (r *HostedControlPlaneReconciler) reconcileKonnectivityServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := konnectivity.NewKonnectivityServiceParams(hcp)
	serviceStrategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.Konnectivity)
	if serviceStrategy == nil {
		//lint:ignore ST1005 Konnectivity is proper name
		return fmt.Errorf("Konnectivity service strategy not specified")
	}
	konnectivityServerService := manifests.KonnectivityServerService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, konnectivityServerService, func() error {
		return konnectivity.ReconcileServerService(konnectivityServerService, p.OwnerRef, serviceStrategy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile Konnectivity service: %w", err)
	}
	if serviceStrategy.Type != hyperv1.Route {
		return nil
	}
	konnectivityRoute := manifests.KonnectivityServerRoute(hcp.Namespace)
	if util.IsPrivateHCP(hcp) {
		if _, err := createOrUpdate(ctx, r.Client, konnectivityRoute, func() error {
			return konnectivity.ReconcileInternalRoute(konnectivityRoute, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile Konnectivity server internal route: %w", err)
		}
	} else {
		if _, err := createOrUpdate(ctx, r.Client, konnectivityRoute, func() error {
			hostname := ""
			if serviceStrategy.Route != nil {
				hostname = serviceStrategy.Route.Hostname
			}
			return konnectivity.ReconcileExternalRoute(konnectivityRoute, p.OwnerRef, hostname, r.DefaultIngressDomain)
		}); err != nil {
			return fmt.Errorf("failed to reconcile Konnectivity server external route: %w", err)
		}
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOAuthServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	serviceStrategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.OAuthServer)
	if serviceStrategy == nil {
		return fmt.Errorf("OAuthServer service strategy not specified")
	}
	p := oauth.NewOAuthServiceParams(hcp)
	oauthServerService := manifests.OauthServerService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, oauthServerService, func() error {
		return oauth.ReconcileService(oauthServerService, p.OwnerRef, serviceStrategy)
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
		err := r.Get(ctx, client.ObjectKeyFromObject(oauthExternalPrivateRoute), oauthExternalPrivateRoute)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to check whether OAuth external private route exists: %w", err)
			}
		} else {
			if err := r.Delete(ctx, oauthExternalPrivateRoute); err != nil {
				return fmt.Errorf("failed to delete OAuth external private route: %w", err)
			}
		}
		// Reconcile the external public route
		if _, err := createOrUpdate(ctx, r.Client, oauthExternalPublicRoute, func() error {
			hostname := ""
			if serviceStrategy.Route != nil {
				hostname = serviceStrategy.Route.Hostname
			}
			return oauth.ReconcileExternalPublicRoute(oauthExternalPublicRoute, p.OwnerRef, hostname, r.DefaultIngressDomain)
		}); err != nil {
			return fmt.Errorf("failed to reconcile OAuth external public route: %w", err)
		}
	} else {
		// Remove the external route if it exists
		err := r.Get(ctx, client.ObjectKeyFromObject(oauthExternalPublicRoute), oauthExternalPublicRoute)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to check whether OAuth external public route exists: %w", err)
			}
		} else {
			if err := r.Delete(ctx, oauthExternalPublicRoute); err != nil {
				return fmt.Errorf("failed to delete OAuth external public route: %w", err)
			}
		}
		// Reconcile the external private route
		if _, err := createOrUpdate(ctx, r.Client, oauthExternalPrivateRoute, func() error {
			hostname := ""
			if serviceStrategy.Route != nil {
				hostname = serviceStrategy.Route.Hostname
			}
			return oauth.ReconcileExternalPrivateRoute(oauthExternalPrivateRoute, p.OwnerRef, hostname, r.DefaultIngressDomain)
		}); err != nil {
			return fmt.Errorf("failed to reconcile OAuth external private route: %w", err)
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

func (r *HostedControlPlaneReconciler) reconcileOpenshiftAPIServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	svc := manifests.OpenshiftAPIServerService(hcp.Namespace)
	p := oapi.NewOpenShiftAPIServerServiceParams(hcp)
	if _, err := createOrUpdate(ctx, r.Client, svc, func() error {
		return oapi.ReconcileOpenShiftAPIService(svc, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile OpenShift API server service: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOAuthAPIServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	svc := manifests.OauthAPIServerService(hcp.Namespace)
	p := oapi.NewOpenShiftAPIServerServiceParams(hcp)
	if _, err := createOrUpdate(ctx, r.Client, svc, func() error {
		return oapi.ReconcileOAuthAPIService(svc, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile OAuth API server service: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOLMPackageServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
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

func (r *HostedControlPlaneReconciler) reconcileHCPRouterServices(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	exposeKASThroughRouter := util.IsRouteKAS(hcp)
	// Create the Service type LB internal for private endpoints.
	pubSvc := manifests.RouterPublicService(hcp.Namespace)
	if util.IsPrivateHCP(hcp) {
		svc := manifests.PrivateRouterService(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r.Client, svc, func() error {
			return ingress.ReconcileRouterService(svc, util.InternalAPIPortWithDefault(hcp, config.DefaultAPIServerPort), true, true)
		}); err != nil {
			return fmt.Errorf("failed to reconcile private router service: %w", err)
		}
		if !util.IsPublicHCP(hcp) {
			// Remove the public router Service if it exists
			err := r.Get(ctx, client.ObjectKeyFromObject(pubSvc), pubSvc)
			if err != nil {
				if !apierrors.IsNotFound(err) {
					return fmt.Errorf("failed to check whether public router service exists: %w", err)
				}
			} else {
				if err := r.Delete(ctx, pubSvc); err != nil {
					return fmt.Errorf("failed to delete public router service: %w", err)
				}
			}
		}
	}

	// When Public access endpoint we need to create a Service type LB external for the KAS.
	if util.IsPublicHCP(hcp) && exposeKASThroughRouter {
		if _, err := createOrUpdate(ctx, r.Client, pubSvc, func() error {
			return ingress.ReconcileRouterService(pubSvc, util.InternalAPIPortWithDefault(hcp, config.DefaultAPIServerPort), false, util.IsPrivateHCP(hcp))
		}); err != nil {
			return fmt.Errorf("failed to reconcile router service: %w", err)
		}
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileInfrastructure(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	if hcp.Spec.Services == nil {
		return fmt.Errorf("service publishing strategy undefined")
	}
	if err := r.reconcileAPIServerService(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile API server service: %w", err)
	}
	if err := r.reconcileKonnectivityServerService(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile Konnectivity servier service: %w", err)
	}
	if err := r.reconcileOAuthServerService(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile OAuth server service: %w", err)
	}
	if err := r.reconcileOpenshiftAPIServerService(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile OpenShift api service: %w", err)
	}
	if err := r.reconcileOAuthAPIServerService(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile OpenShift OAuth api service: %w", err)
	}
	if err := r.reconcileOLMPackageServerService(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile OLM PackageServer service: %w", err)
	}
	if err := r.reconcileHCPRouterServices(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile HCP router services: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) defaultReconcileInfrastructureStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (InfrastructureStatus, error) {
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
	if infraStatus.OAuthHost, infraStatus.OAuthPort, msg, err = r.reconcileOAuthServiceStatus(ctx, hcp); err != nil {
		errs = append(errs, err)
	}
	if len(msg) > 0 {
		messages = append(messages, msg)
	}
	if infraStatus.OpenShiftAPIHost, err = r.reconcileOpenShiftAPIServerServiceStatus(ctx, hcp); err != nil {
		errs = append(errs, err)
	}
	if infraStatus.OauthAPIServerHost, err = r.reconcileOAuthAPIServerServiceStatus(ctx, hcp); err != nil {
		errs = append(errs, err)
	}
	if infraStatus.PackageServerAPIAddress, err = r.reconcileOLMPackageServerServiceStatus(ctx, hcp); err != nil {
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

func (r *HostedControlPlaneReconciler) reconcileInternalRouterServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (host string, needed bool, message string, err error) {
	if !util.IsPrivateHCP(hcp) {
		return
	}
	return r.reconcileRouterServiceStatus(ctx, manifests.PrivateRouterService(hcp.Namespace), events.NewMessageCollector(ctx, r.Client))
}

func (r *HostedControlPlaneReconciler) reconcileExternalRouterServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (host string, needed bool, message string, err error) {
	if !util.IsPublicHCP(hcp) || !util.IsRouteKAS(hcp) {
		return
	}
	return r.reconcileRouterServiceStatus(ctx, manifests.RouterPublicService(hcp.Namespace), events.NewMessageCollector(ctx, r.Client))
}

func (r *HostedControlPlaneReconciler) reconcileRouterServiceStatus(ctx context.Context, svc *corev1.Service, messageCollector events.MessageCollector) (host string, needed bool, message string, err error) {
	needed = true
	if err = r.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
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

func (r *HostedControlPlaneReconciler) reconcileAPIServerServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (host string, port int32, message string, err error) {
	serviceStrategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.APIServer)
	if serviceStrategy == nil {
		return "", 0, "", errors.New("APIServer service strategy not specified")
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
	if err = r.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		if apierrors.IsNotFound(err) {
			err = nil
			return
		}
		err = fmt.Errorf("failed to get kube apiserver service: %w", err)
		return
	}
	p := kas.NewKubeAPIServerServiceParams(hcp)
	return kas.ReconcileServiceStatus(svc, serviceStrategy, p.APIServerPort, events.NewMessageCollector(ctx, r.Client))
}

func (r *HostedControlPlaneReconciler) reconcileKonnectivityServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (host string, port int32, message string, err error) {
	serviceStrategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.Konnectivity)
	if serviceStrategy == nil {
		err = fmt.Errorf("konnectivity service strategy not specified")
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
	var route *routev1.Route
	if serviceStrategy.Type == hyperv1.Route {
		route = manifests.KonnectivityServerRoute(hcp.Namespace)
		if err = r.Get(ctx, client.ObjectKeyFromObject(route), route); err != nil {
			if apierrors.IsNotFound(err) {
				err = nil
				return
			}
			err = fmt.Errorf("failed to get konnectivity route: %w", err)
			return
		}
	}
	return konnectivity.ReconcileServerServiceStatus(svc, route, serviceStrategy, events.NewMessageCollector(ctx, r.Client))
}

func (r *HostedControlPlaneReconciler) reconcileOAuthServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (host string, port int32, message string, err error) {
	serviceStrategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.OAuthServer)
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
		if util.IsPublicHCP(hcp) {
			route = manifests.OauthServerExternalPublicRoute(hcp.Namespace)
			if err = r.Get(ctx, client.ObjectKeyFromObject(route), route); err != nil {
				if apierrors.IsNotFound(err) {
					err = nil
					return
				}
				err = fmt.Errorf("failed to get oauth external route: %w", err)
				return
			}
		} else {
			route = manifests.OauthServerExternalPrivateRoute(hcp.Namespace)
			if err = r.Get(ctx, client.ObjectKeyFromObject(route), route); err != nil {
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

func (r *HostedControlPlaneReconciler) reconcileKubeadminPassword(ctx context.Context, hcp *hyperv1.HostedControlPlane, explicitOauthConfig bool, createOrUpdate upsert.CreateOrUpdateFN) error {
	if explicitOauthConfig {
		return nil
	}
	var kubeadminPassword string
	kubeadminPasswordSecret := common.KubeadminPasswordSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kubeadminPasswordSecret, func() error {
		return reconcileKubeadminPasswordSecret(kubeadminPasswordSecret, hcp, &kubeadminPassword)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kubeadminPasswordSecret: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcilePKI(ctx context.Context, hcp *hyperv1.HostedControlPlane, infraStatus InfrastructureStatus, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := pki.NewPKIParams(hcp, infraStatus.APIHost, infraStatus.OAuthHost, infraStatus.KonnectivityHost)

	// Root CA
	rootCASecret := manifests.RootCASecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, rootCASecret, func() error {
		return pki.ReconcileRootCA(rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile root CA: %w", err)
	}

	rootCAConfigMap := manifests.RootCAConfigMap(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, rootCAConfigMap, func() error {
		return pki.ReconcileRootCAConfigMap(rootCAConfigMap, p.OwnerRef, rootCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile root CA configmap: %w", err)
	}

	// Etcd signer for all the etcd-related certs
	etcdSignerSecret := manifests.EtcdSignerSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, etcdSignerSecret, func() error {
		return pki.ReconcileEtcdSignerSecret(etcdSignerSecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd signer CA secret: %w", err)
	}

	etcdSignerCM := manifests.EtcdSignerCAConfigMap(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, etcdSignerCM, func() error {
		return pki.ReconcileEtcdSignerConfigMap(etcdSignerCM, p.OwnerRef, etcdSignerSecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd signer CA configmap: %w", err)
	}

	// Etcd client secret
	etcdClientSecret := manifests.EtcdClientSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, etcdClientSecret, func() error {
		return pki.ReconcileEtcdClientSecret(etcdClientSecret, etcdSignerSecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd client secret: %w", err)
	}

	// Etcd server secret
	etcdServerSecret := manifests.EtcdServerSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, etcdServerSecret, func() error {
		return pki.ReconcileEtcdServerSecret(etcdServerSecret, etcdSignerSecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd server secret: %w", err)
	}

	// Etcd peer secret
	etcdPeerSecret := manifests.EtcdPeerSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, etcdPeerSecret, func() error {
		return pki.ReconcileEtcdPeerSecret(etcdPeerSecret, etcdSignerSecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd peer secret: %w", err)
	}

	// Etcd metrics signer
	// Etcd signer for all the etcd-related certs
	etcdMetricsSignerSecret := manifests.EtcdMetricsSignerSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, etcdMetricsSignerSecret, func() error {
		return pki.ReconcileEtcdMetricsSignerSecret(etcdMetricsSignerSecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd signer CA secret: %w", err)
	}

	etcdMetricsSignerCM := manifests.EtcdMetricsSignerCAConfigMap(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, etcdMetricsSignerCM, func() error {
		return pki.ReconcileEtcdMetricsSignerConfigMap(etcdMetricsSignerCM, p.OwnerRef, etcdMetricsSignerSecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd signer CA configmap: %w", err)
	}

	// Etcd client secret
	etcdMetricsClientSecret := manifests.EtcdMetricsClientSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, etcdMetricsClientSecret, func() error {
		return pki.ReconcileEtcdMetricsClientSecret(etcdMetricsClientSecret, etcdMetricsSignerSecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd client secret: %w", err)
	}

	// KAS server secret
	kasServerSecret := manifests.KASServerCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kasServerSecret, func() error {
		return pki.ReconcileKASServerCertSecret(kasServerSecret, rootCASecret, p.OwnerRef, p.ExternalAPIAddress, p.InternalAPIAddress, p.ServiceCIDR)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kas server secret: %w", err)
	}

	if err := r.setupKASClientSigners(ctx, hcp, p, createOrUpdate, rootCASecret); err != nil {
		return err
	}

	// Service account signing key secret
	serviceAccountSigningKeySecret := manifests.ServiceAccountSigningKeySecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, serviceAccountSigningKeySecret, func() error {
		return pki.ReconcileServiceAccountSigningKeySecret(serviceAccountSigningKeySecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server service account key secret: %w", err)
	}

	// OpenShift APIServer
	openshiftAPIServerCertSecret := manifests.OpenShiftAPIServerCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, openshiftAPIServerCertSecret, func() error {
		return pki.ReconcileOpenShiftAPIServerCertSecret(openshiftAPIServerCertSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kas admin client secret: %w", err)
	}

	// OpenShift OAuth APIServer
	openshiftOAuthAPIServerCertSecret := manifests.OpenShiftOAuthAPIServerCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, openshiftOAuthAPIServerCertSecret, func() error {
		return pki.ReconcileOpenShiftOAuthAPIServerCertSecret(openshiftOAuthAPIServerCertSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver cert: %w", err)
	}

	// OpenShift ControllerManager Cert
	openshiftControllerManagerCertSecret := manifests.OpenShiftControllerManagerCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, openshiftControllerManagerCertSecret, func() error {
		return pki.ReconcileOpenShiftControllerManagerCertSecret(openshiftControllerManagerCertSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift controller manager cert: %w", err)
	}

	// OpenShift Route ControllerManager Cert
	openshiftRouteControllerManagerCertSecret := manifests.OpenShiftRouteControllerManagerCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, openshiftRouteControllerManagerCertSecret, func() error {
		return pki.ReconcileOpenShiftControllerManagerCertSecret(openshiftRouteControllerManagerCertSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift route controller manager cert: %w", err)
	}

	// Cluster Policy Controller Cert
	clusterPolicyControllerCertSecret := manifests.ClusterPolicyControllerCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, clusterPolicyControllerCertSecret, func() error {
		return pki.ReconcileOpenShiftControllerManagerCertSecret(clusterPolicyControllerCertSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster policy controller cert: %w", err)
	}

	konnectivitySigner := manifests.KonnectivitySignerSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, konnectivitySigner, func() error {
		return pki.ReconcileKonnectivitySignerSecret(konnectivitySigner, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity signer secret: %v", err)
	}

	konnectivityCACM := manifests.KonnectivityCAConfigMap(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, konnectivityCACM, func() error {
		return pki.ReconcileKonnectivityConfigMap(konnectivityCACM, p.OwnerRef, konnectivitySigner)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity CA config map: %v", err)
	}

	// Konnectivity Server Cert
	konnectivityServerSecret := manifests.KonnectivityServerSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, konnectivityServerSecret, func() error {
		return pki.ReconcileKonnectivityServerSecret(konnectivityServerSecret, konnectivitySigner, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity server cert: %w", err)
	}

	// Konnectivity Cluster Cert
	konnectivityClusterSecret := manifests.KonnectivityClusterSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, konnectivityClusterSecret, func() error {
		return pki.ReconcileKonnectivityClusterSecret(konnectivityClusterSecret, konnectivitySigner, p.OwnerRef, p.ExternalKconnectivityAddress)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity cluster cert: %w", err)
	}

	// Konnectivity Client Cert
	konnectivityClientSecret := manifests.KonnectivityClientSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, konnectivityClientSecret, func() error {
		return pki.ReconcileKonnectivityClientSecret(konnectivityClientSecret, konnectivitySigner, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity client cert: %w", err)
	}

	// Konnectivity Agent Cert
	konnectivityAgentSecret := manifests.KonnectivityAgentSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, konnectivityAgentSecret, func() error {
		return pki.ReconcileKonnectivityAgentSecret(konnectivityAgentSecret, konnectivitySigner, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity agent cert: %w", err)
	}

	// Ingress Cert
	ingressCert := manifests.IngressCert(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, ingressCert, func() error {
		return pki.ReconcileIngressCert(ingressCert, rootCASecret, p.OwnerRef, p.IngressSubdomain)
	}); err != nil {
		return fmt.Errorf("failed to reconcile ingress cert secret: %w", err)
	}

	// OAuth server Cert
	oauthServerCert := manifests.OpenShiftOAuthServerCert(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, oauthServerCert, func() error {
		return pki.ReconcileOAuthServerCert(oauthServerCert, rootCASecret, p.OwnerRef, p.ExternalOauthAddress)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth cert secret: %w", err)
	}

	// OAuth master CA Bundle
	bundleSources := []*corev1.Secret{oauthServerCert}
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.APIServer != nil {
		for _, namedCert := range hcp.Spec.Configuration.APIServer.ServingCerts.NamedCertificates {
			if namedCert.ServingCertificate.Name == "" {
				continue
			}
			certSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: namedCert.ServingCertificate.Name, Namespace: hcp.Namespace}}
			if err := r.Get(ctx, client.ObjectKeyFromObject(certSecret), certSecret); err != nil {
				return fmt.Errorf("failed to get named certificate %s: %w", certSecret.Name, err)
			}
			bundleSources = append(bundleSources, certSecret)
		}
	}
	oauthMasterCABundle := manifests.OpenShiftOAuthMasterCABundle(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, oauthMasterCABundle, func() error {
		return pki.ReconcileOAuthMasterCABundle(oauthMasterCABundle, p.OwnerRef, bundleSources)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth cert secret: %w", err)
	}
	// MCS Cert
	machineConfigServerCert := manifests.MachineConfigServerCert(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, machineConfigServerCert, func() error {
		return pki.ReconcileMachineConfigServerCert(machineConfigServerCert, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile machine config server cert secret: %w", err)
	}

	// Cluster Node Tuning Operator metrics Serving Cert
	NodeTuningOperatorServingCert := manifests.ClusterNodeTuningOperatorServingCertSecret(hcp.Namespace)
	NodeTuningOperatorService := manifests.ClusterNodeTuningOperatorMetricsService(hcp.Namespace)
	removeServiceCAAnnotationAndSecret(ctx, r.Client, NodeTuningOperatorService, NodeTuningOperatorServingCert)
	if _, err := createOrUpdate(ctx, r, NodeTuningOperatorServingCert, func() error {
		return pki.ReconcileNodeTuningOperatorServingCertSecret(NodeTuningOperatorServingCert, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile node tuning operator serving cert: %w", err)
	}

	// OLM PackageServer Cert
	packageServerCertSecret := manifests.OLMPackageServerCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, packageServerCertSecret, func() error {
		return pki.ReconcileOLMPackageServerCertSecret(packageServerCertSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile packageserver cert: %w", err)
	}

	// OLM Catalog Operator Serving Cert
	catalogOperatorServingCert := manifests.OLMCatalogOperatorServingCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, catalogOperatorServingCert, func() error {
		return pki.ReconcileOLMCatalogOperatorServingCertSecret(catalogOperatorServingCert, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile olm catalog operator serving cert: %w", err)
	}

	// OLM Operator Serving Cert
	olmOperatorServingCert := manifests.OLMOperatorServingCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, olmOperatorServingCert, func() error {
		return pki.ReconcileOLMOperatorServingCertSecret(olmOperatorServingCert, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile olm operator serving cert: %w", err)
	}

	// Image Registry Operator Serving Cert
	imageRegistryOperatorServingCert := manifests.ImageRegistryOperatorServingCert(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, imageRegistryOperatorServingCert, func() error {
		return pki.ReconcileRegistryOperatorServingCert(imageRegistryOperatorServingCert, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile image registry operator serving cert: %w", err)
	}

	kcmServerSecret := manifests.KCMServerCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kcmServerSecret, func() error {
		return pki.ReconcileKCMServerSecret(kcmServerSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile olm operator serving cert: %w", err)
	}

	cvoServerCert := manifests.ClusterVersionOperatorServerCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, cvoServerCert, func() error {
		return pki.ReconcileCVOServerSecret(cvoServerCert, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cvo serving cert: %w", err)
	}

	csiSnapshotWebhookSecret := manifests.CSISnapshotControllerWebhookCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, csiSnapshotWebhookSecret, func() error {
		return pki.ReconcileCSISnapshotWebhookTLS(csiSnapshotWebhookSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile CSI snapshot webhook cert: %w", err)
	}

	if hcp.Spec.Platform.Type == hyperv1.AWSPlatform {
		awsPodIdentityWebhookServingCert := manifests.AWSPodIdentityWebhookServingCert(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, awsPodIdentityWebhookServingCert, func() error {
			return pki.ReconcileAWSPodIdentityWebhookServingCert(awsPodIdentityWebhookServingCert, rootCASecret, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile %s secret: %w", awsPodIdentityWebhookServingCert.Name, err)
		}
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileCSIDriver(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	switch hcp.Spec.Platform.Type {
	// Most csi drivers should be laid down by the Cluster Storage Operator (CSO) instead of
	// the hcp operator. Only KubeVirt is unique at the moment.
	case hyperv1.KubevirtPlatform:
		err := kubevirtcsi.ReconcileInfra(r.Client, hcp, ctx, createOrUpdate, releaseImage.ComponentImages())
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileCloudProviderConfig(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		p := aws.NewAWSParams(hcp)
		awsProviderConfig := manifests.AWSProviderConfig(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, awsProviderConfig, func() error {
			return p.ReconcileCloudConfig(awsProviderConfig)
		}); err != nil {
			return fmt.Errorf("failed to reconcile aws provider config: %w", err)
		}
	case hyperv1.AzurePlatform:
		credentialsSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: hcp.Spec.Platform.Azure.Credentials.Name}}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(credentialsSecret), credentialsSecret); err != nil {
			return fmt.Errorf("failed to get Azure credentials secret: %w", err)
		}

		// We need different configs for KAS/KCM and Kubelet in Nodes
		cfg := manifests.AzureProviderConfig(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, cfg, func() error {
			return azure.ReconcileCloudConfig(cfg, hcp, credentialsSecret)
		}); err != nil {
			return fmt.Errorf("failed to reconcile Azure cloud config: %w", err)
		}
		withSecrets := manifests.AzureProviderConfigWithCredentials(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, withSecrets, func() error {
			return azure.ReconcileCloudConfigWithCredentials(withSecrets, hcp, credentialsSecret)
		}); err != nil {
			return fmt.Errorf("failed to reconcile Azure cloud config with credentials: %w", err)
		}
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileManagedEtcd(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := etcd.NewEtcdParams(hcp, releaseImage.ComponentImages())

	discoveryService := manifests.EtcdDiscoveryService(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, discoveryService, func() error {
		return etcd.ReconcileDiscoveryService(discoveryService, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd discovery service: %w", err)
	} else {
		r.Log.Info("reconciled etcd discovery service", "result", result)
	}

	clientService := manifests.EtcdClientService(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, clientService, func() error {
		return etcd.ReconcileClientService(clientService, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd client service: %w", err)
	} else {
		r.Log.Info("reconciled etcd client service", "result", result)
	}

	serviceMonitor := manifests.EtcdServiceMonitor(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, serviceMonitor, func() error {
		return etcd.ReconcileServiceMonitor(serviceMonitor, p.OwnerRef, hcp.Spec.ClusterID, r.MetricsSet)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd servicemonitor: %w", err)
	} else {
		r.Log.Info("reconciled etcd servicemonitor", "result", result)
	}

	pdb := manifests.EtcdPodDisruptionBudget(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, pdb, func() error {
		return etcd.ReconcilePodDisruptionBudget(pdb, p)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd pdb: %w", err)
	} else {
		r.Log.Info("reconciled etcd pdb", "result", result)
	}

	statefulSet := manifests.EtcdStatefulSet(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, statefulSet, func() error {
		return etcd.ReconcileStatefulSet(statefulSet, p)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd statefulset: %w", err)
	} else {
		r.Log.Info("reconciled etcd statefulset", "result", result)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileUnmanagedEtcd(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	// reconcile client secret over
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
	_, err := createOrUpdate(ctx, r.Client, kubeComponentEtcdClientSecret, func() error {
		if kubeComponentEtcdClientSecret.Data == nil {
			kubeComponentEtcdClientSecret.Data = map[string][]byte{}
		}
		kubeComponentEtcdClientSecret.Data = src.Data
		kubeComponentEtcdClientSecret.Type = corev1.SecretTypeOpaque
		return nil
	})
	return err
}

func (r *HostedControlPlaneReconciler) reconcileKonnectivity(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, infraStatus InfrastructureStatus, createOrUpdate upsert.CreateOrUpdateFN) error {
	r.Log.Info("Reconciling Konnectivity")
	p := konnectivity.NewKonnectivityParams(hcp, releaseImage.ComponentImages(), infraStatus.KonnectivityHost, infraStatus.KonnectivityPort, r.SetDefaultSecurityContext)
	serverDeployment := manifests.KonnectivityServerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, serverDeployment, func() error {
		return konnectivity.ReconcileServerDeployment(serverDeployment, p.OwnerRef, p.ServerDeploymentConfig, p.KonnectivityServerImage)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity server deployment: %w", err)
	}
	serverLocalService := manifests.KonnectivityServerLocalService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, serverLocalService, func() error {
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
	if _, err := createOrUpdate(ctx, r, agentDeployment, func() error {
		return konnectivity.ReconcileAgentDeployment(agentDeployment, p.OwnerRef, p.AgentDeploymentConfig, p.KonnectivityAgentImage, ips)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity agent deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileKubeAPIServer(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, apiAddress string, apiPort int32, oauthAddress string, oauthPort int32, createOrUpdate upsert.CreateOrUpdateFN) error {
	ocpVersion, err := semver.Parse(releaseImage.Version())
	if err != nil {
		return fmt.Errorf("failed to parse release version: %w", err)
	}

	p := kas.NewKubeAPIServerParams(ctx, hcp, releaseImage.ComponentImages(), apiAddress, apiPort, oauthAddress, oauthPort, r.SetDefaultSecurityContext)

	rootCA := manifests.RootCAConfigMap(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return fmt.Errorf("failed to get root ca cert secret: %w", err)
	}

	clientCertSecret := manifests.SystemAdminClientCertSecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(clientCertSecret), clientCertSecret); err != nil {
		return fmt.Errorf("failed to get admin client cert secret: %w", err)
	}
	bootstrapClientCertSecret := manifests.KASMachineBootstrapClientCertSecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(bootstrapClientCertSecret), bootstrapClientCertSecret); err != nil {
		return fmt.Errorf("failed to get bootstrap client cert secret: %w", err)
	}

	serviceKubeconfigSecret := manifests.KASServiceKubeconfigSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, serviceKubeconfigSecret, func() error {
		return kas.ReconcileServiceKubeconfigSecret(serviceKubeconfigSecret, clientCertSecret, rootCA, p.OwnerRef, p.InternalPort)
	}); err != nil {
		return fmt.Errorf("failed to reconcile service admin kubeconfig secret: %w", err)
	}

	// The client used by CAPI machine controller expects the kubeconfig to follow this naming and key convention
	// https://github.com/kubernetes-sigs/cluster-api/blob/5c85a0a01ee44ecf7c8a3c3fdc867a88af87d73c/util/secret/secret.go#L29-L33
	capiKubeconfigSecret := manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID)
	if _, err := createOrUpdate(ctx, r, capiKubeconfigSecret, func() error {
		// TODO(alberto): This secret is currently using the cluster-admin kubeconfig for the guest cluster.
		// We should create a separate kubeconfig with a tight set of permissions for it to use.
		return kas.ReconcileServiceCAPIKubeconfigSecret(capiKubeconfigSecret, clientCertSecret, rootCA, p.OwnerRef, p.InternalPort)
	}); err != nil {
		return fmt.Errorf("failed to reconcile CAPI service admin kubeconfig secret: %w", err)
	}

	localhostKubeconfigSecret := manifests.KASLocalhostKubeconfigSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, localhostKubeconfigSecret, func() error {
		return kas.ReconcileLocalhostKubeconfigSecret(localhostKubeconfigSecret, clientCertSecret, rootCA, p.OwnerRef, p.APIServerPort)
	}); err != nil {
		return fmt.Errorf("failed to reconcile localhost kubeconfig secret: %w", err)
	}

	externalKubeconfigSecret := manifests.KASExternalKubeconfigSecret(hcp.Namespace, hcp.Spec.KubeConfig)
	if _, err := createOrUpdate(ctx, r, externalKubeconfigSecret, func() error {
		return kas.ReconcileExternalKubeconfigSecret(externalKubeconfigSecret, clientCertSecret, rootCA, p.OwnerRef, p.ExternalURL(), p.ExternalKubeconfigKey())
	}); err != nil {
		return fmt.Errorf("failed to reconcile external kubeconfig secret: %w", err)
	}

	bootstrapKubeconfigSecret := manifests.KASBootstrapKubeconfigSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, bootstrapKubeconfigSecret, func() error {
		if util.IsPrivateHCP(hcp) {
			return kas.ReconcileBootstrapKubeconfigSecret(bootstrapKubeconfigSecret, bootstrapClientCertSecret, rootCA, p.OwnerRef, p.InternalURL())
		}
		return kas.ReconcileBootstrapKubeconfigSecret(bootstrapKubeconfigSecret, bootstrapClientCertSecret, rootCA, p.OwnerRef, p.ExternalURL())
	}); err != nil {
		return fmt.Errorf("failed to reconcile bootstrap kubeconfig secret: %w", err)
	}

	kubeAPIServerAuditConfig := manifests.KASAuditConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kubeAPIServerAuditConfig, func() error {
		return kas.ReconcileAuditConfig(kubeAPIServerAuditConfig, p.OwnerRef, p.AuditPolicyConfig())
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server audit config: %w", err)
	}

	kubeAPIServerConfig := manifests.KASConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kubeAPIServerConfig, func() error {
		return kas.ReconcileConfig(kubeAPIServerConfig,
			p.OwnerRef,
			p.ConfigParams(),
			ocpVersion,
		)
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server config: %w", err)
	}

	kubeAPIServerEgressSelectorConfig := manifests.KASEgressSelectorConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kubeAPIServerEgressSelectorConfig, func() error {
		return kas.ReconcileEgressSelectorConfig(kubeAPIServerEgressSelectorConfig, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server egress selector config: %w", err)
	}

	oauthMetadata := manifests.KASOAuthMetadata(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, oauthMetadata, func() error {
		return kas.ReconcileOauthMetadata(oauthMetadata, p.OwnerRef, p.ExternalOAuthAddress, p.ExternalOAuthPort)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth metadata: %w", err)
	}

	var aesCBCActiveKey, aesCBCBackupKey []byte

	if hcp.Spec.SecretEncryption != nil {
		r.Log.Info("Reconciling kube-apiserver secret encryption configuration")
		encryptionConfigFile := manifests.KASSecretEncryptionConfigFile(hcp.Namespace)
		switch hcp.Spec.SecretEncryption.Type {
		case hyperv1.AESCBC:
			if hcp.Spec.SecretEncryption.AESCBC == nil || len(hcp.Spec.SecretEncryption.AESCBC.ActiveKey.Name) == 0 {
				return fmt.Errorf("aescbc metadata not specified")
			}
			activeKeySecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hcp.Spec.SecretEncryption.AESCBC.ActiveKey.Name,
					Namespace: hcp.Namespace,
				},
			}
			if err := r.Get(ctx, client.ObjectKeyFromObject(activeKeySecret), activeKeySecret); err != nil {
				return fmt.Errorf("failed to get aescbc active secret: %w", err)
			}
			if _, ok := activeKeySecret.Data[hyperv1.AESCBCKeySecretKey]; !ok {
				return fmt.Errorf("aescbc key field '%s' in active key secret not specified", hyperv1.AESCBCKeySecretKey)
			}
			aesCBCActiveKey = activeKeySecret.Data[hyperv1.AESCBCKeySecretKey]
			if hcp.Spec.SecretEncryption.AESCBC.BackupKey != nil && len(hcp.Spec.SecretEncryption.AESCBC.BackupKey.Name) > 0 {
				backupKeySecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      hcp.Spec.SecretEncryption.AESCBC.BackupKey.Name,
						Namespace: hcp.Namespace,
					},
				}
				if err := r.Get(ctx, client.ObjectKeyFromObject(backupKeySecret), backupKeySecret); err != nil {
					return fmt.Errorf("failed to get aescbc backup key secret: %w", err)
				}
				if _, ok := backupKeySecret.Data[hyperv1.AESCBCKeySecretKey]; !ok {
					return fmt.Errorf("aescbc key field %s in backup key secret not specified", hyperv1.AESCBCKeySecretKey)
				}
				aesCBCBackupKey = backupKeySecret.Data[hyperv1.AESCBCKeySecretKey]
			}
			if _, err := createOrUpdate(ctx, r, encryptionConfigFile, func() error {
				return kas.ReconcileAESCBCEncryptionConfig(encryptionConfigFile, p.OwnerRef, aesCBCActiveKey, aesCBCBackupKey)
			}); err != nil {
				return fmt.Errorf("failed to reconcile aes encryption config secret: %w", err)
			}
		case hyperv1.KMS:
			if hcp.Spec.SecretEncryption.KMS == nil {
				return fmt.Errorf("kms metadata not specified")
			}
			if _, err := createOrUpdate(ctx, r, encryptionConfigFile, func() error {
				return kas.ReconcileKMSEncryptionConfig(encryptionConfigFile, p.OwnerRef, hcp.Spec.SecretEncryption.KMS)
			}); err != nil {
				return fmt.Errorf("failed to reconcile kms encryption config secret: %w", err)
			}
		}
	}

	openshiftAuthenticatorCertSecret := manifests.OpenshiftAuthenticatorCertSecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(openshiftAuthenticatorCertSecret), openshiftAuthenticatorCertSecret); err != nil {
		return fmt.Errorf("failed to get authenticator cert secret: %w", err)
	}
	authenticationTokenWebhookConfigSecret := manifests.KASAuthenticationTokenWebhookConfigSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, authenticationTokenWebhookConfigSecret, func() error {
		return kas.ReconcileAuthenticationTokenWebhookConfigSecret(
			authenticationTokenWebhookConfigSecret,
			p.OwnerRef,
			openshiftAuthenticatorCertSecret,
			rootCA,
		)
	}); err != nil {
		return fmt.Errorf("failed to reconcile authentication token webhook config: %w", err)
	}

	pdb := manifests.KASPodDisruptionBudget(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, pdb, func() error {
		return kas.ReconcilePodDisruptionBudget(pdb, p)
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server pdb: %w", err)
	} else {
		r.Log.Info("Reconciled api server pdb", "result", result)
	}

	serviceMonitor := manifests.KASServiceMonitor(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, serviceMonitor, func() error {
		return kas.ReconcileServiceMonitor(serviceMonitor, int(p.APIServerPort), config.OwnerRefFrom(hcp), hcp.Spec.ClusterID, r.MetricsSet)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kas service monitor: %w", err)
	} else {
		r.Log.Info("Reconciled api server service monitor", "result", result)
	}

	recordingRules := manifests.ControlPlaneRecordingRules(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, recordingRules, func() error {
		kas.ReconcileRecordingRules(recordingRules, hcp.Spec.ClusterID)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile control plane recording rules: %w", err)
	} else {
		r.Log.Info("Reconciled control plane recording rules", "result", result)
	}

	if hcp.Spec.Platform.Type == hyperv1.AWSPlatform {
		csrSigner := manifests.CSRSignerCASecret(hcp.Namespace)
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(csrSigner), csrSigner); err != nil {
			return err
		}

		podIdentityWebhookSecret := manifests.AWSPodIdentityWebhookKubeconfig(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, podIdentityWebhookSecret, func() error {
			return pki.ReconcileServiceAccountKubeconfig(podIdentityWebhookSecret, csrSigner, rootCA, hcp, "openshift-authentication", "aws-pod-identity-webhook")
		}); err != nil {
			return fmt.Errorf("failecd to reconcile aws pod identity webhook kubeconfig secret: %w", err)
		}
	}

	kubeAPIServerDeployment := manifests.KASDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kubeAPIServerDeployment, func() error {
		return kas.ReconcileKubeAPIServerDeployment(kubeAPIServerDeployment,
			hcp,
			p.OwnerRef,
			p.DeploymentConfig,
			p.NamedCertificates(),
			p.CloudProvider,
			p.CloudProviderConfig,
			p.CloudProviderCreds,
			p.Images,
			kubeAPIServerConfig,
			p.AuditWebhookRef,
			aesCBCActiveKey,
			aesCBCBackupKey,
			p.APIServerPort,
		)
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server deployment: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileKubeControllerManager(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := kcm.NewKubeControllerManagerParams(ctx, hcp, releaseImage.ComponentImages(), r.SetDefaultSecurityContext)

	service := manifests.KCMService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, service, func() error {
		return kcm.ReconcileService(service, config.OwnerRefFrom(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile kcm service: %w", err)
	}

	rootCAConfigMap := manifests.RootCAConfigMap(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(rootCAConfigMap), rootCAConfigMap); err != nil {
		return fmt.Errorf("failed to fetch combined ca configmap: %w", err)
	}

	// TODO: the following is weird, it adds the rootCA to the service-ca configmap
	//       why would anyone want that?
	serviceServingCA := manifests.ServiceServingCA(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, serviceServingCA, func() error {
		return kcm.ReconcileKCMServiceServingCA(serviceServingCA, rootCAConfigMap, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kcm serving ca: %w", err)
	}

	recyclerConfig := manifests.RecyclerConfigMap(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, recyclerConfig, func() error {
		return kcm.ReconcileRecyclerConfig(recyclerConfig, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kcm recycler config: %w", err)
	}

	clientCertSecret := manifests.KubeControllerManagerClientCertSecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(clientCertSecret), clientCertSecret); err != nil {
		return fmt.Errorf("failed to get KCM client cert secret: %w", err)
	}

	kcmKubeconfigSecret := manifests.KCMKubeconfigSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kcmKubeconfigSecret, func() error {
		return kas.ReconcileServiceKubeconfigSecret(
			kcmKubeconfigSecret,
			clientCertSecret,
			rootCAConfigMap,
			p.OwnerRef,
			util.InternalAPIPortWithDefault(hcp, config.DefaultAPIServerPort),
		)
	}); err != nil {
		return fmt.Errorf("failed to reconcile secret '%s/%s': %v", kcmKubeconfigSecret.Namespace, kcmKubeconfigSecret.Name, err)
	}

	serviceMonitor := manifests.KCMServiceMonitor(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, serviceMonitor, func() error {
		return kcm.ReconcileServiceMonitor(serviceMonitor, config.OwnerRefFrom(hcp), hcp.Spec.ClusterID, r.MetricsSet)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kcm service monitor: %w", err)
	} else {
		r.Log.Info("Reconciled kcm service monitor", "result", result)
	}

	kcmConfig := manifests.KCMConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kcmConfig, func() error {
		return kcm.ReconcileConfig(kcmConfig, serviceServingCA, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kcm config: %w", err)
	}

	kcmDeployment := manifests.KCMDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kcmDeployment, func() error {
		return kcm.ReconcileDeployment(kcmDeployment, kcmConfig, serviceServingCA, p, util.APIPort(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile kcm deployment: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileKubeScheduler(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := scheduler.NewKubeSchedulerParams(ctx, hcp, releaseImage.ComponentImages(), r.SetDefaultSecurityContext)

	rootCA := manifests.RootCAConfigMap(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return fmt.Errorf("failed to get root ca cert secret: %w", err)
	}

	clientCertSecret := manifests.KubeSchedulerClientCertSecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(clientCertSecret), clientCertSecret); err != nil {
		return fmt.Errorf("failed to get KCM client cert secret: %w", err)
	}

	schedulerKubeconfigSecret := manifests.SchedulerKubeconfigSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, schedulerKubeconfigSecret, func() error {
		return kas.ReconcileServiceKubeconfigSecret(
			schedulerKubeconfigSecret,
			clientCertSecret,
			rootCA,
			p.OwnerRef,
			util.InternalAPIPortWithDefault(hcp, config.DefaultAPIServerPort),
		)
	}); err != nil {
		return fmt.Errorf("failed to reconcile secret '%s/%s': %v", schedulerKubeconfigSecret.Namespace, schedulerKubeconfigSecret.Name, err)
	}

	schedulerConfig := manifests.SchedulerConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, schedulerConfig, func() error {
		return scheduler.ReconcileConfig(schedulerConfig, p.OwnerRef, p.SchedulerProfile())
	}); err != nil {
		return fmt.Errorf("failed to reconcile scheduler config: %w", err)
	}

	schedulerDeployment := manifests.SchedulerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, schedulerDeployment, func() error {
		return scheduler.ReconcileDeployment(schedulerDeployment, p.OwnerRef, p.DeploymentConfig, p.HyperkubeImage, p.FeatureGates(), p.SchedulerPolicy(), p.AvailabilityProberImage, util.APIPort(hcp), p.CipherSuites(), p.MinTLSVersion(), p.DisableProfiling)
	}); err != nil {
		return fmt.Errorf("failed to reconcile scheduler deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOpenShiftAPIServer(ctx context.Context, hcp *hyperv1.HostedControlPlane, observedConfig *globalconfig.ObservedConfig, releaseImage *releaseinfo.ReleaseImage, serviceClusterIP string, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := oapi.NewOpenShiftAPIServerParams(hcp, observedConfig, releaseImage.ComponentImages(), r.SetDefaultSecurityContext)
	oapicfg := manifests.OpenShiftAPIServerConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, oapicfg, func() error {
		return oapi.ReconcileConfig(oapicfg, p.AuditWebhookRef, p.OwnerRef, p.EtcdURL, p.IngressDomain(), p.MinTLSVersion(), p.CipherSuites(), p.Image, p.Project)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift apiserver config: %w", err)
	}

	auditCfg := manifests.OpenShiftAPIServerAuditConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, auditCfg, func() error {
		return oapi.ReconcileAuditConfig(auditCfg, p.OwnerRef, p.AuditPolicyConfig())
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift apiserver audit config: %w", err)
	}

	pdb := manifests.OpenShiftAPIServerPodDisruptionBudget(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, pdb, func() error {
		return oapi.ReconcilePodDisruptionBudget(pdb, p)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift apiserver pdb: %w", err)
	} else {
		r.Log.Info("Reconciled openshift apiserver pdb", "result", result)
	}

	serviceMonitor := manifests.OpenShiftAPIServerServiceMonitor(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, serviceMonitor, func() error {
		return oapi.ReconcileServiceMonitor(serviceMonitor, p.OwnerRef, hcp.Spec.ClusterID, r.MetricsSet)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift apiserver servicemonitor: %w", err)
	} else {
		r.Log.Info("reconciled openshift apiserver servicemonitor", "result", result)
	}

	deployment := manifests.OpenShiftAPIServerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return oapi.ReconcileDeployment(deployment, p.AuditWebhookRef, p.OwnerRef, oapicfg, p.OpenShiftAPIServerDeploymentConfig, p.OpenShiftAPIServerImage, p.ProxyImage, p.EtcdURL, p.AvailabilityProberImage, util.APIPort(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift apiserver deployment: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOpenShiftOAuthAPIServer(ctx context.Context, hcp *hyperv1.HostedControlPlane, observedConfig *globalconfig.ObservedConfig, releaseImage *releaseinfo.ReleaseImage, serviceClusterIP string, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := oapi.NewOpenShiftAPIServerParams(hcp, observedConfig, releaseImage.ComponentImages(), r.SetDefaultSecurityContext)
	auditCfg := manifests.OpenShiftOAuthAPIServerAuditConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, auditCfg, func() error {
		return oapi.ReconcileAuditConfig(auditCfg, p.OwnerRef, p.AuditPolicyConfig())
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver audit config: %w", err)
	}

	pdb := manifests.OpenShiftOAuthAPIServerDisruptionBudget(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, pdb, func() error {
		return oapi.ReconcileOpenShiftOAuthAPIServerPodDisruptionBudget(pdb, p.OAuthAPIServerDeploymentParams(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver pdb: %w", err)
	} else {
		r.Log.Info("Reconciled openshift oauth apiserver pdb", "result", result)
	}

	deployment := manifests.OpenShiftOAuthAPIServerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return oapi.ReconcileOAuthAPIServerDeployment(deployment, p.OwnerRef, p.OAuthAPIServerDeploymentParams(hcp), util.APIPort(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOAuthServer(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, oauthHost string, oauthPort int32, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := oauth.NewOAuthServerParams(hcp, releaseImage.ComponentImages(), oauthHost, oauthPort, r.SetDefaultSecurityContext)

	sessionSecret := manifests.OAuthServerServiceSessionSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, sessionSecret, func() error {
		return oauth.ReconcileSessionSecret(sessionSecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth session secret: %w", err)
	}

	loginTemplate := manifests.OAuthServerDefaultLoginTemplateSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, loginTemplate, func() error {
		return oauth.ReconcileLoginTemplateSecret(loginTemplate, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile login template secret: %w", err)
	}

	providersTemplate := manifests.OAuthServerDefaultProviderSelectionTemplateSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, providersTemplate, func() error {
		return oauth.ReconcileProviderSelectionTemplateSecret(providersTemplate, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile provider selection template secret: %w", err)
	}

	errorTemplate := manifests.OAuthServerDefaultErrorTemplateSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, errorTemplate, func() error {
		return oauth.ReconcileErrorTemplateSecret(errorTemplate, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile error template secret: %w", err)
	}

	oauthServingCert := manifests.OpenShiftOAuthServerCert(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(oauthServingCert), oauthServingCert); err != nil {
		return fmt.Errorf("cannot get oauth serving cert: %w", err)
	}

	oauthConfig := manifests.OAuthServerConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, oauthConfig, func() error {
		return oauth.ReconcileOAuthServerConfig(ctx, oauthConfig, p.OwnerRef, r.Client, p.ConfigParams(oauthServingCert))
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth server config: %w", err)
	}

	pdb := manifests.OAuthServerPodDisruptionBudget(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, pdb, func() error {
		return oauth.ReconcilePodDisruptionBudget(pdb, p)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth pdb: %w", err)
	} else {
		r.Log.V(2).Info("Reconciled oauth pdb", "result", result)
	}

	deployment := manifests.OAuthServerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return oauth.ReconcileDeployment(ctx, r, deployment, p.OwnerRef, oauthConfig, p.OAuthServerImage, p.DeploymentConfig, p.IdentityProviders(), p.OauthConfigOverrides, p.AvailabilityProberImage, util.APIPort(hcp), p.NamedCertificates(), p.Socks5ProxyImage, p.NoProxy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOpenShiftControllerManager(ctx context.Context, hcp *hyperv1.HostedControlPlane, observedConfig *globalconfig.ObservedConfig, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := ocm.NewOpenShiftControllerManagerParams(hcp, observedConfig, releaseImage.ComponentImages(), r.SetDefaultSecurityContext)
	config := manifests.OpenShiftControllerManagerConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, config, func() error {
		return ocm.ReconcileOpenShiftControllerManagerConfig(config, p.OwnerRef, p.DeployerImage, p.DockerBuilderImage, p.MinTLSVersion(), p.CipherSuites(), p.Image, p.Build, p.Network)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift controller manager config: %w", err)
	}

	service := manifests.OpenShiftControllerService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, service, func() error {
		return ocm.ReconcileService(service, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift controller manager service: %w", err)
	}

	serviceMonitor := manifests.OpenShiftControllerServiceMonitor(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, serviceMonitor, func() error {
		return ocm.ReconcileServiceMonitor(serviceMonitor, p.OwnerRef, hcp.Spec.ClusterID, r.MetricsSet)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift controller manager service monitor: %w", err)
	}

	deployment := manifests.OpenShiftControllerManagerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return ocm.ReconcileDeployment(deployment, p.OwnerRef, p.OpenShiftControllerManagerImage, config, p.DeploymentConfig)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift controller manager deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOpenShiftRouteControllerManager(ctx context.Context, hcp *hyperv1.HostedControlPlane, observedConfig *globalconfig.ObservedConfig, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := routecm.NewOpenShiftRouteControllerManagerParams(hcp, observedConfig, releaseImage.ComponentImages(), r.SetDefaultSecurityContext)
	config := manifests.OpenShiftControllerManagerConfig(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(config), config); err != nil {
		return fmt.Errorf("failed to get openshift controller manager config: %w", err)
	}

	service := manifests.OpenShiftRouteControllerManagerService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, service, func() error {
		return routecm.ReconcileService(service, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift route controller manager service: %w", err)
	}

	serviceMonitor := manifests.OpenShiftRouteControllerManagerServiceMonitor(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, serviceMonitor, func() error {
		return routecm.ReconcileServiceMonitor(serviceMonitor, p.OwnerRef, hcp.Spec.ClusterID, r.MetricsSet)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift route controller manager service monitor: %w", err)
	}

	deployment := manifests.OpenShiftRouteControllerManagerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return routecm.ReconcileDeployment(deployment, p.OpenShiftControllerManagerImage, config, p.DeploymentConfig)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift route controller manager deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileClusterPolicyController(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := clusterpolicy.NewClusterPolicyControllerParams(hcp, releaseImage.ComponentImages(), r.SetDefaultSecurityContext)

	config := manifests.ClusterPolicyControllerConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, config, func() error {
		return clusterpolicy.ReconcileClusterPolicyControllerConfig(config, p.OwnerRef, p.MinTLSVersion(), p.CipherSuites())
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster policy controller config: %w", err)
	}

	deployment := manifests.ClusterPolicyControllerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return clusterpolicy.ReconcileDeployment(deployment, p.OwnerRef, p.Image, p.DeploymentConfig, p.AvailabilityProberImage, util.APIPort(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster policy controller deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileClusterVersionOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := cvo.NewCVOParams(hcp, releaseImage.ComponentImages(), r.SetDefaultSecurityContext)

	service := manifests.ClusterVersionOperatorService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, service, func() error {
		return cvo.ReconcileService(service, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster version operator service: %w", err)
	}

	serviceMonitor := manifests.ClusterVersionOperatorServiceMonitor(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, serviceMonitor, func() error {
		return cvo.ReconcileServiceMonitor(serviceMonitor, p.OwnerRef, hcp.Spec.ClusterID, r.MetricsSet)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster version operator service monitor: %w", err)
	}

	deployment := manifests.ClusterVersionOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return cvo.ReconcileDeployment(deployment, p.OwnerRef, p.DeploymentConfig, p.Image, p.CLIImage, p.AvailabilityProberImage, p.ClusterID, util.APIPort(hcp), p.PlatformType)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster version operator deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileClusterNetworkOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := cno.NewParams(hcp, releaseImage.Version(), releaseImage.ComponentImages(), r.SetDefaultSecurityContext, r.DefaultIngressDomain)

	sa := manifests.ClusterNetworkOperatorServiceAccount(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, sa, func() error {
		return cno.ReconcileServiceAccount(sa, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster network operator serviceaccount: %w", err)
	}

	role := manifests.ClusterNetworkOperatorRole(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, role, func() error {
		return cno.ReconcileRole(role, p.OwnerRef, hcp.Spec.Networking.NetworkType)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster network operator role: %w", err)
	}

	rb := manifests.ClusterNetworkOperatorRoleBinding(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, rb, func() error {
		return cno.ReconcileRoleBinding(rb, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster network operator rolebinding: %w", err)
	}

	deployment := manifests.ClusterNetworkOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return cno.ReconcileDeployment(deployment, p, util.APIPort(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster network operator deployment: %w", err)
	}

	// CNO manages overall multus-admission-controller deployment. CPO manages restarts.
	multusDeployment := manifests.MultusAdmissionControllerDeployment(hcp.Namespace)
	if err := cno.SetRestartAnnotationAndPatch(ctx, r.Client, multusDeployment, p.DeploymentConfig); err != nil {
		return fmt.Errorf("failed to restart multus admission controller: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileClusterNodeTuningOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := nto.NewParams(hcp, releaseImage.Version(), releaseImage.ComponentImages(), r.SetDefaultSecurityContext)

	metricsService := manifests.ClusterNodeTuningOperatorMetricsService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, metricsService, func() error {
		return nto.ReconcileClusterNodeTuningOperatorMetricsService(metricsService, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile node tuning operator metrics service: %w", err)
	}

	serviceMonitor := manifests.ClusterNodeTuningOperatorServiceMonitor(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, serviceMonitor, func() error {
		return nto.ReconcileClusterNodeTuningOperatorServiceMonitor(serviceMonitor, hcp.Spec.ClusterID, r.MetricsSet, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile node tuning operator service monitor: %w", err)
	}

	sa := manifests.ClusterNodeTuningOperatorServiceAccount(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, sa, func() error {
		return nto.ReconcileServiceAccount(sa, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster node tuning operator serviceaccount: %w", err)
	}

	role := manifests.ClusterNodeTuningOperatorRole(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, role, func() error {
		return nto.ReconcileRole(role, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster node tuning operator role: %w", err)
	}

	rb := manifests.ClusterNodeTuningOperatorRoleBinding(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, rb, func() error {
		return nto.ReconcileRoleBinding(rb, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster node tuning operator rolebinding: %w", err)
	}

	deployment := manifests.ClusterNodeTuningOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return nto.ReconcileDeployment(deployment, p)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster node tuning operator deployment: %w", err)
	}
	return nil
}

// reconcileDNSOperator ensures that the management cluster has the expected DNS
// operator deployment and kubeconfig secret for the hosted cluster.
func (r *HostedControlPlaneReconciler) reconcileDNSOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := dnsoperator.NewParams(hcp, releaseImage.Version(), releaseImage.ComponentImages(), r.SetDefaultSecurityContext)

	rootCA := manifests.RootCAConfigMap(hcp.Namespace)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return err
	}

	csrSigner := manifests.CSRSignerCASecret(hcp.Namespace)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(csrSigner), csrSigner); err != nil {
		return err
	}

	kubeconfig := manifests.DNSOperatorKubeconfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kubeconfig, func() error {
		return pki.ReconcileServiceAccountKubeconfig(kubeconfig, csrSigner, rootCA, hcp, "openshift-dns-operator", "dns-operator")
	}); err != nil {
		return fmt.Errorf("failed to reconcile dnsoperator kubeconfig: %w", err)
	}

	deployment := manifests.DNSOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		dnsoperator.ReconcileDeployment(deployment, p, util.APIPort(hcp))
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile dnsoperator deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileIngressOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := ingressoperator.NewParams(hcp, releaseImage.Version(), releaseImage.ComponentImages(), r.SetDefaultSecurityContext, hcp.Spec.Platform.Type)

	rootCA := manifests.RootCAConfigMap(hcp.Namespace)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return err
	}

	csrSigner := manifests.CSRSignerCASecret(hcp.Namespace)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(csrSigner), csrSigner); err != nil {
		return err
	}

	kubeconfig := manifests.IngressOperatorKubeconfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kubeconfig, func() error {
		return pki.ReconcileServiceAccountKubeconfig(kubeconfig, csrSigner, rootCA, hcp, "openshift-ingress-operator", "ingress-operator")
	}); err != nil {
		return fmt.Errorf("failed to reconcile ingressoperator kubeconfig: %w", err)
	}

	deployment := manifests.IngressOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		ingressoperator.ReconcileDeployment(deployment, p, util.APIPort(hcp))
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile ingressoperator deployment: %w", err)
	}

	pm := manifests.IngressOperatorPodMonitor(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, pm, func() error {
		ingressoperator.ReconcilePodMonitor(pm, hcp.Spec.ClusterID, r.MetricsSet)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile ingressoperator pod monitor: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOperatorLifecycleManager(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, packageServerAddress string, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := olm.NewOperatorLifecycleManagerParams(hcp, releaseImage.ComponentImages(), releaseImage.Version(), r.SetDefaultSecurityContext)

	if hcp.Spec.OLMCatalogPlacement == hyperv1.ManagementOLMCatalogPlacement {
		catalogsImageStream := manifests.CatalogsImageStream(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, catalogsImageStream, func() error {
			return olm.ReconcileCatalogsImageStream(catalogsImageStream, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile certified operators service: %w", err)
		}

		certifiedOperatorsService := manifests.CertifiedOperatorsService(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, certifiedOperatorsService, func() error {
			return olm.ReconcileCertifiedOperatorsService(certifiedOperatorsService, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile certified operators service: %w", err)
		}
		communityOperatorsService := manifests.CommunityOperatorsService(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, communityOperatorsService, func() error {
			return olm.ReconcileCommunityOperatorsService(communityOperatorsService, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile community operators service: %w", err)
		}
		marketplaceOperatorsService := manifests.RedHatMarketplaceOperatorsService(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, marketplaceOperatorsService, func() error {
			return olm.ReconcileRedHatMarketplaceOperatorsService(marketplaceOperatorsService, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile marketplace operators service: %w", err)
		}
		redHatOperatorsService := manifests.RedHatOperatorsService(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, redHatOperatorsService, func() error {
			return olm.ReconcileRedHatOperatorsService(redHatOperatorsService, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile red hat operators service: %w", err)
		}

		certifiedOperatorsDeployment := manifests.CertifiedOperatorsDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, certifiedOperatorsDeployment, func() error {
			return olm.ReconcileCertifiedOperatorsDeployment(certifiedOperatorsDeployment, p.OwnerRef, p.DeploymentConfig)
		}); err != nil {
			return fmt.Errorf("failed to reconcile certified operators deployment: %w", err)
		}
		communityOperatorsDeployment := manifests.CommunityOperatorsDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, communityOperatorsDeployment, func() error {
			return olm.ReconcileCommunityOperatorsDeployment(communityOperatorsDeployment, p.OwnerRef, p.DeploymentConfig)
		}); err != nil {
			return fmt.Errorf("failed to reconcile community operators deployment: %w", err)
		}
		marketplaceOperatorsDeployment := manifests.RedHatMarketplaceOperatorsDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, marketplaceOperatorsDeployment, func() error {
			return olm.ReconcileRedHatMarketplaceOperatorsDeployment(marketplaceOperatorsDeployment, p.OwnerRef, p.DeploymentConfig)
		}); err != nil {
			return fmt.Errorf("failed to reconcile marketplace operators deployment: %w", err)
		}
		redHatOperatorsDeployment := manifests.RedHatOperatorsDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, redHatOperatorsDeployment, func() error {
			return olm.ReconcileRedHatOperatorsDeployment(redHatOperatorsDeployment, p.OwnerRef, p.DeploymentConfig)
		}); err != nil {
			return fmt.Errorf("failed to reconcile red hat operators deployment: %w", err)
		}

		catalogRolloutSA := manifests.CatalogRolloutServiceAccount(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, catalogRolloutSA, func() error {
			return olm.ReconcileCatalogRolloutServiceAccount(catalogRolloutSA, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile catalog rollout service account: %w", err)
		}
		catalogRolloutRole := manifests.CatalogRolloutRole(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, catalogRolloutRole, func() error {
			return olm.ReconcileCatalogRolloutRole(catalogRolloutRole, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile catalog rollout role: %w", err)
		}
		catalogRolloutRoleBinding := manifests.CatalogRolloutRoleBinding(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, catalogRolloutRoleBinding, func() error {
			return olm.ReconcileCatalogRolloutRoleBinding(catalogRolloutRoleBinding, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile catalog rollout rolebinding: %w", err)
		}
	}

	catalogOperatorMetricsService := manifests.CatalogOperatorMetricsService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, catalogOperatorMetricsService, func() error {
		return olm.ReconcileCatalogOperatorMetricsService(catalogOperatorMetricsService, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile catalog operator metrics service: %w", err)
	}
	catalogOperatorServiceMonitor := manifests.CatalogOperatorServiceMonitor(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, catalogOperatorServiceMonitor, func() error {
		return olm.ReconcileCatalogServiceMonitor(catalogOperatorServiceMonitor, p.OwnerRef, hcp.Spec.ClusterID, r.MetricsSet)
	}); err != nil {
		return fmt.Errorf("failed to reconcile catalog operator service monitor: %w", err)
	}
	catalogOperatorDeployment := manifests.CatalogOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, catalogOperatorDeployment, func() error {
		return olm.ReconcileCatalogOperatorDeployment(catalogOperatorDeployment, p.OwnerRef, p.OLMImage, p.ProxyImage, p.OperatorRegistryImage, p.ReleaseVersion, p.DeploymentConfig, p.AvailabilityProberImage, util.APIPort(hcp), p.NoProxy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile catalog operator deployment: %w", err)
	}

	olmOperatorMetricsService := manifests.OLMOperatorMetricsService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, olmOperatorMetricsService, func() error {
		return olm.ReconcileOLMOperatorMetricsService(olmOperatorMetricsService, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile olm operator metrics service: %w", err)
	}

	olmOperatorServiceMonitor := manifests.OLMOperatorServiceMonitor(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, olmOperatorServiceMonitor, func() error {
		return olm.ReconcileOLMOperatorServiceMonitor(olmOperatorServiceMonitor, p.OwnerRef, hcp.Spec.ClusterID, r.MetricsSet)
	}); err != nil {
		return fmt.Errorf("failed to reconcile olm operator service monitor: %w", err)
	}

	olmOperatorDeployment := manifests.OLMOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, olmOperatorDeployment, func() error {
		return olm.ReconcileOLMOperatorDeployment(olmOperatorDeployment, p.OwnerRef, p.OLMImage, p.ProxyImage, p.ReleaseVersion, p.DeploymentConfig, p.AvailabilityProberImage, util.APIPort(hcp), p.NoProxy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile olm operator deployment: %w", err)
	}

	packageServerDeployment := manifests.OLMPackageServerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, packageServerDeployment, func() error {
		return olm.ReconcilePackageServerDeployment(packageServerDeployment, p.OwnerRef, p.OLMImage, p.ProxyImage, p.ReleaseVersion, p.PackageServerConfig, p.AvailabilityProberImage, util.APIPort(hcp), p.NoProxy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile packageserver deployment: %w", err)
	}

	// no need to run heap collection in IBM Cloud
	if hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		collectProfilesConfigMap := manifests.CollectProfilesConfigMap(hcp.Namespace)
		if err := deleteIfExists(ctx, r, collectProfilesConfigMap); err != nil {
			return fmt.Errorf("failed to remove collect profiles config map: %w", err)
		}
		collectProfilesCronJob := manifests.CollectProfilesCronJob(hcp.Namespace)
		if err := deleteIfExists(ctx, r, collectProfilesCronJob); err != nil {
			return fmt.Errorf("failed to remove collect profiles cronjob: %w", err)
		}
		collectProfilesRole := manifests.CollectProfilesRole(hcp.Namespace)
		if err := deleteIfExists(ctx, r, collectProfilesRole); err != nil {
			return fmt.Errorf("failed to remove collect profiles role: %w", err)
		}
		collectProfilesRoleBinding := manifests.CollectProfilesRoleBinding(hcp.Namespace)
		if err := deleteIfExists(ctx, r, collectProfilesRoleBinding); err != nil {
			return fmt.Errorf("failed to remove collect profiles role binding: %w", err)
		}
		collectProfilesSecret := manifests.CollectProfilesSecret(hcp.Namespace)
		if err := deleteIfExists(ctx, r, collectProfilesSecret); err != nil {
			return fmt.Errorf("failed to remove collect profiles secret: %w", err)
		}
		collectProfilesServiceAccount := manifests.CollectProfilesServiceAccount(hcp.Namespace)
		if err := deleteIfExists(ctx, r, collectProfilesServiceAccount); err != nil {
			return fmt.Errorf("failed to remove collect profiles serviceaccount: %w", err)
		}
		return nil
	}

	// Collect Profiles
	collectProfilesConfigMap := manifests.CollectProfilesConfigMap(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, collectProfilesConfigMap, func() error {
		olm.ReconcileCollectProfilesConfigMap(collectProfilesConfigMap, p.OwnerRef)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile collect profiles config map: %w", err)
	}

	collectProfilesCronJob := manifests.CollectProfilesCronJob(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, collectProfilesCronJob, func() error {
		olm.ReconcileCollectProfilesCronJob(collectProfilesCronJob, p.OwnerRef, p.OLMImage, hcp.Namespace)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile collect profiles cronjob: %w", err)
	}

	collectProfilesRole := manifests.CollectProfilesRole(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, collectProfilesRole, func() error {
		olm.ReconcileCollectProfilesRole(collectProfilesRole, p.OwnerRef)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile collect profiles role: %w", err)
	}

	collectProfilesRoleBinding := manifests.CollectProfilesRoleBinding(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, collectProfilesRoleBinding, func() error {
		olm.ReconcileCollectProfilesRoleBinding(collectProfilesRoleBinding, p.OwnerRef)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile collect profiles rolebinding: %w", err)
	}

	collectProfilesSecret := manifests.CollectProfilesSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, collectProfilesSecret, func() error {
		olm.ReconcileCollectProfilesSecret(collectProfilesSecret, p.OwnerRef)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile collect profiles secret: %w", err)
	}

	collectProfilesServiceAccount := manifests.CollectProfilesServiceAccount(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, collectProfilesServiceAccount, func() error {
		olm.ReconcileCollectProfilesServiceAccount(collectProfilesServiceAccount, p.OwnerRef)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile collect profiles serviceaccount: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileImageRegistryOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	params := registryoperator.NewParams(hcp, releaseImage.Version(), releaseImage.ComponentImages(), r.SetDefaultSecurityContext)
	deployment := manifests.ImageRegistryOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return registryoperator.ReconcileDeployment(deployment, params)
	}); err != nil {
		return fmt.Errorf("failed to reconcile image registry operator deployment: %w", err)
	}

	pm := manifests.ImageRegistryOperatorPodMonitor(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, pm, func() error {
		registryoperator.ReconcilePodMonitor(pm, hcp.Spec.ClusterID, r.MetricsSet)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile image registry operator pod monitor: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileMachineConfigServerConfig(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	rootCA := manifests.RootCASecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return fmt.Errorf("failed to get root ca: %w", err)
	}

	pullSecret := common.PullSecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
		return fmt.Errorf("failed to get pull secret: %w", err)
	}

	var userCA *corev1.ConfigMap
	if hcp.Spec.AdditionalTrustBundle != nil {
		userCA = manifests.UserCAConfigMap(hcp.Namespace)
		if err := r.Get(ctx, client.ObjectKeyFromObject(userCA), userCA); err != nil {
			return fmt.Errorf("failed to get user ca: %w", err)
		}
	}

	kubeletClientCA := manifests.KubeletClientCABundle(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(kubeletClientCA), kubeletClientCA); err != nil {
		return fmt.Errorf("failed to get root kubelet client CA: %w", err)
	}

	p := mcs.NewMCSParams(hcp, rootCA, pullSecret, userCA, kubeletClientCA)

	cm := manifests.MachineConfigServerConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, cm, func() error {
		return mcs.ReconcileMachineConfigServerConfig(cm, p)
	}); err != nil {
		return fmt.Errorf("failed to reconcile machine config server config: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileCoreIgnitionConfig(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	sshKey := ""
	if len(hcp.Spec.SSHKey.Name) > 0 {
		var sshKeySecret corev1.Secret
		err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcp.Namespace, Name: hcp.Spec.SSHKey.Name}, &sshKeySecret)
		if err != nil {
			return fmt.Errorf("failed to get SSH key secret %s: %w", hcp.Spec.SSHKey.Name, err)
		}
		data, hasSSHKeyData := sshKeySecret.Data["id_rsa.pub"]
		if !hasSSHKeyData {
			return fmt.Errorf("SSH key secret secret %s is missing the id_rsa.pub key", hcp.Spec.SSHKey.Name)
		}
		sshKey = string(data)
	}

	p := ignition.NewIgnitionConfigParams(hcp, sshKey)

	fipsConfig := manifests.IgnitionFIPSConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, fipsConfig, func() error {
		return ignition.ReconcileFIPSIgnitionConfig(fipsConfig, p.OwnerRef, p.FIPSEnabled)
	}); err != nil {
		return fmt.Errorf("failed to reconcile fips ignition config: %w", err)
	}

	sshKeyConfig := manifests.IgnitionWorkerSSHConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, sshKeyConfig, func() error {
		return ignition.ReconcileWorkerSSHIgnitionConfig(sshKeyConfig, p.OwnerRef, sshKey)
	}); err != nil {
		return fmt.Errorf("failed to reconcile ssh key ignition config: %w", err)
	}

	imageContentSourceIgnitionConfig := manifests.ImageContentSourcePolicyIgnitionConfig(hcp.GetNamespace())
	if !p.HasImageContentSourcePolicy {
		// ensure the icsp configmap has been removed if no longer needed
		err := r.Get(ctx, client.ObjectKeyFromObject(imageContentSourceIgnitionConfig), imageContentSourceIgnitionConfig)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to check whether image content source policy configuration configmap exists: %w", err)
			}
		} else {
			if err := r.Delete(ctx, imageContentSourceIgnitionConfig); err != nil {
				return fmt.Errorf("failed to delete image content source policy configuration configmap: %w", err)
			}
		}
		return nil
	}

	icsp := globalconfig.ImageContentSourcePolicy()
	if err := globalconfig.ReconcileImageContentSourcePolicy(icsp, hcp); err != nil {
		return fmt.Errorf("failed to reconcile image content source policy: %w", err)
	}

	if _, err := createOrUpdate(ctx, r, imageContentSourceIgnitionConfig, func() error {
		return ignition.ReconcileImageContentSourcePolicyIgnitionConfig(imageContentSourceIgnitionConfig, p.OwnerRef, icsp)
	}); err != nil {
		return fmt.Errorf("failed to reconcile image content source policy ignition config: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcilePrivateIngressController(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	ic := manifests.IngressPrivateIngressController(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(ic), ic); err == nil {
		if err = r.Delete(ctx, ic); err != nil {
			return fmt.Errorf("failed to delete private ingress controller: %w", err)
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get private ingress controller: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileRouter(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseInfo *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN, exposeKASThroughRouter bool, privateRouterHost, externalRouterHost string) error {
	sa := manifests.RouterServiceAccount(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, sa, func() error {
		return ingress.ReconcileRouterServiceAccount(sa, config.OwnerRefFrom(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile router service account: %w", err)
	}
	role := manifests.RouterRole(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, role, func() error {
		return ingress.ReconcileRouterRole(role, config.OwnerRefFrom(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile router role: %w", err)
	}
	rb := manifests.RouterRoleBinding(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, rb, func() error {
		return ingress.ReconcileRouterRoleBinding(rb, config.OwnerRefFrom(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile router rolebinding: %w", err)
	}

	// Calculate router canonical hostname
	var canonicalHostname string
	if util.IsPublicHCP(hcp) {
		canonicalHostname = externalRouterHost
	} else if util.IsPrivateHCP(hcp) {
		canonicalHostname = privateRouterHost
	}

	// If the KAS is a route we need to tweak the default template.
	if exposeKASThroughRouter {
		routerTemplate := manifests.RouterTemplateConfigMap(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r.Client, routerTemplate, func() error {
			ingress.ReconcileRouterTemplateConfigmap(routerTemplate)
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile router template configmap: %w", err)
		}
	}

	if util.IsPrivateHCP(hcp) || exposeKASThroughRouter {
		deployment := manifests.RouterDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r.Client, deployment, func() error {
			return ingress.ReconcileRouterDeployment(deployment,
				config.OwnerRefFrom(hcp),
				ingress.HCPRouterConfig(hcp, r.SetDefaultSecurityContext),
				ingress.PrivateRouterImage(releaseInfo.ComponentImages()),
				canonicalHostname,
				exposeKASThroughRouter,
				!util.IsPublicHCP(hcp),
			)
		}); err != nil {
			return fmt.Errorf("failed to reconcile router deployment: %w", err)
		}
	}

	oldRouterResources := []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "private-router"}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "private-router"}},
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "private-router"}},
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "private-router"}},
	}
	for _, resource := range oldRouterResources {
		if err := deleteIfExists(ctx, r.Client, resource); err != nil {
			return fmt.Errorf("failed to delete %T %s: %w", resource, resource.GetName(), err)
		}
	}

	return nil
}

func deleteIfExists(ctx context.Context, c client.Client, o client.Object) error {
	if err := c.Get(ctx, client.ObjectKeyFromObject(o), o); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get object: %w", err)
	}
	if err := c.Delete(ctx, o); err != nil {
		return fmt.Errorf("failed to delete object: %w", err)
	}
	return nil
}

// removeServiceCAAnnotationAndSecret will delete Secret 'secret' and
// remove the annotation "service.beta.openshift.io/serving-cert-secret-name"
// from Service 'service' if it contains this annotation.
// This is used to remove Secrets generated by the service-ca in case
// of upgrade, from a control-plane version using service-ca generated certs
// to a version where the service uses HCP controller generated certs.
func removeServiceCAAnnotationAndSecret(ctx context.Context, c client.Client, service *corev1.Service, secret *corev1.Secret) error {
	if err := c.Get(ctx, client.ObjectKeyFromObject(service), service); err != nil {
		return fmt.Errorf("failed to get service: %w", err)
	}

	if err := c.Get(ctx, client.ObjectKeyFromObject(secret), secret); err != nil {
		return fmt.Errorf("failed to get secret: %w", err)
	}

	_, ok := service.Annotations["service.beta.openshift.io/serving-cert-secret-name"]
	if ok {
		delete(service.Annotations, "service.beta.openshift.io/serving-cert-secret-name")
		err := c.Update(ctx, service)
		if err != nil {
			return fmt.Errorf("failed to update service: %w", err)
		}
	}

	_, ok = secret.Annotations["service.beta.openshift.io/originating-service-name"]
	if ok {
		err := deleteIfExists(ctx, c, secret)
		if err != nil {
			return fmt.Errorf("failed to delete secret generated by service-ca: %w", err)
		}
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileHostedClusterConfigOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseInfo *releaseinfo.ReleaseImage, infraStatus InfrastructureStatus, createOrUpdate upsert.CreateOrUpdateFN) error {
	versions, err := releaseInfo.ComponentVersions()
	if err != nil {
		return fmt.Errorf("failed to get component versions: %w", err)
	}
	p := configoperator.NewHostedClusterConfigOperatorParams(ctx, hcp, releaseInfo.ComponentImages(), releaseInfo.Version(), versions["kubernetes"], r.SetDefaultSecurityContext)

	sa := manifests.ConfigOperatorServiceAccount(hcp.Namespace)
	if _, err = createOrUpdate(ctx, r.Client, sa, func() error {
		return configoperator.ReconcileServiceAccount(sa, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile config operator service account: %w", err)
	}

	role := manifests.ConfigOperatorRole(hcp.Namespace)
	if _, err = createOrUpdate(ctx, r.Client, role, func() error {
		return configoperator.ReconcileRole(role, p.OwnerRef, hcp.Spec.Platform.Type)
	}); err != nil {
		return fmt.Errorf("failed to reconcile config operator role: %w", err)
	}

	rb := manifests.ConfigOperatorRoleBinding(hcp.Namespace)
	if _, err = createOrUpdate(ctx, r.Client, rb, func() error {
		return configoperator.ReconcileRoleBinding(rb, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile config operator rolebinding: %w", err)
	}

	deployment := manifests.ConfigOperatorDeployment(hcp.Namespace)
	if _, err = createOrUpdate(ctx, r.Client, deployment, func() error {
		return configoperator.ReconcileDeployment(deployment, p.Image, hcp.Name, p.OpenShiftVersion, p.KubernetesVersion, p.OwnerRef, &p.DeploymentConfig, p.AvailabilityProberImage, r.EnableCIDebugOutput, hcp.Spec.Platform.Type, util.APIPort(hcp), infraStatus.KonnectivityHost, infraStatus.KonnectivityPort, infraStatus.OAuthHost, infraStatus.OAuthPort, hcp.Spec.ReleaseImage, hcp.Spec.AdditionalTrustBundle, hcp)
	}); err != nil {
		return fmt.Errorf("failed to reconcile config operator deployment: %w", err)
	}

	podMonitor := manifests.ConfigOperatorPodMonitor(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, podMonitor, func() error {
		podMonitor.Spec.Selector = *deployment.Spec.Selector
		podMonitor.Spec.PodMetricsEndpoints = []prometheusoperatorv1.PodMetricsEndpoint{{
			Port:                 "metrics",
			MetricRelabelConfigs: metrics.HostedClusterConfigOperatorRelabelConfigs(r.MetricsSet),
		}}
		podMonitor.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{MatchNames: []string{hcp.Namespace}}
		podMonitor.SetOwnerReferences([]metav1.OwnerReference{{
			APIVersion: hyperv1.GroupVersion.String(),
			Kind:       "HostedControlPlane",
			Name:       hcp.Name,
			UID:        hcp.UID,
		}})
		util.ApplyClusterIDLabelToPodMonitor(&podMonitor.Spec.PodMetricsEndpoints[0], hcp.Spec.ClusterID)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile pod monitor for config operator: %w", err)
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

func reconcileKubeadminPasswordSecret(secret *corev1.Secret, hcp *hyperv1.HostedControlPlane, password *string) error {
	ownerRef := config.OwnerRefFrom(hcp)
	ownerRef.ApplyTo(secret)
	existingPassword, exists := secret.Data["password"]
	if !exists || len(existingPassword) == 0 {
		generated, err := generateKubeadminPassword()
		if err != nil {
			return fmt.Errorf("failed to generate kubeadmin password: %w", err)
		}
		*password = generated
		secret.Data = map[string][]byte{"password": []byte(generated)}
	} else {
		*password = string(existingPassword)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) hostedControlPlaneInNamespace(resource client.Object) []reconcile.Request {
	hcpList := &hyperv1.HostedControlPlaneList{}
	if err := r.List(context.Background(), hcpList, &client.ListOptions{
		Namespace: resource.GetNamespace(),
	}); err != nil {
		r.Log.Error(err, "failed to list hosted control planes in namespace", "namespace", resource.GetNamespace())
		return nil
	}
	if len(hcpList.Items) > 1 {
		r.Log.Error(fmt.Errorf("more than one HostedControlPlane resource found in namespace %s", resource.GetNamespace()), "unexpected number of HostedControlPlane resources")
		return nil
	}
	var result []reconcile.Request
	for _, hcp := range hcpList.Items {
		result = append(result, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: hcp.Namespace, Name: hcp.Name}})
	}
	return result
}

func (r *HostedControlPlaneReconciler) etcdRestoredCondition(ctx context.Context, sts *appsv1.StatefulSet) *metav1.Condition {
	if sts.Status.ReadyReplicas == *sts.Spec.Replicas {
		// Check that all etcd pods have initContainers that started
		podList := &corev1.PodList{}
		initContainerCount := int32(0)
		if err := r.List(ctx, podList, &client.ListOptions{
			Namespace:     sts.Namespace,
			LabelSelector: labels.SelectorFromValidatedSet(labels.Set{"app": "etcd"}),
		}); err == nil {
			for _, pod := range podList.Items {
				for _, status := range pod.Status.InitContainerStatuses {
					if status.Name == "etcd-init" {
						if status.Ready {
							initContainerCount += 1
						} else if status.LastTerminationState.Terminated != nil {
							if status.LastTerminationState.Terminated.ExitCode != 0 {
								return &metav1.Condition{
									Type:   string(hyperv1.EtcdSnapshotRestored),
									Status: metav1.ConditionFalse,
									Reason: status.LastTerminationState.Terminated.Reason,
								}
							}
						}
					}
				}
			}
		}

		if initContainerCount == *sts.Spec.Replicas {
			return &metav1.Condition{
				Type:   string(hyperv1.EtcdSnapshotRestored),
				Status: metav1.ConditionTrue,
				Reason: hyperv1.AsExpectedReason,
			}
		}
	}
	return nil
}

func (r *HostedControlPlaneReconciler) etcdStatefulSetCondition(ctx context.Context, sts *appsv1.StatefulSet) (*metav1.Condition, error) {
	if sts.Status.ReadyReplicas >= *sts.Spec.Replicas/2+1 {
		return &metav1.Condition{
			Type:   string(hyperv1.EtcdAvailable),
			Status: metav1.ConditionTrue,
			Reason: hyperv1.EtcdQuorumAvailableReason,
		}, nil
	}

	var message string

	// Check that any etcd PVCs have been provisioned
	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcList, &client.ListOptions{
		Namespace:     sts.Namespace,
		LabelSelector: labels.SelectorFromValidatedSet(labels.Set{"app": "etcd"}),
	}); err != nil {
		return nil, err
	}

	messageCollector := events.NewMessageCollector(ctx, r.Client)
	for _, pvc := range pvcList.Items {
		if pvc.Status.Phase != corev1.ClaimBound {
			eventMessages, err := messageCollector.ErrorMessages(&pvc)
			if err != nil {
				return nil, err
			}
			if len(eventMessages) > 0 {
				message = fmt.Sprintf("Etcd volume claim %s pending: %s", pvc.Name, strings.Join(eventMessages, "; "))
				break
			}
		}
	}

	if len(message) == 0 {
		message = "Waiting for etcd to reach quorum"
	}
	return &metav1.Condition{
		Type:    string(hyperv1.EtcdAvailable),
		Status:  metav1.ConditionFalse,
		Reason:  hyperv1.EtcdWaitingForQuorumReason,
		Message: message,
	}, nil

}

func (r *HostedControlPlaneReconciler) reconcileCloudControllerManager(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		ownerRef := config.OwnerRefFrom(hcp)
		sa := aws.CCMServiceAccount(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, sa, func() error {
			return aws.ReconcileCCMServiceAccount(sa, ownerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile Kubevirt cloud provider service account: %w", err)
		}
		deployment := aws.CCMDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, deployment, func() error {
			return aws.ReconcileDeployment(deployment, hcp, sa.Name, releaseImage)
		}); err != nil {
			return fmt.Errorf("failed to reconcile ccm deployment: %w", err)
		}
	case hyperv1.PowerVSPlatform:
		ccmConfig := manifests.PowerVSCCMConfigMap(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, ccmConfig, func() error {
			return powervs.ReconcileCCMConfigMap(ccmConfig, hcp)
		}); err != nil {
			return fmt.Errorf("failed to reconcile cloud controller manager config: %w", err)
		}

		deployment := manifests.PowerVSCCMDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, deployment, func() error {
			return powervs.ReconcileCCMDeployment(deployment, hcp, ccmConfig, releaseImage)
		}); err != nil {
			return fmt.Errorf("failed to reconcile cloud controller manager deployment: %w", err)
		}
	case hyperv1.KubevirtPlatform:
		// Create the cloud-config file used by Kubevirt CCM
		ccmConfig := kubevirt.CCMConfigMap(hcp.Namespace)

		r.Log.Info("creating kubevirt cloud-config ConfigMap")
		if _, err := createOrUpdate(ctx, r, ccmConfig, func() error {

			r.Log.Info("reconciling kubevirt CCM ConfigMap")
			return kubevirt.ReconcileCloudConfig(ccmConfig, hcp)

		}); err != nil {
			return fmt.Errorf("failed to reconcile Kubevirt cloud config: %w", err)
		}

		// Create the ServiceAccount used by Kubevirt CCM to access
		// the KubevirtInfraCluster (which is the ManagementCluster)
		ownerRef := config.OwnerRefFrom(hcp)
		sa := kubevirt.CCMServiceAccount(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, sa, func() error {
			return kubevirt.ReconcileCCMServiceAccount(sa, ownerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile Kubevirt cloud provider service account: %w", err)
		}
		role := kubevirt.CCMRole(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, role, func() error {
			return kubevirt.ReconcileCCMRole(role, ownerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile Kubevirt cloud provider role: %w", err)
		}
		roleBinding := kubevirt.CCMRoleBinding(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, roleBinding, func() error {
			return kubevirt.ReconcileCCMRoleBinding(roleBinding, ownerRef, sa, role)
		}); err != nil {
			return fmt.Errorf("failed to reconcile Kubevirt cloud provider rolebinding: %w", err)
		}

		// Deploy Kubevirt CCM
		deployment := kubevirt.CCMDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, deployment, func() error {
			return kubevirt.ReconcileDeployment(deployment, hcp, sa.Name, releaseImage)
		}); err != nil {
			return fmt.Errorf("failed to reconcile ccm deployment: %w", err)
		}
	}
	return nil
}

// reconcileAutoscaler orchestrates reconciliation of autoscaler components using
func (r *HostedControlPlaneReconciler) reconcileAutoscaler(ctx context.Context, hcp *hyperv1.HostedControlPlane, images map[string]string, createOrUpdate upsert.CreateOrUpdateFN) error {
	autoscalerImage, ok := images[ImageStreamAutoscalerImage]
	if !ok {
		return fmt.Errorf("autoscaler image not found")
	}

	availabilityProberImage, ok := images[util.AvailabilityProberImageName]
	if !ok {
		return fmt.Errorf("availability prober image not found")
	}

	return autoscaler.ReconcileAutoscaler(ctx, r.Client, hcp, autoscalerImage, availabilityProberImage, createOrUpdate, r.SetDefaultSecurityContext, config.OwnerRefFrom(hcp))
}

func (r *HostedControlPlaneReconciler) reconcileMachineApprover(ctx context.Context, hcp *hyperv1.HostedControlPlane, images map[string]string, createOrUpdate upsert.CreateOrUpdateFN) error {
	machineApproverImage, ok := images[ImageStreamClusterMachineApproverImage]
	if !ok {
		return fmt.Errorf("autoscaler image not found")
	}

	availabilityProberImage, ok := images[util.AvailabilityProberImageName]
	if !ok {
		return fmt.Errorf("availability prober image not found")
	}

	return machineapprover.ReconcileMachineApprover(ctx, r.Client, hcp, machineApproverImage, availabilityProberImage, createOrUpdate, r.SetDefaultSecurityContext, config.OwnerRefFrom(hcp))
}

func shouldCleanupCloudResources(log logr.Logger, hcp *hyperv1.HostedControlPlane) bool {
	if msg, isValid := hasValidCloudCredentials(hcp); !isValid {
		log.Info("Skipping hosted cluster cloud resources cleanup", "reason", msg)
		return false
	}
	return hcp.Annotations[hyperv1.CleanupCloudResourcesAnnotation] == "true"
}

func (r *HostedControlPlaneReconciler) removeCloudResources(ctx context.Context, hcp *hyperv1.HostedControlPlane) (bool, error) {

	log := ctrl.LoggerFrom(ctx)
	log.Info("Removing cloud resources")

	// check if resources have been destroyed
	resourcesDestroyedCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.CloudResourcesDestroyed))
	if resourcesDestroyedCond != nil && resourcesDestroyedCond.Status == metav1.ConditionTrue {
		log.Info("Guest resources have been destroyed")
		return true, nil
	}

	// if CVO has been scaled down, we're waiting for resources to be destroyed
	cvoScaledDownCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.CVOScaledDown))
	if cvoScaledDownCond != nil && cvoScaledDownCond.Status == metav1.ConditionTrue {
		log.Info("Waiting for guest resources to be destroyed")

		// Determine if too much time has passed since the last time we got an update
		var timeElapsed time.Duration
		if resourcesDestroyedCond != nil {
			timeElapsed = time.Since(resourcesDestroyedCond.LastTransitionTime.Time)
		} else {
			timeElapsed = time.Since(cvoScaledDownCond.LastTransitionTime.Time)
		}

		if timeElapsed > resourceDeletionTimeout {
			log.Info("Giving up on resource deletion since there has not been an update before timeout", "timeElapsed", duration.ShortHumanDuration(timeElapsed))
			return true, nil
		}
		return false, nil
	}

	// ensure CVO has been scaled down
	cvoDeployment := manifests.ClusterVersionOperatorDeployment(hcp.Namespace)
	err := r.Get(ctx, client.ObjectKeyFromObject(cvoDeployment), cvoDeployment)
	if err != nil && !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("failed get CVO deployment: %w", err)
	}
	if err == nil && cvoDeployment.Spec.Replicas != nil && *cvoDeployment.Spec.Replicas > 0 {
		log.Info("Scaling down cluster version operator deployment")
		cvoDeployment.Spec.Replicas = pointer.Int32(0)
		if err := r.Update(ctx, cvoDeployment); err != nil {
			return false, fmt.Errorf("failed to scale down CVO deployment: %w", err)
		}
	}
	if cvoDeployment.Status.Replicas > 0 {
		log.Info("Waiting for CVO to scale down to 0")
		return false, nil
	}
	if cvoScaledDownCond == nil || cvoScaledDownCond.Status != metav1.ConditionTrue {
		cvoScaledDownCond = &metav1.Condition{
			Type:               string(hyperv1.CVOScaledDown),
			Status:             metav1.ConditionTrue,
			Reason:             "CVOScaledDown",
			LastTransitionTime: metav1.Now(),
		}
		meta.SetStatusCondition(&hcp.Status.Conditions, *cvoScaledDownCond)
		if err := r.Status().Update(ctx, hcp); err != nil {
			return false, fmt.Errorf("failed to set CVO scaled down condition: %w", err)
		}
	}
	return false, nil
}

func (r *HostedControlPlaneReconciler) reconcileCSISnapshotControllerOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	params := snapshotcontroller.NewParams(hcp, releaseImage.Version(), releaseImage.ComponentImages(), r.SetDefaultSecurityContext)

	deployment := manifests.CSISnapshotControllerOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return snapshotcontroller.ReconcileOperatorDeployment(deployment, params)
	}); err != nil {
		return fmt.Errorf("failed to reconcile CSI snapshot controller operator deployment: %w", err)
	}

	sa := manifests.CSISnapshotControllerOperatorServiceAccount(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, sa, func() error {
		return snapshotcontroller.ReconcileOperatorServiceAccount(sa, params)
	}); err != nil {
		return fmt.Errorf("failed to reconcile CSI snapshot controller operator service account: %w", err)
	}

	role := manifests.CSISnapshotControllerOperatorRole(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, role, func() error {
		return snapshotcontroller.ReconcileOperatorRole(role, params)
	}); err != nil {
		return fmt.Errorf("failed to reconcile CSI snapshot controller operator role: %w", err)
	}

	roleBinding := manifests.CSISnapshotControllerOperatorRoleBinding(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, roleBinding, func() error {
		return snapshotcontroller.ReconcileOperatorRoleBinding(roleBinding, params)
	}); err != nil {
		return fmt.Errorf("failed to reconcile CSI snapshot controller operator roleBinding: %w", err)
	}

	// TODO: create custom kubeconfig to the guest cluster + RBAC

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileClusterStorageOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	params := storage.NewParams(hcp, releaseImage.Version(), releaseImage.ComponentImages(), r.SetDefaultSecurityContext)

	deployment := manifests.ClusterStorageOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return storage.ReconcileOperatorDeployment(deployment, params)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster storage operator deployment: %w", err)
	}

	sa := manifests.ClusterStorageOperatorServiceAccount(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, sa, func() error {
		return storage.ReconcileOperatorServiceAccount(sa, params)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster storage operator service account: %w", err)
	}

	role := manifests.ClusterStorageOperatorRole(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, role, func() error {
		return storage.ReconcileOperatorRole(role, params)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster storage operator role: %w", err)
	}

	roleBinding := manifests.ClusterStorageOperatorRoleBinding(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, roleBinding, func() error {
		return storage.ReconcileOperatorRoleBinding(roleBinding, params)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster storage operator roleBinding: %w", err)
	}

	// TODO: create custom kubeconfig to the guest cluster + RBAC

	return nil
}

func healthCheckIdentityProvider(ctx context.Context, hcp *hyperv1.HostedControlPlane) {
	if hcp.Spec.Platform.AWS == nil {
		return
	}

	log := ctrl.LoggerFrom(ctx)

	ec2Client, _ := getEC2Client()
	if ec2Client == nil {
		return
	}

	// We try to interact with cloud provider to see validate is operational.
	if _, err := ec2Client.DescribeVpcEndpointsWithContext(ctx, &ec2.DescribeVpcEndpointsInput{}); err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			// When awsErr.Code() is WebIdentityErr it's likely to be an external issue, e.g the idp resource was deleted.
			// We don't set awsErr.Message() in the condition as it might contain aws requests IDs that would make the condition be updated in loop.
			if awsErr.Code() == "WebIdentityErr" {
				condition := metav1.Condition{
					Type:               string(hyperv1.ValidAWSIdentityProvider),
					ObservedGeneration: hcp.Generation,
					Status:             metav1.ConditionFalse,
					Message:            awsErr.Code(),
					Reason:             hyperv1.InvalidIdentityProvider,
				}
				meta.SetStatusCondition(&hcp.Status.Conditions, condition)
				log.Info("Error health checking AWS identity provider", awsErr.Code(), awsErr.Message())
				return
			}

			condition := metav1.Condition{
				Type:               string(hyperv1.ValidAWSIdentityProvider),
				ObservedGeneration: hcp.Generation,
				Status:             metav1.ConditionUnknown,
				Message:            awsErr.Code(),
				Reason:             hyperv1.AWSErrorReason,
			}
			meta.SetStatusCondition(&hcp.Status.Conditions, condition)
			log.Info("Error health checking AWS identity provider", awsErr.Code(), awsErr.Message())
			return
		}

		condition := metav1.Condition{
			Type:               string(hyperv1.ValidAWSIdentityProvider),
			ObservedGeneration: hcp.Generation,
			Status:             metav1.ConditionUnknown,
			Message:            err.Error(),
			Reason:             hyperv1.StatusUnknownReason,
		}
		meta.SetStatusCondition(&hcp.Status.Conditions, condition)
		log.Info("Error health checking AWS identity provider", "error", err)
		return
	}

	condition := metav1.Condition{
		Type:               string(hyperv1.ValidAWSIdentityProvider),
		ObservedGeneration: hcp.Generation,
		Status:             metav1.ConditionTrue,
		Message:            hyperv1.AllIsWellMessage,
		Reason:             hyperv1.AsExpectedReason,
	}
	meta.SetStatusCondition(&hcp.Status.Conditions, condition)
}

func (r *HostedControlPlaneReconciler) reconcileDefaultSecurityGroup(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	logger := ctrl.LoggerFrom(ctx)
	if hcp.Spec.Platform.Type != hyperv1.AWSPlatform {
		// Not AWS platform, skip
		return nil
	}
	if hcp.Status.Platform != nil && hcp.Status.Platform.AWS != nil && hcp.Status.Platform.AWS.DefaultWorkerSecurityGroupID != "" {
		// Security group has already been created, nothing to do
		return nil
	}
	validProvider := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidAWSIdentityProvider))
	if validProvider == nil || validProvider.Status != metav1.ConditionTrue {
		logger.Info("Identity provider not ready. Skipping security group creation.")
		return nil
	}

	originalHCP := hcp.DeepCopy()
	var condition *metav1.Condition
	sgID, creationErr := createAWSDefaultSecurityGroup(ctx, r.ec2Client, hcp.Spec.InfraID, hcp.Spec.Platform.AWS.CloudProviderConfig.VPC, hcp.Spec.Platform.AWS.ResourceTags)
	if creationErr != nil {
		condition = &metav1.Condition{
			Type:    string(hyperv1.AWSDefaultSecurityGroupCreated),
			Status:  metav1.ConditionFalse,
			Message: creationErr.Error(),
			Reason:  hyperv1.AWSErrorReason,
		}
	} else {
		condition = &metav1.Condition{
			Type:    string(hyperv1.AWSDefaultSecurityGroupCreated),
			Status:  metav1.ConditionTrue,
			Message: hyperv1.AllIsWellMessage,
			Reason:  hyperv1.AsExpectedReason,
		}
		hcp.Status.Platform = &hyperv1.PlatformStatus{
			AWS: &hyperv1.AWSPlatformStatus{
				DefaultWorkerSecurityGroupID: sgID,
			},
		}
	}
	meta.SetStatusCondition(&hcp.Status.Conditions, *condition)

	if err := r.Client.Status().Patch(ctx, hcp, client.MergeFromWithOptions(originalHCP, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	return creationErr
}

func awsSecurityGroupFilters(infraID string) []*ec2.Filter {
	return []*ec2.Filter{
		{
			Name:   awssdk.String(fmt.Sprintf("tag:kubernetes.io/cluster/%s", infraID)),
			Values: []*string{awssdk.String("owned")},
		},
		{
			Name:   awssdk.String("tag:Name"),
			Values: []*string{awssdk.String(awsSecurityGroupName(infraID))},
		},
	}
}

func awsSecurityGroupName(infraID string) string {
	return fmt.Sprintf("%s-default-sg", infraID)
}

func createAWSDefaultSecurityGroup(ctx context.Context, ec2Client ec2iface.EC2API, infraID, vpcID string, additionalTags []hyperv1.AWSResourceTag) (string, error) {
	logger := ctrl.LoggerFrom(ctx)

	// Determine VPC cidr
	vpcResult, err := ec2Client.DescribeVpcsWithContext(ctx, &ec2.DescribeVpcsInput{
		VpcIds: []*string{awssdk.String(vpcID)},
	})
	if err != nil {
		logger.Error(err, "Failed to describe vpc", "vpcID", vpcID)
		return "", fmt.Errorf("failed to describe vpc %s, code %s", vpcID, awsErrorCode(err))
	}
	if len(vpcResult.Vpcs) == 0 {
		return "", fmt.Errorf("vpc %s not found", vpcID)
	}
	vpcCIDR := awssdk.StringValue(vpcResult.Vpcs[0].CidrBlock)
	describeSGResult, err := ec2Client.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{Filters: awsSecurityGroupFilters(infraID)})
	if err != nil {
		logger.Error(err, "Failed to list security groups")
		return "", fmt.Errorf("cannot list security groups, code: %s", awsErrorCode(err))
	}
	sgID := ""
	var sg *ec2.SecurityGroup
	if len(describeSGResult.SecurityGroups) > 0 {
		sg = describeSGResult.SecurityGroups[0]
		sgID = awssdk.StringValue(sg.GroupId)
	}
	if sgID == "" {
		// Create a security group if one is not found

		tagKeys := sets.NewString()
		var tags []*ec2.Tag
		for _, tag := range additionalTags {
			tagKeys.Insert(tag.Key)
			tags = append(tags, &ec2.Tag{
				Key:   awssdk.String(tag.Key),
				Value: awssdk.String(tag.Value),
			})
		}
		clusterKey := fmt.Sprintf("kubernetes.io/cluster/%s", infraID)
		if !tagKeys.Has(clusterKey) {
			tags = append(tags, &ec2.Tag{
				Key:   awssdk.String(clusterKey),
				Value: awssdk.String("owned"),
			})
		}
		if !tagKeys.Has("Name") {
			tags = append(tags, &ec2.Tag{
				Key:   awssdk.String("Name"),
				Value: awssdk.String(awsSecurityGroupName(infraID)),
			})
		}
		createSGResult, err := ec2Client.CreateSecurityGroup(&ec2.CreateSecurityGroupInput{
			GroupName:   awssdk.String(awsSecurityGroupName(infraID)),
			Description: awssdk.String("default worker security group"),
			VpcId:       awssdk.String(vpcID),
			TagSpecifications: []*ec2.TagSpecification{
				{
					ResourceType: awssdk.String("security-group"),
					Tags:         tags,
				},
			},
		})
		if err != nil {
			logger.Error(err, "Failed to create security group")
			return "", fmt.Errorf("failed to create security group, code: %s", awsErrorCode(err))
		}
		sgID = awssdk.StringValue(createSGResult.GroupId)

		// Fetch just-created SG
		describeSGInput := &ec2.DescribeSecurityGroupsInput{
			GroupIds: []*string{awssdk.String(sgID)},
		}
		if err = ec2Client.WaitUntilSecurityGroupExistsWithContext(ctx, describeSGInput); err != nil {
			logger.Error(err, "Failed to wait for security group to exist")
			return "", fmt.Errorf("failed to find created security group (id: %s), code: %s", sgID, awsErrorCode(err))
		}

		describeSGResult, err = ec2Client.DescribeSecurityGroups(describeSGInput)
		if err != nil || len(describeSGResult.SecurityGroups) == 0 {
			logger.Error(err, "Failed to fetch security group", "sgID", sgID)
			return "", fmt.Errorf("failed to fetch security group (id: %s), code: %s", sgID, awsErrorCode(err))
		}

		sg = describeSGResult.SecurityGroups[0]
		logger.Info("Created security group", "id", sgID)
	}
	ingressPermissions := supportawsutil.DefaultWorkerSGIngressRules(vpcCIDR, sgID, awssdk.StringValue(sg.OwnerId))
	_, err = ec2Client.AuthorizeSecurityGroupIngress(&ec2.AuthorizeSecurityGroupIngressInput{
		GroupId:       awssdk.String(sgID),
		IpPermissions: ingressPermissions,
	})
	if err != nil {
		if awsErrorCode(err) != "InvalidPermission.Duplicate" {
			logger.Error(err, "Failed to set security group ingress rules")
			return "", fmt.Errorf("failed to set security group ingress rules, code: %s", awsErrorCode(err))
		}
		logger.Info("WARNING: got duplicate permissions error when setting security group ingress permissions", "sgID", sgID)
	}
	return sgID, nil
}

func (r *HostedControlPlaneReconciler) destroyAWSDefaultSecurityGroup(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	if hcp.Spec.Platform.Type != hyperv1.AWSPlatform {
		return nil
	}

	describeSGResult, err := r.ec2Client.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{Filters: awsSecurityGroupFilters(hcp.Spec.InfraID)})
	if err != nil {
		return fmt.Errorf("cannot list security groups: %w", err)
	}
	if len(describeSGResult.SecurityGroups) == 0 {
		return nil
	}
	sg := describeSGResult.SecurityGroups[0]

	if len(sg.IpPermissions) > 0 {
		if _, err = r.ec2Client.RevokeSecurityGroupIngressWithContext(ctx, &ec2.RevokeSecurityGroupIngressInput{
			GroupId:       sg.GroupId,
			IpPermissions: sg.IpPermissions,
		}); err != nil {
			return fmt.Errorf("failed to revoke security group ingress permissions for %s: %w", awssdk.StringValue(sg.GroupId), err)
		}
	}

	if len(sg.IpPermissionsEgress) > 0 {
		if _, err = r.ec2Client.RevokeSecurityGroupEgressWithContext(ctx, &ec2.RevokeSecurityGroupEgressInput{
			GroupId:       sg.GroupId,
			IpPermissions: sg.IpPermissionsEgress,
		}); err != nil {
			return fmt.Errorf("failed to revoke security group egress permissions for %s: %w", awssdk.StringValue(sg.GroupId), err)
		}
	}

	if _, err = r.ec2Client.DeleteSecurityGroupWithContext(ctx, &ec2.DeleteSecurityGroupInput{
		GroupId: sg.GroupId,
	}); err != nil {
		return fmt.Errorf("failed to delete security group %s: %w", awssdk.StringValue(sg.GroupId), err)
	}
	return nil

}

func awsErrorCode(err error) string {
	var awsErr awserr.Error
	if errors.As(err, &awsErr) {
		return awsErr.Code()
	}
	return ""
}

func hasValidCloudCredentials(hcp *hyperv1.HostedControlPlane) (string, bool) {
	if hcp.Spec.Platform.Type != hyperv1.AWSPlatform {
		return "", true
	}
	oidcConfigValid := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidOIDCConfiguration))
	if oidcConfigValid != nil && oidcConfigValid.Status == metav1.ConditionFalse {
		return "Invalid OIDC configuration", false
	}
	validIdentityProvider := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidAWSIdentityProvider))
	if validIdentityProvider != nil && validIdentityProvider.Status == metav1.ConditionFalse {
		return "Invalid AWS identity provider", false
	}
	return "", true
}

func (r *HostedControlPlaneReconciler) validateAWSKMSConfig(ctx context.Context, hcp *hyperv1.HostedControlPlane) {
	if hcp.Spec.SecretEncryption == nil || hcp.Spec.SecretEncryption.KMS == nil || hcp.Spec.SecretEncryption.KMS.AWS == nil {
		// AWS KMS not configured
		condition := metav1.Condition{
			Type:               string(hyperv1.ValidAWSKMSConfig),
			ObservedGeneration: hcp.Generation,
			Status:             metav1.ConditionUnknown,
			Message:            "AWS KMS is not configured",
			Reason:             hyperv1.StatusUnknownReason,
		}
		meta.SetStatusCondition(&hcp.Status.Conditions, condition)
		return
	}
	log := ctrl.LoggerFrom(ctx)

	guestClient, err := r.GetGuestClusterClient(ctx, hcp)
	if err != nil {
		// guest cluster is not ready yet.
		log.Error(err, "failed to create guest client")
		return
	}

	token, err := util.CreateTokenForServiceAccount(ctx, manifests.KASContainerAWSKMSProviderServiceAccount(), guestClient)
	if err != nil {
		// service account might not be created in the guest cluster or KAS is not operational.
		condition := metav1.Condition{
			Type:               string(hyperv1.ValidAWSKMSConfig),
			ObservedGeneration: hcp.Generation,
			Status:             metav1.ConditionUnknown,
			Message:            fmt.Sprintf("failed to create token for KMS provider service account: %v", err),
			Reason:             hyperv1.StatusUnknownReason,
		}
		meta.SetStatusCondition(&hcp.Status.Conditions, condition)
		return
	}

	roleArn := hcp.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN
	kmsKeyArn := hcp.Spec.SecretEncryption.KMS.AWS.ActiveKey.ARN

	creds, err := supportawsutil.AssumeRoleWithWebIdentity(r.awsSession, "control-plane-operator", roleArn, token)
	if err != nil {
		condition := metav1.Condition{
			Type:               string(hyperv1.ValidAWSKMSConfig),
			ObservedGeneration: hcp.Generation,
			Status:             metav1.ConditionFalse,
			Message:            fmt.Sprintf("failed to assume role web identity (%s), code: %s", roleArn, awsErrorCode(err)),
			Reason:             hyperv1.InvalidIAMRoleReason,
		}
		meta.SetStatusCondition(&hcp.Status.Conditions, condition)
		return
	}

	condition := metav1.Condition{
		Type:               string(hyperv1.ValidAWSKMSConfig),
		ObservedGeneration: hcp.Generation,
		Status:             metav1.ConditionTrue,
		Message:            hyperv1.AllIsWellMessage,
		Reason:             hyperv1.AsExpectedReason,
	}

	kmsService := kms.New(r.awsSession, awssdk.NewConfig().WithCredentials(creds))

	input := &kms.EncryptInput{
		KeyId:     awssdk.String(kmsKeyArn),
		Plaintext: []byte("text"),
	}
	if _, err = kmsService.Encrypt(input); err != nil {
		condition = metav1.Condition{
			Type:               string(hyperv1.ValidAWSKMSConfig),
			ObservedGeneration: hcp.Generation,
			Status:             metav1.ConditionFalse,
			Message:            fmt.Sprintf("failed to encrypt data using KMS (key: %s), code: %s", kmsKeyArn, awsErrorCode(err)),
			Reason:             hyperv1.AWSErrorReason,
		}
	}

	meta.SetStatusCondition(&hcp.Status.Conditions, condition)
}

func (r *HostedControlPlaneReconciler) GetGuestClusterClient(ctx context.Context, hcp *hyperv1.HostedControlPlane) (*kubernetes.Clientset, error) {
	kubeconfigSecret := manifests.KASExternalKubeconfigSecret(hcp.Namespace, hcp.Spec.KubeConfig)
	if err := r.Get(ctx, client.ObjectKeyFromObject(kubeconfigSecret), kubeconfigSecret); err != nil {
		return nil, err
	}

	kubeconfig := kubeconfigSecret.Data[DefaultAdminKubeconfigKey]
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}
