package karpenterignition

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	haproxy "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/apiserver-haproxy"
	"github.com/openshift/hypershift/hypershift-operator/featuregate"
	"github.com/openshift/hypershift/support/imageregistry"
	"github.com/openshift/hypershift/support/k8sutil"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/supportedversion"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sigs.k8s.io/yaml"

	"github.com/blang/semver"
)

const (
	nodeClassAnnotationCurrentConfigVersion = "hypershift.openshift.io/nodeClassCurrentConfigVersion"
	nodeClassAnnotationCurrentRolloutConfig = "hypershift.openshift.io/nodeClassCurrentRolloutConfig"

	// nodePoolAnnotationCurrentConfigVersion mirrors the annotation from nodepool_controller.go
	// It's used to track the current config version for outdated token cleanup
	nodePoolAnnotationCurrentConfigVersion = "hypershift.openshift.io/nodePoolCurrentConfigVersion"
	nodePoolAnnotationCurrentRolloutConfig = "hypershift.openshift.io/nodePoolCurrentRolloutConfig"

	kubeletConfigFinalizer = "hypershift.openshift.io/karpenter-kubelet-config-finalizer"
)

type KarpenterIgnitionReconciler struct {
	ManagementClient        client.Client
	GuestClient             client.Client
	NodeClassGVK            schema.GroupVersionKind
	ReleaseProvider         releaseinfo.Provider
	VersionResolver         releaseinfo.VersionResolver
	ImageMetadataProvider   imageregistry.ImageMetadataProvider
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
		// Watch the configured NodeClass in the guest cluster (main manager)
		For(newNodeClass(r.NodeClassGVK).object).
		// Watch HostedControlPlane in the management cluster
		WatchesRawSource(source.Kind[client.Object](managementCluster.GetCache(), &hyperv1.HostedControlPlane{},
			handler.EnqueueRequestsFromMapFunc(r.mapToNodeClasses),
			r.hcpPredicate())).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 10,
		})

	return bldr.Complete(r)
}

func (r *KarpenterIgnitionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("kind", r.NodeClassGVK.Kind)
	ctx = ctrl.LoggerInto(ctx, log)
	log.Info("Reconciling Karpenter ignition config for NodeClass", "nodeclass", req.Name)

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

	nodeClass := newNodeClass(r.NodeClassGVK)
	if err := r.GuestClient.Get(ctx, req.NamespacedName, nodeClass.object); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("NodeClass not found, aborting reconcile")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get NodeClass: %w", err)
	}

	// Handle deletion: clean up management-cluster ConfigMap before finalizer is removed
	if !nodeClass.object.GetDeletionTimestamp().IsZero() {
		return r.reconcileDeletedNodeClass(ctx, hcp, nodeClass.object)
	}

	specVersion, err := nodeClass.specVersion()
	if err != nil {
		return ctrl.Result{}, err
	}
	statusVersion, err := nodeClass.statusVersion()
	if err != nil {
		return ctrl.Result{}, err
	}
	kubeletConfig, err := nodeClass.kubeletConfig()
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileTaintConfigMap(ctx, hcp); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile taint configmap: %w", err)
	}

	hostedCluster, err := hostedClusterFromHCP(hcp, r.IgnitionEndpoint)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get HostedCluster: %w", err)
	}

	releaseImage, version, requeue := currentClusterRelease(hostedCluster)
	if requeue && specVersion == "" {
		log.Info("No version history available for unpinned NodeClass, requeueing")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// When the user requests a version for the NodeClass we perform further validation and lookup of the release image.
	// We only detect skew and if the version is valid in this case, as under normal circumstances we can assume the release image
	// and version on the HostedCluster and HostedControlPlane have already been validated.
	var skewErr error
	if specVersion != "" {
		version = specVersion

		if err := validateVersion(hostedCluster, version, statusVersion); err != nil {
			if updateErr := r.updateVersionStatus(ctx, nodeClass, "", version,
				versionResolvedCondition(nodeClass.object.GetGeneration(), specVersion, "", err)); updateErr != nil {
				log.Error(updateErr, "failed to update version status after validation error")
			}
			return ctrl.Result{}, fmt.Errorf("failed to validate version for NodeClass %q: %w", nodeClass.object.GetName(), err)
		}

		releaseImage, err = r.resolveReleaseImage(ctx, hcp, version)
		if err != nil {
			if updateErr := r.updateVersionStatus(ctx, nodeClass, "", version,
				versionResolvedCondition(nodeClass.object.GetGeneration(), specVersion, "", err)); updateErr != nil {
				log.Error(updateErr, "failed to update version status after resolve error")
			}
			return ctrl.Result{}, fmt.Errorf("failed to resolve version for NodeClass %q: %w", nodeClass.object.GetName(), err)
		}
		log.Info("Resolved version to release image", "version", version, "channel", hcp.Spec.Channel, "releaseImage", releaseImage)

		// If the version skew is unacceptable, we don't want to block reconciling. The skew result
		// is set as a status condition alongside VersionResolved in a single atomic status patch.
		skewErr = detectVersionSkew(hostedCluster, version)
	}

	if err := r.reconcileKubeletConfig(ctx, hcp, nodeClass, kubeletConfig); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileNodeClassToken(ctx, hcp, hostedCluster, nodeClass, releaseImage, !kubeletConfig.IsZero()); err != nil {
		log.Error(err, "failed to reconcile token for NodeClass", "name", nodeClass.object.GetName())
		// Update conditions without advancing version status before token reconciliation succeeds.
		r.updateVersionStatusAfterTokenError(ctx, nodeClass, releaseImage, skewErr)
		return ctrl.Result{}, err
	}

	// Metadata patch responses during token reconciliation may have refreshed the NodeClass spec.
	specVersion, err = nodeClass.specVersion()
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update version status: failed to set version skew condition: %w", err)
	}
	generation := nodeClass.object.GetGeneration()
	if err := r.updateVersionStatus(ctx, nodeClass, releaseImage, version,
		versionResolvedCondition(generation, specVersion, releaseImage, nil),
		versionSkewCondition(generation, specVersion, skewErr)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update version status: %w", err)
	}

	return ctrl.Result{}, nil
}

// updateVersionStatusAfterTokenError updates conditions without advancing the
// stored version and image, so retries can still detect a pending transition.
// Failures are logged without replacing the original token reconciliation error.
func (r *KarpenterIgnitionReconciler) updateVersionStatusAfterTokenError(
	ctx context.Context,
	nodeClass *nodeClass,
	releaseImage string,
	skewErr error,
) {
	log := ctrl.LoggerFrom(ctx)

	// Token reconciliation may have patched the object, so re-fetch it first.
	latestNodeClass := newNodeClass(r.NodeClassGVK)
	err := r.GuestClient.Get(ctx, client.ObjectKeyFromObject(nodeClass.object), latestNodeClass.object)
	if err != nil {
		log.Error(err, "failed to re-fetch NodeClass after token reconciliation error")
		return
	}

	latestSpecVersion, err := latestNodeClass.specVersion()
	if err != nil {
		log.Error(
			fmt.Errorf("failed to set version skew condition: %w", err),
			"failed to update version status after token reconciliation error",
		)
		return
	}

	latestStatus, err := latestNodeClass.status()
	if err != nil {
		log.Error(err, "failed to read NodeClass status after token reconciliation error")
		return
	}

	generation := latestNodeClass.object.GetGeneration()
	err = r.updateVersionStatus(ctx, latestNodeClass, latestStatus.ReleaseImage, latestStatus.Version,
		versionResolvedCondition(generation, latestSpecVersion, releaseImage, nil),
		versionSkewCondition(generation, latestSpecVersion, skewErr))
	if err != nil {
		log.Error(err, "failed to update version status after token reconciliation error")
	}
}

func (r *KarpenterIgnitionReconciler) reconcileKubeletConfig(
	ctx context.Context,
	hcp *hyperv1.HostedControlPlane,
	nodeClass *nodeClass,
	kubeletConfig hyperkarpenterv1.KubeletConfiguration,
) error {
	// If spec.kubelet is configured, add a finalizer to clean up the configmap. We can't just use owner
	// references because this is cross cluster (the configmap lives in the control plane)
	if !kubeletConfig.IsZero() {
		if !controllerutil.ContainsFinalizer(nodeClass.object, kubeletConfigFinalizer) {
			original := nodeClass.object.DeepCopy()
			controllerutil.AddFinalizer(nodeClass.object, kubeletConfigFinalizer)
			if err := r.GuestClient.Patch(ctx, nodeClass.object,
				client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
				return fmt.Errorf("failed to add kubelet config finalizer: %w", err)
			}
		}
	}

	if err := r.reconcileKubeletConfigMap(ctx, hcp, nodeClass.object, kubeletConfig); err != nil {
		return fmt.Errorf("failed to reconcile kubelet config configmap: %w", err)
	}

	// The reconcile will have deleted the configmap if we make it here, so we can
	// remove the finalizer
	if kubeletConfig.IsZero() && controllerutil.ContainsFinalizer(nodeClass.object, kubeletConfigFinalizer) {
		original := nodeClass.object.DeepCopy()
		controllerutil.RemoveFinalizer(nodeClass.object, kubeletConfigFinalizer)
		if err := r.GuestClient.Patch(ctx, nodeClass.object,
			client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
			return fmt.Errorf("failed to remove kubelet config finalizer: %w", err)
		}
	}

	return nil
}

// reconcileDeletedNodeClass handles cleanup when a NodeClass is being deleted.
// It deletes the kubelet ConfigMap from the management cluster and removes the finalizer
// to allow the NodeClass deletion to proceed.
func (r *KarpenterIgnitionReconciler) reconcileDeletedNodeClass(
	ctx context.Context,
	hcp *hyperv1.HostedControlPlane,
	nodeClass client.Object,
) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	if !controllerutil.ContainsFinalizer(nodeClass, kubeletConfigFinalizer) {
		return ctrl.Result{}, nil
	}

	// Delete the kubelet ConfigMap from the management cluster
	configMapName := karpenterutil.KarpenterNodeClassKubeletConfigName(nodeClass.GetName())
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: hcp.Namespace,
		},
	}
	if _, err := k8sutil.DeleteIfNeeded(ctx, r.ManagementClient, cm); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to delete kubelet config configmap %s: %w", configMapName, err)
	}
	log.Info("Deleted kubelet config ConfigMap", "name", configMapName)

	// Remove the finalizer to allow NodeClass deletion to proceed
	original := nodeClass.DeepCopyObject().(client.Object)
	controllerutil.RemoveFinalizer(nodeClass, kubeletConfigFinalizer)
	if err := r.GuestClient.Patch(ctx, nodeClass,
		client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to remove kubelet config finalizer: %w", err)
	}
	log.Info("Removed kubelet config finalizer from NodeClass", "name", nodeClass.GetName())

	return ctrl.Result{}, nil
}

// reconcileTaintConfigMap ensures the set-karpenter-taint ConfigMap exists in the HCP namespace.
// Ignition configs reference it to taint nodes on firstboot until Karpenter okays them, see createInMemoryNodePool.
func (r *KarpenterIgnitionReconciler) reconcileTaintConfigMap(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      karpenterutil.KarpenterTaintConfigMapName,
			Namespace: hcp.Namespace,
		},
	}
	_, err := r.CreateOrUpdate(ctx, r.ManagementClient, cm, func() error {
		manifest, err := karpenterutil.KarpenterTaintConfigManifest()
		if err != nil {
			return fmt.Errorf("failed to generate taint config manifest: %w", err)
		}
		cm.Data = map[string]string{"config": manifest}
		return nil
	})
	return err
}

// reconcileNodeClassToken reconciles the ignition token and user-data secrets for a NodeClass.
func (r *KarpenterIgnitionReconciler) reconcileNodeClassToken(
	ctx context.Context,
	hcp *hyperv1.HostedControlPlane,
	hostedCluster *hyperv1.HostedCluster,
	nodeClass *nodeClass,
	releaseImage string,
	hasKubeletConfig bool,
) error {
	log := ctrl.LoggerFrom(ctx).WithValues("nodeclass", nodeClass.object.GetName())

	np := r.createInMemoryNodePool(hcp, nodeClass.object, releaseImage, hasKubeletConfig)

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

	nodeClassVersion, err := nodeClass.statusVersion()
	if err != nil {
		return fmt.Errorf("failed to get status version from nodeclass: %w", err)
	}

	// Populate the in-memory NodePool annotations and status from the stored nodeclass
	// state so that Token.isOutdated() can correctly detect config/version changes.
	// On first reconcile (no stored state), leave annotations absent so isOutdated()
	// returns true and creates the initial secrets.
	currentConfigVersion := nodeClass.object.GetAnnotations()[nodeClassAnnotationCurrentConfigVersion]
	currentRolloutConfig := nodeClass.object.GetAnnotations()[nodeClassAnnotationCurrentRolloutConfig]
	if currentConfigVersion != "" {
		np.Annotations[nodePoolAnnotationCurrentConfigVersion] = currentConfigVersion
		if currentRolloutConfig != "" {
			np.Annotations[nodePoolAnnotationCurrentRolloutConfig] = currentRolloutConfig
		}

		if nodeClassVersion != "" {
			np.Status.Version = nodeClassVersion
		}
	}

	// Set the deployed bootstrap hash so effectiveSecrets() can maintain the
	// in-progress rollout target during management-side drift, and so
	// cleanupOutdated() avoids expiring secrets still referenced by Karpenter.
	if currentConfigVersion != "" {
		token.SetDeployedBootstrapHash(currentConfigVersion)
	}

	if err := token.Reconcile(ctx); err != nil {
		return fmt.Errorf("failed to reconcile token: %w", err)
	}

	// Token.EffectiveHash() returns the hash that Reconcile() actually
	// maintained secrets for. When only management-side content changed,
	// this is the deployed hash; otherwise it's the current calculated hash.
	hashToRecord := token.EffectiveHash()

	// Update annotations when a spec-driven change occurred (version or rollout
	// config changed), or on first reconcile after operator upgrade to seed the
	// rollout annotation.
	versionChanged := cg.Version() != nodeClassVersion
	rolloutConfigChanged := currentRolloutConfig != "" && currentRolloutConfig != cg.RolloutHashWithoutVersion()
	shouldSeedAnnotation := currentRolloutConfig == "" && currentConfigVersion != ""

	if versionChanged || rolloutConfigChanged || shouldSeedAnnotation {
		if err := r.updateConfigVersionAnnotation(ctx, nodeClass.object, hashToRecord, cg.RolloutHashWithoutVersion()); err != nil {
			return err
		}
		log.Info("Updated config version annotation", "oldVersion", currentConfigVersion, "newVersion", hashToRecord)
	}

	return nil
}

func (r *KarpenterIgnitionReconciler) createInMemoryNodePool(
	hcp *hyperv1.HostedControlPlane,
	nodeClass client.Object,
	releaseImage string,
	hasKubeletConfig bool,
) *hyperv1.NodePool {
	// When kubelet configuration is present, the per-nodeclass kubelet ConfigMap is generated with
	// KarpenterBaseTaintMap merged in (so registerWithTaints is always present), so
	// set-karpenter-taint must NOT also be included — the MCO bootstrap rejects two
	// KubeletConfigs targeting the same MachineConfigPool.
	var configRefs []corev1.LocalObjectReference
	if hasKubeletConfig {
		configRefs = []corev1.LocalObjectReference{
			{Name: karpenterutil.KarpenterNodeClassKubeletConfigName(nodeClass.GetName())},
		}
	} else {
		configRefs = []corev1.LocalObjectReference{
			{Name: karpenterutil.KarpenterTaintConfigMapName},
		}
	}

	return &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:        karpenterutil.KarpenterNodePoolName(nodeClass),
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
			Config: configRefs,
			Arch:   hyperv1.ArchitectureAMD64, // used to find default AMI
		},
	}
}

// currentClusterRelease returns the release image and version of the most recently
// completed update from the HostedCluster's version history, plus a requeue flag.
// During a control plane upgrade, this returns the last completed version rather than
// the desired version, preventing premature drift detection and worker node replacement
// before the control plane is ready.
//
// The function relies on the API ordering guarantee: History is ordered newest-first
// (per the HostedCluster CRD contract), so the first Completed entry is the most recent.
func currentClusterRelease(hostedCluster *hyperv1.HostedCluster) (string, string, bool) {
	if hostedCluster.Status.Version == nil {
		// CPO hasn't populated version status yet. Use spec as best-effort for initial install.
		return hostedCluster.Spec.Release.Image, "", false
	}

	// Return the first Completed entry (most recent finished version).
	for _, entry := range hostedCluster.Status.Version.History {
		if entry.State == configv1.CompletedUpdate {
			return entry.Image, entry.Version, false
		}
	}

	// No completed entries: initial install still in progress.
	// Use the first history entry (the version being installed).
	if len(hostedCluster.Status.Version.History) > 0 {
		entry := &hostedCluster.Status.Version.History[0]
		return entry.Image, entry.Version, false
	}

	// Empty history: signal caller to requeue.
	return "", "", true
}

// validateVersion checks whether the requested version is valid for the given HostedCluster.
// It validates semver parsing, range bounds, and y-stream downgrade detection.
// currentStatusVersion is the NodeClass's current status.version, used to detect y-stream downgrades.
// Returns nil when version is empty (nodes use the control plane image).
func validateVersion(hostedCluster *hyperv1.HostedCluster, version string, currentStatusVersion string) error {
	if version == "" {
		return nil
	}

	nodeClassVersion, err := semver.Parse(version)
	if err != nil {
		return fmt.Errorf("failed to parse OpenshiftEC2NodeClass version %q: %w", version, err)
	}

	hostedClusterVersion, err := semver.Parse(hostedCluster.Status.Version.Desired.Version)
	if err != nil {
		return fmt.Errorf("failed to parse HostedCluster version %q: %w", hostedCluster.Status.Version.Desired.Version, err)
	}

	// maxSupportedVersion is the current control plane version, as nodes can't use a version greater than the control plane.
	// The n-3 skew policy is enforced separately by detectVersionSkew.
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
		return fmt.Errorf("failed to validate if version %q is valid: %w", version, err)
	}

	return nil
}

// resolveReleaseImage resolves a version string to a release image via Cincinnati.
// When version is empty, it returns the control plane's release image.
func (r *KarpenterIgnitionReconciler) resolveReleaseImage(
	ctx context.Context,
	hcp *hyperv1.HostedControlPlane,
	version string,
) (string, error) {
	if version == "" {
		return hcp.Spec.ReleaseImage, nil
	}

	resolved, err := r.VersionResolver.Resolve(ctx, version, hcp.Spec.Channel)
	if err != nil {
		return "", fmt.Errorf("failed to resolve version %q: %w", version, err)
	}

	return resolved, nil
}

// detectVersionSkew checks whether the NodeClass version falls outside the supported skew policy
// relative to the control plane version. Returns nil when version is empty (nodes use the control
// plane image) or when the skew is within policy.
func detectVersionSkew(hostedCluster *hyperv1.HostedCluster, version string) error {
	if version == "" {
		return nil
	}

	nodeClassVersion, err := semver.Parse(version)
	if err != nil {
		return fmt.Errorf("failed to parse OpenshiftEC2NodeClass version %q: %w", version, err)
	}

	hostedClusterVersion, err := semver.Parse(hostedCluster.Status.Version.Desired.Version)
	if err != nil {
		return fmt.Errorf("failed to parse HostedCluster version %q: %w", hostedCluster.Status.Version.Desired.Version, err)
	}

	return supportedversion.ValidateVersionSkew(&hostedClusterVersion, &nodeClassVersion)
}

// reconcileKubeletConfigMap creates, updates, or deletes the per-NodeClass KubeletConfig ConfigMap
// in the HCP namespace based on the extracted kubelet configuration.
func (r *KarpenterIgnitionReconciler) reconcileKubeletConfigMap(
	ctx context.Context,
	hcp *hyperv1.HostedControlPlane,
	nodeClass client.Object,
	kubeletConfig hyperkarpenterv1.KubeletConfiguration,
) error {
	configMapName := karpenterutil.KarpenterNodeClassKubeletConfigName(nodeClass.GetName())
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: hcp.Namespace,
		},
	}

	// Delete the configmap if there are no kubelet settings on the nodeclass
	if kubeletConfig.IsZero() {
		if _, err := k8sutil.DeleteIfNeeded(ctx, r.ManagementClient, cm); err != nil {
			return fmt.Errorf("failed to delete kubelet config configmap %s: %w", configMapName, err)
		}
		return nil
	}

	kubeletMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&kubeletConfig)
	if err != nil {
		return fmt.Errorf("failed to encode kubelet configuration: %w", err)
	}

	// Merge the required taints last so user configuration cannot override them.
	merged := mergeKubeletConfigMaps(kubeletMap, karpenterutil.KarpenterBaseTaintMap())
	manifest, err := kubeletConfigManifest(configMapName, merged)
	if err != nil {
		return fmt.Errorf("failed to generate kubelet config manifest: %w", err)
	}

	_, err = r.CreateOrUpdate(ctx, r.ManagementClient, cm, func() error {
		if cm.Labels == nil {
			cm.Labels = map[string]string{}
		}
		cm.Labels[karpenterutil.KarpenterNodeClassKubeletConfigLabel] = "true"
		cm.Data = map[string]string{
			"config": manifest,
		}
		return nil
	})
	return err
}

// mergeKubeletConfigMaps merges two kubeletConfig maps. Base keys are included unless
// overlay defines the same key, in which case overlay wins. Callers should pass our
// required fields (e.g. taint base) as overlay so they are never clobbered by user config.
func mergeKubeletConfigMaps(base, overlay map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(base)+len(overlay))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range overlay {
		result[k] = v
	}
	return result
}

// kubeletConfigManifest serializes a pre-merged kubeletConfig map into a KubeletConfig CR YAML
// string suitable for storage in a ConfigMap "config" key.
func kubeletConfigManifest(name string, kubeletConfig map[string]interface{}) (string, error) {
	cr := map[string]interface{}{
		"apiVersion": "machineconfiguration.openshift.io/v1",
		"kind":       "KubeletConfig",
		"metadata":   map[string]interface{}{"name": name},
		"spec":       map[string]interface{}{"kubeletConfig": kubeletConfig},
	}
	out, err := yaml.Marshal(cr)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(out), "\n"), nil
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

	haproxyImage, err := haproxy.ResolveHAProxyImage(releaseImage.ComponentImages())
	if err != nil {
		return nil, err
	}

	haproxyClient := haproxy.HAProxy{
		Client:                  r.ManagementClient,
		HAProxyImage:            haproxyImage,
		HypershiftOperatorImage: r.HypershiftOperatorImage,
		ReleaseProvider:         r.ReleaseProvider,
		ImageMetadataProvider:   r.ImageMetadataProvider,
	}
	haproxyRawConfig, err := haproxyClient.GenerateHAProxyRawConfig(ctx, hostedCluster, controlPlaneNamespace)
	if err != nil {
		return nil, fmt.Errorf("failed to generate HAProxy raw config: %w", err)
	}

	osStreamsEnabled := featuregate.Gate().Enabled(featuregate.OSStreams)
	resolvedRHELStream, err := nodepool.GetRHELStreamForBootImage(ctx, r.ManagementClient, np, releaseImage, osStreamsEnabled)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve RHEL stream for boot image: %w", err)
	}
	return nodepool.NewConfigGenerator(ctx, r.ManagementClient, hostedCluster, np, releaseImage, haproxyRawConfig, controlPlaneNamespace, resolvedRHELStream)
}

// hostedClusterFromHCP creates a barebones in-memory HostedCluster from a HostedControlPlane.
// Note that the Namespace field is set to the HCP namespace rather than the original HC namespace.
// This ensures object lookups the configGenerator does internally which reference the HostedCluster object have necessary permissions since the operator is only allowed to read from the HCP namespace.
// This works since these objects are mirrored by hypershift-operator in both namespaces.
// 1. pullSecret lookup in https://github.com/openshift/hypershift/blob/825484eb33d14b4ab849b428d134582320655fcf/hypershift-operator/controllers/nodepool/nodepool_controller.go#L958
// 2. additionalTrustBundle lookup in https://github.com/openshift/hypershift/blob/825484eb33d14b4ab849b428d134582320655fcf/hypershift-operator/controllers/nodepool/nodepool_controller.go#L985
func hostedClusterFromHCP(hcp *hyperv1.HostedControlPlane, ignitionEndpoint string) (*hyperv1.HostedCluster, error) {
	if hcp == nil {
		return nil, fmt.Errorf("cannot create HostedCluster from nil HostedControlPlane object")
	}
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
		},
	}

	// Prefer ControlPlaneVersion (management-side CP rollout status) so that the
	// release selector and the ec2nodeclass controller's pause gate agree on the
	// same completion signal. Fall back to VersionStatus for backward compatibility
	// with HCPs that don't yet populate ControlPlaneVersion.
	cpv := hcp.Status.ControlPlaneVersion
	if cpv.Desired.Image != "" || len(cpv.History) > 0 {
		history := make([]configv1.UpdateHistory, len(cpv.History))
		for i, entry := range cpv.History {
			history[i] = configv1.UpdateHistory{
				State:   entry.State,
				Image:   entry.Image,
				Version: entry.Version,
			}
			if !entry.CompletionTime.IsZero() {
				history[i].CompletionTime = &entry.CompletionTime
			}
		}
		hc.Status.Version = &hyperv1.ClusterVersionStatus{
			Desired: cpv.Desired,
			History: history,
		}
	} else if hcp.Status.VersionStatus != nil {
		hc.Status.Version = &hyperv1.ClusterVersionStatus{
			Desired: hcp.Status.VersionStatus.Desired,
			History: hcp.Status.VersionStatus.History,
		}
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

	return hc, nil
}

func (r *KarpenterIgnitionReconciler) updateConfigVersionAnnotation(ctx context.Context, nodeClass client.Object, newVersion, rolloutConfig string) error {
	original := nodeClass.DeepCopyObject().(client.Object)
	annotations := nodeClass.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[nodeClassAnnotationCurrentConfigVersion] = newVersion
	annotations[nodeClassAnnotationCurrentRolloutConfig] = rolloutConfig
	nodeClass.SetAnnotations(annotations)
	if err := r.GuestClient.Patch(ctx, nodeClass, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("failed to update config version annotation on NodeClass: %w", err)
	}
	return nil
}

// updateVersionStatus sets the resolved image and version and upserts the supplied conditions
// in a single status patch. It skips the patch when nothing changed.
func (r *KarpenterIgnitionReconciler) updateVersionStatus(
	ctx context.Context,
	nodeClass *nodeClass,
	resolvedImage string,
	resolvedVersion string,
	conditions ...metav1.Condition,
) error {
	original := nodeClass.object.DeepCopy()
	current, err := nodeClass.status()
	if err != nil {
		return err
	}

	updated := current
	updated.Conditions = slices.Clone(current.Conditions)
	updated.ReleaseImage = resolvedImage
	updated.Version = resolvedVersion
	for _, condition := range conditions {
		meta.SetStatusCondition(&updated.Conditions, condition)
	}
	if equality.Semantic.DeepEqual(current, updated) {
		return nil
	}

	if err := nodeClass.setStatus(updated); err != nil {
		return err
	}
	if err := r.GuestClient.Status().Patch(ctx, nodeClass.object, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("failed to update version status on NodeClass: %w", err)
	}
	return nil
}

// versionResolvedCondition reports whether the NodeClass version resolved to a release image.
func versionResolvedCondition(generation int64, specVersion, resolvedImage string, resolveErr error) metav1.Condition {
	condition := metav1.Condition{
		Type:               hyperkarpenterv1.ConditionTypeVersionResolved,
		ObservedGeneration: generation,
		LastTransitionTime: metav1.Now(),
	}

	if specVersion == "" {
		condition.Status = metav1.ConditionTrue
		condition.Reason = hyperkarpenterv1.ConditionReasonVersionNotSpecified
		condition.Message = "No version specified, using control plane release image"
	} else if resolveErr != nil {
		condition.Status = metav1.ConditionFalse
		condition.Reason = hyperkarpenterv1.ConditionReasonResolutionFailed
		condition.Message = fmt.Sprintf("Failed to resolve version %q: %v", specVersion, resolveErr)
	} else {
		condition.Status = metav1.ConditionTrue
		condition.Reason = hyperkarpenterv1.ConditionReasonVersionResolved
		condition.Message = fmt.Sprintf("Version %q resolved to %s", specVersion, resolvedImage)
	}

	return condition
}

// versionSkewCondition reports whether the NodeClass version is within the supported skew.
func versionSkewCondition(generation int64, specVersion string, skewErr error) metav1.Condition {
	condition := metav1.Condition{
		Type:               hyperkarpenterv1.ConditionTypeSupportedVersionSkew,
		ObservedGeneration: generation,
		LastTransitionTime: metav1.Now(),
	}

	if specVersion == "" {
		condition.Status = metav1.ConditionTrue
		condition.Reason = hyperkarpenterv1.ConditionReasonVersionNotSpecified
		condition.Message = "No version specified, nodes use the control plane release image"
	} else if skewErr != nil {
		condition.Status = metav1.ConditionFalse
		condition.Reason = hyperkarpenterv1.ConditionReasonUnsupportedSkew
		condition.Message = skewErr.Error()
	} else {
		condition.Status = metav1.ConditionTrue
		condition.Reason = hyperkarpenterv1.ConditionReasonAsExpected
		condition.Message = fmt.Sprintf("Version %q is within supported skew", specVersion)
	}
	return condition
}

// mapToNodeClasses maps HCP events to all configured NodeClass reconcile requests.
func (r *KarpenterIgnitionReconciler) mapToNodeClasses(ctx context.Context, obj client.Object) []reconcile.Request {
	nodeClassList := newNodeClassList(r.NodeClassGVK)
	if err := r.GuestClient.List(ctx, nodeClassList); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list NodeClasses")
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
