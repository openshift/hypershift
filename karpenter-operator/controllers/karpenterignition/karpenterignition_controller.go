package karpenterignition

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/blang/semver"
	configv1 "github.com/openshift/api/config/v1"
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
			log.Info("HostedControlPlane not found, requeueing")
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

	resolvedImage, err := r.reconcileNodeClassToken(ctx, hcp, hostedCluster, openshiftEC2NodeClass)
	if err != nil {
		if updateErr := r.updateVersionStatus(ctx, openshiftEC2NodeClass, resolvedImage, err); updateErr != nil {
			log.Error(updateErr, "failed to update version status after reconcile error")
		}
		log.Error(err, "failed to reconcile token for OpenshiftEC2NodeClass", "name", openshiftEC2NodeClass.Name)
		return ctrl.Result{}, err
	}

	if err := r.updateVersionStatus(ctx, openshiftEC2NodeClass, resolvedImage, nil); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update version status: %w", err)
	}

	return ctrl.Result{}, nil
}

// reconcileNodeClassToken will reconcile an OpenshiftEC2NodeClass and returning the image used and any error
func (r *KarpenterIgnitionReconciler) reconcileNodeClassToken(
	ctx context.Context,
	hcp *hyperv1.HostedControlPlane,
	hostedCluster *hyperv1.HostedCluster,
	openshiftEC2NodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass,
) (string, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("nodeclass", openshiftEC2NodeClass.Name)

	np := r.createInMemoryNodePool(hcp, openshiftEC2NodeClass)

	// Determine the release image to use for this NodeClass.
	// When spec.version is set, resolve it to a release image via the Cincinnati graph API
	// and validate version skew against the control plane version.
	// Otherwise, fall back to the control plane's release image.
	var pullSpec string
	var resolvedImage string
	if openshiftEC2NodeClass.Spec.Version != nil {
		nodeClassVersion, err := semver.Parse(*openshiftEC2NodeClass.Spec.Version)
		if err != nil {
			return "", fmt.Errorf("failed to parse OpenshiftEC2NodeClass version %q: %w", *openshiftEC2NodeClass.Spec.Version, err)
		}

		hostedClusterVersion, err := semver.Parse(hostedCluster.Status.Version.Desired.Version)
		if err != nil {
			return "", fmt.Errorf("failed to parse HostedCluster version %q: %w", *openshiftEC2NodeClass.Spec.Version, err)
		}

		// Validate the release version is valid (basic checks not including version skew)
		minSupportedVersion := supportedversion.GetMinSupportedVersion(hostedCluster)
		if err = supportedversion.IsValidReleaseVersion(
			&nodeClassVersion,
			&hostedClusterVersion,
			&hostedClusterVersion,
			&minSupportedVersion,
			hostedCluster.Spec.Networking.NetworkType,
			hostedCluster.Spec.Platform.Type,
		); err != nil {
			return "", fmt.Errorf("failed to validate if version %q is valid: %w", *openshiftEC2NodeClass.Spec.Version, err)
		}

		// Validate the release version does not exceed allowed skew between control plane and data plane
		if err = supportedversion.ValidateVersionSkew(&hostedClusterVersion, &nodeClassVersion); err != nil {
			return "", fmt.Errorf("version skew validation failed: %w", err)
		}

		resolved, err := r.VersionResolver.Resolve(ctx, *openshiftEC2NodeClass.Spec.Version, hcp.Spec.Channel)
		if err != nil {
			return "", fmt.Errorf("failed to resolve version %q: %w", *openshiftEC2NodeClass.Spec.Version, err)
		}
		pullSpec = resolved
		resolvedImage = resolved
		log.Info("Resolved version to release image", "version", *openshiftEC2NodeClass.Spec.Version, "channel", hcp.Spec.Channel, "releaseImage", resolved)
	} else {
		pullSpec = hcp.Spec.ReleaseImage
	}
	// Set the NodePool release image
	np.Spec.Release.Image = pullSpec

	cg, err := r.buildConfigGenerator(ctx, hostedCluster, np, hcp.Namespace)
	if err != nil {
		return "", fmt.Errorf("failed to build config generator: %w", err)
	}

	token, err := nodepool.NewToken(ctx, cg, &nodepool.CPOCapabilities{
		DecompressAndDecodeConfig: true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create token: %w", err)
	}

	// Get the current config version from OpenshiftEC2NodeClass to track outdated tokens
	currentConfigVersion := openshiftEC2NodeClass.GetAnnotations()[openshiftEC2NodeClassAnnotationCurrentConfigVersion]
	if currentConfigVersion == "" {
		np.GetAnnotations()[nodePoolAnnotationCurrentConfigVersion] = cg.Hash()
	} else {
		np.GetAnnotations()[nodePoolAnnotationCurrentConfigVersion] = currentConfigVersion
	}

	if err := token.Reconcile(ctx); err != nil {
		return "", fmt.Errorf("failed to reconcile token: %w", err)
	}

	// Update the OpenshiftEC2NodeClass annotation if the config hash changed
	if currentConfigVersion != cg.Hash() {
		if err := r.updateConfigVersionAnnotation(ctx, openshiftEC2NodeClass, cg.Hash()); err != nil {
			return "", err
		}
		log.Info("Updated config version annotation", "oldVersion", currentConfigVersion, "newVersion", cg.Hash())
	}

	return resolvedImage, nil
}

func (r *KarpenterIgnitionReconciler) createInMemoryNodePool(
	hcp *hyperv1.HostedControlPlane,
	openshiftEC2NodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass,
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
					Version: hcp.Status.Version,
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

// updateVersionStatus updates the OpenshiftEC2NodeClass status with the resolved release image
// and sets the VersionResolved condition based on whether resolution succeeded.
func (r *KarpenterIgnitionReconciler) updateVersionStatus(ctx context.Context, openshiftEC2NodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass, resolvedImage string, resolveErr error) error {
	// Only update version-related status if spec.version is set
	if openshiftEC2NodeClass.Spec.Version == nil {
		// Clear any stale status if version was previously set but is now removed
		if openshiftEC2NodeClass.Status.ReleaseImage == "" {
			return nil
		}
		resolvedImage = ""
	}

	original := openshiftEC2NodeClass.DeepCopy()
	openshiftEC2NodeClass.Status.ReleaseImage = resolvedImage

	condition := metav1.Condition{
		Type:               hyperkarpenterv1.ConditionTypeVersionResolved,
		ObservedGeneration: openshiftEC2NodeClass.Generation,
		LastTransitionTime: metav1.Now(),
	}

	if openshiftEC2NodeClass.Spec.Version == nil {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "VersionNotSpecified"
		condition.Message = "No version specified, using control plane release image"
	} else if resolveErr != nil {
		condition.Status = metav1.ConditionFalse
		condition.Reason = "ResolutionFailed"
		condition.Message = fmt.Sprintf("Failed to resolve version %q: %v", *openshiftEC2NodeClass.Spec.Version, resolveErr)
	} else {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "VersionResolved"
		condition.Message = fmt.Sprintf("Version %q resolved to %s", *openshiftEC2NodeClass.Spec.Version, resolvedImage)
	}

	meta.SetStatusCondition(&openshiftEC2NodeClass.Status.Conditions, condition)

	if err := r.GuestClient.Status().Patch(ctx, openshiftEC2NodeClass, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("failed to update version status on OpenshiftEC2NodeClass: %w", err)
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
