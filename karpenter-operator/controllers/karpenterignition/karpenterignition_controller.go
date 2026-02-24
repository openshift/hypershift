package karpenterignition

import (
	"context"
	"errors"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	haproxy "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/apiserver-haproxy"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/supportedversion"
	"github.com/openshift/hypershift/support/upsert"
	supportutil "github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/blang/semver"
)

const (
	openshiftEC2NodeClassAnnotationCurrentConfigVersion = "hypershift.openshift.io/nodeClassCurrentConfigVersion"

	// nodePoolAnnotationCurrentConfigVersion mirrors the annotation from nodepool_controller.go
	// It's used to track the current config version for outdated token cleanup
	nodePoolAnnotationCurrentConfigVersion = "hypershift.openshift.io/nodePoolCurrentConfigVersion"
)

type KarpenterIgnitionReconciler struct {
	ManagementClient        client.Client
	GuestClient             client.Client
	ReleaseProvider         releaseinfo.Provider
	VersionResolver         releaseinfo.VersionResolver
	ImageMetadataProvider   supportutil.ImageMetadataProvider
	HypershiftOperatorImage string
	IgnitionEndpoint        string
	Namespace               string
	upsert.CreateOrUpdateProvider
}

func (r *KarpenterIgnitionReconciler) SetupWithManager(mgr ctrl.Manager, managementCluster cluster.Cluster) error {
	r.GuestClient = mgr.GetClient()
	r.ManagementClient = managementCluster.GetClient()
	r.CreateOrUpdateProvider = upsert.New(false)

	bldr := ctrl.NewControllerManagedBy(mgr).
		Named("karpenter-ignition-controller").
		// Watch OpenshiftEC2NodeClass in the guest cluster (main manager)
		For(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
		// Watch HostedControlPlane in the management cluster
		WatchesRawSource(source.Kind[client.Object](managementCluster.GetCache(), &hyperv1.HostedControlPlane{},
			handler.EnqueueRequestsFromMapFunc(r.mapToOpenshiftEC2NodeClasses),
			r.hcpPredicate())).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 10,
		})

	return bldr.Complete(r)
}

func (r *KarpenterIgnitionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling Karpenter ignition config for OpenshiftEC2NodeClass", "nodeclass", req.Name)

	hcp, err := karpenterutil.GetHCP(ctx, r.ManagementClient, r.Namespace)
	if err != nil {
		if errors.Is(err, karpenterutil.ErrHCPNotFound) {
			log.Info("HostedControlPlane not found, re-queueing")
			return ctrl.Result{RequeueAfter: time.Second * 5}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get HostedControlPlane: %w", err)
	}

	// In practice, this shouldn't happen since the karpenter-operator pod would not exist if karpenter is not enabled
	// TODO(maxcao13): if we ever support disablement, that may change
	if !karpenterutil.IsKarpenterEnabled(hcp.Spec.AutoNode) {
		log.Info("Karpenter is not enabled, skipping reconcile")
		return ctrl.Result{}, nil
	}

	openshiftEC2NodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
	if err := r.GuestClient.Get(ctx, req.NamespacedName, openshiftEC2NodeClass); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("OpenshiftEC2NodeClass not found, aborting reconcile")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get OpenshiftEC2NodeClass: %w", err)
	}

	hostedCluster := hostedClusterFromHCP(hcp, r.IgnitionEndpoint)

	releaseImage, skewErr, err := r.resolveVersion(ctx, hcp, hostedCluster, openshiftEC2NodeClass.Spec.Version, openshiftEC2NodeClass.Status.Version)
	if err != nil {
		if updateErr := r.updateVersionStatus(ctx, openshiftEC2NodeClass, "", "", err); updateErr != nil {
			log.Error(updateErr, "failed to update version status after resolve error")
		}
		return ctrl.Result{}, fmt.Errorf("failed to resolve version for OpenshiftEC2NodeClass %q: %w", openshiftEC2NodeClass.Name, err)
	}

	// Determine the resolved version string for status.version.
	// When spec.version is set, it is the version. When not set, use the HCP's version.
	resolvedVersion := openshiftEC2NodeClass.Spec.Version
	if resolvedVersion == "" {
		resolvedVersion = hostedCluster.Status.Version.Desired.Version
	}

	if openshiftEC2NodeClass.Spec.Version != "" {
		log.Info("Resolved version to release image", "version", openshiftEC2NodeClass.Spec.Version, "channel", hcp.Spec.Channel, "releaseImage", releaseImage)
	}

	if err := r.updateVersionSkewStatus(ctx, openshiftEC2NodeClass, skewErr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update version skew status: %w", err)
	}

	if err := r.reconcileNodeClassToken(ctx, hcp, hostedCluster, openshiftEC2NodeClass, releaseImage); err != nil {
		log.Error(err, "failed to reconcile token for OpenshiftEC2NodeClass", "name", openshiftEC2NodeClass.Name)
		// Still update version status so conditions are set even when token reconciliation fails.
		// Re-fetch the object to get the latest resourceVersion since reconcileNodeClassToken may have patched it.
		if getErr := r.GuestClient.Get(ctx, client.ObjectKeyFromObject(openshiftEC2NodeClass), openshiftEC2NodeClass); getErr != nil {
			log.Error(getErr, "failed to re-fetch OpenshiftEC2NodeClass after token reconciliation error")
		} else if updateErr := r.updateVersionStatus(ctx, openshiftEC2NodeClass, releaseImage, resolvedVersion, nil); updateErr != nil {
			log.Error(updateErr, "failed to update version status after token reconciliation error")
		}
		return ctrl.Result{}, err
	}

	if err := r.updateVersionStatus(ctx, openshiftEC2NodeClass, releaseImage, resolvedVersion, nil); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update version status: %w", err)
	}

	return ctrl.Result{}, nil
}

// reconcileNodeClassToken reconciles the ignition token and user-data secrets for an OpenshiftEC2NodeClass.
func (r *KarpenterIgnitionReconciler) reconcileNodeClassToken(
	ctx context.Context,
	hcp *hyperv1.HostedControlPlane,
	hostedCluster *hyperv1.HostedCluster,
	openshiftEC2NodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass,
	releaseImage string,
) error {
	log := ctrl.LoggerFrom(ctx).WithValues("nodeclass", openshiftEC2NodeClass.Name)

	np := r.createInMemoryNodePool(hcp, openshiftEC2NodeClass, releaseImage)

	cg, err := r.buildConfigGenerator(ctx, hostedCluster, np, hcp.Namespace)
	if err != nil {
		return fmt.Errorf("failed to build config generator: %w", err)
	}

	token, err := nodepool.NewToken(ctx, cg, &nodepool.CPOCapabilities{
		DecompressAndDecodeConfig: true,
	})
	if err != nil {
		return fmt.Errorf("failed to create token: %w", err)
	}

	// Get the current config version from OpenshiftEC2NodeClass to track outdated tokens
	currentConfigVersion := openshiftEC2NodeClass.GetAnnotations()[openshiftEC2NodeClassAnnotationCurrentConfigVersion]
	if currentConfigVersion == "" {
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

func (r *KarpenterIgnitionReconciler) createInMemoryNodePool(
	hcp *hyperv1.HostedControlPlane,
	openshiftEC2NodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass,
	releaseImage string,
) *hyperv1.NodePool {
	return &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:        karpenterutil.KarpenterNodePoolName(openshiftEC2NodeClass),
			Namespace:   hcp.Namespace,
			Annotations: map[string]string{},
			Labels: map[string]string{
				// The nodepool must have this label to propagate to the secrets, so they don't get cleaned up by the secret janitor
				karpenterutil.ManagedByKarpenterLabel: "true",
			},
		},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: hcp.Name,
			Replicas:    ptr.To[int32](0),
			Release: hyperv1.Release{
				Image: releaseImage,
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

// resolveVersion determines the release image to use for the given NodeClass.
// When version is set, it validates the version and resolves it to a release image via Cincinnati.
// When version is not set, it returns the control plane's release image.
// The returned skewErr is non-nil when the version falls outside the supported skew policy;
// this is reported as a status condition but does not block reconciliation.
// currentStatusVersion is the NodeClass's current status.version, used to detect y-stream downgrades.
func (r *KarpenterIgnitionReconciler) resolveVersion(
	ctx context.Context,
	hcp *hyperv1.HostedControlPlane,
	hostedCluster *hyperv1.HostedCluster,
	version string,
	currentStatusVersion string,
) (releaseImage string, skewErr error, err error) {
	if version == "" {
		return hcp.Spec.ReleaseImage, nil, nil
	}

	nodeClassVersion, err := semver.Parse(version)
	if err != nil {
		return "", nil, fmt.Errorf("failed to parse OpenshiftEC2NodeClass version %q: %w", version, err)
	}

	hostedClusterVersion, err := semver.Parse(hostedCluster.Status.Version.Desired.Version)
	if err != nil {
		return "", nil, fmt.Errorf("failed to parse HostedCluster version %q: %w", hostedCluster.Status.Version.Desired.Version, err)
	}

	// maxSupportedVersion is the current control plane version, as nodes can't use a version greater than the control plane.
	// The n-3 skew policy is enforced separately by ValidateVersionSkew below.
	maxSupportedVersion := hostedClusterVersion
	minSupportedVersion := supportedversion.GetMinSupportedVersion(hostedCluster)

	// If the NodeClass has a previously resolved version, use it as currentVersion so that
	// IsValidReleaseVersion can detect y-stream downgrades.
	var currentVersion *semver.Version
	if currentStatusVersion != "" {
		parsed, parseErr := semver.Parse(currentStatusVersion)
		if parseErr == nil {
			currentVersion = &parsed
		}
	}

	if err = supportedversion.IsValidReleaseVersion(
		&nodeClassVersion,
		currentVersion,
		&maxSupportedVersion,
		&minSupportedVersion,
		hostedCluster.Spec.Networking.NetworkType,
		hostedCluster.Spec.Platform.Type,
	); err != nil {
		return "", nil, fmt.Errorf("failed to validate if version %q is valid: %w", version, err)
	}

	skewErr = supportedversion.ValidateVersionSkew(&hostedClusterVersion, &nodeClassVersion)

	resolved, err := r.VersionResolver.Resolve(ctx, version, hcp.Spec.Channel)
	if err != nil {
		return "", skewErr, fmt.Errorf("failed to resolve version %q: %w", version, err)
	}

	return resolved, skewErr, nil
}

// buildConfigGenerator creates a ConfigGenerator for the in-memory NodePool
func (r *KarpenterIgnitionReconciler) buildConfigGenerator(
	ctx context.Context,
	hostedCluster *hyperv1.HostedCluster,
	np *hyperv1.NodePool,
	controlPlaneNamespace string,
) (*nodepool.ConfigGenerator, error) {
	pullSecret := common.PullSecret(controlPlaneNamespace)
	if err := r.ManagementClient.Get(ctx, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
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
		Client:                  r.ManagementClient,
		HAProxyImage:            haProxyImage,
		HypershiftOperatorImage: r.HypershiftOperatorImage,
		ReleaseProvider:         r.ReleaseProvider,
		ImageMetadataProvider:   r.ImageMetadataProvider,
	}
	haproxyRawConfig, err := haproxyClient.GenerateHAProxyRawConfig(ctx, hostedCluster, controlPlaneNamespace)
	if err != nil {
		return nil, fmt.Errorf("failed to generate HAProxy raw config: %w", err)
	}

	return nodepool.NewConfigGenerator(ctx, r.ManagementClient, hostedCluster, np, releaseImage, haproxyRawConfig, controlPlaneNamespace)
}

// hostedClusterFromHCP creates a barebones in-memory HostedCluster from a HostedControlPlane.
// Note that the Namespace field is set to the HCP namespace rather than the original HC namespace.
// This ensures object lookups the configGenerator does internally which reference the HostedCluster object have necessary permissions since the operator is only allowed to read from the HCP namespace.
// This works since these objects are mirrored by hypershift-operator in both namespaces.
// 1. pullSecret lookup in https://github.com/openshift/hypershift/blob/825484eb33d14b4ab849b428d134582320655fcf/hypershift-operator/controllers/nodepool/nodepool_controller.go#L958
// 2. additionalTrustBundle lookup in https://github.com/openshift/hypershift/blob/825484eb33d14b4ab849b428d134582320655fcf/hypershift-operator/controllers/nodepool/nodepool_controller.go#L985
func hostedClusterFromHCP(hcp *hyperv1.HostedControlPlane, ignitionEndpoint string) *hyperv1.HostedCluster {
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        hcp.Name,
			Namespace:   hcp.Namespace,
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
		Status: hyperv1.HostedClusterStatus{
			IgnitionEndpoint: ignitionEndpoint,
			Version: &hyperv1.ClusterVersionStatus{
				Desired: configv1.Release{
					Version: hcp.Status.VersionStatus.Desired.Version,
				},
			},
		},
	}

	if hcp.Spec.ControlPlaneReleaseImage != nil {
		hc.Spec.ControlPlaneRelease = &hyperv1.Release{
			Image: *hcp.Spec.ControlPlaneReleaseImage,
		}
	}

	if hcp.Status.KubeConfig != nil {
		hc.Status.KubeConfig = &corev1.LocalObjectReference{
			Name: hcp.Status.KubeConfig.Name,
		}
	}

	return hc
}

func (r *KarpenterIgnitionReconciler) updateConfigVersionAnnotation(ctx context.Context, openshiftEC2NodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass, newVersion string) error {
	original := openshiftEC2NodeClass.DeepCopy()
	if openshiftEC2NodeClass.Annotations == nil {
		openshiftEC2NodeClass.Annotations = make(map[string]string)
	}
	openshiftEC2NodeClass.Annotations[openshiftEC2NodeClassAnnotationCurrentConfigVersion] = newVersion
	if err := r.GuestClient.Patch(ctx, openshiftEC2NodeClass, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("failed to update config version annotation on OpenshiftEC2NodeClass: %w", err)
	}
	return nil
}

// updateVersionStatus updates the OpenshiftEC2NodeClass status with the resolved release image,
// resolved version, and sets the VersionResolved condition based on whether resolution succeeded.
// resolvedVersion is the OpenShift version string (e.g. "4.17.0") corresponding to resolvedImage.
func (r *KarpenterIgnitionReconciler) updateVersionStatus(ctx context.Context, openshiftEC2NodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass, resolvedImage string, resolvedVersion string, resolveErr error) error {
	original := openshiftEC2NodeClass.DeepCopy()
	openshiftEC2NodeClass.Status.ReleaseImage = resolvedImage
	openshiftEC2NodeClass.Status.Version = resolvedVersion

	condition := metav1.Condition{
		Type:               hyperkarpenterv1.ConditionTypeVersionResolved,
		ObservedGeneration: openshiftEC2NodeClass.Generation,
		LastTransitionTime: metav1.Now(),
	}

	if openshiftEC2NodeClass.Spec.Version == "" {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "VersionNotSpecified"
		condition.Message = "No version specified, using control plane release image"
	} else if resolveErr != nil {
		condition.Status = metav1.ConditionFalse
		condition.Reason = "ResolutionFailed"
		condition.Message = fmt.Sprintf("Failed to resolve version %q: %v", openshiftEC2NodeClass.Spec.Version, resolveErr)
	} else {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "VersionResolved"
		condition.Message = fmt.Sprintf("Version %q resolved to %s", openshiftEC2NodeClass.Spec.Version, resolvedImage)
	}

	conditionChanged := meta.SetStatusCondition(&openshiftEC2NodeClass.Status.Conditions, condition)
	releaseImageChanged := original.Status.ReleaseImage != resolvedImage
	versionChanged := original.Status.Version != resolvedVersion
	if conditionChanged || releaseImageChanged || versionChanged {
		if err := r.GuestClient.Status().Patch(ctx, openshiftEC2NodeClass, client.MergeFrom(original)); err != nil {
			return fmt.Errorf("failed to update version status on OpenshiftEC2NodeClass: %w", err)
		}
	}

	return nil
}

// updateVersionSkewStatus sets the SupportedVersionSkew condition on the OpenshiftEC2NodeClass.
// When spec.version is not set, the condition is not applicable because nodes use the control plane image.
func (r *KarpenterIgnitionReconciler) updateVersionSkewStatus(ctx context.Context, openshiftEC2NodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass, skewErr error) error {
	original := openshiftEC2NodeClass.DeepCopy()

	condition := metav1.Condition{
		Type:               hyperkarpenterv1.ConditionTypeSupportedVersionSkew,
		ObservedGeneration: openshiftEC2NodeClass.Generation,
		LastTransitionTime: metav1.Now(),
	}

	if openshiftEC2NodeClass.Spec.Version == "" {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "VersionNotSpecified"
		condition.Message = "No version specified, nodes use the control plane release image"
	} else if skewErr != nil {
		condition.Status = metav1.ConditionFalse
		condition.Reason = "UnsupportedSkew"
		condition.Message = skewErr.Error()
	} else {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "AsExpected"
		condition.Message = fmt.Sprintf("Version %q is within supported skew", openshiftEC2NodeClass.Spec.Version)
	}

	if meta.SetStatusCondition(&openshiftEC2NodeClass.Status.Conditions, condition) {
		if err := r.GuestClient.Status().Patch(ctx, openshiftEC2NodeClass, client.MergeFrom(original)); err != nil {
			return fmt.Errorf("failed to update SupportedVersionSkew condition on OpenshiftEC2NodeClass: %w", err)
		}
	}

	return nil
}

// mapToOpenshiftEC2NodeClasses maps HCP events to all OpenshiftEC2NodeClass reconcile requests.
func (r *KarpenterIgnitionReconciler) mapToOpenshiftEC2NodeClasses(ctx context.Context, obj client.Object) []reconcile.Request {
	nodeClassList := &hyperkarpenterv1.OpenshiftEC2NodeClassList{}
	if err := r.GuestClient.List(ctx, nodeClassList); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list OpenshiftEC2NodeClasses")
		return nil
	}

	requests := make([]reconcile.Request, 0, len(nodeClassList.Items))
	for _, nc := range nodeClassList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(&nc),
		})
	}

	return requests
}

// hcpPredicate filters HCP events to only watch HCPs in our namespace.
func (r *KarpenterIgnitionReconciler) hcpPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == r.Namespace
	})
}
