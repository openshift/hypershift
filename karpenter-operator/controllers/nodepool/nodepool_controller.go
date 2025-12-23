package nodepool

import (
	"context"
	"fmt"
	"reflect"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/globalps"
	"github.com/openshift/hypershift/karpenter-operator/controllers/karpenter/assets"
	supportassets "github.com/openshift/hypershift/support/assets"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/util/workqueue"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	karpenterapis "sigs.k8s.io/karpenter/pkg/apis"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

const (
	finalizer = "hypershift.openshift.io/nodepool-finalizer"
)

var (
	crdNodePool          = supportassets.MustCRD(assets.ReadFile, "karpenter.sh_nodepools.yaml")
	crdOpenshiftNodePool = supportassets.MustCRD(assets.ReadFile, "karpenter.hypershift.openshift.io_openshiftnodepools.yaml")
)

type NodePoolReconciler struct {
	Namespace string

	managementClient client.Client
	guestClient      client.Client
	upsert.CreateOrUpdateProvider
}

func (r *NodePoolReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, managementCluster cluster.Cluster) error {
	r.managementClient = managementCluster.GetClient()
	r.guestClient = mgr.GetClient()
	r.CreateOrUpdateProvider = upsert.New(false)

	if err := r.reconcileCRDs(ctx, true); err != nil {
		return err
	}

	if err := r.reconcileVAP(ctx, true); err != nil {
		return err
	}

	bldr := ctrl.NewControllerManagedBy(mgr).
		Named("nodepool").
		For(&hyperkarpenterv1.OpenshiftNodePool{}).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 10,
		}).
		Watches(&karpenterv1.NodePool{}, &handler.EnqueueRequestForObject{})

	return bldr.Complete(r)
}

func (r *NodePoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling", "req", req)

	hcp, err := r.getHCP(ctx)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if hcp.Annotations[hyperkarpenterv1.KarpenterCoreE2EOverrideAnnotation] == "true" {
		return ctrl.Result{}, nil
	}

	if err := r.reconcileCRDs(ctx, false); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileVAP(ctx, false); err != nil {
		return ctrl.Result{}, err
	}

	openshiftNodePool := &hyperkarpenterv1.OpenshiftNodePool{}
	if err := r.guestClient.Get(ctx, req.NamespacedName, openshiftNodePool); err != nil {
		if apierrors.IsNotFound(err) {
			// check if there is an offending orphaned NodePool
			exists, err := util.DeleteIfNeeded(ctx, r.guestClient, &karpenterv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      req.NamespacedName.Name,
					Namespace: req.NamespacedName.Namespace,
				},
			})
			if err != nil {
				return ctrl.Result{}, err
			}
			if exists {
				log.Info("Deleting orphaned NodePool", "name", req.NamespacedName)
				return ctrl.Result{}, nil
			}

			log.Info("OpenshiftNodePool not found, aborting reconcile", "name", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get OpenshiftNodePool %q: %w", req.NamespacedName, err)
	}

	nodePool := &karpenterv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      openshiftNodePool.Name,
			Namespace: openshiftNodePool.Namespace,
		},
	}

	if !openshiftNodePool.DeletionTimestamp.IsZero() {
		exists, err := util.DeleteIfNeeded(ctx, r.guestClient, nodePool)
		if err != nil {
			return ctrl.Result{}, err
		}
		if exists {
			// wait until NodePool is deleted
			return ctrl.Result{RequeueAfter: time.Second * 5}, nil
		}

		if controllerutil.ContainsFinalizer(openshiftNodePool, finalizer) {
			original := openshiftNodePool.DeepCopy()
			controllerutil.RemoveFinalizer(openshiftNodePool, finalizer)
			if err := r.guestClient.Patch(ctx, openshiftNodePool, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from OpenshiftNodePool: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(openshiftNodePool, finalizer) {
		original := openshiftNodePool.DeepCopy()
		controllerutil.AddFinalizer(openshiftNodePool, finalizer)
		if err := r.guestClient.Patch(ctx, openshiftNodePool, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to openshiftNodePool: %w", err)
		}
	}

	if _, err := r.CreateOrUpdate(ctx, r.guestClient, nodePool, func() error {
		return r.reconcileNodePool(nodePool, openshiftNodePool)
	}); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileStatus(ctx, nodePool, openshiftNodePool); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *NodePoolReconciler) reconcileCRDs(ctx context.Context, onlyCreate bool) error {
	log := ctrl.LoggerFrom(ctx)

	errs := []error{}
	var op controllerutil.OperationResult
	var err error
	for _, crd := range []*apiextensionsv1.CustomResourceDefinition{
		crdNodePool,
		crdOpenshiftNodePool,
	} {
		if onlyCreate {
			if err := r.guestClient.Create(ctx, crd); err != nil {
				if !apierrors.IsAlreadyExists(err) {
					errs = append(errs, err)
				}
			}
		} else {
			op, err = r.CreateOrUpdate(ctx, r.guestClient, crd, func() error {
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

func (r *NodePoolReconciler) reconcileNodePool(nodePool *karpenterv1.NodePool, openshiftNodePool *hyperkarpenterv1.OpenshiftNodePool) error {
	ownerRef := config.OwnerRefFrom(openshiftNodePool)
	ownerRef.ApplyTo(nodePool)

	nodePool.Spec.Template = openshiftNodePool.Spec.Template
	nodePool.Spec.Disruption = openshiftNodePool.Spec.Disruption
	nodePool.Spec.Limits = openshiftNodePool.Spec.Limits
	nodePool.Spec.Weight = openshiftNodePool.Spec.Weight

	// Auto-inject GlobalPullSecret label to enable the GlobalPullSecret DaemonSet on Karpenter nodes
	if nodePool.Spec.Template.ObjectMeta.Labels == nil {
		nodePool.Spec.Template.ObjectMeta.Labels = make(map[string]string)
	}
	nodePool.Spec.Template.ObjectMeta.Labels[globalps.GlobalPSLabelKey] = "true"

	// Translate OpenshiftEC2NodeClass reference to EC2NodeClass
	if openshiftNodePool.Spec.Template.Spec.NodeClassRef != nil && openshiftNodePool.Spec.Template.Spec.NodeClassRef.Kind == "OpenshiftEC2NodeClass" {
		nodePool.Spec.Template.Spec.NodeClassRef = &karpenterv1.NodeClassReference{
			Group: "karpenter.k8s.aws",
			Kind:  "EC2NodeClass",
			Name:  openshiftNodePool.Spec.Template.Spec.NodeClassRef.Name,
		}
	}

	return nil
}

func (r *NodePoolReconciler) reconcileStatus(ctx context.Context, nodePool *karpenterv1.NodePool, openshiftNodePool *hyperkarpenterv1.OpenshiftNodePool) error {
	log := ctrl.LoggerFrom(ctx)

	originalObj := openshiftNodePool.DeepCopy()

	// Copy status fields explicitly
	openshiftNodePool.Status.Resources = nodePool.Status.Resources
	openshiftNodePool.Status.Conditions = nodePool.Status.Conditions

	if !reflect.DeepEqual(originalObj.Status, openshiftNodePool.Status) {
		if err := r.guestClient.Status().Patch(ctx, openshiftNodePool, client.MergeFrom(originalObj)); err != nil {
			return fmt.Errorf("failed to update status: %v", err)
		}
	}

	log.Info("Reconciled OpenshiftNodePool status")
	return nil
}

func (r *NodePoolReconciler) reconcileVAP(ctx context.Context, onlyCreate bool) error {
	vap := &admissionv1.ValidatingAdmissionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "karpenter.nodepool.hypershift.io",
		},
	}

	setVAPSpec := func() {
		vap.Spec.MatchConstraints = &admissionv1.MatchResources{
			ResourceRules: []admissionv1.NamedRuleWithOperations{
				{
					RuleWithOperations: admissionv1.RuleWithOperations{
						Operations: []admissionv1.OperationType{
							admissionv1.OperationAll,
						},
						Rule: admissionv1.Rule{
							APIGroups:   []string{karpenterapis.Group},
							APIVersions: []string{"v1"},
							Resources:   []string{"nodepools"},
						},
					},
				},
			},
		}
		vap.Spec.MatchConditions = []admissionv1.MatchCondition{
			{
				Name:       "exclude-hcco-user",
				Expression: "'system:hosted-cluster-config' != request.userInfo.username",
			},
		}

		vap.Spec.Validations = []admissionv1.Validation{
			{
				Expression: "has(oldObject.spec) && has(object.spec) && object.spec == oldObject.spec",
				Message:    "NodePool resource can't be created/updated/deleted directly, please use OpenshiftNodePool resource instead",
			},
		}
	}

	vapBinding := &admissionv1.ValidatingAdmissionPolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "karpenter-binding.nodepool.hypershift.io",
		},
	}

	setVAPBindingSpec := func() {
		vapBinding.Spec.PolicyName = vap.Name
		vapBinding.Spec.ValidationActions = []admissionv1.ValidationAction{admissionv1.Deny}
	}

	if onlyCreate {
		setVAPSpec()
		if err := r.guestClient.Create(ctx, vap); err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
		setVAPBindingSpec()
		if err := r.guestClient.Create(ctx, vapBinding); err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
		return nil
	}

	if _, err := r.CreateOrUpdate(ctx, r.guestClient, vap, func() error {
		setVAPSpec()
		return nil
	}); err != nil {
		return err
	}

	_, err := r.CreateOrUpdate(ctx, r.guestClient, vapBinding, func() error {
		setVAPBindingSpec()
		return nil
	})

	return err
}

func (r *NodePoolReconciler) getHCP(ctx context.Context) (*hyperv1.HostedControlPlane, error) {
	hcpList := &hyperv1.HostedControlPlaneList{}
	if err := r.managementClient.List(ctx, hcpList, client.InNamespace(r.Namespace)); err != nil {
		return nil, err
	}
	if len(hcpList.Items) == 0 {
		return nil, apierrors.NewNotFound(
			hyperv1.GroupVersion.WithResource("hostedcontrolplanes").GroupResource(),
			r.Namespace,
		)
	}

	return &hcpList.Items[0], nil
}
