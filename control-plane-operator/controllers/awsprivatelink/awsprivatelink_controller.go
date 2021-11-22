package awsprivatelink

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/support/upsert"
)

const (
	defaultResync = 10 * time.Hour
)

type PrivateServiceObserver struct {
	client.Client
	log logr.Logger

	ControllerName   string
	ServiceNamespace string
	ServiceName      string
	HCPNamespace     string
	upsert.CreateOrUpdateProvider
}

func nameMapper(names []string) handler.MapFunc {
	nameSet := sets.NewString(names...)
	return func(obj client.Object) []reconcile.Request {
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
	kubeClient, err := kubeclient.NewForConfig(mgr.GetConfig())
	if err != nil {
		return err
	}
	informerFactory := informers.NewSharedInformerFactoryWithOptions(kubeClient, defaultResync, informers.WithNamespace(r.ServiceNamespace))
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
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
		},
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		return ctrl.Result{}, err
	}

	// Fetch the HostedControlPlane
	hcpList := &hyperv1.HostedControlPlaneList{}
	if err := r.List(ctx, hcpList, &client.ListOptions{Namespace: r.HCPNamespace}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get resource: %w", err)
	}
	if len(hcpList.Items) != 1 {
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
		return nil
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile AWSEndpointService: %w", err)
	}
	r.log.Info("reconcile complete", "request", req)
	return ctrl.Result{}, nil
}

const (
	finalizer                              = "hypershift.openshift.io/control-plane-operator-finalizer"
	endpointServiceDeletionRequeueDuration = time.Duration(5 * time.Second)
)

type AWSEndpointServiceReconciler struct {
	client.Client
	ec2Client ec2iface.EC2API
}

func (r *AWSEndpointServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.AWSEndpointService{}).
		Build(r)
	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager: %w", err)
	}

	r.Client = mgr.GetClient()

	// AWS_SHARED_CREDENTIALS_FILE and AWS_REGION envvar should be set in operator deployment
	awsSession := awsutil.NewSession("control-plane-operator")
	awsConfig := aws.NewConfig()
	r.ec2Client = ec2.New(awsSession, awsConfig)

	return nil
}

func (r *AWSEndpointServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logr.FromContext(ctx)
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

	// fetch the HostedControlPlane
	hcpList := &hyperv1.HostedControlPlaneList{}
	if err := r.List(ctx, hcpList, &client.ListOptions{Namespace: req.Namespace}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get resource: %w", err)
	}
	if len(hcpList.Items) != 1 {
		return ctrl.Result{}, fmt.Errorf("unexpected number of HostedControlPlanes in namespace, expected: 1, actual: %d", len(hcpList.Items))
	}
	hcp := hcpList.Items[0]

	// Return early if deleted
	if !awsEndpointService.DeletionTimestamp.IsZero() {
		completed, err := r.delete(ctx, awsEndpointService, r.ec2Client)
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

	// Reconcile the AWSEndpointService
	if err := reconcileAWSEndpointService(ctx, awsEndpointService, r.ec2Client, hcp); err != nil {
		meta.SetStatusCondition(&awsEndpointService.Status.Conditions, metav1.Condition{
			Type:    string(hyperv1.AWSEndpointAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.AWSErrorReason,
			Message: err.Error(),
		})
		if err := r.Status().Update(ctx, awsEndpointService); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	meta.SetStatusCondition(&awsEndpointService.Status.Conditions, metav1.Condition{
		Type:    string(hyperv1.AWSEndpointAvailable),
		Status:  metav1.ConditionTrue,
		Reason:  hyperv1.AWSSuccessReason,
		Message: "",
	})

	if err := r.Status().Update(ctx, awsEndpointService); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("reconcilation complete")
	return ctrl.Result{}, nil
}

func reconcileAWSEndpointService(ctx context.Context, awsEndpointService *hyperv1.AWSEndpointService, ec2Client ec2iface.EC2API, hcp hyperv1.HostedControlPlane) error {
	log := logr.FromContext(ctx)
	serviceName := awsEndpointService.Status.EndpointServiceName

	endpointID := awsEndpointService.Status.EndpointID
	if endpointID != "" {
		// check if Endpoint exists in AWS
		output, err := ec2Client.DescribeVpcEndpointsWithContext(ctx, &ec2.DescribeVpcEndpointsInput{
			VpcEndpointIds: []*string{aws.String(endpointID)},
		})
		if err != nil {
			return err
		}
		if len(output.VpcEndpoints) == 0 {
			// clear the EndpointID so a new Endpoint is created on the requeue
			awsEndpointService.Status.EndpointID = ""
			return fmt.Errorf("endpoint %s not found, resetting status", serviceName)
		}
		log.Info("endpoint exists", "endpointID", endpointID)
		return nil

	}

	if hcp.Spec.Platform.Type != hyperv1.AWSPlatform || hcp.Spec.Platform.AWS == nil || hcp.Spec.Platform.AWS.CloudProviderConfig == nil {
		return fmt.Errorf("AWS platform information not provided in HostedControlPlane")
	}
	input := &ec2.CreateVpcEndpointInput{
		ServiceName:     aws.String(serviceName),
		VpcId:           aws.String(hcp.Spec.Platform.AWS.CloudProviderConfig.VPC),
		VpcEndpointType: aws.String(ec2.VpcEndpointTypeInterface),
	}
	if hcp.Spec.Platform.AWS.CloudProviderConfig.Subnet != nil && hcp.Spec.Platform.AWS.CloudProviderConfig.Subnet.ID != nil {
		input.SubnetIds = []*string{hcp.Spec.Platform.AWS.CloudProviderConfig.Subnet.ID}
	}
	output, err := ec2Client.CreateVpcEndpointWithContext(ctx, input)
	if err != nil {
		return err
	}

	endpointID = *output.VpcEndpoint.VpcEndpointId
	log.Info("endpoint created", "endpointID", endpointID)
	awsEndpointService.Status.EndpointID = endpointID

	return nil
}

func (r *AWSEndpointServiceReconciler) delete(ctx context.Context, awsEndpointService *hyperv1.AWSEndpointService, ec2Client ec2iface.EC2API) (bool, error) {
	log := logr.FromContext(ctx)

	endpointID := awsEndpointService.Status.EndpointID
	if endpointID == "" {
		// nothing to clean up
		return true, nil
	}

	if _, err := ec2Client.DeleteVpcEndpointsWithContext(ctx, &ec2.DeleteVpcEndpointsInput{
		VpcEndpointIds: []*string{aws.String(endpointID)},
	}); err != nil {
		return false, err
	}

	log.Info("endpoint deleted", "endpointID", endpointID)
	return true, nil
}
