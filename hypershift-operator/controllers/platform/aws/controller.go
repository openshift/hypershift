package aws

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/aws/aws-sdk-go/service/elbv2/elbv2iface"
	"github.com/go-logr/logr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/support/upsert"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	finalizer                              = "hypershift.openshift.io/hypershift-operator-finalizer"
	endpointServiceDeletionRequeueDuration = 5 * time.Second
	lbNotActiveRequeueDuration             = 20 * time.Second
)

type AWSEndpointServiceReconciler struct {
	client.Client
	upsert.CreateOrUpdateProvider
	ec2Client   ec2iface.EC2API
	elbv2Client elbv2iface.ELBV2API
}

func (r *AWSEndpointServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.AWSEndpointService{}).
		Build(r)
	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager: %w", err)
	}

	// AWS_SHARED_CREDENTIALS_FILE and AWS_REGION envvar should be set in operator deployment
	awsSession := awsutil.NewSession("hypershift-operator")
	awsConfig := aws.NewConfig()
	r.ec2Client = ec2.New(awsSession, awsConfig)
	r.elbv2Client = elbv2.New(awsSession, awsConfig)

	return nil
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

	// Reconcile the AWSEndpointService
	if err = reconcileAWSEndpointService(ctx, awsEndpointService, r.ec2Client, r.elbv2Client); err != nil {
		meta.SetStatusCondition(&awsEndpointService.Status.Conditions, metav1.Condition{
			Type:    string(hyperv1.AWSEndpointServiceAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.AWSErrorReason,
			Message: err.Error(),
		})
		if err := r.Status().Update(ctx, awsEndpointService); err != nil {
			return ctrl.Result{}, err
		}
		// Most likely cause of error here is the NLB is not yet active.  This can take ~2m so
		// a longer requeue time is warranted.  The ratelimits AWS calls and updates to the CR.
		log.Info("reconcilation failed, retrying in 20s", "err", err)
		return ctrl.Result{RequeueAfter: lbNotActiveRequeueDuration}, nil
	}

	meta.SetStatusCondition(&awsEndpointService.Status.Conditions, metav1.Condition{
		Type:    string(hyperv1.AWSEndpointServiceAvailable),
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

func reconcileAWSEndpointService(ctx context.Context, awsEndpointService *hyperv1.AWSEndpointService, ec2Client ec2iface.EC2API, elbv2Client elbv2iface.ELBV2API) error {
	log, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("no logger found: %w", err)
	}

	serviceName := awsEndpointService.Status.EndpointServiceName
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
			return err
		}
		if len(output.ServiceConfigurations) == 0 {
			// clear the EndpointServiceName so a new Endpoint Service is created on the requeue
			awsEndpointService.Status.EndpointServiceName = ""
			return fmt.Errorf("endpoint service %s not found, resetting status", serviceName)
		}
		log.Info("endpoint service exists", "serviceName", serviceName)
		return nil
	}

	// determine the LB ARN
	lbName := awsEndpointService.Spec.NetworkLoadBalancerName
	output, err := elbv2Client.DescribeLoadBalancersWithContext(ctx, &elbv2.DescribeLoadBalancersInput{
		Names: []*string{aws.String(lbName)},
	})
	if err != nil {
		return err
	}
	if len(output.LoadBalancers) == 0 {
		return fmt.Errorf("NLB %s not found", lbName)
	}
	lb := output.LoadBalancers[0]
	lbARN := lb.LoadBalancerArn
	if lbARN == nil {
		return fmt.Errorf("NLB ARN is nil")
	}
	if lb.State == nil || *lb.State.Code != elbv2.LoadBalancerStateEnumActive {
		return fmt.Errorf("load balancer %s is not yet active", *lbARN)
	}

	// create the Endpoint Service
	createEndpointServiceOutput, err := ec2Client.CreateVpcEndpointServiceConfigurationWithContext(ctx, &ec2.CreateVpcEndpointServiceConfigurationInput{
		// TODO: we should probably do some sort of automated acceptance check against the VPC ID in the HostedCluster
		AcceptanceRequired:      aws.Bool(false),
		NetworkLoadBalancerArns: []*string{lbARN},
		TagSpecifications: []*ec2.TagSpecification{{
			ResourceType: aws.String("vpc-endpoint-service"),
			Tags:         apiTagToEC2Tag(awsEndpointService.Spec.ResourceTags),
		}},
	})
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			if awsErr.Code() == request.InvalidParameterErrCode {
				// TODO: optional filter by regex on error msg (could be fragile)
				// e.g. "LBs are already associated with another VPC Endpoint Service Configuration"
				log.Info("service endpoint might already exist, attempting adoption")
				var err error
				serviceName, err = findExistingVpcEndpointService(ctx, ec2Client, *lbARN)
				if err != nil {
					log.Info("existing endpoint service not found, adoption failed", "err", err)
					return awsErr
				}
			} else {
				return awsErr
			}
		}
		if len(serviceName) == 0 {
			return err
		}
		log.Info("endpoint service adopted", "serviceName", serviceName)
	} else {
		serviceName = *createEndpointServiceOutput.ServiceConfiguration.ServiceName
		log.Info("endpoint service created", "serviceName", serviceName)
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

func findExistingVpcEndpointService(ctx context.Context, ec2Client ec2iface.EC2API, lbARN string) (string, error) {
	output, err := ec2Client.DescribeVpcEndpointServiceConfigurationsWithContext(ctx, &ec2.DescribeVpcEndpointServiceConfigurationsInput{})
	if err != nil {
		return "", err
	}
	if len(output.ServiceConfigurations) == 0 {
		return "", fmt.Errorf("no endpoint services found")
	}
	for _, svc := range output.ServiceConfigurations {
		for _, arn := range svc.NetworkLoadBalancerArns {
			if arn != nil && *arn == lbARN {
				return *svc.ServiceName, nil
			}
		}
	}
	return "", fmt.Errorf("no endpoint service found with LB ARN %s", lbARN)
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
		return false, err
	}
	if output != nil && len(output.Unsuccessful) != 0 && output.Unsuccessful[0].Error != nil {
		itemErr := *output.Unsuccessful[0].Error
		if itemErr.Code != nil && *itemErr.Code == "InvalidVpcEndpointService.NotFound" {
			log.Info("endpoint service already deleted", "serviceID", serviceID)
			return true, nil
		}
		return false, fmt.Errorf("%s", *output.Unsuccessful[0].Error.Message)
	}

	log.Info("endpoint service deleted", "serviceID", serviceID)
	return true, nil
}
