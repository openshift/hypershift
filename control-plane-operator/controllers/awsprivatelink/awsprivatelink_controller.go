package awsprivatelink

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/awsapi"
	supportawsutil "github.com/openshift/hypershift/support/awsutil"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/netutil"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/workqueue"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/go-logr/logr"
)

const (
	defaultResync               = 10 * time.Hour
	externalPrivateServiceLabel = "hypershift.openshift.io/external-private-service"
	throttleRequeueDelay        = 2 * time.Minute
)

// PrivateServiceObserver watches a given Service type LB and reconciles
// an awsEndpointService CR representation for it.
type PrivateServiceObserver struct {
	client.Client

	clientset *kubeclient.Clientset
	log       logr.Logger

	ControllerName   string
	ServiceNamespace string
	ServiceName      string
	HCPNamespace     string
	upsert.CreateOrUpdateProvider
}

func nameMapper(names []string) handler.MapFunc {
	nameSet := sets.NewString(names...)
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		if !nameSet.Has(obj.GetName()) {
			return nil
		}
		return []reconcile.Request{
			{
				NamespacedName: types.NamespacedName{
					Namespace: obj.GetNamespace(),
					Name:      obj.GetName(),
				},
			},
		}
	}
}

func namedResourceHandler(names ...string) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(nameMapper(names))
}

func ControllerName(name string) string {
	return fmt.Sprintf("%s-observer", name)
}

func (r *PrivateServiceObserver) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	r.log = ctrl.Log.WithName(r.ControllerName).WithValues("name", r.ServiceName, "namespace", r.ServiceNamespace)
	var err error
	r.clientset, err = kubeclient.NewForConfig(mgr.GetConfig())
	if err != nil {
		return err
	}
	informerFactory := informers.NewSharedInformerFactoryWithOptions(r.clientset, defaultResync, informers.WithNamespace(r.ServiceNamespace))
	services := informerFactory.Core().V1().Services()
	c, err := controller.New(r.ControllerName, mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}
	if err := c.Watch(&source.Informer{
		Informer: services.Informer(),
		Handler:  namedResourceHandler(r.ServiceName),
	}); err != nil {
		return err
	}
	err = mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		informerFactory.Start(ctx.Done())
		return nil
	}))
	if err != nil {
		return err
	}
	return nil
}

func (r *PrivateServiceObserver) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Fetch the Service
	svc, err := r.clientset.CoreV1().Services(req.Namespace).Get(ctx, req.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			r.log.Info("service not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Fetch the HostedControlPlane
	hcpList := &hyperv1.HostedControlPlaneList{}
	if err := r.List(ctx, hcpList, &client.ListOptions{Namespace: r.HCPNamespace}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get resource: %w", err)
	}
	if len(hcpList.Items) == 0 {
		// Return early if HostedControlPlane is deleted
		return ctrl.Result{}, nil
	}
	if len(hcpList.Items) > 1 {
		return ctrl.Result{}, fmt.Errorf("unexpected number of HostedControlPlanes in namespace, expected: 1, actual: %d", len(hcpList.Items))
	}

	hcp := hcpList.Items[0]

	// Return early if HostedControlPlane is deleted
	if !hcp.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if len(svc.Status.LoadBalancer.Ingress) == 0 {
		r.log.Info("load balancer not provisioned yet")
		return ctrl.Result{}, nil
	}
	awsEndpointService := &hyperv1.AWSEndpointService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.ServiceName,
			Namespace: r.HCPNamespace,
		},
	}
	lbName := extractNLBName(svc.Status.LoadBalancer.Ingress[0].Hostname)
	if _, err := r.CreateOrUpdate(ctx, r, awsEndpointService, func() error {
		awsEndpointService.Spec.NetworkLoadBalancerName = lbName
		if hcp.Spec.Platform.AWS != nil {
			awsEndpointService.Spec.ResourceTags = hcp.Spec.Platform.AWS.ResourceTags
		}
		return nil
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile AWSEndpointService: %w", err)
	}
	return ctrl.Result{}, nil
}

// extractNLBName extracts the NLB name from its DNS hostname.
//
// AWS NLB DNS format is "{name}-{id}.elb.{region}.amazonaws.com"
// where {name} is the value passed to CreateLoadBalancer and {id} is an
// AWS-assigned hex suffix.
// Ref: https://docs.aws.amazon.com/elasticloadbalancing/latest/network/network-load-balancers.html#dns-name
//
// The in-tree cloud provider generates hyphen-free names ("a" + UID),
// but the AWS LB Controller (EKS Auto Mode) uses "k8s-{ns}-{svc}-{hash}".
// We strip only the last dash-delimited segment (the AWS-assigned ID)
// because {id} is always hex (no hyphens), as shown in every AWS API
// example and required structurally — since {name} may contain hyphens,
// a hyphenated {id} would make the format ambiguous.
//
// In-tree name generation: https://github.com/kubernetes/cloud-provider/blob/v0.32.3/cloud.go#L89-L98
// AWS LB Controller name generation: https://github.com/kubernetes-sigs/aws-load-balancer-controller/blob/v2.12.0/pkg/service/model_build_load_balancer.go#L591-L608
func extractNLBName(hostname string) string {
	firstLabel := strings.Split(hostname, ".")[0]
	lastDash := strings.LastIndex(firstLabel, "-")
	if lastDash == -1 {
		return firstLabel
	}
	return firstLabel[:lastDash]
}

// errDependencyViolation is returned when AWS reports a DependencyViolation,
// indicating the VPC endpoint is still being deleted. The caller translates
// this into a controlled requeue rather than an error-driven requeue.
var errDependencyViolation = errors.New("security group dependency violation")

const (
	finalizer                              = "hypershift.openshift.io/control-plane-operator-finalizer"
	endpointServiceDeletionRequeueDuration = 5 * time.Second
	hypershiftLocalZone                    = "hypershift.local"
	routerDomain                           = "apps"

	// hcpAWSPrivateLinkFinalizerName is a finalizer placed on the HostedControlPlane by
	// the AWSEndpointService reconciler. It blocks HCP deletion until all AWS PrivateLink
	// resources (VPC endpoints, security groups, DNS records) are cleaned up.
	//
	// Without this finalizer, the AWSEndpointService CR's own finalizer runs during CR
	// deletion, but the controller may not be able to construct valid AWS clients if the
	// HCP has already been deleted — particularly for SharedVPC clusters where cross-account
	// role ARNs are sourced from the HCP spec. This would orphan AWS resources.
	hcpAWSPrivateLinkFinalizerName = "hypershift.openshift.io/aws-private-link-endpoint-cleanup"
)

// AWSEndpointServiceReconciler watches AWSEndpointService resources and reconciles
// the existence of AWS Endpoints for it in the guest cluster infrastructure.
type AWSEndpointServiceReconciler struct {
	client.Client
	upsert.CreateOrUpdateProvider
	awsClientBuilder awsClientProvider

	subnetAZMu    sync.RWMutex
	subnetAZCache map[string]string
}

// awsClientProvider abstracts AWS client creation for testability.
//
//go:generate ../../../hack/tools/bin/mockgen -source=awsprivatelink_controller.go -package=awsprivatelink -destination=awsprivatelink_controller_mock.go
type awsClientProvider interface {
	getClients(ctx context.Context) (awsapi.EC2API, awsapi.ROUTE53API, error)
	initializeWithHCP(log logr.Logger, hcp *hyperv1.HostedControlPlane)
	getLocalHostedZoneID() string
	setLocalHostedZoneID(zoneID string)
}

// Verify clientBuilder implements awsClientProvider.
var _ awsClientProvider = (*clientBuilder)(nil)

type clientBuilder struct {
	mu                             sync.Mutex
	initialized                    bool
	assumeSharedVPCEndpointRoleARN string
	assumeSharedVPCRoute53RoleARN  string
	localZoneID                    string
}

func (b *clientBuilder) awsConfig(ctx context.Context) (aws.Config, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithAPIOptions([]func(*middleware.Stack) error{
			awsmiddleware.AddUserAgentKeyValue("openshift.io hypershift", "control-plane-operator"),
		}),
	)
	if err != nil {
		return aws.Config{}, fmt.Errorf("failed to create AWS config: %w", err)
	}
	return cfg, nil
}

func (b *clientBuilder) getClients(ctx context.Context) (awsapi.EC2API, awsapi.ROUTE53API, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.initialized {
		return nil, nil, errors.New("clients not initialized")
	}

	// AWS_SHARED_CREDENTIALS_FILE and AWS_REGION envvar should be set in operator deployment
	ec2Cfg, err := b.awsConfig(ctx)
	if err != nil {
		return nil, nil, err
	}
	route53Cfg, err := b.awsConfig(ctx)
	if err != nil {
		return nil, nil, err
	}

	// When sharedVPC we need assume these additional roles
	if b.assumeSharedVPCEndpointRoleARN != "" {
		stsClient := sts.NewFromConfig(ec2Cfg)
		ec2Cfg.Credentials = aws.NewCredentialsCache(
			stscreds.NewAssumeRoleProvider(stsClient, b.assumeSharedVPCEndpointRoleARN),
		)
	}
	if b.assumeSharedVPCRoute53RoleARN != "" {
		stsClient := sts.NewFromConfig(route53Cfg)
		route53Cfg.Credentials = aws.NewCredentialsCache(
			stscreds.NewAssumeRoleProvider(stsClient, b.assumeSharedVPCRoute53RoleARN),
		)
	}

	ec2Client := ec2.NewFromConfig(ec2Cfg)
	route53Client := route53.NewFromConfig(route53Cfg)

	return ec2Client, route53Client, nil
}

func (b *clientBuilder) getLocalHostedZoneID() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.localZoneID
}

func (b *clientBuilder) setLocalHostedZoneID(zoneID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.localZoneID = zoneID
}

func (b *clientBuilder) initializeWithHCP(log logr.Logger, hcp *hyperv1.HostedControlPlane) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.initialized {
		b.setFromHCP(hcp)
		b.initialized = true
	} else {
		b.warnOnDifferentValues(log, hcp)
		b.setFromHCP(hcp)
	}
}

func (b *clientBuilder) warnOnDifferentValues(log logr.Logger, hcp *hyperv1.HostedControlPlane) {
	newEndpointRoleARN := ""
	newRoute53RoleARN := ""
	newLocalZoneID := ""
	if hcp.Spec.Platform.AWS != nil && hcp.Spec.Platform.AWS.SharedVPC != nil {
		newEndpointRoleARN = hcp.Spec.Platform.AWS.SharedVPC.RolesRef.ControlPlaneARN
		newRoute53RoleARN = hcp.Spec.Platform.AWS.SharedVPC.RolesRef.IngressARN
		newLocalZoneID = hcp.Spec.Platform.AWS.SharedVPC.LocalZoneID
	}
	if b.assumeSharedVPCEndpointRoleARN != newEndpointRoleARN {
		log.Info("WARNING: Setting different value for the endpoint role ARN", "previous", b.assumeSharedVPCEndpointRoleARN, "new", newEndpointRoleARN)
	}
	if b.assumeSharedVPCRoute53RoleARN != newRoute53RoleARN {
		log.Info("WARNING: Setting different value for the route53 role ARN", "previous", b.assumeSharedVPCRoute53RoleARN, "new", newRoute53RoleARN)
	}
	if b.localZoneID != newLocalZoneID {
		log.Info("WARNING: Setting different value for local zone ID", "previous", b.localZoneID, "new", newLocalZoneID)
	}
}

func (b *clientBuilder) setFromHCP(hcp *hyperv1.HostedControlPlane) {
	if hcp.Spec.Platform.AWS != nil && hcp.Spec.Platform.AWS.SharedVPC != nil {
		b.assumeSharedVPCEndpointRoleARN = hcp.Spec.Platform.AWS.SharedVPC.RolesRef.ControlPlaneARN
		b.assumeSharedVPCRoute53RoleARN = hcp.Spec.Platform.AWS.SharedVPC.RolesRef.IngressARN
		b.localZoneID = hcp.Spec.Platform.AWS.SharedVPC.LocalZoneID
	} else {
		b.assumeSharedVPCEndpointRoleARN = ""
		b.assumeSharedVPCRoute53RoleARN = ""
		b.localZoneID = ""
	}
}

func (r *AWSEndpointServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.awsClientBuilder == nil {
		r.awsClientBuilder = &clientBuilder{}
	}
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.AWSEndpointService{}).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](3*time.Second, 30*time.Second),
			MaxConcurrentReconciles: 10,
		}).
		Watches(&hyperv1.HostedControlPlane{}, handler.EnqueueRequestsFromMapFunc(
			r.mapHCPToAWSEndpointService(),
		)).
		Build(r)
	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager: %w", err)
	}
	r.Client = mgr.GetClient()

	return nil
}

// mapHCPToAWSEndpointService maps HostedControlPlane events to the AWSEndpointService CRs
// in the same namespace. This uses EnqueueRequestsFromMapFunc (instead of handler.Funcs)
// so that all event types — Create, Update, Delete — trigger the mapping. This is critical
// for controller restarts: if the CPO restarts while an HCP is being deleted (DeletionTimestamp
// already set), the informer cache sync generates a Create event, not an Update. Using
// handler.Funcs{UpdateFunc: ...} would miss this, defeating the purpose of the HCP finalizer.
//
// The function only enqueues CRs when the HCP has our finalizer, avoiding unnecessary
// reconciliations for unrelated HCPs. EndpointAccess changes (previously detected via
// old/new comparison) are picked up by the reconciler's periodic 5-minute requeue.
func (r *AWSEndpointServiceReconciler) mapHCPToAWSEndpointService() handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		hcp, ok := obj.(*hyperv1.HostedControlPlane)
		if !ok {
			return nil
		}

		// Only trigger reconciliation when the HCP has our finalizer; this avoids
		// unnecessary reconciliations for HCPs that are not related to AWS PrivateLink.
		if !controllerutil.ContainsFinalizer(hcp, hcpAWSPrivateLinkFinalizerName) {
			return nil
		}

		awsEndpointServiceList := &hyperv1.AWSEndpointServiceList{}
		if err := r.List(ctx, awsEndpointServiceList, client.InNamespace(hcp.Namespace)); err != nil {
			log.FromContext(ctx).Error(err, "mapHCPToAWSEndpointService: cannot list AWSEndpointService resources", "namespace", hcp.Namespace)
			return nil
		}

		var requests []reconcile.Request
		for i := range awsEndpointServiceList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(&awsEndpointServiceList.Items[i]),
			})
		}
		return requests
	}
}

func (r *AWSEndpointServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log, err := logr.FromContext(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("logger not found: %w", err)
	}

	// Fetch the AWSEndpointService
	obj := &hyperv1.AWSEndpointService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
		},
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get resource: %w", err)
	}

	// Don't change the cached object
	awsEndpointService := obj.DeepCopy()

	// Return early if deleted
	if !awsEndpointService.DeletionTimestamp.IsZero() {
		return r.reconcileCRDeletion(ctx, awsEndpointService, req.Namespace, log)
	}

	serviceName := awsEndpointService.Status.EndpointServiceName
	if serviceName == "" {
		// Service Endpoint is not yet set, wait for hypershift-operator to populate
		// Likely observing our own Create
		return ctrl.Result{}, nil
	}

	hcp, err := r.getHostedControlPlane(ctx, req.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}
	if hcp == nil {
		log.Info("WARNING: HostedControlPlane not found, AWS PrivateLink resources may be orphaned if the HCP was deleted before the finalizer was placed")
		return ctrl.Result{}, nil
	}

	// Handle HCP deletion: clean up AWS resources while HCP credentials are still valid.
	// This check is before the CR finalizer addition so that during HCP deletion,
	// reconcileHCPDeletion does not re-add the CR finalizer after removing it.
	if !hcp.DeletionTimestamp.IsZero() {
		return r.reconcileHCPDeletion(ctx, awsEndpointService, hcp, log)
	}

	// Ensure the awsEndpointService has a finalizer for cleanup.
	// This is placed after the serviceName check so that CRs not yet populated
	// by hypershift-operator don't get a finalizer that would block HCP deletion,
	// and after the HCP deletion check to avoid re-adding the finalizer during
	// HCP deletion cleanup cycles.
	if !controllerutil.ContainsFinalizer(awsEndpointService, finalizer) {
		originalAES := awsEndpointService.DeepCopy()
		controllerutil.AddFinalizer(awsEndpointService, finalizer)
		if err := r.Patch(ctx, awsEndpointService, client.MergeFromWithOptions(originalAES, client.MergeFromWithOptimisticLock{})); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{RequeueAfter: time.Second}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
	}

	if isPaused, duration := util.IsReconciliationPaused(log, hcp.Spec.PausedUntil); isPaused {
		log.Info("Reconciliation paused", "pausedUntil", *hcp.Spec.PausedUntil)
		return ctrl.Result{RequeueAfter: duration}, nil
	}

	r.awsClientBuilder.initializeWithHCP(log, hcp)
	ec2Client, route53Client, err := r.awsClientBuilder.getClients(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get AWS clients for endpoint reconciliation: %w", err)
	}

	// Add HCP finalizer to block HCP deletion until AWS PrivateLink cleanup is done.
	// This is done after initializing clients, ensuring the reconciler is operational
	// and there are actually resources to protect.
	if result, err := r.ensureHCPFinalizer(ctx, hcp, log); err != nil || !result.IsZero() {
		return result, err
	}

	// Reconcile the AWSEndpointService
	oldStatus := awsEndpointService.Status.DeepCopy()
	if err := r.reconcileAWSEndpointService(ctx, awsEndpointService, hcp, ec2Client, route53Client); err != nil {
		meta.SetStatusCondition(&awsEndpointService.Status.Conditions, metav1.Condition{
			Type:    string(hyperv1.AWSEndpointAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.AWSErrorReason,
			Message: err.Error(),
		})
		if !equality.Semantic.DeepEqual(*oldStatus, awsEndpointService.Status) {
			if err := r.Status().Update(ctx, awsEndpointService); err != nil {
				return ctrl.Result{}, err
			}
		}
		if isAWSThrottleError(err) {
			log.Info("AWS rate limit hit, backing off", "requeueAfter", throttleRequeueDelay)
			return ctrl.Result{RequeueAfter: throttleRequeueDelay}, nil
		}
		return ctrl.Result{}, err
	}

	meta.SetStatusCondition(&awsEndpointService.Status.Conditions, metav1.Condition{
		Type:    string(hyperv1.AWSEndpointAvailable),
		Status:  metav1.ConditionTrue,
		Reason:  hyperv1.AWSSuccessReason,
		Message: "",
	})

	if !equality.Semantic.DeepEqual(*oldStatus, awsEndpointService.Status) {
		if err := r.Status().Update(ctx, awsEndpointService); err != nil {
			return ctrl.Result{}, err
		}
	}

	// always requeue to catch and report out of band changes in AWS
	// NOTICE: if the RequeueAfter interval is short enough, it could result in hitting some AWS request limits.
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

func isAWSThrottleError(err error) bool {
	switch supportawsutil.AWSErrorCode(err) {
	case "Throttling", "ThrottlingException", "RequestLimitExceeded", "TooManyRequestsException":
		return true
	}
	return false
}

// reconcileCRDeletion handles the AWSEndpointService CR deletion path.
// It initializes AWS clients (best-effort from HCP if available), cleans up
// AWS resources, and removes the CR finalizer.
func (r *AWSEndpointServiceReconciler) reconcileCRDeletion(ctx context.Context, awsEndpointService *hyperv1.AWSEndpointService, namespace string, log logr.Logger) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(awsEndpointService, finalizer) {
		return ctrl.Result{}, nil
	}

	// Best-effort initialization for deletion reconciles: after a controller restart
	// the clientBuilder is uninitialized because initializeWithHCP is only called in
	// the non-deletion path. If the HCP still exists, initialize from it so that
	// getClients can succeed and deletion can proceed.
	//
	// The HCP finalizer (hcpAWSPrivateLinkFinalizerName), when present, blocks HCP
	// deletion so the HCP remains available during this cleanup. Note: the finalizer
	// is only added after a successful normal reconciliation. If the controller has
	// not yet reconciled (e.g., controller was down since cluster creation), the HCP
	// may be deleted before the finalizer is placed, and this best-effort initialization
	// is the only protection. See reconcileHCPDeletion for the HCP-deletion cleanup path.
	hcpList := &hyperv1.HostedControlPlaneList{}
	if err := r.List(ctx, hcpList, &client.ListOptions{Namespace: namespace}); err == nil && len(hcpList.Items) == 1 {
		r.awsClientBuilder.initializeWithHCP(log, &hcpList.Items[0])
	}

	ec2Client, route53Client, err := r.awsClientBuilder.getClients(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get AWS clients for endpoint service cleanup: %w", err)
	}
	completed, err := r.delete(ctx, awsEndpointService, ec2Client, route53Client)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to delete resource: %w", err)
	}
	if !completed {
		return ctrl.Result{RequeueAfter: endpointServiceDeletionRequeueDuration}, nil
	}
	originalAES := awsEndpointService.DeepCopy()
	controllerutil.RemoveFinalizer(awsEndpointService, finalizer)
	if err := r.Patch(ctx, awsEndpointService, client.MergeFromWithOptions(originalAES, client.MergeFromWithOptimisticLock{})); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

// ensureHCPFinalizer adds the HCP finalizer if not already present.
// Uses optimistic locking via MergeFromWithOptimisticLock to safely update the HCP.
func (r *AWSEndpointServiceReconciler) ensureHCPFinalizer(ctx context.Context, hcp *hyperv1.HostedControlPlane, log logr.Logger) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(hcp, hcpAWSPrivateLinkFinalizerName) {
		return ctrl.Result{}, nil
	}

	log.Info("Adding HCP finalizer for AWS PrivateLink cleanup")
	originalHCP := hcp.DeepCopy()
	controllerutil.AddFinalizer(hcp, hcpAWSPrivateLinkFinalizerName)
	if err := r.Patch(ctx, hcp, client.MergeFromWithOptions(originalHCP, client.MergeFromWithOptimisticLock{})); err != nil {
		if apierrors.IsConflict(err) {
			// Use RequeueAfter to avoid a tight retry loop when multiple AWSEndpointService
			// reconcilers concurrently try to patch the same HCP.
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to add HCP finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *AWSEndpointServiceReconciler) getHostedControlPlane(ctx context.Context, namespace string) (*hyperv1.HostedControlPlane, error) {
	hcpList := &hyperv1.HostedControlPlaneList{}
	if err := r.List(ctx, hcpList, &client.ListOptions{Namespace: namespace}); err != nil {
		return nil, fmt.Errorf("failed to get resource: %w", err)
	}
	if len(hcpList.Items) == 0 {
		return nil, nil
	}
	if len(hcpList.Items) > 1 {
		return nil, fmt.Errorf("unexpected number of HostedControlPlanes in namespace, expected: 1, actual: %d", len(hcpList.Items))
	}
	return &hcpList.Items[0], nil
}

// reconcileHCPDeletion handles HCP deletion by cleaning up AWS PrivateLink resources
// and removing the HCP finalizer. This ensures AWS credentials remain valid during
// the cleanup process, which is critical for SharedVPC clusters where the cross-account
// role ARNs are sourced from the HCP spec.
//
// Convergent multi-CR coordination:
// With MaxConcurrentReconciles: 10 and the HCP watch enqueuing ALL CRs on HCP deletion,
// multiple reconcilers run this function concurrently — one per AWSEndpointService CR.
// Each reconciler cleans up its own CR's AWS resources, then checks whether all CRs in
// the namespace are done. Only the last reconciler to finish (the one that finds no pending
// CRs) removes the HCP finalizer. Earlier reconcilers see pending CRs and return
// RequeueAfter to retry; when they re-enter, they find the HCP finalizer already removed
// and return early at the top of this function. This convergent pattern produces a small
// number of no-op requeues but is correct and self-healing.
func (r *AWSEndpointServiceReconciler) reconcileHCPDeletion(ctx context.Context, awsEndpointService *hyperv1.AWSEndpointService, hcp *hyperv1.HostedControlPlane, log logr.Logger) (ctrl.Result, error) {
	if hcp.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(hcp, hcpAWSPrivateLinkFinalizerName) {
		return ctrl.Result{}, nil
	}

	// If the AWSEndpointService itself is being deleted, let the existing CR deletion
	// path (which returns early at the top of Reconcile) handle cleanup. This prevents
	// the two deletion paths from racing under MaxConcurrentReconciles > 1, where the
	// CR deletion path and HCP deletion path could both try to clean up the same CR.
	if !awsEndpointService.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	log.Info("HCP is being deleted, cleaning up AWS PrivateLink resources before removing HCP finalizer")

	// Clean up this AWSEndpointService's AWS resources.
	// Client initialization is inside the finalizer check so that already-cleaned-up
	// CRs (no finalizer) proceed straight to the pending-CRs check without needing
	// AWS API calls. This prevents transient STS/credential issues from blocking
	// HCP finalizer removal when all CRs are already cleaned up.
	if controllerutil.ContainsFinalizer(awsEndpointService, finalizer) {
		r.awsClientBuilder.initializeWithHCP(log, hcp)
		ec2Client, route53Client, err := r.awsClientBuilder.getClients(ctx)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get AWS clients during HCP deletion cleanup: %w", err)
		}

		completed, err := r.delete(ctx, awsEndpointService, ec2Client, route53Client)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to clean up AWS resources during HCP deletion: %w", err)
		}
		if !completed {
			return ctrl.Result{RequeueAfter: endpointServiceDeletionRequeueDuration}, nil
		}

		originalAES := awsEndpointService.DeepCopy()
		controllerutil.RemoveFinalizer(awsEndpointService, finalizer)
		if err := r.Patch(ctx, awsEndpointService, client.MergeFromWithOptions(originalAES, client.MergeFromWithOptimisticLock{})); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{RequeueAfter: time.Second}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to remove AWSEndpointService finalizer during HCP deletion: %w", err)
		}
		log.Info("Removed AWSEndpointService finalizer after AWS resource cleanup")
	}

	// Check if all AWSEndpointService CRs in this namespace have been cleaned up.
	// Only remove the HCP finalizer once all CRs no longer have our CR finalizer,
	// meaning all AWS resources have been cleaned up.
	// Note: the CR finalizer is only added after serviceName is set (see Reconcile),
	// so CRs that were never populated by hypershift-operator won't block this check.
	awsEndpointServiceList := &hyperv1.AWSEndpointServiceList{}
	if err := r.List(ctx, awsEndpointServiceList, client.InNamespace(hcp.Namespace)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list AWSEndpointService resources: %w", err)
	}
	for i := range awsEndpointServiceList.Items {
		aes := &awsEndpointServiceList.Items[i]
		if aes.Name == awsEndpointService.Name {
			// Skip self: the informer cache may not yet reflect our Patch that just
			// removed the CR finalizer, causing a spurious extra requeue.
			continue
		}
		if controllerutil.ContainsFinalizer(aes, finalizer) {
			log.Info("Other AWSEndpointService resources still have finalizers, keeping HCP finalizer",
				"pendingResource", aes.Name)
			return ctrl.Result{RequeueAfter: endpointServiceDeletionRequeueDuration}, nil
		}
	}

	// All AWSEndpointService resources are cleaned up — remove the HCP finalizer
	// to unblock HCP deletion.
	log.Info("All AWS PrivateLink resources cleaned up, removing HCP finalizer")
	originalHCP := hcp.DeepCopy()
	controllerutil.RemoveFinalizer(hcp, hcpAWSPrivateLinkFinalizerName)
	if err := r.Patch(ctx, hcp, client.MergeFromWithOptions(originalHCP, client.MergeFromWithOptimisticLock{})); err != nil {
		if apierrors.IsConflict(err) {
			// Use RequeueAfter to avoid a tight retry loop when multiple AWSEndpointService
			// reconcilers concurrently try to patch the same HCP.
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to remove HCP finalizer: %w", err)
	}

	return ctrl.Result{}, nil
}

func hasAWSConfig(platform *hyperv1.PlatformSpec) bool {
	return platform.Type == hyperv1.AWSPlatform && platform.AWS != nil && platform.AWS.CloudProviderConfig != nil &&
		platform.AWS.CloudProviderConfig.Subnet != nil && platform.AWS.CloudProviderConfig.Subnet.ID != nil
}

func diffIDs(desired []string, existing []string) (added, removed []string) {
	var found bool
	for _, desiredID := range desired {
		found = false
		for _, existingID := range existing {
			if desiredID == existingID {
				found = true
				break
			}
		}
		if !found {
			added = append(added, desiredID)
		}
	}
	for _, existingID := range existing {
		found = false
		for _, desiredID := range desired {
			if desiredID == existingID {
				found = true
				break
			}
		}
		if !found {
			removed = append(removed, existingID)
		}
	}
	return
}

// deduplicateSubnetsByAZ ensures at most one subnet per AZ is passed to
// CreateVpcEndpoint/ModifyVpcEndpoint, since AWS rejects requests with
// multiple subnets in the same AZ.
func (r *AWSEndpointServiceReconciler) deduplicateSubnetsByAZ(ctx context.Context, ec2Client awsapi.EC2API, subnetIDs []string) ([]string, error) {
	if len(subnetIDs) <= 1 {
		return subnetIDs, nil
	}

	// Read-only path: all subnets already cached, no AWS call needed.
	r.subnetAZMu.RLock()
	allCached := r.subnetAZCache != nil
	if allCached {
		for _, id := range subnetIDs {
			if _, ok := r.subnetAZCache[id]; !ok {
				allCached = false
				break
			}
		}
	}
	if allCached {
		azForSubnet := make(map[string]string, len(subnetIDs))
		for _, id := range subnetIDs {
			azForSubnet[id] = r.subnetAZCache[id]
		}
		r.subnetAZMu.RUnlock()
		return pickOneSubnetPerAZ(subnetIDs, azForSubnet), nil
	}
	r.subnetAZMu.RUnlock()

	// Write path: new subnets found, call DescribeSubnets to populate cache.
	r.subnetAZMu.Lock()
	if r.subnetAZCache == nil {
		r.subnetAZCache = make(map[string]string)
	}

	// Re-check which subnets are uncached — between the RUnlock and Lock above,
	// another reconcile may have already fetched them.
	var uncached []string
	for _, id := range subnetIDs {
		if _, ok := r.subnetAZCache[id]; !ok {
			uncached = append(uncached, id)
		}
	}

	if len(uncached) > 0 {
		output, err := ec2Client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
			SubnetIds: uncached,
		})
		if err != nil {
			r.subnetAZMu.Unlock()
			return nil, fmt.Errorf("failed to describe subnets for AZ deduplication: %w", err)
		}
		for _, subnet := range output.Subnets {
			r.subnetAZCache[aws.ToString(subnet.SubnetId)] = aws.ToString(subnet.AvailabilityZone)
		}
	}

	azForSubnet := make(map[string]string, len(subnetIDs))
	for _, id := range subnetIDs {
		azForSubnet[id] = r.subnetAZCache[id]
	}
	r.subnetAZMu.Unlock()

	return pickOneSubnetPerAZ(subnetIDs, azForSubnet), nil
}

func pickOneSubnetPerAZ(subnetIDs []string, azForSubnet map[string]string) []string {
	sorted := make([]string, len(subnetIDs))
	copy(sorted, subnetIDs)
	sort.Strings(sorted)

	azToSubnet := make(map[string]string)
	for _, id := range sorted {
		az := azForSubnet[id]
		if _, exists := azToSubnet[az]; !exists {
			azToSubnet[az] = id
		}
	}

	deduped := make([]string, 0, len(azToSubnet))
	for _, id := range azToSubnet {
		deduped = append(deduped, id)
	}
	sort.Strings(deduped)
	return deduped
}

func (r *AWSEndpointServiceReconciler) reconcileAWSEndpointService(ctx context.Context, awsEndpointService *hyperv1.AWSEndpointService, hcp *hyperv1.HostedControlPlane, ec2Client awsapi.EC2API, route53Client awsapi.ROUTE53API) error {
	log, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("logger not found: %w", err)
	}

	if len(awsEndpointService.Status.EndpointServiceName) == 0 {
		log.Info("endpoint service name is not set, ignoring", "name", awsEndpointService.Name)
		return nil
	}

	if err := r.reconcileAWSEndpointSecurityGroup(ctx, ec2Client, awsEndpointService, hcp); err != nil {
		return err
	}

	endpointID, endpointDNSEntries, err := r.ensureVPCEndpoint(ctx, ec2Client, awsEndpointService, hcp, log)
	if err != nil {
		return err
	}

	if len(endpointDNSEntries) == 0 {
		log.Info("endpoint has no DNS entries, skipping DNS record creation", "endpointID", endpointID)
		return nil
	}

	fqdns, zoneID, err := r.reconcileEndpointDNSRecords(ctx, route53Client, awsEndpointService, hcp, endpointDNSEntries, log)
	if err != nil {
		return err
	}

	awsEndpointService.Status.DNSNames = fqdns
	awsEndpointService.Status.DNSZoneID = zoneID

	return r.reconcileExternalNameServices(ctx, hcp, endpointDNSEntries, log)
}

func (r *AWSEndpointServiceReconciler) ensureVPCEndpoint(ctx context.Context, ec2Client awsapi.EC2API, awsEndpointService *hyperv1.AWSEndpointService, hcp *hyperv1.HostedControlPlane, log logr.Logger) (string, []ec2types.DnsEntry, error) {
	deduped, err := r.deduplicateSubnetsByAZ(ctx, ec2Client, awsEndpointService.Spec.SubnetIDs)
	if err != nil {
		log.Error(err, "failed to deduplicate subnets by AZ, proceeding with original list")
	} else {
		awsEndpointService.Spec.SubnetIDs = deduped
	}

	endpointID := awsEndpointService.Status.EndpointID
	if endpointID != "" {
		return r.reconcileExistingEndpoint(ctx, ec2Client, awsEndpointService, endpointID, log)
	}
	return r.reconcileNewEndpoint(ctx, ec2Client, awsEndpointService, hcp, log)
}

func (r *AWSEndpointServiceReconciler) reconcileExistingEndpoint(ctx context.Context, ec2Client awsapi.EC2API, awsEndpointService *hyperv1.AWSEndpointService, endpointID string, log logr.Logger) (string, []ec2types.DnsEntry, error) {
	output, err := ec2Client.DescribeVpcEndpoints(ctx, &ec2.DescribeVpcEndpointsInput{
		VpcEndpointIds: []string{endpointID},
	})
	if err != nil {
		log.Error(err, "failed to describe vpc endpoint", "endpointID", endpointID)
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			if apiErr.ErrorCode() == "InvalidVpcEndpointId.NotFound" {
				awsEndpointService.Status.EndpointID = ""
				return "", nil, fmt.Errorf("endpoint with id %s not found, resetting status", endpointID)
			} else {
				return "", nil, errors.New(apiErr.ErrorCode())
			}
		}
		return "", nil, err
	}

	if len(output.VpcEndpoints) == 0 {
		awsEndpointService.Status.EndpointID = ""
		return "", nil, fmt.Errorf("endpoint with id %s not found, resetting status", endpointID)
	}

	if err := deleteEndpointIfWrongService(ctx, ec2Client, output.VpcEndpoints[0], awsEndpointService.Status.EndpointServiceName, log); err != nil {
		return "", nil, err
	}

	log.Info("endpoint exists", "endpointID", endpointID)

	if err := modifyEndpointIfNeeded(ctx, ec2Client, awsEndpointService, output.VpcEndpoints[0], endpointID, log); err != nil {
		return "", nil, err
	}

	return endpointID, output.VpcEndpoints[0].DnsEntries, nil
}

func deleteEndpointIfWrongService(ctx context.Context, ec2Client awsapi.EC2API, endpoint ec2types.VpcEndpoint, expectedServiceName string, log logr.Logger) error {
	if aws.ToString(endpoint.ServiceName) == expectedServiceName {
		return nil
	}
	log.Info("endpoint links to wrong endpointservice, deleting...", "LinkedVPCEndpointServiceName", aws.ToString(endpoint.ServiceName), "WantedVPCEndpointService", expectedServiceName)
	if _, err := ec2Client.DeleteVpcEndpoints(ctx, &ec2.DeleteVpcEndpointsInput{
		VpcEndpointIds: []string{aws.ToString(endpoint.VpcEndpointId)},
	}); err != nil {
		log.Error(err, "failed to delete vpc endpoint", "id", aws.ToString(endpoint.VpcEndpointId))
		return fmt.Errorf("error deleting AWSEndpoint: %w", err)
	}
	return fmt.Errorf("current endpoint %s is not pointing to the existing .Status.EndpointServiceName, reconciling by deleting endpoint", aws.ToString(endpoint.ServiceName))
}

func modifyEndpointIfNeeded(ctx context.Context, ec2Client awsapi.EC2API, awsEndpointService *hyperv1.AWSEndpointService, endpoint ec2types.VpcEndpoint, endpointID string, log logr.Logger) error {
	// Ensure endpoint has the right subnets.
	addedSubnet, removedSubnet := diffIDs(awsEndpointService.Spec.SubnetIDs, endpoint.SubnetIds)

	// Ensure endpoint has the right SG.
	existingSG := make([]string, 0)
	for _, group := range endpoint.Groups {
		existingSG = append(existingSG, aws.ToString(group.GroupId))
	}
	addedSG, _ := diffIDs([]string{awsEndpointService.Status.SecurityGroupID}, existingSG)

	if addedSubnet == nil && removedSubnet == nil && addedSG == nil {
		log.Info("endpoint subnets are unchanged")
		return nil
	}

	log.Info("endpoint subnets or security groups have changed")
	_, err := ec2Client.ModifyVpcEndpoint(ctx, &ec2.ModifyVpcEndpointInput{
		VpcEndpointId:       aws.String(endpointID),
		AddSubnetIds:        addedSubnet,
		RemoveSubnetIds:     removedSubnet,
		AddSecurityGroupIds: addedSG,
	})
	if err != nil {
		log.Error(err, "failed to modify vpc endpoint", "id", endpointID, "addSubnets", addedSubnet, "removeSubnets", removedSubnet, "addSG", addedSG)
		msg := err.Error()
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			msg = apiErr.ErrorCode()
		}
		log.Error(err, "failed to modify vpc endpoint")
		return fmt.Errorf("failed to modify vpc endpoint: %s", msg)
	}
	log.Info("endpoint subnets updated")
	return nil
}

func (r *AWSEndpointServiceReconciler) reconcileNewEndpoint(ctx context.Context, ec2Client awsapi.EC2API, awsEndpointService *hyperv1.AWSEndpointService, hcp *hyperv1.HostedControlPlane, log logr.Logger) (string, []ec2types.DnsEntry, error) {
	if !hasAWSConfig(&hcp.Spec.Platform) {
		return "", nil, fmt.Errorf("AWS platform information not provided in HostedControlPlane")
	}

	output, err := ec2Client.DescribeVpcEndpoints(ctx, &ec2.DescribeVpcEndpointsInput{
		Filters: apiTagToEC2Filter(awsEndpointService.Name, hcp.Spec.Platform.AWS.ResourceTags),
	})
	if err != nil {
		msg := err.Error()
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			msg = apiErr.ErrorCode()
		}
		log.Error(err, "failed to describe vpc endpoints")
		return "", nil, fmt.Errorf("failed to describe vpc endpoints: %s", msg)
	}

	if len(output.VpcEndpoints) != 0 {
		if err := deleteEndpointIfWrongService(ctx, ec2Client, output.VpcEndpoints[0], awsEndpointService.Status.EndpointServiceName, log); err != nil {
			return "", nil, err
		}
		endpointID := aws.ToString(output.VpcEndpoints[0].VpcEndpointId)
		log.Info("endpoint already exists, adopting", "endpointID", endpointID)
		awsEndpointService.Status.EndpointID = endpointID
		return endpointID, output.VpcEndpoints[0].DnsEntries, nil
	}

	return r.createVPCEndpoint(ctx, ec2Client, awsEndpointService, hcp, log)
}

func (r *AWSEndpointServiceReconciler) createVPCEndpoint(ctx context.Context, ec2Client awsapi.EC2API, awsEndpointService *hyperv1.AWSEndpointService, hcp *hyperv1.HostedControlPlane, log logr.Logger) (string, []ec2types.DnsEntry, error) {
	log.Info("endpoint does not already exist")

	if awsEndpointService.Status.SecurityGroupID == "" {
		return "", nil, fmt.Errorf("security group ID doesn't exist yet for the endpoint to use")
	}
	output, err := ec2Client.CreateVpcEndpoint(ctx, &ec2.CreateVpcEndpointInput{
		SecurityGroupIds: []string{awsEndpointService.Status.SecurityGroupID},
		ServiceName:      aws.String(awsEndpointService.Status.EndpointServiceName),
		VpcId:            aws.String(hcp.Spec.Platform.AWS.CloudProviderConfig.VPC),
		VpcEndpointType:  ec2types.VpcEndpointTypeInterface,
		SubnetIds:        awsEndpointService.Spec.SubnetIDs,
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeVpcEndpoint,
			Tags:         apiTagToEC2Tag(awsEndpointService.Name, hcp.Spec.Platform.AWS.ResourceTags),
		}},
	})
	if err != nil {
		msg := err.Error()
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			msg = apiErr.ErrorCode()
		}
		log.Error(err, "failed to create vpc endpoint")
		return "", nil, fmt.Errorf("failed to create vpc endpoint: %s", msg)
	}
	if output == nil || output.VpcEndpoint == nil {
		return "", nil, fmt.Errorf("CreateVpcEndpoint output is nil")
	}

	endpointID := aws.ToString(output.VpcEndpoint.VpcEndpointId)
	log.Info("endpoint created", "endpointID", endpointID)
	awsEndpointService.Status.EndpointID = endpointID
	return endpointID, output.VpcEndpoint.DnsEntries, nil
}

func (r *AWSEndpointServiceReconciler) reconcileEndpointDNSRecords(ctx context.Context, route53Client awsapi.ROUTE53API, awsEndpointService *hyperv1.AWSEndpointService, hcp *hyperv1.HostedControlPlane, endpointDNSEntries []ec2types.DnsEntry, log logr.Logger) ([]string, string, error) {
	recordNames := recordsForService(awsEndpointService, hcp)
	if len(recordNames) == 0 {
		log.Info("WARNING: no mapping from AWSEndpointService to DNS")
		return nil, "", nil
	}

	zn := zoneName(hcp.Name)
	var zoneID string
	if localZoneID := r.awsClientBuilder.getLocalHostedZoneID(); localZoneID != "" {
		zoneID = localZoneID
	} else if awsEndpointService.Status.DNSZoneID != "" {
		zoneID = awsEndpointService.Status.DNSZoneID
		r.awsClientBuilder.setLocalHostedZoneID(zoneID)
		log.Info("using DNSZoneID from status", "zoneID", zoneID)
	} else {
		var err error
		zoneID, err = lookupZoneID(ctx, route53Client, zn)
		if err != nil {
			return nil, "", err
		}
		r.awsClientBuilder.setLocalHostedZoneID(zoneID)
	}

	var fqdns []string
	for _, recordName := range recordNames {
		fqdn := fmt.Sprintf("%s.%s", recordName, zn)
		fqdns = append(fqdns, fqdn)
		err := CreateRecord(ctx, route53Client, zoneID, fqdn, aws.ToString(endpointDNSEntries[0].DnsName), route53types.RRTypeCname)
		if err != nil {
			var noSuchZone *route53types.NoSuchHostedZone
			if errors.As(err, &noSuchZone) {
				r.awsClientBuilder.setLocalHostedZoneID("")
				awsEndpointService.Status.DNSZoneID = ""
				log.Info("hosted zone not found, clearing cached DNSZoneID", "zoneID", zoneID)
			}
			return nil, "", err
		}
		log.Info("DNS record created", "fqdn", fqdn)
	}

	return fqdns, zoneID, nil
}

func (r *AWSEndpointServiceReconciler) reconcileExternalNameServices(ctx context.Context, hcp *hyperv1.HostedControlPlane, endpointDNSEntries []ec2types.DnsEntry, log logr.Logger) error {
	isPublic := netutil.IsPublicHCP(hcp)
	externalNames := hcpExternalNames(hcp)

	// only if not public and external names are configured, create services of type ExternalName so external-dns
	// can create records for them
	if !isPublic && len(externalNames) > 0 {
		var errs []error
		for svcType, externalName := range externalNames {
			var svc *corev1.Service
			switch svcType {
			case "api":
				svc = manifests.KubeAPIServerExternalPrivateService(hcp.Namespace)
			case "oauth":
				svc = manifests.OauthServerExternalPrivateService(hcp.Namespace)
			}
			if _, err := r.CreateOrUpdate(ctx, r, svc, func() error {
				log.Info("Reconciling external name service", "service", svc.Name, "externalName", externalName)
				return reconcileExternalService(svc, hcp, externalName, aws.ToString(endpointDNSEntries[0].DnsName))
			}); err != nil {
				errs = append(errs, fmt.Errorf("failed to reconcile %s external service: %w", svcType, err))
			}
		}
		if len(errs) > 0 {
			return fmt.Errorf("failed to create external services for private endpoints: %w", utilerrors.NewAggregate(errs))
		}
		return nil
	}

	// if the cluster is public, ensure that any ExternalName services are removed
	privateExternalServices := &corev1.ServiceList{}
	if err := r.List(ctx, privateExternalServices, client.InNamespace(hcp.Namespace), client.HasLabels{externalPrivateServiceLabel}); err != nil {
		return fmt.Errorf("cannot list private external services: %w", err)
	}
	if len(privateExternalServices.Items) > 0 {
		log.Info("Removing private external services", "count", len(privateExternalServices.Items))
		var errs []error
		for i := range privateExternalServices.Items {
			svc := &privateExternalServices.Items[i]
			if err := r.Delete(ctx, svc); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete private external service %s: %w", svc.Name, err))
			}
		}
		if len(errs) > 0 {
			return utilerrors.NewAggregate(errs)
		}
	}
	return nil
}

func (r *AWSEndpointServiceReconciler) reconcileAWSEndpointSecurityGroup(ctx context.Context, ec2Client awsapi.EC2API, awsEndpointService *hyperv1.AWSEndpointService, hcp *hyperv1.HostedControlPlane) error {
	var sgID string
	var sg *ec2types.SecurityGroup

	log := ctrl.LoggerFrom(ctx)
	var err error
	sg, err = supportawsutil.GetSecurityGroup(ctx, ec2Client, vpcEndpointSecurityGroupFilter(hcp.Spec.InfraID, awsEndpointService.Name))
	if err != nil {
		log.Error(err, "failed to get security group for endpoint", "infraID", hcp.Spec.InfraID, "name", awsEndpointService.Name)
		return err
	}
	if sg != nil {
		sgID = aws.ToString(sg.GroupId)
		if awsEndpointService.Status.SecurityGroupID != sgID {
			log.Info("WARNING: found existing security group, but it doesn't match status id, updating", "id", sgID)
			awsEndpointService.Status.SecurityGroupID = sgID
		}
	}
	if sgID == "" {
		var err error
		if sg, err = r.createSecurityGroup(ctx, ec2Client, awsEndpointService, hcp); err != nil {
			return err
		}
		sgID = aws.ToString(sg.GroupId)
		awsEndpointService.Status.SecurityGroupID = sgID
	}
	machineCIDRs := make([]string, len(hcp.Spec.Networking.MachineNetwork))
	for i, mNet := range hcp.Spec.Networking.MachineNetwork {
		machineCIDRs[i] = mNet.CIDR.String()
	}
	ingressPermissions := supportawsutil.VPCEndpointSecurityGroupRules(machineCIDRs, vpcEndpointPort(awsEndpointService))
	missingPermissions := diffPermissions(sg.IpPermissions, ingressPermissions)
	if len(missingPermissions) > 0 {
		if _, err = ec2Client.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
			GroupId:       aws.String(sgID),
			IpPermissions: missingPermissions,
		}); err != nil {
			if supportawsutil.AWSErrorCode(err) != "InvalidPermission.Duplicate" {
				log.Error(err, "failed to set security group ingress rules", "id", sgID)
				return fmt.Errorf("failed to set security group ingress rules, code: %s", supportawsutil.AWSErrorCode(err))
			}
			log.Info("WARNING: got duplicate permissions error when setting security group ingress permissions", "sgID", sgID)
		}
		log.Info("authorized security group rules", "id", sgID)
	} else {
		log.Info("security group already has required permissions", "id", sgID)
	}
	return nil
}

func vpcEndpointPort(awsEndpointService *hyperv1.AWSEndpointService) int32 {
	switch awsEndpointService.Name {
	case manifests.KubeAPIServerPrivateService("").Name:
		return 6443
	default:
		return 443
	}
}

func (r *AWSEndpointServiceReconciler) createSecurityGroup(ctx context.Context, ec2Client awsapi.EC2API, awsEndpointService *hyperv1.AWSEndpointService, hcp *hyperv1.HostedControlPlane) (*ec2types.SecurityGroup, error) {
	log := ctrl.LoggerFrom(ctx)
	tagKeys := sets.NewString()
	var tags []ec2types.Tag
	for _, tag := range hcp.Spec.Platform.AWS.ResourceTags {
		tagKeys.Insert(tag.Key)
		tags = append(tags, ec2types.Tag{
			Key:   aws.String(tag.Key),
			Value: aws.String(tag.Value),
		})
	}
	clusterKey := fmt.Sprintf("kubernetes.io/cluster/%s", hcp.Spec.InfraID)
	if !tagKeys.Has(clusterKey) {
		tags = append(tags, ec2types.Tag{
			Key:   aws.String(clusterKey),
			Value: aws.String("owned"),
		})
	}
	name := vpcEndpointSecurityGroupName(hcp.Spec.InfraID, awsEndpointService.Name)
	if !tagKeys.Has("Name") {
		tags = append(tags, ec2types.Tag{
			Key:   aws.String("Name"),
			Value: aws.String(name),
		})
	}
	createSGResult, err := ec2Client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String(name),
		Description: aws.String("VPC endpoint security group"),
		VpcId:       aws.String(hcp.Spec.Platform.AWS.CloudProviderConfig.VPC),
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeSecurityGroup,
				Tags:         tags,
			},
		},
	})
	if err != nil {
		log.Error(err, "failed to create security group for aws endpoint", "name", name, "vpc", hcp.Spec.Platform.AWS.CloudProviderConfig.VPC)
		return nil, fmt.Errorf("failed to create security group, code: %s", supportawsutil.AWSErrorCode(err))
	}
	sgID := aws.ToString(createSGResult.GroupId)

	// Fetch just-created SG using the SecurityGroupExistsWaiter
	waiter := ec2.NewSecurityGroupExistsWaiter(ec2Client)
	if err = waiter.Wait(ctx, &ec2.DescribeSecurityGroupsInput{
		GroupIds: []string{sgID},
	}, 30*time.Second); err != nil {
		log.Error(err, "failed to wait for security group to exist", "id", sgID)
		return nil, fmt.Errorf("failed to wait for security group to exist (id: %s), code: %s", sgID, supportawsutil.AWSErrorCode(err))
	}
	sg, err := supportawsutil.GetSecurityGroupById(ctx, ec2Client, sgID)
	if err != nil {
		log.Error(err, "failed to fetch security group by ID", "id", sgID)
		return nil, err
	}
	if sg == nil {
		log.Error(errors.New("security group not found"), "id", sgID)
		return nil, fmt.Errorf("failed to fetch security group (id: %s)", sgID)
	}
	log.Info("created security group", "id", sgID)
	return sg, nil
}

func vpcEndpointSecurityGroupFilter(infraID, endpointName string) []ec2types.Filter {
	return []ec2types.Filter{
		{
			Name:   aws.String(fmt.Sprintf("tag:kubernetes.io/cluster/%s", infraID)),
			Values: []string{"owned"},
		},
		{
			Name:   aws.String("tag:Name"),
			Values: []string{vpcEndpointSecurityGroupName(infraID, endpointName)},
		},
	}
}

func vpcEndpointSecurityGroupName(infraID, endpointName string) string {
	return fmt.Sprintf("%s-vpce-%s", infraID, endpointName)
}

func reconcileExternalService(svc *corev1.Service, hcp *hyperv1.HostedControlPlane, hostName, targetCName string) error {
	ownerRef := config.OwnerRefFrom(hcp)
	ownerRef.ApplyTo(svc)
	if svc.Labels == nil {
		svc.Labels = map[string]string{}
	}
	if svc.Annotations == nil {
		svc.Annotations = map[string]string{}
	}
	svc.Labels[externalPrivateServiceLabel] = "true"
	svc.Annotations[hyperv1.ExternalDNSHostnameAnnotation] = hostName
	svc.Spec.Type = corev1.ServiceTypeExternalName
	svc.Spec.ExternalName = targetCName
	return nil
}

func hcpExternalNames(hcp *hyperv1.HostedControlPlane) map[string]string {
	result := map[string]string{}
	apiStrategy := netutil.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.APIServer)
	if apiStrategy != nil && apiStrategy.Type == hyperv1.Route && apiStrategy.Route != nil && apiStrategy.Route.Hostname != "" {
		result["api"] = apiStrategy.Route.Hostname
	}

	oauthStrategy := netutil.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.OAuthServer)
	if oauthStrategy != nil && oauthStrategy.Type == hyperv1.Route && oauthStrategy.Route != nil && oauthStrategy.Route.Hostname != "" {
		result["oauth"] = oauthStrategy.Route.Hostname
	}
	return result
}

func zoneName(hcpName string) string {
	return fmt.Sprintf("%s.%s", hcpName, hypershiftLocalZone)
}

func RouterZoneName(hcpName string) string {
	return routerDomain + "." + zoneName(hcpName)
}

func recordsForService(awsEndpointService *hyperv1.AWSEndpointService, hcp *hyperv1.HostedControlPlane) []string {
	if awsEndpointService.Name == manifests.KubeAPIServerPrivateService("").Name {
		return []string{"api"}

	}
	if awsEndpointService.Name != manifests.PrivateRouterService("").Name {
		return nil
	}

	// If the kas is exposed through a route, the router needs to have DNS entries for both
	// the kas and the apps domain
	if m := netutil.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.APIServer); m != nil && m.Type == hyperv1.Route {
		return []string{"api", "*." + routerDomain}
	}

	return []string{"*.apps"}

}

func apiTagToEC2Tag(name string, in []hyperv1.AWSResourceTag) []ec2types.Tag {
	result := make([]ec2types.Tag, 0, len(in)+1)
	for _, val := range in {
		result = append(result, ec2types.Tag{Key: aws.String(val.Key), Value: aws.String(val.Value)})
	}
	result = append(result, ec2types.Tag{Key: aws.String("AWSEndpointService"), Value: aws.String(name)})

	return result
}

func apiTagToEC2Filter(name string, in []hyperv1.AWSResourceTag) []ec2types.Filter {
	result := make([]ec2types.Filter, 0, len(in)+1)
	for _, val := range in {
		result = append(result, ec2types.Filter{Name: aws.String("tag:" + val.Key), Values: []string{val.Value}})
	}
	result = append(result, ec2types.Filter{Name: aws.String("tag:AWSEndpointService"), Values: []string{name}})

	return result
}

func (r *AWSEndpointServiceReconciler) delete(ctx context.Context, awsEndpointService *hyperv1.AWSEndpointService, ec2Client awsapi.EC2API, route53Client awsapi.ROUTE53API) (bool, error) {
	log, err := logr.FromContext(ctx)
	if err != nil {
		return false, fmt.Errorf("logger not found: %w", err)
	}

	endpointID := awsEndpointService.Status.EndpointID
	if endpointID != "" {
		if _, err := ec2Client.DeleteVpcEndpoints(ctx, &ec2.DeleteVpcEndpointsInput{
			VpcEndpointIds: []string{endpointID},
		}); err != nil {
			return false, err
		}

		// check if Endpoint exists in AWS
		output, err := ec2Client.DescribeVpcEndpoints(ctx, &ec2.DescribeVpcEndpointsInput{
			VpcEndpointIds: []string{endpointID},
		})
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) {
				if apiErr.ErrorCode() != "InvalidVpcEndpointId.NotFound" {
					return false, err
				}
			} else {
				return false, err
			}

		}

		if output != nil && len(output.VpcEndpoints) != 0 {
			// Once the VPC Endpoint is deleted, we need to return an error to reexecute the reconciliation
			return false, fmt.Errorf("resource requested for deletion but still present")
		}

		log.Info("endpoint deleted", "endpointID", endpointID)
	}

	if awsEndpointService.Status.SecurityGroupID != "" {
		if err := r.deleteSecurityGroup(ctx, ec2Client, awsEndpointService.Status.SecurityGroupID); err != nil {
			if errors.Is(err, errDependencyViolation) {
				log.Info("security group has dependencies, will retry", "id", awsEndpointService.Status.SecurityGroupID)
				return false, nil
			}
			return false, err
		}
		log.Info("security group deleted", "id", awsEndpointService.Status.SecurityGroupID)
	}

	zoneID := awsEndpointService.Status.DNSZoneID

	for _, fqdn := range awsEndpointService.Status.DNSNames {
		if fqdn != "" && zoneID != "" {
			record, err := FindRecord(ctx, route53Client, zoneID, fqdn, route53types.RRTypeCname)
			if err != nil {
				var noSuchZone *route53types.NoSuchHostedZone
				if errors.As(err, &noSuchZone) {
					log.Info("Hosted Zone not found", "hostedzone", zoneID)
					return true, nil
				}

				return false, err
			}
			if record != nil {
				err = DeleteRecord(ctx, route53Client, zoneID, record)
				if err != nil {
					return false, err
				}
				log.Info("DNS record deleted", "fqdn", fqdn)
			} else {
				log.Info("no DNS record found", "fqdn", fqdn)
			}
		}
	}

	return true, nil
}

func (r *AWSEndpointServiceReconciler) deleteSecurityGroup(ctx context.Context, ec2Client awsapi.EC2API, sgID string) error {
	describeSGResult, err := ec2Client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: []string{sgID}})
	if err != nil {
		if supportawsutil.AWSErrorCode(err) == "InvalidGroup.NotFound" {
			return nil
		}
		return fmt.Errorf("cannot describe security group: %s", supportawsutil.AWSErrorCode(err))
	}
	if len(describeSGResult.SecurityGroups) == 0 {
		return nil
	}
	sg := describeSGResult.SecurityGroups[0]

	if len(sg.IpPermissions) > 0 {
		if _, err = ec2Client.RevokeSecurityGroupIngress(ctx, &ec2.RevokeSecurityGroupIngressInput{
			GroupId:       sg.GroupId,
			IpPermissions: sg.IpPermissions,
		}); err != nil {
			if supportawsutil.AWSErrorCode(err) == supportawsutil.DependencyViolation {
				return fmt.Errorf("%w: %w", errDependencyViolation, err)
			}
			return fmt.Errorf("failed to revoke security group %s ingress rules: %w", aws.ToString(sg.GroupId), err)
		}
	}

	if len(sg.IpPermissionsEgress) > 0 {
		if _, err = ec2Client.RevokeSecurityGroupEgress(ctx, &ec2.RevokeSecurityGroupEgressInput{
			GroupId:       sg.GroupId,
			IpPermissions: sg.IpPermissionsEgress,
		}); err != nil {
			if supportawsutil.AWSErrorCode(err) == supportawsutil.DependencyViolation {
				return fmt.Errorf("%w: %w", errDependencyViolation, err)
			}
			return fmt.Errorf("failed to revoke security group %s egress rules: %w", aws.ToString(sg.GroupId), err)
		}
	}

	if _, err = ec2Client.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{
		GroupId: sg.GroupId,
	}); err != nil {
		if supportawsutil.AWSErrorCode(err) == supportawsutil.DependencyViolation {
			return fmt.Errorf("%w: %w", errDependencyViolation, err)
		}
		return fmt.Errorf("failed to delete security group %s: %w", aws.ToString(sg.GroupId), err)
	}

	return nil
}

func diffPermissions(actual, required []ec2types.IpPermission) []ec2types.IpPermission {
	var result []ec2types.IpPermission
	for _, req := range required {
		if !isPermissionPresent(req, actual) {
			result = append(result, req)
		}
	}
	return result
}

func isPermissionPresent(perm ec2types.IpPermission, list []ec2types.IpPermission) bool {
	for _, existing := range list {
		if aws.ToInt32(existing.FromPort) == aws.ToInt32(perm.FromPort) &&
			aws.ToInt32(existing.ToPort) == aws.ToInt32(perm.ToPort) &&
			aws.ToString(existing.IpProtocol) == aws.ToString(perm.IpProtocol) &&
			equalIPRanges(existing.IpRanges, perm.IpRanges) {
			return true
		}
	}
	return false
}

func equalIPRanges(a, b []ec2types.IpRange) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if aws.ToString(a[i].Description) != aws.ToString(b[i].Description) ||
			aws.ToString(a[i].CidrIp) != aws.ToString(b[i].CidrIp) {
			return false
		}
	}
	return true
}
