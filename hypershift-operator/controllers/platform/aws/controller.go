package aws

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/upsert"
	supportutil "github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/arn"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/aws/aws-sdk-go/service/elbv2/elbv2iface"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
)

const (
	finalizer                              = "hypershift.openshift.io/hypershift-operator-finalizer"
	endpointServiceDeletionRequeueDuration = 5 * time.Second
	lbNotActiveRequeueDuration             = 20 * time.Second
)

// AWSEndpointServiceReconciler watches HC/NodePools/awsEndpointService and reconcile the awsEndpointService
// CRs existing for the KubeAPIServerPrivateService and the PrivateRouterService.
// It creates the endpoint service in AWS and keeps the SubnetIDs up to date so NodePools are able to attach to the service endpoint.
type AWSEndpointServiceReconciler struct {
	client.Client
	upsert.CreateOrUpdateProvider
	ec2Client   ec2iface.EC2API
	elbv2Client elbv2iface.ELBV2API
}

func awsEndpointServicesByName(ns string) []reconcile.Request {
	// This is a pretty fragile but without a client or context with which to list the
	// AWSEndpointServices and no way to return and error from here, hardcoding the known
	// names of the potential AWSEndpointServices (won't exist if Public) is a way to do it.
	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Namespace: ns,
				Name:      manifests.KubeAPIServerPrivateService("").Name,
			},
		},
		{
			NamespacedName: types.NamespacedName{
				Namespace: ns,
				Name:      manifests.PrivateRouterService("").Name,
			},
		},
		// TODO: Remove this once initial commit is merged. Not needed for
		// current version of CPO.
		{
			NamespacedName: types.NamespacedName{
				Namespace: ns,
				Name:      fmt.Sprintf("router-%s", ns),
			},
		},
	}
}

func (r *AWSEndpointServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.AWSEndpointService{}).
		Watches(&hyperv1.NodePool{}, handler.Funcs{
			CreateFunc: r.enqueueOnNodePoolCreate(mgr),
			UpdateFunc: r.enqueueOnNodePoolChange(mgr),
			DeleteFunc: r.enqueueOnNodePoolDelete(mgr),
		}).
		Watches(&hyperv1.HostedCluster{}, handler.Funcs{UpdateFunc: r.enqueueOnHostedClusterChange(mgr)}).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](3*time.Second, 30*time.Second),
			MaxConcurrentReconciles: 10,
		}).
		Build(r)
	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager: %w", err)
	}

	// AWS_SHARED_CREDENTIALS_FILE and AWS_REGION envvar should be set in operator deployment
	awsSession := awsutil.NewSession("hypershift-operator", "", "", "", "")
	awsConfig := aws.NewConfig()
	r.ec2Client = ec2.New(awsSession, awsConfig)
	r.elbv2Client = elbv2.New(awsSession, awsConfig)

	return nil
}

func (r *AWSEndpointServiceReconciler) enqueueOnNodePoolCreate(mgr ctrl.Manager) func(context.Context, event.CreateEvent, workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	return func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		logger := mgr.GetLogger()
		np, isOk := e.Object.(*hyperv1.NodePool)
		if !isOk {
			logger.Info("WARNING: enqueueOnNodePoolCreate: resource is not of type NodePool")
			return
		}
		for _, req := range awsEndpointServicesByName(fmt.Sprintf("%s-%s", np.Namespace, np.Spec.ClusterName)) {
			q.Add(req)
		}
	}
}

func (r *AWSEndpointServiceReconciler) enqueueOnNodePoolChange(mgr ctrl.Manager) func(context.Context, event.UpdateEvent, workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	return func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		logger := mgr.GetLogger()
		newNP, isOk := e.ObjectNew.(*hyperv1.NodePool)
		if !isOk {
			logger.Info("WARNING: enqueueOnNodePoolChange: new resource is not of type NodePool")
			return
		}
		oldNP, isOk := e.ObjectOld.(*hyperv1.NodePool)
		if !isOk {
			logger.Info("WARNING: enqueueOnNodePoolChange: old resource is not of type NodePool")
			return
		}
		// Only enqueue awsendpointservices when there is a change in the subnet IDs, otherwise ignore changes
		if newNP.Spec.Platform.AWS != nil && oldNP.Spec.Platform.AWS != nil &&
			newNP.Spec.Platform.AWS.Subnet.ID != nil && oldNP.Spec.Platform.AWS.Subnet.ID != nil &&
			!equality.Semantic.DeepEqual(newNP.Spec.Platform.AWS.Subnet.ID, oldNP.Spec.Platform.AWS.Subnet.ID) {
			for _, req := range awsEndpointServicesByName(fmt.Sprintf("%s-%s", newNP.Namespace, newNP.Spec.ClusterName)) {
				q.Add(req)
			}
		}
	}
}

func (r *AWSEndpointServiceReconciler) enqueueOnNodePoolDelete(mgr ctrl.Manager) func(context.Context, event.DeleteEvent, workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	return func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		logger := mgr.GetLogger()
		np, isOk := e.Object.(*hyperv1.NodePool)
		if !isOk {
			logger.Info("WARNING: enqueueOnNodePoolDelete: resource is not of type NodePool")
			return
		}
		for _, req := range awsEndpointServicesByName(fmt.Sprintf("%s-%s", np.Namespace, np.Spec.ClusterName)) {
			q.Add(req)
		}
	}
}

func (r *AWSEndpointServiceReconciler) enqueueOnHostedClusterChange(mgr ctrl.Manager) func(context.Context, event.UpdateEvent, workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	return func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		logger := mgr.GetLogger()
		newHC, isOk := e.ObjectNew.(*hyperv1.HostedCluster)
		if !isOk {
			logger.Info("WARNING: enqueueOnHostedClusterChange: new resource is not of type HostedCluster")
			return
		}
		oldHC, isOk := e.ObjectOld.(*hyperv1.HostedCluster)
		if !isOk {
			logger.Info("WARNING: enqueueOnHostedClusterChange: old resource is not of type HostedCluster")
			return
		}
		// Only enqueue awsendpointservices when there is a change in the AdditionalAllowedPrincipals, otherwise ignore changes
		if newHC.Spec.Platform.AWS != nil && oldHC.Spec.Platform.AWS != nil &&
			!equality.Semantic.DeepEqual(newHC.Spec.Platform.AWS.AdditionalAllowedPrincipals, oldHC.Spec.Platform.AWS.AdditionalAllowedPrincipals) {
			for _, req := range awsEndpointServicesByName(fmt.Sprintf("%s-%s", newHC.Namespace, newHC.Name)) {
				q.Add(req)
			}
		}
	}
}

func (r *AWSEndpointServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log, err := logr.FromContext(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("no logger found: %w", err)
	}
	log.Info("reconciling")

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
		if !controllerutil.ContainsFinalizer(awsEndpointService, finalizer) {
			// If we previously removed our finalizer, don't delete again and return early
			return ctrl.Result{}, nil
		}
		completed, err := r.delete(ctx, awsEndpointService)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete resource: %w", err)
		}
		if !completed {
			return ctrl.Result{RequeueAfter: endpointServiceDeletionRequeueDuration}, nil
		}
		if controllerutil.ContainsFinalizer(awsEndpointService, finalizer) {
			controllerutil.RemoveFinalizer(awsEndpointService, finalizer)
			if err := r.Update(ctx, awsEndpointService); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure the awsEndpointService has a finalizer for cleanup
	if !controllerutil.ContainsFinalizer(awsEndpointService, finalizer) {
		controllerutil.AddFinalizer(awsEndpointService, finalizer)
		if err := r.Update(ctx, awsEndpointService); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
	}

	// Find the hosted control plane
	hcp, err := r.hostedControlPlane(ctx, awsEndpointService.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}
	hc, err := r.hostedCluster(ctx, hcp)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get hosted cluster: %w", err)
	}

	if isPaused, duration := supportutil.IsReconciliationPaused(log, hc.Spec.PausedUntil); isPaused {
		log.Info("Reconciliation paused", "pausedUntil", *hc.Spec.PausedUntil)
		return ctrl.Result{RequeueAfter: duration}, nil
	}

	// Reconcile the AWSEndpointService Spec
	if _, err := r.CreateOrUpdate(ctx, r.Client, awsEndpointService, func() error {
		return reconcileAWSEndpointServiceSpec(ctx, r, awsEndpointService, hc)
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile AWSEndpointService spec: %w", err)
	}

	// Reconcile the AWSEndpointService Status
	oldStatus := awsEndpointService.Status.DeepCopy()
	if err = r.reconcileAWSEndpointServiceStatus(ctx, awsEndpointService, hc, r.ec2Client, r.elbv2Client); err != nil {
		meta.SetStatusCondition(&awsEndpointService.Status.Conditions, metav1.Condition{
			Type:    string(hyperv1.AWSEndpointServiceAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.AWSErrorReason,
			Message: err.Error(),
		})

		if !equality.Semantic.DeepEqual(*oldStatus, awsEndpointService.Status) {
			if err := r.Status().Update(ctx, awsEndpointService); err != nil {
				return ctrl.Result{}, err
			}
		}
		// Most likely cause of error here is the NLB is not yet active.  This can take ~2m so
		// a longer requeue time is warranted.  This ratelimits AWS calls and updates to the CR.
		log.Info("reconciliation failed, retrying in 20s", "err", err)
		return ctrl.Result{RequeueAfter: lbNotActiveRequeueDuration}, nil
	}

	meta.SetStatusCondition(&awsEndpointService.Status.Conditions, metav1.Condition{
		Type:    string(hyperv1.AWSEndpointServiceAvailable),
		Status:  metav1.ConditionTrue,
		Reason:  hyperv1.AWSSuccessReason,
		Message: "",
	})

	if !equality.Semantic.DeepEqual(*oldStatus, awsEndpointService.Status) {
		if err := r.Status().Update(ctx, awsEndpointService); err != nil {
			return ctrl.Result{}, err
		}
	}

	log.Info("reconciliation complete")
	// always requeue to catch and report out of band changes in AWS
	// NOTICE: if the RequeueAfter interval is short enough, it could result in hitting some AWS request limits.
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

func reconcileAWSEndpointServiceSpec(ctx context.Context, c client.Client, awsEndpointService *hyperv1.AWSEndpointService, hc *hyperv1.HostedCluster) error {
	return reconcileAWSEndpointServiceSubnetIDs(ctx, c, awsEndpointService, hc)
}

func reconcileAWSEndpointServiceSubnetIDs(ctx context.Context, c client.Client, awsEndpointService *hyperv1.AWSEndpointService, hc *hyperv1.HostedCluster) error {
	subnetIDs, err := listSubnetIDs(ctx, c, hc.Name, hc.Namespace)
	if err != nil {
		return fmt.Errorf("failed to list subnetIDs: %w", err)
	}
	awsEndpointService.Spec.SubnetIDs = subnetIDs
	return nil
}

func listNodePools(ctx context.Context, c client.Client, nodePoolNamespace string, clusterName string) ([]hyperv1.NodePool, error) {
	nodePoolList := &hyperv1.NodePoolList{}
	if err := c.List(ctx, nodePoolList, &client.ListOptions{Namespace: nodePoolNamespace}); err != nil {
		return nil, fmt.Errorf("failed to list NodePools in namespace %s for cluster %s : %w", nodePoolNamespace, clusterName, err)
	}
	filtered := []hyperv1.NodePool{}
	for i, nodePool := range nodePoolList.Items {
		if nodePool.Spec.ClusterName == clusterName {
			filtered = append(filtered, nodePoolList.Items[i])
		}
	}
	return filtered, nil
}

func listSubnetIDs(ctx context.Context, c client.Client, clusterName, nodePoolNamespace string) ([]string, error) {
	nodePools, err := listNodePools(ctx, c, nodePoolNamespace, clusterName)
	if err != nil {
		return nil, err
	}
	subnetIDs := []string{}
	for _, nodePool := range nodePools {
		if nodePool.Spec.Platform.AWS != nil &&
			nodePool.Spec.Platform.AWS.Subnet.ID != nil {
			subnetIDs = append(subnetIDs, *nodePool.Spec.Platform.AWS.Subnet.ID)
		}
	}
	sort.Strings(subnetIDs)
	return subnetIDs, nil
}

func (r *AWSEndpointServiceReconciler) reconcileAWSEndpointServiceStatus(ctx context.Context, awsEndpointService *hyperv1.AWSEndpointService, hostedCluster *hyperv1.HostedCluster, ec2Client ec2iface.EC2API, elbv2Client elbv2iface.ELBV2API) error {
	log := ctrl.LoggerFrom(ctx)

	// If a previous awsendpointservice that points to an ingress controller exists, remove it
	endpointServices := &hyperv1.AWSEndpointServiceList{}
	if err := r.List(ctx, endpointServices, client.InNamespace(awsEndpointService.Namespace)); err != nil {
		return fmt.Errorf("failed to list aws endpoint services in namespace: %s: %w", awsEndpointService.Namespace, err)
	}
	privateRouterEPServiceName := fmt.Sprintf("router-%s", awsEndpointService.Namespace)
	hasPrivateRouterEPService := false
	hasPrivateIngressControllerEPService := false
	for _, eps := range endpointServices.Items {
		if eps.Name == manifests.PrivateRouterService("").Name {
			hasPrivateRouterEPService = true
		}
		if eps.Name == privateRouterEPServiceName {
			hasPrivateIngressControllerEPService = true
		}
	}
	// Only if both router and private ingress controller AWSEndpointServices exist, delete the obsolete one
	if hasPrivateRouterEPService && hasPrivateIngressControllerEPService {
		privateIngressControllerEPService := &hyperv1.AWSEndpointService{
			ObjectMeta: metav1.ObjectMeta{
				Name:      privateRouterEPServiceName,
				Namespace: awsEndpointService.Namespace,
			},
		}
		if err := r.Delete(ctx, privateIngressControllerEPService); err != nil {
			return fmt.Errorf("failed to delete awsendpointservice %s: %w", client.ObjectKeyFromObject(privateIngressControllerEPService).String(), err)
		}
		// No need to further reconcile if the endpointservice is the one we just deleted.
		if awsEndpointService.Name == privateRouterEPServiceName {
			return nil
		}
	}

	serviceName := awsEndpointService.Status.EndpointServiceName
	var serviceID string
	if len(serviceName) != 0 {
		// check if Endpoint Service exists in AWS
		output, err := ec2Client.DescribeVpcEndpointServiceConfigurationsWithContext(ctx, &ec2.DescribeVpcEndpointServiceConfigurationsInput{
			Filters: []*ec2.Filter{
				{
					Name:   aws.String("service-name"),
					Values: []*string{aws.String(serviceName)},
				},
			},
		})
		if err != nil {
			if awsErr, ok := err.(awserr.Error); ok {
				return errors.New(awsErr.Code())
			}
			return err
		}
		if len(output.ServiceConfigurations) == 0 {
			// clear the EndpointServiceName so a new Endpoint Service is created on the requeue
			awsEndpointService.Status.EndpointServiceName = ""
			return fmt.Errorf("endpoint service %s not found, resetting status", serviceName)
		}
		serviceID = aws.StringValue(output.ServiceConfigurations[0].ServiceId)
		log.Info("endpoint service exists", "serviceName", serviceName)
	} else {
		// determine the LB ARN
		lbName := awsEndpointService.Spec.NetworkLoadBalancerName
		output, err := elbv2Client.DescribeLoadBalancersWithContext(ctx, &elbv2.DescribeLoadBalancersInput{
			Names: []*string{aws.String(lbName)},
		})
		if err != nil {
			if awsErr, ok := err.(awserr.Error); ok {
				return errors.New(awsErr.Code())
			}
			return err
		}
		if len(output.LoadBalancers) == 0 {
			return fmt.Errorf("load balancer %s not found", lbName)
		}
		lb := output.LoadBalancers[0]
		lbARN := lb.LoadBalancerArn
		if lbARN == nil {
			return fmt.Errorf("load balancer ARN is nil")
		}
		if lb.State == nil || *lb.State.Code != elbv2.LoadBalancerStateEnumActive {
			return fmt.Errorf("load balancer %s is not yet active", *lbARN)
		}

		managementClusterInfrastructure := &configv1.Infrastructure{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
		if err := r.Get(ctx, client.ObjectKeyFromObject(managementClusterInfrastructure), managementClusterInfrastructure); err != nil {
			return fmt.Errorf("failed to get management cluster infrastructure: %w", err)
		}

		// create the Endpoint Service
		createEndpointServiceOutput, err := ec2Client.CreateVpcEndpointServiceConfigurationWithContext(ctx, &ec2.CreateVpcEndpointServiceConfigurationInput{
			// TODO: we should probably do some sort of automated acceptance check against the VPC ID in the HostedCluster
			AcceptanceRequired:      aws.Bool(false),
			NetworkLoadBalancerArns: []*string{lbARN},
			TagSpecifications: []*ec2.TagSpecification{{
				ResourceType: aws.String("vpc-endpoint-service"),
				Tags: append(apiTagToEC2Tag(awsEndpointService.Spec.ResourceTags), &ec2.Tag{
					Key:   aws.String("kubernetes.io/cluster/" + managementClusterInfrastructure.Status.InfrastructureName),
					Value: aws.String("owned"),
				}),
			}},
		})
		if err != nil {
			if awsErr, ok := err.(awserr.Error); ok {
				if awsErr.Code() == request.InvalidParameterErrCode {
					// TODO: optional filter by regex on error msg (could be fragile)
					// e.g. "LBs are already associated with another VPC Endpoint Service Configuration"
					log.Info("service endpoint might already exist, attempting adoption")
					var err error
					serviceName, serviceID, err = findExistingVpcEndpointService(ctx, ec2Client, *lbARN)
					if err != nil {
						log.Info("existing endpoint service not found, adoption failed", "err", err)
						return errors.New(awsErr.Code())
					}
				} else {
					return errors.New(awsErr.Code())
				}
			}
			if len(serviceName) == 0 {
				return err
			}
			log.Info("endpoint service adopted", "serviceName", serviceName)
		} else {
			serviceName = aws.StringValue(createEndpointServiceOutput.ServiceConfiguration.ServiceName)
			serviceID = aws.StringValue(createEndpointServiceOutput.ServiceConfiguration.ServiceId)
			log.Info("endpoint service created", "serviceName", serviceName)
		}
	}
	awsEndpointService.Status.EndpointServiceName = serviceName

	// reconcile permissions for aws endpoint service
	permResp, err := ec2Client.DescribeVpcEndpointServicePermissions(&ec2.DescribeVpcEndpointServicePermissionsInput{
		ServiceId: aws.String(serviceID),
	})
	if err != nil {
		return fmt.Errorf("failed to get vpc endpoint permissions with service ID %s: %w", serviceID, err)
	}

	controlPlaneOperatorRoleARN, err := r.controlPlaneOperatorRoleARNWithoutPath(hostedCluster)
	if err != nil {
		return fmt.Errorf("failed to get control plane operator role ARN: %w", err)
	}

	oldPerms := sets.NewString()
	for _, allowed := range permResp.AllowedPrincipals {
		oldPerms.Insert(aws.StringValue(allowed.Principal))
	}
	desiredPerms := sets.NewString(controlPlaneOperatorRoleARN)
	desiredPerms = desiredPerms.Insert(hostedCluster.Spec.Platform.AWS.AdditionalAllowedPrincipals...)

	if !desiredPerms.Equal(oldPerms) {
		input := &ec2.ModifyVpcEndpointServicePermissionsInput{
			ServiceId: aws.String(serviceID),
		}
		if added := desiredPerms.Difference(oldPerms).List(); len(added) > 0 {
			input.AddAllowedPrincipals = aws.StringSlice(added)
		}
		if removed := oldPerms.Difference(desiredPerms).List(); len(removed) > 0 {
			input.RemoveAllowedPrincipals = aws.StringSlice(removed)
		}
		_, err := ec2Client.ModifyVpcEndpointServicePermissions(input)
		if err != nil {
			return fmt.Errorf("failed to update vpc endpoint permissions: %w", err)
		}
	}
	awsEndpointService.Status.EndpointServiceName = serviceName

	return nil
}

func apiTagToEC2Tag(in []hyperv1.AWSResourceTag) []*ec2.Tag {
	result := make([]*ec2.Tag, len(in))
	for _, val := range in {
		result = append(result, &ec2.Tag{Key: aws.String(val.Key), Value: aws.String(val.Value)})
	}

	return result
}

func findExistingVpcEndpointService(ctx context.Context, ec2Client ec2iface.EC2API, lbARN string) (string, string, error) {
	output, err := ec2Client.DescribeVpcEndpointServiceConfigurationsWithContext(ctx, &ec2.DescribeVpcEndpointServiceConfigurationsInput{})
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			return "", "", errors.New(awsErr.Code())
		}
		return "", "", err
	}
	if len(output.ServiceConfigurations) == 0 {
		return "", "", fmt.Errorf("no endpoint services found")
	}
	for _, svc := range output.ServiceConfigurations {
		for _, arn := range svc.NetworkLoadBalancerArns {
			if arn != nil && *arn == lbARN {
				return aws.StringValue(svc.ServiceName), aws.StringValue(svc.ServiceId), nil
			}
		}
	}
	return "", "", fmt.Errorf("no endpoint service found with load balancer ARN %s", lbARN)
}

func (r *AWSEndpointServiceReconciler) delete(ctx context.Context, awsEndpointService *hyperv1.AWSEndpointService) (bool, error) {
	log, err := logr.FromContext(ctx)
	if err != nil {
		return false, fmt.Errorf("no logger found: %w", err)
	}

	serviceName := awsEndpointService.Status.EndpointServiceName
	if len(serviceName) == 0 {
		// nothing to clean up
		return true, nil
	}

	// parse serviceID from serviceName e.g. com.amazonaws.vpce.us-west-1.vpce-svc-014f44db649a87c02 -> vpce-svc-014f44db649a87c02
	parts := strings.Split(serviceName, ".")
	serviceID := parts[len(parts)-1]

	// delete the Endpoint Service
	output, err := r.ec2Client.DeleteVpcEndpointServiceConfigurationsWithContext(ctx, &ec2.DeleteVpcEndpointServiceConfigurationsInput{
		ServiceIds: []*string{aws.String(serviceID)},
	})
	if err != nil {
		log.Info("failed to delete endpoint service, attempting to reject connections", "serviceID", serviceID)
		if rejectErr := r.rejectVpcEndpointConnections(ctx, serviceID); rejectErr != nil {
			return false, unwrapError(log, rejectErr)
		}

		return false, unwrapError(log, err)
	}

	// DeleteVpcEndpointServiceConfigurations doesn't return errors directly when a VPC Endpoint Service doesn't exist
	// or when it has active connections, instead returning errors within output.Unsuccessful
	// https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteVpcEndpointServiceConfigurations.html
	if output != nil && len(output.Unsuccessful) != 0 && output.Unsuccessful[0].Error != nil {
		log.Error(err, "unsuccessful deleting vpc endpoint service", "serviceID", serviceID)
		itemErr := *output.Unsuccessful[0].Error
		if itemErr.Code != nil {
			switch *itemErr.Code {
			case "InvalidVpcEndpointService.NotFound":
				log.Info("endpoint service already deleted", "serviceID", serviceID)
				return true, nil
			case "ExistingVpcEndpointConnections":
				log.Info("endpoint service has existing connections", "serviceID", serviceID)
				if err := r.rejectVpcEndpointConnections(ctx, serviceID); err != nil {
					return false, unwrapError(log, err)
				}
			}
		}

		return false, fmt.Errorf("%s", *output.Unsuccessful[0].Error.Message)
	}

	log.Info("endpoint service deleted", "serviceID", serviceID)
	return true, nil
}

func (r *AWSEndpointServiceReconciler) rejectVpcEndpointConnections(ctx context.Context, serviceID string) error {
	log, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("no logger found: %w", err)
	}

	existingConnectionsResult, describeConnectionsErr := r.ec2Client.DescribeVpcEndpointConnectionsWithContext(ctx, &ec2.DescribeVpcEndpointConnectionsInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("service-id"),
				Values: []*string{aws.String(serviceID)},
			},
		},
	})
	if describeConnectionsErr != nil {
		return unwrapError(log, describeConnectionsErr)
	}
	var existingEndpointIDs []*string
	for _, conn := range existingConnectionsResult.VpcEndpointConnections {
		state := aws.StringValue(conn.VpcEndpointState)
		switch state {
		case "pendingAcceptance", "pending", "available":
			existingEndpointIDs = append(existingEndpointIDs, conn.VpcEndpointId)
		}
	}
	if len(existingEndpointIDs) > 0 {
		log.Info("rejecting vpc endpoint connections", "serviceID", serviceID)
		if _, rejectEndpointsErr := r.ec2Client.RejectVpcEndpointConnectionsWithContext(ctx, &ec2.RejectVpcEndpointConnectionsInput{
			ServiceId:      aws.String(serviceID),
			VpcEndpointIds: existingEndpointIDs,
		}); rejectEndpointsErr != nil {
			return unwrapError(log, rejectEndpointsErr)
		}
	}

	return nil
}

func unwrapError(log logr.Logger, err error) error {
	if awsErr, ok := err.(awserr.Error); ok {
		log.Info("AWS Error", "code", awsErr.Code(), "message", awsErr.Message())
		return fmt.Errorf("error code: %s", awsErr.Code())
	}
	return err
}

func (r *AWSEndpointServiceReconciler) hostedControlPlane(ctx context.Context, hcpNamespace string) (*hyperv1.HostedControlPlane, error) {
	hcps := &hyperv1.HostedControlPlaneList{}
	if err := r.List(ctx, hcps, client.InNamespace(hcpNamespace)); err != nil {
		return nil, fmt.Errorf("failed to list HostedControlPlanes in namespace %s: %w", hcpNamespace, err)
	}
	if len(hcps.Items) != 1 {
		return nil, fmt.Errorf("unexpected number of HostedControlPlanes in namespace %s: expected 1, got %d", hcpNamespace, len(hcps.Items))
	}
	hcp := hcps.Items[0]
	return &hcp, nil
}

func hostedClusterNamespaceAndName(hcp *hyperv1.HostedControlPlane) (string, string) {
	hcNamespaceName, exists := hcp.Annotations[supportutil.HostedClusterAnnotation]
	if !exists {
		return "", ""
	}
	parts := strings.SplitN(hcNamespaceName, "/", 2)
	return parts[0], parts[1]
}

func (r *AWSEndpointServiceReconciler) hostedCluster(ctx context.Context, hcp *hyperv1.HostedControlPlane) (*hyperv1.HostedCluster, error) {
	namespace, name := hostedClusterNamespaceAndName(hcp)
	if namespace == "" || name == "" {
		return nil, fmt.Errorf("cannot determine hosted cluster name/namespace from HostedControlPlane %s", client.ObjectKeyFromObject(hcp).String())
	}
	hc := &hyperv1.HostedCluster{}
	hc.Namespace = namespace
	hc.Name = name
	if err := r.Get(ctx, client.ObjectKeyFromObject(hc), hc); err != nil {
		return nil, fmt.Errorf("failed to get hosted cluster %s: %w", client.ObjectKeyFromObject(hc).String(), err)
	}
	return hc, nil
}

// controlPlaneOperatorRoleWithoutPathFn excludes the IAM path from an ARN, which is needed when adding the CPO
// IAM role to AWS IAM trust policies, namely the AWS VPC Endpoint Service allowed principals' policy.
func (r *AWSEndpointServiceReconciler) controlPlaneOperatorRoleARNWithoutPath(hc *hyperv1.HostedCluster) (string, error) {
	if hc.Spec.Platform.AWS == nil || hc.Spec.Platform.AWS.RolesRef.ControlPlaneOperatorARN == "" {
		return "", fmt.Errorf("hosted cluster does not have control plane operator credentials")
	}
	arn, err := arn.Parse(hc.Spec.Platform.AWS.RolesRef.ControlPlaneOperatorARN)
	if err != nil {
		return "", fmt.Errorf("failed to parse %s into an ARN: %v", hc.Spec.Platform.AWS.RolesRef.ControlPlaneOperatorARN, err)
	}

	// IAM names cannot have a "/" while path names are the only way to get "/" into the name
	// IAM path names must begin and end with a "/", so the last chunk will be the name of the IAM role
	// https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_iam-quotas.html#reference_iam-quotas-names
	name := arn.Resource[strings.LastIndex(arn.Resource, "/")+1:]
	return fmt.Sprintf("arn:%s:%s::%s:role/%s", arn.Partition, arn.Service, arn.AccountID, name), nil
}
