package hostedcontrolplane

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/autoscaler"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cco"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/aws"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/kubevirt"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/openstack"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/powervs"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/clusterpolicy"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cno"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/configoperator"
	kubevirtcsi "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/csi/kubevirt"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cvo"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/dnsoperator"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/etcd"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/fg"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ignition"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ignitionserver"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/infra"
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
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pkioperator"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/registryoperator"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/routecm"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/scheduler"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/snapshotcontroller"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/storage"
	autoscalerv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/autoscaler"
	awsccmv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/aws"
	azureccmv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/azure"
	kubevirtccmv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/kubevirt"
	openstackccmv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/openstack"
	powervsccmv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/powervs"
	ccov2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/cloud_credential_operator"
	clusterpolicyv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/clusterpolicy"
	cnov2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/cno"
	configoperatorv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/configoperator"
	kubevirtcsiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/csi/kubevirt"
	cvov2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/cvo"
	dnsoperatorv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/dnsoperator"
	etcdv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/etcd"
	fgv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/fg"
	ignitionserverv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/ignitionserver"
	ignitionproxyv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/ignitionserver_proxy"
	ingressoperatorv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/ingressoperator"
	kasv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas"
	kcmv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kcm"
	konnectivityv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/konnectivity_agent"
	schedulerv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kube_scheduler"
	machineapproverv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/machine_approver"
	ntov2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/nto"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	oauthv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oauth"
	oauthapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oauth_apiserver"
	ocmv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/ocm"
	olmv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/olm"
	pkioperatorv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/pkioperator"
	registryoperatorv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/registryoperator"
	routecmv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/routecm"
	routerv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/router"
	snapshotcontrollerv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/snapshotcontroller"
	storagev2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/storage"
	pkimanifests "github.com/openshift/hypershift/control-plane-pki-operator/manifests"
	ignitionmanifests "github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	sharedingress "github.com/openshift/hypershift/hypershift-operator/controllers/sharedingress"
	supportawsutil "github.com/openshift/hypershift/support/awsutil"
	hyperazureutil "github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/catalogs"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/conditions"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/events"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/secretproviderclass"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
	"github.com/openshift/hypershift/support/validations"

	routev1 "github.com/openshift/api/route/v1"

	awssdk "github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/kms"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	"github.com/Azure/msi-dataplane/pkg/dataplane"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/duration"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

const (
	// LastAppliedSecurityGroupTagsAnnotation is an annotation that stores the last applied security group tags for the hosted cluster.
	// This is used to track changes to security group tags and ensure that tags changes are applied to the default security group.
	// The value is a JSON string containing the tags.
	// Example: {"Name": "my-cluster", "Environment": "production"}
	LastAppliedSecurityGroupTagsAnnotation = "hypershift.openshift.io/last-applied-security-group-tags"

	finalizer                              = "hypershift.openshift.io/finalizer"
	DefaultAdminKubeconfigKey              = "kubeconfig"
	ImageStreamAutoscalerImage             = "cluster-autoscaler"
	ImageStreamClusterMachineApproverImage = "cluster-machine-approver"

	resourceDeletionTimeout = 10 * time.Minute

	hcpReadyRequeueInterval    = 1 * time.Minute
	hcpNotReadyRequeueInterval = 15 * time.Second

	cpoAzureCredentials = "CPOAzureCredentials"
	kmsAzureCredentials = "KMSAzureCredentials"
)

var catalogImages map[string]string

type HostedControlPlaneReconciler struct {
	client.Client

	components []component.ControlPlaneComponent

	// ManagementClusterCapabilities can be asked for support of optional management cluster capabilities
	ManagementClusterCapabilities capabilities.CapabiltyChecker

	// SetDefaultSecurityContext is used to configure Security Context for containers
	SetDefaultSecurityContext bool

	// CertRotationScale determines how quickly we rotate certificates - should only be set faster in testing
	CertRotationScale time.Duration

	Log                                     logr.Logger
	ReleaseProvider                         releaseinfo.ProviderWithOpenShiftImageRegistryOverrides
	UserReleaseProvider                     releaseinfo.Provider
	createOrUpdate                          func(hcp *hyperv1.HostedControlPlane) upsert.CreateOrUpdateFN
	EnableCIDebugOutput                     bool
	OperateOnReleaseImage                   string
	DefaultIngressDomain                    string
	MetricsSet                              metrics.MetricsSet
	SREConfigHash                           string
	ec2Client                               ec2iface.EC2API
	awsSession                              *session.Session
	reconcileInfrastructureStatus           func(ctx context.Context, hcp *hyperv1.HostedControlPlane) (infra.InfrastructureStatus, error)
	EnableCVOManagementClusterMetricsAccess bool
	ImageMetadataProvider                   util.ImageMetadataProvider
	cpoAzureCredentialsLoaded               sync.Map
	kmsAzureCredentialsLoaded               sync.Map

	IsCPOV2 bool
}

func (r *HostedControlPlaneReconciler) SetupWithManager(mgr ctrl.Manager, createOrUpdate upsert.CreateOrUpdateFN) error {
	r.setup(createOrUpdate)
	b := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedControlPlane{}).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 10*time.Second),
		})
	for _, handler := range r.eventHandlers(mgr.GetScheme(), mgr.GetRESTMapper()) {
		b.Watches(handler.obj, handler.handler)
	}
	if _, err := b.Build(r); err != nil {
		return fmt.Errorf("failed setting up with a controller manager %w", err)
	}

	// Set based on SCC capability
	// When SCC is available (OpenShift), the container's security context and UID range is automatically set
	// When SCC is not available (Kubernetes), we want to explicitly set a default (non-root) security context
	r.SetDefaultSecurityContext = !r.ManagementClusterCapabilities.Has(capabilities.CapabilitySecurityContextConstraint)

	r.reconcileInfrastructureStatus = r.defaultReconcileInfrastructureStatus

	r.ec2Client, r.awsSession = GetEC2Client()

	if r.IsCPOV2 {
		r.registerComponents()
	}

	return nil
}

func (r *HostedControlPlaneReconciler) registerComponents() {
	r.components = append(r.components,
		pkioperatorv2.NewComponent(r.CertRotationScale),
		etcdv2.NewComponent(),
		fgv2.NewComponent(),
		kasv2.NewComponent(),
		kcmv2.NewComponent(),
		schedulerv2.NewComponent(),
		oapiv2.NewComponent(),
		routerv2.NewComponent(),
		oauthapiv2.NewComponent(),
		autoscalerv2.NewComponent(),
		cvov2.NewComponent(r.EnableCVOManagementClusterMetricsAccess),
		ocmv2.NewComponent(),
		oauthv2.NewComponent(),
		routecmv2.NewComponent(),
		clusterpolicyv2.NewComponent(),
		configoperatorv2.NewComponent(r.ReleaseProvider.GetRegistryOverrides(), r.ReleaseProvider.GetOpenShiftImageRegistryOverrides()),
		awsccmv2.NewComponent(),
		azureccmv2.NewComponent(),
		kubevirtccmv2.NewComponent(),
		openstackccmv2.NewComponent(),
		powervsccmv2.NewComponent(),
		ccov2.NewComponent(),
		storagev2.NewComponent(),
		kubevirtcsiv2.NewComponent(),
		cnov2.NewComponent(),
		ntov2.NewComponent(),
		dnsoperatorv2.NewComponent(),
		machineapproverv2.NewComponent(),
		ingressoperatorv2.NewComponent(),
		snapshotcontrollerv2.NewComponent(),
		registryoperatorv2.NewComponent(),
		konnectivityv2.NewComponent(),
		ignitionserverv2.NewComponent(r.ReleaseProvider, r.DefaultIngressDomain),
		ignitionproxyv2.NewComponent(r.DefaultIngressDomain),
	)
	r.components = append(r.components,
		olmv2.NewComponents(r.ManagementClusterCapabilities.Has(capabilities.CapabilityImageStream))...,
	)
}

func GetEC2Client() (ec2iface.EC2API, *session.Session) {
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

func (r *HostedControlPlaneReconciler) eventHandlers(scheme *runtime.Scheme, restMapper meta.RESTMapper) []eventHandler {
	handlers := []eventHandler{
		{obj: &corev1.Event{}, handler: handler.EnqueueRequestsFromMapFunc(r.hostedControlPlaneInNamespace)},
		{obj: &corev1.ConfigMap{}, handler: handler.EnqueueRequestsFromMapFunc(r.hostedControlPlaneInNamespace)},
		{obj: &corev1.Service{}, handler: handler.EnqueueRequestForOwner(scheme, restMapper, &hyperv1.HostedControlPlane{})},
		{obj: &appsv1.Deployment{}, handler: handler.EnqueueRequestForOwner(scheme, restMapper, &hyperv1.HostedControlPlane{})},
		{obj: &appsv1.StatefulSet{}, handler: handler.EnqueueRequestForOwner(scheme, restMapper, &hyperv1.HostedControlPlane{})},
		{obj: &corev1.Secret{}, handler: handler.EnqueueRequestForOwner(scheme, restMapper, &hyperv1.HostedControlPlane{})},
		{obj: &corev1.ServiceAccount{}, handler: handler.EnqueueRequestForOwner(scheme, restMapper, &hyperv1.HostedControlPlane{})},
		{obj: &policyv1.PodDisruptionBudget{}, handler: handler.EnqueueRequestForOwner(scheme, restMapper, &hyperv1.HostedControlPlane{})},
		{obj: &prometheusoperatorv1.PodMonitor{}, handler: handler.EnqueueRequestForOwner(scheme, restMapper, &hyperv1.HostedControlPlane{})},
		{obj: &prometheusoperatorv1.ServiceMonitor{}, handler: handler.EnqueueRequestForOwner(scheme, restMapper, &hyperv1.HostedControlPlane{})},
		{obj: &prometheusoperatorv1.PrometheusRule{}, handler: handler.EnqueueRequestForOwner(scheme, restMapper, &hyperv1.HostedControlPlane{})},
		{obj: &rbacv1.Role{}, handler: handler.EnqueueRequestForOwner(scheme, restMapper, &hyperv1.HostedControlPlane{})},
		{obj: &rbacv1.RoleBinding{}, handler: handler.EnqueueRequestForOwner(scheme, restMapper, &hyperv1.HostedControlPlane{})},
		{obj: &batchv1.CronJob{}, handler: handler.EnqueueRequestForOwner(scheme, restMapper, &hyperv1.HostedControlPlane{})},
		{obj: &batchv1.Job{}, handler: handler.EnqueueRequestForOwner(scheme, restMapper, &hyperv1.HostedControlPlane{})},
	}
	if r.ManagementClusterCapabilities.Has(capabilities.CapabilityRoute) {
		handlers = append(handlers, eventHandler{obj: &routev1.Route{}, handler: handler.EnqueueRequestForOwner(scheme, restMapper, &hyperv1.HostedControlPlane{})})
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

	// Return early if deleted
	if !hostedControlPlane.DeletionTimestamp.IsZero() {
		condition := &metav1.Condition{
			Type: string(hyperv1.AWSDefaultSecurityGroupDeleted),
		}
		if shouldCleanupCloudResources(r.Log, hostedControlPlane) {
			if code, destroyErr := r.destroyAWSDefaultSecurityGroup(ctx, hostedControlPlane); destroyErr != nil {
				condition.Message = "failed to delete AWS default security group"
				if code == "DependencyViolation" {
					condition.Message = destroyErr.Error()
				}
				condition.Reason = hyperv1.AWSErrorReason
				condition.Status = metav1.ConditionFalse
				meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, *condition)

				if err := r.Client.Status().Patch(ctx, hostedControlPlane, client.MergeFromWithOptions(originalHostedControlPlane, client.MergeFromWithOptimisticLock{})); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to update status on hcp for security group deletion: %w. Condition error message: %v", err, condition.Message)
				}

				if code == "UnauthorizedOperation" {
					r.Log.Error(destroyErr, "Skipping AWS default security group deletion because of unauthorized operation.")
				}
				if code == "DependencyViolation" {
					r.Log.Error(destroyErr, "Skipping AWS default security group deletion because of dependency violation.")
				} else {
					return ctrl.Result{}, fmt.Errorf("failed to delete AWS default security group: %w", destroyErr)
				}
			} else {
				condition.Message = hyperv1.AllIsWellMessage
				condition.Reason = hyperv1.AsExpectedReason
				condition.Status = metav1.ConditionTrue
				meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, *condition)

				if err := r.Client.Status().Patch(ctx, hostedControlPlane, client.MergeFromWithOptions(originalHostedControlPlane, client.MergeFromWithOptimisticLock{})); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to update status on hcp for security group deletion: %w. Condition message: %v", err, condition.Message)
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

	if r.OperateOnReleaseImage != "" && r.OperateOnReleaseImage != util.HCPControlPlaneReleaseImage(hostedControlPlane) {
		r.Log.Info("releaseImage is " + util.HCPControlPlaneReleaseImage(hostedControlPlane) + ", but this operator is configured for " + r.OperateOnReleaseImage + ", skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Reconcile global configuration validation status
	{
		condition := metav1.Condition{
			Type:               string(hyperv1.ValidHostedControlPlaneConfiguration),
			ObservedGeneration: hostedControlPlane.Generation,
		}
		if err := r.validateConfigAndClusterCapabilities(ctx, hostedControlPlane); err != nil {
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

	// Validate KMS config
	switch hostedControlPlane.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		r.validateAWSKMSConfig(ctx, hostedControlPlane)
	case hyperv1.AzurePlatform:
		r.validateAzureKMSConfig(ctx, hostedControlPlane)
	}

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
				if util.HCPOAuthEnabled(hostedControlPlane) {
					hostedControlPlane.Status.OAuthCallbackURLTemplate = fmt.Sprintf("https://%s:%d/oauth2callback/[identity-provider-name]", infraStatus.OAuthHost, infraStatus.OAuthPort)
				}
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
		healthCheckErr := r.healthCheckKASLoadBalancers(ctx, hostedControlPlane)

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
		case healthCheckErr != nil:
			reason = hyperv1.KASLoadBalancerNotReachableReason
			message = healthCheckErr.Error()
		default:
			reason = hyperv1.AsExpectedReason
			message = ""
			status = metav1.ConditionTrue
		}
		hostedControlPlane.Status.Ready = status == metav1.ConditionTrue
		condition := metav1.Condition{
			Type:               string(hyperv1.HostedControlPlaneAvailable),
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: hostedControlPlane.Generation,
		}
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, condition)
	}

	// Admin Kubeconfig
	kubeconfig := manifests.KASAdminKubeconfigSecret(hostedControlPlane.Namespace, hostedControlPlane.Spec.KubeConfig)
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

	if err := setKASCustomKubeconfigStatus(ctx, hostedControlPlane, r.Client); err != nil {
		return reconcile.Result{}, err
	}

	explicitOauthConfig := hostedControlPlane.Spec.Configuration != nil && hostedControlPlane.Spec.Configuration.OAuth != nil
	if explicitOauthConfig {
		hostedControlPlane.Status.KubeadminPassword = nil
	} else {
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
	}

	// Reconcile valid release info status
	releaseImage, err := r.LookupReleaseImage(ctx, hostedControlPlane)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to look up release image metadata: %w", err)
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

	// Block here if the cluster configuration does not pass validation
	{
		validConfig := meta.FindStatusCondition(hostedControlPlane.Status.Conditions, string(hyperv1.ValidHostedControlPlaneConfiguration))
		if validConfig != nil && validConfig.Status == metav1.ConditionFalse {
			r.Log.Info("Configuration is invalid, reconciliation is blocked")
			return reconcile.Result{}, nil
		}
	}

	// Perform the hosted control plane reconciliation
	result, err := r.update(ctx, hostedControlPlane, releaseImage)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update control plane: %w", err)
	}

	r.Log.Info("Successfully reconciled")

	if !result.IsZero() {
		return result, nil
	}

	if !hostedControlPlane.Status.Ready {
		return ctrl.Result{RequeueAfter: hcpNotReadyRequeueInterval}, nil
	}

	return ctrl.Result{RequeueAfter: hcpReadyRequeueInterval}, nil
}

// healthCheckKASLoadBalancers performs a health check on the KubeAPI server /healthz endpoint using the public and private load balancers hostnames directly
// This will detect if load balancers are down or deleted out of band
func (r *HostedControlPlaneReconciler) healthCheckKASLoadBalancers(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	serviceStrategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.APIServer)
	if serviceStrategy == nil {
		return fmt.Errorf("APIServer service strategy not specified")
	}

	switch {
	case !util.IsPublicHCP(hcp):
		// When the cluster is private, checking the load balancers will depend on whether the load balancer is
		// using the right subnets. To avoid uncertainty, we'll limit the check to the service endpoint.
		if hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
			return healthCheckKASEndpoint(manifests.KubeAPIServerService("").Name, config.KASSVCIBMCloudPort)
		}
		return healthCheckKASEndpoint(manifests.KubeAPIServerService("").Name, config.KASSVCPort)
	case serviceStrategy.Type == hyperv1.Route:
		if hcp.Spec.Platform.Type != hyperv1.IBMCloudPlatform {
			externalRoute := manifests.KubeAPIServerExternalPublicRoute(hcp.Namespace)
			if err := r.Get(ctx, client.ObjectKeyFromObject(externalRoute), externalRoute); err != nil {
				return fmt.Errorf("failed to get kube apiserver external route: %w", err)
			}

			endpoint, port, err := kas.GetHealthcheckEndpointForRoute(externalRoute, hcp)
			if err != nil {
				return err
			}
			return healthCheckKASEndpoint(endpoint, port)
		}
	case serviceStrategy.Type == hyperv1.LoadBalancer:
		svc := manifests.KubeAPIServerService(hcp.Namespace)
		port := config.KASSVCPort
		if hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
			port = config.KASSVCIBMCloudPort
		}
		if hcp.Spec.Platform.Type == hyperv1.AzurePlatform ||
			hcp.Annotations[hyperv1.ManagementPlatformAnnotation] == string(hyperv1.AzurePlatform) {
			// If Azure or Kubevirt on Azure we get the SVC handling the LB.
			// TODO(alberto): remove this hack when having proper traffic management for Azure.
			svc = manifests.KubeAPIServerServiceAzureLB(hcp.Namespace)
			port = config.KASSVCLBAzurePort
		}
		if err := r.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
			return fmt.Errorf("failed to get kube apiserver service: %w", err)
		}
		if len(svc.Status.LoadBalancer.Ingress) == 0 ||
			svc.Status.LoadBalancer.Ingress[0].Hostname == "" && svc.Status.LoadBalancer.Ingress[0].IP == "" {
			return fmt.Errorf("APIServer load balancer is not provisioned")
		}
		LBIngress := svc.Status.LoadBalancer.Ingress[0]
		ingressPoint := ""
		if LBIngress.Hostname != "" {
			ingressPoint = LBIngress.Hostname
		} else if LBIngress.IP != "" {
			ingressPoint = LBIngress.IP
		}
		return healthCheckKASEndpoint(ingressPoint, port)
	}
	return nil
}

func healthCheckKASEndpoint(ingressPoint string, port int) error {
	healthEndpoint := fmt.Sprintf("https://%s:%d/healthz", ingressPoint, port)

	httpClient := util.InsecureHTTPClient()
	httpClient.Timeout = 10 * time.Second
	resp, err := httpClient.Get(healthEndpoint)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("APIServer endpoint %s is not healthy", ingressPoint)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) validateConfigAndClusterCapabilities(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	for _, svc := range hcp.Spec.Services {
		if svc.Type == hyperv1.Route && !r.ManagementClusterCapabilities.Has(capabilities.CapabilityRoute) {
			return fmt.Errorf("cluster does not support Routes, but service %q is exposed via a Route", svc.Service)
		}
	}

	if hcp.Spec.Platform.Type == hyperv1.AzurePlatform && hyperazureutil.IsAroHCP() {
		if err := r.verifyResourceGroupLocationsMatch(ctx, hcp); err != nil {
			return err
		}
	}

	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.Authentication != nil {
		if err := validations.ValidateAuthenticationSpec(ctx, r.Client, hcp.Spec.Configuration.Authentication, hcp.Namespace, []string{hcp.Spec.IssuerURL}); err != nil {
			return err
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
	return r.ReleaseProvider.Lookup(lookupCtx, util.HCPControlPlaneReleaseImage(hcp), pullSecret.Data[corev1.DockerConfigJsonKey])
}

func (r *HostedControlPlaneReconciler) update(ctx context.Context, hostedControlPlane *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage) (reconcile.Result, error) {
	createOrUpdate := r.createOrUpdate(hostedControlPlane)

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

	// releaseImage might be overridden by spec.controlPlaneReleaseImage
	// User facing components should reflect the version from spec.releaseImage
	pullSecret := common.PullSecret(hostedControlPlane.Namespace)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
		return ctrl.Result{}, err
	}
	// UserReleaseProvider doesn't include registry overrides as they should not get propagated to the data plane.
	userReleaseImage, err := r.UserReleaseProvider.Lookup(ctx, hostedControlPlane.Spec.ReleaseImage, pullSecret.Data[corev1.DockerConfigJsonKey])
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get lookup release image: %w", err)
	}

	userReleaseImageProvider := imageprovider.New(userReleaseImage)
	releaseImageProvider := imageprovider.New(releaseImage)

	var errs []error
	if err := r.reconcile(ctx, hostedControlPlane, createOrUpdate, releaseImageProvider, userReleaseImageProvider, infraStatus); err != nil {
		errs = append(errs, err)
	}

	if r.IsCPOV2 {
		if err := r.reconcileCPOV2(ctx, hostedControlPlane, infraStatus, releaseImageProvider, userReleaseImageProvider); err != nil {
			errs = append(errs, err)
		}
	}

	// Get the latest HCP in memory before we patch the status
	if err = r.Client.Get(ctx, client.ObjectKeyFromObject(hostedControlPlane), hostedControlPlane); err != nil {
		return reconcile.Result{}, err
	}

	originalHostedControlPlane := hostedControlPlane.DeepCopy()
	missingImages := sets.New(releaseImageProvider.GetMissingImages()...).Insert(userReleaseImageProvider.GetMissingImages()...)
	if missingImages.Len() == 0 {
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.ValidReleaseInfo),
			Status:             metav1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			Message:            hyperv1.AllIsWellMessage,
			ObservedGeneration: hostedControlPlane.Generation,
		})
	} else {
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.ValidReleaseInfo),
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.MissingReleaseImagesReason,
			Message:            strings.Join(missingImages.UnsortedList(), ", "),
			ObservedGeneration: hostedControlPlane.Generation,
		})
	}

	if err := r.Client.Status().Patch(ctx, hostedControlPlane, client.MergeFromWithOptions(originalHostedControlPlane, client.MergeFromWithOptimisticLock{})); err != nil {
		errs = append(errs, fmt.Errorf("failed to update status: %w", err))
	}

	return ctrl.Result{}, utilerrors.NewAggregate(errs)
}

func (r *HostedControlPlaneReconciler) reconcileCPOV2(ctx context.Context, hcp *hyperv1.HostedControlPlane, infraStatus infra.InfrastructureStatus, releaseImageProvider, userReleaseImageProvider imageprovider.ReleaseImageProvider) error {
	if err := r.cleanupOldKonnectivityServerDeployment(ctx, hcp); err != nil {
		return err
	}

	if hcp.Spec.Platform.Type != hyperv1.IBMCloudPlatform {
		role := ignitionmanifests.ProxyRole(hcp.Namespace)
		sa := ignitionmanifests.ProxyServiceAccount(hcp.Namespace)
		roleBinding := ignitionmanifests.ProxyRoleBinding(hcp.Namespace)

		for _, resource := range []client.Object{role, sa, roleBinding} {
			if _, err := util.DeleteIfNeeded(ctx, r.Client, resource); err != nil {
				r.Log.Error(err, "Failed to delete deprecated resource", "resource", client.ObjectKeyFromObject(resource).String())
			}
		}
	}

	cpContext := component.ControlPlaneContext{
		Context:                   ctx,
		Client:                    r.Client,
		HCP:                       hcp,
		ApplyProvider:             upsert.NewApplyProvider(r.EnableCIDebugOutput),
		InfraStatus:               infraStatus,
		ReleaseImageProvider:      releaseImageProvider,
		UserReleaseImageProvider:  userReleaseImageProvider,
		SetDefaultSecurityContext: r.SetDefaultSecurityContext,
		MetricsSet:                r.MetricsSet,
		EnableCIDebugOutput:       r.EnableCIDebugOutput,
		ImageMetadataProvider:     r.ImageMetadataProvider,
	}

	var errs []error
	for _, c := range r.components {
		r.Log.Info("Reconciling component", "component_name", c.Name())
		if err := c.Reconcile(cpContext); err != nil {
			errs = append(errs, err)
		}
	}

	return utilerrors.NewAggregate(errs)
}

// useHCPRouter returns true if a dedicated common router is created for a HCP to handle ingress for the managed endpoints.
// This is true when the API input specifies intent for the following:
// 1 - AWS endpointAccess is private somehow (i.e. publicAndPrivate or private) or is public and configured with external DNS.
// 2 - When 1 is true, we recommend (and automate via CLI) ServicePublishingStrategy to be "Route" for all endpoints but the KAS
// which needs a dedicated Service type LB external to be exposed if no external DNS is supported.
// Otherwise, the Routes use the management cluster Domain and resolve through the default ingress controller.
func useHCPRouter(hostedControlPlane *hyperv1.HostedControlPlane) bool {
	if sharedingress.UseSharedIngress() {
		return false
	}
	return labelHCPRoutes(hostedControlPlane)
}

func labelHCPRoutes(hcp *hyperv1.HostedControlPlane) bool {
	return util.IsPrivateHCP(hcp) || util.IsPublicKASWithDNS(hcp)
}

func IsStorageAndCSIManaged(hostedControlPlane *hyperv1.HostedControlPlane) bool {
	if hostedControlPlane.Spec.Platform.Type == hyperv1.IBMCloudPlatform || hostedControlPlane.Spec.Platform.Type == hyperv1.PowerVSPlatform {
		return false
	}
	return true
}

func (r *HostedControlPlaneReconciler) reconcile(ctx context.Context, hostedControlPlane *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN, releaseImageProvider, userReleaseImageProvider imageprovider.ReleaseImageProvider, infraStatus infra.InfrastructureStatus) error {
	// Reconcile default service account
	r.Log.Info("Reconciling default service account")
	if err := r.reconcileDefaultServiceAccount(ctx, hostedControlPlane, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile default service account: %w", err)
	}

	openShiftTrustedCABundleConfigMapForCPOExists, err := doesOpenShiftTrustedCABundleConfigMapForCPOExist(ctx, r.Client, hostedControlPlane.Namespace)
	if err != nil {
		return err
	}

	// Reconcile PKI
	if _, exists := hostedControlPlane.Annotations[hyperv1.DisablePKIReconciliationAnnotation]; !exists {
		r.Log.Info("Reconciling PKI")
		if err := r.reconcilePKI(ctx, hostedControlPlane, infraStatus, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile PKI: %w", err)
		}

		if !r.IsCPOV2 {
			r.Log.Info("Reconciling Control Plane PKI Operator")
			if err := r.reconcileControlPlanePKIOperator(ctx, hostedControlPlane, releaseImageProvider, createOrUpdate, openShiftTrustedCABundleConfigMapForCPOExists, r.CertRotationScale); err != nil {
				return fmt.Errorf("failed to reconcile control plane pki operator: %w", err)
			}
		}

	}

	// Reconcile etcd
	r.Log.Info("Reconciling Etcd")
	switch hostedControlPlane.Spec.Etcd.ManagementType {
	case hyperv1.Managed:
		if !r.IsCPOV2 {
			statefulSet := manifests.EtcdStatefulSet(hostedControlPlane.Namespace)
			if err := r.reconcileManagedEtcd(ctx, hostedControlPlane, releaseImageProvider, createOrUpdate, statefulSet); err != nil {
				return fmt.Errorf("failed to reconcile etcd: %w", err)
			}
			// Block until etcd is fully rolled out at the desired generation
			if ready := util.IsStatefulSetReady(ctx, statefulSet); !ready {
				r.Log.Info("Waiting for etcd statefulset to become ready")
				return nil
			}
		}
	case hyperv1.Unmanaged:
		if err := r.reconcileUnmanagedEtcd(ctx, hostedControlPlane, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile etcd: %w", err)
		}
	default:
		return fmt.Errorf("unrecognized etcd management type: %s", hostedControlPlane.Spec.Etcd.ManagementType)
	}

	r.Log.Info("Looking up observed configuration")
	observedConfig := &globalconfig.ObservedConfig{}
	if err := globalconfig.ReadObservedConfig(ctx, r.Client, observedConfig, hostedControlPlane.Namespace); err != nil {
		return fmt.Errorf("failed to read observed global config: %w", err)
	}

	if err := r.reconcileSREMetricsConfig(ctx, createOrUpdate, hostedControlPlane.Namespace); err != nil {
		return fmt.Errorf("failed to reconcile metrics config: %w", err)
	}

	var featureGates []string

	if !r.IsCPOV2 {
		// Reconcile the feature gate Job
		r.Log.Info("Reconciling the feature gate job to capture release featuregates")
		proceed, err := r.reconcileFeatureGateGenerationJob(ctx, hostedControlPlane, createOrUpdate, releaseImageProvider, userReleaseImageProvider)
		if err != nil {
			return fmt.Errorf("failed to reconcile the featuregate generation job: %w", err)
		}
		if !proceed {
			r.Log.Info("Waiting for successful completion of featuregate generation job")
			return nil
		}

		featureGates, err = config.FeatureGatesFromConfigMap(ctx, r.Client, hostedControlPlane.Namespace)
		if err != nil {
			return fmt.Errorf("failed to read feature gates from configmap: %w", err)
		}

		// Reconcile Cloud Provider Config
		r.Log.Info("Reconciling cloud provider config")
		if err := r.reconcileCloudProviderConfig(ctx, hostedControlPlane, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile cloud provider config: %w", err)
		}

		// Reconcile kube apiserver
		r.Log.Info("Reconciling Kube API Server")
		kubeAPIServerDeployment := manifests.KASDeployment(hostedControlPlane.Namespace)
		if err := r.reconcileKubeAPIServer(ctx, hostedControlPlane, releaseImageProvider, userReleaseImageProvider, infraStatus.APIHost, infraStatus.APIPort, infraStatus.OAuthHost, infraStatus.OAuthPort, createOrUpdate, kubeAPIServerDeployment, featureGates); err != nil {
			return fmt.Errorf("failed to reconcile kube apiserver: %w", err)
		}

		// Block until kube apiserver is fully ready to enforce upgrade order of version skew policy
		// https://kubernetes.io/releases/version-skew-policy/#supported-component-upgrade-order
		if ready := util.IsDeploymentReady(ctx, kubeAPIServerDeployment); !ready {
			r.Log.Info("Waiting for kube apiserver deployment to become ready")
			return nil
		}

		// Reconcile kube controller manager
		r.Log.Info("Reconciling Kube Controller Manager")
		kcmDeployment := manifests.KCMDeployment(hostedControlPlane.Namespace)
		if err := r.reconcileKubeControllerManager(ctx, hostedControlPlane, releaseImageProvider, createOrUpdate, kcmDeployment, featureGates); err != nil {
			return fmt.Errorf("failed to reconcile kube controller manager: %w", err)
		}

		// Reconcile kube scheduler
		r.Log.Info("Reconciling Kube Scheduler")
		schedulerDeployment := manifests.SchedulerDeployment(hostedControlPlane.Namespace)
		if err := r.reconcileKubeScheduler(ctx, hostedControlPlane, releaseImageProvider, createOrUpdate, schedulerDeployment, featureGates); err != nil {
			return fmt.Errorf("failed to reconcile kube scheduler: %w", err)
		}

		// Reconcile openshift apiserver
		r.Log.Info("Reconciling OpenShift API Server")
		openshiftAPIServerDeployment := manifests.OpenShiftAPIServerDeployment(hostedControlPlane.Namespace)
		if err := r.reconcileOpenShiftAPIServer(ctx, hostedControlPlane, observedConfig, releaseImageProvider, createOrUpdate, openshiftAPIServerDeployment, featureGates); err != nil {
			return fmt.Errorf("failed to reconcile openshift apiserver: %w", err)
		}

		// Block on kube controller manager being rolled out at the desired version
		if ready := util.IsDeploymentReady(ctx, kcmDeployment); !ready {
			r.Log.Info("Waiting for kube controller manager deployment to become ready")
			return nil
		}

		// Block on kube scheduler being rolled out at the desired version
		if ready := util.IsDeploymentReady(ctx, schedulerDeployment); !ready {
			r.Log.Info("Waiting for kube scheduler deployment to become ready")
			return nil
		}

		// Block until openshift apiserver is fully ready to enforce upgrade order of version skew policy
		// https://github.com/openshift/enhancements/blob/master/enhancements/update/eus-upgrades-mvp.md
		if ready := util.IsDeploymentReady(ctx, openshiftAPIServerDeployment); !ready {
			r.Log.Info("Waiting for openshift apiserver deployment to become ready")
			return nil
		}

		if useHCPRouter(hostedControlPlane) {
			r.Log.Info("Reconciling router")
			if err := r.reconcileRouter(ctx, hostedControlPlane, releaseImageProvider, createOrUpdate); err != nil {
				return fmt.Errorf("failed to reconcile router: %w", err)
			}
		}
	}

	if useHCPRouter(hostedControlPlane) {
		if err := r.admitHCPManagedRoutes(ctx, hostedControlPlane, infraStatus.InternalHCPRouterHost, infraStatus.ExternalHCPRouterHost); err != nil {
			return fmt.Errorf("failed to admit HCP managed routes: %w", err)
		}
		if err := r.cleanupOldRouterResources(ctx, hostedControlPlane); err != nil {
			return fmt.Errorf("failed to cleanup old router resources: %w", err)
		}
	}

	if !r.IsCPOV2 {
		if _, exists := hostedControlPlane.Annotations[hyperv1.DisableIgnitionServerAnnotation]; !exists {
			r.Log.Info("Reconciling ignition server")
			if err := ignitionserver.ReconcileIgnitionServer(ctx,
				r.Client,
				createOrUpdate,
				releaseImageProvider.Version(),
				releaseImageProvider.GetImage(util.CPOImageName),
				releaseImageProvider.ComponentImages(),
				hostedControlPlane,
				r.DefaultIngressDomain,
				// The healthz handler was added before the CPO started to manage the ignition server, and it's the same binary,
				// so we know it always exists here.
				true,
				r.ReleaseProvider.GetRegistryOverrides(),
				util.ConvertOpenShiftImageRegistryOverridesToCommandLineFlag(r.ReleaseProvider.GetOpenShiftImageRegistryOverrides()),
				r.ManagementClusterCapabilities.Has(capabilities.CapabilitySecurityContextConstraint),
				config.OwnerRefFrom(hostedControlPlane),
				openShiftTrustedCABundleConfigMapForCPOExists,
				r.ReleaseProvider.GetMirroredReleaseImage(),
				labelHCPRoutes(hostedControlPlane),
			); err != nil {
				return fmt.Errorf("failed to reconcile ignition server: %w", err)
			}
		} else {
			r.Log.Info("Skipping ignition server reconciliation as specified")
		}
	}

	if !r.IsCPOV2 {
		// Reconcile Konnectivity
		r.Log.Info("Reconciling Konnectivity")
		if err := r.reconcileKonnectivity(ctx, hostedControlPlane, releaseImageProvider, infraStatus, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile konnectivity: %w", err)
		}
	}

	if util.HCPOAuthEnabled(hostedControlPlane) {
		// Reconcile kubeadmin password
		r.Log.Info("Reconciling kubeadmin password secret")
		explicitOauthConfig := hostedControlPlane.Spec.Configuration != nil && hostedControlPlane.Spec.Configuration.OAuth != nil
		if err := r.reconcileKubeadminPassword(ctx, hostedControlPlane, explicitOauthConfig, createOrUpdate); err != nil {
			return fmt.Errorf("failed to ensure control plane: %w", err)
		}

		if !r.IsCPOV2 {
			// Reconcile openshift oauth apiserver
			r.Log.Info("Reconciling OpenShift OAuth API Server")
			if err := r.reconcileOpenShiftOAuthAPIServer(ctx, hostedControlPlane, observedConfig, releaseImageProvider, createOrUpdate); err != nil {
				return fmt.Errorf("failed to reconcile openshift oauth apiserver: %w", err)
			}

			// Reconcile oauth server
			r.Log.Info("Reconciling OAuth Server")
			if err := r.reconcileOAuthServer(ctx, hostedControlPlane, releaseImageProvider, infraStatus.OAuthHost, infraStatus.OAuthPort, createOrUpdate); err != nil {
				return fmt.Errorf("failed to reconcile openshift oauth apiserver: %w", err)
			}

		}

		// TODO: move this up with the rest of conditions reconciliation logic?
		if err := r.reconcileValidIDPConfigurationCondition(ctx, hostedControlPlane, releaseImageProvider, infraStatus.OAuthHost, infraStatus.OAuthPort); err != nil {
			return fmt.Errorf("failed to reconcile ValidIDPConfiguration condition: %w", err)
		}
	}

	if !r.IsCPOV2 {
		// Reconcile openshift controller manager
		r.Log.Info("Reconciling OpenShift Controller Manager")
		if err := r.reconcileOpenShiftControllerManager(ctx, hostedControlPlane, observedConfig, releaseImageProvider, createOrUpdate, featureGates); err != nil {
			return fmt.Errorf("failed to reconcile openshift controller manager: %w", err)
		}

		// Reconcile openshift route controller manager
		r.Log.Info("Reconciling OpenShift Route Controller Manager")
		if err := r.reconcileOpenShiftRouteControllerManager(ctx, hostedControlPlane, releaseImageProvider, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile openshift route controller manager: %w", err)
		}

		// Reconcile cluster policy controller
		r.Log.Info("Reconciling Cluster Policy Controller")
		if err := r.reconcileClusterPolicyController(ctx, hostedControlPlane, releaseImageProvider, createOrUpdate, featureGates); err != nil {
			return fmt.Errorf("failed to reconcile cluster policy controller: %w", err)
		}

		// Reconcile cluster version operator
		r.Log.Info("Reconciling Cluster Version Operator")
		if err := r.reconcileClusterVersionOperator(ctx, hostedControlPlane, releaseImageProvider, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile cluster version operator: %w", err)
		}

		r.Log.Info("Reconciling ClusterNetworkOperator")
		if err := r.reconcileClusterNetworkOperator(ctx, hostedControlPlane, releaseImageProvider, userReleaseImageProvider, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile cluster network operator: %w", err)
		}

		r.Log.Info("Reconciling Cluster Node Tuning Operator")
		if err := r.reconcileClusterNodeTuningOperator(ctx, hostedControlPlane, releaseImageProvider, userReleaseImageProvider, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile cluster node tuning operator: %w", err)
		}
	}

	if err := r.cleanupClusterNetworkOperatorResources(ctx, hostedControlPlane, r.ManagementClusterCapabilities.Has(capabilities.CapabilityRoute)); err != nil {
		return fmt.Errorf("failed to reconcile cluster network operator operands: %w", err)
	}

	if !r.IsCPOV2 {
		r.Log.Info("Reconciling DNSOperator")
		if err := r.reconcileDNSOperator(ctx, hostedControlPlane, releaseImageProvider, userReleaseImageProvider, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile DNS operator: %w", err)
		}
	}

	if !r.IsCPOV2 {
		r.Log.Info("Reconciling IngressOperator")
		if err := r.reconcileIngressOperator(ctx, hostedControlPlane, releaseImageProvider, userReleaseImageProvider, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile ingress operator: %w", err)
		}

		// Reconcile hosted cluster config operator
		r.Log.Info("Reconciling Hosted Cluster Config Operator")
		if err := r.reconcileHostedClusterConfigOperator(ctx, hostedControlPlane, userReleaseImageProvider, infraStatus, createOrUpdate, openShiftTrustedCABundleConfigMapForCPOExists); err != nil {
			return fmt.Errorf("failed to reconcile hosted cluster config operator: %w", err)
		}

		// Reconcile cloud controller manager
		r.Log.Info("Reconciling Cloud Controller Manager")
		if err := r.reconcileCloudControllerManager(ctx, hostedControlPlane, releaseImageProvider, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile cloud controller manager: %w", err)
		}

		if hostedControlPlane.Spec.Platform.Type == hyperv1.AWSPlatform {
			// Reconcile cloud credential operator
			r.Log.Info("Reconciling Cloud Credential Operator")
			if err := r.reconcileCloudCredentialOperator(ctx, hostedControlPlane, releaseImageProvider, createOrUpdate); err != nil {
				return fmt.Errorf("failed to reconcile cloud controller manager: %w", err)
			}
		}

		// Reconcile OLM
		r.Log.Info("Reconciling OLM")
		if err := r.reconcileOperatorLifecycleManager(ctx, hostedControlPlane, releaseImageProvider, userReleaseImageProvider, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile olm: %w", err)
		}
	}

	if !r.IsCPOV2 {
		// Reconcile image registry operator
		if capabilities.IsImageRegistryCapabilityEnabled(hostedControlPlane.Spec.Capabilities) {
			r.Log.Info("Reconciling Image Registry Operator")
			if err := r.reconcileImageRegistryOperator(ctx, hostedControlPlane, releaseImageProvider, userReleaseImageProvider, createOrUpdate); err != nil {
				return fmt.Errorf("failed to reconcile image registry operator: %w", err)
			}
		}
		if IsStorageAndCSIManaged(hostedControlPlane) {
			// Reconcile cluster storage operator
			r.Log.Info("Reconciling cluster storage operator")
			if err := r.reconcileClusterStorageOperator(ctx, hostedControlPlane, releaseImageProvider, userReleaseImageProvider, createOrUpdate); err != nil {
				return fmt.Errorf("failed to reconcile cluster storage operator: %w", err)
			}

			// Reconcile cloud csi driver
			r.Log.Info("Reconciling CSI Driver")
			if err := r.reconcileCSIDriver(ctx, hostedControlPlane, releaseImageProvider, createOrUpdate); err != nil {
				return fmt.Errorf("failed to reconcile csi driver: %w", err)
			}

			// Reconcile CSI snapshot controller operator
			r.Log.Info("Reconciling CSI snapshot controller operator")
			if err := r.reconcileCSISnapshotControllerOperator(ctx, hostedControlPlane, releaseImageProvider, userReleaseImageProvider, createOrUpdate); err != nil {
				return fmt.Errorf("failed to reconcile CSI snapshot controller operator: %w", err)
			}
		}
	}

	// Reconcile Ignition-server configs
	if _, exists := hostedControlPlane.Annotations[hyperv1.DisableIgnitionServerAnnotation]; !exists {
		r.Log.Info("Reconciling ignition-server configs")
		if err := r.reconcileIgnitionServerConfigs(ctx, hostedControlPlane, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile ignition-server configs: %w", err)
		}
	}

	// Disable machine management components if enabled
	if _, exists := hostedControlPlane.Annotations[hyperv1.DisableMachineManagement]; !exists {
		if !r.IsCPOV2 {
			r.Log.Info("Reconciling autoscaler")
			if err := r.reconcileAutoscaler(ctx, hostedControlPlane, releaseImageProvider, createOrUpdate); err != nil {
				return fmt.Errorf("failed to reconcile autoscaler: %w", err)
			}

			r.Log.Info("Reconciling machine approver")
			if err := r.reconcileMachineApprover(ctx, hostedControlPlane, releaseImageProvider, createOrUpdate); err != nil {
				return fmt.Errorf("failed to reconcile machine approver: %w", err)
			}
		}
	}

	r.Log.Info("Reconciling default security group")
	if err := r.reconcileDefaultSecurityGroup(ctx, hostedControlPlane); err != nil {
		return fmt.Errorf("failed to reconcile default security group: %w", err)
	}

	// TODO: consider using a side container in the etcd pod instead.
	r.Log.Info("Reconciling etcd-backup cronJob")
	if hostedControlPlane.Spec.Platform.Type == hyperv1.AWSPlatform {
		configMapName := "etcd-backup-config" // TODO: get configMap name from annotation?
		configMap := &corev1.ConfigMap{}
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hostedControlPlane.Namespace, Name: configMapName}, configMap); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to get etcd backup configMap: %w", err)
			}

			serviceAccount := manifests.EtcdBackupServiceAccount(hostedControlPlane.Namespace)
			if _, err := util.DeleteIfNeeded(ctx, r.Client, serviceAccount); err != nil {
				return fmt.Errorf("failed to delete etcd backup service account: %w", err)
			}
			cronJob := manifests.EtcdBackupCronJob(hostedControlPlane.Namespace)
			if _, err := util.DeleteIfNeeded(ctx, r.Client, cronJob); err != nil {
				return fmt.Errorf("failed to delete etcd backup cronJob: %w", err)
			}
		} else {
			serviceAccount := manifests.EtcdBackupServiceAccount(hostedControlPlane.Namespace)
			if _, err = createOrUpdate(ctx, r.Client, serviceAccount, func() error {
				util.EnsurePullSecret(serviceAccount, common.PullSecret("").Name)
				return nil
			}); err != nil {
				return fmt.Errorf("failed to reconcile etcd-backup cronJob service account: %w", err)
			}

			cronJob := manifests.EtcdBackupCronJob(hostedControlPlane.Namespace)
			if _, err = createOrUpdate(ctx, r.Client, cronJob, func() error {
				return r.reconcileEtcdBackupCronJob(cronJob,
					configMap,
					serviceAccount,
					hostedControlPlane,
					releaseImageProvider.GetImage(util.CPOImageName),
					releaseImageProvider.GetImage("etcd"))
			}); err != nil {
				return fmt.Errorf("failed to reconcile etcd-backup cronJob: %w", err)
			}
		}
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileEtcdBackupCronJob(cronJob *batchv1.CronJob, configMap *corev1.ConfigMap, serviceAccount *corev1.ServiceAccount, hcp *hyperv1.HostedControlPlane, cpoImage, etcdImage string) error {
	clusterID, ok := hcp.Labels["api.openshift.com/id"]
	if !ok {
		clusterID = hcp.Spec.ClusterID
	}
	orgID, ok := hcp.Labels["api.openshift.com/legal-entity-id"]
	if !ok {
		orgID = "openshift" // TODO: non OCM environment
	}

	cronJob.Spec = batchv1.CronJobSpec{
		Schedule: "0 */1 * * *", // TODO: make it configurable, read from annotation?
		JobTemplate: batchv1.JobTemplateSpec{
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						InitContainers: []corev1.Container{
							{
								Name:                     "copy-cpo-binary",
								Image:                    cpoImage,
								ImagePullPolicy:          corev1.PullIfNotPresent,
								TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
								Command: []string{
									"/bin/bash",
									"-c",
									`#!/bin/sh

									while ! nslookup etcd-client; do sleep 10; done
									cp /usr/bin/control-plane-operator /etc/backup/
									`,
								},

								VolumeMounts: []corev1.VolumeMount{
									{
										Name:      "backup-volume",
										MountPath: "/etc/backup",
									},
								},
							},
						},
						Containers: []corev1.Container{
							{
								Name:                     "etcd-backup",
								Image:                    etcdImage,
								ImagePullPolicy:          corev1.PullIfNotPresent,
								TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
								Command: []string{
									"/etc/backup/control-plane-operator",
								},
								Args: []string{
									"etcd-backup",
									"--backup-dir",
									"/etc/backup",
									"--s3-bucket-name",
									"$(BUCKET_NAME)",
									"--s3-key-prefix",
									fmt.Sprintf("hourly/%s", clusterID),
									"--s3-object-tags",
									fmt.Sprintf("cluster_id=%s,org_id=%s", clusterID, orgID),
									"--etcd-endpoint",
									"etcd-client:2379",
									"--etcd-client-cert",
									"/etc/etcd/tls/client/etcd-client.crt",
									"--etcd-client-key",
									"/etc/etcd/tls/client/etcd-client.key",
									"--etcd-ca-cert",
									"/etc/etcd/tls/etcd-ca/ca.crt",
								},
								Env: []corev1.EnvVar{
									{
										Name: "BUCKET_NAME",
										ValueFrom: &corev1.EnvVarSource{
											ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
												LocalObjectReference: corev1.LocalObjectReference{
													Name: configMap.Name,
												},
												Key: "bucket-name",
											},
										},
									},
									{
										Name: "AWS_REGION",
										ValueFrom: &corev1.EnvVarSource{
											ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
												LocalObjectReference: corev1.LocalObjectReference{
													Name: configMap.Name,
												},
												Key: "region",
											},
										},
									},
									{
										Name: "AWS_ROLE_ARN",
										ValueFrom: &corev1.EnvVarSource{
											ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
												LocalObjectReference: corev1.LocalObjectReference{
													Name: configMap.Name,
												},
												Key: "role-arn",
											},
										},
									},
									{
										Name: "AWS_ENDPOINT_URL_S3",
										ValueFrom: &corev1.EnvVarSource{
											ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
												LocalObjectReference: corev1.LocalObjectReference{
													Name: configMap.Name,
												},
												Key:      "s3-endpoint-url",
												Optional: ptr.To(true),
											},
										},
									},
									{
										Name:  "AWS_WEB_IDENTITY_TOKEN_FILE",
										Value: "/var/run/secrets/openshift/serviceaccount/token",
									},
								},
								VolumeMounts: []corev1.VolumeMount{
									{
										MountPath: "/etc/backup",
										Name:      "backup-volume",
									},
									{
										MountPath: "/etc/etcd/tls/client",
										Name:      "client-tls",
									},
									{
										MountPath: "/etc/etcd/tls/etcd-ca",
										Name:      "etcd-ca",
									},
									{
										MountPath: "/var/run/secrets/openshift/serviceaccount",
										Name:      "cloud-token",
										ReadOnly:  true,
									},
								},
							},
						},
						RestartPolicy:      corev1.RestartPolicyNever,
						ServiceAccountName: serviceAccount.Name,
						Volumes: []corev1.Volume{
							{
								Name: "backup-volume",
								VolumeSource: corev1.VolumeSource{
									EmptyDir: &corev1.EmptyDirVolumeSource{},
								},
							},
							{
								Name: "client-tls",
								VolumeSource: corev1.VolumeSource{
									Secret: &corev1.SecretVolumeSource{
										SecretName:  manifests.EtcdClientSecret("").Name,
										DefaultMode: ptr.To[int32](420),
									},
								},
							},
							{
								Name: "etcd-ca",
								VolumeSource: corev1.VolumeSource{
									ConfigMap: &corev1.ConfigMapVolumeSource{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: manifests.EtcdSignerCAConfigMap("").Name,
										},
										DefaultMode: ptr.To[int32](420),
									},
								},
							},
							{
								Name: "cloud-token",
								VolumeSource: corev1.VolumeSource{
									Projected: &corev1.ProjectedVolumeSource{
										Sources: []corev1.VolumeProjection{
											{
												ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
													Audience:          "openshift",
													ExpirationSeconds: ptr.To[int64](86400),
													Path:              "token",
												},
											},
										},
										DefaultMode: ptr.To[int32](420),
									},
								},
							},
						},
					},
				},
			},
		},
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
			return kas.ReconcileKonnectivityExternalRoute(konnectivityRoute, p.OwnerRef, hostname, r.DefaultIngressDomain, labelHCPRoutes(hcp))
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
			return oauth.ReconcileExternalPublicRoute(oauthExternalPublicRoute, p.OwnerRef, hostname, r.DefaultIngressDomain, labelHCPRoutes(hcp))
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
				return oauth.ReconcileExternalPrivateRoute(oauthExternalPrivateRoute, p.OwnerRef, serviceStrategy.Route.Hostname, r.DefaultIngressDomain, labelHCPRoutes(hcp))
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
	if sharedingress.UseSharedIngress() || hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		return nil
	}
	exposeKASThroughRouter := util.IsRouteKAS(hcp)
	// Create the Service type LB internal for private endpoints.
	pubSvc := manifests.RouterPublicService(hcp.Namespace)
	if util.IsPrivateHCP(hcp) {
		svc := manifests.PrivateRouterService(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r.Client, svc, func() error {
			return ingress.ReconcileRouterService(svc, true, true, hcp)
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
			return ingress.ReconcileRouterService(pubSvc, false, util.IsPrivateHCP(hcp), hcp)
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

func (r *HostedControlPlaneReconciler) defaultReconcileInfrastructureStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (infra.InfrastructureStatus, error) {
	var (
		infraStatus infra.InfrastructureStatus
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
	if !util.IsPrivateHCP(hcp) || hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		return
	}
	return r.reconcileRouterServiceStatus(ctx, manifests.PrivateRouterService(hcp.Namespace), events.NewMessageCollector(ctx, r.Client))
}

func (r *HostedControlPlaneReconciler) reconcileExternalRouterServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (host string, needed bool, message string, err error) {
	if !util.IsPublicHCP(hcp) || !util.IsRouteKAS(hcp) || sharedingress.UseSharedIngress() || hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
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

	if err = r.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		if apierrors.IsNotFound(err) {
			err = nil
			return
		}
		err = fmt.Errorf("failed to get kube apiserver service: %w", err)
		return
	}

	return kas.ReconcileServiceStatus(svc, serviceStrategy, kasSVCLBPort, events.NewMessageCollector(ctx, r.Client))
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
	return kas.ReconcileKonnectivityServerServiceStatus(svc, route, serviceStrategy, events.NewMessageCollector(ctx, r.Client))
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
		} else if serviceStrategy.Route != nil && serviceStrategy.Route.Hostname != "" {
			route = manifests.OauthServerExternalPrivateRoute(hcp.Namespace)
			if err = r.Get(ctx, client.ObjectKeyFromObject(route), route); err != nil {
				if apierrors.IsNotFound(err) {
					err = nil
					return
				}
				err = fmt.Errorf("failed to get oauth internal route: %w", err)
				return
			}
		} else {
			route = manifests.OauthServerInternalRoute(hcp.Namespace)
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
	kubeadminPasswordSecret := common.KubeadminPasswordSecret(hcp.Namespace)
	if explicitOauthConfig {
		// delete kubeadminPasswordSecret if it exist
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(kubeadminPasswordSecret), kubeadminPasswordSecret); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}

		return r.Client.Delete(ctx, kubeadminPasswordSecret)
	}

	var kubeadminPassword string
	if _, err := createOrUpdate(ctx, r, kubeadminPasswordSecret, func() error {
		return reconcileKubeadminPasswordSecret(kubeadminPasswordSecret, hcp, &kubeadminPassword)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kubeadminPasswordSecret: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcilePKI(ctx context.Context, hcp *hyperv1.HostedControlPlane, infraStatus infra.InfrastructureStatus, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := pki.NewPKIParams(hcp, infraStatus.APIHost, infraStatus.OAuthHost, infraStatus.KonnectivityHost)

	// Root CA
	rootCASecret := manifests.RootCASecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, rootCASecret, func() error {
		return pki.ReconcileRootCA(rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile root CA: %w", err)
	}

	observedDefaultIngressCert := manifests.IngressObservedDefaultIngressCertCA(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(observedDefaultIngressCert), observedDefaultIngressCert); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get observed default ingress cert: %w", err)
		}
		observedDefaultIngressCert = nil
	}

	rootCAConfigMap := manifests.RootCAConfigMap(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, rootCAConfigMap, func() error {
		return pki.ReconcileRootCAConfigMap(rootCAConfigMap, p.OwnerRef, rootCASecret, observedDefaultIngressCert)
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
		return pki.ReconcileKASServerCertSecret(kasServerSecret, rootCASecret, p.OwnerRef, p.ExternalAPIAddress, p.InternalAPIAddress, p.ServiceCIDR, p.NodeInternalAPIServerIP)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kas server secret: %w", err)
	}

	// KAS server private secret
	kasServerPrivateSecret := manifests.KASServerPrivateCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kasServerPrivateSecret, func() error {
		return pki.ReconcileKASServerPrivateCertSecret(kasServerPrivateSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kas server private secret: %w", err)
	}

	totalKASClientCABundle := pkimanifests.TotalKASClientCABundle(hcp.Namespace)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(totalKASClientCABundle), totalKASClientCABundle); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to fetch KAS total client CA bundle: %w", err)
	}

	if err := r.setupKASClientSigners(ctx, hcp, p, createOrUpdate, rootCASecret, totalKASClientCABundle); err != nil {
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

	if util.HCPOAuthEnabled(hcp) {
		// OpenShift OAuth APIServer
		openshiftOAuthAPIServerCertSecret := manifests.OpenShiftOAuthAPIServerCertSecret(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, openshiftOAuthAPIServerCertSecret, func() error {
			return pki.ReconcileOpenShiftOAuthAPIServerCertSecret(openshiftOAuthAPIServerCertSecret, rootCASecret, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile openshift oauth apiserver cert: %w", err)
		}
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

	var userCABundles []client.ObjectKey
	if hcp.Spec.AdditionalTrustBundle != nil {
		userCABundles = append(userCABundles, client.ObjectKey{Namespace: hcp.Namespace, Name: hcp.Spec.AdditionalTrustBundle.Name})
	}
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.Proxy != nil && hcp.Spec.Configuration.Proxy.TrustedCA.Name != "" {
		userCABundles = append(userCABundles, client.ObjectKey{Namespace: hcp.Namespace, Name: hcp.Spec.Configuration.Proxy.TrustedCA.Name})
	}

	trustedCABundle := manifests.TrustedCABundleConfigMap(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, trustedCABundle, func() error {
		return r.reconcileManagedTrustedCABundle(ctx, trustedCABundle, userCABundles)
	}); err != nil {
		return fmt.Errorf("failed to reconcile managed UserCA configMap: %w", err)
	}

	if util.HCPOAuthEnabled(hcp) {
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

		if trustedCABundle.Data[certs.UserCABundleMapKey] != "" {
			bundleSources = append(bundleSources, &corev1.Secret{
				Data: map[string][]byte{
					certs.CASignerCertMapKey: []byte(trustedCABundle.Data[certs.UserCABundleMapKey]),
				},
			})
		}

		oauthMasterCABundle := manifests.OpenShiftOAuthMasterCABundle(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, oauthMasterCABundle, func() error {
			return pki.ReconcileOAuthMasterCABundle(oauthMasterCABundle, p.OwnerRef, bundleSources)
		}); err != nil {
			return fmt.Errorf("failed to reconcile oauth cert secret: %w", err)
		}
	}

	// MCS Cert
	if _, exists := hcp.Annotations[hyperv1.DisableIgnitionServerAnnotation]; !exists {
		machineConfigServerCert := manifests.MachineConfigServerCert(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, machineConfigServerCert, func() error {
			return pki.ReconcileMachineConfigServerCert(machineConfigServerCert, rootCASecret, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile machine config server cert secret: %w", err)
		}
	}

	// Cluster Node Tuning Operator metrics Serving Cert
	NodeTuningOperatorServingCert := manifests.ClusterNodeTuningOperatorServingCertSecret(hcp.Namespace)
	NodeTuningOperatorService := manifests.ClusterNodeTuningOperatorMetricsService(hcp.Namespace)
	err := removeServiceCAAnnotationAndSecret(ctx, r.Client, NodeTuningOperatorService, NodeTuningOperatorServingCert)
	if err != nil {
		r.Log.Error(err, "failed to remove service ca annotation and secret: %w")
	}
	if _, err = createOrUpdate(ctx, r, NodeTuningOperatorServingCert, func() error {
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

	if capabilities.IsImageRegistryCapabilityEnabled(hcp.Spec.Capabilities) {
		// Image Registry Operator Serving Cert
		imageRegistryOperatorServingCert := manifests.ImageRegistryOperatorServingCert(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, imageRegistryOperatorServingCert, func() error {
			return pki.ReconcileRegistryOperatorServingCert(imageRegistryOperatorServingCert, rootCASecret, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile image registry operator serving cert: %w", err)
		}
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

	// For the Multus Admission Controller, Network Node Identity, and OVN Control Plane Metrics Serving Certs:
	//   We want to remove the secret if there was an existing one created by service-ca; otherwise, it will cause
	//   issues in cases where you are upgrading an older CPO prior to us adding the feature to reconcile the serving
	//   cert secret ourselves.

	// Multus Admission Controller Serving Cert
	multusAdmissionControllerService := manifests.MultusAdmissionControllerService(hcp.Namespace)
	if err = r.Get(ctx, client.ObjectKeyFromObject(multusAdmissionControllerService), multusAdmissionControllerService); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to retrieve multus-admission-controller service: %w", err)
		}
	}

	// If the service doesn't have the service ca annotation, delete any previous secret with the annotation and
	// reconcile the secret with our own rootCA; otherwise, skip reconciling the secret with our own rootCA.
	if hasServiceCAAnnotation := doesServiceHaveServiceCAAnnotation(multusAdmissionControllerService); !hasServiceCAAnnotation {
		multusAdmissionControllerServingCertSecret := manifests.MultusAdmissionControllerServingCert(hcp.Namespace)

		err = removeServiceCASecret(ctx, r.Client, multusAdmissionControllerServingCertSecret)
		if err != nil {
			return err
		}

		if _, err = createOrUpdate(ctx, r, multusAdmissionControllerServingCertSecret, func() error {
			return pki.ReconcileMultusAdmissionControllerServingCertSecret(multusAdmissionControllerServingCertSecret, rootCASecret, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile multus admission controller serving cert: %w", err)
		}
	}

	// Network Node Identity Serving Cert
	networkNodeIdentityService := manifests.NetworkNodeIdentityService(hcp.Namespace)
	if err = r.Get(ctx, client.ObjectKeyFromObject(networkNodeIdentityService), networkNodeIdentityService); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to retrieve network-node-identity service: %w", err)
		}
	}

	// If the service doesn't have the service ca annotation, delete any previous secret with the annotation and
	// reconcile the secret with our own rootCA; otherwise, skip reconciling the secret with our own rootCA.
	if hasServiceCAAnnotation := doesServiceHaveServiceCAAnnotation(networkNodeIdentityService); !hasServiceCAAnnotation {
		networkNodeIdentityServingCertSecret := manifests.NetworkNodeIdentityControllerServingCert(hcp.Namespace)

		err = removeServiceCASecret(ctx, r.Client, networkNodeIdentityServingCertSecret)
		if err != nil {
			return err
		}

		if _, err = createOrUpdate(ctx, r, networkNodeIdentityServingCertSecret, func() error {
			return pki.ReconcileNetworkNodeIdentityServingCertSecret(networkNodeIdentityServingCertSecret, rootCASecret, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile network node identity serving cert: %w", err)
		}
	}

	// OVN Control Plane Metrics Serving Cert
	ovnControlPlaneService := manifests.OVNKubernetesControlPlaneService(hcp.Namespace)
	if err = r.Get(ctx, client.ObjectKeyFromObject(ovnControlPlaneService), ovnControlPlaneService); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to retrieve ovn-kubernetes-control-plane service: %w", err)
		}
	}

	// If the service doesn't have the service ca annotation, delete any previous secret with the annotation and
	// reconcile the secret with our own rootCA; otherwise, skip reconciling the secret with our own rootCA.
	if hasServiceCAAnnotation := doesServiceHaveServiceCAAnnotation(ovnControlPlaneService); !hasServiceCAAnnotation {
		ovnControlPlaneMetricsServingCertSecret := manifests.OVNControlPlaneMetricsServingCert(hcp.Namespace)

		err = removeServiceCASecret(ctx, r.Client, ovnControlPlaneMetricsServingCertSecret)
		if err != nil {
			return err
		}

		if _, err = createOrUpdate(ctx, r, ovnControlPlaneMetricsServingCertSecret, func() error {
			return pki.ReconcileOVNControlPlaneMetricsServingCertSecret(ovnControlPlaneMetricsServingCertSecret, rootCASecret, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile OVN control plane serving cert: %w", err)
		}
	}

	if _, exists := hcp.Annotations[hyperv1.DisableIgnitionServerAnnotation]; !exists {
		if hcp.Spec.Platform.Type != hyperv1.IBMCloudPlatform {
			ignitionServerCert := manifests.IgnitionServerCertSecret(hcp.Namespace)
			if _, err := createOrUpdate(ctx, r, ignitionServerCert, func() error {
				return pki.ReconcileIgnitionServerCertSecret(ignitionServerCert, rootCASecret, p.OwnerRef)
			}); err != nil {
				return fmt.Errorf("failed to reconcile ignition server cert: %w", err)
			}
		}
	}

	// Platform specific certs
	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		awsPodIdentityWebhookServingCert := manifests.AWSPodIdentityWebhookServingCert(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, awsPodIdentityWebhookServingCert, func() error {
			return pki.ReconcileAWSPodIdentityWebhookServingCert(awsPodIdentityWebhookServingCert, rootCASecret, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile %s secret: %w", awsPodIdentityWebhookServingCert.Name, err)
		}
	case hyperv1.AzurePlatform:
		azureDiskCsiDriverControllerMetricsService := manifests.AzureDiskCsiDriverControllerMetricsService(hcp.Namespace)
		if err = r.Get(ctx, client.ObjectKeyFromObject(azureDiskCsiDriverControllerMetricsService), azureDiskCsiDriverControllerMetricsService); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to retrieve azure-disk-csi-driver-controller-metrics service: %w", err)
			}
		}

		if hasServiceCAAnnotation := doesServiceHaveServiceCAAnnotation(azureDiskCsiDriverControllerMetricsService); !hasServiceCAAnnotation {
			azureDiskCsiDriverControllerMetricsServingCert := manifests.AzureDiskCsiDriverControllerMetricsServingCert(hcp.Namespace)

			err = removeServiceCASecret(ctx, r.Client, azureDiskCsiDriverControllerMetricsServingCert)
			if err != nil {
				return err
			}

			if _, err = createOrUpdate(ctx, r, azureDiskCsiDriverControllerMetricsServingCert, func() error {
				return pki.ReconcileAzureDiskCsiDriverControllerMetricsServingCertSecret(azureDiskCsiDriverControllerMetricsServingCert, rootCASecret, p.OwnerRef)
			}); err != nil {
				return fmt.Errorf("failed to reconcile azure disk csi driver controller metrics serving cert: %w", err)
			}
		}

		azureFileCsiDriverControllerMetricsService := manifests.AzureFileCsiDriverControllerMetricsService(hcp.Namespace)
		if err = r.Get(ctx, client.ObjectKeyFromObject(azureFileCsiDriverControllerMetricsService), azureFileCsiDriverControllerMetricsService); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to retrieve azure-file-csi-driver-controller-metrics service: %w", err)
			}
		}

		if hasServiceCAAnnotation := doesServiceHaveServiceCAAnnotation(azureFileCsiDriverControllerMetricsService); !hasServiceCAAnnotation {
			azureFileCsiDriverControllerMetricsServingCert := manifests.AzureFileCsiDriverControllerMetricsServingCert(hcp.Namespace)

			err = removeServiceCASecret(ctx, r.Client, azureFileCsiDriverControllerMetricsServingCert)
			if err != nil {
				return err
			}

			if _, err := createOrUpdate(ctx, r, azureFileCsiDriverControllerMetricsServingCert, func() error {
				return pki.ReconcileAzureFileCsiDriverControllerMetricsServingCertSecret(azureFileCsiDriverControllerMetricsServingCert, rootCASecret, p.OwnerRef)
			}); err != nil {
				return fmt.Errorf("failed to reconcile azure file csi driver controller metrics serving cert: %w", err)
			}
		}
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileCSIDriver(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN) error {
	switch hcp.Spec.Platform.Type {
	// Most csi drivers should be laid down by the Cluster Storage Operator (CSO) instead of
	// the hcp operator. Only KubeVirt is unique at the moment.
	case hyperv1.KubevirtPlatform:
		err := kubevirtcsi.ReconcileInfra(r.Client, hcp, ctx, createOrUpdate, releaseImageProvider)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileFeatureGateGenerationJob(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN, releaseImageProvider, userReleaseImageProvider imageprovider.ReleaseImageProvider) (bool, error) {
	existing := manifests.FeatureGateGenerationJob(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(existing), existing); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("failed to fetch feature gate generation job: %w", err)
		}
	}

	updated := manifests.FeatureGateGenerationJob(hcp.Namespace)
	configAPIImage := releaseImageProvider.GetImage("cluster-config-api")
	cpoImage := releaseImageProvider.GetImage(util.CPOImageName)
	releaseVersion := userReleaseImageProvider.Version()
	if err := fg.ReconcileFeatureGateGenerationJob(ctx, updated, hcp, releaseVersion, configAPIImage, cpoImage, r.SetDefaultSecurityContext); err != nil {
		return false, fmt.Errorf("failed to reconcile feature gate generation job: %w", err)
	}

	if existing.ResourceVersion == "" {
		// A job doesn't exist, create it
		_, err := createOrUpdate(ctx, r.Client, updated, func() error { return nil })
		if err != nil {
			return false, fmt.Errorf("failed to create feature gate generation job: %w", err)
		}
		return false, nil
	}

	// There is an existing job, check its completion status and whether it matches the spec we want
	failedCond := util.FindJobCondition(existing, batchv1.JobFailed)
	completedCond := util.FindJobCondition(existing, batchv1.JobComplete)
	switch {
	case failedCond != nil && failedCond.Status == corev1.ConditionTrue:
		// If the job has failed, delete it so it can be recreated
		_, err := util.DeleteIfNeededWithOptions(ctx, r.Client, existing, client.PropagationPolicy(metav1.DeletePropagationForeground))
		return false, err
	case completedCond == nil || completedCond.Status == corev1.ConditionFalse:
		// Job not done yet
		return false, nil
	case fg.NeedsUpdate(existing, updated):
		// If the job needs to be updated, delete the job so it can be recreated
		_, err := util.DeleteIfNeededWithOptions(ctx, r.Client, existing, client.PropagationPolicy(metav1.DeletePropagationForeground))
		return false, err
	}

	isValidFeatureGateConfigMap := func() (bool, error) {
		cm, err := config.FeatureGateConfigMap(ctx, r.Client, hcp.Namespace)
		switch {
		case apierrors.IsNotFound(err):
			return false, nil
		case err != nil:
			return false, err
		default:
			_, err = config.ParseFeatureGates(cm)
			if err != nil {
				r.Log.Error(err, "Failed to parse feature gates")
				// If an error occurs parsing the feature gates, regardless of the error
				// we want to delete the job so the configmap can be re-generated.
				return false, nil
			}
			return true, nil
		}
	}

	// Job is completed, ensure that a ConfigMap exists and has valid data.
	valid, err := isValidFeatureGateConfigMap()
	if err != nil {
		return false, err
	}
	if !valid {
		// If the job is done, but the configmap is not valid, delete the job so it can be recreated
		_, err := util.DeleteIfNeededWithOptions(ctx, r.Client, existing, client.PropagationPolicy(metav1.DeletePropagationForeground))
		return false, err
	}
	return true, nil
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
		// We need different configs for KAS/KCM and Kubelet in Nodes
		cfg := manifests.AzureProviderConfig(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, cfg, func() error {
			return azure.ReconcileCloudConfig(cfg, hcp)
		}); err != nil {
			return fmt.Errorf("failed to reconcile Azure cloud config: %w", err)
		}

		// Reconcile the Cloud Provider configuration secret
		withSecrets := manifests.AzureProviderConfigWithCredentials(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, withSecrets, func() error {
			return azure.ReconcileCloudConfigWithCredentials(withSecrets, hcp)
		}); err != nil {
			return fmt.Errorf("failed to reconcile Azure cloud config with credentials: %w", err)
		}
	case hyperv1.OpenStackPlatform:
		credentialsSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: hcp.Spec.Platform.OpenStack.IdentityRef.Name}}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(credentialsSecret), credentialsSecret); err != nil {
			return fmt.Errorf("failed to get OpenStack credentials secret: %w", err)
		}
		caCertData := openstack.GetCACertFromCredentialsSecret(credentialsSecret)

		// Reconcile the Cloud Provider configuration config map
		cfg := manifests.OpenStackProviderConfig(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, cfg, func() error {
			return openstack.ReconcileCloudConfigConfigMap(hcp.Spec.Platform.OpenStack, cfg, credentialsSecret, caCertData, hcp.Spec.Networking.MachineNetwork)
		}); err != nil {
			return fmt.Errorf("failed to reconcile OpenStack cloud config: %w", err)
		}

		// This is for CCM to use the CA cert for OpenStack.
		if caCertData != nil {
			ca := manifests.OpenStackTrustedCA(hcp.Namespace)
			if _, err := createOrUpdate(ctx, r, ca, func() error {
				return openstack.ReconcileTrustedCA(ca, hcp, caCertData)
			}); err != nil {
				return fmt.Errorf("failed to reconcile OpenStack cloud CA: %w", err)
			}
		}
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileManagedEtcd(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN, statefulSet *appsv1.StatefulSet) error {
	p, err := etcd.NewEtcdParams(hcp, releaseImageProvider, r.SetDefaultSecurityContext)
	if err != nil {
		return fmt.Errorf("error creating etcd params")
	}

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

	// reconcile etcd-defrag-operator serviceAccount, role and roleBinding
	if p.DeploymentConfig.Replicas > 1 {
		sa := manifests.EtcdDefragControllerServiceAccount(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, sa, func() error {
			p.OwnerRef.ApplyTo(sa)
			util.EnsurePullSecret(sa, common.PullSecret(hcp.Namespace).Name)
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile etcd-defrag-operator service account: %w", err)
		}

		role := manifests.EtcdDefragControllerRole(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, role, func() error {
			return etcd.ReconcileDefragControllerRole(role, p)
		}); err != nil {
			return fmt.Errorf("failed to reconcile etcd-defrag-operator role: %w", err)
		}

		roleBinding := manifests.EtcdDefragControllerRoleBinding(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, roleBinding, func() error {
			return etcd.ReconcileDefragControllerRoleBinding(roleBinding, p)
		}); err != nil {
			return fmt.Errorf("failed to reconcile etcd-defrag-operator role binding: %w", err)
		}
	}

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

func (r *HostedControlPlaneReconciler) reconcileKonnectivity(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, infraStatus infra.InfrastructureStatus, createOrUpdate upsert.CreateOrUpdateFN) error {
	r.Log.Info("Reconciling Konnectivity")
	if err := r.cleanupOldKonnectivityServerDeployment(ctx, hcp); err != nil {
		return err
	}

	p := konnectivity.NewKonnectivityParams(hcp, releaseImageProvider, infraStatus.KonnectivityHost, infraStatus.KonnectivityPort, r.SetDefaultSecurityContext)
	serverLocalService := manifests.KonnectivityServerLocalService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, serverLocalService, func() error {
		return kas.ReconcileKonnectivityServerLocalService(serverLocalService, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity server local service: %w", err)
	}
	agentDeployment := manifests.KonnectivityAgentDeployment(hcp.Namespace)
	ips := []string{
		infraStatus.OpenShiftAPIHost,
		infraStatus.PackageServerAPIAddress,
	}
	if util.HCPOAuthEnabled(hcp) {
		ips = append(ips, infraStatus.OauthAPIServerHost)
	}
	if _, err := createOrUpdate(ctx, r, agentDeployment, func() error {
		return konnectivity.ReconcileAgentDeployment(agentDeployment, p.OwnerRef, p.AgentDeploymentConfig, p.KonnectivityAgentImage, ips)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity agent deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) cleanupOldKonnectivityServerDeployment(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	serverDeployment := manifests.KonnectivityServerDeployment(hcp.Namespace)
	// Remove the konnectivity-server deployment if it exists
	if _, err := util.DeleteIfNeeded(ctx, r, serverDeployment); err != nil {
		return fmt.Errorf("failed to remove konnectivity-server deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileKubeAPIServer(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider, userReleaseImageProvider imageprovider.ReleaseImageProvider, apiAddress string, apiPort int32, oauthAddress string, oauthPort int32, createOrUpdate upsert.CreateOrUpdateFN, kubeAPIServerDeployment *appsv1.Deployment, featureGates []string) error {
	p := kas.NewKubeAPIServerParams(ctx, hcp, releaseImageProvider, apiAddress, apiPort, oauthAddress, oauthPort, r.SetDefaultSecurityContext, featureGates)

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
	hccoClientCertSecret := manifests.HCCOClientCertSecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(hccoClientCertSecret), hccoClientCertSecret); err != nil {
		return fmt.Errorf("failed to get HCCO client cert secret: %w", err)
	}

	serviceKubeconfigSecret := manifests.KASServiceKubeconfigSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, serviceKubeconfigSecret, func() error {
		return kas.ReconcileServiceKubeconfigSecret(serviceKubeconfigSecret, clientCertSecret, rootCA, p.OwnerRef, hcp.Spec.Platform.Type)
	}); err != nil {
		return fmt.Errorf("failed to reconcile service admin kubeconfig secret: %w", err)
	}

	// The client used by CAPI machine controller expects the kubeconfig to follow this naming and key convention
	// https://github.com/kubernetes-sigs/cluster-api/blob/5c85a0a01ee44ecf7c8a3c3fdc867a88af87d73c/util/secret/secret.go#L29-L33
	capiKubeconfigSecret := manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID)
	if _, err := createOrUpdate(ctx, r, capiKubeconfigSecret, func() error {
		// TODO(alberto): This secret is currently using the cluster-admin kubeconfig for the guest cluster.
		// We should create a separate kubeconfig with a tight set of permissions for it to use.
		return kas.ReconcileServiceCAPIKubeconfigSecret(capiKubeconfigSecret, clientCertSecret, rootCA, p.OwnerRef, hcp.Spec.InfraID, hcp.Spec.Platform.Type)
	}); err != nil {
		return fmt.Errorf("failed to reconcile CAPI service admin kubeconfig secret: %w", err)
	}

	hccoKubeconfigSecret := manifests.HCCOKubeconfigSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, hccoKubeconfigSecret, func() error {
		return kas.ReconcileHCCOKubeconfigSecret(hccoKubeconfigSecret, hccoClientCertSecret, rootCA, p.OwnerRef, hcp.Spec.Platform.Type)
	}); err != nil {
		return fmt.Errorf("failed to reconcile HCCO kubeconfig secret: %w", err)
	}

	localhostKubeconfigSecret := manifests.KASLocalhostKubeconfigSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, localhostKubeconfigSecret, func() error {
		return kas.ReconcileLocalhostKubeconfigSecret(localhostKubeconfigSecret, clientCertSecret, rootCA, p.OwnerRef, p.KASPodPort)
	}); err != nil {
		return fmt.Errorf("failed to reconcile localhost kubeconfig secret: %w", err)
	}

	// Generate the new KASCustomKubeconfig secret if the KubeAPIServerDNSName is set
	kasCustomKubeconfigSecret := manifests.KASCustomKubeconfigSecret(hcp.Namespace, nil)
	if len(hcp.Spec.KubeAPIServerDNSName) > 0 {
		newRootCA, err := includeServingCertificates(ctx, r.Client, hcp, rootCA)
		if err != nil {
			return fmt.Errorf("failed to include serving certificates: %w", err)
		}

		if _, err := createOrUpdate(ctx, r, kasCustomKubeconfigSecret, func() error {
			return kas.ReconcileKASCustomKubeconfigSecret(kasCustomKubeconfigSecret, clientCertSecret, newRootCA, p.OwnerRef, p.CustomExternalURL(), p.KASCustomKubeconfigKey())
		}); err != nil {
			return fmt.Errorf("failed to reconcile custom external kubeconfig secret: %w", err)
		}
	} else {
		// Cleanup the new kasCustomKubeconfigSecret secret if the KubeAPIServerDNSName is removed
		if _, err := util.DeleteIfNeeded(ctx, r.Client, kasCustomKubeconfigSecret); err != nil {
			return fmt.Errorf("failed to delete customKubeconfig status from HCP object: %w", err)
		}
	}

	// Renamed the old externalKubeconfigSecret to adminKubeconfigSecret
	adminKubeconfigSecret := manifests.KASAdminKubeconfigSecret(hcp.Namespace, hcp.Spec.KubeConfig)
	if _, err := createOrUpdate(ctx, r, adminKubeconfigSecret, func() error {
		if !util.IsPublicHCP(hcp) && !util.IsRouteKAS(hcp) {
			return kas.ReconcileKASCustomKubeconfigSecret(adminKubeconfigSecret, clientCertSecret, rootCA, p.OwnerRef, p.InternalURL(), p.ExternalKubeconfigKey())
		}
		return kas.ReconcileKASCustomKubeconfigSecret(adminKubeconfigSecret, clientCertSecret, rootCA, p.OwnerRef, p.ExternalURL(), p.ExternalKubeconfigKey())
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
		return kas.ReconcileConfig(kubeAPIServerConfig, p.OwnerRef, p.ConfigParams(), hcp.Spec.Capabilities)
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server config: %w", err)
	}

	kubeAPIServerAuthConfig := manifests.AuthConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kubeAPIServerAuthConfig, func() error {
		return kas.ReconcileAuthConfig(ctx, r, kubeAPIServerAuthConfig, p.OwnerRef, p.ConfigParams())
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server authentication config: %w", err)
	}

	kubeAPIServerEgressSelectorConfig := manifests.KASEgressSelectorConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kubeAPIServerEgressSelectorConfig, func() error {
		return kas.ReconcileEgressSelectorConfig(kubeAPIServerEgressSelectorConfig, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server egress selector config: %w", err)
	}

	userOauthMetadata := ""
	if hcp.Spec.Configuration != nil &&
		hcp.Spec.Configuration.Authentication != nil &&
		len(hcp.Spec.Configuration.Authentication.OAuthMetadata.Name) > 0 {
		var userOauthMetadataConfigMap corev1.ConfigMap
		err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcp.Namespace, Name: hcp.Spec.Configuration.Authentication.OAuthMetadata.Name}, &userOauthMetadataConfigMap)
		if err != nil {
			return fmt.Errorf("failed to get user oauth metadata configmap: %w", err)
		}
		if len(userOauthMetadataConfigMap.Data) == 0 {
			return fmt.Errorf("user oauth metadata configmap %s has no data", userOauthMetadataConfigMap.Name)
		}
		var ok bool
		if userOauthMetadata, ok = userOauthMetadataConfigMap.Data["oauthMetadata"]; !ok {
			return fmt.Errorf("user oauth metadata configmap %s has no oauthMetadata key", userOauthMetadataConfigMap.Name)
		}
	}
	oauthMetadata := manifests.KASOAuthMetadata(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, oauthMetadata, func() error {
		return kas.ReconcileOauthMetadata(oauthMetadata, p.OwnerRef, userOauthMetadata, p.ExternalOAuthAddress, p.ExternalOAuthPort)
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
		return kas.ReconcileServiceMonitor(serviceMonitor, config.OwnerRefFrom(hcp), hcp.Spec.ClusterID, r.MetricsSet)
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

	var oidcCA *corev1.LocalObjectReference
	if !util.HCPOAuthEnabled(hcp) &&
		len(hcp.Spec.Configuration.Authentication.OIDCProviders) != 0 &&
		hcp.Spec.Configuration.Authentication.OIDCProviders[0].Issuer.CertificateAuthority.Name != "" {
		// This is needed for version skew between HO and CPO.  Older versions of the HO wrote the CA to a fixed
		// oidc-ca configmap.  Newer versions just copy the configmap with its original name.
		name := hcp.Spec.Configuration.Authentication.OIDCProviders[0].Issuer.CertificateAuthority.Name
		err := r.Get(ctx, client.ObjectKey{Namespace: hcp.Namespace, Name: name}, &corev1.ConfigMap{})
		if err != nil {
			oidcCA = &corev1.LocalObjectReference{Name: manifests.OIDCCAConfigMap("").Name}
		} else {
			oidcCA = &corev1.LocalObjectReference{Name: name}
		}
	}

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
			kubeAPIServerAuditConfig,
			kubeAPIServerAuthConfig,
			p.AuditWebhookRef,
			aesCBCActiveKey,
			aesCBCBackupKey,
			p.KASPodPort,
			userReleaseImageProvider.Version(),
			p.FeatureGate,
			oidcCA,
			p.CipherSuites(),
		)
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server deployment: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileKubeControllerManager(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN, kcmDeployment *appsv1.Deployment, featureGates []string) error {
	p := kcm.NewKubeControllerManagerParams(ctx, hcp, releaseImageProvider, r.SetDefaultSecurityContext, featureGates)

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

	serviceServingCA := manifests.ServiceServingCA(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(serviceServingCA), serviceServingCA); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get service serving CA")
		} else {
			serviceServingCA = nil
		}
	}

	recyclerConfig := manifests.RecyclerConfigMap(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, recyclerConfig, func() error {
		return kcm.ReconcileRecyclerConfig(recyclerConfig, p.OwnerRef, releaseImageProvider)
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
			hcp.Spec.Platform.Type,
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

	if _, err := createOrUpdate(ctx, r, kcmDeployment, func() error {
		return kcm.ReconcileDeployment(kcmDeployment, kcmConfig, rootCAConfigMap, serviceServingCA, p, hcp.Spec.Platform.Type)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kcm deployment: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileKubeScheduler(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN, schedulerDeployment *appsv1.Deployment, featureGates []string) error {
	p := scheduler.NewKubeSchedulerParams(ctx, hcp, releaseImageProvider, r.SetDefaultSecurityContext, featureGates)

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
			hcp.Spec.Platform.Type,
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

	if _, err := createOrUpdate(ctx, r, schedulerDeployment, func() error {
		return scheduler.ReconcileDeployment(schedulerDeployment, p.OwnerRef, p.DeploymentConfig, p.HyperkubeImage, p.FeatureGates(), p.SchedulerPolicy(), p.AvailabilityProberImage, p.CipherSuites(), p.MinTLSVersion(), p.DisableProfiling, schedulerConfig, hcp.Spec.Platform.Type)
	}); err != nil {
		return fmt.Errorf("failed to reconcile scheduler deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOpenShiftAPIServer(ctx context.Context, hcp *hyperv1.HostedControlPlane, observedConfig *globalconfig.ObservedConfig, releaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN, deployment *appsv1.Deployment, featureGates []string) error {
	p := oapi.NewOpenShiftAPIServerParams(hcp, observedConfig, releaseImageProvider, r.SetDefaultSecurityContext)
	oapicfg := manifests.OpenShiftAPIServerConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, oapicfg, func() error {
		return oapi.ReconcileConfig(oapicfg, p.AuditWebhookRef, p.OwnerRef, p.EtcdURL, p.IngressDomain(), p.MinTLSVersion(), p.CipherSuites(), p.Image, p.Project, hcp.Spec.Capabilities, featureGates)
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

	serviceServingCA := manifests.ServiceServingCA(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(serviceServingCA), serviceServingCA); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get service serving CA")
		} else {
			serviceServingCA = nil
		}
	}

	noProxy := proxy.DefaultNoProxy(hcp)

	var imageRegistryAdditionalTrustedCAs *corev1.ConfigMap
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.Image != nil && hcp.Spec.Configuration.Image.AdditionalTrustedCA.Name != "" {
		imageRegistryAdditionalTrustedCAs = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: hcp.Spec.Configuration.Image.AdditionalTrustedCA.Name, Namespace: hcp.Namespace}}
		if err := r.Get(ctx, client.ObjectKeyFromObject(imageRegistryAdditionalTrustedCAs), imageRegistryAdditionalTrustedCAs); err != nil {
			return fmt.Errorf("failed to get image registry additional trusted CA configmap: %w", err)
		}
	}

	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return oapi.ReconcileDeployment(deployment, p.AuditWebhookRef, p.OwnerRef, oapicfg, auditCfg, serviceServingCA, p.OpenShiftAPIServerDeploymentConfig, p.OpenShiftAPIServerImage, p.ProxyImage, p.EtcdURL, p.AvailabilityProberImage, p.InternalOAuthDisable, hcp.Spec.Platform.Type, hcp.Spec.AdditionalTrustBundle, imageRegistryAdditionalTrustedCAs, hcp.Spec.Configuration, p.Proxy, noProxy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift apiserver deployment: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOpenShiftOAuthAPIServer(ctx context.Context, hcp *hyperv1.HostedControlPlane, observedConfig *globalconfig.ObservedConfig, releaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := oapi.NewOpenShiftAPIServerParams(hcp, observedConfig, releaseImageProvider, r.SetDefaultSecurityContext)
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
		return oapi.ReconcileOAuthAPIServerDeployment(deployment, p.OwnerRef, auditCfg, p.OAuthAPIServerDeploymentParams(hcp), hcp.Spec.Platform.Type)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOAuthServer(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, oauthHost string, oauthPort int32, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := oauth.NewOAuthServerParams(hcp, releaseImageProvider, oauthHost, oauthPort, r.SetDefaultSecurityContext)

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

	auditCfg := manifests.OAuthAuditConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, auditCfg, func() error {
		return oauth.ReconcileAuditConfig(auditCfg, p.OwnerRef, p.AuditPolicyConfig())
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth openshift audit config: %w", err)
	}

	deployment := manifests.OAuthServerDeployment(hcp.Namespace)
	clusterNoProxy := proxy.DefaultNoProxy(hcp)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return oauth.ReconcileDeployment(ctx, r, deployment, p.AuditWebhookRef, p.OwnerRef, oauthConfig, auditCfg, p.OAuthServerImage, p.DeploymentConfig, p.IdentityProviders(), p.OauthConfigOverrides, p.AvailabilityProberImage, p.NamedCertificates(), p.ProxyImage, p.ProxyConfig, clusterNoProxy, p.OAuthNoProxy, p.ConfigParams(oauthServingCert), hcp.Spec.Platform.Type)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth deployment: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileValidIDPConfigurationCondition(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, oauthHost string, oauthPort int32) error {
	p := oauth.NewOAuthServerParams(hcp, releaseImageProvider, oauthHost, oauthPort, r.SetDefaultSecurityContext)

	// Report any IDP configuration errors as a condition on the HCP
	new := metav1.Condition{
		Type:    string(hyperv1.ValidIDPConfiguration),
		Status:  metav1.ConditionTrue,
		Reason:  "IDPConfigurationValid",
		Message: "Identity provider configuration is valid",
	}
	if _, _, err := oauth.ConvertIdentityProviders(ctx, p.IdentityProviders(), p.OauthConfigOverrides, r, hcp.Namespace); err != nil {
		// Report the error in a condition on the HCP
		r.Log.Error(err, "failed to initialize identity providers")
		new = metav1.Condition{
			Type:    string(hyperv1.ValidIDPConfiguration),
			Status:  metav1.ConditionFalse,
			Reason:  "IDPConfigurationError",
			Message: fmt.Sprintf("failed to initialize identity providers: %v", err),
		}
	}
	// Update the condition on the HCP if it has changed
	if meta.SetStatusCondition(&hcp.Status.Conditions, new) {
		if err := r.Status().Update(ctx, hcp); err != nil {
			return fmt.Errorf("failed to update valid IDP configuration condition: %w", err)
		}
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOpenShiftControllerManager(ctx context.Context, hcp *hyperv1.HostedControlPlane, observedConfig *globalconfig.ObservedConfig, releaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN, featureGates []string) error {
	p := ocm.NewOpenShiftControllerManagerParams(hcp, observedConfig, releaseImageProvider, r.SetDefaultSecurityContext)
	config := manifests.OpenShiftControllerManagerConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, config, func() error {
		return ocm.ReconcileOpenShiftControllerManagerConfig(config,
			p.OwnerRef, p.DeployerImage, p.DockerBuilderImage,
			p.MinTLSVersion(), p.CipherSuites(), p.Image, p.Build,
			p.Network, hcp.Spec.Capabilities, featureGates)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift controller manager config: %w", err)
	}

	if _, exists := hcp.Annotations[hyperv1.DisableMonitoringServices]; !exists {
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
	}

	deployment := manifests.OpenShiftControllerManagerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return ocm.ReconcileDeployment(deployment, p.OwnerRef, p.OpenShiftControllerManagerImage, config, p.DeploymentConfig)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift controller manager deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOpenShiftRouteControllerManager(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := routecm.NewOpenShiftRouteControllerManagerParams(hcp, releaseImageProvider, r.SetDefaultSecurityContext)
	config := manifests.OpenShiftRouteControllerManagerConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, config, func() error {
		return routecm.ReconcileOpenShiftRouteControllerManagerConfig(config, p.OwnerRef, p.MinTLSVersion(), p.CipherSuites(), p.Network)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift route controller manager config: %w", err)
	}

	if err := r.Get(ctx, client.ObjectKeyFromObject(config), config); err != nil {
		return fmt.Errorf("failed to get openshift controller manager config: %w", err)
	}

	if _, exists := hcp.Annotations[hyperv1.DisableMonitoringServices]; !exists {
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
	}

	deployment := manifests.OpenShiftRouteControllerManagerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return routecm.ReconcileDeployment(deployment, p.OpenShiftControllerManagerImage, config, p.DeploymentConfig)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift route controller manager deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileClusterPolicyController(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN, featureGates []string) error {
	p := clusterpolicy.NewClusterPolicyControllerParams(hcp, releaseImageProvider, r.SetDefaultSecurityContext, featureGates)

	config := manifests.ClusterPolicyControllerConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, config, func() error {
		return clusterpolicy.ReconcileClusterPolicyControllerConfig(config, p.OwnerRef, p.MinTLSVersion(), p.CipherSuites(), p.FeatureGates())
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster policy controller config: %w", err)
	}

	deployment := manifests.ClusterPolicyControllerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return clusterpolicy.ReconcileDeployment(deployment, p.OwnerRef, p.Image, p.DeploymentConfig, p.AvailabilityProberImage, hcp.Spec.Platform.Type)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster policy controller deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileClusterVersionOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := cvo.NewCVOParams(hcp, releaseImageProvider, r.SetDefaultSecurityContext, r.EnableCVOManagementClusterMetricsAccess)

	if _, exists := hcp.Annotations[hyperv1.DisableMonitoringServices]; !exists {
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
	}

	if r.EnableCVOManagementClusterMetricsAccess {
		sa := manifests.ClusterVersionOperatorServiceAccount(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r.Client, sa, func() error {
			return cvo.ReconcileServiceAccount(sa, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile cluster version operator serviceaccount: %w", err)
		}

		role := manifests.ClusterVersionOperatorRole(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r.Client, role, func() error {
			return cvo.ReconcileRole(role, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile cluster version operator role: %w", err)
		}

		rb := manifests.ClusterVersionOperatorRoleBinding(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r.Client, rb, func() error {
			return cvo.ReconcileRoleBinding(rb, role, p.OwnerRef, hcp.Namespace)
		}); err != nil {
			return fmt.Errorf("failed to reconcile cluster version operator rolebinding: %w", err)
		}
	}

	var (
		cvoImage              string
		dataPlaneReleaseImage string
	)
	// The CVO prepare-payload script needs the ReleaseImage digest for disconnected environments
	pullSecret := common.PullSecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
		return fmt.Errorf("failed to get pull secret for namespace %s: %w", hcp.Namespace, err)
	}
	pullSecretBytes := pullSecret.Data[corev1.DockerConfigJsonKey]
	// Get the CVO image digest. p.CVOImage includes image overrides from the annotation if specified.
	_, cvoImageRef, err := r.ImageMetadataProvider.GetDigest(ctx, p.CVOImage, pullSecretBytes)
	if err != nil {
		return fmt.Errorf("failed to get CVO image digest %s: %w", p.CVOImage, err)
	}
	cvoImage = cvoImageRef.String()

	// Get the data plane release image digest
	_, dataPlaneReleaseImageRef, err := r.ImageMetadataProvider.GetDigest(ctx, hcp.Spec.ReleaseImage, pullSecretBytes)
	if err != nil {
		return fmt.Errorf("failed to get data plane release image digest %s: %w", hcp.Spec.ReleaseImage, err)
	}
	dataPlaneReleaseImage = dataPlaneReleaseImageRef.String()

	deployment := manifests.ClusterVersionOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return cvo.ReconcileDeployment(deployment, p.OwnerRef, p.DeploymentConfig, cvoImage, dataPlaneReleaseImage, p.CLIImage, p.AvailabilityProberImage, p.ClusterID, hcp.Spec.UpdateService, p.PlatformType, util.HCPOAuthEnabled(hcp), r.EnableCVOManagementClusterMetricsAccess, p.FeatureSet, hcp.Spec.Capabilities)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster version operator deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileClusterNetworkOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider, userReleaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := cno.NewParams(hcp, userReleaseImageProvider.Version(), releaseImageProvider, userReleaseImageProvider, r.SetDefaultSecurityContext, r.DefaultIngressDomain)

	// Create SecretProviderClass when deploying on ARO HCP
	if hyperazureutil.IsAroHCP() {
		cnccSecretProviderClass := manifests.ManagedAzureSecretProviderClass(config.ManagedAzureNetworkSecretStoreProviderClassName, hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, cnccSecretProviderClass, func() error {
			secretproviderclass.ReconcileManagedAzureSecretProviderClass(cnccSecretProviderClass, hcp, hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.Network)
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile ingressoperator secret provider class: %w", err)
		}
	}

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
		return cno.ReconcileDeployment(deployment, p, hcp.Spec.Platform.Type)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster network operator deployment: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) cleanupClusterNetworkOperatorResources(ctx context.Context, hcp *hyperv1.HostedControlPlane, hasRouteCap bool) error {
	if restartAnnotation, ok := hcp.Annotations[hyperv1.RestartDateAnnotation]; ok {
		// CNO manages overall multus-admission-controller deployment. CPO manages restarts.
		// TODO: why is this not done in CNO?
		multusDeployment := manifests.MultusAdmissionControllerDeployment(hcp.Namespace)
		if err := cno.SetRestartAnnotationAndPatch(ctx, r.Client, multusDeployment, restartAnnotation); err != nil {
			return fmt.Errorf("failed to restart multus admission controller: %w", err)
		}

		// CNO manages overall network-node-identity deployment. CPO manages restarts.
		networkNodeIdentityDeployment := manifests.NetworkNodeIdentityDeployment(hcp.Namespace)
		if err := cno.SetRestartAnnotationAndPatch(ctx, r.Client, networkNodeIdentityDeployment, restartAnnotation); err != nil {
			return fmt.Errorf("failed to restart network node identity: %w", err)
		}
	}

	// Clean up ovnkube-sbdb Route if exists
	if hasRouteCap {
		if _, err := util.DeleteIfNeeded(ctx, r.Client, manifests.OVNKubeSBDBRoute(hcp.Namespace)); err != nil {
			return fmt.Errorf("failed to clean up ovnkube-sbdb route: %w", err)
		}
	}

	// Clean up ovnkube-master-external Service if exists
	if _, err := util.DeleteIfNeeded(ctx, r.Client, manifests.MasterExternalService(hcp.Namespace)); err != nil {
		return fmt.Errorf("failed to clean up ovnkube-master-external service: %w", err)
	}

	// Clean up ovnkube-master-internal Service if exists
	if _, err := util.DeleteIfNeeded(ctx, r.Client, manifests.MasterInternalService(hcp.Namespace)); err != nil {
		return fmt.Errorf("failed to clean up ovnkube-master-internal service: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileClusterNodeTuningOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider, userReleaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := nto.NewParams(hcp, userReleaseImageProvider.Version(), releaseImageProvider, userReleaseImageProvider, r.SetDefaultSecurityContext)

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
func (r *HostedControlPlaneReconciler) reconcileDNSOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider, userReleaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := dnsoperator.NewParams(hcp, userReleaseImageProvider.Version(), releaseImageProvider, userReleaseImageProvider, r.SetDefaultSecurityContext)

	if _, exists := hcp.Annotations[hyperv1.DisablePKIReconciliationAnnotation]; !exists {
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
	}

	deployment := manifests.DNSOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		dnsoperator.ReconcileDeployment(deployment, p, hcp.Spec.Platform.Type)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile dnsoperator deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileIngressOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider, userReleaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := ingressoperator.NewParams(hcp, userReleaseImageProvider.Version(), releaseImageProvider, userReleaseImageProvider, r.SetDefaultSecurityContext, hcp.Spec.Platform.Type)

	// Create SecretProviderClass when deploying on managed Azure
	if hyperazureutil.IsAroHCP() {
		ingressSecretProviderClass := manifests.ManagedAzureSecretProviderClass(config.ManagedAzureIngressSecretStoreProviderClassName, hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, ingressSecretProviderClass, func() error {
			secretproviderclass.ReconcileManagedAzureSecretProviderClass(ingressSecretProviderClass, hcp, hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.Ingress)
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile ingress operator secret provider class: %w", err)
		}
	}

	if _, exists := hcp.Annotations[hyperv1.DisablePKIReconciliationAnnotation]; !exists {
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
	}

	deployment := manifests.IngressOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		ingressoperator.ReconcileDeployment(deployment, p, hcp.Spec.Platform.Type)
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

func (r *HostedControlPlaneReconciler) reconcileCloudCredentialOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN) error {
	params := cco.NewParams(hcp, releaseImageProvider.Version(), releaseImageProvider, r.SetDefaultSecurityContext)

	rootCA := manifests.RootCAConfigMap(hcp.Namespace)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return err
	}

	csrSigner := manifests.CSRSignerCASecret(hcp.Namespace)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(csrSigner), csrSigner); err != nil {
		return err
	}

	kubeconfig := manifests.CloudCredentialOperatorKubeconfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kubeconfig, func() error {
		return pki.ReconcileServiceAccountKubeconfig(kubeconfig, csrSigner, rootCA, hcp, cco.WorkerNamespace, cco.WorkerServiceAccount)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cloud credential operator kubeconfig: %w", err)
	}

	deployment := manifests.CloudCredentialOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return cco.ReconcileDeployment(deployment, params, hcp.Spec.Platform.Type)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cloud credential operator deployment: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOperatorLifecycleManager(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider, userReleaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := olm.NewOperatorLifecycleManagerParams(hcp, releaseImageProvider, userReleaseImageProvider.Version(), r.SetDefaultSecurityContext)
	if (hcp.Spec.Configuration != nil && hcp.Spec.Configuration.OperatorHub != nil &&
		hcp.Spec.Configuration.OperatorHub.DisableAllDefaultSources) ||
		hcp.Spec.OLMCatalogPlacement != hyperv1.ManagementOLMCatalogPlacement {
		// Disable all default sources
		olmServices := olm.OLMServices(hcp.Namespace)
		for _, svc := range olmServices {
			if err := r.Client.Delete(ctx, svc.Manifest); err != nil {
				if !apierrors.IsNotFound(err) {
					return fmt.Errorf("failed to delete %s service on OLM reconcile: %w", svc.Name, err)
				}
			}
		}
		olmDeployments := olm.OLMDeployments(p, hcp.Namespace)
		for _, dep := range olmDeployments {
			if _, err := util.DeleteIfNeeded(ctx, r.Client, dep.Manifest); err != nil {
				if !apierrors.IsNotFound(err) {
					return fmt.Errorf("failed to delete %s deployment on OLM reconcile: %w", dep.Name, err)
				}
			}
		}

	} else {
		// Enable all default sources
		if hcp.Spec.OLMCatalogPlacement == hyperv1.ManagementOLMCatalogPlacement {
			overrideImages, err := checkCatalogImageOverides(p.CertifiedOperatorsCatalogImageOverride, p.CommunityOperatorsCatalogImageOverride, p.RedHatMarketplaceCatalogImageOverride, p.RedHatOperatorsCatalogImageOverride)
			if err != nil {
				return fmt.Errorf("failed to reconcile catalogs: %w", err)
			}

			isImageRegistryOverrides := util.ConvertImageRegistryOverrideStringToMap(p.OLMCatalogsISRegistryOverridesAnnotation)

			pullSecret := common.PullSecret(hcp.Namespace)
			if err := r.Get(ctx, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
				return fmt.Errorf("failed to get pull secret for namespace %s: %w", hcp.Namespace, err)
			}

			catalogImages, err = catalogs.GetCatalogImages(ctx, *hcp, pullSecret.Data[corev1.DockerConfigJsonKey], r.ImageMetadataProvider, isImageRegistryOverrides)
			if err != nil {
				return fmt.Errorf("failed to get catalog images: %w", err)
			}

			if r.ManagementClusterCapabilities.Has(capabilities.CapabilityImageStream) {
				catalogsImageStream := manifests.CatalogsImageStream(hcp.Namespace)
				if !overrideImages {
					if _, err := createOrUpdate(ctx, r, catalogsImageStream, func() error {
						return olm.ReconcileCatalogsImageStream(catalogsImageStream, p.OwnerRef, catalogImages)
					}); err != nil {
						return fmt.Errorf("failed to reconcile catalogs image stream: %w", err)
					}
				} else {
					if _, err := util.DeleteIfNeeded(ctx, r, catalogsImageStream); err != nil {
						return fmt.Errorf("failed to remove OLM Catalog ImageStream: %w", err)
					}
				}
			} else {
				if !overrideImages {
					for name, catalog := range catalogImages {
						imageRef, err := reference.Parse(catalog)
						if err != nil {
							return fmt.Errorf("failed to parse catalog image %s: %w", catalog, err)
						}

						digest, _, err := r.ImageMetadataProvider.GetDigest(ctx, imageRef.Exact(), pullSecret.Data[corev1.DockerConfigJsonKey])
						if err != nil {
							return fmt.Errorf("failed to get manifest for image %s: %v", imageRef.Exact(), err)
						}
						imageRef.ID = digest.String()

						catalogOverrides := map[string]*string{
							"redhat-operators":    &p.RedHatOperatorsCatalogImageOverride,
							"certified-operators": &p.CertifiedOperatorsCatalogImageOverride,
							"community-operators": &p.CommunityOperatorsCatalogImageOverride,
							"redhat-marketplace":  &p.RedHatMarketplaceCatalogImageOverride,
						}

						if override, exists := catalogOverrides[name]; exists {
							if ptr.Deref(override, "") == "" {
								*override = imageRef.Exact()
							}
						} else {
							return fmt.Errorf("unknown catalog for catalog image override %s", name)
						}
					}
				}
			}

			olmServices := olm.OLMServices(hcp.Namespace)
			for _, svc := range olmServices {
				if _, err := createOrUpdate(ctx, r, svc.Manifest, func() error {
					return svc.Reconciler(svc.Manifest, p.OwnerRef)
				}); err != nil {
					return fmt.Errorf("failed to reconcile %s service: %w", svc.Name, err)
				}
			}

			olmManagerImage := ""
			exists := false
			if olmManagerImage, exists = releaseImageProvider.ImageExist("operator-lifecycle-manager"); !exists {
				return fmt.Errorf("failed to get olm image from release image provider")
			}

			olmDeployments := olm.OLMDeployments(p, hcp.Namespace)
			for _, dep := range olmDeployments {
				if _, err := createOrUpdate(ctx, r, dep.Manifest, func() error {
					return dep.Reconciler(dep.Manifest, p.OwnerRef, p.DeploymentConfig, dep.Image, olmManagerImage)
				}); err != nil {
					return fmt.Errorf("failed to reconcile %s deployment with image %s: %w", dep.Name, dep.Image, err)
				}
			}
		}
	}

	if _, exists := hcp.Annotations[hyperv1.DisableMonitoringServices]; !exists {
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
	}
	catalogOperatorDeployment := manifests.CatalogOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, catalogOperatorDeployment, func() error {
		return olm.ReconcileCatalogOperatorDeployment(catalogOperatorDeployment, p.OwnerRef, p.OLMImage, p.ProxyImage, p.OperatorRegistryImage, p.ReleaseVersion, p.DeploymentConfig, p.AvailabilityProberImage, p.NoProxy, hcp.Spec.Platform.Type)
	}); err != nil {
		return fmt.Errorf("failed to reconcile catalog operator deployment: %w", err)
	}

	if _, exists := hcp.Annotations[hyperv1.DisableMonitoringServices]; !exists {
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
	}

	olmOperatorDeployment := manifests.OLMOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, olmOperatorDeployment, func() error {
		return olm.ReconcileOLMOperatorDeployment(olmOperatorDeployment, p.OwnerRef, p.OLMImage, p.ProxyImage, p.ReleaseVersion, p.DeploymentConfig, p.AvailabilityProberImage, p.NoProxy, hcp.Spec.Platform.Type)
	}); err != nil {
		return fmt.Errorf("failed to reconcile olm operator deployment: %w", err)
	}

	packageServerDeployment := manifests.OLMPackageServerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, packageServerDeployment, func() error {
		return olm.ReconcilePackageServerDeployment(packageServerDeployment, p.OwnerRef, p.OLMImage, p.ProxyImage, p.ReleaseVersion, p.PackageServerConfig, p.AvailabilityProberImage, p.NoProxy, hcp.Spec.Platform.Type)
	}); err != nil {
		return fmt.Errorf("failed to reconcile packageserver deployment: %w", err)
	}

	// no need to run heap collection in IBM Cloud
	if hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		collectProfilesConfigMap := manifests.CollectProfilesConfigMap(hcp.Namespace)
		if _, err := util.DeleteIfNeeded(ctx, r, collectProfilesConfigMap); err != nil {
			return fmt.Errorf("failed to remove collect profiles config map: %w", err)
		}
		collectProfilesCronJob := manifests.CollectProfilesCronJob(hcp.Namespace)
		if _, err := util.DeleteIfNeeded(ctx, r, collectProfilesCronJob); err != nil {
			return fmt.Errorf("failed to remove collect profiles cronjob: %w", err)
		}
		collectProfilesRole := manifests.CollectProfilesRole(hcp.Namespace)
		if _, err := util.DeleteIfNeeded(ctx, r, collectProfilesRole); err != nil {
			return fmt.Errorf("failed to remove collect profiles role: %w", err)
		}
		collectProfilesRoleBinding := manifests.CollectProfilesRoleBinding(hcp.Namespace)
		if _, err := util.DeleteIfNeeded(ctx, r, collectProfilesRoleBinding); err != nil {
			return fmt.Errorf("failed to remove collect profiles role binding: %w", err)
		}
		collectProfilesSecret := manifests.CollectProfilesSecret(hcp.Namespace)
		if _, err := util.DeleteIfNeeded(ctx, r, collectProfilesSecret); err != nil {
			return fmt.Errorf("failed to remove collect profiles secret: %w", err)
		}
		collectProfilesServiceAccount := manifests.CollectProfilesServiceAccount(hcp.Namespace)
		if _, err := util.DeleteIfNeeded(ctx, r, collectProfilesServiceAccount); err != nil {
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
		olm.ReconcileCollectProfilesCronJob(collectProfilesCronJob, p.OwnerRef, p.OLMImage, hcp)
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

func checkCatalogImageOverides(images ...string) (bool, error) {
	override := false
	for _, image := range images {
		if image != "" {
			override = true
			if !strings.Contains(image, "@sha256:") {
				return false, errors.New("images for OLM catalogs should be referenced only by digest")
			}
		}
	}
	if override {
		for _, image := range images {
			if image == "" {
				return false, errors.New("if OLM catalog images are overridden, all the values for the 4 default catalogs should be provided")
			}
		}
	}
	return override, nil
}

func (r *HostedControlPlaneReconciler) reconcileImageRegistryOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider, userReleaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN) error {
	params := registryoperator.NewParams(hcp, userReleaseImageProvider.Version(), releaseImageProvider, userReleaseImageProvider, r.SetDefaultSecurityContext)

	// Create SecretProviderClass when deploying on managed Azure
	if hyperazureutil.IsAroHCP() {
		imageRegistrySecretProviderClass := manifests.ManagedAzureSecretProviderClass(config.ManagedAzureImageRegistrySecretStoreProviderClassName, hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, imageRegistrySecretProviderClass, func() error {
			secretproviderclass.ReconcileManagedAzureSecretProviderClass(imageRegistrySecretProviderClass, hcp, hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.ImageRegistry)
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile image registry operator secret provider class: %w", err)
		}
	}

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

func (r *HostedControlPlaneReconciler) reconcileIgnitionServerConfigs(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	// Reconcile core ignition config
	r.Log.Info("Reconciling core ignition config")
	if err := r.reconcileCoreIgnitionConfig(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile core ignition config: %w", err)
	}

	// Reconcile machine config server config
	r.Log.Info("Reconciling machine config server config")
	if err := r.reconcileMachineConfigServerConfig(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile mcs config: %w", err)
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

	trustedCABundle := manifests.TrustedCABundleConfigMap(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(trustedCABundle), trustedCABundle); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get trustedCABundle: %w", err)
	}

	kubeletClientCA := manifests.KubeletClientCABundle(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(kubeletClientCA), kubeletClientCA); err != nil {
		return fmt.Errorf("failed to get root kubelet client CA: %w", err)
	}

	p, err := mcs.NewMCSParams(hcp, rootCA, pullSecret, trustedCABundle, kubeletClientCA)
	if err != nil {
		return fmt.Errorf("failed to initialise machine config server parameters config: %w", err)
	}

	cm := manifests.MachineConfigServerConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, cm, func() error {
		return mcs.ReconcileMachineConfigServerConfig(cm, p)
	}); err != nil {
		return fmt.Errorf("failed to reconcile machine config server config: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileManagedTrustedCABundle(ctx context.Context, trustedCABundle *corev1.ConfigMap, caBundleConfigMaps []client.ObjectKey) error {
	caBundles := make([]string, len(caBundleConfigMaps))
	for _, key := range caBundleConfigMaps {
		cm := &corev1.ConfigMap{}
		if err := r.Get(ctx, key, cm); err != nil {
			return fmt.Errorf("failed to get configMap %s: %w", key.Name, err)
		}
		data, hasData := cm.Data[certs.UserCABundleMapKey]
		if !hasData {
			return fmt.Errorf("configMap %s must have a %s key", cm.Name, certs.UserCABundleMapKey)
		}

		caBundles = append(caBundles, data)
	}

	trustedCABundle.Data = make(map[string]string)
	trustedCABundle.Data[certs.UserCABundleMapKey] = strings.Join(caBundles, "")

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

	// Ensure the imageDigestMirrorSet configmap has been removed if no longer needed
	imageContentPolicyIgnitionConfig := manifests.ImageContentPolicyIgnitionConfig(hcp.GetNamespace())
	if !p.HasImageMirrorPolicies {
		_, err := util.DeleteIfNeeded(ctx, r.Client, imageContentPolicyIgnitionConfig)
		if err != nil {
			return fmt.Errorf("failed to delete image content source policy configuration configmap: %w", err)
		}

		return nil
	}

	// ImageDigestMirrorSet is only applicable for release image versions >= 4.13
	r.Log.Info("Reconciling ImageDigestMirrorSet")
	imageDigestMirrorSet := globalconfig.ImageDigestMirrorSet()
	if err := globalconfig.ReconcileImageDigestMirrors(imageDigestMirrorSet, hcp); err != nil {
		return fmt.Errorf("failed to reconcile image content policy: %w", err)
	}

	if _, err := createOrUpdate(ctx, r, imageContentPolicyIgnitionConfig, func() error {
		return ignition.ReconcileImageSourceMirrorsIgnitionConfigFromIDMS(imageContentPolicyIgnitionConfig, p.OwnerRef, imageDigestMirrorSet)
	}); err != nil {
		return fmt.Errorf("failed to reconcile image content source policy ignition config: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileRouter(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN) error {
	if hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		return nil
	}

	routeList := &routev1.RouteList{}
	if err := r.List(ctx, routeList, client.InNamespace(hcp.Namespace)); err != nil {
		return fmt.Errorf("failed to list routes: %w", err)
	}

	// reconcile the router's configuration
	svcsNameToIP := make(map[string]string)
	for _, route := range routeList.Items {
		svc := &corev1.Service{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name:      route.Spec.To.Name,
				Namespace: hcp.Namespace,
			},
		}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
			return err
		}

		svcsNameToIP[route.Spec.To.Name] = svc.Spec.ClusterIP
	}

	routerConfig := manifests.RouterConfigurationConfigMap(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, routerConfig, func() error {
		return ingress.ReconcileRouterConfiguration(config.OwnerRefFrom(hcp), routerConfig, routeList, svcsNameToIP)
	}); err != nil {
		return fmt.Errorf("failed to reconcile router configuration: %w", err)
	}

	deployment := manifests.RouterDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, deployment, func() error {
		return ingress.ReconcileRouterDeployment(deployment,
			config.OwnerRefFrom(hcp),
			ingress.HCPRouterConfig(hcp, r.SetDefaultSecurityContext),
			releaseImageProvider.GetImage(ingress.PrivateRouterImage),
			routerConfig,
		)
	}); err != nil {
		return fmt.Errorf("failed to reconcile router deployment: %w", err)
	}

	pdb := manifests.RouterPodDisruptionBudget(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, pdb, func() error {
		ingress.ReconcileRouterPodDisruptionBudget(pdb, hcp.Spec.ControllerAvailabilityPolicy, config.OwnerRefFrom(hcp))
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile router pod disruption budget: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) admitHCPManagedRoutes(ctx context.Context, hcp *hyperv1.HostedControlPlane, privateRouterHost, externalRouterHost string) error {
	if hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform && externalRouterHost == "" {
		externalRouterHost = "not.used"
	}

	routeList := &routev1.RouteList{}
	if err := r.List(ctx, routeList, client.InNamespace(hcp.Namespace)); err != nil {
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
			if err := r.Status().Patch(ctx, route, client.MergeFrom(originalRoute)); err != nil {
				return fmt.Errorf("failed to update route %s status: %w", route.Name, err)
			}
		}
	}

	return nil
}

func (r *HostedControlPlaneReconciler) cleanupOldRouterResources(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	oldRouterResources := []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "private-router"}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "private-router"}},
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "private-router"}},
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "private-router"}},
		manifests.RouterTemplateConfigMap(hcp.Namespace),
		manifests.RouterServiceAccount(hcp.Namespace),
		manifests.RouterRole(hcp.Namespace),
		manifests.RouterRoleBinding(hcp.Namespace),
	}
	for _, resource := range oldRouterResources {
		if _, err := util.DeleteIfNeeded(ctx, r.Client, resource); err != nil {
			return fmt.Errorf("failed to delete %T %s: %w", resource, resource.GetName(), err)
		}
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
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get service: %w", err)
		}
	} else {
		_, ok := service.Annotations["service.alpha.openshift.io/serving-cert-secret-name"]
		if ok {
			delete(service.Annotations, "service.alpha.openshift.io/serving-cert-secret-name")
			err := c.Update(ctx, service)
			if err != nil {
				return fmt.Errorf("failed to update service: %w", err)
			}
		}

		_, ok = service.Annotations["service.beta.openshift.io/serving-cert-secret-name"]
		if ok {
			delete(service.Annotations, "service.beta.openshift.io/serving-cert-secret-name")
			err := c.Update(ctx, service)
			if err != nil {
				return fmt.Errorf("failed to update service: %w", err)
			}
		}
	}

	err := removeServiceCASecret(ctx, c, secret)
	if err != nil {
		return err
	}

	return nil
}

func doesServiceHaveServiceCAAnnotation(service *corev1.Service) bool {
	_, ok := service.Annotations["service.alpha.openshift.io/serving-cert-secret-name"]
	if ok {
		return true
	}

	_, ok = service.Annotations["service.beta.openshift.io/serving-cert-secret-name"]
	return ok
}

func removeServiceCASecret(ctx context.Context, c client.Client, secret *corev1.Secret) error {
	if err := c.Get(ctx, client.ObjectKeyFromObject(secret), secret); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get secret: %w", err)
		}
	} else {
		_, ok := secret.Annotations["service.alpha.openshift.io/originating-service-name"]
		if ok {
			_, err := util.DeleteIfNeeded(ctx, c, secret)
			if err != nil {
				return fmt.Errorf("failed to delete secret generated by service-ca: %w", err)
			}
		}

		_, ok = secret.Annotations["service.beta.openshift.io/originating-service-name"]
		if ok {
			_, err := util.DeleteIfNeeded(ctx, c, secret)
			if err != nil {
				return fmt.Errorf("failed to delete secret generated by service-ca: %w", err)
			}
		}
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileControlPlanePKIOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN, openShiftTrustedCABundleConfigMapForCPOExists bool, certRotationScale time.Duration) error {
	ownerRef := config.OwnerRefFrom(hcp)

	sa := manifests.PKIOperatorServiceAccount(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, sa, func() error {
		return pkioperator.ReconcileServiceAccount(sa, ownerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile control plane pki operator service account: %w", err)
	}

	role := manifests.PKIOperatorRole(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, role, func() error {
		return pkioperator.ReconcileRole(role, ownerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile control plane pki operator role: %w", err)
	}

	rb := manifests.PKIOperatorRoleBinding(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, rb, func() error {
		return pkioperator.ReconcileRoleBinding(rb, role, sa, ownerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile control plane pki operator rolebinding: %w", err)
	}

	deployment := manifests.PKIOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, deployment, func() error {
		return pkioperator.ReconcileDeployment(deployment, openShiftTrustedCABundleConfigMapForCPOExists, hcp, releaseImageProvider.GetImage(util.CPPKIOImageName), r.SetDefaultSecurityContext, sa, certRotationScale)
	}); err != nil {
		return fmt.Errorf("failed to reconcile control plane pki operator deployment: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileHostedClusterConfigOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, infraStatus infra.InfrastructureStatus, createOrUpdate upsert.CreateOrUpdateFN, openShiftTrustedCABundleConfigMapForCPOExists bool) error {
	versions, err := releaseImageProvider.ComponentVersions()
	if err != nil {
		return fmt.Errorf("failed to get component versions: %w", err)
	}
	p := configoperator.NewHostedClusterConfigOperatorParams(ctx, hcp, releaseImageProvider, releaseImageProvider.Version(), versions["kubernetes"], r.SetDefaultSecurityContext)

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
		return configoperator.ReconcileDeployment(deployment, p.Image, hcp.Name, p.OpenShiftVersion, p.KubernetesVersion, p.OwnerRef, &p.DeploymentConfig, p.AvailabilityProberImage, r.EnableCIDebugOutput, hcp.Spec.Platform.Type, infraStatus.KonnectivityHost, infraStatus.KonnectivityPort, infraStatus.OAuthHost, infraStatus.OAuthPort, hcp.Spec.ReleaseImage, hcp.Spec.AdditionalTrustBundle, hcp, openShiftTrustedCABundleConfigMapForCPOExists, r.ReleaseProvider.GetRegistryOverrides(), r.ReleaseProvider.GetOpenShiftImageRegistryOverrides())
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

func (r *HostedControlPlaneReconciler) hostedControlPlaneInNamespace(ctx context.Context, resource client.Object) []reconcile.Request {
	hcpList := &hyperv1.HostedControlPlaneList{}
	if err := r.List(ctx, hcpList, &client.ListOptions{
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

func (r *HostedControlPlaneReconciler) reconcileCloudControllerManager(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN) error {
	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		ownerRef := config.OwnerRefFrom(hcp)
		sa := aws.CCMServiceAccount(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, sa, func() error {
			return aws.ReconcileCCMServiceAccount(sa, ownerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile %s cloud provider service account: %w", hcp.Spec.Platform.Type, err)
		}
		p := aws.NewAWSParams(hcp)
		deployment := aws.CCMDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, deployment, func() error {
			return aws.ReconcileDeployment(deployment, hcp, p.DeploymentConfig, sa.Name, releaseImageProvider)
		}); err != nil {
			return fmt.Errorf("failed to reconcile %s cloud controller manager deployment: %w", hcp.Spec.Platform.Type, err)
		}
	case hyperv1.AzurePlatform:
		// Set up the params
		p := azure.NewAzureParams(hcp, r.SetDefaultSecurityContext)

		// Reconcile CCM ServiceAccount
		ownerRef := config.OwnerRefFrom(hcp)
		sa := azure.CCMServiceAccount(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, sa, func() error {
			return azure.ReconcileCCMServiceAccount(sa, ownerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile %s cloud provider service account: %w", hcp.Spec.Platform.Type, err)
		}

		// Reconcile SecretProviderClass
		azureCloudProviderSecretProviderClass := manifests.ManagedAzureSecretProviderClass(config.ManagedAzureCloudProviderSecretProviderClassName, hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, azureCloudProviderSecretProviderClass, func() error {
			secretproviderclass.ReconcileManagedAzureSecretProviderClass(azureCloudProviderSecretProviderClass, hcp, hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.CloudProvider)
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile azure cloud provider secret provider class: %w", err)
		}

		p.TenantID = hcp.Spec.Platform.Azure.TenantID

		// Reconcile the CCM Deployment
		deployment := azure.CCMDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, deployment, func() error {
			return azure.ReconcileDeployment(deployment, hcp, p, sa.Name, releaseImageProvider)
		}); err != nil {
			return fmt.Errorf("failed to reconcile %s cloud controller manager deployment: %w", hcp.Spec.Platform.Type, err)
		}
	case hyperv1.OpenStackPlatform:
		ownerRef := config.OwnerRefFrom(hcp)
		sa := openstack.CCMServiceAccount(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, sa, func() error {
			return openstack.ReconcileCCMServiceAccount(sa, ownerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile %s cloud provider service account: %w", hcp.Spec.Platform.Type, err)
		}

		p := openstack.NewOpenStackParams(hcp)

		// It might be worth handling the this block, including GetCACertFromCredentialsSecret in the ReconcileDeployment as this is specific to openstack
		// and would maintain the pattern of same function signature for all platforms
		credentialsSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: hcp.Spec.Platform.OpenStack.IdentityRef.Name}}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(credentialsSecret), credentialsSecret); err != nil {
			return fmt.Errorf("failed to get OpenStack credentials secret: %w", err)
		}
		caCertData := openstack.GetCACertFromCredentialsSecret(credentialsSecret)
		hasCACert := caCertData != nil

		deployment := openstack.CCMDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, deployment, func() error {
			return openstack.ReconcileDeployment(deployment, hcp, p, sa.Name, releaseImageProvider, hasCACert)
		}); err != nil {
			return fmt.Errorf("failed to reconcile %s cloud controller manager deployment: %w", hcp.Spec.Platform.Type, err)
		}
	case hyperv1.PowerVSPlatform:
		ccmConfig := manifests.PowerVSCCMConfigMap(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, ccmConfig, func() error {
			return powervs.ReconcileCCMConfigMap(ccmConfig, hcp)
		}); err != nil {
			return fmt.Errorf("failed to reconcile %s cloud controller manager config: %w", hcp.Spec.Platform.Type, err)
		}

		deployment := manifests.PowerVSCCMDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, deployment, func() error {
			return powervs.ReconcileCCMDeployment(deployment, hcp, ccmConfig, releaseImageProvider, r.SetDefaultSecurityContext)
		}); err != nil {
			return fmt.Errorf("failed to reconcile %s cloud controller manager deployment: %w", hcp.Spec.Platform.Type, err)
		}
	case hyperv1.KubevirtPlatform:
		// Create the cloud-config file used by Kubevirt CCM
		ccmConfig := kubevirt.CCMConfigMap(hcp.Namespace)

		r.Log.Info("creating kubevirt cloud-config ConfigMap")
		if _, err := createOrUpdate(ctx, r, ccmConfig, func() error {
			r.Log.Info("reconciling kubevirt CCM ConfigMap")
			return kubevirt.ReconcileCloudConfig(ccmConfig, hcp)
		}); err != nil {
			return fmt.Errorf("failed to reconcile %s cloud config: %w", hcp.Spec.Platform.Type, err)
		}

		// Create the ServiceAccount used by Kubevirt CCM to access
		// the KubevirtInfraCluster (which is the ManagementCluster)
		ownerRef := config.OwnerRefFrom(hcp)
		sa := kubevirt.CCMServiceAccount(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, sa, func() error {
			return kubevirt.ReconcileCCMServiceAccount(sa, ownerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile %s cloud provider service account: %w", hcp.Spec.Platform.Type, err)
		}
		role := kubevirt.CCMRole(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, role, func() error {
			return kubevirt.ReconcileCCMRole(role, ownerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile %s cloud provider role: %w", hcp.Spec.Platform.Type, err)
		}
		roleBinding := kubevirt.CCMRoleBinding(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, roleBinding, func() error {
			return kubevirt.ReconcileCCMRoleBinding(roleBinding, ownerRef, sa, role)
		}); err != nil {
			return fmt.Errorf("failed to reconcile %s cloud provider rolebinding: %w", hcp.Spec.Platform.Type, err)
		}

		// Deploy Kubevirt CCM
		deployment := kubevirt.CCMDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, deployment, func() error {
			return kubevirt.ReconcileDeployment(deployment, hcp, sa.Name, releaseImageProvider)
		}); err != nil {
			return fmt.Errorf("failed to reconcile %s cloud config manager deployment: %w", hcp.Spec.Platform.Type, err)
		}
	}
	return nil
}

// reconcileAutoscaler orchestrates reconciliation of autoscaler components using
func (r *HostedControlPlaneReconciler) reconcileAutoscaler(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN) error {
	autoscalerImage := releaseImageProvider.GetImage(ImageStreamAutoscalerImage)
	availabilityProberImage := releaseImageProvider.GetImage(util.AvailabilityProberImageName)
	return autoscaler.ReconcileAutoscaler(ctx, r.Client, hcp, autoscalerImage, availabilityProberImage, createOrUpdate, r.SetDefaultSecurityContext, config.OwnerRefFrom(hcp))
}

func (r *HostedControlPlaneReconciler) reconcileMachineApprover(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN) error {
	machineApproverImage := releaseImageProvider.GetImage(ImageStreamClusterMachineApproverImage)
	availabilityProberImage := releaseImageProvider.GetImage(util.AvailabilityProberImageName)
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
			log.Info("Giving up on resource deletion after timeout", "timeElapsed", duration.ShortHumanDuration(timeElapsed))
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
		cvoDeployment.Spec.Replicas = ptr.To[int32](0)
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

func (r *HostedControlPlaneReconciler) reconcileCSISnapshotControllerOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider, userReleaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN) error {
	params := snapshotcontroller.NewParams(hcp, userReleaseImageProvider.Version(), releaseImageProvider, r.SetDefaultSecurityContext)

	deployment := manifests.CSISnapshotControllerOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return snapshotcontroller.ReconcileOperatorDeployment(deployment, params, hcp.Spec.Platform.Type, r.SetDefaultSecurityContext)
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

func (r *HostedControlPlaneReconciler) reconcileClusterStorageOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider, userReleaseImageProvider imageprovider.ReleaseImageProvider, createOrUpdate upsert.CreateOrUpdateFN) error {
	params := storage.NewParams(hcp, userReleaseImageProvider.Version(), releaseImageProvider, userReleaseImageProvider, r.SetDefaultSecurityContext)

	if hyperazureutil.IsAroHCP() {
		// Reconcile SecretProviderClasses
		azureDiskSecretProviderClass := manifests.ManagedAzureSecretProviderClass(config.ManagedAzureDiskCSISecretStoreProviderClassName, hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, azureDiskSecretProviderClass, func() error {
			secretproviderclass.ReconcileManagedAzureSecretProviderClass(azureDiskSecretProviderClass, hcp, hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.Disk)
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile Azure Disk Secret Provider Class: %w", err)
		}

		azureFileSecretProviderClass := manifests.ManagedAzureSecretProviderClass(config.ManagedAzureFileCSISecretStoreProviderClassName, hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, azureFileSecretProviderClass, func() error {
			secretproviderclass.ReconcileManagedAzureSecretProviderClass(azureFileSecretProviderClass, hcp, hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.File)
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile Azure File Secret Provider Class: %w", err)
		}

		tenantID := hcp.Spec.Platform.Azure.TenantID

		// Reconcile the secret needed for azure-disk-csi-controller
		// This is related to https://github.com/openshift/csi-operator/pull/290.
		azureDiskCSISecret := manifests.AzureDiskConfigWithCredentials(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, azureDiskCSISecret, func() error {
			return storage.ReconcileAzureDiskCSISecret(azureDiskCSISecret, hcp, tenantID)
		}); err != nil {
			return fmt.Errorf("failed to reconcile Azure Disk CSI config: %w", err)
		}

		// Reconcile the secret needed for azure-disk-csi-controller
		// This is related to https://github.com/openshift/csi-operator/pull/290.
		azureFileCSISecret := manifests.AzureFileConfigWithCredentials(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, azureFileCSISecret, func() error {
			return storage.ReconcileAzureFileCSISecret(azureFileCSISecret, hcp, tenantID)
		}); err != nil {
			return fmt.Errorf("failed to reconcile Azure File CSI config: %w", err)
		}
	}

	deployment := manifests.ClusterStorageOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return storage.ReconcileOperatorDeployment(deployment, params, hcp.Spec.Platform.Type)
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

func (r *HostedControlPlaneReconciler) reconcileDefaultSecurityGroup(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	logger := ctrl.LoggerFrom(ctx)
	if hcp.Spec.Platform.Type != hyperv1.AWSPlatform {
		// Not AWS platform, skip
		return nil
	}

	if hcp.Status.Platform != nil && hcp.Status.Platform.AWS != nil && hcp.Status.Platform.AWS.DefaultWorkerSecurityGroupID != "" {
		// Security group has already been created, update tags if necessary and return.
		lastAppliedTags, err := getLastAppliedSecurityGroupTags(hcp)
		if err != nil {
			return fmt.Errorf("failed to get last applied security group tags annotation: %w", err)
		}

		// if we failed to apply the annotation prviously, fallback to fetching the current tags from the SG resource directly.
		if lastAppliedTags == nil {
			sg, err := supportawsutil.GetSecurityGroupById(r.ec2Client, hcp.Status.Platform.AWS.DefaultWorkerSecurityGroupID)
			if err != nil {
				return fmt.Errorf("failed to get default security group: %w", err)
			}
			lastAppliedTags = supportawsutil.EC2TagsToMap(sg.Tags)
		}

		desiredTags := awsSecurityGroupTags(hcp)
		changed, deleted, isDifferent := util.MapsDiff(lastAppliedTags, desiredTags)
		if !isDifferent {
			return nil
		}

		logger.Info("Security group tags have changed", "changed", changed, "deleted", deleted)
		if err := supportawsutil.UpdateResourceTags(r.ec2Client, hcp.Status.Platform.AWS.DefaultWorkerSecurityGroupID, changed, deleted); err != nil {
			if supportawsutil.IsPermissionsError(err) {
				logger.Info("insufficient permissions to update security group tags", "sg", hcp.Status.Platform.AWS.DefaultWorkerSecurityGroupID)
				return nil
			}
			return err
		}

		// Update the last-applied-security-group-tags annotation on the HCP with the tags applied to the SG.
		// This is used to track changes to the tags and update them if necessary.
		if err := util.UpdateObject(ctx, r.Client, hcp, func() error {
			if err := updateLastAppliedSecurityGroupTagsAnnotation(hcp, desiredTags); err != nil {
				return fmt.Errorf("failed to update last applied security group tags annotation")
			}
			return nil
		}); err != nil {
			return fmt.Errorf("failed to update HostedControlPlane object: %w", err)
		}

		return nil
	}

	validProvider := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidAWSIdentityProvider))
	if validProvider == nil || validProvider.Status != metav1.ConditionTrue {
		logger.Info("Identity provider not ready. Skipping security group creation.")
		return nil
	}

	originalHCP := hcp.DeepCopy()
	var condition *metav1.Condition
	sgID, appliedTags, creationErr := createAWSDefaultSecurityGroup(ctx, r.ec2Client, hcp)
	if creationErr != nil {
		condition = &metav1.Condition{
			Type:    string(hyperv1.AWSDefaultSecurityGroupCreated),
			Status:  metav1.ConditionFalse,
			Message: creationErr.Error(),
			Reason:  hyperv1.AWSErrorReason,
		}
	} else {
		// Ensure the last-applied-security-group-tags annotation is set on the HCP on SG creation.
		// This is used to track changes to the tags and update them if necessary.
		if err := util.UpdateObject(ctx, r.Client, hcp, func() error {
			if err := updateLastAppliedSecurityGroupTagsAnnotation(hcp, appliedTags); err != nil {
				return fmt.Errorf("failed to update last applied security group tags annotation")
			}
			return nil
		}); err != nil {
			return fmt.Errorf("failed to update HostedControlPlane object: %w", err)
		}
		originalHCP = hcp.DeepCopy()

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

func createAWSDefaultSecurityGroup(ctx context.Context, ec2Client ec2iface.EC2API, hcp *hyperv1.HostedControlPlane) (string, map[string]string, error) {
	logger := ctrl.LoggerFrom(ctx)

	var (
		vpcID   = hcp.Spec.Platform.AWS.CloudProviderConfig.VPC
		infraID = hcp.Spec.InfraID
	)

	// Validate VPC exists
	vpcResult, err := ec2Client.DescribeVpcsWithContext(ctx, &ec2.DescribeVpcsInput{
		VpcIds: []*string{awssdk.String(vpcID)},
	})
	if err != nil {
		logger.Error(err, "Failed to describe vpc", "vpcID", vpcID)
		return "", nil, fmt.Errorf("failed to describe vpc %s, code %s", vpcID, supportawsutil.AWSErrorCode(err))
	}
	if len(vpcResult.Vpcs) == 0 {
		return "", nil, fmt.Errorf("vpc %s not found", vpcID)
	}

	if len(hcp.Spec.Networking.MachineNetwork) == 0 {
		// Should never happen
		return "", nil, errors.New("failed to extract machine CIDR while creating default security group: hostedcontrolplane.spec.networking.machineNetwork length is 0")
	}
	machineCIDRs := make([]string, len(hcp.Spec.Networking.MachineNetwork))
	for i, mNet := range hcp.Spec.Networking.MachineNetwork {
		machineCIDRs[i] = mNet.CIDR.String()
	}

	// Search for an existing default worker security group and create one if not found
	describeSGResult, err := ec2Client.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{Filters: awsSecurityGroupFilters(infraID)})
	if err != nil {
		return "", nil, fmt.Errorf("cannot list security groups, code: %s", supportawsutil.AWSErrorCode(err))
	}
	sgID := ""
	var sg *ec2.SecurityGroup
	if len(describeSGResult.SecurityGroups) > 0 {
		sg = describeSGResult.SecurityGroups[0]
		sgID = awssdk.StringValue(sg.GroupId)
	}

	var tags map[string]string
	if sgID == "" {
		// Create a security group if one is not found
		tags = awsSecurityGroupTags(hcp)

		createSGResult, err := ec2Client.CreateSecurityGroup(&ec2.CreateSecurityGroupInput{
			GroupName:   awssdk.String(awsSecurityGroupName(infraID)),
			Description: awssdk.String("default worker security group"),
			VpcId:       awssdk.String(vpcID),
			TagSpecifications: []*ec2.TagSpecification{
				{
					ResourceType: awssdk.String("security-group"),
					Tags:         supportawsutil.MapToEC2Tags(tags),
				},
			},
		})
		if err != nil {
			return "", nil, fmt.Errorf("failed to create security group, code: %s", supportawsutil.AWSErrorCode(err))
		}
		sgID = awssdk.StringValue(createSGResult.GroupId)

		// Fetch just-created SG
		describeSGInput := &ec2.DescribeSecurityGroupsInput{
			GroupIds: []*string{awssdk.String(sgID)},
		}
		if err = ec2Client.WaitUntilSecurityGroupExistsWithContext(ctx, describeSGInput); err != nil {
			return "", nil, fmt.Errorf("failed to find created security group (id: %s), code: %s", sgID, supportawsutil.AWSErrorCode(err))
		}

		describeSGResult, err = ec2Client.DescribeSecurityGroups(describeSGInput)
		if err != nil || len(describeSGResult.SecurityGroups) == 0 {
			return "", nil, fmt.Errorf("failed to fetch security group (id: %s), code: %s", sgID, supportawsutil.AWSErrorCode(err))
		}

		sg = describeSGResult.SecurityGroups[0]
		logger.Info("Created security group", "id", sgID)
	} else {
		tags = supportawsutil.EC2TagsToMap(sg.Tags)
	}

	ingressPermissions := supportawsutil.DefaultWorkerSGIngressRules(machineCIDRs, sgID, awssdk.StringValue(sg.OwnerId))
	_, err = ec2Client.AuthorizeSecurityGroupIngress(&ec2.AuthorizeSecurityGroupIngressInput{
		GroupId:       awssdk.String(sgID),
		IpPermissions: ingressPermissions,
	})
	if err != nil {
		if supportawsutil.AWSErrorCode(err) != "InvalidPermission.Duplicate" {
			return "", nil, fmt.Errorf("failed to set security group ingress rules, code: %s", supportawsutil.AWSErrorCode(err))
		}
		logger.Info("WARNING: got duplicate permissions error when setting security group ingress permissions", "sgID", sgID)
	}
	return sgID, tags, nil
}

func updateLastAppliedSecurityGroupTagsAnnotation(hcp *hyperv1.HostedControlPlane, tags map[string]string) error {
	if hcp.Annotations == nil {
		hcp.Annotations = make(map[string]string)
	}

	jsonTags, err := json.Marshal(tags)
	if err != nil {
		return err
	}

	hcp.Annotations[LastAppliedSecurityGroupTagsAnnotation] = string(jsonTags)
	return nil
}

func getLastAppliedSecurityGroupTags(hcp *hyperv1.HostedControlPlane) (map[string]string, error) {
	tagsAnnotation, ok := hcp.Annotations[LastAppliedSecurityGroupTagsAnnotation]
	if !ok {
		return nil, nil
	}

	tags := make(map[string]string)
	if err := json.Unmarshal([]byte(tagsAnnotation), &tags); err != nil {
		return nil, err
	}
	return tags, nil
}

func awsSecurityGroupTags(hcp *hyperv1.HostedControlPlane) map[string]string {
	var (
		infraID        = hcp.Spec.InfraID
		additionalTags = hcp.Spec.Platform.AWS.ResourceTags
	)

	tags := map[string]string{}
	for _, tag := range additionalTags {
		tags[tag.Key] = tag.Value
	}

	clusterKey := fmt.Sprintf("kubernetes.io/cluster/%s", infraID)
	if _, exist := tags[clusterKey]; !exist {
		tags[clusterKey] = "owned"
	}

	if _, exist := tags["Name"]; !exist {
		tags["Name"] = awsSecurityGroupName(infraID)
	}

	if hcp.Spec.AutoNode != nil && hcp.Spec.AutoNode.Provisioner.Name == hyperv1.ProvisionerKarpeneter &&
		hcp.Spec.AutoNode.Provisioner.Karpenter.Platform == hyperv1.AWSPlatform {
		if _, exist := tags["karpenter.sh/discovery"]; !exist {
			tags["karpenter.sh/discovery"] = infraID
		}
	}

	return tags
}

func (r *HostedControlPlaneReconciler) destroyAWSDefaultSecurityGroup(ctx context.Context, hcp *hyperv1.HostedControlPlane) (string, error) {
	log := ctrl.LoggerFrom(ctx)

	if hcp.Spec.Platform.Type != hyperv1.AWSPlatform {
		return "", nil
	}

	// Get the security group to delete. If it no longer exists, then there's nothing to do
	sg, err := supportawsutil.GetSecurityGroup(r.ec2Client, awsSecurityGroupFilters(hcp.Spec.InfraID))
	if err != nil {
		return "", err
	}
	if sg == nil {
		return "", nil
	}

	if len(sg.IpPermissions) > 0 {
		if _, err = r.ec2Client.RevokeSecurityGroupIngressWithContext(ctx, &ec2.RevokeSecurityGroupIngressInput{
			GroupId:       sg.GroupId,
			IpPermissions: sg.IpPermissions,
		}); err != nil {
			code := "UnknownError"
			if awsErr, ok := err.(awserr.Error); ok {
				code = awsErr.Code()
			}
			log.Error(err, "failed to revoke security group ingress permissions", "SecurityGroupID", awssdk.StringValue(sg.GroupId), "code", code)

			return code, fmt.Errorf("failed to revoke security group ingress rules: %s", code)
		}
	}

	if len(sg.IpPermissionsEgress) > 0 {
		if _, err = r.ec2Client.RevokeSecurityGroupEgressWithContext(ctx, &ec2.RevokeSecurityGroupEgressInput{
			GroupId:       sg.GroupId,
			IpPermissions: sg.IpPermissionsEgress,
		}); err != nil {
			code := "UnknownError"
			if awsErr, ok := err.(awserr.Error); ok {
				code = awsErr.Code()
			}
			log.Error(err, "failed to revoke security group egress permissions", "SecurityGroupID", awssdk.StringValue(sg.GroupId), "code", code)

			return code, fmt.Errorf("failed to revoke security group egress rules: %s", code)
		}
	}

	if _, err = r.ec2Client.DeleteSecurityGroupWithContext(ctx, &ec2.DeleteSecurityGroupInput{
		GroupId: sg.GroupId,
	}); err != nil {
		code := "UnknownError"
		if awsErr, ok := err.(awserr.Error); ok {
			code = awsErr.Code()
		}
		log.Error(err, "failed to delete security group", "SecurityGroupID", awssdk.StringValue(sg.GroupId), "code", code)

		return code, fmt.Errorf("failed to delete security group %s: %s", awssdk.StringValue(sg.GroupId), code)
	}

	// Once the security group delete function has been invoked, attempt to get the security group again
	// to ensure that it no longer exists. If it does still exist, then return an error so that we can retry
	// the delete until it's no longer there.
	sg, err = supportawsutil.GetSecurityGroup(r.ec2Client, awsSecurityGroupFilters(hcp.Spec.InfraID))
	if err != nil {
		return "", err
	}
	if sg != nil {
		return "", fmt.Errorf("security group still exists, waiting on deletion")
	}
	return "", nil
}

func hasValidCloudCredentials(hcp *hyperv1.HostedControlPlane) (string, bool) {
	if hcp.Spec.Platform.Type != hyperv1.AWSPlatform {
		return "", true
	}
	validIdentityProvider := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidAWSIdentityProvider))
	if validIdentityProvider != nil && validIdentityProvider.Status != metav1.ConditionTrue {
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
			Message:            fmt.Sprintf("failed to assume role web identity (%s), code: %s", roleArn, supportawsutil.AWSErrorCode(err)),
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
			Message:            fmt.Sprintf("failed to encrypt data using KMS (key: %s), code: %s", kmsKeyArn, supportawsutil.AWSErrorCode(err)),
			Reason:             hyperv1.AWSErrorReason,
		}
	}

	meta.SetStatusCondition(&hcp.Status.Conditions, condition)
}

func (r *HostedControlPlaneReconciler) validateAzureKMSConfig(ctx context.Context, hcp *hyperv1.HostedControlPlane) {
	var (
		cred azcore.TokenCredential
		ok   bool
		err  error
	)

	log := ctrl.LoggerFrom(ctx)

	if hcp.Spec.SecretEncryption == nil || hcp.Spec.SecretEncryption.KMS == nil || hcp.Spec.SecretEncryption.KMS.Azure == nil {
		condition := metav1.Condition{
			Type:               string(hyperv1.ValidAzureKMSConfig),
			ObservedGeneration: hcp.Generation,
			Status:             metav1.ConditionUnknown,
			Message:            "Azure KMS is not configured",
			Reason:             hyperv1.StatusUnknownReason,
		}
		meta.SetStatusCondition(&hcp.Status.Conditions, condition)
		return
	}
	azureKmsSpec := hcp.Spec.SecretEncryption.KMS.Azure

	if hyperazureutil.IsAroHCP() {
		key := hcp.Namespace + kmsAzureCredentials

		// We need to only store the Azure credentials once and reuse them after that.
		storedCreds, found := r.kmsAzureCredentialsLoaded.Load(key)
		if !found {
			// Retrieve the KMS UserAssignedCredentials path
			credentialsPath := config.ManagedAzureCredentialsPathForKMS + hcp.Spec.SecretEncryption.KMS.Azure.KMS.CredentialsSecretName
			cred, err := dataplane.NewUserAssignedIdentityCredential(ctx, credentialsPath, dataplane.WithClientOpts(azcore.ClientOptions{Cloud: cloud.AzurePublic}))
			if err != nil {
				conditions.SetFalseCondition(hcp, hyperv1.ValidAzureKMSConfig, hyperv1.InvalidAzureCredentialsReason,
					fmt.Sprintf("failed to obtain azure client credentials: %v", err))
				return
			}
			r.kmsAzureCredentialsLoaded.Store(key, cred)
			log.Info("Storing new UserAssignedManagedIdentity credentials for KMS to authenticate to Azure")
		} else {
			cred, ok = storedCreds.(azcore.TokenCredential)
			if !ok {
				conditions.SetFalseCondition(hcp, hyperv1.ValidAzureKMSConfig, hyperv1.InvalidAzureCredentialsReason,
					fmt.Sprintf("expected %T to be a TokenCredential", storedCreds))
				return
			}
			log.Info("Reusing existing UserAssignedManagedIdentity credentials for KMS to authenticate to Azure")
		}
	}

	azureKeyVaultDNSSuffix, err := hyperazureutil.GetKeyVaultDNSSuffixFromCloudType(hcp.Spec.Platform.Azure.Cloud)
	if err != nil {
		conditions.SetFalseCondition(hcp, hyperv1.ValidAzureKMSConfig, hyperv1.InvalidAzureCredentialsReason,
			fmt.Sprintf("vault dns suffix not available for cloud: %s", hcp.Spec.Platform.Azure.Cloud))
		return
	}

	vaultURL := fmt.Sprintf("https://%s.%s", azureKmsSpec.ActiveKey.KeyVaultName, azureKeyVaultDNSSuffix)
	keysClient, err := azkeys.NewClient(vaultURL, cred, nil)
	if err != nil {
		conditions.SetFalseCondition(hcp, hyperv1.ValidAzureKMSConfig, hyperv1.AzureErrorReason,
			fmt.Sprintf("failed to create azure keys client: %v", err))
		return
	}

	condition := metav1.Condition{
		Type:               string(hyperv1.ValidAzureKMSConfig),
		ObservedGeneration: hcp.Generation,
		Status:             metav1.ConditionTrue,
		Message:            hyperv1.AllIsWellMessage,
		Reason:             hyperv1.AsExpectedReason,
	}

	input := azkeys.KeyOperationParameters{
		Algorithm: ptr.To(azkeys.EncryptionAlgorithmRSAOAEP256),
		Value:     []byte("text"),
	}
	if _, err := keysClient.Encrypt(ctx, azureKmsSpec.ActiveKey.KeyName, azureKmsSpec.ActiveKey.KeyVersion, input, &azkeys.EncryptOptions{}); err != nil {
		condition = metav1.Condition{
			Type:               string(hyperv1.ValidAzureKMSConfig),
			ObservedGeneration: hcp.Generation,
			Status:             metav1.ConditionFalse,
			Message:            fmt.Sprintf("failed to encrypt data using KMS (key: %s/%s): %v", azureKmsSpec.ActiveKey.KeyName, azureKmsSpec.ActiveKey.KeyVersion, err),
			Reason:             hyperv1.AzureErrorReason,
		}
	}

	meta.SetStatusCondition(&hcp.Status.Conditions, condition)
}

func (r *HostedControlPlaneReconciler) GetGuestClusterClient(ctx context.Context, hcp *hyperv1.HostedControlPlane) (*kubernetes.Clientset, error) {
	kubeconfigSecret := manifests.KASAdminKubeconfigSecret(hcp.Namespace, hcp.Spec.KubeConfig)
	if err := r.Get(ctx, client.ObjectKeyFromObject(kubeconfigSecret), kubeconfigSecret); err != nil {
		return nil, err
	}

	kubeconfig := kubeconfigSecret.Data[DefaultAdminKubeconfigKey]
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, err
	}

	return kubernetes.NewForConfig(restConfig)
}

// reconcileSREMetricsConfig ensures that if using the SRE metrics set that the loaded configuration
// is the latest from the ConfigMap.
func (r *HostedControlPlaneReconciler) reconcileSREMetricsConfig(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, cpNamespace string) error {
	log := ctrl.LoggerFrom(ctx)
	if r.MetricsSet != metrics.MetricsSetSRE {
		return nil
	}
	log.Info("Reconciling SRE metrics configuration")
	cm := metrics.SREMetricsSetConfigurationConfigMap(cpNamespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(cm), cm); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("SRE configuration does not exist")
			return nil
		}
		return fmt.Errorf("failed to get SRE configuration configmap: %w", err)
	}
	currentMetricsSetConfigHash := metrics.SREMetricsSetConfigHash(cm)
	if currentMetricsSetConfigHash != r.SREConfigHash {
		// Only load a new config if configuration content has changed
		if err := metrics.LoadSREMetricsSetConfigurationFromConfigMap(cm); err != nil {
			return fmt.Errorf("failed to load SRE configuration: %w", err)
		}
		r.SREConfigHash = currentMetricsSetConfigHash
	}
	return nil
}

func doesOpenShiftTrustedCABundleConfigMapForCPOExist(ctx context.Context, c client.Client, hcpNamespace string) (bool, error) {
	openShiftTrustedCABundleConfigMapForCPO := manifests.OpenShiftTrustedCABundleFromCPO(hcpNamespace)
	if err := c.Get(ctx, client.ObjectKeyFromObject(openShiftTrustedCABundleConfigMapForCPO), openShiftTrustedCABundleConfigMapForCPO); err != nil {
		// It's okay if this ConfigMap doesn't exist. It won't for non-OCP clusters. Only return an error if the error is something other than not existing.
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("error getting %T: %w", openShiftTrustedCABundleConfigMapForCPO, err)
		}
	}
	if openShiftTrustedCABundleConfigMapForCPO.Data != nil {
		return true, nil
	}
	return false, nil
}

// verifyResourceGroupLocationsMatch verifies the locations match for the VNET, network security group, and managed resource groups
func (r *HostedControlPlaneReconciler) verifyResourceGroupLocationsMatch(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	var (
		creds     azcore.TokenCredential
		found, ok bool
		err       error
	)

	key := hcp.Namespace + cpoAzureCredentials
	log := ctrl.LoggerFrom(ctx)

	// We need to only store the Azure credentials once and reuse them after that.
	storedCreds, found := r.cpoAzureCredentialsLoaded.Load(key)
	if !found {
		certPath := config.ManagedAzureCertificatePath + hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.ControlPlaneOperator.CredentialsSecretName
		creds, err = dataplane.NewUserAssignedIdentityCredential(ctx, certPath, dataplane.WithClientOpts(azcore.ClientOptions{Cloud: cloud.AzurePublic}), dataplane.WithLogger(&log))
		if err != nil {
			return fmt.Errorf("failed to create azure creds to verify resource group locations: %v", err)
		}

		r.cpoAzureCredentialsLoaded.Store(key, creds)
		log.Info("Storing new UserAssignedManagedIdentity credentials for the CPO to authenticate to Azure")
	} else {
		creds, ok = storedCreds.(azcore.TokenCredential)
		if !ok {
			return fmt.Errorf("expected %T to be a TokenCredential", storedCreds)
		}
		log.Info("Reusing existing UserAssignedManagedIdentity credentials for the CPO to authenticate to Azure")
	}

	// Retrieve full vnet information from the VNET ID
	vnet, err := hyperazureutil.GetVnetInfoFromVnetID(ctx, hcp.Spec.Platform.Azure.VnetID, hcp.Spec.Platform.Azure.SubscriptionID, creds)
	if err != nil {
		return fmt.Errorf("failed to get vnet info to verify its location: %v", err)
	}
	// Retrieve full network security group information from the network security group ID
	nsg, err := hyperazureutil.GetNetworkSecurityGroupInfo(ctx, hcp.Spec.Platform.Azure.SecurityGroupID, hcp.Spec.Platform.Azure.SubscriptionID, creds)
	if err != nil {
		return fmt.Errorf("failed to get network security group info to verify its location: %v", err)
	}
	// Retrieve full resource group information from the resource group name
	rg, err := hyperazureutil.GetResourceGroupInfo(ctx, hcp.Spec.Platform.Azure.ResourceGroupName, hcp.Spec.Platform.Azure.SubscriptionID, creds)
	if err != nil {
		return fmt.Errorf("failed to get resource group info to verify its location: %v", err)
	}
	// Verify the vnet resource group location, network security group resource group location, and the managed resource group location match
	if ptr.Deref(vnet.Location, "") != ptr.Deref(nsg.Location, "") || ptr.Deref(nsg.Location, "") != ptr.Deref(rg.Location, "") {
		return fmt.Errorf("the locations of the resource groups do not match - vnet location: %v; network security group location: %v; managed resource group location: %v", ptr.Deref(vnet.Location, ""), ptr.Deref(nsg.Location, ""), ptr.Deref(rg.Location, ""))
	}
	return nil
}

func setKASCustomKubeconfigStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane, c client.Client) error {
	customKubeconfig := manifests.KASCustomKubeconfigSecret(hcp.Namespace, nil)
	if err := c.Get(ctx, client.ObjectKeyFromObject(customKubeconfig), customKubeconfig); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get custom kubeconfig secret: %w", err)
		}
	}

	if len(hcp.Spec.KubeAPIServerDNSName) > 0 {
		// Reconcile custom kubeconfig status
		hcp.Status.CustomKubeconfig = &hyperv1.KubeconfigSecretRef{
			Name: customKubeconfig.Name,
			Key:  DefaultAdminKubeconfigKey,
		}
	} else {
		// Cleaning up custom kubeconfig status
		hcp.Status.CustomKubeconfig = nil
	}

	return nil
}

// includeServingCertificates includes additional serving certificates into the provided root CA ConfigMap.
// It retrieves the named certificates specified in the HostedControlPlane's APIServer configuration and appends
// their contents to the "ca.crt" entry in the root CA ConfigMap.
func includeServingCertificates(ctx context.Context, c client.Client, hcp *hyperv1.HostedControlPlane, rootCA *corev1.ConfigMap) (*corev1.ConfigMap, error) {
	var tlsCRT string
	newRootCA := rootCA.DeepCopy()

	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.APIServer != nil && len(hcp.Spec.Configuration.APIServer.ServingCerts.NamedCertificates) > 0 {
		for _, servingCert := range hcp.Spec.Configuration.APIServer.ServingCerts.NamedCertificates {
			newCRT := &corev1.Secret{}
			if err := c.Get(ctx, client.ObjectKey{Namespace: hcp.Namespace, Name: servingCert.ServingCertificate.Name}, newCRT); err != nil {
				return nil, fmt.Errorf("failed to get serving certificate secret: %w", err)
			}

			if len(tlsCRT) <= 0 {
				tlsCRT = newRootCA.Data["ca.crt"]
			}

			tlsCRT = fmt.Sprintf("%s\n%s", tlsCRT, string(newCRT.Data["tls.crt"]))
		}

		if len(tlsCRT) > 0 {
			newRootCA.Data["ca.crt"] = tlsCRT
		}
	}

	return newRootCA, nil
}
