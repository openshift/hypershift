package karpenter

import (
	"context"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	"github.com/openshift/hypershift/karpenter-operator/controllers/karpenter/assets"
	supportassets "github.com/openshift/hypershift/support/assets"
	"github.com/openshift/hypershift/support/upsert"

	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"

	appsv1 "k8s.io/api/apps/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if hcp.DeletionTimestamp != nil {
		if controllerutil.ContainsFinalizer(hcp, karpenterFinalizer) {
			nodePoolList := &karpenterv1.NodePoolList{}
			if err := r.GuestClient.List(ctx, nodePoolList); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to list NodePools: %w", err)
			}

			for _, nodePool := range nodePoolList.Items {
				// If we still get the NodePool, but it's already marked as terminating, we don't need to call Delete again
				if !nodePool.GetDeletionTimestamp().IsZero() {
					continue
				}
				if err := r.GuestClient.Delete(ctx, &nodePool, &client.DeleteOptions{
					PropagationPolicy: ptr.To(metav1.DeletePropagationForeground),
				}); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to delete NodePool: %w", err)
				}
			}
			// Wait until all NodePools are deleted before removing the finalizer
			if len(nodePoolList.Items) > 0 {
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}

			nodeClaimList := &karpenterv1.NodeClaimList{}
			// Make sure all NodeClaims are actually gone
			if err := r.GuestClient.List(ctx, nodeClaimList); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to list NodeClaims: %w", err)
			}
			if len(nodeClaimList.Items) > 0 {
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}

			originalHCP := hcp.DeepCopy()
			controllerutil.RemoveFinalizer(hcp, karpenterFinalizer)
			if err := r.ManagementClient.Patch(ctx, hcp, client.MergeFromWithOptions(originalHCP, client.MergeFromWithOptimisticLock{})); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from cluster: %w", err)
			}
			log.Info("Successfully removed all Karpenter NodePools and NodeClaims")
		}
		return ctrl.Result{}, nil
	}
	if !controllerutil.ContainsFinalizer(hcp, karpenterFinalizer) {
		originalHCP := hcp.DeepCopy()
		controllerutil.AddFinalizer(hcp, karpenterFinalizer)
		if err := r.ManagementClient.Patch(ctx, hcp, client.MergeFromWithOptions(originalHCP, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to hostedControlPlane: %w", err)
		}
	}

	if err := r.reconcileOpenshiftEC2NodeClassDefault(ctx, hcp); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileKarpenter(ctx, hcp); err != nil {
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
	ec2NodeClass.SetName("default")

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
