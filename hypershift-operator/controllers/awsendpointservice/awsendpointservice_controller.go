package awsendpointservice

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
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
	finalizer                              = "hypershift.openshift.io/finalizer"
	endpointServiceDeletionRequeueDuration = time.Duration(5 * time.Second)
)

type AWSEndpointServiceReconciler struct {
	client.Client
	upsert.CreateOrUpdateProvider
	Region      string
	ec2Client   ec2iface.EC2API
	elbv2Client elbv2iface.ELBV2API
}

func (r *AWSEndpointServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Only add this controller to the manager if we have a non-empty credentials file
	credsFile := os.Getenv("AWS_SHARED_CREDENTIALS_FILE")
	if len(credsFile) == 0 {
		return nil
	}
	credsBytes, err := ioutil.ReadFile(credsFile)
	if err != nil || len(credsBytes) == 0 {
		return nil
	}

	_, err = ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.AWSEndpointService{}).
		Build(r)
	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager: %w", err)
	}

	awsSession := awsutil.NewSession("hypershift-operator")
	awsConfig := awsutil.NewConfig(credsFile, r.Region)
	r.ec2Client = ec2.New(awsSession, awsConfig)
	r.elbv2Client = elbv2.New(awsSession, awsConfig)

	return nil
}

func (r *AWSEndpointServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logr.FromContext(ctx)
	log.Info("reconciling")

	// fetch the AWSEndpointService
	awsEndpointService := &hyperv1.AWSEndpointService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
		},
	}
	err := r.Get(ctx, client.ObjectKeyFromObject(awsEndpointService), awsEndpointService)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get resource: %w", err)
	}

	// Return early if deleted
	if !awsEndpointService.DeletionTimestamp.IsZero() {
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
		return ctrl.Result{}, err
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
	log := logr.FromContext(ctx)

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
	lbARN := output.LoadBalancers[0].LoadBalancerArn
	if lbARN == nil {
		return fmt.Errorf("NLB ARN is nil")
	}

	// create the Endpoint Service
	createEndpointServiceOutput, err := ec2Client.CreateVpcEndpointServiceConfigurationWithContext(ctx, &ec2.CreateVpcEndpointServiceConfigurationInput{
		// TODO: we should probably do some sort of automated acceptance check against the VPC ID in the HostedCluster
		AcceptanceRequired:      aws.Bool(false),
		NetworkLoadBalancerArns: []*string{lbARN},
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
					log.Error(err, "adoption failed")
					return err
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
	log := logr.FromContext(ctx)

	serviceName := awsEndpointService.Status.EndpointServiceName
	if len(serviceName) == 0 {
		// nothing to clean up
		return true, nil
	}

	// parse serviceID from serviceName e.g. com.amazonaws.vpce.us-west-1.vpce-svc-014f44db649a87c02 -> vpce-svc-014f44db649a87c02
	parts := strings.Split(serviceName, ".")
	serviceID := parts[len(parts)-1]

	// delete the Endpoint Service
	if _, err := r.ec2Client.DeleteVpcEndpointServiceConfigurationsWithContext(ctx, &ec2.DeleteVpcEndpointServiceConfigurationsInput{
		ServiceIds: []*string{aws.String(serviceID)},
	}); err != nil {
		return false, err
	}
	log.Info("endpoint service deleted", "serviceName", serviceName)

	return true, nil
}
