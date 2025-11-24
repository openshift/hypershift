package karpenter

import (
	"context"
	"fmt"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	karpenterv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenter"
	"github.com/openshift/hypershift/karpenter-operator/controllers/karpenter/assets"
	supportassets "github.com/openshift/hypershift/support/assets"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

const (
	karpenterFinalizer = "hypershift.openshift.io/karpenter-finalizer"

	// NodeClaimDeletionTimeout is the timeout for the deletion of a NodeClaim during cluster deletion.
	// If the timeout is reached, the underlying node instance will be deleted directly without waiting for graceful termination.
	NodeClaimDeletionTimeout = 3 * time.Minute

	// KarpenterDeletionRequeueInterval is the interval at which the controller will requeue deletion of Karpenter resources during a hosted cluster deletion.
	KarpenterDeletionRequeueInterval = 15 * time.Second

	// KarpenterInstanceTerminationAttemptedAnnotation is a tracking annotation to prevent repeated calls to terminate the underlying node instance.
	KarpenterInstanceTerminationAttemptedAnnotation = "hypershift.openshift.io/karpenter-instance-termination-attempted"
)

var (
	crdEC2NodeClass = supportassets.MustCRD(assets.ReadFile, "karpenter.k8s.aws_ec2nodeclasses.yaml")
	crdNodePool     = supportassets.MustCRD(assets.ReadFile, "karpenter.sh_nodepools.yaml")
	crdNodeClaim    = supportassets.MustCRD(assets.ReadFile, "karpenter.sh_nodeclaims.yaml")
)

type Reconciler struct {
	ManagementClient          client.Client
	GuestClient               client.Client
	Namespace                 string
	ControlPlaneOperatorImage string
	KarpenterProviderAWSImage string
	KarpenterComponent        controlplanecomponent.ControlPlaneComponent
	ControlPlaneContext       controlplanecomponent.ControlPlaneContext
	ReleaseProvider           releaseinfo.Provider
	upsert.CreateOrUpdateProvider

	EC2ClientFactory func(region string) (ec2iface.EC2API, error)
	// Cached EC2 client
	ec2Client ec2iface.EC2API
}

func (r *Reconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, managementCluster cluster.Cluster) error {
	r.ManagementClient = managementCluster.GetClient()
	r.GuestClient = mgr.GetClient()
	r.CreateOrUpdateProvider = upsert.New(false)

	// First install the CRDs so we can create a watch below.
	if err := r.reconcileCRDs(ctx, true); err != nil {
		return err
	}

	c, err := controller.New("karpenter", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	// Watch CRDs guest side.
	if err := c.Watch(source.Kind[client.Object](mgr.GetCache(), &apiextensionsv1.CustomResourceDefinition{}, handler.EnqueueRequestsFromMapFunc(
		func(ctx context.Context, o client.Object) []ctrl.Request {
			// Only watch our Karpenter CRDs
			switch o.GetName() {
			case "ec2nodeclasses.karpenter.k8s.aws",
				"nodepools.karpenter.sh",
				"nodeclaims.karpenter.sh":
				return []ctrl.Request{{NamespacedName: client.ObjectKey{Namespace: r.Namespace}}}
			}
			return nil
		},
	))); err != nil {
		return fmt.Errorf("failed to watch CRDs: %w", err)
	}

	// Watch EC2NodeClass guest side.
	if err := c.Watch(source.Kind(mgr.GetCache(), &awskarpenterv1.EC2NodeClass{},
		&handler.TypedEnqueueRequestForObject[*awskarpenterv1.EC2NodeClass]{})); err != nil {
		return fmt.Errorf("failed to watch EC2NodeClass: %w", err)
	}

	// Watch the karpenter Deployment management side.
	if err := c.Watch(source.Kind[client.Object](managementCluster.GetCache(), &appsv1.Deployment{}, handler.EnqueueRequestsFromMapFunc(
		func(ctx context.Context, o client.Object) []ctrl.Request {
			if o.GetNamespace() != r.Namespace || o.GetName() != "karpenter" {
				return nil
			}
			return []ctrl.Request{{NamespacedName: client.ObjectKeyFromObject(o)}}
		},
	))); err != nil {
		return fmt.Errorf("failed to watch Deployment: %w", err)
	}

	namespacedPredicates := predicate.NewPredicateFuncs(func(object client.Object) bool {
		return object.GetNamespace() == r.Namespace
	})
	// Watch the HCP management side.
	if err := c.Watch(source.Kind[client.Object](managementCluster.GetCache(), &hyperv1.HostedControlPlane{}, handler.EnqueueRequestsFromMapFunc(
		func(ctx context.Context, o client.Object) []ctrl.Request {
			if o.GetNamespace() != r.Namespace {
				return nil
			}
			return []ctrl.Request{{NamespacedName: client.ObjectKeyFromObject(o)}}
		},
	), namespacedPredicates)); err != nil {
		return fmt.Errorf("failed to watch HostedControlPlane: %w", err)
	}

	// Trigger initial sync.
	initialSync := make(chan event.GenericEvent)
	if err := c.Watch(source.Channel(initialSync, &handler.EnqueueRequestForObject{})); err != nil {
		return fmt.Errorf("failed to watch initial sync channel: %w", err)
	}
	go func() {
		initialSync <- event.GenericEvent{Object: &hyperv1.HostedControlPlane{}}
	}()

	return nil
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling", "req", req)

	hcp, err := r.getHCP(ctx)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Setup for ControlPlaneContext and the Karpenter control plane v2 component.
	pullSecret := common.PullSecret(hcp.Namespace)
	if err := r.ManagementClient.Get(ctx, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get pull secret: %w", err)
	}

	releaseImage, err := r.ReleaseProvider.Lookup(ctx, util.HCPControlPlaneReleaseImage(hcp), pullSecret.Data[corev1.DockerConfigJsonKey])
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to lookup release image: %w", err)
	}
	imageProvider := imageprovider.New(releaseImage)
	imageProvider.ComponentImages()["token-minter"] = r.ControlPlaneOperatorImage
	imageProvider.ComponentImages()[util.AvailabilityProberImageName] = r.ControlPlaneOperatorImage

	cpContext := controlplanecomponent.ControlPlaneContext{
		Context:              ctx,
		Client:               r.ManagementClient,
		ApplyProvider:        upsert.NewApplyProvider(false),
		HCP:                  hcp,
		ReleaseImageProvider: imageProvider,
	}

	r.ControlPlaneContext = cpContext
	if r.KarpenterComponent == nil {
		r.KarpenterComponent = karpenterv2.NewComponent()
	}

	if hcp.DeletionTimestamp != nil {
		if controllerutil.ContainsFinalizer(hcp, karpenterFinalizer) {
			// The deletion flow is:
			// 1. Delete all NodePools (NodeClaims will be marked for deletion from deleting the NodePools due to ownerReferences)
			// 2. Make sure all NodeClaims are actually gone (gracefully first)
			// 3. If graceful timeout, forcefully terminate the underlying instance to bypass PDBs, preStop hooks, etc.
			// 4. Remove the finalizer from the HostedControlPlane to allow the rest of the HCP deletion to complete

			// Karpenter itself will make sure Nodes objects are deleted (and underlying instances are terminated) before finalizing the NodeClaims
			nodePoolList := &karpenterv1.NodePoolList{}
			if err := r.GuestClient.List(ctx, nodePoolList); err != nil {
				return ctrl.Result{RequeueAfter: KarpenterDeletionRequeueInterval}, fmt.Errorf("failed to list NodePools: %w", err)
			}

			// Delete all NodePools first
			if len(nodePoolList.Items) > 0 {
				for _, nodePool := range nodePoolList.Items {
					// If we still get the NodePool, but it's already marked as terminating, we don't need to call Delete again
					if !nodePool.GetDeletionTimestamp().IsZero() {
						continue
					}
					if err := r.GuestClient.Delete(ctx, &nodePool, &client.DeleteOptions{
						GracePeriodSeconds: ptr.To(int64(0)),
					}); err != nil {
						return ctrl.Result{}, fmt.Errorf("failed to delete NodePool: %w", err)
					}
				}
				return ctrl.Result{RequeueAfter: KarpenterDeletionRequeueInterval}, nil
			}

			// Make sure all NodeClaims are actually gone (gracefully first)
			nodeClaimList := &karpenterv1.NodeClaimList{}
			if err := r.GuestClient.List(ctx, nodeClaimList); err != nil {
				return ctrl.Result{RequeueAfter: KarpenterDeletionRequeueInterval}, fmt.Errorf("failed to list NodeClaims: %w", err)
			}
			if len(nodeClaimList.Items) > 0 {
				var elapsed time.Duration
				for _, nodeClaim := range nodeClaimList.Items {
					if nodeClaim.DeletionTimestamp == nil {
						// This should not happen; log and explicitly delete to avoid HCP deletion getting stuck.
						log.Info("NodeClaim has no deletion timestamp during HostedControlPlane deletion, deleting explicitly",
							"nodeClaim", nodeClaim.Name)
						if err := r.GuestClient.Delete(ctx, &nodeClaim, &client.DeleteOptions{
							GracePeriodSeconds: ptr.To(int64(0)),
						}); err != nil && !apierrors.IsNotFound(err) {
							log.Error(err, "Failed to delete NodeClaim without deletion timestamp", "nodeClaim", nodeClaim.Name)
						}
						continue
					}
					elapsed = time.Since(nodeClaim.DeletionTimestamp.Time)
					if elapsed < NodeClaimDeletionTimeout {
						continue
					}

					if err := r.handleNodeClaimTimeout(ctx, hcp, &nodeClaim); err != nil {
						log.Error(err, "Failed to handle NodeClaim timeout", "nodeClaim", nodeClaim.Name)
					}
				}
				// All NodeClaims should have similar deletion timestamps
				log.Info("Waiting for NodeClaims to be deleted, requeueing...", "nodeClaimCount", len(nodeClaimList.Items), "elapsed", elapsed)
				return ctrl.Result{RequeueAfter: KarpenterDeletionRequeueInterval}, nil
			}
		}

		originalHCP := hcp.DeepCopy()
		controllerutil.RemoveFinalizer(hcp, karpenterFinalizer)
		if err := r.ManagementClient.Patch(ctx, hcp, client.MergeFromWithOptions(originalHCP, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{RequeueAfter: KarpenterDeletionRequeueInterval}, fmt.Errorf("failed to remove finalizer from cluster: %w", err)
		}
		log.Info("Successfully removed all Karpenter NodePools and NodeClaims")
		return ctrl.Result{}, nil
	}
	if !controllerutil.ContainsFinalizer(hcp, karpenterFinalizer) {
		originalHCP := hcp.DeepCopy()
		controllerutil.AddFinalizer(hcp, karpenterFinalizer)
		if err := r.ManagementClient.Patch(ctx, hcp, client.MergeFromWithOptions(originalHCP, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to hostedControlPlane: %w", err)
		}
	}

	// Don't reconcile if Karpenter E2E override is set.
	if hcp.Annotations[hyperkarpenterv1.KarpenterCoreE2EOverrideAnnotation] != "true" {
		if err := r.reconcileOpenshiftEC2NodeClassDefault(ctx, hcp); err != nil {
			return ctrl.Result{}, err
		}
	}

	if err := r.KarpenterComponent.Reconcile(r.ControlPlaneContext); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile karpenter deployment: %w", err)
	}

	if err := r.reconcileCRDs(ctx, false); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reconcileCRDs reconcile the Karpenter CRDs, if onlyCreate is true it uses an only write non cached client.
func (r *Reconciler) reconcileCRDs(ctx context.Context, onlyCreate bool) error {
	log := ctrl.LoggerFrom(ctx)

	errs := []error{}
	var op controllerutil.OperationResult
	var err error
	for _, crd := range []*apiextensionsv1.CustomResourceDefinition{
		crdEC2NodeClass,
		crdNodePool,
		crdNodeClaim,
	} {
		if onlyCreate {
			if err := r.GuestClient.Create(ctx, crd); err != nil {
				if !apierrors.IsAlreadyExists(err) {
					errs = append(errs, err)
				}
			}
		} else {
			op, err = r.CreateOrUpdate(ctx, r.GuestClient, crd, func() error {
				return nil
			})
			if err != nil {
				errs = append(errs, err)
			}

		}
	}
	if err := utilerrors.NewAggregate(errs); err != nil {
		return fmt.Errorf("failed to reconcile CRDs: %w", err)
	}
	log.Info("Reconciled CRDs", "op", op)

	return nil
}

func (r *Reconciler) reconcileOpenshiftEC2NodeClassDefault(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	log := ctrl.LoggerFrom(ctx)

	ec2NodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
	ec2NodeClass.SetName(assets.EC2NodeClassDefault)

	op, err := r.CreateOrUpdate(ctx, r.GuestClient, ec2NodeClass, func() error {
		ec2NodeClass.Spec = hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
			SubnetSelectorTerms: []hyperkarpenterv1.SubnetSelectorTerm{
				{
					Tags: map[string]string{
						"karpenter.sh/discovery": hcp.Spec.InfraID,
					},
				},
			},
			SecurityGroupSelectorTerms: []hyperkarpenterv1.SecurityGroupSelectorTerm{
				{
					Tags: map[string]string{
						"karpenter.sh/discovery": hcp.Spec.InfraID,
					},
				},
			},
		}

		if hcp.Annotations[hyperv1.AWSMachinePublicIPs] == "true" {
			ec2NodeClass.Spec.AssociatePublicIPAddress = ptr.To(true)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile default OpenshiftEC2NodeClass: %w", err)
	}

	log.Info("Reconciled default OpenshiftEC2NodeClass", "op", op)
	return nil
}

func (r *Reconciler) getHCP(ctx context.Context) (*hyperv1.HostedControlPlane, error) {
	hcpList := &hyperv1.HostedControlPlaneList{}
	if err := r.ManagementClient.List(ctx, hcpList, client.InNamespace(r.Namespace)); err != nil {
		return nil, err
	}
	if len(hcpList.Items) == 0 {
		return nil, fmt.Errorf("failed to find HostedControlPlane in namespace %s", r.Namespace)
	}

	return &hcpList.Items[0], nil
}

// handleNodeClaimTimeout handles the timeout of a NodeClaim during cluster deletion.
func (r *Reconciler) handleNodeClaimTimeout(ctx context.Context, hcp *hyperv1.HostedControlPlane, nodeClaim *karpenterv1.NodeClaim) error {
	log := ctrl.LoggerFrom(ctx)

	// Check if we've already attempted termination
	if nodeClaim.Annotations[KarpenterInstanceTerminationAttemptedAnnotation] == "true" {
		log.Info("Instance termination already attempted, skipping", "nodeClaim", nodeClaim.Name)
		return nil
	}

	log.Info("NodeClaim deletion timeout exceeded, terminating instance", "nodeClaim", nodeClaim.Name, "platformType", hcp.Spec.Platform.Type)

	// We terminate the underlying cloud instance directly, because we emulate Karpenter deletion behavior.
	// Through its own control flow, Karpenter terminates instances directly by through the cloudprovider API when a NodeClaim terminationGracePeriod is reached.
	// If a terminationGracePeriod is not set, Nodes and instances will be hung indefinitely if there are problems like blocking PDBs or volumes not being detached.

	// Since we can't hook into Karpenter itself nor set a default terminationGracePeriod, instead we terminate the instance directly here.
	// Karpenter will then notice the instance is deleted because the corresponding Node will have a NotReady condition, and then delete the Node API object.
	if err := r.terminateInstance(ctx, hcp, nodeClaim); err != nil {
		return fmt.Errorf("failed to terminate instance: %w", err)
	}

	if err := r.markTerminationAttempted(ctx, nodeClaim); err != nil {
		log.Error(err, "Failed to mark termination attempted", "nodeClaim", nodeClaim.Name)
	}

	return nil
}

// getOrCreateEC2Client returns a cached EC2 client, or creates one if not yet initialized.
func (r *Reconciler) getOrCreateEC2Client(region string) (ec2iface.EC2API, error) {
	if r.ec2Client != nil {
		return r.ec2Client, nil
	}

	client, err := r.EC2ClientFactory(region)
	if err != nil {
		return nil, err
	}

	r.ec2Client = client
	return client, nil
}

// terminateInstance terminates underlying node machine instances depending on the platform type
func (r *Reconciler) terminateInstance(ctx context.Context, hcp *hyperv1.HostedControlPlane, nodeClaim *karpenterv1.NodeClaim) error {
	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		return r.terminateInstanceAWS(ctx, hcp, nodeClaim)
	default:
		return fmt.Errorf("unsupported platform type: %s", hcp.Spec.Platform.Type)
	}
}

func (r *Reconciler) terminateInstanceAWS(ctx context.Context, hcp *hyperv1.HostedControlPlane, nodeClaim *karpenterv1.NodeClaim) error {
	ec2Client, err := r.getOrCreateEC2Client(hcp.Spec.Platform.AWS.Region)
	if err != nil {
		return fmt.Errorf("failed to get EC2 client: %w", err)
	}

	instanceID := parseEC2InstanceIDFromProviderID(nodeClaim.Status.ProviderID)
	if instanceID == "" {
		return fmt.Errorf("failed to parse instance ID from providerID: %s", nodeClaim.Status.ProviderID)
	}

	_, err = ec2Client.TerminateInstancesWithContext(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []*string{aws.String(instanceID)},
	})
	return err
}

func parseEC2InstanceIDFromProviderID(providerID string) string {
	// providerID format: aws:///<availability-zone>/<instance-id>
	// Example: aws:///us-east-1a/i-0123456789abcdef0

	// Validate scheme
	if !strings.HasPrefix(providerID, "aws://") {
		return ""
	}

	// Split and validate structure
	parts := strings.Split(providerID, "/")
	// Expected format: ["aws:", "", "", "<zone>", "<instance-id>"]
	// Minimum valid length is 5 (scheme + 2 empty parts + zone + instance-id)
	if len(parts) < 5 {
		return ""
	}

	instanceID := parts[len(parts)-1]

	// Validate instance ID format (must start with "i-")
	if !strings.HasPrefix(instanceID, "i-") {
		return ""
	}

	return instanceID
}

func (r *Reconciler) markTerminationAttempted(ctx context.Context, nodeClaim *karpenterv1.NodeClaim) error {
	patch := client.MergeFrom(nodeClaim.DeepCopy())
	if nodeClaim.Annotations == nil {
		nodeClaim.Annotations = make(map[string]string)
	}
	nodeClaim.Annotations[KarpenterInstanceTerminationAttemptedAnnotation] = "true"
	return r.GuestClient.Patch(ctx, nodeClaim, patch)
}
