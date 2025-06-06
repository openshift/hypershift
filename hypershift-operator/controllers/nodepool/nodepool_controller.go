package nodepool

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	haproxy "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/apiserver-haproxy"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/kubevirt"
	kvinfra "github.com/openshift/hypershift/kubevirtexternalinfra"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/supportedversion"
	"github.com/openshift/hypershift/support/upsert"
	supportutil "github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"

	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capiopenstackv1beta1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/blang/semver"
	"github.com/pkg/errors"
)

const (
	finalizer                                = "hypershift.openshift.io/finalizer"
	autoscalerMaxAnnotation                  = "cluster.x-k8s.io/cluster-api-autoscaler-node-group-max-size"
	autoscalerMinAnnotation                  = "cluster.x-k8s.io/cluster-api-autoscaler-node-group-min-size"
	nodePoolAnnotation                       = "hypershift.openshift.io/nodePool"
	nodePoolAnnotationCurrentConfig          = "hypershift.openshift.io/nodePoolCurrentConfig"
	nodePoolAnnotationCurrentConfigVersion   = "hypershift.openshift.io/nodePoolCurrentConfigVersion"
	nodePoolAnnotationTargetConfigVersion    = "hypershift.openshift.io/nodePoolTargetConfigVersion"
	nodePoolAnnotationUpgradeInProgressTrue  = "hypershift.openshift.io/nodePoolUpgradeInProgressTrue"
	nodePoolAnnotationUpgradeInProgressFalse = "hypershift.openshift.io/nodePoolUpgradeInProgressFalse"
	nodePoolAnnotationMaxUnavailable         = "hypershift.openshift.io/nodePoolMaxUnavailable"

	// ec2InstanceMetadataHTTPTokensAnnotation can be set to change the instance metadata options of the nodepool underlying EC2 instances
	// possible values are 'required' (i.e. IMDSv2) or 'optional' which is the default.
	ec2InstanceMetadataHTTPTokensAnnotation = "hypershift.openshift.io/ec2-instance-metadata-http-tokens"

	nodePoolAnnotationPlatformMachineTemplate = "hypershift.openshift.io/nodePoolPlatformMachineTemplate"
	nodePoolAnnotationTaints                  = "hypershift.openshift.io/nodePoolTaints"
	nodePoolCoreIgnitionConfigLabel           = "hypershift.openshift.io/core-ignition-config"

	tuningConfigKey                                      = "tuning"
	tunedConfigMapLabel                                  = "hypershift.openshift.io/tuned-config"
	nodeTuningGeneratedConfigLabel                       = "hypershift.openshift.io/nto-generated-machine-config"
	PerformanceProfileConfigMapLabel                     = "hypershift.openshift.io/performanceprofile-config"
	NodeTuningGeneratedPerformanceProfileStatusLabel     = "hypershift.openshift.io/nto-generated-performance-profile-status"
	ContainerRuntimeConfigConfigMapLabel                 = "hypershift.openshift.io/containerruntimeconfig-config"
	KubeletConfigConfigMapLabel                          = "hypershift.openshift.io/kubeletconfig-config"
	controlPlaneOperatorManagesDecompressAndDecodeConfig = "io.openshift.hypershift.control-plane-operator-manages.decompress-decode-config"

	controlPlaneOperatorCreatesDefaultAWSSecurityGroup = "io.openshift.hypershift.control-plane-operator-creates-aws-sg"

	labelManagedPrefix = "managed.hypershift.openshift.io"
	// NTOMirroredConfigLabel added to objects that were mirrored from the node pool namespace into the HCP namespace
	NTOMirroredConfigLabel = "hypershift.openshift.io/mirrored-config"
)

type NodePoolReconciler struct {
	client.Client
	recorder        record.EventRecorder
	ReleaseProvider releaseinfo.Provider
	upsert.CreateOrUpdateProvider
	HypershiftOperatorImage string
	ImageMetadataProvider   supportutil.ImageMetadataProvider
	KubevirtInfraClients    kvinfra.KubevirtInfraClientMap

	EC2Client ec2iface.EC2API
}

type NotReadyError struct {
	error
}

type CPOCapabilities struct {
	DecompressAndDecodeConfig     bool
	CreateDefaultAWSSecurityGroup bool
}

var (
	// when using the conditions.SetSummary, with the WithStepCounter or WithStepCounterIf(true) options,
	// the result Ready condition message is something like "1 of 2 completed". If we want to use this kind
	// of messages for our own condition message, this is not useful. This regexp finds these condition messages
	isSetupCounterCondMessage = regexp.MustCompile(`\d+ of \d+ completed`)
)

var capiRelatedNodePoolManagedResourcesToWatch = []client.Object{
	&capiaws.AWSMachineTemplate{},
	&capiazure.AzureMachineTemplate{},
	&agentv1.AgentMachineTemplate{},
	&capiopenstackv1beta1.OpenStackMachineTemplate{},
}

func (r *NodePoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	bldr := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.NodePool{}, builder.WithPredicates(supportutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient()))).
		// We want to reconcile when the HostedCluster IgnitionEndpoint is available.
		Watches(&hyperv1.HostedCluster{}, handler.EnqueueRequestsFromMapFunc(r.enqueueNodePoolsForHostedCluster), builder.WithPredicates(supportutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient()))).
		Watches(&capiv1.MachineDeployment{}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool), builder.WithPredicates(supportutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient()))).
		Watches(&capiv1.MachineSet{}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool), builder.WithPredicates(supportutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient()))).
		// We want to reconcile when the user data Secret or the token Secret is unexpectedly changed out of band.
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool), builder.WithPredicates(supportutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient()))).
		// We want to reconcile when the ConfigMaps referenced by the spec.config and also the core ones change.
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(r.enqueueNodePoolsForConfig), builder.WithPredicates(supportutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient()))).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 10,
		})
	for _, managedResource := range r.managedResources() {
		bldr.Watches(managedResource, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool), builder.WithPredicates(supportutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient())))
	}
	if err := bldr.Complete(r); err != nil {
		return errors.Wrap(err, "failed setting up with a controller manager")
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}, builder.WithPredicates(supportutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient()))).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 10,
		}).
		Complete(&secretJanitor{
			NodePoolReconciler: r,
		}); err != nil {
		return errors.Wrap(err, "failed setting up with a controller manager")
	}

	r.recorder = mgr.GetEventRecorderFor("nodepool-controller")

	return nil
}

// managedResources are all the resources that are managed as childresources for a HostedCluster
func (r *NodePoolReconciler) managedResources() []client.Object {
	var managedResources []client.Object

	if platformsInstalled := os.Getenv("PLATFORMS_INSTALLED"); len(platformsInstalled) > 0 {
		// Watch based on platforms installed
		managedResources = append(managedResources, supportutil.GetNodePoolManagedResources(platformsInstalled)...)
	} else {
		// Watch all CAPI platform related resources
		managedResources = append(managedResources, capiRelatedNodePoolManagedResourcesToWatch...)
	}

	return managedResources
}

func (r *NodePoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling")

	// Fetch the nodePool instance
	nodePool := &hyperv1.NodePool{}
	err := r.Client.Get(ctx, req.NamespacedName, nodePool)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("not found", "request", req.String())
			return ctrl.Result{}, nil
		}
		log.Error(err, "error getting nodepool")
		return ctrl.Result{}, err
	}

	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(nodePool.Namespace, nodePool.Spec.ClusterName)

	// If deleted, clean up and return early.
	if !nodePool.DeletionTimestamp.IsZero() {
		if err := r.delete(ctx, nodePool, controlPlaneNamespace); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete nodepool: %w", err)
		}

		// Now we can remove the finalizer.
		if controllerutil.ContainsFinalizer(nodePool, finalizer) {
			controllerutil.RemoveFinalizer(nodePool, finalizer)
			if err := r.Update(ctx, nodePool); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from nodepool: %w", err)
			}
		}

		log.Info("Deleted nodepool", "name", req.NamespacedName)
		return ctrl.Result{}, nil
	}

	hcluster, err := GetHostedClusterByName(ctx, r.Client, nodePool.GetNamespace(), nodePool.Spec.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Ensure the nodePool has a finalizer for cleanup
	if !controllerutil.ContainsFinalizer(nodePool, finalizer) {
		controllerutil.AddFinalizer(nodePool, finalizer)
		if err := r.Update(ctx, nodePool); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to nodepool: %w", err)
		}
	}

	// Initialize the patch helper
	patchHelper, err := patch.NewHelper(nodePool, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	result, err := r.reconcile(ctx, hcluster, nodePool)
	if err != nil {
		log.Error(err, "Failed to reconcile NodePool")
		r.recorder.Eventf(nodePool, corev1.EventTypeWarning, "ReconcileError", "%v", err)
		if err := patchHelper.Patch(ctx, nodePool); err != nil {
			log.Error(err, "failed to patch")
			return ctrl.Result{}, fmt.Errorf("failed to patch: %w", err)
		}
		return result, err
	}

	if err := patchHelper.Patch(ctx, nodePool); err != nil {
		log.Error(err, "failed to patch")
		return ctrl.Result{}, fmt.Errorf("failed to patch: %w", err)
	}

	log.Info("Successfully reconciled")
	return result, nil
}

func (r *NodePoolReconciler) reconcile(ctx context.Context, hcluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	// HostedCluster owns NodePools. This should ensure orphan NodePools are garbage collected when cascading deleting.
	nodePool.OwnerReferences = util.EnsureOwnerRef(nodePool.OwnerReferences, metav1.OwnerReference{
		APIVersion: hyperv1.GroupVersion.String(),
		Kind:       "HostedCluster",
		Name:       hcluster.Name,
		UID:        hcluster.UID,
	})

	// Initialize NodePool annotations
	if nodePool.Annotations == nil {
		nodePool.Annotations = make(map[string]string)
	}

	// Get HostedCluster deps.
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
	infraID := hcluster.Spec.InfraID

	// Loop over all conditions.
	// Order matter as conditions might choose to short circuit returning ctrl.Result or error.
	signalConditions := map[string]func(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster) (*ctrl.Result, error){
		hyperv1.NodePoolAutoscalingEnabledConditionType:      r.autoscalerEnabledCondition,
		hyperv1.NodePoolUpdateManagementEnabledConditionType: r.updateManagementEnabledCondition,
		hyperv1.NodePoolValidReleaseImageConditionType:       r.releaseImageCondition,
		string(hyperv1.IgnitionEndpointAvailable):            r.ignitionEndpointAvailableCondition,
		hyperv1.NodePoolValidArchPlatform:                    r.validArchPlatformCondition,
		hyperv1.NodePoolReconciliationActiveConditionType:    r.reconciliationActiveCondition,
		// Conditition that depends on a valid release image.
		hyperv1.NodePoolValidMachineConfigConditionType: r.validMachineConfigCondition,
		hyperv1.NodePoolUpdatingConfigConditionType:     r.updatingConfigCondition,
		hyperv1.NodePoolUpdatingVersionConditionType:    r.updatingVersionCondition,
		// Conditition that depends on a valid config/token.
		hyperv1.NodePoolValidGeneratedPayloadConditionType: r.validGeneratedPayloadCondition,
		hyperv1.NodePoolReachedIgnitionEndpoint:            r.reachedIgnitionEndpointCondition,
		hyperv1.NodePoolAllMachinesReadyConditionType:      r.machineAndNodeConditions,
		hyperv1.NodePoolValidPlatformConfigConditionType:   r.validPlatformConfigCondition,
		// TODO(alberto): consider moving here:
		// NodePoolUpdatingPlatformMachineTemplateConditionType,
		// NodePoolAutorepairEnabledConditionType.
	}
	for _, f := range signalConditions {
		result, err := f(ctx, nodePool, hcluster)
		if result != nil || err != nil {
			return *result, err
		}
	}

	// Additional short circuiting validations:
	// TODO(alberto): capture these in conditions.
	// Consider having a condition "Degraded" with buckets for error types, e.g. INTERNAL_ERR, INFRA_ERR...
	if err := validateInfraID(infraID); err != nil {
		// We don't return the error here as reconciling won't solve the input problem.
		// An update event will trigger reconciliation.
		log.Error(err, "Invalid infraID, waiting.")
		return ctrl.Result{}, nil
	}
	// Retrieve pull secret name to check for changes when config is checked for updates
	_, err := r.getPullSecretName(ctx, hcluster)
	if err != nil {
		return ctrl.Result{}, err
	}
	if hcluster.Spec.AdditionalTrustBundle != nil {
		_, err = r.getAdditionalTrustBundle(ctx, hcluster)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	// Validate and get releaseImage.
	releaseImage, err := r.getReleaseImage(ctx, hcluster, nodePool.Status.Version, nodePool.Spec.Release.Image)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to look up release image metadata: %w", err)
	}

	if err := r.setPlatformConditions(ctx, hcluster, nodePool, controlPlaneNamespace, releaseImage); err != nil {
		return ctrl.Result{}, err
	}

	if hcluster.Status.KubeConfig == nil {
		log.Info("waiting on hostedCluster.status.kubeConfig to be set")
		return ctrl.Result{}, nil
	}

	haproxyRawConfig, err := r.generateHAProxyRawConfig(ctx, hcluster, releaseImage)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to generate HAProxy raw config: %w", err)
	}
	configGenerator, err := NewConfigGenerator(ctx, r.Client, hcluster, nodePool, releaseImage, haproxyRawConfig)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to generate config: %w", err)
	}

	cpoCapabilities, err := r.detectCPOCapabilities(ctx, hcluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to detect CPO capabilities: %w", err)
	}
	token, err := NewToken(ctx, configGenerator, cpoCapabilities)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create token: %w", err)
	}

	if err := r.ntoReconcile(ctx, nodePool, configGenerator, controlPlaneNamespace); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile NTO: %w", err)
	}

	// If reconciliation is paused we return before modifying any state
	capi, err := newCAPI(token, infraID)
	if err != nil {
		return ctrl.Result{}, err
	}
	if isPaused, duration := supportutil.IsReconciliationPaused(log, nodePool.Spec.PausedUntil); isPaused {
		if err := capi.Pause(ctx); err != nil {
			return ctrl.Result{}, fmt.Errorf("error pausing CAPI: %w", err)
		}
		log.Info("Reconciliation paused", "pausedUntil", *nodePool.Spec.PausedUntil)
		return ctrl.Result{RequeueAfter: duration}, nil
	}

	// 2. - Reconcile towards expected state of the world.
	if err := token.Reconcile(ctx); err != nil {
		return ctrl.Result{}, err
	}

	// non automated infrastructure should not have any machine level cluster-api components
	if !isAutomatedMachineManagement(nodePool) {
		targetConfigHash := token.HashWithoutVersion()
		targetPayloadConfigHash := token.Hash()
		nodePool.Status.Version = releaseImage.Version()
		if nodePool.Annotations == nil {
			nodePool.Annotations = make(map[string]string)
		}
		if nodePool.Annotations[nodePoolAnnotationCurrentConfig] != targetConfigHash {
			log.Info("Config update complete",
				"previous", nodePool.Annotations[nodePoolAnnotationCurrentConfig], "new", targetConfigHash)
			nodePool.Annotations[nodePoolAnnotationCurrentConfig] = targetConfigHash
		}
		nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion] = targetPayloadConfigHash
		return ctrl.Result{}, nil
	}

	if err := capi.Reconcile(ctx); err != nil {
		if _, isNotReady := err.(*NotReadyError); isNotReady {
			log.Info("Waiting to create machine template", "message", err.Error())
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *NodePoolReconciler) token(ctx context.Context, hcluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool) (*Token, error) {
	// Validate and get releaseImage.
	releaseImage, err := r.getReleaseImage(ctx, hcluster, nodePool.Status.Version, nodePool.Spec.Release.Image)
	if err != nil {
		return nil, fmt.Errorf("failed to look up release image metadata: %w", err)
	}

	haproxyRawConfig, err := r.generateHAProxyRawConfig(ctx, hcluster, releaseImage)
	if err != nil {
		return nil, fmt.Errorf("failed to generate HAProxy raw config: %w", err)
	}
	configGenerator, err := NewConfigGenerator(ctx, r.Client, hcluster, nodePool, releaseImage, haproxyRawConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to generate config: %w", err)
	}

	cpoCapabilities, err := r.detectCPOCapabilities(ctx, hcluster)
	if err != nil {
		return nil, fmt.Errorf("failed to detect CPO capabilities: %w", err)
	}
	token, err := NewToken(ctx, configGenerator, cpoCapabilities)
	if err != nil {
		return nil, fmt.Errorf("failed to create token: %w", err)
	}
	return token, nil
}

func isArchAndPlatformSupported(nodePool *hyperv1.NodePool) bool {
	supported := false

	switch nodePool.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		if nodePool.Spec.Arch == hyperv1.ArchitectureAMD64 || nodePool.Spec.Arch == hyperv1.ArchitectureARM64 {
			supported = true
		}
	case hyperv1.AzurePlatform:
		if nodePool.Spec.Arch == hyperv1.ArchitectureAMD64 || nodePool.Spec.Arch == hyperv1.ArchitectureARM64 {
			supported = true
		}
	case hyperv1.KubevirtPlatform:
		if nodePool.Spec.Arch == hyperv1.ArchitectureAMD64 || nodePool.Spec.Arch == hyperv1.ArchitectureS390X {
			supported = true
		}
	case hyperv1.OpenStackPlatform:
		if nodePool.Spec.Arch == hyperv1.ArchitectureAMD64 {
			supported = true
		}
	case hyperv1.AgentPlatform:
		if nodePool.Spec.Arch == hyperv1.ArchitectureAMD64 || nodePool.Spec.Arch == hyperv1.ArchitecturePPC64LE || nodePool.Spec.Arch == hyperv1.ArchitectureARM64 {
			supported = true
		}
	case hyperv1.PowerVSPlatform:
		if nodePool.Spec.Arch == hyperv1.ArchitecturePPC64LE {
			supported = true
		}
	case hyperv1.NonePlatform:
		if nodePool.Spec.Arch == hyperv1.ArchitectureAMD64 || nodePool.Spec.Arch == hyperv1.ArchitectureARM64 {
			supported = true
		}
	}

	return supported
}

func (r *NodePoolReconciler) delete(ctx context.Context, nodePool *hyperv1.NodePool, controlPlaneNamespace string) error {
	capi := &CAPI{
		Token: &Token{
			CreateOrUpdateProvider: r.CreateOrUpdateProvider,
			ConfigGenerator: &ConfigGenerator{
				Client:                r.Client,
				nodePool:              nodePool,
				controlplaneNamespace: controlPlaneNamespace,
				rolloutConfig:         &rolloutConfig{},
			},
		},
	}
	md := capi.machineDeployment()
	ms := capi.machineSet()
	mhc := capi.machineHealthCheck()
	machineTemplates, err := capi.listMachineTemplates()
	if err != nil {
		return fmt.Errorf("failed to list MachineTemplates: %w", err)
	}
	for k := range machineTemplates {
		if err := r.Delete(ctx, machineTemplates[k]); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete MachineTemplate: %w", err)
		}
	}

	if err := deleteMachineDeployment(ctx, r.Client, md); err != nil {
		return fmt.Errorf("failed to delete MachineDeployment: %w", err)
	}

	if err := deleteMachineHealthCheck(ctx, r.Client, mhc); err != nil {
		return fmt.Errorf("failed to delete MachineHealthCheck: %w", err)
	}

	if err := deleteMachineSet(ctx, r.Client, ms); err != nil {
		return fmt.Errorf("failed to delete MachineSet: %w", err)
	}

	// Delete any ConfigMap belonging to this NodePool i.e. TunedConfig ConfigMaps.
	err = r.DeleteAllOf(ctx, &corev1.ConfigMap{},
		client.InNamespace(controlPlaneNamespace),
		client.MatchingLabels{nodePoolAnnotation: nodePool.GetName()},
	)
	if err != nil {
		return fmt.Errorf("failed to delete ConfigMaps with nodePool label: %w", err)
	}

	// Ensure all machines in NodePool are deleted
	if err = r.ensureMachineDeletion(ctx, nodePool); err != nil {
		return err
	}

	// Delete all secrets related to the NodePool
	if err = r.deleteNodePoolSecrets(ctx, nodePool); err != nil {
		return fmt.Errorf("failed to delete NodePool secrets: %w", err)
	}

	err = r.deleteKubeVirtCache(ctx, nodePool, controlPlaneNamespace)
	if err != nil {
		return err
	}

	r.KubevirtInfraClients.Delete(string(nodePool.GetUID()))

	return nil
}

func (r *NodePoolReconciler) deleteKubeVirtCache(ctx context.Context, nodePool *hyperv1.NodePool, controlPlaneNamespace string) error {
	if nodePool.Status.Platform != nil {
		if nodePool.Status.Platform.KubeVirt != nil {
			if cacheName := nodePool.Status.Platform.KubeVirt.CacheName; len(cacheName) > 0 {
				uid := string(nodePool.GetUID())
				cl, err := r.KubevirtInfraClients.DiscoverKubevirtClusterClient(ctx, r.Client, uid, nodePool.Status.Platform.KubeVirt.Credentials, controlPlaneNamespace, nodePool.GetNamespace())
				if err != nil {
					return fmt.Errorf("failed to get KubeVirt external infra-cluster:  %w", err)
				}

				ns := controlPlaneNamespace
				if nodePool.Status.Platform.KubeVirt.Credentials != nil && len(nodePool.Status.Platform.KubeVirt.Credentials.InfraNamespace) > 0 {
					ns = nodePool.Status.Platform.KubeVirt.Credentials.InfraNamespace
				}

				err = kubevirt.DeleteCache(ctx, cl.GetInfraClient(), cacheName, ns)
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// deleteNodePoolSecrets deletes any secret belonging to this NodePool (ex. token Secret and userdata Secret)
func (r *NodePoolReconciler) deleteNodePoolSecrets(ctx context.Context, nodePool *hyperv1.NodePool) error {
	secrets, err := r.listSecrets(ctx, nodePool)
	if err != nil {
		return fmt.Errorf("failed to list secrets: %w", err)
	}
	for k := range secrets {
		if err := r.Delete(ctx, &secrets[k]); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete secret: %w", err)
		}
	}
	return nil
}

// validateManagement does additional backend validation. API validation/default should
// prevent this from ever fail.
func validateManagement(nodePool *hyperv1.NodePool) error {
	// TODO actually validate the inplace upgrade type
	if nodePool.Spec.Management.UpgradeType == hyperv1.UpgradeTypeInPlace {
		return nil
	}

	// Only upgradeType "Replace" is supported atm.
	if nodePool.Spec.Management.UpgradeType != hyperv1.UpgradeTypeReplace ||
		nodePool.Spec.Management.Replace == nil {
		return fmt.Errorf("this is unsupported. %q upgrade type and a strategy: %q or %q are required",
			hyperv1.UpgradeTypeReplace, hyperv1.UpgradeStrategyRollingUpdate, hyperv1.UpgradeStrategyOnDelete)
	}

	if nodePool.Spec.Management.Replace.Strategy != hyperv1.UpgradeStrategyRollingUpdate &&
		nodePool.Spec.Management.Replace.Strategy != hyperv1.UpgradeStrategyOnDelete {
		return fmt.Errorf("this is unsupported. %q upgrade type only support strategies %q and %q",
			hyperv1.UpgradeTypeReplace, hyperv1.UpgradeStrategyOnDelete, hyperv1.UpgradeStrategyRollingUpdate)
	}

	// RollingUpdate strategy requires MaxUnavailable and MaxSurge
	if nodePool.Spec.Management.Replace.Strategy == hyperv1.UpgradeStrategyRollingUpdate &&
		nodePool.Spec.Management.Replace.RollingUpdate == nil {
		return fmt.Errorf("this is unsupported. %q upgrade type with strategy %q require a MaxUnavailable and MaxSurge",
			hyperv1.UpgradeTypeReplace, hyperv1.UpgradeStrategyRollingUpdate)
	}

	return nil
}

func (r *NodePoolReconciler) getReleaseImage(ctx context.Context, hostedCluster *hyperv1.HostedCluster, currentVersion string, releaseImage string) (*releaseinfo.ReleaseImage, error) {
	pullSecretBytes, err := r.getPullSecretBytes(ctx, hostedCluster)
	if err != nil {
		return nil, err
	}
	ReleaseImage, err := func(ctx context.Context) (*releaseinfo.ReleaseImage, error) {
		lookupCtx, lookupCancel := context.WithTimeout(ctx, 1*time.Minute)
		defer lookupCancel()
		img, err := r.ReleaseProvider.Lookup(lookupCtx, releaseImage, pullSecretBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to look up release image metadata: %w", err)
		}
		return img, nil
	}(ctx)
	if err != nil {
		return nil, err
	}

	if _, exists := hostedCluster.Annotations[hyperv1.SkipReleaseImageValidation]; exists {
		return ReleaseImage, nil
	}

	wantedVersion, err := semver.Parse(ReleaseImage.Version())
	if err != nil {
		return nil, err
	}

	var currentVersionParsed semver.Version
	if currentVersion != "" {
		currentVersionParsed, err = semver.Parse(currentVersion)
		if err != nil {
			return nil, err
		}
	}

	minSupportedVersion := supportedversion.GetMinSupportedVersion(hostedCluster)

	hostedClusterVersion, err := r.getHostedClusterVersion(ctx, hostedCluster, pullSecretBytes)
	if err != nil {
		return nil, err
	}

	return ReleaseImage, supportedversion.IsValidReleaseVersion(&wantedVersion, &currentVersionParsed, hostedClusterVersion, &minSupportedVersion, hostedCluster.Spec.Networking.NetworkType, hostedCluster.Spec.Platform.Type)
}

func (r *NodePoolReconciler) getHostedClusterVersion(ctx context.Context, hostedCluster *hyperv1.HostedCluster, pullSecretBytes []byte) (*semver.Version, error) {
	if hostedCluster.Status.Version != nil && len(hostedCluster.Status.Version.History) > 0 {
		for _, version := range hostedCluster.Status.Version.History {
			// find first completed version
			if version.CompletionTime == nil {
				continue
			}

			hostedClusterVersion, err := semver.Parse(version.Version)
			if err != nil {
				return nil, err
			}
			return &hostedClusterVersion, nil
		}
	}

	// use Spec.Release.Image if there is no completed version yet. This could happen at the initial creation of the cluster.
	releaseInfo, err := r.ReleaseProvider.Lookup(ctx, hostedCluster.Spec.Release.Image, pullSecretBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup release image: %w", err)
	}
	hostedClusterVersion, err := semver.Parse(releaseInfo.Version())
	if err != nil {
		return nil, err
	}
	return &hostedClusterVersion, nil
}

func isUpdatingVersion(nodePool *hyperv1.NodePool, targetVersion string) bool {
	return targetVersion != nodePool.Status.Version
}

func isUpdatingConfig(nodePool *hyperv1.NodePool, targetConfigHash string) bool {
	return targetConfigHash != nodePool.GetAnnotations()[nodePoolAnnotationCurrentConfig]
}

func isUpdatingMachineTemplate(nodePool *hyperv1.NodePool, targetMachineTemplate string) bool {
	return targetMachineTemplate != nodePool.GetAnnotations()[nodePoolAnnotationPlatformMachineTemplate]
}

func isAutoscalingEnabled(nodePool *hyperv1.NodePool) bool {
	return nodePool.Spec.AutoScaling != nil
}

func defaultNodePoolAMI(region string, specifiedArch string, releaseImage *releaseinfo.ReleaseImage) (string, error) {
	arch, foundArch := releaseImage.StreamMetadata.Architectures[hyperv1.ArchAliases[specifiedArch]]
	if !foundArch {
		return "", fmt.Errorf("couldn't find OS metadata for architecture %q", specifiedArch)
	}

	regionData, hasRegionData := arch.Images.AWS.Regions[region]
	if !hasRegionData {
		return "", fmt.Errorf("couldn't find AWS image for region %q", region)
	}
	if len(regionData.Image) == 0 {
		return "", fmt.Errorf("release image metadata has no image for region %q", region)
	}
	return regionData.Image, nil
}

// MachineDeploymentComplete considers a MachineDeployment to be complete once all of its desired replicas
// are updated and available, and no old machines are running.
func MachineDeploymentComplete(deployment *capiv1.MachineDeployment) bool {
	newStatus := &deployment.Status
	return newStatus.UpdatedReplicas == *(deployment.Spec.Replicas) &&
		newStatus.Replicas == *(deployment.Spec.Replicas) &&
		newStatus.AvailableReplicas == *(deployment.Spec.Replicas) &&
		newStatus.ObservedGeneration >= deployment.Generation
}

// GetHostedClusterByName finds and return a HostedCluster object using the specified params.
func GetHostedClusterByName(ctx context.Context, c client.Client, namespace, name string) (*hyperv1.HostedCluster, error) {
	hcluster := &hyperv1.HostedCluster{}
	key := client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}

	if err := c.Get(ctx, key, hcluster); err != nil {
		return nil, err
	}

	return hcluster, nil
}

func (r *NodePoolReconciler) enqueueNodePoolsForHostedCluster(ctx context.Context, obj client.Object) []reconcile.Request {
	var result []reconcile.Request

	hc, ok := obj.(*hyperv1.HostedCluster)
	if !ok {
		panic(fmt.Sprintf("Expected a HostedCluster but got a %T", obj))
	}

	nodePoolList := &hyperv1.NodePoolList{}
	if err := r.List(ctx, nodePoolList, client.InNamespace(hc.Namespace)); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "Failed to list nodePools")
		return result
	}

	// Requeue all NodePools matching the HostedCluster name.
	for key := range nodePoolList.Items {
		if nodePoolList.Items[key].Spec.ClusterName == hc.GetName() {
			result = append(result,
				reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&nodePoolList.Items[key])},
			)
		}
	}

	return result
}

func (r *NodePoolReconciler) enqueueNodePoolsForConfig(ctx context.Context, obj client.Object) []reconcile.Request {
	var result []reconcile.Request

	cm, ok := obj.(*corev1.ConfigMap)
	if !ok {
		panic(fmt.Sprintf("Expected a ConfigMap but got a %T", obj))
	}

	// Get all NodePools in the ConfigMap Namespace.
	nodePoolList := &hyperv1.NodePoolList{}
	if err := r.List(ctx, nodePoolList, client.InNamespace(cm.Namespace)); err != nil {
		return result
	}

	// If the ConfigMap is a core one reconcile all NodePools.
	if _, ok := obj.GetLabels()[nodePoolCoreIgnitionConfigLabel]; ok {
		for key := range nodePoolList.Items {
			result = append(result,
				reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&nodePoolList.Items[key])},
			)
		}
		return result
	}

	// If the ConfigMap is generated by the NodePool controller and contains Tuned manifests
	// return the ConfigMaps parent NodePool.
	if isNodePoolGeneratedTuningConfigMap(cm) {
		return enqueueParentNodePool(ctx, obj)
	}

	// Check if the ConfigMap is generated by an operator in the control plane namespace
	// corresponding to this nodepool.
	_, isNodeTuningGeneratedConfigLabel := obj.GetLabels()[nodeTuningGeneratedConfigLabel]
	_, isNodeTuningGeneratedPerformanceProfileStatusLabel := obj.GetLabels()[NodeTuningGeneratedPerformanceProfileStatusLabel]
	if isNodeTuningGeneratedConfigLabel || isNodeTuningGeneratedPerformanceProfileStatusLabel {
		nodePoolName := obj.GetLabels()[hyperv1.NodePoolLabel]
		nodePoolNamespacedName, err := r.getNodePoolNamespacedName(nodePoolName, obj.GetNamespace())
		if err != nil {
			return result
		}
		obj.SetAnnotations(map[string]string{
			nodePoolAnnotation: nodePoolNamespacedName.String(),
		})
		return enqueueParentNodePool(ctx, obj)
	}

	// Otherwise reconcile NodePools which are referencing the given ConfigMap.
	for key := range nodePoolList.Items {
		reconcileNodePool := false
		for _, v := range nodePoolList.Items[key].Spec.Config {
			if v.Name == cm.Name {
				reconcileNodePool = true
				break
			}
		}

		// Check TuningConfig as well, unless ConfigMap was already found in .Spec.Config.
		if !reconcileNodePool {
			for _, v := range nodePoolList.Items[key].Spec.TuningConfig {
				if v.Name == cm.Name {
					reconcileNodePool = true
					break
				}
			}
		}
		if reconcileNodePool {
			result = append(result,
				reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&nodePoolList.Items[key])},
			)
		}

	}

	return result
}

// getNodePoolNamespace returns the namespaced name of a NodePool, given the NodePools name
// and the control plane namespace name for the hosted cluster that this NodePool is a part of.
func (r *NodePoolReconciler) getNodePoolNamespacedName(nodePoolName string, controlPlaneNamespace string) (types.NamespacedName, error) {
	hcpList := &hyperv1.HostedControlPlaneList{}
	if err := r.List(context.Background(), hcpList, &client.ListOptions{
		Namespace: controlPlaneNamespace,
	}); err != nil || len(hcpList.Items) < 1 {
		return types.NamespacedName{Name: nodePoolName}, err
	}
	hostedCluster, ok := hcpList.Items[0].Annotations[supportutil.HostedClusterAnnotation]
	if !ok {
		return types.NamespacedName{Name: nodePoolName}, fmt.Errorf("failed to get Hosted Cluster name for HostedControlPlane %s", hcpList.Items[0].Name)
	}
	nodePoolNamespace := supportutil.ParseNamespacedName(hostedCluster).Namespace

	return types.NamespacedName{Name: nodePoolName, Namespace: nodePoolNamespace}, nil
}

func isNodePoolGeneratedTuningConfigMap(cm *corev1.ConfigMap) bool {
	if _, ok := cm.GetLabels()[tunedConfigMapLabel]; ok {
		return true
	}
	_, ok := cm.GetLabels()[PerformanceProfileConfigMapLabel]
	return ok
}

func enqueueParentNodePool(ctx context.Context, obj client.Object) []reconcile.Request {
	var nodePoolName string
	if obj.GetAnnotations() != nil {
		nodePoolName = obj.GetAnnotations()[nodePoolAnnotation]
	}
	if nodePoolName == "" {
		return []reconcile.Request{}
	}
	return []reconcile.Request{
		{NamespacedName: supportutil.ParseNamespacedName(nodePoolName)},
	}
}

func (r *NodePoolReconciler) listSecrets(ctx context.Context, nodePool *hyperv1.NodePool) ([]corev1.Secret, error) {
	secretList := &corev1.SecretList{}
	if err := r.List(ctx, secretList); err != nil {
		return nil, fmt.Errorf("failed to list secrets: %w", err)
	}
	filtered := []corev1.Secret{}
	for i, secret := range secretList.Items {
		if secret.GetAnnotations() != nil {
			if annotation, ok := secret.GetAnnotations()[nodePoolAnnotation]; ok &&
				annotation == client.ObjectKeyFromObject(nodePool).String() {
				filtered = append(filtered, secretList.Items[i])
			}
		}
	}
	return filtered, nil
}
func isAutomatedMachineManagement(nodePool *hyperv1.NodePool) bool {
	return !(isIBMUPI(nodePool) || isPlatformNone(nodePool))
}

func isIBMUPI(nodePool *hyperv1.NodePool) bool {
	return nodePool.Spec.Platform.IBMCloud != nil && nodePool.Spec.Platform.IBMCloud.ProviderType == configv1.IBMCloudProviderTypeUPI
}

func isPlatformNone(nodePool *hyperv1.NodePool) bool {
	return nodePool.Spec.Platform.Type == hyperv1.NonePlatform
}

func validateInfraID(infraID string) error {
	if infraID == "" {
		return fmt.Errorf("infraID can't be empty")
	}
	return nil
}

func (r *NodePoolReconciler) detectCPOCapabilities(ctx context.Context, hostedCluster *hyperv1.HostedCluster) (*CPOCapabilities, error) {
	pullSecretBytes, err := r.getPullSecretBytes(ctx, hostedCluster)
	if err != nil {
		return nil, err
	}
	controlPlaneOperatorImage, err := supportutil.GetControlPlaneOperatorImage(ctx, hostedCluster, r.ReleaseProvider, r.HypershiftOperatorImage, pullSecretBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to get controlPlaneOperatorImage: %w", err)
	}

	controlPlaneOperatorImageMetadata, err := r.ImageMetadataProvider.ImageMetadata(ctx, controlPlaneOperatorImage, pullSecretBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to look up image metadata for %s: %w", controlPlaneOperatorImage, err)
	}

	imageLabels := supportutil.ImageLabels(controlPlaneOperatorImageMetadata)
	result := &CPOCapabilities{}
	_, result.DecompressAndDecodeConfig = imageLabels[controlPlaneOperatorManagesDecompressAndDecodeConfig]
	_, result.CreateDefaultAWSSecurityGroup = imageLabels[controlPlaneOperatorCreatesDefaultAWSSecurityGroup]

	return result, nil
}

// getPullSecretBytes retrieves the pull secret bytes from the hosted cluster
func (r *NodePoolReconciler) getPullSecretBytes(ctx context.Context, hostedCluster *hyperv1.HostedCluster) ([]byte, error) {
	pullSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hostedCluster.Namespace, Name: hostedCluster.Spec.PullSecret.Name}, pullSecret); err != nil {
		return nil, fmt.Errorf("cannot get pull secret %s/%s: %w", hostedCluster.Namespace, hostedCluster.Spec.PullSecret.Name, err)
	}
	if _, hasKey := pullSecret.Data[corev1.DockerConfigJsonKey]; !hasKey {
		return nil, fmt.Errorf("pull secret %s/%s missing %q key", pullSecret.Namespace, pullSecret.Name, corev1.DockerConfigJsonKey)
	}
	return pullSecret.Data[corev1.DockerConfigJsonKey], nil
}

// getPullSecretName retrieves the name of the pull secret in the hosted cluster spec
func (r *NodePoolReconciler) getPullSecretName(ctx context.Context, hostedCluster *hyperv1.HostedCluster) (string, error) {
	return getPullSecretName(ctx, r.Client, hostedCluster)
}

func getPullSecretName(ctx context.Context, crclient client.Client, hostedCluster *hyperv1.HostedCluster) (string, error) {
	pullSecret := &corev1.Secret{}
	if err := crclient.Get(ctx, client.ObjectKey{Namespace: hostedCluster.Namespace, Name: hostedCluster.Spec.PullSecret.Name}, pullSecret); err != nil {
		return "", fmt.Errorf("cannot get pull secret %s/%s: %w", hostedCluster.Namespace, hostedCluster.Spec.PullSecret.Name, err)
	}
	if _, hasKey := pullSecret.Data[corev1.DockerConfigJsonKey]; !hasKey {
		return "", fmt.Errorf("pull secret %s/%s missing %q key when retrieving pull secret name", pullSecret.Namespace, pullSecret.Name, corev1.DockerConfigJsonKey)
	}
	return pullSecret.Name, nil
}

func (r *NodePoolReconciler) getAdditionalTrustBundle(ctx context.Context, hostedCluster *hyperv1.HostedCluster) (*corev1.ConfigMap, error) {
	additionalTrustBundle := &corev1.ConfigMap{}
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hostedCluster.Namespace, Name: hostedCluster.Spec.AdditionalTrustBundle.Name}, additionalTrustBundle); err != nil {
		return additionalTrustBundle, fmt.Errorf("cannot get additionalTrustBundle %s/%s: %w", hostedCluster.Namespace, hostedCluster.Spec.AdditionalTrustBundle.Name, err)
	}
	if _, hasKey := additionalTrustBundle.Data["ca-bundle.crt"]; !hasKey {
		return additionalTrustBundle, fmt.Errorf(" additionalTrustBundle %s/%s missing %q key", additionalTrustBundle.Namespace, additionalTrustBundle.Name, "ca-bundle.crt")
	}
	return additionalTrustBundle, nil
}

func (r *NodePoolReconciler) generateHAProxyRawConfig(ctx context.Context, hcluster *hyperv1.HostedCluster, releaseImage *releaseinfo.ReleaseImage) (string, error) {
	haProxyImage, ok := releaseImage.ComponentImages()[haproxy.HAProxyRouterImageName]
	if !ok {
		return "", fmt.Errorf("release image doesn't have a %s image", haproxy.HAProxyRouterImageName)
	}

	haProxy := haproxy.HAProxy{
		Client:                  r.Client,
		HAProxyImage:            haProxyImage,
		HypershiftOperatorImage: r.HypershiftOperatorImage,
		ReleaseProvider:         r.ReleaseProvider,
		ImageMetadataProvider:   r.ImageMetadataProvider,
	}
	return haProxy.GenerateHAProxyRawConfig(ctx, hcluster)
}

// machinesByCreationTimestamp sorts a list of Machine by creation timestamp, using their names as a tie breaker.
type machinesByCreationTimestamp []*capiv1.Machine

func (o machinesByCreationTimestamp) Len() int      { return len(o) }
func (o machinesByCreationTimestamp) Swap(i, j int) { o[i], o[j] = o[j], o[i] }
func (o machinesByCreationTimestamp) Less(i, j int) bool {
	if o[i].CreationTimestamp.Equal(&o[j].CreationTimestamp) {
		return o[i].Name < o[j].Name
	}
	return o[i].CreationTimestamp.Before(&o[j].CreationTimestamp)
}

// SortedByCreationTimestamp returns the machines sorted by creation timestamp.
func sortedByCreationTimestamp(machines []*capiv1.Machine) []*capiv1.Machine {
	sort.Sort(machinesByCreationTimestamp(machines))
	return machines
}

const (
	endOfMessage                         = "... too many similar errors\n"
	maxMessageLength                     = 1000
	aggregatorMachineStateReady          = "ready"
	aggregatorMachineStateLiveMigratable = "live migratable"
)

func aggregateMachineReasonsAndMessages(messageMap map[string][]string, numMachines, numNotReady int, state string) (string, string) {
	msgBuilder := &strings.Builder{}
	reasons := make([]string, len(messageMap))

	msgBuilder.WriteString(fmt.Sprintf("%d of %d machines are not %s\n", numNotReady, numMachines, state))

	// as map order is not deterministic, we must sort the reasons in order to get deterministic reason and message, so
	// we won't need to update the nodepool condition just because we've got different order from the map, the machine
	// conditions weren't actually changed.
	i := 0
	for reason := range messageMap {
		reasons[i] = reason
		i++
	}
	sort.Strings(reasons)

	for _, reason := range reasons {
		msgBuilder.WriteString(aggregateMachineMessages(messageMap[reason]))
	}

	return strings.Join(reasons, ","), msgBuilder.String()
}

func aggregateMachineMessages(msgs []string) string {
	builder := strings.Builder{}
	for _, msg := range msgs {
		if builder.Len()+len(msg) > maxMessageLength {
			builder.WriteString(endOfMessage)
			break
		}

		builder.WriteString(msg)
	}

	return builder.String()
}

func deleteConfigByLabel(ctx context.Context, c client.Client, lbl map[string]string, controlPlaneNamespace string) error {
	cmList := &corev1.ConfigMapList{}
	if err := c.List(ctx, cmList, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(lbl),
		Namespace:     controlPlaneNamespace,
	}); err != nil {
		return err
	}
	for i := range cmList.Items {
		cm := &cmList.Items[i]
		if _, err := supportutil.DeleteIfNeeded(ctx, c, cm); err != nil {
			return err
		}
	}
	return nil
}

// validateHCPayloadSupportsNodePoolCPUArch validates the HostedCluster payload can support the NodePool's CPU arch
func validateHCPayloadSupportsNodePoolCPUArch(hc *hyperv1.HostedCluster, np *hyperv1.NodePool) error {
	if hc.Status.PayloadArch == hyperv1.Multi {
		return nil
	}

	if hc.Status.PayloadArch == hyperv1.ToPayloadArch(np.Spec.Arch) {
		return nil
	}

	return fmt.Errorf("NodePool CPU arch, %s, is not supported by the HostedCluster payload type, %s; either change the NodePool CPU arch or use a multi-arch release image", np.Spec.Arch, hc.Status.PayloadArch)
}
