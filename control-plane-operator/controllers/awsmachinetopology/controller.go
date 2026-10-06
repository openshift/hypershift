package awsmachinetopology

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/autoscaling"
	"github.com/openshift/hypershift/support/reconcilerpolicy"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"k8s.io/client-go/util/workqueue"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type subnetClient interface {
	DescribeSubnets(context.Context, *ec2.DescribeSubnetsInput, ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
}

// Reconciler discovers worker topology using the hosted cluster's AWS account.
// It only publishes template metadata, never rollout-driving configuration.
type Reconciler struct {
	client.Client
	EC2Client          subnetClient
	HostedControlPlane client.ObjectKey
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("aws-machine-topology").
		For(&capiaws.AWSMachineTemplate{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetNamespace() == r.HostedControlPlane.Namespace && obj.GetAnnotations()[hyperv1.NodePoolLabel] != ""
		}))).
		Watches(&hyperv1.HostedControlPlane{}, handler.EnqueueRequestsFromMapFunc(r.enqueueTemplates), builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](time.Second, 30*time.Second),
		}).
		Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if req.Namespace != r.HostedControlPlane.Namespace {
		return ctrl.Result{}, nil
	}
	template := &capiaws.AWSMachineTemplate{}
	if err := r.Get(ctx, req.NamespacedName, template); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !template.DeletionTimestamp.IsZero() || template.Annotations[hyperv1.NodePoolLabel] == "" {
		return ctrl.Result{}, nil
	}
	if _, paused := template.Annotations[capiv1.PausedAnnotation]; paused {
		return ctrl.Result{}, nil
	}
	hcp := &hyperv1.HostedControlPlane{}
	if err := r.Get(ctx, r.HostedControlPlane, hcp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !hcp.DeletionTimestamp.IsZero() || hcp.Spec.Platform.Type != hyperv1.AWSPlatform || hcp.Spec.Platform.AWS == nil {
		return ctrl.Result{}, nil
	}
	if paused, duration := reconcilerpolicy.IsReconciliationPaused(ctrl.LoggerFrom(ctx), hcp.Spec.PausedUntil); paused {
		return ctrl.Result{RequeueAfter: duration}, nil
	}

	topology, lookupErr := r.resolveSubnetTopology(ctx, hcp.Spec.Platform.AWS.Region, template.Spec.Template.Spec.Subnet)
	encoded := ""
	if lookupErr == nil {
		data, err := json.Marshal(topology)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to encode AWS subnet topology: %w", err)
		}
		encoded = string(data)
	}

	if template.Annotations[autoscaling.AWSSubnetTopologyAnnotation] != encoded {
		patch := client.MergeFromWithOptions(template.DeepCopy(), client.MergeFromWithOptimisticLock{})
		if encoded == "" {
			// Never leave stale topology after an unresolved or ambiguous filter lookup.
			delete(template.Annotations, autoscaling.AWSSubnetTopologyAnnotation)
		} else {
			template.Annotations[autoscaling.AWSSubnetTopologyAnnotation] = encoded
		}
		if err := r.Patch(ctx, template, patch); err != nil {
			return ctrl.Result{}, errors.Join(lookupErr, fmt.Errorf("failed to patch AWS subnet topology: %w", err))
		}
	}
	if lookupErr != nil {
		return ctrl.Result{}, fmt.Errorf("failed to discover AWS subnet topology: %w", lookupErr)
	}
	// Filter matches may change without a Kubernetes resource update.
	return ctrl.Result{RequeueAfter: 10 * time.Minute}, nil
}

func (r *Reconciler) resolveSubnetTopology(ctx context.Context, region string, reference *capiaws.AWSResourceReference) (*autoscaling.AWSSubnetTopology, error) {
	if r.EC2Client == nil {
		return nil, fmt.Errorf("cluster-scoped EC2 client is not configured")
	}
	if region == "" || reference == nil {
		return nil, fmt.Errorf("AWS region and worker subnet reference are required")
	}
	input := &ec2.DescribeSubnetsInput{}
	if id := aws.ToString(reference.ID); id != "" {
		if len(reference.Filters) != 0 {
			return nil, fmt.Errorf("worker subnet must specify either an ID or filters, not both")
		}
		input.SubnetIds = []string{id}
	} else {
		if len(reference.Filters) == 0 {
			return nil, fmt.Errorf("worker subnet ID or filters are required")
		}
		for _, filter := range reference.Filters {
			input.Filters = append(input.Filters, ec2types.Filter{Name: aws.String(filter.Name), Values: filter.Values})
		}
	}

	var subnets []ec2types.Subnet
	paginator := ec2.NewDescribeSubnetsPaginator(r.EC2Client, input)
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("DescribeSubnets failed: %w", err)
		}
		subnets = append(subnets, output.Subnets...)
		if len(subnets) > 1 {
			return nil, fmt.Errorf("worker subnet reference matches multiple subnets; topology is ambiguous")
		}
	}
	if len(subnets) != 1 {
		return nil, fmt.Errorf("worker subnet reference did not match a subnet")
	}
	subnet := subnets[0]
	if id := aws.ToString(reference.ID); id != "" && aws.ToString(subnet.SubnetId) != id {
		return nil, fmt.Errorf("DescribeSubnets returned a different subnet than requested")
	}
	zone, zoneID := aws.ToString(subnet.AvailabilityZone), aws.ToString(subnet.AvailabilityZoneId)
	if zone == "" || zoneID == "" {
		return nil, fmt.Errorf("worker subnet is missing its availability zone or zone ID")
	}
	return &autoscaling.AWSSubnetTopology{Region: region, Zone: zone, ZoneID: zoneID}, nil
}

func (r *Reconciler) enqueueTemplates(ctx context.Context, obj client.Object) []ctrl.Request {
	if client.ObjectKeyFromObject(obj) != r.HostedControlPlane {
		return nil
	}
	templates := &capiaws.AWSMachineTemplateList{}
	if err := r.List(ctx, templates, client.InNamespace(r.HostedControlPlane.Namespace)); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list AWSMachineTemplates for topology discovery")
		return nil
	}
	var requests []ctrl.Request
	for _, template := range templates.Items {
		if template.Annotations[hyperv1.NodePoolLabel] != "" {
			requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&template)})
		}
	}
	return requests
}
