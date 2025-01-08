package karpenter

import (
	"context"
	"fmt"
	"sort"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/karpenter-operator/controllers/karpenter/assets"
	supportassets "github.com/openshift/hypershift/support/assets"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	karpenterFinalizer = "hypershift.openshift.io/karpenter-finalizer"
	// userDataAMILabel is a label set in the userData secret generated for karpenter instances.
	userDataAMILabel = "hypershift.openshift.io/ami"
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
	if err := c.Watch(source.Kind[client.Object](mgr.GetCache(), &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "karpenter.k8s.aws/v1",
		"kind":       "EC2NodeClass",
	}}, &handler.EnqueueRequestForObject{})); err != nil {
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
		// TODO(alberto): implement deletion. E.g. loop over nodeClaims delete them, wait and delete karpeneter deployment.
		if controllerutil.ContainsFinalizer(hcp, karpenterFinalizer) {
			originalHCP := hcp.DeepCopy()
			controllerutil.RemoveFinalizer(hcp, karpenterFinalizer)
			if err := r.ManagementClient.Patch(ctx, hcp, client.MergeFromWithOptions(originalHCP, client.MergeFromWithOptimisticLock{})); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from cluster: %w", err)
			}
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

	// TODO(alberto):
	// - reconcile validatingAdmissionPolicy to enforce shared ownership.
	// - Watch userDataSecret.
	// - Solve token rotation causing drift.
	// - CSR approval.

	userDataSecret, err := r.getUserDataSecret(ctx, hcp)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileEC2NodeClassOwnedFields(ctx, userDataSecret); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileEC2NodeClassDefault(ctx, userDataSecret, hcp); err != nil {
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

func (r *Reconciler) reconcileEC2NodeClassOwnedFields(ctx context.Context, userDataSecret *corev1.Secret) error {
	log := ctrl.LoggerFrom(ctx)

	ec2NodeClassList := &unstructured.UnstructuredList{}
	ec2NodeClassList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "karpenter.k8s.aws",
		Version: "v1",
		Kind:    "EC2NodeClassList",
	})
	err := r.GuestClient.List(ctx, ec2NodeClassList)
	if err != nil {
		return fmt.Errorf("failed to get EC2NodeClassList: %w", err)
	}

	errs := []error{}
	for _, ec2NodeClass := range ec2NodeClassList.Items {
		ec2NodeClass.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "karpenter.k8s.aws",
			Version: "v1",
			Kind:    "EC2NodeClass",
		})
		op, err := r.CreateOrUpdate(ctx, r.GuestClient, &ec2NodeClass, func() error {
			ec2NodeClass.Object["spec"].(map[string]interface{})["userData"] = string(userDataSecret.Data["value"])
			ec2NodeClass.Object["spec"].(map[string]interface{})["amiFamily"] = "Custom"
			ec2NodeClass.Object["spec"].(map[string]interface{})["amiSelectorTerms"] = []map[string]interface{}{
				{
					"id": string(userDataSecret.Labels[userDataAMILabel]),
				},
			}
			return nil
		})
		if err != nil {
			errs = append(errs, err)
		}
		if err == nil {
			log.Info("Set managed fields in ec2NodeClass", "ec2NodeClass", ec2NodeClass.GetName(), "op", op)
		}
	}
	if err := utilerrors.NewAggregate(errs); err != nil {
		return fmt.Errorf("failed to update EC2NodeClass: %w", err)
	}
	return nil
}

func (r *Reconciler) reconcileEC2NodeClassDefault(ctx context.Context, userDataSecret *corev1.Secret, hcp *hyperv1.HostedControlPlane) error {
	log := ctrl.LoggerFrom(ctx)

	// Create an unstructured object for the EC2NodeClass
	ec2NodeClass := &unstructured.Unstructured{}
	ec2NodeClass.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "karpenter.k8s.aws",
		Version: "v1",
		Kind:    "EC2NodeClass",
	})
	ec2NodeClass.SetName("default")

	op, err := r.CreateOrUpdate(ctx, r.GuestClient, ec2NodeClass, func() error {
		ec2NodeClass.Object["spec"] = map[string]interface{}{}
		ec2NodeClass.Object["spec"].(map[string]interface{})["role"] = "KarpenterNodeRole-agl" // TODO(alberto): set a convention for this e.g. openshift-karpenter-infraID
		ec2NodeClass.Object["spec"].(map[string]interface{})["userData"] = string(userDataSecret.Data["value"])
		ec2NodeClass.Object["spec"].(map[string]interface{})["amiFamily"] = "Custom"
		ec2NodeClass.Object["spec"].(map[string]interface{})["amiSelectorTerms"] = []map[string]interface{}{
			{
				"id": string(userDataSecret.Labels[userDataAMILabel]),
			},
		}
		ec2NodeClass.Object["spec"].(map[string]interface{})["subnetSelectorTerms"] = []map[string]interface{}{
			{
				"tags": map[string]interface{}{
					"karpenter.sh/discovery": hcp.Spec.InfraID,
				},
			},
		}
		ec2NodeClass.Object["spec"].(map[string]interface{})["securityGroupSelectorTerms"] = []map[string]interface{}{
			{
				"tags": map[string]interface{}{
					"karpenter.sh/discovery": hcp.Spec.InfraID,
				},
			},
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile default EC2NodeClass: %w", err)
	}
	log.Info("Reconciled default EC2NodeClass", "op", op)
	return nil
}

func (r *Reconciler) getUserDataSecret(ctx context.Context, hcp *hyperv1.HostedControlPlane) (*corev1.Secret, error) {
	labelSelector := labels.SelectorFromSet(labels.Set{hyperv1.NodePoolLabel: fmt.Sprintf("%s-karpenter", hcp.GetName())})
	listOptions := &client.ListOptions{
		LabelSelector: labelSelector,
		Namespace:     r.Namespace,
	}
	secretList := &corev1.SecretList{}
	err := r.ManagementClient.List(ctx, secretList, listOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to list secrets: %w", err)
	}

	sort.Slice(secretList.Items, func(i, j int) bool {
		return secretList.Items[i].CreationTimestamp.After(secretList.Items[j].CreationTimestamp.Time)
	})
	if len(secretList.Items) < 1 {
		return nil, fmt.Errorf("expected 1 secret, got 0")
	}
	return &secretList.Items[0], err
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
