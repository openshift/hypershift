package nodepool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/blang/semver"
	configv1 "github.com/openshift/api/config/v1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/kubevirt"
	ignserver "github.com/openshift/hypershift/ignition-server/controllers"
	kvinfra "github.com/openshift/hypershift/kubevirtexternalinfra"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/supportedversion"
	"github.com/openshift/hypershift/support/upsert"
	supportutil "github.com/openshift/hypershift/support/util"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
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

	tuningConfigKey                                  = "tuning"
	tunedConfigMapLabel                              = "hypershift.openshift.io/tuned-config"
	nodeTuningGeneratedConfigLabel                   = "hypershift.openshift.io/nto-generated-machine-config"
	PerformanceProfileConfigMapLabel                 = "hypershift.openshift.io/performanceprofile-config"
	NodeTuningGeneratedPerformanceProfileStatusLabel = "hypershift.openshift.io/nto-generated-performance-profile-status"
	ContainerRuntimeConfigConfigMapLabel             = "hypershift.openshift.io/containerruntimeconfig-config"

	controlPlaneOperatorManagesDecompressAndDecodeConfig = "io.openshift.hypershift.control-plane-operator-manages.decompress-decode-config"

	controlPlaneOperatorCreatesDefaultAWSSecurityGroup = "io.openshift.hypershift.control-plane-operator-creates-aws-sg"

	labelManagedPrefix = "managed.hypershift.openshift.io"
	// mirroredConfigLabel added to objects that were mirrored from the node pool namespace into the HCP namespace
	mirroredConfigLabel = "hypershift.openshift.io/mirrored-config"
)

type NodePoolReconciler struct {
	client.Client
	recorder        record.EventRecorder
	ReleaseProvider releaseinfo.Provider
	upsert.CreateOrUpdateProvider
	HypershiftOperatorImage string
	ImageMetadataProvider   supportutil.ImageMetadataProvider
	KubevirtInfraClients    kvinfra.KubevirtInfraClientMap
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

func (r *NodePoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.NodePool{}, builder.WithPredicates(supportutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient()))).
		// We want to reconcile when the HostedCluster IgnitionEndpoint is available.
		Watches(&hyperv1.HostedCluster{}, handler.EnqueueRequestsFromMapFunc(r.enqueueNodePoolsForHostedCluster), builder.WithPredicates(supportutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient()))).
		Watches(&capiv1.MachineDeployment{}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool), builder.WithPredicates(supportutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient()))).
		Watches(&capiv1.MachineSet{}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool), builder.WithPredicates(supportutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient()))).
		Watches(&capiaws.AWSMachineTemplate{}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool), builder.WithPredicates(supportutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient()))).
		Watches(&agentv1.AgentMachineTemplate{}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool), builder.WithPredicates(supportutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient()))).
		Watches(&capiazure.AzureMachineTemplate{}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool), builder.WithPredicates(supportutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient()))).
		// We want to reconcile when the user data Secret or the token Secret is unexpectedly changed out of band.
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool), builder.WithPredicates(supportutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient()))).
		// We want to reconcile when the ConfigMaps referenced by the spec.config and also the core ones change.
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(r.enqueueNodePoolsForConfig), builder.WithPredicates(supportutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient()))).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 10,
		}).
		Complete(r); err != nil {
		return errors.Wrap(err, "failed setting up with a controller manager")
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
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
	ignEndpoint := hcluster.Status.IgnitionEndpoint
	infraID := hcluster.Spec.InfraID

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

	if isAutoscalingEnabled(nodePool) {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolAutoscalingEnabledConditionType,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			Message:            fmt.Sprintf("Maximum nodes: %v, Minimum nodes: %v", nodePool.Spec.AutoScaling.Max, nodePool.Spec.AutoScaling.Min),
			ObservedGeneration: nodePool.Generation,
		})
	} else {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolAutoscalingEnabledConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.AsExpectedReason,
			ObservedGeneration: nodePool.Generation,
		})
	}

	// Validate management input.
	if err := validateManagement(nodePool); err != nil {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdateManagementEnabledConditionType,
			Status:             corev1.ConditionFalse,
			Message:            err.Error(),
			Reason:             hyperv1.NodePoolValidationFailedReason,
			ObservedGeneration: nodePool.Generation,
		})
		// We don't return the error here as reconciling won't solve the input problem.
		// An update event will trigger reconciliation.
		log.Error(err, "validating management parameters failed")
		return ctrl.Result{}, nil
	}
	SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolUpdateManagementEnabledConditionType,
		Status:             corev1.ConditionTrue,
		Reason:             hyperv1.AsExpectedReason,
		ObservedGeneration: nodePool.Generation,
	})

	// Validate and get releaseImage.
	releaseImage, err := r.getReleaseImage(ctx, hcluster, nodePool.Status.Version, nodePool.Spec.Release.Image)
	if err != nil {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidReleaseImageConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.NodePoolValidationFailedReason,
			Message:            fmt.Sprintf("Failed to get release image: %v", err.Error()),
			ObservedGeneration: nodePool.Generation,
		})
		return ctrl.Result{}, fmt.Errorf("failed to look up release image metadata: %w", err)
	}
	SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolValidReleaseImageConditionType,
		Status:             corev1.ConditionTrue,
		Reason:             hyperv1.AsExpectedReason,
		Message:            fmt.Sprintf("Using release image: %s", nodePool.Spec.Release.Image),
		ObservedGeneration: nodePool.Generation,
	})

	if err := r.setPlatformConditions(ctx, hcluster, nodePool, controlPlaneNamespace, releaseImage); err != nil {
		return ctrl.Result{}, err
	}

	// Validate IgnitionEndpoint.
	if ignEndpoint == "" {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               string(hyperv1.IgnitionEndpointAvailable),
			Status:             corev1.ConditionFalse,
			Message:            "Ignition endpoint not available, waiting",
			Reason:             hyperv1.IgnitionEndpointMissingReason,
			ObservedGeneration: nodePool.Generation,
		})
		log.Info("Ignition endpoint not available, waiting")
		return ctrl.Result{}, nil
	}
	removeStatusCondition(&nodePool.Status.Conditions, string(hyperv1.IgnitionEndpointAvailable))

	// Validate Ignition CA Secret.
	caSecret := ignitionserver.IgnitionCACertSecret(controlPlaneNamespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(caSecret), caSecret); err != nil {
		if apierrors.IsNotFound(err) {
			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               string(hyperv1.IgnitionEndpointAvailable),
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.IgnitionCACertMissingReason,
				Message:            "still waiting for ignition CA cert Secret to exist",
				ObservedGeneration: nodePool.Generation,
			})
			log.Info("still waiting for ignition CA cert Secret to exist")
			return ctrl.Result{}, nil
		} else {
			return ctrl.Result{}, fmt.Errorf("failed to get ignition CA Secret: %w", err)
		}
	}
	removeStatusCondition(&nodePool.Status.Conditions, string(hyperv1.IgnitionEndpointAvailable))

	_, hasCACert := caSecret.Data[corev1.TLSCertKey]
	if !hasCACert {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               string(hyperv1.IgnitionEndpointAvailable),
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.IgnitionCACertMissingReason,
			Message:            "CA Secret is missing tls.crt key",
			ObservedGeneration: nodePool.Generation,
		})
		log.Info("CA Secret is missing tls.crt key")
		return ctrl.Result{}, nil
	}
	removeStatusCondition(&nodePool.Status.Conditions, string(hyperv1.IgnitionEndpointAvailable))

	// Validate modifying CPU arch support for platform
	if !isArchAndPlatformSupported(nodePool) {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidArchPlatform,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.NodePoolInvalidArchPlatform,
			Message:            fmt.Sprintf("CPU arch %s is not supported for platform: %s, use 'amd64' instead", nodePool.Spec.Arch, nodePool.Spec.Platform.Type),
			ObservedGeneration: nodePool.Generation,
		})
	} else {
		if err = validateHCPayloadSupportsNodePoolCPUArch(hcluster, nodePool); err != nil {
			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidArchPlatform,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.NodePoolInvalidArchPlatform,
				Message:            err.Error(),
				ObservedGeneration: nodePool.Generation,
			})
			return ctrl.Result{}, err
		}
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidArchPlatform,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			ObservedGeneration: nodePool.Generation,
		})
	}

	haproxyRawConfig, err := r.generateHAProxyRawConfig(ctx, hcluster, releaseImage)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to generate HAProxy raw config: %w", err)
	}
	configGenerator, err := NewConfigGenerator(ctx, r.Client, hcluster, nodePool, releaseImage, haproxyRawConfig)
	if err != nil {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidMachineConfigConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.NodePoolValidationFailedReason,
			Message:            err.Error(),
			ObservedGeneration: nodePool.Generation,
		})
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

	mirroredConfigs, err := BuildMirrorConfigs(ctx, configGenerator)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to build mirror configs: %w", err)
	}
	if err := r.reconcileMirroredConfigs(ctx, log, mirroredConfigs, controlPlaneNamespace, nodePool); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to mirror configs: %w", err)
	}

	SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolValidMachineConfigConditionType,
		Status:             corev1.ConditionTrue,
		Reason:             hyperv1.AsExpectedReason,
		ObservedGeneration: nodePool.Generation,
	})

	// Check if config needs to be updated.
	targetConfigHash := configGenerator.HashWithoutVersion()
	targetPayloadConfigHash := configGenerator.Hash()
	isUpdatingConfig := isUpdatingConfig(nodePool, targetConfigHash)
	if isUpdatingConfig {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdatingConfigConditionType,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			Message:            fmt.Sprintf("Updating config in progress. Target config: %s", targetConfigHash),
			ObservedGeneration: nodePool.Generation,
		})
		log.Info("NodePool config is updating",
			"current", nodePool.GetAnnotations()[nodePoolAnnotationCurrentConfig],
			"target", targetConfigHash)
	} else {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdatingConfigConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.AsExpectedReason,
			ObservedGeneration: nodePool.Generation,
		})
	}

	// Check if release image version needs to be updated.
	targetVersion := releaseImage.Version()
	isUpdatingVersion := isUpdatingVersion(nodePool, targetVersion)
	if isUpdatingVersion {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdatingVersionConditionType,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			Message:            fmt.Sprintf("Updating version in progress. Target version: %s", targetVersion),
			ObservedGeneration: nodePool.Generation,
		})
		log.Info("NodePool version is updating",
			"current", nodePool.Status.Version, "target", targetVersion)
	} else {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdatingVersionConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.AsExpectedReason,
			ObservedGeneration: nodePool.Generation,
		})
	}

	// Signal ignition payload generation
	tokenSecret := token.TokenSecret()
	condition, err := r.createValidGeneratedPayloadCondition(ctx, tokenSecret, nodePool.Generation)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error setting ValidGeneratedPayload condition: %w", err)
	}
	SetStatusCondition(&nodePool.Status.Conditions, *condition)

	oldReachedIgnitionEndpointCondition := FindStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolReachedIgnitionEndpoint)
	// when an InPlace upgrade occurs, a new token-secret is generated, but since nodes don't reboot and reignite,
	// the new token-secret wouldn't have the `hypershift.openshift.io/ignition-reached` annotation set.
	// this results in the NodePoolReachedIgnitionEndpoint condition to report False, although the ignition-server could have been already reached.
	//
	// if ignition is already reached and InPlace upgrade is used, skip recomputing the NodePoolReachedIgnitionEndpoint condition
	// to avoid resetting the condition to False because of the missing the annotation on the new generated token-secret.
	if oldReachedIgnitionEndpointCondition == nil || oldReachedIgnitionEndpointCondition.Status != corev1.ConditionTrue || nodePool.Spec.Management.UpgradeType != hyperv1.UpgradeTypeInPlace {
		reachedIgnitionEndpointCondition, err := r.createReachedIgnitionEndpointCondition(ctx, tokenSecret, nodePool.Generation)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("error setting IgnitionReached condition: %w", err)
		}

		SetStatusCondition(&nodePool.Status.Conditions, *reachedIgnitionEndpointCondition)
	}

	// Validate tuningConfig input.
	tunedConfig, performanceProfileConfig, performanceProfileConfigMapName, err := r.getTuningConfig(ctx, nodePool)
	if err != nil {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidTuningConfigConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.NodePoolValidationFailedReason,
			Message:            err.Error(),
			ObservedGeneration: nodePool.Generation,
		})
		return ctrl.Result{}, fmt.Errorf("failed to get tuningConfig: %w", err)
	}

	SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolValidTuningConfigConditionType,
		Status:             corev1.ConditionTrue,
		Reason:             hyperv1.AsExpectedReason,
		ObservedGeneration: nodePool.Generation,
	})

	// Set ReconciliationActive condition
	SetStatusCondition(&nodePool.Status.Conditions, generateReconciliationActiveCondition(nodePool.Spec.PausedUntil, nodePool.Generation))

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

	tunedConfigMap := TunedConfigMap(controlPlaneNamespace, nodePool.Name)
	if tunedConfig == "" {
		if _, err := supportutil.DeleteIfNeeded(ctx, r.Client, tunedConfigMap); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete tunedConfig ConfigMap: %w", err)
		}
	} else {
		if result, err := r.CreateOrUpdate(ctx, r.Client, tunedConfigMap, func() error {
			return reconcileTunedConfigMap(tunedConfigMap, nodePool, tunedConfig)
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile Tuned ConfigMap: %w", err)
		} else {
			log.Info("Reconciled Tuned ConfigMap", "result", result)
		}
	}

	if performanceProfileConfig == "" {
		// at this point in time, we no longer know the name of the ConfigMap in the HCP NS
		// so, we remove it by listing by a label unique to PerformanceProfile
		if err := deleteConfigByLabel(ctx, r.Client, map[string]string{PerformanceProfileConfigMapLabel: "true"}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete performanceprofileConfig ConfigMap: %w", err)
		}
		if err := r.SetPerformanceProfileConditions(ctx, log, nodePool, controlPlaneNamespace, true); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		performanceProfileConfigMap := PerformanceProfileConfigMap(controlPlaneNamespace, performanceProfileConfigMapName, nodePool.Name)
		result, err := r.CreateOrUpdate(ctx, r.Client, performanceProfileConfigMap, func() error {
			return reconcilePerformanceProfileConfigMap(performanceProfileConfigMap, nodePool, performanceProfileConfig)
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile PerformanceProfile ConfigMap: %w", err)
		}
		log.Info("Reconciled PerformanceProfile ConfigMap", "result", result)
		if err := r.SetPerformanceProfileConditions(ctx, log, nodePool, controlPlaneNamespace, false); err != nil {
			return ctrl.Result{}, err
		}
	}
	// Set AllMachinesReadyCondition.
	// Get all Machines for NodePool.
	err = r.setMachineAndNodeConditions(ctx, nodePool, hcluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	// 2. - Reconcile towards expected state of the world.
	if err := token.Reconcile(ctx); err != nil {
		return ctrl.Result{}, err
	}

	// non automated infrastructure should not have any machine level cluster-api components
	if !isAutomatedMachineManagement(nodePool) {
		nodePool.Status.Version = targetVersion
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

// setMachineAndNodeConditions sets the nodePool's AllMachinesReady and AllNodesHealthy conditions.
func (r *NodePoolReconciler) setMachineAndNodeConditions(ctx context.Context, nodePool *hyperv1.NodePool, hc *hyperv1.HostedCluster) error {
	// Get all Machines for NodePool.
	machines, err := r.getMachinesForNodePool(ctx, nodePool)
	if err != nil {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolAllMachinesReadyConditionType,
			Status:             corev1.ConditionUnknown,
			Reason:             hyperv1.NodePoolFailedToGetReason,
			Message:            err.Error(),
			ObservedGeneration: nodePool.Generation,
		})
		return fmt.Errorf("failed to get Machines: %w", err)
	}

	r.setAllMachinesReadyCondition(nodePool, machines)

	r.setAllNodesHealthyCondition(nodePool, machines)

	r.setCIDRConflictCondition(nodePool, machines, hc)

	if nodePool.Spec.Platform.Type == hyperv1.KubevirtPlatform {
		err = r.setAllMachinesLMCondition(ctx, nodePool, hc)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *NodePoolReconciler) setAllNodesHealthyCondition(nodePool *hyperv1.NodePool, machines []*capiv1.Machine) {
	status := corev1.ConditionTrue
	reason := hyperv1.AsExpectedReason
	var message string

	if len(machines) < 1 {
		status = corev1.ConditionFalse
		reason = hyperv1.NodePoolNotFoundReason
		message = "No Machines are created"
		if nodePool.Spec.Replicas != nil && *nodePool.Spec.Replicas == 0 {
			reason = hyperv1.AsExpectedReason
			message = "NodePool set to no replicas"
		}
	}

	for _, machine := range machines {
		condition := findCAPIStatusCondition(machine.Status.Conditions, capiv1.MachineNodeHealthyCondition)
		if condition != nil && condition.Status != corev1.ConditionTrue {
			status = corev1.ConditionFalse
			reason = condition.Reason
			message = message + fmt.Sprintf("Machine %s: %s\n", machine.Name, condition.Reason)
		}
	}

	if status == corev1.ConditionTrue {
		message = hyperv1.AllIsWellMessage
	}

	allMachinesHealthyCondition := &hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolAllNodesHealthyConditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: nodePool.Generation,
	}
	SetStatusCondition(&nodePool.Status.Conditions, *allMachinesHealthyCondition)
}

func (r *NodePoolReconciler) setAllMachinesReadyCondition(nodePool *hyperv1.NodePool, machines []*capiv1.Machine) {
	status := corev1.ConditionTrue
	reason := hyperv1.AsExpectedReason
	message := hyperv1.AllIsWellMessage

	if numMachines := len(machines); numMachines == 0 {
		status = corev1.ConditionFalse
		reason = hyperv1.NodePoolNotFoundReason
		message = "No Machines are created"
		if nodePool.Spec.Replicas != nil && *nodePool.Spec.Replicas == 0 {
			reason = hyperv1.AsExpectedReason
			message = "NodePool set to no replicas"
		}
	} else {
		// Aggregate conditions.
		// TODO (alberto): consider bubbling failureReason / failureMessage.
		// This a rudimentary approach which aggregates every Machine, until
		// https://github.com/kubernetes-sigs/cluster-api/pull/6218 and
		// https://github.com/kubernetes-sigs/cluster-api/pull/6025
		// are solved.
		// Eventually we should solve this in CAPI to make it available in MachineDeployments / MachineSets
		// with a consumable "Reason" and an aggregated "Message".

		numNotReady := 0
		messageMap := make(map[string][]string)

		for _, machine := range machines {
			readyCond := findCAPIStatusCondition(machine.Status.Conditions, capiv1.ReadyCondition)
			if readyCond != nil && readyCond.Status != corev1.ConditionTrue {
				status = corev1.ConditionFalse
				numNotReady++
				infraReadyCond := findCAPIStatusCondition(machine.Status.Conditions, capiv1.InfrastructureReadyCondition)
				// We append the reason as part of the higher Message, since the message is meaningless.
				// This is how a CAPI condition looks like in AWS for an instance deleted out of band failure.
				//	- lastTransitionTime: "2022-11-28T15:14:28Z"
				//		message: 1 of 2 completed
				//		reason: InstanceTerminated
				//		severity: Error
				//		status: "False"
				//		type: Ready
				var mapReason, mapMessage string
				if infraReadyCond != nil && infraReadyCond.Status != corev1.ConditionTrue && !isSetupCounterCondMessage.MatchString(infraReadyCond.Message) {
					mapReason = infraReadyCond.Reason
					mapMessage = fmt.Sprintf("Machine %s: %s: %s\n", machine.Name, infraReadyCond.Reason, infraReadyCond.Message)
				} else {
					mapReason = readyCond.Reason
					mapMessage = fmt.Sprintf("Machine %s: %s\n", machine.Name, readyCond.Reason)
				}

				messageMap[mapReason] = append(messageMap[mapReason], mapMessage)
			}
		}
		if numNotReady > 0 {
			reason, message = aggregateMachineReasonsAndMessages(messageMap, numMachines, numNotReady, aggregatorMachineStateReady)
		}
	}

	allMachinesReadyCondition := &hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolAllMachinesReadyConditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: nodePool.Generation,
	}

	SetStatusCondition(&nodePool.Status.Conditions, *allMachinesReadyCondition)
}

func (r *NodePoolReconciler) setAllMachinesLMCondition(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster) error {
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
	kubevirtMachines := &capikubevirt.KubevirtMachineList{}
	err := r.Client.List(ctx, kubevirtMachines, &client.ListOptions{
		Namespace: controlPlaneNamespace,
	})
	if err != nil {
		return fmt.Errorf("failed to list KubeVirt Machines: %w", err)
	}

	if len(kubevirtMachines.Items) == 0 {
		// not setting the condition if there are no kubevirt machines present
		return nil
	}

	numNotLiveMigratable := 0
	messageMap := make(map[string][]string)
	var mapReason, mapMessage string
	for _, kubevirtmachine := range kubevirtMachines.Items {
		for _, cond := range kubevirtmachine.Status.Conditions {
			if cond.Type == capikubevirt.VMLiveMigratableCondition && cond.Status == corev1.ConditionFalse {
				mapReason = cond.Reason
				mapMessage = fmt.Sprintf("Machine %s: %s: %s\n", kubevirtmachine.Name, cond.Reason, cond.Message)
				numNotLiveMigratable++
				messageMap[mapReason] = append(messageMap[mapReason], mapMessage)
			}
		}
	}

	if numNotLiveMigratable == 0 {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolKubeVirtLiveMigratableType,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			Message:            hyperv1.AllIsWellMessage,
			ObservedGeneration: nodePool.Generation,
		})
	} else {
		reason, message := aggregateMachineReasonsAndMessages(messageMap, len(kubevirtMachines.Items), numNotLiveMigratable, aggregatorMachineStateLiveMigratable)
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolKubeVirtLiveMigratableType,
			Status:             corev1.ConditionFalse,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: nodePool.Generation,
		})
	}
	return nil
}

func (r *NodePoolReconciler) setCIDRConflictCondition(nodePool *hyperv1.NodePool, machines []*capiv1.Machine, hc *hyperv1.HostedCluster) error {
	maxMessageLength := 256

	if len(machines) < 1 || len(hc.Spec.Networking.ClusterNetwork) < 1 {
		removeStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolClusterNetworkCIDRConflictType)
		return nil
	}

	clusterNetworkStr := hc.Spec.Networking.ClusterNetwork[0].CIDR.String()
	clusterNetwork, err := netip.ParsePrefix(clusterNetworkStr)
	if err != nil {
		return err
	}

	messages := []string{}
	for _, machine := range machines {
		for _, addr := range machine.Status.Addresses {
			if addr.Type != capiv1.MachineExternalIP && addr.Type != capiv1.MachineInternalIP {
				continue
			}
			ipaddr, err := netip.ParseAddr(addr.Address)
			if err != nil {
				return err
			}
			if clusterNetwork.Contains(ipaddr) {
				messages = append(messages, fmt.Sprintf("machine [%s] with ip [%s] collides with cluster-network cidr [%s]", machine.Name, addr.Address, clusterNetworkStr))
			}
		}
	}

	if len(messages) > 0 {
		message := ""
		for _, entry := range messages {

			if len(message) == 0 {
				message = entry
			} else if len(entry)+len(message) < maxMessageLength {
				message = message + ", " + entry
			} else {
				message = message + ", too many similar errors..."
			}
		}

		cidrConflictCondition := &hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolClusterNetworkCIDRConflictType,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.InvalidConfigurationReason,
			Message:            message,
			ObservedGeneration: nodePool.Generation,
		}
		SetStatusCondition(&nodePool.Status.Conditions, *cidrConflictCondition)
	}

	return nil
}

func (r *NodePoolReconciler) addKubeVirtCacheNameToStatus(kubevirtBootImage kubevirt.BootImage, nodePool *hyperv1.NodePool) {
	if namer, ok := kubevirtBootImage.(kubevirt.BootImageNamer); ok {
		if cacheName := namer.GetCacheName(); len(cacheName) > 0 {
			if nodePool.Status.Platform == nil {
				nodePool.Status.Platform = &hyperv1.NodePoolPlatformStatus{}
			}

			if nodePool.Status.Platform.KubeVirt == nil {
				nodePool.Status.Platform.KubeVirt = &hyperv1.KubeVirtNodePoolStatus{}
			}

			nodePool.Status.Platform.KubeVirt.CacheName = cacheName
		}
	}
}

// createReachedIgnitionEndpointCondition creates a condition for the NodePool based on the tokenSecret data.
func (r NodePoolReconciler) createReachedIgnitionEndpointCondition(ctx context.Context, tokenSecret *corev1.Secret, generation int64) (*hyperv1.NodePoolCondition, error) {
	var condition *hyperv1.NodePoolCondition
	if err := r.Get(ctx, client.ObjectKeyFromObject(tokenSecret), tokenSecret); err != nil {
		if !apierrors.IsNotFound(err) {
			condition = &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolReachedIgnitionEndpoint,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.NodePoolFailedToGetReason,
				Message:            err.Error(),
				ObservedGeneration: generation,
			}
			return nil, fmt.Errorf("failed to get token secret: %w", err)
		} else {
			condition = &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolReachedIgnitionEndpoint,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.NodePoolNotFoundReason,
				Message:            err.Error(),
				ObservedGeneration: generation,
			}
		}
		return condition, nil
	}

	if _, ok := tokenSecret.Annotations[TokenSecretIgnitionReachedAnnotation]; !ok {
		condition = &hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolReachedIgnitionEndpoint,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.IgnitionNotReached,
			Message:            "",
			ObservedGeneration: generation,
		}
		return condition, nil
	}

	condition = &hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolReachedIgnitionEndpoint,
		Status:             corev1.ConditionTrue,
		Reason:             hyperv1.AsExpectedReason,
		Message:            "",
		ObservedGeneration: generation,
	}

	return condition, nil
}

// createValidGeneratedPayloadCondition creates a condition for the NodePool based on the tokenSecret data.
func (r NodePoolReconciler) createValidGeneratedPayloadCondition(ctx context.Context, tokenSecret *corev1.Secret, generation int64) (*hyperv1.NodePoolCondition, error) {
	var condition *hyperv1.NodePoolCondition
	if err := r.Get(ctx, client.ObjectKeyFromObject(tokenSecret), tokenSecret); err != nil {
		if !apierrors.IsNotFound(err) {
			condition = &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidGeneratedPayloadConditionType,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.NodePoolFailedToGetReason,
				Message:            err.Error(),
				ObservedGeneration: generation,
			}
			return nil, fmt.Errorf("failed to get token secret: %w", err)
		} else {
			condition = &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidGeneratedPayloadConditionType,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.NodePoolNotFoundReason,
				Message:            err.Error(),
				ObservedGeneration: generation,
			}
		}
		return condition, nil
	}

	if _, ok := tokenSecret.Data[ignserver.TokenSecretReasonKey]; !ok {
		condition = &hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidGeneratedPayloadConditionType,
			Status:             corev1.ConditionUnknown,
			Reason:             string(tokenSecret.Data[ignserver.TokenSecretReasonKey]),
			Message:            "Unable to get status data from token secret",
			ObservedGeneration: generation,
		}
		return condition, nil
	}

	var status corev1.ConditionStatus
	if string(tokenSecret.Data[ignserver.TokenSecretReasonKey]) == hyperv1.AsExpectedReason {
		status = corev1.ConditionTrue
	}
	condition = &hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolValidGeneratedPayloadConditionType,
		Status:             status,
		Reason:             string(tokenSecret.Data[ignserver.TokenSecretReasonKey]),
		Message:            string(tokenSecret.Data[ignserver.TokenSecretMessageKey]),
		ObservedGeneration: generation,
	}

	return condition, nil
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
	hostedCluster, ok := hcpList.Items[0].Annotations[hostedcluster.HostedClusterAnnotation]
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
	controlPlaneOperatorImage, err := hostedcluster.GetControlPlaneOperatorImage(ctx, hostedCluster, r.ReleaseProvider, r.HypershiftOperatorImage, pullSecretBytes)
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

func globalConfigString(hcluster *hyperv1.HostedCluster) (string, error) {
	// 1. - Reconcile conditions according to current state of the world.
	proxy := globalconfig.ProxyConfig()
	globalconfig.ReconcileProxyConfigWithStatusFromHostedCluster(proxy, hcluster)

	// NOTE: The image global config is not injected via userdata or NodePool ignition config.
	// It is included directly by the ignition server.  However, we need to detect the change
	// here to trigger a nodepool update.
	image := globalconfig.ImageConfig()
	globalconfig.ReconcileImageConfigFromHostedCluster(image, hcluster)

	// Serialize proxy and image into a single string to use in the token secret hash.
	globalConfigBytes := bytes.NewBuffer(nil)
	enc := json.NewEncoder(globalConfigBytes)
	if err := enc.Encode(proxy); err != nil {
		return "", fmt.Errorf("failed to encode proxy global config: %w", err)
	}
	if err := enc.Encode(image); err != nil {
		return "", fmt.Errorf("failed to encode image global config: %w", err)
	}
	return globalConfigBytes.String(), nil
}

func deleteConfigByLabel(ctx context.Context, c client.Client, lbl map[string]string) error {
	cmList := &corev1.ConfigMapList{}
	if err := c.List(ctx, cmList, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(lbl),
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

func (r *NodePoolReconciler) setPlatformConditions(ctx context.Context, hcluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool, controlPlaneNamespace string, releaseImage *releaseinfo.ReleaseImage) error {
	switch nodePool.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		return r.setKubevirtConditions(ctx, nodePool, hcluster, controlPlaneNamespace, releaseImage)
	case hyperv1.AWSPlatform:
		return r.setAWSConditions(ctx, nodePool, hcluster, controlPlaneNamespace, releaseImage)
	case hyperv1.PowerVSPlatform:
		return r.setPowerVSconditions(ctx, nodePool, hcluster, controlPlaneNamespace, releaseImage)
	default:
		return nil
	}
}
