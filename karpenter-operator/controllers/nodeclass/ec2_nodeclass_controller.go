package nodeclass

import (
	"context"
	"fmt"
	"reflect"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	haproxy "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/apiserver-haproxy"
	"github.com/openshift/hypershift/karpenter-operator/controllers/karpenter/assets"
	supportassets "github.com/openshift/hypershift/support/assets"
	"github.com/openshift/hypershift/support/config"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	awskarpenterapis "github.com/aws/karpenter-provider-aws/pkg/apis"
	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"

	"github.com/blang/semver"
	"github.com/openshift/hypershift/support/supportedversion"
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

	// nodePoolAnnotationCurrentConfigVersion mirrors the annotation from nodepool_controller.go
	// It's used to track the current config version for outdated token cleanup
	// from hypershift-operator/controllers/nodepool/nodepool_controller.go
	nodePoolAnnotationCurrentConfigVersion = "hypershift.openshift.io/nodePoolCurrentConfigVersion"

	// openshiftEC2NodeClassAnnotationCurrentConfigVersion tracks the config version on the OpenshiftEC2NodeClass
	// since the NodePool is in-memory only and doesn't persist between reconciles
	openshiftEC2NodeClassAnnotationCurrentConfigVersion = "hypershift.openshift.io/currentConfigVersion"
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
	ReleaseProvider           releaseinfo.Provider
	ImageMetadataProvider     util.ImageMetadataProvider
	ControlPlaneOperatorImage string
	IgnitionEndpoint          string
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
			r.userDataSecretPredicate())).
		// Watch HostedControlPlane for annotation changes
		WatchesRawSource(source.Kind[client.Object](managementCluster.GetCache(), &hyperv1.HostedControlPlane{},
			handler.EnqueueRequestsFromMapFunc(r.mapToOpenShiftEC2NodeClasses),
			r.hcpAnnotationPredicate()))
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

	// The below code is used to create a config generator and in-memory NodePool for each EC2NodeClass.
	// This allows karpenter-operator to reconcile separate tokens, so that each NodeClass is able to track it's own openshift release image version.
	np := hcpNodePool(hcp, openshiftEC2NodeClass)

	// Get the correct release image based on the OpenshiftEC2NodeClass's openshiftVersion
	pullSpec, err := getReleaseImagePullSpec(ctx, hcp, openshiftEC2NodeClass)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get release image pullSpec: %w", err)
	}
	np.Spec.Release.Image = pullSpec

	cg, err := r.buildConfigGenerator(ctx, hcp, np)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.reconcileToken(ctx, cg, np, openshiftEC2NodeClass); err != nil {
		return ctrl.Result{}, err
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

	userDataSecret, err := r.getUserDataSecret(ctx, np)
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
				ID: string(userDataSecret.Labels[hyperkarpenterv1.UserDataAMILabel]),
			},
		},
		AssociatePublicIPAddress: openshiftEC2NodeClass.Spec.AssociatePublicIPAddress,
		Tags:                     openshiftEC2NodeClass.Spec.Tags,
		DetailedMonitoring:       openshiftEC2NodeClass.Spec.DetailedMonitoring,
		BlockDeviceMappings:      openshiftEC2NodeClass.Spec.KarpenterBlockDeviceMapping(),
		InstanceStorePolicy:      openshiftEC2NodeClass.Spec.KarpenterInstanceStorePolicy(),
	}

	// Set instance profile from HostedCluster annotation (platform-controlled)
	if instanceProfile, ok := hcp.Annotations[hyperv1.AWSKarpenterDefaultInstanceProfile]; ok && instanceProfile != "" {
		ec2NodeClass.Spec.InstanceProfile = ptr.To(instanceProfile)
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

	// Set default BlockDeviceMappings if not specified
	if ec2NodeClass.Spec.BlockDeviceMappings == nil {
		ec2NodeClass.Spec.BlockDeviceMappings = []*awskarpenterv1.BlockDeviceMapping{
			{
				DeviceName: ptr.To("/dev/xvda"),
				EBS: &awskarpenterv1.BlockDevice{
					VolumeSize: ptr.To(resource.MustParse("75Gi")),
					VolumeType: ptr.To("gp3"),
					Encrypted:  ptr.To(true),
				},
			},
		}
	}

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

func (r *EC2NodeClassReconciler) getUserDataSecret(ctx context.Context, np *hyperv1.NodePool) (*corev1.Secret, error) {
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

	// get the secret with the annotation namespaced name that matches the nodepool name
	for _, item := range secretList.Items {
		annotations := item.GetAnnotations()
		if annotations == nil || annotations[hyperkarpenterv1.TokenSecretNodePoolAnnotation] == "" {
			continue
		}
		if annotations[nodepool.TokenSecretAnnotation] == "true" {
			// we want the userData secret, not the token secret
			continue
		}
		nodePoolAnnotation := util.ParseNamespacedName(annotations[hyperkarpenterv1.TokenSecretNodePoolAnnotation])
		if nodePoolAnnotation.Name == np.GetName() {
			return &item, nil
		}
	}
	return nil, fmt.Errorf("failed to find user data secret for nodepool %s", np.GetName())
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

// hcpNodePool returns an in-memory hyperv1.NodePool for token generation.
func hcpNodePool(hcp *hyperv1.HostedControlPlane, openshiftEC2NodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass) *hyperv1.NodePool {
	return &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			// the nodepool needs to end with "karpenter", so the resulting secrets don't get cleaned up by the secret janitor
			Name:        fmt.Sprintf("%s-%s", openshiftEC2NodeClass.Name, hyperkarpenterv1.KarpenterNodePool),
			Namespace:   hcp.Namespace,
			Annotations: map[string]string{},
			Labels: map[string]string{
				karpenterutil.ManagedByKarpenterLabel: "true",
			},
		},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: hcp.Name,
			Replicas:    ptr.To[int32](0),
			Release: hyperv1.Release{
				Image: hcp.Spec.ReleaseImage,
			},
			Config: []corev1.LocalObjectReference{
				{
					Name: karpenterutil.KarpenterTaintConfigMapName,
				},
			},
			Arch: hyperv1.ArchitectureAMD64, // used to find default AMI
		},
	}
}

// hostedClusterFromHCP creates a barebones in-memory HostedCluster from a HostedControlPlane.
// This allows reusing HostedCluster-based code paths to build a ConfigGenerator without needing the actual HostedCluster object.
// NOTE: The Namespace field is set to hcp.Namespace (the HCP namespace) rather than the original HC namespace.
// This ensures secret lookups work correctly since the operator is only allowed to read secrets in the HCP namespace.
func hostedClusterFromHCP(hcp *hyperv1.HostedControlPlane) *hyperv1.HostedCluster {
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        hcp.Name,
			Namespace:   hcp.Namespace, // Use HCP namespace for secret lookups
			Annotations: hcp.Annotations,
			Labels:      hcp.Labels,
		},
		Spec: hyperv1.HostedClusterSpec{
			Release: hyperv1.Release{
				Image: hcp.Spec.ReleaseImage,
			},
			ClusterID:             hcp.Spec.ClusterID,
			InfraID:               hcp.Spec.InfraID,
			Platform:              hcp.Spec.Platform,
			Networking:            hcp.Spec.Networking,
			PullSecret:            hcp.Spec.PullSecret,
			Services:              hcp.Spec.Services,
			Configuration:         hcp.Spec.Configuration,
			AdditionalTrustBundle: hcp.Spec.AdditionalTrustBundle,
			ImageContentSources:   hcp.Spec.ImageContentSources,
			Capabilities:          hcp.Spec.Capabilities,
			AutoNode:              hcp.Spec.AutoNode,
		},
	}

	if hcp.Spec.ControlPlaneReleaseImage != nil {
		hc.Spec.ControlPlaneRelease = &hyperv1.Release{
			Image: *hcp.Spec.ControlPlaneReleaseImage,
		}
	}

	// Convert HCP's KubeconfigSecretRef to LocalObjectReference (only Name is needed)
	if hcp.Status.KubeConfig != nil {
		hc.Status.KubeConfig = &corev1.LocalObjectReference{
			Name: hcp.Status.KubeConfig.Name,
		}
	}

	return hc
}

// adapted from hypershift-operator/controllers/nodepool/conditions.go#supportedVersionSkewCondition
func getHCPControlPlaneVersion(hcp *hyperv1.HostedControlPlane) (semver.Version, error) {
	// Get the control plane version string
	var controlPlaneVersion string
	if len(hcp.Status.VersionStatus.History) == 0 {
		// If the cluster is in the process of installation, there is no history
		// Use the desired version as the control plane version
		controlPlaneVersion = hcp.Status.VersionStatus.Desired.Version
	} else {
		// If the cluster is installed or upgrading
		// Start with the most recent version from history as the default
		controlPlaneVersion = hcp.Status.VersionStatus.History[0].Version
		// Find the most recent Completed version
		for _, history := range hcp.Status.VersionStatus.History {
			if history.State == "Completed" {
				controlPlaneVersion = history.Version
				break
			}
		}
	}
	return semver.Parse(controlPlaneVersion)
}

// getReleaseImagePullSpec returns the release image pullSpec for the given OpenshiftEC2NodeClass.
// If OpenshiftVersion is not specified, it falls back to the control plane version.
// It also validates version skew between the nodeclass version and the control plane.
func getReleaseImagePullSpec(ctx context.Context, hcp *hyperv1.HostedControlPlane, openshiftEC2NodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass) (string, error) {
	var nodeVersion semver.Version
	var err error
	if openshiftEC2NodeClass.Spec.OpenshiftVersion != nil {
		nodeVersion, err = semver.Parse(*openshiftEC2NodeClass.Spec.OpenshiftVersion)
		if err != nil {
			return "", fmt.Errorf("failed to parse OpenshiftVersion: %w", err)
		}
	} else {
		// Fall back to control plane version
		return hcp.Status.VersionStatus.Desired.Image, nil
	}

	controlPlaneVersion, err := getHCPControlPlaneVersion(hcp)
	if err != nil {
		return "", fmt.Errorf("failed to get control plane version: %w", err)
	}

	if err := supportedversion.ValidateVersionSkew(&controlPlaneVersion, &nodeVersion); err != nil {
		return "", fmt.Errorf("version skew validation failed: %w", err)
	}

	// TODO(maxcao13): currently we don't validate versions other than skew
	// supportedversion.IsValidReleaseVersion(&wantedVersion, &currentVersionParsed, hostedClusterVersion, &minSupportedVersion, hostedCluster.Spec.Networking.NetworkType, hostedCluster.Spec.Platform.Type)

	// TODO(maxcao13): this is quite gross since we do internet lookups through this function
	pullSpec, err := supportedversion.LookupReleaseImageFromVersion(ctx, nodeVersion)
	if err != nil {
		return "", fmt.Errorf("failed to lookup release image for version %s: %w", nodeVersion.String(), err)
	}

	return pullSpec, nil
}

// buildConfigGenerator creates a ConfigGenerator for the NodePool by looking up the release image,
// generating HAProxy configuration, and combining all node configuration sources.
func (r *EC2NodeClassReconciler) buildConfigGenerator(ctx context.Context, hcp *hyperv1.HostedControlPlane, np *hyperv1.NodePool) (*nodepool.ConfigGenerator, error) {
	// Convert HCP to a barebones HostedCluster for use with existing HostedCluster-based code paths.
	hostedCluster := hostedClusterFromHCP(hcp)
	hostedCluster.Status.IgnitionEndpoint = r.IgnitionEndpoint

	pullSecret := common.PullSecret(hcp.Namespace)
	if err := r.managementClient.Get(ctx, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
		return nil, fmt.Errorf("failed to get pull secret: %w", err)
	}

	pullSecretBytes, ok := pullSecret.Data[corev1.DockerConfigJsonKey]
	if !ok {
		return nil, fmt.Errorf("expected %s key in pull secret", corev1.DockerConfigJsonKey)
	}

	releaseImage, err := r.ReleaseProvider.Lookup(ctx, np.Spec.Release.Image, pullSecretBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup release image: %w", err)
	}

	haProxyImage, ok := releaseImage.ComponentImages()[haproxy.HAProxyRouterImageName]
	if !ok {
		return nil, fmt.Errorf("release image doesn't have %s image", haproxy.HAProxyRouterImageName)
	}

	haproxyClient := haproxy.HAProxy{
		Client:                  r.managementClient,
		HAProxyImage:            haProxyImage,
		HypershiftOperatorImage: r.ControlPlaneOperatorImage,
		ReleaseProvider:         r.ReleaseProvider,
		ImageMetadataProvider:   r.ImageMetadataProvider,
	}
	// hcp.Namespace IS the control plane namespace
	haproxyRawConfig, err := haproxyClient.GenerateHAProxyRawConfig(ctx, hostedCluster, hcp.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to generate HAProxy config: %w", err)
	}

	cg, err := nodepool.NewConfigGenerator(ctx, r.managementClient, hostedCluster, np, releaseImage, haproxyRawConfig, hcp.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to create config generator: %w", err)
	}

	return cg, nil
}

// reconcileToken creates and reconciles token secrets for node bootstrapping.
// It tracks the config version on OpenshiftEC2NodeClass to enable cleanup of outdated secrets.
func (r *EC2NodeClassReconciler) reconcileToken(ctx context.Context, cg *nodepool.ConfigGenerator, np *hyperv1.NodePool, openshiftEC2NodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass) error {
	log := ctrl.LoggerFrom(ctx)

	token, err := nodepool.NewToken(ctx, cg, &nodepool.CPOCapabilities{
		DecompressAndDecodeConfig: true,
	})
	if err != nil {
		return fmt.Errorf("failed to create token: %w", err)
	}

	// Get the current config version from OpenshiftEC2NodeClass to track outdated tokens
	currentConfigVersion := openshiftEC2NodeClass.GetAnnotations()[openshiftEC2NodeClassAnnotationCurrentConfigVersion]
	if currentConfigVersion == "" {
		// First reconcile - use the new hash
		np.GetAnnotations()[nodePoolAnnotationCurrentConfigVersion] = cg.Hash()
	} else {
		np.GetAnnotations()[nodePoolAnnotationCurrentConfigVersion] = currentConfigVersion
	}

	if err := token.Reconcile(ctx); err != nil {
		return fmt.Errorf("failed to reconcile token: %w", err)
	}

	// Update the OpenshiftEC2NodeClass annotation if the config hash changed
	if currentConfigVersion != cg.Hash() {
		if err := r.updateConfigVersionAnnotation(ctx, openshiftEC2NodeClass, cg.Hash()); err != nil {
			return err
		}
		log.Info("Updated config version annotation", "oldVersion", currentConfigVersion, "newVersion", cg.Hash())
	}

	return nil
}

// updateConfigVersionAnnotation patches the config version annotation on OpenshiftEC2NodeClass
func (r *EC2NodeClassReconciler) updateConfigVersionAnnotation(ctx context.Context, openshiftEC2NodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass, newVersion string) error {
	original := openshiftEC2NodeClass.DeepCopy()
	if openshiftEC2NodeClass.Annotations == nil {
		openshiftEC2NodeClass.Annotations = make(map[string]string)
	}
	openshiftEC2NodeClass.Annotations[openshiftEC2NodeClassAnnotationCurrentConfigVersion] = newVersion
	if err := r.guestClient.Patch(ctx, openshiftEC2NodeClass, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("failed to update config version annotation on OpenshiftEC2NodeClass: %w", err)
	}
	return nil
}

// userDataSecretPredicate only returns true on creates/updates to the userData secrets managed for Karpenter
func (r *EC2NodeClassReconciler) userDataSecretPredicate() predicate.Predicate {
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
	userDataSecretPredicate := predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return filterKarpenterSecret(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return filterKarpenterSecret(e.ObjectNew) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return false },
		GenericFunc: func(e event.GenericEvent) bool { return false },
	}
	return userDataSecretPredicate
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
