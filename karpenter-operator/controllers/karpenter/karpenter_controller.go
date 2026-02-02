package karpenter

import (
	"context"
	"fmt"
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
	// If the timeout is reached, the NodeClaim will be forcefully deleted by setting the termination timestamp annotation.
	NodeClaimDeletionTimeout = 3 * time.Minute

	// KarpenterDeletionRequeueInterval is the interval at which the controller will requeue deletion of Karpenter resources during a hosted cluster deletion.
	KarpenterDeletionRequeueInterval = 15 * time.Second
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
		return ctrl.Result{}, err
	}
	if hcp == nil {
		log.Info("HostedControlPlane not found")
		return ctrl.Result{}, nil
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
		// TODO(maxcao13): if supporting disablement, we don't want to force delete immediately.
		// When force=true, we skip the graceful timeout and immediately trigger forceful deletion.
		// When force=false, we wait for NodeClaimDeletionTimeout before triggering forceful deletion.
		force := true
		if controllerutil.ContainsFinalizer(hcp, karpenterFinalizer) {
			// The deletion flow is:
			// 1. Delete all NodePools (NodeClaims will be marked for deletion from deleting the NodePools due to ownerReferences)
			// 2. Make sure all NodeClaims are actually gone (gracefully first, unless force=true)
			// 3. If graceful timeout or force=true, set the termination timestamp annotation to trigger Karpenter's forceful deletion
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
						// This could happen if a NodeClaim has been orphaned without a NodePool owner ref
						log.Info("NodeClaim has no deletion timestamp during deletion, deleting explicitly", "nodeClaim", nodeClaim.Name)
						if err := r.GuestClient.Delete(ctx, &nodeClaim, &client.DeleteOptions{GracePeriodSeconds: ptr.To(int64(0))}); err != nil {
							return ctrl.Result{RequeueAfter: KarpenterDeletionRequeueInterval}, fmt.Errorf("failed to delete NodeClaim: %w", err)
						}
						continue
					}
					elapsed = time.Since(nodeClaim.DeletionTimestamp.Time)
					if !force && elapsed < NodeClaimDeletionTimeout {
						continue
					}

					if err := r.handleForcefulNodeClaimDeletion(ctx, &nodeClaim); err != nil {
						return ctrl.Result{RequeueAfter: KarpenterDeletionRequeueInterval}, fmt.Errorf("failed to handle forceful NodeClaim deletion: %w", err)
					}
				}
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
		return nil, nil
	}

	return &hcpList.Items[0], nil
}

// handleForcefulNodeClaimDeletion handles the timeout of a NodeClaim during cluster deletion.
func (r *Reconciler) handleForcefulNodeClaimDeletion(ctx context.Context, nodeClaim *karpenterv1.NodeClaim) error {
	log := ctrl.LoggerFrom(ctx)

	// Check if we've already attempted termination
	if nodeClaim.Annotations[karpenterv1.NodeClaimTerminationTimestampAnnotationKey] != "" {
		log.Info("NodeClaim termination already attempted, skipping", "nodeClaim", nodeClaim.Name)
		return nil
	}

	log.Info("Allowing Karpenter to forcefully delete the NodeClaim", "nodeClaim", nodeClaim.Name)

	// TODO(maxcao13): upstream has a escape hatch to forcefully delete NodeClaims using this annotation
	// there is an upstream issue to enable forceful deletion through a better interface: https://github.com/kubernetes-sigs/karpenter/issues/2815
	// we should come back later to fix this when that is resolved: https://issues.redhat.com/browse/AUTOSCALE-527
	patch := client.MergeFrom(nodeClaim.DeepCopy())
	if nodeClaim.Annotations == nil {
		nodeClaim.Annotations = make(map[string]string)
	}
	nodeClaim.Annotations[karpenterv1.NodeClaimTerminationTimestampAnnotationKey] = nodeClaim.GetDeletionTimestamp().Format(time.RFC3339)
	if err := r.GuestClient.Patch(ctx, nodeClaim, patch); err != nil {
		return fmt.Errorf("failed to apply nodeClaim termination annotation: %w", err)
	}

	return nil
}
