package awsprivatelink

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/aws/aws-sdk-go/service/route53/route53iface"

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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
)

const (
	defaultResync               = 10 * time.Hour
	externalPrivateServiceLabel = "hypershift.openshift.io/external-private-service"
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
	if err := c.Watch(&source.Informer{Informer: services.Informer()}, namedResourceHandler(r.ServiceName)); err != nil {
		return err
	}
	mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		informerFactory.Start(ctx.Done())
		return nil
	}))
	return nil
}

func (r *PrivateServiceObserver) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.log.Info("reconciling")

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
	lbName := strings.Split(strings.Split(svc.Status.LoadBalancer.Ingress[0].Hostname, ".")[0], "-")[0]
	if _, err := r.CreateOrUpdate(ctx, r, awsEndpointService, func() error {
		awsEndpointService.Spec.NetworkLoadBalancerName = lbName
		if hcp.Spec.Platform.AWS != nil {
			awsEndpointService.Spec.ResourceTags = hcp.Spec.Platform.AWS.ResourceTags
		}
		return nil
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile AWSEndpointService: %w", err)
	}
	r.log.Info("reconcile complete", "request", req)
	return ctrl.Result{}, nil
}

const (
	finalizer                              = "hypershift.openshift.io/control-plane-operator-finalizer"
	endpointServiceDeletionRequeueDuration = 5 * time.Second
	hypershiftLocalZone                    = "hypershift.local"
	routerDomain                           = "apps"
)

// AWSEndpointServiceReconciler watches AWSEndpointService resources and reconciles
// the existence of AWS Endpoints for it in the guest cluster infrastructure.
type AWSEndpointServiceReconciler struct {
	client.Client
	ec2Client     ec2iface.EC2API
	route53Client route53iface.Route53API
	upsert.CreateOrUpdateProvider
}

func (r *AWSEndpointServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.AWSEndpointService{}).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewItemExponentialFailureRateLimiter(3*time.Second, 30*time.Second),
			MaxConcurrentReconciles: 10,
		}).
		Watches(&hyperv1.HostedControlPlane{}, handler.Funcs{UpdateFunc: r.enqueueOnAccessChange(mgr)}).
		Build(r)
	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager: %w", err)
	}
	r.Client = mgr.GetClient()

	// AWS_SHARED_CREDENTIALS_FILE and AWS_REGION envvar should be set in operator deployment
	awsSession := awsutil.NewSession("control-plane-operator", "", "", "", "")
	awsConfig := aws.NewConfig()
	r.ec2Client = ec2.New(awsSession, awsConfig)
	route53Config := aws.NewConfig()
	r.route53Client = route53.New(awsSession, route53Config)

	return nil
}

func (r *AWSEndpointServiceReconciler) enqueueOnAccessChange(mgr ctrl.Manager) func(context.Context, event.UpdateEvent, workqueue.RateLimitingInterface) {
	return func(ctx context.Context, e event.UpdateEvent, q workqueue.RateLimitingInterface) {
		logger := mgr.GetLogger()
		newHCP, isOk := e.ObjectNew.(*hyperv1.HostedControlPlane)
		if !isOk {
			logger.Info("WARNING: enqueueOnAccessChange: new resource is not of type HostedControlPlane")
			return
		}
		oldHCP, isOk := e.ObjectOld.(*hyperv1.HostedControlPlane)
		if !isOk {
			logger.Info("WARNING: enqueueOnAccessChange: old resource is not of type HostedControlPlane")
			return
		}
		// Only enqueue awsendpointservices when there is a change in the endpointaccess value, otherwise ignore changes
		if newHCP.Spec.Platform.AWS != nil && oldHCP.Spec.Platform.AWS != nil && newHCP.Spec.Platform.AWS.EndpointAccess != oldHCP.Spec.Platform.AWS.EndpointAccess {
			awsEndpointServiceList := &hyperv1.AWSEndpointServiceList{}
			if err := r.List(context.Background(), awsEndpointServiceList, client.InNamespace(newHCP.Namespace)); err != nil {
				logger.Error(err, "enqueueOnAccessChange: cannot list awsendpointservices")
				return
			}
			for i := range awsEndpointServiceList.Items {
				q.Add(reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&awsEndpointServiceList.Items[i])})
			}
		}
	}
}

func (r *AWSEndpointServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log, err := logr.FromContext(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("logger not found: %w", err)
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
		completed, err := r.delete(ctx, awsEndpointService, r.ec2Client, r.route53Client)
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

	serviceName := awsEndpointService.Status.EndpointServiceName
	if serviceName == "" {
		// Service Endpoint is not yet set, wait for hypershift-operator to populate
		// Likely observing our own Create
		return ctrl.Result{}, nil
	}

	// Fetch the HostedControlPlane
	hcpList := &hyperv1.HostedControlPlaneList{}
	if err := r.List(ctx, hcpList, &client.ListOptions{Namespace: req.Namespace}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get resource: %w", err)
	}
	if len(hcpList.Items) == 0 {
		// Return early if HostedControlPlane is deleted
		return ctrl.Result{}, nil
	}
	if len(hcpList.Items) > 1 {
		return ctrl.Result{}, fmt.Errorf("unexpected number of HostedControlPlanes in namespace, expected: 1, actual: %d", len(hcpList.Items))
	}
	hcp := &hcpList.Items[0]

	if isPaused, duration := util.IsReconciliationPaused(log, hcp.Spec.PausedUntil); isPaused {
		log.Info("Reconciliation paused", "pausedUntil", *hcp.Spec.PausedUntil)
		return ctrl.Result{RequeueAfter: duration}, nil
	}

	// Reconcile the AWSEndpointService
	oldStatus := awsEndpointService.Status.DeepCopy()
	if err := r.reconcileAWSEndpointService(ctx, awsEndpointService, hcp, r.ec2Client, r.route53Client); err != nil {
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

	log.Info("reconcilation complete")
	// always requeue to catch and report out of band changes in AWS
	// NOTICE: if the RequeueAfter interval is short enough, it could result in hitting some AWS request limits.
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

func hasAWSConfig(platform *hyperv1.PlatformSpec) bool {
	return platform.Type == hyperv1.AWSPlatform && platform.AWS != nil && platform.AWS.CloudProviderConfig != nil &&
		platform.AWS.CloudProviderConfig.Subnet != nil && platform.AWS.CloudProviderConfig.Subnet.ID != nil
}

func diffIDs(desired []string, existing []*string) (added, removed []*string) {
	var found bool
	for i, desiredID := range desired {
		found = false
		for _, existingID := range existing {
			if desiredID == *existingID {
				found = true
				break
			}
		}
		if !found {
			added = append(added, &desired[i])
		}
	}
	for _, existingID := range existing {
		found = false
		for _, desiredID := range desired {
			if desiredID == *existingID {
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

func (r *AWSEndpointServiceReconciler) reconcileAWSEndpointService(ctx context.Context, awsEndpointService *hyperv1.AWSEndpointService, hcp *hyperv1.HostedControlPlane, ec2Client ec2iface.EC2API, route53Client route53iface.Route53API) error {
	log, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("logger not found: %w", err)
	}

	if len(awsEndpointService.Status.EndpointServiceName) == 0 {
		log.Info("endpoint service name is not set, ignoring", "name", awsEndpointService.Name)
		return nil
	}

	endpointID := awsEndpointService.Status.EndpointID
	var endpointDNSEntries []*ec2.DnsEntry
	if endpointID != "" {
		// check if Endpoint exists in AWS
		output, err := ec2Client.DescribeVpcEndpointsWithContext(ctx, &ec2.DescribeVpcEndpointsInput{
			VpcEndpointIds: []*string{aws.String(endpointID)},
		})
		if err != nil {
			if awsErr, ok := err.(awserr.Error); ok {
				if awsErr.Code() == "InvalidVpcEndpointId.NotFound" {
					// clear the EndpointID so a new Endpoint is created on the requeue
					awsEndpointService.Status.EndpointID = ""
					return fmt.Errorf("endpoint with id %s not found, resetting status", endpointID)
				} else {
					return errors.New(awsErr.Code())
				}
			}
			return err
		}

		if aws.StringValue(output.VpcEndpoints[0].ServiceName) != awsEndpointService.Status.EndpointServiceName {
			log.Info("endpoint links to wrong endpointservice, deleting...", "LinkedVPCEndpointServiceName", aws.StringValue(output.VpcEndpoints[0].ServiceName), "WantedVPCEndpointService", awsEndpointService.Status.EndpointServiceName)
			if _, err := ec2Client.DeleteVpcEndpointsWithContext(ctx, &ec2.DeleteVpcEndpointsInput{
				VpcEndpointIds: []*string{output.VpcEndpoints[0].VpcEndpointId},
			}); err != nil {
				return fmt.Errorf("error deleting AWSEndpoint: %w", err)
			}

			// Once the VPC Endpoint is deleted, we need to send an error in order to reexecute the reconcilliation
			return fmt.Errorf("current endpoint %s is not pointing to the existing .Status.EndpointServiceName, reconciling by deleting endpoint", aws.StringValue(output.VpcEndpoints[0].ServiceName))
		}

		if len(output.VpcEndpoints) == 0 {
			// This should not happen but just in case
			// clear the EndpointID so a new Endpoint is created on the requeue
			awsEndpointService.Status.EndpointID = ""
			return fmt.Errorf("endpoint with id %s not found, resetting status", endpointID)
		}
		log.Info("endpoint exists", "endpointID", endpointID)
		endpointDNSEntries = output.VpcEndpoints[0].DnsEntries

		// Ensure endpoint has the right subnets.
		addedSubnet, removedSubnet := diffIDs(awsEndpointService.Spec.SubnetIDs, output.VpcEndpoints[0].SubnetIds)

		// Ensure endpoint has the right SG.
		exitingSG := make([]*string, 0)
		for _, group := range output.VpcEndpoints[0].Groups {
			exitingSG = append(exitingSG, group.GroupId)
		}
		addedSG, removedSG := diffIDs([]string{hcp.Status.Platform.AWS.DefaultWorkerSecurityGroupID}, exitingSG)

		if addedSubnet != nil || removedSubnet != nil || addedSG != nil || removedSG != nil {
			log.Info("endpoint subnets or security groups have changed")
			_, err := ec2Client.ModifyVpcEndpointWithContext(ctx, &ec2.ModifyVpcEndpointInput{
				VpcEndpointId:          aws.String(endpointID),
				AddSubnetIds:           addedSubnet,
				RemoveSubnetIds:        removedSubnet,
				AddSecurityGroupIds:    addedSG,
				RemoveSecurityGroupIds: removedSG,
			})
			if err != nil {
				msg := err.Error()
				if awsErr, ok := err.(awserr.Error); ok {
					msg = awsErr.Code()
				}
				log.Error(err, "failed to modify vpc endpoint")
				return fmt.Errorf("failed to modify vpc endpoint: %s", msg)
			}
			log.Info("endpoint subnets updated")
		} else {
			log.Info("endpoint subnets are unchanged")
		}
	} else {
		if !hasAWSConfig(&hcp.Spec.Platform) {
			return fmt.Errorf("AWS platform information not provided in HostedControlPlane")
		}

		// Verify there is not already an Endpoint that we can adopt
		// This can happen if we have a stale status on AWSEndpointService or encoutered
		// an error updating the AWSEndpointService on the previous reconcile
		output, err := ec2Client.DescribeVpcEndpointsWithContext(ctx, &ec2.DescribeVpcEndpointsInput{
			Filters: apiTagToEC2Filter(awsEndpointService.Name, hcp.Spec.Platform.AWS.ResourceTags),
		})
		if err != nil {
			msg := err.Error()
			if awsErr, ok := err.(awserr.Error); ok {
				msg = awsErr.Code()
			}
			log.Error(err, "failed to describe vpc endpoints")
			return fmt.Errorf("failed to describe vpc endpoints: %s", msg)
		}
		if len(output.VpcEndpoints) != 0 {
			if aws.StringValue(output.VpcEndpoints[0].ServiceName) != awsEndpointService.Status.EndpointServiceName {
				log.Info("endpoint links to wrong endpointservice, deleting...", "LinkedVPCEndpointServiceName", aws.StringValue(output.VpcEndpoints[0].ServiceName), "WantedVPCEndpointService", awsEndpointService.Status.EndpointServiceName)
				if _, err := ec2Client.DeleteVpcEndpointsWithContext(ctx, &ec2.DeleteVpcEndpointsInput{
					VpcEndpointIds: []*string{output.VpcEndpoints[0].VpcEndpointId},
				}); err != nil {
					return fmt.Errorf("error deleting AWSEndpoint: %w", err)
				}

				// Once the VPC Endpoint is deleted, we need to send an error in order to reexecute the reconcilliation
				return fmt.Errorf("current endpoint %s is not pointing to the existing .Status.EndpointServiceName, reconciling by deleting endpoint", aws.StringValue(output.VpcEndpoints[0].ServiceName))
			}
			endpointID = *output.VpcEndpoints[0].VpcEndpointId
			log.Info("endpoint already exists, adopting", "endpointID", endpointID)
			awsEndpointService.Status.EndpointID = endpointID
			endpointDNSEntries = output.VpcEndpoints[0].DnsEntries
		} else {
			log.Info("endpoint does not already exist")
			// Create the Endpoint
			subnetIDs := []*string{}
			for i := range awsEndpointService.Spec.SubnetIDs {
				subnetIDs = append(subnetIDs, &awsEndpointService.Spec.SubnetIDs[i])
			}

			if hcp.Status.Platform == nil || hcp.Status.Platform.AWS == nil || hcp.Status.Platform.AWS.DefaultWorkerSecurityGroupID == "" {
				return fmt.Errorf("DefaultWorkerSecurityGroupID doesn't exist yet for the endpoint to use")
			}
			output, err := ec2Client.CreateVpcEndpointWithContext(ctx, &ec2.CreateVpcEndpointInput{
				SecurityGroupIds: []*string{aws.String(hcp.Status.Platform.AWS.DefaultWorkerSecurityGroupID)},
				ServiceName:      aws.String(awsEndpointService.Status.EndpointServiceName),
				VpcId:            aws.String(hcp.Spec.Platform.AWS.CloudProviderConfig.VPC),
				VpcEndpointType:  aws.String(ec2.VpcEndpointTypeInterface),
				SubnetIds:        subnetIDs,
				TagSpecifications: []*ec2.TagSpecification{{
					ResourceType: aws.String("vpc-endpoint"),
					Tags:         apiTagToEC2Tag(awsEndpointService.Name, hcp.Spec.Platform.AWS.ResourceTags),
				}},
			})
			if err != nil {
				msg := err.Error()
				if awsErr, ok := err.(awserr.Error); ok {
					msg = awsErr.Code()
				}
				log.Error(err, "failed to create vpc endpoint")
				return fmt.Errorf("failed to create vpc endpoint: %s", msg)
			}
			if output == nil || output.VpcEndpoint == nil {
				return fmt.Errorf("CreateVpcEndpointWithContext output is nil")
			}

			endpointID = *output.VpcEndpoint.VpcEndpointId
			log.Info("endpoint created", "endpointID", endpointID)
			awsEndpointService.Status.EndpointID = endpointID
			endpointDNSEntries = output.VpcEndpoint.DnsEntries
		}
	}

	if len(endpointDNSEntries) == 0 {
		log.Info("endpoint has no DNS entries, skipping DNS record creation", "endpointID", endpointID)
		return nil
	}

	recordNames := recordsForService(awsEndpointService, hcp)
	if len(recordNames) == 0 {
		log.Info("WARNING: no mapping from AWSEndpointService to DNS")
		return nil
	}

	zoneName := zoneName(hcp.Name)
	zoneID, err := lookupZoneID(ctx, route53Client, zoneName)
	if err != nil {
		return err
	}

	var fqdns []string
	for _, recordName := range recordNames {
		fqdn := fmt.Sprintf("%s.%s", recordName, zoneName)
		fqdns = append(fqdns, fqdn)
		err = createRecord(ctx, route53Client, zoneID, fqdn, *(endpointDNSEntries[0].DnsName))
		if err != nil {
			return err
		}
		log.Info("DNS record created", "fqdn", fqdn)
	}

	awsEndpointService.Status.DNSNames = fqdns
	awsEndpointService.Status.DNSZoneID = zoneID

	if isPublic, externalNames := util.IsPublicHCP(hcp), hcpExternalNames(hcp); !isPublic && len(externalNames) > 0 {
		// only if not public and external names are configured, create services of type ExternalName so external-dns
		// can create records for them
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
				return reconcileExternalService(svc, hcp, externalName, aws.StringValue(endpointDNSEntries[0].DnsName))
			}); err != nil {
				errs = append(errs, fmt.Errorf("failed to reconcile %s external service: %w", svcType, err))
			}
		}
		if len(errs) > 0 {
			return fmt.Errorf("failed to create external services for private endpoints: %w", utilerrors.NewAggregate(errs))
		}
	} else {
		// if the cluster is public, ensure that any ExternalName services are removed
		privateExternalServices := &corev1.ServiceList{}
		if err := r.List(ctx, privateExternalServices, client.HasLabels{externalPrivateServiceLabel}); err != nil {
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
	}

	return nil
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
	apiStrategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.APIServer)
	if apiStrategy != nil && apiStrategy.Type == hyperv1.Route && apiStrategy.Route != nil && apiStrategy.Route.Hostname != "" {
		result["api"] = apiStrategy.Route.Hostname
	}

	oauthStrategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.OAuthServer)
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
	if m := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.APIServer); m != nil && m.Type == hyperv1.Route {
		return []string{"api", "*." + routerDomain}
	}

	return []string{"*.apps"}

}

func apiTagToEC2Tag(name string, in []hyperv1.AWSResourceTag) []*ec2.Tag {
	result := make([]*ec2.Tag, len(in))
	for _, val := range in {
		result = append(result, &ec2.Tag{Key: aws.String(val.Key), Value: aws.String(val.Value)})
	}
	result = append(result, &ec2.Tag{Key: aws.String("AWSEndpointService"), Value: aws.String(name)})

	return result
}

func apiTagToEC2Filter(name string, in []hyperv1.AWSResourceTag) []*ec2.Filter {
	result := make([]*ec2.Filter, len(in))
	for _, val := range in {
		result = append(result, &ec2.Filter{Name: aws.String("tag:" + val.Key), Values: aws.StringSlice([]string{val.Value})})
	}
	result = append(result, &ec2.Filter{Name: aws.String("tag:AWSEndpointService"), Values: aws.StringSlice([]string{name})})

	return result
}

func (r *AWSEndpointServiceReconciler) delete(ctx context.Context, awsEndpointService *hyperv1.AWSEndpointService, ec2Client ec2iface.EC2API, route53Client route53iface.Route53API) (bool, error) {
	log, err := logr.FromContext(ctx)
	if err != nil {
		return false, fmt.Errorf("logger not found: %w", err)
	}

	endpointID := awsEndpointService.Status.EndpointID
	if endpointID != "" {
		if _, err := ec2Client.DeleteVpcEndpointsWithContext(ctx, &ec2.DeleteVpcEndpointsInput{
			VpcEndpointIds: []*string{aws.String(endpointID)},
		}); err != nil {
			return false, err
		}

		// check if Endpoint exists in AWS
		output, err := ec2Client.DescribeVpcEndpointsWithContext(ctx, &ec2.DescribeVpcEndpointsInput{
			VpcEndpointIds: []*string{aws.String(endpointID)},
		})
		if err != nil {
			awsErr, ok := err.(awserr.Error)
			if ok {
				if awsErr.Code() != "InvalidVpcEndpointId.NotFound" {
					return false, err
				}
			} else {
				return false, err
			}

		}

		if output != nil && len(output.VpcEndpoints) != 0 {
			return false, fmt.Errorf("resource requested for deletion but still present")
		}

		log.Info("endpoint deleted", "endpointID", endpointID)
	}

	zoneID := awsEndpointService.Status.DNSZoneID

	for _, fqdn := range awsEndpointService.Status.DNSNames {
		if fqdn != "" && zoneID != "" {
			record, err := findRecord(ctx, route53Client, zoneID, fqdn)
			if err != nil {
				if awsErr, ok := err.(awserr.Error); ok {
					if awsErr.Code() == route53.ErrCodeNoSuchHostedZone {
						log.Info("Hosted Zone not found", "hostedzone", zoneID)
						return true, nil
					}
				}

				return false, err
			}
			if record != nil {
				err = deleteRecord(ctx, route53Client, zoneID, record)
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
