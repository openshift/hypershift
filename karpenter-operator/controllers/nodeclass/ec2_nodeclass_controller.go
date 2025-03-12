package nodeclass

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	"github.com/openshift/hypershift/karpenter-operator/controllers/karpenter/assets"
	supportassets "github.com/openshift/hypershift/support/assets"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	awskarpenterapis "github.com/aws/karpenter-provider-aws/pkg/apis"
	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// userDataAMILabel is a label set in the userData secret generated for karpenter instances.
	userDataAMILabel = "hypershift.openshift.io/ami"

	finalizer = "hypershift.openshift.io/ec2-nodeclass-finalizer"
)

var (
	crdEC2NodeClass = supportassets.MustCRD(assets.ReadFile, "karpenter.k8s.aws_ec2nodeclasses.yaml")

	crdOpenshiftEC2NodeClass = supportassets.MustCRD(assets.ReadFile, "karpenter.hypershift.openshift.io_openshiftec2nodeclasses.yaml")
)

type EC2NodeClassReconciler struct {
	Namespace string

	managementClient client.Client
	guestClient      client.Client
	upsert.CreateOrUpdateProvider
}

func (r *EC2NodeClassReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, managementCluster cluster.Cluster) error {
	r.managementClient = managementCluster.GetClient()
	r.guestClient = mgr.GetClient()
	r.CreateOrUpdateProvider = upsert.New(false)

	// First install the CRDs so we can create a watch below.
	if err := r.reconcileCRDs(ctx, true); err != nil {
		return err
	}

	bldr := ctrl.NewControllerManagedBy(mgr).
		For(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 10,
		}).
		Watches(&apiextensionsv1.CustomResourceDefinition{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, o client.Object) []ctrl.Request {
				// Only watch our Karpenter CRDs
				switch o.GetName() {
				case "ec2nodeclasses.karpenter.k8s.aws",
					"openshiftec2nodeclasses.karpenter.hypershift.openshift.io":
					return []ctrl.Request{{NamespacedName: client.ObjectKey{Namespace: r.Namespace}}}
				}
				return nil
			},
		)).
		Watches(&awskarpenterv1.EC2NodeClass{}, &handler.EnqueueRequestForObject{})

	return bldr.Complete(r)
}

func (r *EC2NodeClassReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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

	openshiftEC2NodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
	if err := r.guestClient.Get(ctx, req.NamespacedName, openshiftEC2NodeClass); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("openshiftEC2NodeClass not found, aborting reconcile", "name", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get openshiftEC2NodeClass %q: %w", req.NamespacedName, err)
	}

	ec2NodeClass := &awskarpenterv1.EC2NodeClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:      openshiftEC2NodeClass.Name,
			Namespace: openshiftEC2NodeClass.Namespace,
		},
	}

	if !openshiftEC2NodeClass.DeletionTimestamp.IsZero() {
		exists, err := util.DeleteIfNeeded(ctx, r.guestClient, ec2NodeClass)
		if err != nil {
			return ctrl.Result{}, err
		}
		if exists {
			// wait until EC2NodeClass is deleted
			return ctrl.Result{RequeueAfter: time.Second * 5}, nil
		}

		if controllerutil.ContainsFinalizer(openshiftEC2NodeClass, finalizer) {
			original := openshiftEC2NodeClass.DeepCopy()
			controllerutil.RemoveFinalizer(openshiftEC2NodeClass, finalizer)
			if err := r.guestClient.Patch(ctx, openshiftEC2NodeClass, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from openshiftEC2NodeClass: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(hcp, finalizer) {
		original := openshiftEC2NodeClass.DeepCopy()
		controllerutil.AddFinalizer(openshiftEC2NodeClass, finalizer)
		if err := r.guestClient.Patch(ctx, openshiftEC2NodeClass, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to openshiftEC2NodeClass: %w", err)
		}
	}

	userDataSecret, err := r.getUserDataSecret(ctx, hcp)
	if err != nil {
		return ctrl.Result{}, err
	}

	if _, err := r.CreateOrUpdate(ctx, r.guestClient, ec2NodeClass, func() error {
		return reconcileEC2NodeClass(ec2NodeClass, openshiftEC2NodeClass, hcp, userDataSecret)
	}); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileStatus(ctx, ec2NodeClass, openshiftEC2NodeClass); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileVAP(ctx); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reconcileCRDs reconcile the Karpenter CRDs, if onlyCreate is true it uses an only write non cached client.
func (r *EC2NodeClassReconciler) reconcileCRDs(ctx context.Context, onlyCreate bool) error {
	log := ctrl.LoggerFrom(ctx)

	errs := []error{}
	var op controllerutil.OperationResult
	var err error
	for _, crd := range []*apiextensionsv1.CustomResourceDefinition{
		crdEC2NodeClass,
		crdOpenshiftEC2NodeClass,
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

func reconcileEC2NodeClass(ec2NodeClass *awskarpenterv1.EC2NodeClass, openshiftEC2NodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass, hcp *hyperv1.HostedControlPlane, userDataSecret *corev1.Secret) error {
	ownerRef := config.OwnerRefFrom(openshiftEC2NodeClass)
	ownerRef.ApplyTo(ec2NodeClass)

	ec2NodeClass.Spec = awskarpenterv1.EC2NodeClassSpec{
		UserData:  ptr.To(string(userDataSecret.Data["value"])),
		AMIFamily: ptr.To("Custom"),
		AMISelectorTerms: []awskarpenterv1.AMISelectorTerm{
			{
				ID: string(userDataSecret.Labels[userDataAMILabel]),
			},
		},
		AssociatePublicIPAddress: openshiftEC2NodeClass.Spec.AssociatePublicIPAddress,
		Tags:                     openshiftEC2NodeClass.Spec.Tags,
		DetailedMonitoring:       openshiftEC2NodeClass.Spec.DetailedMonitoring,
		BlockDeviceMappings:      openshiftEC2NodeClass.Spec.KarpenterBlockDeviceMapping(),
		InstanceStorePolicy:      openshiftEC2NodeClass.Spec.KarpenterInstanceStorePolicy(),
	}

	var subnetSelectorTerms []awskarpenterv1.SubnetSelectorTerm
	if openshiftEC2NodeClass.Spec.SubnetSelectorTerms != nil {
		for _, term := range openshiftEC2NodeClass.Spec.SubnetSelectorTerms {
			subnetSelectorTerms = append(subnetSelectorTerms, awskarpenterv1.SubnetSelectorTerm{
				Tags: term.Tags,
				ID:   term.ID,
			})
		}
	} else {
		subnetSelectorTerms = []awskarpenterv1.SubnetSelectorTerm{
			{
				Tags: map[string]string{
					"karpenter.sh/discovery": hcp.Spec.InfraID,
				},
			},
		}
	}
	ec2NodeClass.Spec.SubnetSelectorTerms = subnetSelectorTerms

	var securityGroupSelectorTerms []awskarpenterv1.SecurityGroupSelectorTerm
	if openshiftEC2NodeClass.Spec.SecurityGroupSelectorTerms != nil {
		for _, term := range openshiftEC2NodeClass.Spec.SecurityGroupSelectorTerms {
			securityGroupSelectorTerms = append(securityGroupSelectorTerms, awskarpenterv1.SecurityGroupSelectorTerm{
				Tags: term.Tags,
				ID:   term.ID,
				Name: term.Name,
			})
		}
	} else {
		securityGroupSelectorTerms = []awskarpenterv1.SecurityGroupSelectorTerm{
			{
				Tags: map[string]string{
					"karpenter.sh/discovery": hcp.Spec.InfraID,
				},
			},
		}
	}
	ec2NodeClass.Spec.SecurityGroupSelectorTerms = securityGroupSelectorTerms

	return nil
}

func (r *EC2NodeClassReconciler) reconcileStatus(ctx context.Context, ec2NodeClass *awskarpenterv1.EC2NodeClass, openshiftNodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass) error {
	log := ctrl.LoggerFrom(ctx)

	originalObj := openshiftNodeClass.DeepCopy()

	openshiftNodeClass.Status = hyperkarpenterv1.OpenshiftEC2NodeClassStatus{}
	for _, securityGroup := range ec2NodeClass.Status.SecurityGroups {
		openshiftNodeClass.Status.SecurityGroups = append(openshiftNodeClass.Status.SecurityGroups, hyperkarpenterv1.SecurityGroup{
			ID:   securityGroup.ID,
			Name: securityGroup.Name,
		})
	}
	for _, subnet := range ec2NodeClass.Status.Subnets {
		openshiftNodeClass.Status.Subnets = append(openshiftNodeClass.Status.Subnets, hyperkarpenterv1.Subnet{
			ID:     subnet.ID,
			Zone:   subnet.Zone,
			ZoneID: subnet.ZoneID,
		})
	}
	for _, condition := range ec2NodeClass.Status.Conditions {
		openshiftNodeClass.Status.Conditions = append(openshiftNodeClass.Status.Conditions, metav1.Condition(condition))
	}

	if !reflect.DeepEqual(originalObj.Status, openshiftNodeClass.Status) {
		if err := r.guestClient.Status().Patch(ctx, openshiftNodeClass, client.MergeFrom(originalObj)); err != nil {
			return fmt.Errorf("failed to update status: %v", err)
		}
	}

	log.Info("Reconciled OpenshiftEC2NodeClass status")
	return nil
}

func (r *EC2NodeClassReconciler) reconcileVAP(ctx context.Context) error {
	vap := &admissionv1.ValidatingAdmissionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "karpenter.ec2nodeclass.hypershift.io",
		},
	}

	if _, err := r.CreateOrUpdate(ctx, r.guestClient, vap, func() error {
		vap.Spec.MatchConstraints = &admissionv1.MatchResources{
			ResourceRules: []admissionv1.NamedRuleWithOperations{
				{
					RuleWithOperations: admissionv1.RuleWithOperations{
						Operations: []admissionv1.OperationType{
							admissionv1.OperationAll,
						},
						Rule: admissionv1.Rule{
							APIGroups:   []string{awskarpenterapis.Group},
							APIVersions: []string{"v1"},
							Resources:   []string{"ec2nodeclasses"},
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
				Message:    "EC2NodeClass resource can't be created/updated/deleted directly, please use OpenshiftEC2NodeClass resource instead",
			},
		}

		return nil
	}); err != nil {
		return err
	}

	vapBinding := &admissionv1.ValidatingAdmissionPolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "karpenter-binding.ec2nodeclass.hypershift.io",
		},
	}
	_, err := r.CreateOrUpdate(ctx, r.guestClient, vapBinding, func() error {
		vapBinding.Spec.PolicyName = vap.Name
		vapBinding.Spec.ValidationActions = []admissionv1.ValidationAction{admissionv1.Deny}
		return nil
	})

	return err
}

func (r *EC2NodeClassReconciler) getUserDataSecret(ctx context.Context, hcp *hyperv1.HostedControlPlane) (*corev1.Secret, error) {
	labelSelector := labels.SelectorFromSet(labels.Set{hyperv1.NodePoolLabel: fmt.Sprintf("%s-karpenter", hcp.GetName())})
	listOptions := &client.ListOptions{
		LabelSelector: labelSelector,
		Namespace:     r.Namespace,
	}
	secretList := &corev1.SecretList{}
	err := r.managementClient.List(ctx, secretList, listOptions)
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

func (r *EC2NodeClassReconciler) getHCP(ctx context.Context) (*hyperv1.HostedControlPlane, error) {
	hcpList := &hyperv1.HostedControlPlaneList{}
	if err := r.managementClient.List(ctx, hcpList, client.InNamespace(r.Namespace)); err != nil {
		return nil, err
	}
	if len(hcpList.Items) == 0 {
		return nil, fmt.Errorf("failed to find HostedControlPlane in namespace %s", r.Namespace)
	}

	return &hcpList.Items[0], nil
}
