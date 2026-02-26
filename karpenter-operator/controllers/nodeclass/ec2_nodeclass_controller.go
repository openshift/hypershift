package nodeclass

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	"github.com/openshift/hypershift/karpenter-operator/controllers/karpenter/assets"
	supportassets "github.com/openshift/hypershift/support/assets"
	"github.com/openshift/hypershift/support/config"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	awskarpenterapis "github.com/aws/karpenter-provider-aws/pkg/apis"
	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	finalizer = "hypershift.openshift.io/ec2-nodeclass-finalizer"

	// DefaultRootVolumeSize is 120Gi because HCP NodePools provisioned with HCP CLI are set with 120Gi root volume by default.
	// https://github.com/openshift/hypershift/blob/8be1d9c6f8f79106444e48f2b7d0069b942ba0d7/cmd/nodepool/aws/create.go#L30
	DefaultRootVolumeSize = "120Gi"
)

var (
	errKarpenterUserDataSecretNotFound = errors.New("failed to find user data secret for OpenshiftEC2NodeClass")
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
		Watches(&awskarpenterv1.EC2NodeClass{}, &handler.EnqueueRequestForObject{}).
		// Watch secrets in the management cluster and reconcile all ec2nodeclasses
		WatchesRawSource(source.Kind[client.Object](managementCluster.GetCache(), &corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.mapToOpenShiftEC2NodeClasses),
			r.karpenterSecretPredicate())).
		// Watch HostedControlPlane for annotation changes
		WatchesRawSource(source.Kind[client.Object](managementCluster.GetCache(), &hyperv1.HostedControlPlane{},
			handler.EnqueueRequestsFromMapFunc(r.mapToOpenShiftEC2NodeClasses),
			r.hcpAnnotationPredicate()))
	return bldr.Complete(r)
}

func (r *EC2NodeClassReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling", "req", req)

	hcp, err := karpenterutil.GetHCP(ctx, r.managementClient, r.Namespace)
	if err != nil {
		if errors.Is(err, karpenterutil.ErrHCPNotFound) {
			log.Info("HostedControlPlane not found, requeueing")
			return ctrl.Result{RequeueAfter: time.Second * 5}, nil
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

	userDataSecret, err := r.getUserDataSecret(ctx, openshiftEC2NodeClass)
	if err != nil {
		if errors.Is(err, errKarpenterUserDataSecretNotFound) {
			// Don't treat this as an error
			// ec2nodeclass controller might have been faster than karpenterignition controller to generate the user data secret
			log.Info(err.Error())
			return ctrl.Result{RequeueAfter: time.Second * 1}, nil
		}
		return ctrl.Result{}, err
	}

	if _, err := r.CreateOrUpdate(ctx, r.guestClient, ec2NodeClass, func() error {
		return reconcileEC2NodeClass(ctx, ec2NodeClass, openshiftEC2NodeClass, hcp, userDataSecret)
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

func reconcileEC2NodeClass(ctx context.Context, ec2NodeClass *awskarpenterv1.EC2NodeClass, openshiftEC2NodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass, hcp *hyperv1.HostedControlPlane, userDataSecret *corev1.Secret) error {
	ownerRef := config.OwnerRefFrom(openshiftEC2NodeClass)
	ownerRef.ApplyTo(ec2NodeClass)

	ec2NodeClass.Spec = awskarpenterv1.EC2NodeClassSpec{
		UserData:  ptr.To(string(userDataSecret.Data["value"])),
		AMIFamily: ptr.To("Custom"),
		AMISelectorTerms: []awskarpenterv1.AMISelectorTerm{
			{
				ID: string(userDataSecret.Labels[hyperkarpenterv1.UserDataAMILabel]),
			},
		},
		AssociatePublicIPAddress: openshiftEC2NodeClass.Spec.AssociatePublicIPAddress,
		Tags:                     mergeEC2NodeClassTags(ctx, openshiftEC2NodeClass, hcp),
		DetailedMonitoring:       openshiftEC2NodeClass.Spec.DetailedMonitoring,
		BlockDeviceMappings:      openshiftEC2NodeClass.Spec.KarpenterBlockDeviceMapping(),
		InstanceStorePolicy:      openshiftEC2NodeClass.Spec.KarpenterInstanceStorePolicy(),
	}

	// Set instance profile from HostedCluster annotation (platform-controlled)
	if instanceProfile, ok := hcp.Annotations[hyperv1.AWSKarpenterDefaultInstanceProfile]; ok && instanceProfile != "" {
		ec2NodeClass.Spec.InstanceProfile = ptr.To(instanceProfile)
	}

	// Set default BlockDeviceMappings if not specified in OpenshiftEC2NodeClass.
	if ec2NodeClass.Spec.BlockDeviceMappings == nil {
		ec2NodeClass.Spec.BlockDeviceMappings = []*awskarpenterv1.BlockDeviceMapping{
			{
				DeviceName: ptr.To("/dev/xvda"),
				EBS: &awskarpenterv1.BlockDevice{
					VolumeSize: ptr.To(resource.MustParse(DefaultRootVolumeSize)),
					VolumeType: ptr.To("gp3"),
					Encrypted:  ptr.To(true),
				},
			},
		}
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

	// Preserve fields managed by the ignition controller before resetting status
	resolvedReleaseImage := openshiftNodeClass.Status.ReleaseImage
	resolvedVersion := openshiftNodeClass.Status.Version
	preservedConditions := make([]metav1.Condition, 0)
	for _, c := range openshiftNodeClass.Status.Conditions {
		if c.Type == hyperkarpenterv1.ConditionTypeVersionResolved || c.Type == hyperkarpenterv1.ConditionTypeSupportedVersionSkew {
			preservedConditions = append(preservedConditions, c)
		}
	}

	openshiftNodeClass.Status = hyperkarpenterv1.OpenshiftEC2NodeClassStatus{}
	openshiftNodeClass.Status.ReleaseImage = resolvedReleaseImage
	openshiftNodeClass.Status.Version = resolvedVersion
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
	// Re-add conditions managed by the ignition controller
	openshiftNodeClass.Status.Conditions = append(openshiftNodeClass.Status.Conditions, preservedConditions...)

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

func (r *EC2NodeClassReconciler) getUserDataSecret(ctx context.Context, openshiftEC2NodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass) (*corev1.Secret, error) {
	labelSelector := labels.SelectorFromSet(labels.Set{karpenterutil.ManagedByKarpenterLabel: "true"})
	listOptions := &client.ListOptions{
		LabelSelector: labelSelector,
		Namespace:     r.Namespace,
	}
	secretList := &corev1.SecretList{}
	err := r.managementClient.List(ctx, secretList, listOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to list secrets: %w", err)
	}

	expectedNodePoolName := karpenterutil.KarpenterNodePoolName(openshiftEC2NodeClass)

	for _, secret := range secretList.Items {
		annotations := secret.GetAnnotations()
		if annotations == nil || annotations[hyperkarpenterv1.TokenSecretNodePoolAnnotation] == "" {
			continue
		}
		// we want the userData secret, not the token secret
		if annotations[nodepool.TokenSecretAnnotation] == "true" {
			continue
		}
		nodePoolAnnotation := util.ParseNamespacedName(annotations[hyperkarpenterv1.TokenSecretNodePoolAnnotation])
		if nodePoolAnnotation.Name == expectedNodePoolName {
			return &secret, nil
		}
	}

	return nil, fmt.Errorf("%w: expectedNodePoolName: %s, nodeclassName: %s", errKarpenterUserDataSecretNotFound, expectedNodePoolName, openshiftEC2NodeClass.Name)
}

// karpenterSecretPredicate only returns true on creates/updates on secrets with ManagedByKarpenterLabel
func (r *EC2NodeClassReconciler) karpenterSecretPredicate() predicate.Predicate {
	filterKarpenterSecret := func(obj client.Object) bool {
		if obj.GetNamespace() != r.Namespace {
			return false
		}
		if secret, ok := obj.(*corev1.Secret); ok {
			labels := secret.GetLabels()
			if labels != nil && labels[karpenterutil.ManagedByKarpenterLabel] == "true" {
				return true
			}
		}
		return false
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return filterKarpenterSecret(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return filterKarpenterSecret(e.ObjectNew) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return false },
		GenericFunc: func(e event.GenericEvent) bool { return false },
	}
}

// hcpAnnotationPredicate filters HostedControlPlane events to only trigger reconciliation when the
// AWSKarpenterDefaultInstanceProfile annotation changes
func (r *EC2NodeClassReconciler) hcpAnnotationPredicate() predicate.Predicate {
	filterHCP := func(obj client.Object) bool {
		if obj.GetNamespace() != r.Namespace {
			return false
		}
		if hcp, ok := obj.(*hyperv1.HostedControlPlane); ok {
			// Trigger if the annotation exists
			if _, exists := hcp.Annotations[hyperv1.AWSKarpenterDefaultInstanceProfile]; exists {
				return true
			}
		}
		return false
	}

	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return filterHCP(e.Object)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldHCP, oldOK := e.ObjectOld.(*hyperv1.HostedControlPlane)
			newHCP, newOK := e.ObjectNew.(*hyperv1.HostedControlPlane)
			if oldOK && newOK {
				if e.ObjectNew.GetNamespace() != r.Namespace {
					return false
				}
				oldVal := oldHCP.Annotations[hyperv1.AWSKarpenterDefaultInstanceProfile]
				newVal := newHCP.Annotations[hyperv1.AWSKarpenterDefaultInstanceProfile]
				return oldVal != newVal
			}
			return false
		},
		DeleteFunc:  func(e event.DeleteEvent) bool { return false },
		GenericFunc: func(e event.GenericEvent) bool { return false },
	}
}

// mapToOpenShiftEC2NodeClasses maps a request to all OpenshiftEC2NodeClass resources
func (r *EC2NodeClassReconciler) mapToOpenShiftEC2NodeClasses(ctx context.Context, obj client.Object) []reconcile.Request {
	openshiftEC2NodeClassList := &hyperkarpenterv1.OpenshiftEC2NodeClassList{}
	if err := r.guestClient.List(ctx, openshiftEC2NodeClassList); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list OpenShiftEC2NodeClass")
		return []reconcile.Request{}
	}

	var requests []reconcile.Request
	for _, nodeClass := range openshiftEC2NodeClassList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKey{
				Name:      nodeClass.Name,
				Namespace: nodeClass.Namespace,
			},
		})
	}

	return requests
}

// mergeEC2NodeClassTags merges platform tags from HostedControlPlane with OpenshiftEC2NodeClass tags.
// Platform tags take precedence over nodeclass tags in case of conflicts.
// Tags matching Karpenter's restricted patterns are filtered out to prevent validation errors.
// Karpenter restricts the patterns because it manages those tags itself, so the result is not "the karpenter-managed tags won't be present",
// the result is "the tags will still be present and managed by Karpenter"
func mergeEC2NodeClassTags(ctx context.Context, openshiftEC2NodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass, hcp *hyperv1.HostedControlPlane) map[string]string {
	log := ctrl.LoggerFrom(ctx)
	tags := make(map[string]string)

	// First add nodeclass tags
	for k, v := range openshiftEC2NodeClass.Spec.Tags {
		tags[k] = v
	}

	// Then add platform tags (these will override any conflicts)
	if hcp.Spec.Platform.AWS != nil {
		for _, tag := range hcp.Spec.Platform.AWS.ResourceTags {
			tags[tag.Key] = tag.Value
		}
	}

	// Filter out restricted tags that Karpenter manages automatically
	filteredTags, removedTags := filterRestrictedTags(tags)

	if len(removedTags) > 0 {
		log.V(4).Info("Filtered restricted Karpenter tags", "removedTags", removedTags)
	}

	// If we were nil coming in, we should be nil going out, test case comparisons care, {} is
	// not the same as nil
	if openshiftEC2NodeClass.Spec.Tags == nil && len(filteredTags) == 0 {
		return nil
	}

	return filteredTags
}

// filterRestrictedTags removes tags that match Karpenter's restricted tag patterns.
// Karpenter manages certain tags automatically and prohibits users from setting them.
// Returns a new map with restricted tags filtered out and a slice of removed tag keys.
func filterRestrictedTags(tags map[string]string) (map[string]string, []string) {
	if len(tags) == 0 {
		return tags, nil
	}

	filteredTags := make(map[string]string)
	removedTags := []string{}

	for key, value := range tags {
		isRestricted := false
		for _, pattern := range awskarpenterv1.RestrictedTagPatterns {
			if pattern.MatchString(key) {
				isRestricted = true
				removedTags = append(removedTags, key)
				break
			}
		}
		if !isRestricted {
			filteredTags[key] = value
		}
	}

	return filteredTags, removedTags
}
