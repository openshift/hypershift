package nodepool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	coreerrors "errors"
	"fmt"
	"io"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/blang/semver"
	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	configv1 "github.com/openshift/api/config/v1"
	configv1alpha1 "github.com/openshift/api/config/v1alpha1"
	"github.com/openshift/api/operator/v1alpha1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	performanceprofilev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/kubevirt"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/openstack"
	ignserver "github.com/openshift/hypershift/ignition-server/controllers"
	kvinfra "github.com/openshift/hypershift/kubevirtexternalinfra"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/supportedversion"
	"github.com/openshift/hypershift/support/upsert"
	supportutil "github.com/openshift/hypershift/support/util"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	serializer "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	k8sutilspointer "k8s.io/utils/pointer"
	"k8s.io/utils/set"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capipowervs "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiopenstack "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
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
	TokenSecretTokenGenerationTime            = "hypershift.openshift.io/last-token-generation-time"
	TokenSecretReleaseKey                     = "release"
	TokenSecretTokenKey                       = "token"
	TokenSecretPullSecretHashKey              = "pull-secret-hash"
	TokenSecretHCConfigurationHashKey         = "hc-configuration-hash"
	TokenSecretConfigKey                      = "config"
	TokenSecretAnnotation                     = "hypershift.openshift.io/ignition-config"
	TokenSecretIgnitionReachedAnnotation      = "hypershift.openshift.io/ignition-reached"
	TokenSecretNodePoolUpgradeType            = "hypershift.openshift.io/node-pool-upgrade-type"

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

// MirrorConfig holds the information needed to mirror a config object to HCP namespace
type MirrorConfig struct {
	*corev1.ConfigMap
	Labels map[string]string
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
		Watches(&capiopenstack.OpenStackMachineTemplate{}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool), builder.WithPredicates(supportutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient()))).
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
	if err := validateInfraID(infraID); err != nil {
		// We don't return the error here as reconciling won't solve the input problem.
		// An update event will trigger reconciliation.
		// TODO (alberto): consider this an condition failure reason when revisiting conditions.
		log.Error(err, "Invalid infraID, waiting.")
		return ctrl.Result{}, nil
	}

	globalConfig, err := globalConfigString(hcluster)
	if err != nil {
		return ctrl.Result{}, err
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

	var kubevirtBootImage kubevirt.BootImage
	// moved KubeVirt specific handling up here, so the caching of the boot image will start as early as possible
	// in order to actually save time. Caching form the original location will take more time, because the VMs can't
	// be created before the caching is 100% done. But moving this logic here, the caching will be done in parallel
	// to the ignition settings, and so it will be ready, or almost ready, when the VMs are created.
	if nodePool.Spec.Platform.Type == hyperv1.KubevirtPlatform {
		if err := kubevirt.PlatformValidation(nodePool); err != nil {
			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidMachineConfigConditionType,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.NodePoolValidationFailedReason,
				Message:            fmt.Sprintf("validation of NodePool KubeVirt platform failed: %s", err.Error()),
				ObservedGeneration: nodePool.Generation,
			})
			return ctrl.Result{}, fmt.Errorf("validation of NodePool KubeVirt platform failed: %w", err)
		}
		removeStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolValidMachineConfigConditionType)

		infraNS := controlPlaneNamespace
		if hcluster.Spec.Platform.Kubevirt != nil &&
			hcluster.Spec.Platform.Kubevirt.Credentials != nil &&
			len(hcluster.Spec.Platform.Kubevirt.Credentials.InfraNamespace) > 0 {

			infraNS = hcluster.Spec.Platform.Kubevirt.Credentials.InfraNamespace

			if nodePool.Status.Platform == nil {
				nodePool.Status.Platform = &hyperv1.NodePoolPlatformStatus{}
			}

			if nodePool.Status.Platform.KubeVirt == nil {
				nodePool.Status.Platform.KubeVirt = &hyperv1.KubeVirtNodePoolStatus{}
			}

			nodePool.Status.Platform.KubeVirt.Credentials = hcluster.Spec.Platform.Kubevirt.Credentials.DeepCopy()
		}
		kubevirtBootImage, err = kubevirt.GetImage(nodePool, releaseImage, infraNS)
		if err != nil {
			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidPlatformImageType,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.NodePoolValidationFailedReason,
				Message:            fmt.Sprintf("Couldn't discover a KubeVirt Image for release image %q: %s", nodePool.Spec.Release.Image, err.Error()),
				ObservedGeneration: nodePool.Generation,
			})
			return ctrl.Result{}, fmt.Errorf("couldn't discover a KubeVirt Image in release payload image: %w", err)
		}

		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidPlatformImageType,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			Message:            fmt.Sprintf("Bootstrap KubeVirt Image is %s", kubevirtBootImage.String()),
			ObservedGeneration: nodePool.Generation,
		})

		uid := string(nodePool.GetUID())

		var creds *hyperv1.KubevirtPlatformCredentials

		if hcluster.Spec.Platform.Kubevirt != nil && hcluster.Spec.Platform.Kubevirt.Credentials != nil {
			creds = hcluster.Spec.Platform.Kubevirt.Credentials
		}

		kvInfraClient, err := r.KubevirtInfraClients.DiscoverKubevirtClusterClient(ctx, r.Client, uid, creds, controlPlaneNamespace, hcluster.GetNamespace())
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get KubeVirt external infra-cluster: %w", err)
		}
		err = kubevirtBootImage.CacheImage(ctx, kvInfraClient.GetInfraClient(), nodePool, uid)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create or validate KubeVirt image cache: %w", err)
		}

		r.addKubeVirtCacheNameToStatus(kubevirtBootImage, nodePool)

		// If this is a new nodepool, or we're currently updating a nodepool, then it is safe to
		// use the new topologySpreadConstraints feature over pod anti-affinity when
		// spreading out the VMs across the infra cluster
		if nodePool.Status.Version == "" || isUpdatingVersion(nodePool, releaseImage.Version()) {
			nodePool.Annotations[hyperv1.NodePoolSupportsKubevirtTopologySpreadConstraintsAnnotation] = "true"
		}
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

	caCertBytes, hasCACert := caSecret.Data[corev1.TLSCertKey]
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

	// Validate and get releaseImage.
	SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolValidReleaseImageConditionType,
		Status:             corev1.ConditionTrue,
		Reason:             hyperv1.AsExpectedReason,
		Message:            fmt.Sprintf("Using release image: %s", nodePool.Spec.Release.Image),
		ObservedGeneration: nodePool.Generation,
	})

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
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidArchPlatform,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			Message:            fmt.Sprintf("CPU arch %s is supported for platform: %s", nodePool.Spec.Arch, nodePool.Spec.Platform.Type),
			ObservedGeneration: nodePool.Generation,
		})
	}

	// Validate AWS platform specific input
	var ami string
	if nodePool.Spec.Platform.Type == hyperv1.AWSPlatform {
		if hcluster.Spec.Platform.AWS == nil {
			return ctrl.Result{}, fmt.Errorf("the HostedCluster for this NodePool has no .Spec.Platform.AWS, this is unsupported")
		}
		if nodePool.Spec.Platform.AWS.AMI != "" {
			ami = nodePool.Spec.Platform.AWS.AMI
			// User-defined AMIs cannot be validated
			removeStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolValidPlatformImageType)
		} else {
			// TODO: Should the region be included in the NodePool platform information?
			ami, err = defaultNodePoolAMI(hcluster.Spec.Platform.AWS.Region, nodePool.Spec.Arch, releaseImage)
			if err != nil {
				SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
					Type:               hyperv1.NodePoolValidPlatformImageType,
					Status:             corev1.ConditionFalse,
					Reason:             hyperv1.NodePoolValidationFailedReason,
					Message:            fmt.Sprintf("Couldn't discover an AMI for release image %q: %s", nodePool.Spec.Release.Image, err.Error()),
					ObservedGeneration: nodePool.Generation,
				})
				return ctrl.Result{}, fmt.Errorf("couldn't discover an AMI for release image: %w", err)
			}
			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidPlatformImageType,
				Status:             corev1.ConditionTrue,
				Reason:             hyperv1.AsExpectedReason,
				Message:            fmt.Sprintf("Bootstrap AMI is %q", ami),
				ObservedGeneration: nodePool.Generation,
			})
		}

		if hcluster.Status.Platform == nil || hcluster.Status.Platform.AWS == nil || hcluster.Status.Platform.AWS.DefaultWorkerSecurityGroupID == "" {
			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolAWSSecurityGroupAvailableConditionType,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.DefaultAWSSecurityGroupNotReadyReason,
				Message:            "Waiting for AWS default security group to be created for hosted cluster",
				ObservedGeneration: nodePool.Generation,
			})
		} else {
			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolAWSSecurityGroupAvailableConditionType,
				Status:             corev1.ConditionTrue,
				Reason:             hyperv1.AsExpectedReason,
				Message:            "NodePool has a default security group",
				ObservedGeneration: nodePool.Generation,
			})
		}
	}

	// Validate PowerVS platform specific input
	var coreOSPowerVSImage *releaseinfo.CoreOSPowerVSImage
	var powervsImageRegion string
	var powervsBootImage string
	if nodePool.Spec.Platform.Type == hyperv1.PowerVSPlatform {
		coreOSPowerVSImage, powervsImageRegion, err = getPowerVSImage(hcluster.Spec.Platform.PowerVS.Region, releaseImage)
		if err != nil {
			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidPlatformImageType,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.NodePoolValidationFailedReason,
				Message:            fmt.Sprintf("Couldn't discover a PowerVS Image for release image %q: %s", nodePool.Spec.Release.Image, err.Error()),
				ObservedGeneration: nodePool.Generation,
			})
			return ctrl.Result{}, fmt.Errorf("couldn't discover a PowerVS Image for release image: %w", err)
		}
		powervsBootImage = coreOSPowerVSImage.Release
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidPlatformImageType,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			Message:            fmt.Sprintf("Bootstrap PowerVS Image is %q", powervsBootImage),
			ObservedGeneration: nodePool.Generation,
		})
	}

	// Validate config input.
	// 3 generic core config resources: fips, ssh and haproxy.
	// TODO (alberto): consider moving the expectedCoreConfigResources check
	// into the token Secret controller so we don't block Machine infra creation on this.
	expectedCoreConfigResources := expectedCoreConfigResourcesForHostedCluster(hcluster)
	config, mirroredConfigs, missingConfigs, err := r.getConfig(ctx, nodePool, expectedCoreConfigResources, controlPlaneNamespace, releaseImage, hcluster)
	if err != nil {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidMachineConfigConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.NodePoolValidationFailedReason,
			Message:            err.Error(),
			ObservedGeneration: nodePool.Generation,
		})
		return ctrl.Result{}, fmt.Errorf("failed to get config: %w", err)
	}
	if missingConfigs {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidMachineConfigConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.NodePoolValidationFailedReason,
			Message:            "Core ignition config has not been created yet",
			ObservedGeneration: nodePool.Generation,
		})
		// We watch configmaps so we will get an event when these get created
		return ctrl.Result{}, nil
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

	// Retrieve pull secret name to check for changes when config is checked for updates
	pullSecretName, err := r.getPullSecretName(ctx, hcluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Check if config needs to be updated.
	targetConfigHash := supportutil.HashSimple(config + pullSecretName)
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
	targetPayloadConfigHash := payloadConfigHash(config, targetVersion, pullSecretName, globalConfig)
	tokenSecret := TokenSecret(controlPlaneNamespace, nodePool.Name, targetPayloadConfigHash)
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
	if isPaused, duration := supportutil.IsReconciliationPaused(log, nodePool.Spec.PausedUntil); isPaused {
		md := machineDeployment(nodePool, controlPlaneNamespace)
		err := pauseMachineDeployment(ctx, r.Client, md)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to pause MachineDeployment: %w", err)
		}
		ms := machineSet(nodePool, controlPlaneNamespace)
		err = pauseMachineSet(ctx, r.Client, ms)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to pause MachineSet: %w", err)
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
	compressedConfig, err := supportutil.CompressAndEncode([]byte(config))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to compress and decode config: %w", err)
	}

	cpoCapabilities, err := r.detectCPOCapabilities(ctx, hcluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to detect CPO capabilities: %w", err)
	}

	// TODO (alberto): Drop this after dropping < 4.12 support.
	// So all CPOs ign server will know to decompress and decode.
	if !cpoCapabilities.DecompressAndDecodeConfig {
		compressedConfig, err = supportutil.Compress([]byte(config))
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to compress config: %w", err)
		}
	}

	// Token Secrets exist for each NodePool config/version and follow "prefixName-configVersionHash" naming convention.
	// Ensure old configVersionHash resources are deleted, i.e. token Secret and userdata Secret.
	if isUpdatingVersion || isUpdatingConfig {
		tokenSecret := TokenSecret(controlPlaneNamespace, nodePool.Name, nodePool.GetAnnotations()[nodePoolAnnotationCurrentConfigVersion])
		err := r.Get(ctx, client.ObjectKeyFromObject(tokenSecret), tokenSecret)
		if err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to get token Secret: %w", err)
		}
		if err == nil {
			if err := setExpirationTimestampOnToken(ctx, r.Client, tokenSecret, nil); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to set expiration on token Secret: %w", err)
			}
		}

		// For AWS, we keep the old userdata Secret so old Machines during rolled out can be deleted.
		// Otherwise, deletion fails because of https://github.com/kubernetes-sigs/cluster-api-provider-aws/pull/3805.
		// TODO (Alberto): enable back deletion when the PR above gets merged.
		if nodePool.Spec.Platform.Type != hyperv1.AWSPlatform {
			userDataSecret := IgnitionUserDataSecret(controlPlaneNamespace, nodePool.GetName(), nodePool.GetAnnotations()[nodePoolAnnotationCurrentConfigVersion])
			err = r.Get(ctx, client.ObjectKeyFromObject(userDataSecret), userDataSecret)
			if err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to get user data Secret: %w", err)
			}
			if err == nil {
				if err := r.Delete(ctx, userDataSecret); err != nil && !apierrors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("failed to delete user data Secret: %w", err)
				}
			}
		}
	}

	tokenSecret = TokenSecret(controlPlaneNamespace, nodePool.Name, targetPayloadConfigHash)
	pullSecretBytes, err := r.getPullSecretBytes(ctx, hcluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get pull secret bytes: %w", err)
	}
	hcConfigurationHash, err := supportutil.HashStruct(hcluster.Spec.Configuration)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to hash HostedCluster configuration: %w", err)
	}
	if result, err := r.CreateOrUpdate(ctx, r.Client, tokenSecret, func() error {
		return reconcileTokenSecret(tokenSecret, nodePool, compressedConfig.Bytes(), pullSecretBytes, hcConfigurationHash)
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile token Secret: %w", err)
	} else {
		log.Info("Reconciled token Secret", "result", result)
	}

	tokenBytes, hasToken := tokenSecret.Data[TokenSecretTokenKey]
	if !hasToken {
		// This should never happen by design.
		return ctrl.Result{}, fmt.Errorf("token secret is missing token key")
	}

	userDataSecret := IgnitionUserDataSecret(controlPlaneNamespace, nodePool.GetName(), targetPayloadConfigHash)
	proxy := globalconfig.ProxyConfig()
	globalconfig.ReconcileProxyConfigWithStatusFromHostedCluster(proxy, hcluster)
	if result, err := r.CreateOrUpdate(ctx, r.Client, userDataSecret, func() error {
		return reconcileUserDataSecret(userDataSecret, nodePool, caCertBytes, tokenBytes, ignEndpoint, targetPayloadConfigHash, proxy)
	}); err != nil {
		return ctrl.Result{}, err
	} else {
		log.Info("Reconciled userData Secret", "result", result)
	}

	// Store new template hash.

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

	// CoreOS images in the IBM Cloud are hosted in the IBM Cloud Object Storage for PowerVS platform, these images
	// needs to be imported into the PowerVS service instance needed for the machines. IBMPowerVSImage is the spec
	// controlled by the CAPIBM to import these images and used in the machine deployments.
	if nodePool.Spec.Platform.Type == hyperv1.PowerVSPlatform {
		ibmPowerVSImage := IBMPowerVSImage(controlPlaneNamespace, coreOSPowerVSImage.Release)
		if result, err := r.CreateOrUpdate(ctx, r.Client, ibmPowerVSImage, func() error {
			return reconcileIBMPowerVSImage(ibmPowerVSImage, hcluster, nodePool, infraID, powervsImageRegion, coreOSPowerVSImage)
		}); err != nil {
			return ctrl.Result{}, err
		} else {
			log.Info("Reconciled IBMPowerVSImage", "result", result)
		}
	}

	if err := r.cleanupMachineTemplates(ctx, log, nodePool, controlPlaneNamespace); err != nil {
		return ctrl.Result{}, err
	}

	// Reconcile (Platform)MachineTemplate.
	template, mutateTemplate, machineTemplateSpecJSON, err := machineTemplateBuilders(hcluster, nodePool, infraID, ami, powervsBootImage, kubevirtBootImage, cpoCapabilities.CreateDefaultAWSSecurityGroup)
	if err != nil {
		if _, isNotReady := err.(*NotReadyError); isNotReady {
			log.Info("Waiting to create machine template", "message", err.Error())
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}
	if result, err := r.CreateOrUpdate(ctx, r.Client, template, func() error {
		return mutateTemplate(template)
	}); err != nil {
		return ctrl.Result{}, err
	} else {
		log.Info("Reconciled Machine template", "result", result)
	}

	// Check if platform machine template needs to be updated.
	targetMachineTemplate := template.GetName()
	if isUpdatingMachineTemplate(nodePool, targetMachineTemplate) {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			Message:            fmt.Sprintf("platform machine template update in progress. Target template: %s", targetMachineTemplate),
			ObservedGeneration: nodePool.Generation,
		})
		log.Info("NodePool machine template is updating",
			"current", nodePool.GetAnnotations()[nodePoolAnnotationPlatformMachineTemplate],
			"target", targetMachineTemplate)
	} else {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.AsExpectedReason,
			ObservedGeneration: nodePool.Generation,
		})
	}

	if nodePool.Spec.Management.UpgradeType == hyperv1.UpgradeTypeInPlace {
		ms := machineSet(nodePool, controlPlaneNamespace)
		if result, err := controllerutil.CreateOrPatch(ctx, r.Client, ms, func() error {
			return r.reconcileMachineSet(
				ctx,
				ms, nodePool,
				userDataSecret,
				template,
				infraID,
				targetVersion, targetConfigHash, targetPayloadConfigHash, machineTemplateSpecJSON)
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile MachineSet %q: %w",
				client.ObjectKeyFromObject(ms).String(), err)
		} else {
			log.Info("Reconciled MachineSet", "result", result)
		}
	}

	if nodePool.Spec.Management.UpgradeType == hyperv1.UpgradeTypeReplace {
		md := machineDeployment(nodePool, controlPlaneNamespace)
		if result, err := controllerutil.CreateOrPatch(ctx, r.Client, md, func() error {
			return r.reconcileMachineDeployment(
				log,
				md, nodePool,
				userDataSecret,
				template,
				infraID,
				targetVersion, targetConfigHash, targetPayloadConfigHash, machineTemplateSpecJSON)
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile MachineDeployment %q: %w",
				client.ObjectKeyFromObject(md).String(), err)
		} else {
			log.Info("Reconciled MachineDeployment", "result", result)
		}
	}

	mhc := machineHealthCheck(nodePool, controlPlaneNamespace)
	if nodePool.Spec.Management.AutoRepair {
		if c := FindStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolReachedIgnitionEndpoint); c == nil || c.Status != corev1.ConditionTrue {
			log.Info("ReachedIgnitionEndpoint is false, MachineHealthCheck won't be created until this is true")
			return ctrl.Result{}, nil
		}

		if result, err := ctrl.CreateOrUpdate(ctx, r.Client, mhc, func() error {
			return r.reconcileMachineHealthCheck(ctx, mhc, nodePool, hcluster, infraID)
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile MachineHealthCheck %q: %w",
				client.ObjectKeyFromObject(mhc).String(), err)
		} else {
			log.Info("Reconciled MachineHealthCheck", "result", result)
		}
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolAutorepairEnabledConditionType,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			ObservedGeneration: nodePool.Generation,
		})
	} else {
		err := r.Get(ctx, client.ObjectKeyFromObject(mhc), mhc)
		if err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		if err == nil {
			if err := r.Delete(ctx, mhc); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
		}
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolAutorepairEnabledConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.AsExpectedReason,
			ObservedGeneration: nodePool.Generation,
		})
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

func expectedCoreConfigResourcesForHostedCluster(hcluster *hyperv1.HostedCluster) int {
	expectedCoreConfigResources := 3
	if len(hcluster.Spec.ImageContentSources) > 0 {
		// additional core config resource created when image content source specified.
		expectedCoreConfigResources += 1
	}
	return expectedCoreConfigResources
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

func (r *NodePoolReconciler) cleanupMachineTemplates(ctx context.Context, log logr.Logger, nodePool *hyperv1.NodePool, controlPlaneNamespace string) error {
	// list machineSets
	machineSets := &capiv1.MachineSetList{}
	if err := r.Client.List(ctx, machineSets, client.InNamespace(controlPlaneNamespace)); err != nil {
		return fmt.Errorf("failed to list machineSets: %w", err)
	}

	// filter machineSets owned by this nodePool.
	nodePoolKey := client.ObjectKeyFromObject(nodePool).String()
	filtered := make([]*capiv1.MachineSet, 0, len(machineSets.Items))
	for idx := range machineSets.Items {
		ms := &machineSets.Items[idx]
		// skip if machineSet doesn't belong to the nodePool
		if ms.Annotations[nodePoolAnnotation] != nodePoolKey {
			continue
		}

		filtered = append(filtered, ms)
	}

	if len(filtered) == 0 {
		// initial machineSet has not been created.
		log.Info("initial machineSet has not been created.")
		return nil
	}

	ref := filtered[0].Spec.Template.Spec.InfrastructureRef
	machineTemplates := new(unstructured.UnstructuredList)
	machineTemplates.SetAPIVersion(ref.APIVersion)
	machineTemplates.SetKind(ref.Kind)
	if err := r.Client.List(ctx, machineTemplates, client.InNamespace(ref.Namespace)); err != nil {
		return fmt.Errorf("failed to list MachineTemplates: %w", err)
	}

	// delete old machine templates not currently referenced by any machineSet.
	for _, mt := range machineTemplates.Items {
		// skip if MachineTempalte doesn't belong to the nodePool.
		if mt.GetAnnotations()[nodePoolAnnotation] != nodePoolKey {
			continue
		}

		shouldDelete := true
		for _, ms := range filtered {
			if mt.GetName() == ms.Spec.Template.Spec.InfrastructureRef.Name {
				shouldDelete = false
				break
			}
		}

		if shouldDelete {
			log.Info("deleting machineTemplate", "name", mt.GetName())
			if err := r.Client.Delete(ctx, &mt); err != nil {
				return fmt.Errorf("failed to delete MachineTemplate %s: %w", mt.GetName(), err)
			}
		}
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

func deleteMachineDeployment(ctx context.Context, c client.Client, md *capiv1.MachineDeployment) error {
	err := c.Get(ctx, client.ObjectKeyFromObject(md), md)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error getting MachineDeployment: %w", err)
	}
	if md.DeletionTimestamp != nil {
		return nil
	}
	err = c.Delete(ctx, md)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error deleting MachineDeployment: %w", err)
	}
	return nil
}

func pauseMachineDeployment(ctx context.Context, c client.Client, md *capiv1.MachineDeployment) error {
	err := c.Get(ctx, client.ObjectKeyFromObject(md), md)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error getting MachineDeployment: %w", err)
	}
	if md.Annotations == nil {
		md.Annotations = make(map[string]string)
	}
	// FIXME: In future we may want to use the spec field instead
	// https://github.com/kubernetes-sigs/cluster-api/issues/6966
	md.Annotations[capiv1.PausedAnnotation] = "true"
	return c.Update(ctx, md)
}

func deleteMachineSet(ctx context.Context, c client.Client, ms *capiv1.MachineSet) error {
	err := c.Get(ctx, client.ObjectKeyFromObject(ms), ms)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error getting MachineSet: %w", err)
	}
	if ms.DeletionTimestamp != nil {
		return nil
	}
	err = c.Delete(ctx, ms)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error deleting MachineSet: %w", err)
	}
	return nil
}

func pauseMachineSet(ctx context.Context, c client.Client, ms *capiv1.MachineSet) error {
	err := c.Get(ctx, client.ObjectKeyFromObject(ms), ms)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error getting MachineSet: %w", err)
	}
	if ms.Annotations == nil {
		ms.Annotations = make(map[string]string)
	}
	// FIXME: In future we may want to use the spec field instead
	// https://github.com/kubernetes-sigs/cluster-api/issues/6966
	// TODO: Also for paused to be complete we will need to pause all MHC if autorepair
	// is enabled and remove the autoscaling labels from the MachineDeployment / Machineset
	ms.Annotations[capiv1.PausedAnnotation] = "true"
	return c.Update(ctx, ms)
}

func deleteMachineHealthCheck(ctx context.Context, c client.Client, mhc *capiv1.MachineHealthCheck) error {
	err := c.Get(ctx, client.ObjectKeyFromObject(mhc), mhc)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error getting MachineHealthCheck: %w", err)
	}
	if mhc.DeletionTimestamp != nil {
		return nil
	}
	err = c.Delete(ctx, mhc)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error deleting MachineHealthCheck: %w", err)
	}
	return nil
}

func (r *NodePoolReconciler) delete(ctx context.Context, nodePool *hyperv1.NodePool, controlPlaneNamespace string) error {
	md := machineDeployment(nodePool, controlPlaneNamespace)
	ms := machineSet(nodePool, controlPlaneNamespace)
	mhc := machineHealthCheck(nodePool, controlPlaneNamespace)
	machineTemplates, err := r.listMachineTemplates(nodePool)
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

func reconcileUserDataSecret(userDataSecret *corev1.Secret, nodePool *hyperv1.NodePool, CA, token []byte, ignEndpoint, targetConfigVersionHash string, proxy *configv1.Proxy) error {
	// The token secret controller deletes expired token Secrets.
	// When that happens the NodePool controller reconciles and create a new one.
	// Then it reconciles the userData Secret with the new generated token.
	// Therefore, this secret is mutable.
	userDataSecret.Immutable = k8sutilspointer.Bool(false)

	if userDataSecret.Annotations == nil {
		userDataSecret.Annotations = make(map[string]string)
	}
	userDataSecret.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()

	encodedCACert := base64.StdEncoding.EncodeToString(CA)
	encodedToken := base64.StdEncoding.EncodeToString(token)
	ignConfig := ignConfig(encodedCACert, encodedToken, ignEndpoint, targetConfigVersionHash, proxy, nodePool)
	userDataValue, err := json.Marshal(ignConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal ignition config: %w", err)
	}
	userDataSecret.Data = map[string][]byte{
		"disableTemplating": []byte(base64.StdEncoding.EncodeToString([]byte("true"))),
		"value":             userDataValue,
	}
	return nil
}

func reconcileNodeTuningConfigMap(tuningConfigMap *corev1.ConfigMap, nodePool *hyperv1.NodePool, rawConfig string) error {
	tuningConfigMap.Immutable = k8sutilspointer.Bool(false)
	if tuningConfigMap.Annotations == nil {
		tuningConfigMap.Annotations = make(map[string]string)
	}
	if tuningConfigMap.Labels == nil {
		tuningConfigMap.Labels = make(map[string]string)
	}

	tuningConfigMap.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
	tuningConfigMap.Labels[nodePoolAnnotation] = nodePool.GetName()

	if tuningConfigMap.Data == nil {
		tuningConfigMap.Data = map[string]string{}
	}
	tuningConfigMap.Data[tuningConfigKey] = rawConfig

	return nil
}

// reconcileTunedConfigMap inserts the Tuned object manifest in tunedConfig into ConfigMap tunedConfigMap.
// This is used to mirror the Tuned object manifest into the control plane namespace, for the Node
// Tuning Operator to mirror and reconcile in the hosted cluster.
func reconcileTunedConfigMap(tunedConfigMap *corev1.ConfigMap, nodePool *hyperv1.NodePool, tunedConfig string) error {
	if err := reconcileNodeTuningConfigMap(tunedConfigMap, nodePool, tunedConfig); err != nil {
		return err
	}
	tunedConfigMap.Labels[tunedConfigMapLabel] = "true"
	return nil
}

// reconcilePerformanceProfileConfigMap inserts the PerformanceProfile object manifest in performanceProfileConfig into ConfigMap performanceProfileConfigMap.
// This is used to mirror the PerformanceProfile object manifest into the control plane namespace, for the Node
// Tuning Operator to mirror and reconcile in the hosted cluster.
func reconcilePerformanceProfileConfigMap(performanceProfileConfigMap *corev1.ConfigMap, nodePool *hyperv1.NodePool, performanceProfileConfig string) error {
	if err := reconcileNodeTuningConfigMap(performanceProfileConfigMap, nodePool, performanceProfileConfig); err != nil {
		return err
	}
	performanceProfileConfigMap.Labels[PerformanceProfileConfigMapLabel] = "true"
	return nil
}

func mutateMirroredConfig(cm *corev1.ConfigMap, mirroredConfig *MirrorConfig, nodePool *hyperv1.NodePool) error {
	cm.Immutable = k8sutilspointer.Bool(true)
	if cm.Annotations == nil {
		cm.Annotations = make(map[string]string)
	}
	if cm.Labels == nil {
		cm.Labels = make(map[string]string)
	}
	cm.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
	cm.Labels[nodePoolAnnotation] = nodePool.GetName()
	cm.Labels[mirroredConfigLabel] = ""
	cm.Labels = labels.Merge(cm.Labels, mirroredConfig.Labels)
	cm.Data = mirroredConfig.Data
	return nil
}

func reconcileTokenSecret(tokenSecret *corev1.Secret, nodePool *hyperv1.NodePool, compressedConfig []byte, pullSecret []byte, hcConfigurationHash string) error {
	// The token secret controller updates expired token IDs for token Secrets.
	// When that happens the NodePool controller reconciles the userData Secret with the new token ID.
	// Therefore, this secret is mutable.
	tokenSecret.Immutable = k8sutilspointer.Bool(false)
	if tokenSecret.Annotations == nil {
		tokenSecret.Annotations = make(map[string]string)
	}

	tokenSecret.Annotations[TokenSecretAnnotation] = "true"
	tokenSecret.Annotations[TokenSecretNodePoolUpgradeType] = string(nodePool.Spec.Management.UpgradeType)
	tokenSecret.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
	// active token should never be marked as expired.
	delete(tokenSecret.Annotations, hyperv1.IgnitionServerTokenExpirationTimestampAnnotation)

	if tokenSecret.Data == nil {
		tokenSecret.Data = map[string][]byte{}
		tokenSecret.Annotations[TokenSecretTokenGenerationTime] = time.Now().Format(time.RFC3339Nano)
		tokenSecret.Data[TokenSecretTokenKey] = []byte(uuid.New().String())
		tokenSecret.Data[TokenSecretReleaseKey] = []byte(nodePool.Spec.Release.Image)
		tokenSecret.Data[TokenSecretConfigKey] = compressedConfig
		tokenSecret.Data[TokenSecretPullSecretHashKey] = []byte(supportutil.HashSimple(pullSecret))
		tokenSecret.Data[TokenSecretHCConfigurationHashKey] = []byte(hcConfigurationHash)
	}
	// TODO (alberto): Only apply this on creation and change the hash generation to only use triggering upgrade fields.
	// We let this change to happen inplace now as the tokenSecret and the mcs config use the whole spec.Config for the comparing hash.
	// Otherwise if something which does not trigger a new token generation from spec.Config changes, like .IDP, both hashes would missmatch forever.
	tokenSecret.Data[TokenSecretHCConfigurationHashKey] = []byte(hcConfigurationHash)

	return nil
}

func (r *NodePoolReconciler) reconcileMachineDeployment(log logr.Logger,
	machineDeployment *capiv1.MachineDeployment,
	nodePool *hyperv1.NodePool,
	userDataSecret *corev1.Secret,
	machineTemplateCR client.Object,
	CAPIClusterName string,
	targetVersion,
	targetConfigHash, targetConfigVersionHash, machineTemplateSpecJSON string) error {

	// Set annotations and labels
	if machineDeployment.GetAnnotations() == nil {
		machineDeployment.Annotations = map[string]string{}
	}
	machineDeployment.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
	// Delete any paused annotation
	delete(machineDeployment.Annotations, capiv1.PausedAnnotation)
	if machineDeployment.GetLabels() == nil {
		machineDeployment.Labels = map[string]string{}
	}
	machineDeployment.Labels[capiv1.ClusterNameLabel] = CAPIClusterName

	resourcesName := generateName(CAPIClusterName, nodePool.Spec.ClusterName, nodePool.GetName())
	machineDeployment.Spec.MinReadySeconds = k8sutilspointer.Int32(int32(0))

	gvk, err := apiutil.GVKForObject(machineTemplateCR, api.Scheme)
	if err != nil {
		return err
	}

	// Set defaults. These are normally set by the CAPI machinedeployment webhook.
	// However, since we don't run the webhook, CAPI updates the machinedeployment
	// after it has been created with defaults.
	machineDeployment.Spec.MinReadySeconds = k8sutilspointer.Int32(0)
	machineDeployment.Spec.RevisionHistoryLimit = k8sutilspointer.Int32(1)
	machineDeployment.Spec.ProgressDeadlineSeconds = k8sutilspointer.Int32(600)

	// Set selector and template
	machineDeployment.Spec.ClusterName = CAPIClusterName
	if machineDeployment.Spec.Selector.MatchLabels == nil {
		machineDeployment.Spec.Selector.MatchLabels = map[string]string{}
	}
	machineDeployment.Spec.Selector.MatchLabels[resourcesName] = resourcesName
	machineDeployment.Spec.Selector.MatchLabels[capiv1.ClusterNameLabel] = CAPIClusterName
	machineDeployment.Spec.Template = capiv1.MachineTemplateSpec{
		ObjectMeta: capiv1.ObjectMeta{
			Labels: map[string]string{
				resourcesName:           resourcesName,
				capiv1.ClusterNameLabel: CAPIClusterName,
			},
			// Annotations here propagate down to Machines
			// https://cluster-api.sigs.k8s.io/developer/architecture/controllers/metadata-propagation.html#machinedeployment.
			Annotations: map[string]string{
				nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
			},
		},
		Spec: capiv1.MachineSpec{
			ClusterName: CAPIClusterName,
			Bootstrap: capiv1.Bootstrap{
				// Keep current user data for later check.
				DataSecretName: machineDeployment.Spec.Template.Spec.Bootstrap.DataSecretName,
			},
			InfrastructureRef: corev1.ObjectReference{
				Kind:       gvk.Kind,
				APIVersion: gvk.GroupVersion().String(),
				Namespace:  machineTemplateCR.GetNamespace(),
				// keep current tempalte name for later check.
				Name: machineDeployment.Spec.Template.Spec.InfrastructureRef.Name,
			},
			// Keep current version for later check.
			Version:                 machineDeployment.Spec.Template.Spec.Version,
			NodeDrainTimeout:        nodePool.Spec.NodeDrainTimeout,
			NodeVolumeDetachTimeout: nodePool.Spec.NodeVolumeDetachTimeout,
		},
	}

	// After a MachineDeployment is created we propagate label/taints directly into Machines.
	// This is to avoid a NodePool label/taints to trigger a rolling upgrade.
	// TODO(Alberto): drop this an rely on core in-place propagation once CAPI 1.4.0 https://github.com/kubernetes-sigs/cluster-api/releases comes through the payload.
	// https://issues.redhat.com/browse/HOSTEDCP-971
	machineList := &capiv1.MachineList{}
	if err := r.List(context.TODO(), machineList, client.InNamespace(machineDeployment.Namespace)); err != nil {
		return err
	}

	for _, machine := range machineList.Items {
		if nodePoolName := machine.GetAnnotations()[nodePoolAnnotation]; nodePoolName != client.ObjectKeyFromObject(nodePool).String() {
			continue
		}

		if machine.Annotations == nil {
			machine.Annotations = make(map[string]string)
		}
		if machine.Labels == nil {
			machine.Labels = make(map[string]string)
		}

		if result, err := controllerutil.CreateOrPatch(context.TODO(), r.Client, &machine, func() error {
			// Propagate labels.
			for k, v := range nodePool.Spec.NodeLabels {
				// Propagated managed labels down to Machines with a known hardcoded prefix
				// so the CPO HCCO Node controller can recognize them and apply them to Nodes.
				labelKey := fmt.Sprintf("%s.%s", labelManagedPrefix, k)
				machine.Labels[labelKey] = v
			}

			// Propagate taints.
			taintsInJSON, err := taintsToJSON(nodePool.Spec.Taints)
			if err != nil {
				return err
			}

			machine.Annotations[nodePoolAnnotationTaints] = taintsInJSON
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile Machine %q: %w",
				client.ObjectKeyFromObject(&machine).String(), err)
		} else {
			log.Info("Reconciled Machine", "result", result)
		}
	}

	// Set strategy
	machineDeployment.Spec.Strategy = &capiv1.MachineDeploymentStrategy{}
	machineDeployment.Spec.Strategy.Type = capiv1.MachineDeploymentStrategyType(nodePool.Spec.Management.Replace.Strategy)
	if nodePool.Spec.Management.Replace.RollingUpdate != nil {
		machineDeployment.Spec.Strategy.RollingUpdate = &capiv1.MachineRollingUpdateDeployment{
			MaxUnavailable: nodePool.Spec.Management.Replace.RollingUpdate.MaxUnavailable,
			MaxSurge:       nodePool.Spec.Management.Replace.RollingUpdate.MaxSurge,
		}
	}

	setMachineDeploymentReplicas(nodePool, machineDeployment)

	isUpdating := false
	// Propagate version and userData Secret to the machineDeployment.
	if userDataSecret.Name != k8sutilspointer.StringDeref(machineDeployment.Spec.Template.Spec.Bootstrap.DataSecretName, "") {
		log.Info("New user data Secret has been generated",
			"current", machineDeployment.Spec.Template.Spec.Bootstrap.DataSecretName,
			"target", userDataSecret.Name)

		if targetVersion != k8sutilspointer.StringDeref(machineDeployment.Spec.Template.Spec.Version, "") {
			log.Info("Starting version update: Propagating new version to the MachineDeployment",
				"releaseImage", nodePool.Spec.Release.Image, "target", targetVersion)
		}

		if targetConfigHash != nodePool.Annotations[nodePoolAnnotationCurrentConfig] {
			log.Info("Starting config update: Propagating new config to the MachineDeployment",
				"current", nodePool.Annotations[nodePoolAnnotationCurrentConfig], "target", targetConfigHash)
		}
		machineDeployment.Spec.Template.Spec.Version = &targetVersion
		machineDeployment.Spec.Template.Spec.Bootstrap.DataSecretName = k8sutilspointer.String(userDataSecret.Name)
		isUpdating = true
	}

	// template spec has changed, signal a rolling upgrade.
	if machineTemplateCR.GetName() != machineDeployment.Spec.Template.Spec.InfrastructureRef.Name {
		log.Info("New machine template has been generated",
			"current", machineDeployment.Spec.Template.Spec.InfrastructureRef.Name,
			"target", machineTemplateCR.GetName())

		machineDeployment.Spec.Template.Spec.InfrastructureRef.Name = machineTemplateCR.GetName()
		isUpdating = true
	}

	if isUpdating {
		// We return early here during a version/config/MachineTemplate update to persist the resource with new user data Secret / MachineTemplate,
		// so in the next reconciling loop we get a new MachineDeployment.Generation
		// and we can do a legit MachineDeploymentComplete/MachineDeployment.Status.ObservedGeneration check.
		return nil
	}

	// If the MachineDeployment is now processing we know
	// is at the expected version (spec.version) and config (userData Secret) so we reconcile status and annotation.
	if MachineDeploymentComplete(machineDeployment) {
		if nodePool.Status.Version != targetVersion {
			log.Info("Version update complete",
				"previous", nodePool.Status.Version, "new", targetVersion)
			nodePool.Status.Version = targetVersion
		}

		if nodePool.Annotations == nil {
			nodePool.Annotations = make(map[string]string)
		}
		if nodePool.Annotations[nodePoolAnnotationCurrentConfig] != targetConfigHash {
			log.Info("Config update complete",
				"previous", nodePool.Annotations[nodePoolAnnotationCurrentConfig], "new", targetConfigHash)
			nodePool.Annotations[nodePoolAnnotationCurrentConfig] = targetConfigHash
		}
		nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion] = targetConfigVersionHash

		if nodePool.Annotations[nodePoolAnnotationPlatformMachineTemplate] != machineTemplateCR.GetName() {
			log.Info("Rolling upgrade complete",
				"previous", nodePool.Annotations[nodePoolAnnotationPlatformMachineTemplate], "new", machineTemplateCR.GetName())
			nodePool.Annotations[nodePoolAnnotationPlatformMachineTemplate] = machineTemplateCR.GetName()
		}
	}

	// Bubble up AvailableReplicas and Ready condition from MachineDeployment.
	nodePool.Status.Replicas = machineDeployment.Status.AvailableReplicas
	for _, c := range machineDeployment.Status.Conditions {
		// This condition should aggregate and summarise readiness from underlying MachineSets and Machines
		// https://github.com/kubernetes-sigs/cluster-api/issues/3486.
		if c.Type == capiv1.ReadyCondition {
			// this is so api server does not complain
			// invalid value: \"\": status.conditions.reason in body should be at least 1 chars long"
			reason := hyperv1.AsExpectedReason
			if c.Reason != "" {
				reason = c.Reason
			}

			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolReadyConditionType,
				Status:             c.Status,
				ObservedGeneration: nodePool.Generation,
				Message:            c.Message,
				Reason:             reason,
			})
			break
		}
	}

	return nil
}

func taintsToJSON(taints []hyperv1.Taint) (string, error) {
	taintsInJSON, err := json.Marshal(taints)
	if err != nil {
		return "", err
	}

	return string(taintsInJSON), nil
}

func (r *NodePoolReconciler) reconcileMachineHealthCheck(ctx context.Context,
	mhc *capiv1.MachineHealthCheck,
	nodePool *hyperv1.NodePool,
	hc *hyperv1.HostedCluster,
	CAPIClusterName string) error {

	log := ctrl.LoggerFrom(ctx)

	// Opinionated spec based on
	// https://github.com/openshift/managed-cluster-config/blob/14d4255ec75dc263ffd3d897dfccc725cb2b7072/deploy/osd-machine-api/011-machine-api.srep-worker-healthcheck.MachineHealthCheck.yaml
	// TODO (alberto): possibly expose this config at the nodePool API.
	maxUnhealthy := intstr.FromInt(2)
	var timeOut time.Duration

	switch nodePool.Spec.Platform.Type {
	case hyperv1.AgentPlatform, hyperv1.NonePlatform:
		timeOut = 16 * time.Minute
	default:
		timeOut = 8 * time.Minute
	}

	maxUnhealthyOverride := nodePool.Annotations[hyperv1.MachineHealthCheckMaxUnhealthyAnnotation]
	if maxUnhealthyOverride == "" {
		maxUnhealthyOverride = hc.Annotations[hyperv1.MachineHealthCheckMaxUnhealthyAnnotation]
	}
	if maxUnhealthyOverride != "" {
		maxUnhealthyValue := intstr.Parse(maxUnhealthyOverride)
		// validate that this is a valid value by getting a scaled value
		if _, err := intstr.GetScaledValueFromIntOrPercent(&maxUnhealthyValue, 100, true); err != nil {
			log.Error(err, "Cannot parse max unhealthy override duration", "value", maxUnhealthyOverride)
		} else {
			maxUnhealthy = maxUnhealthyValue
		}
	}

	timeOutOverride := nodePool.Annotations[hyperv1.MachineHealthCheckTimeoutAnnotation]
	if timeOutOverride == "" {
		timeOutOverride = hc.Annotations[hyperv1.MachineHealthCheckTimeoutAnnotation]
	}
	if timeOutOverride != "" {
		timeOutOverrideTime, err := time.ParseDuration(timeOutOverride)
		if err != nil {
			log.Error(err, "Cannot parse timeout override duration", "value", timeOutOverride)
		} else {
			timeOut = timeOutOverrideTime
		}
	}

	resourcesName := generateName(CAPIClusterName, nodePool.Spec.ClusterName, nodePool.GetName())
	mhc.Spec = capiv1.MachineHealthCheckSpec{
		ClusterName: CAPIClusterName,
		Selector: metav1.LabelSelector{
			MatchLabels: map[string]string{
				resourcesName: resourcesName,
			},
		},
		UnhealthyConditions: []capiv1.UnhealthyCondition{
			{
				Type:   corev1.NodeReady,
				Status: corev1.ConditionFalse,
				Timeout: metav1.Duration{
					Duration: timeOut,
				},
			},
			{
				Type:   corev1.NodeReady,
				Status: corev1.ConditionUnknown,
				Timeout: metav1.Duration{
					Duration: timeOut,
				},
			},
		},
		MaxUnhealthy: &maxUnhealthy,
		NodeStartupTimeout: &metav1.Duration{
			Duration: 20 * time.Minute,
		},
	}
	return nil
}

// setMachineDeploymentReplicas sets wanted replicas:
// If autoscaling is enabled we reconcile min/max annotations and leave replicas untouched.
func setMachineDeploymentReplicas(nodePool *hyperv1.NodePool, machineDeployment *capiv1.MachineDeployment) {
	if machineDeployment.Annotations == nil {
		machineDeployment.Annotations = make(map[string]string)
	}

	if isAutoscalingEnabled(nodePool) {
		// The MachineDeployment replicas field should default to a value inside the (min size, max size) range based on the autoscaler annotations
		// so the autoscaler can take control of the replicas field.
		//
		// 1. if it’s a new MachineDeployment, or the replicas field of the old MachineDeployment is < min size, use min size
		// 2. if the replicas field of the old MachineDeployment is > max size, use max size
		mdReplicas := k8sutilspointer.Int32Deref(machineDeployment.Spec.Replicas, 0)
		if mdReplicas < nodePool.Spec.AutoScaling.Min {
			machineDeployment.Spec.Replicas = &nodePool.Spec.AutoScaling.Min
		} else if mdReplicas > nodePool.Spec.AutoScaling.Max {
			machineDeployment.Spec.Replicas = &nodePool.Spec.AutoScaling.Max
		}

		machineDeployment.Annotations[autoscalerMaxAnnotation] = strconv.Itoa(int(nodePool.Spec.AutoScaling.Max))
		machineDeployment.Annotations[autoscalerMinAnnotation] = strconv.Itoa(int(nodePool.Spec.AutoScaling.Min))
	}

	// If autoscaling is NOT enabled we reset min/max annotations and reconcile replicas.
	if !isAutoscalingEnabled(nodePool) {
		machineDeployment.Annotations[autoscalerMaxAnnotation] = "0"
		machineDeployment.Annotations[autoscalerMinAnnotation] = "0"
		machineDeployment.Spec.Replicas = k8sutilspointer.Int32(k8sutilspointer.Int32Deref(nodePool.Spec.Replicas, 0))
	}
}

func ignConfig(encodedCACert, encodedToken, endpoint, targetConfigVersionHash string, proxy *configv1.Proxy, nodePool *hyperv1.NodePool) ignitionapi.Config {
	cfg := ignitionapi.Config{
		Ignition: ignitionapi.Ignition{
			Version: "3.2.0",
			Security: ignitionapi.Security{
				TLS: ignitionapi.TLS{
					CertificateAuthorities: []ignitionapi.Resource{
						{
							Source: k8sutilspointer.String(fmt.Sprintf("data:text/plain;base64,%s", encodedCACert)),
						},
					},
				},
			},
			Config: ignitionapi.IgnitionConfig{
				Merge: []ignitionapi.Resource{
					{
						Source: k8sutilspointer.String(fmt.Sprintf("https://%s/ignition", endpoint)),
						HTTPHeaders: []ignitionapi.HTTPHeader{
							{
								Name:  "Authorization",
								Value: k8sutilspointer.String(fmt.Sprintf("Bearer %s", encodedToken)),
							},
							{
								Name:  "NodePool",
								Value: k8sutilspointer.String(client.ObjectKeyFromObject(nodePool).String()),
							},
							{
								Name:  "TargetConfigVersionHash",
								Value: k8sutilspointer.String(targetConfigVersionHash),
							},
						},
					},
				},
			},
		},
	}
	if proxy.Status.HTTPProxy != "" {
		cfg.Ignition.Proxy.HTTPProxy = k8sutilspointer.String(proxy.Status.HTTPProxy)
	}
	if proxy.Status.HTTPSProxy != "" {
		cfg.Ignition.Proxy.HTTPSProxy = k8sutilspointer.String(proxy.Status.HTTPSProxy)
	}
	if proxy.Status.NoProxy != "" {
		for _, item := range strings.Split(proxy.Status.NoProxy, ",") {
			cfg.Ignition.Proxy.NoProxy = append(cfg.Ignition.Proxy.NoProxy, ignitionapi.NoProxyItem(item))
		}
	}
	return cfg
}

func (r *NodePoolReconciler) getConfig(ctx context.Context,
	nodePool *hyperv1.NodePool,
	expectedCoreConfigResources int,
	controlPlaneResource string,
	releaseImage *releaseinfo.ReleaseImage,
	hcluster *hyperv1.HostedCluster,
) (configsRaw string, mirroredConfigs []*MirrorConfig, missingConfigs bool, err error) {
	var configs []corev1.ConfigMap
	var allConfigPlainText []string
	var errors []error

	isHAProxyIgnitionConfigManaged, cpoImage, err := r.isHAProxyIgnitionConfigManaged(ctx, hcluster)
	if err != nil {
		return "", nil, false, fmt.Errorf("failed to check if we manage haproxy ignition config: %w", err)
	}
	if isHAProxyIgnitionConfigManaged {
		oldHAProxyIgnitionConfig := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneResource, Name: "ignition-config-apiserver-haproxy"},
		}
		err := r.Client.Get(ctx, client.ObjectKeyFromObject(oldHAProxyIgnitionConfig), oldHAProxyIgnitionConfig)
		if err != nil && !apierrors.IsNotFound(err) {
			return "", nil, false, fmt.Errorf("failed to get CPO-managed haproxy ignition config: %w", err)
		}
		if err == nil {
			if err := r.Client.Delete(ctx, oldHAProxyIgnitionConfig); err != nil && !apierrors.IsNotFound(err) {
				return "", nil, false, fmt.Errorf("failed to delete the CPO-managed haproxy ignition config: %w", err)
			}
		}
		expectedCoreConfigResources--

		haproxyIgnitionConfig, missing, err := r.reconcileHAProxyIgnitionConfig(ctx, releaseImage.ComponentImages(), hcluster, cpoImage)
		if err != nil {
			return "", nil, false, fmt.Errorf("failed to generate haproxy ignition config: %w", err)
		}
		if missing {
			missingConfigs = true
		} else {
			allConfigPlainText = append(allConfigPlainText, haproxyIgnitionConfig)
		}
	}

	coreConfigMapList := &corev1.ConfigMapList{}
	if err := r.List(ctx, coreConfigMapList, client.MatchingLabels{
		nodePoolCoreIgnitionConfigLabel: "true",
	}, client.InNamespace(controlPlaneResource)); err != nil {
		errors = append(errors, err)
	}

	if len(coreConfigMapList.Items) != expectedCoreConfigResources {
		missingConfigs = true
	}

	configs = coreConfigMapList.Items
	for _, config := range nodePool.Spec.Config {
		configConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.Name,
				Namespace: nodePool.Namespace,
			},
		}
		if err := r.Get(ctx, client.ObjectKeyFromObject(configConfigMap), configConfigMap); err != nil {
			errors = append(errors, err)
			continue
		}
		configs = append(configs, *configConfigMap)
	}

	// Look for NTO generated MachineConfigs from the hosted control plane namespace
	nodeTuningGeneratedConfigs := &corev1.ConfigMapList{}
	if err := r.List(ctx, nodeTuningGeneratedConfigs, client.MatchingLabels{
		nodeTuningGeneratedConfigLabel: "true",
		hyperv1.NodePoolLabel:          nodePool.GetName(),
	}, client.InNamespace(controlPlaneResource)); err != nil {
		errors = append(errors, err)
	}

	configs = append(configs, nodeTuningGeneratedConfigs.Items...)

	for i, config := range configs {
		cmPayload := config.Data[TokenSecretConfigKey]
		// ignition config-map payload may contain multiple manifests
		yamlReader := yaml.NewYAMLReader(bufio.NewReader(strings.NewReader(cmPayload)))
		for {
			manifestRaw, err := yamlReader.Read()
			if err != nil && err != io.EOF {
				errors = append(errors, fmt.Errorf("configmap %q contains invalid yaml: %w", config.Name, err))
				continue
			}
			if len(manifestRaw) != 0 && strings.TrimSpace(string(manifestRaw)) != "" {
				manifest, mirrorConfig, err := defaultAndValidateConfigManifest(manifestRaw)
				if err != nil {
					errors = append(errors, fmt.Errorf("configmap %q yaml document failed validation: %w", config.Name, err))
					continue
				}
				allConfigPlainText = append(allConfigPlainText, string(manifest))
				if mirrorConfig != nil && config.Namespace == nodePool.Namespace {
					mirrorConfig.ConfigMap = &configs[i]
					mirroredConfigs = append(mirroredConfigs, mirrorConfig)
				}
			}
			if err == io.EOF {
				break
			}
		}
	}

	// These configs are the input to a hash func whose output is used as part of the name of the user-data secret,
	// so our output must be deterministic.
	sort.Strings(allConfigPlainText)

	return strings.Join(allConfigPlainText, "\n---\n"), mirroredConfigs, missingConfigs, utilerrors.NewAggregate(errors)
}

func (r *NodePoolReconciler) getTuningConfig(ctx context.Context,
	nodePool *hyperv1.NodePool,
) (string, string, string, error) {
	var (
		configs                              []corev1.ConfigMap
		tunedAllConfigPlainText              []string
		performanceProfileConfigMapName      string
		performanceProfileAllConfigPlainText []string
		errors                               []error
	)

	for _, config := range nodePool.Spec.TuningConfig {
		configConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.Name,
				Namespace: nodePool.Namespace,
			},
		}
		if err := r.Get(ctx, client.ObjectKeyFromObject(configConfigMap), configConfigMap); err != nil {
			errors = append(errors, err)
			continue
		}
		configs = append(configs, *configConfigMap)
	}

	for _, config := range configs {
		manifestRaw, ok := config.Data[tuningConfigKey]
		if !ok {
			errors = append(errors, fmt.Errorf("no manifest found in configmap %q with key %q", config.Name, tuningConfigKey))
			continue
		}
		manifestTuned, manifestPerformanceProfile, err := validateTuningConfigManifest([]byte(manifestRaw))
		if err != nil {
			errors = append(errors, fmt.Errorf("configmap %q failed validation: %w", config.Name, err))
			continue
		}
		if manifestTuned != nil {
			tunedAllConfigPlainText = append(tunedAllConfigPlainText, string(manifestTuned))
		}
		if manifestPerformanceProfile != nil {
			performanceProfileConfigMapName = config.Name
			performanceProfileAllConfigPlainText = append(performanceProfileAllConfigPlainText, string(manifestPerformanceProfile))
		}
	}

	if len(performanceProfileAllConfigPlainText) > 1 {
		errors = append(errors, fmt.Errorf("there cannot be more than one PerformanceProfile per NodePool. found: %d", len(performanceProfileAllConfigPlainText)))
	}

	// Keep output deterministic to avoid unnecesary no-op changes to Tuned ConfigMap
	sort.Strings(tunedAllConfigPlainText)
	sort.Strings(performanceProfileAllConfigPlainText)

	return strings.Join(tunedAllConfigPlainText, "\n---\n"), strings.Join(performanceProfileAllConfigPlainText, "\n---\n"), performanceProfileConfigMapName, utilerrors.NewAggregate(errors)

}

func validateTuningConfigManifest(manifest []byte) ([]byte, []byte, error) {
	scheme := runtime.NewScheme()
	tunedv1.AddToScheme(scheme)
	performanceprofilev2.AddToScheme(scheme)

	yamlSerializer := serializer.NewSerializerWithOptions(
		serializer.DefaultMetaFactory, scheme, scheme,
		serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)
	cr, _, err := yamlSerializer.Decode(manifest, nil, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("error decoding config: %w", err)
	}

	switch obj := cr.(type) {
	case *tunedv1.Tuned:
		buff := bytes.Buffer{}
		if err := yamlSerializer.Encode(obj, &buff); err != nil {
			return nil, nil, fmt.Errorf("failed to encode Tuned object: %w", err)
		}
		manifest = buff.Bytes()
		return manifest, nil, nil

	case *performanceprofilev2.PerformanceProfile:
		validationErrors := obj.ValidateBasicFields()
		if len(validationErrors) > 0 {
			return nil, nil, fmt.Errorf("PerformanceProfile validation failed pp:%s : %w", obj.Name, coreerrors.Join(validationErrors.ToAggregate().Errors()...))
		}

		buff := bytes.Buffer{}
		if err := yamlSerializer.Encode(obj, &buff); err != nil {
			return nil, nil, fmt.Errorf("failed to encode performance profile after defaulting it: %w", err)
		}
		manifest = buff.Bytes()
		return nil, manifest, nil

	default:
		return nil, nil, fmt.Errorf("unsupported tuningConfig object type: %T", obj)
	}
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

func defaultAndValidateConfigManifest(manifest []byte) ([]byte, *MirrorConfig, error) {
	scheme := runtime.NewScheme()
	_ = mcfgv1.Install(scheme)
	_ = v1alpha1.Install(scheme)
	_ = configv1.Install(scheme)
	_ = configv1alpha1.Install(scheme)

	yamlSerializer := serializer.NewSerializerWithOptions(
		serializer.DefaultMetaFactory, scheme, scheme,
		serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: false},
	)
	// for manifests that should be mirrored into hosted control plane namespace
	var mirrorConfig *MirrorConfig

	cr, _, err := yamlSerializer.Decode(manifest, nil, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("error decoding config: %w", err)
	}

	switch obj := cr.(type) {
	case *mcfgv1.MachineConfig:
		addWorkerLabel(&obj.ObjectMeta)
		manifest, err = encode(cr, yamlSerializer)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to encode machine config after defaulting it: %w", err)
		}
	case *v1alpha1.ImageContentSourcePolicy:
	case *configv1.ImageDigestMirrorSet:
	case *configv1alpha1.ClusterImagePolicy:
	case *mcfgv1.KubeletConfig:
		obj.Spec.MachineConfigPoolSelector = &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"machineconfiguration.openshift.io/mco-built-in": "",
			},
		}
		manifest, err = encode(cr, yamlSerializer)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to encode kubelet config after setting built-in MCP selector: %w", err)
		}
	case *mcfgv1.ContainerRuntimeConfig:
		obj.Spec.MachineConfigPoolSelector = &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"machineconfiguration.openshift.io/mco-built-in": "",
			},
		}
		manifest, err = encode(cr, yamlSerializer)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to encode container runtime config after setting built-in MCP selector: %w", err)
		}
		mirrorConfig = &MirrorConfig{Labels: map[string]string{ContainerRuntimeConfigConfigMapLabel: ""}}
	default:
		return nil, nil, fmt.Errorf("unsupported config type: %T", obj)
	}
	return manifest, mirrorConfig, err
}

func addWorkerLabel(obj *metav1.ObjectMeta) {
	if obj.Labels == nil {
		obj.Labels = map[string]string{}
	}
	obj.Labels["machineconfiguration.openshift.io/role"] = "worker"
}

func encode(obj runtime.Object, ser *serializer.Serializer) ([]byte, error) {
	buff := bytes.Buffer{}
	if err := ser.Encode(obj, &buff); err != nil {
		return nil, err
	}
	return buff.Bytes(), nil
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

func (r *NodePoolReconciler) listMachineTemplates(nodePool *hyperv1.NodePool) ([]client.Object, error) {
	machineTemplateList := &unstructured.UnstructuredList{}

	var gvk schema.GroupVersionKind
	var err error
	switch nodePool.Spec.Platform.Type {
	// Define the desired template type and mutateTemplate function.
	case hyperv1.AWSPlatform:
		gvk, err = apiutil.GVKForObject(&capiaws.AWSMachineTemplate{}, api.Scheme)
		if err != nil {
			return nil, err
		}
	case hyperv1.KubevirtPlatform:
		gvk, err = apiutil.GVKForObject(&capikubevirt.KubevirtMachineTemplate{}, api.Scheme)
		if err != nil {
			return nil, err
		}
	case hyperv1.AgentPlatform:
		gvk, err = apiutil.GVKForObject(&agentv1.AgentMachine{}, api.Scheme)
		if err != nil {
			return nil, err
		}
	case hyperv1.AzurePlatform:
		gvk, err = apiutil.GVKForObject(&capiazure.AzureMachineTemplate{}, api.Scheme)
		if err != nil {
			return nil, err
		}
	case hyperv1.OpenStackPlatform:
		gvk, err = apiutil.GVKForObject(&capiopenstack.OpenStackMachineTemplate{}, api.Scheme)
		if err != nil {
			return nil, err
		}
	default:
		// need a default path that returns a value that does not cause the hypershift operator to crash
		// if no explicit machineTemplate is defined safe to assume none exist
		return nil, nil
	}

	machineTemplateList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   gvk.Group,
		Kind:    gvk.Kind,
		Version: gvk.Version,
	})
	if err := r.List(context.Background(), machineTemplateList); err != nil {
		return nil, fmt.Errorf("failed to list MachineTemplates: %w", err)
	}
	var filtered []client.Object
	for i, machineTemplate := range machineTemplateList.Items {
		if machineTemplate.GetAnnotations() != nil {
			if annotation, ok := machineTemplate.GetAnnotations()[nodePoolAnnotation]; ok &&
				annotation == client.ObjectKeyFromObject(nodePool).String() {
				filtered = append(filtered, &machineTemplateList.Items[i])
			}
		}
	}

	return filtered, nil
}

// TODO (alberto) drop this deterministic naming logic and get the name for child MachineDeployment from the status/annotation/label?
func generateName(infraName, clusterName, suffix string) string {
	return getName(fmt.Sprintf("%s-%s", infraName, clusterName), suffix, 43)
}

// getName returns a name given a base ("deployment-5") and a suffix ("deploy")
// It will first attempt to join them with a dash. If the resulting name is longer
// than maxLength: if the suffix is too long, it will truncate the base name and add
// an 8-character hash of the [base]-[suffix] string.  If the suffix is not too long,
// it will truncate the base, add the hash of the base and return [base]-[hash]-[suffix]
func getName(base, suffix string, maxLength int) string {
	if maxLength <= 0 {
		return ""
	}
	name := fmt.Sprintf("%s-%s", base, suffix)
	if len(name) <= maxLength {
		return name
	}

	// length of -hash-
	baseLength := maxLength - 10 - len(suffix)

	// if the suffix is too long, ignore it
	if baseLength < 1 {
		prefix := base[0:min(len(base), max(0, maxLength-9))]
		// Calculate hash on initial base-suffix string
		shortName := fmt.Sprintf("%s-%s", prefix, supportutil.HashSimple(name))
		return shortName[:min(maxLength, len(shortName))]
	}

	prefix := base[0:baseLength]
	// Calculate hash on initial base-suffix string
	return fmt.Sprintf("%s-%s-%s", prefix, supportutil.HashSimple(base), suffix)
}

// max returns the greater of its 2 inputs
func max(a, b int) int {
	if b > a {
		return b
	}
	return a
}

// min returns the lesser of its 2 inputs
func min(a, b int) int {
	if b < a {
		return b
	}
	return a
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

// machineTemplateBuilders returns a client.Object with a particular (platform)MachineTemplate type.
// a func to mutate the (platform)MachineTemplate.spec, a json string representation for (platform)MachineTemplate.spec
// and an error.
func machineTemplateBuilders(hcluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool,
	infraID, ami, powervsBootImage string, kubevirtBootImage kubevirt.BootImage, defaultSG bool) (client.Object, func(object client.Object) error, string, error) {
	var mutateTemplate func(object client.Object) error
	var template client.Object
	var machineTemplateSpec interface{}

	switch nodePool.Spec.Platform.Type {
	// Define the desired template type and mutateTemplate function.
	case hyperv1.AWSPlatform:
		template = &capiaws.AWSMachineTemplate{}
		var err error
		machineTemplateSpec, err = awsMachineTemplateSpec(infraID, ami, hcluster, nodePool, defaultSG)
		if err != nil {
			return nil, nil, "", err
		}
		mutateTemplate = func(object client.Object) error {
			o, _ := object.(*capiaws.AWSMachineTemplate)
			o.Spec = *machineTemplateSpec.(*capiaws.AWSMachineTemplateSpec)
			if o.Annotations == nil {
				o.Annotations = make(map[string]string)
			}
			o.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
			return nil
		}
	case hyperv1.AgentPlatform:
		template = &agentv1.AgentMachineTemplate{}
		machineTemplateSpec = agentMachineTemplateSpec(nodePool)
		mutateTemplate = func(object client.Object) error {
			o, _ := object.(*agentv1.AgentMachineTemplate)
			o.Spec = *machineTemplateSpec.(*agentv1.AgentMachineTemplateSpec)
			if o.Annotations == nil {
				o.Annotations = make(map[string]string)
			}
			o.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
			return nil
		}
	case hyperv1.KubevirtPlatform:
		template = &capikubevirt.KubevirtMachineTemplate{}
		var err error
		machineTemplateSpec, err = kubevirt.MachineTemplateSpec(nodePool, kubevirtBootImage, hcluster)
		if err != nil {
			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidMachineTemplateConditionType,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.InvalidKubevirtMachineTemplate,
				Message:            err.Error(),
				ObservedGeneration: nodePool.Generation,
			})

			return nil, nil, "", err
		} else {
			removeStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolValidMachineTemplateConditionType)
		}

		mutateTemplate = func(object client.Object) error {
			o, _ := object.(*capikubevirt.KubevirtMachineTemplate)
			o.Spec = *machineTemplateSpec.(*capikubevirt.KubevirtMachineTemplateSpec)
			if o.Annotations == nil {
				o.Annotations = make(map[string]string)
			}
			o.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
			return nil
		}
	case hyperv1.AzurePlatform:
		var err error
		template = &capiazure.AzureMachineTemplate{}
		machineTemplateSpec, err = azureMachineTemplateSpec(nodePool)
		if err != nil {
			return nil, nil, "", err
		}
		mutateTemplate = func(object client.Object) error {
			o, _ := object.(*capiazure.AzureMachineTemplate)

			// The azure api requires passing a public key. This key is randomly generated, the private portion is thrown away and the public key
			// gets written to the template.
			sshKey := o.Spec.Template.Spec.SSHPublicKey
			if sshKey == "" {
				sshKey, err = generateSSHPubkey()
				if err != nil {
					return fmt.Errorf("failed to generate a SSH key: %w", err)
				}
			}

			o.Spec = *machineTemplateSpec.(*capiazure.AzureMachineTemplateSpec)
			o.Spec.Template.Spec.SSHPublicKey = sshKey

			if o.Annotations == nil {
				o.Annotations = make(map[string]string)
			}
			o.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
			return nil
		}

	case hyperv1.PowerVSPlatform:
		template = &capipowervs.IBMPowerVSMachineTemplate{}
		machineTemplateSpec = ibmPowerVSMachineTemplateSpec(hcluster, nodePool, powervsBootImage)
		mutateTemplate = func(object client.Object) error {
			o, _ := object.(*capipowervs.IBMPowerVSMachineTemplate)
			o.Spec = *machineTemplateSpec.(*capipowervs.IBMPowerVSMachineTemplateSpec)
			if o.Annotations == nil {
				o.Annotations = make(map[string]string)
			}
			o.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
			return nil
		}
	case hyperv1.OpenStackPlatform:
		template = &capiopenstack.OpenStackMachineTemplate{}
		var err error
		machineTemplateSpec, err = openstack.MachineTemplateSpec(hcluster, nodePool)
		if err != nil {
			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidMachineTemplateConditionType,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.InvalidOpenStackMachineTemplate,
				Message:            err.Error(),
				ObservedGeneration: nodePool.Generation,
			})

			return nil, nil, "", err
		} else {
			removeStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolValidMachineTemplateConditionType)
		}

		mutateTemplate = func(object client.Object) error {
			o, _ := object.(*capiopenstack.OpenStackMachineTemplate)
			o.Spec = *machineTemplateSpec.(*capiopenstack.OpenStackMachineTemplateSpec)
			if o.Annotations == nil {
				o.Annotations = make(map[string]string)
			}
			o.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
			return nil
		}
	default:
		// TODO(alberto): Consider signal in a condition.
		return nil, nil, "", fmt.Errorf("unsupported platform type: %s", nodePool.Spec.Platform.Type)
	}
	template.SetNamespace(manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name))

	machineTemplateSpecJSON, err := json.Marshal(machineTemplateSpec)
	if err != nil {
		return nil, nil, "", err
	}

	template.SetName(generateMachineTemplateName(nodePool, machineTemplateSpecJSON))

	return template, mutateTemplate, string(machineTemplateSpecJSON), nil
}

func generateMachineTemplateName(nodePool *hyperv1.NodePool, machineTemplateSpecJSON []byte) string {
	// using HashStruct(machineTemplateSpecJSON) ensures a rolling upgrade is triggered
	// by creating a new template with a different name if any field changes.
	return getName(nodePool.GetName(), supportutil.HashSimple(machineTemplateSpecJSON),
		validation.DNS1123SubdomainMaxLength)
}

func validateInfraID(infraID string) error {
	if infraID == "" {
		return fmt.Errorf("infraID can't be empty")
	}
	return nil
}

func setExpirationTimestampOnToken(ctx context.Context, c client.Client, tokenSecret *corev1.Secret, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}

	// this should be a reasonable value to allow all in flight provisions to complete.
	timeUntilExpiry := 2 * time.Hour
	if tokenSecret.Annotations == nil {
		tokenSecret.Annotations = map[string]string{}
	}
	tokenSecret.Annotations[hyperv1.IgnitionServerTokenExpirationTimestampAnnotation] = now().Add(timeUntilExpiry).Format(time.RFC3339)
	return c.Update(ctx, tokenSecret)
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

// ensureMachineDeletion ensures all the machines belonging to the NodePool's MachineSet are fully deleted.
// This function can be deleted once the upstream PR (https://github.com/kubernetes-sigs/cluster-api-provider-aws/pull/3805) is merged and pulled into https://github.com/openshift/cluster-api-provider-aws.
// This function is necessary to ensure AWSMachines are fully deleted prior to deleting the NodePull secrets being deleted due to a bug introduced by https://github.com/kubernetes-sigs/cluster-api-provider-aws/pull/2271
// See https://github.com/openshift/hypershift/pull/1826#discussion_r1007349564 for more details.
func (r *NodePoolReconciler) ensureMachineDeletion(ctx context.Context, nodePool *hyperv1.NodePool) error {
	machines, err := r.getMachinesForNodePool(ctx, nodePool)
	if err != nil {
		return fmt.Errorf("error getting Machines: %w", err)
	}

	if len(machines) > 0 {
		return fmt.Errorf("there are still Machines in for NodePool %q", nodePool.Name)
	}

	return nil
}

// getMachinesForNodePool get all Machines listed with the nodePoolAnnotation
// within the control plane Namespace for that NodePool.
func (r *NodePoolReconciler) getMachinesForNodePool(ctx context.Context, nodePool *hyperv1.NodePool) ([]*capiv1.Machine, error) {
	npAnnotation := client.ObjectKeyFromObject(nodePool).String()
	machines := capiv1.MachineList{}
	controlPlaneNamespace := fmt.Sprintf("%s-%s", nodePool.Namespace, strings.ReplaceAll(nodePool.Spec.ClusterName, ".", "-"))

	if err := r.List(ctx, &machines, &client.ListOptions{Namespace: controlPlaneNamespace}); err != nil {
		return nil, fmt.Errorf("failed to list Machines: %w", err)
	}

	// Filter out only machines belonging to deleted NodePool
	var machinesForNodePool []*capiv1.Machine
	for i, machine := range machines.Items {
		if machine.Annotations[nodePoolAnnotation] == npAnnotation {
			machinesForNodePool = append(machinesForNodePool, &machines.Items[i])
		}
	}

	return sortedByCreationTimestamp(machinesForNodePool), nil
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

// secretJanitor reconciles secrets and determines which secrets should remain in the cluster and which should be cleaned up.
// Any secret annotated with a nodePool name should only be on the cluster if the nodePool continues to exist
// and if our current calculation for the inputs to the name matches what the secret is named.
type secretJanitor struct {
	*NodePoolReconciler

	now func() time.Time
}

func (r *secretJanitor) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("secret", req.String())

	secret := &corev1.Secret{}
	if err := r.Client.Get(ctx, req.NamespacedName, secret); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("not found", "request", req.String())
			return ctrl.Result{}, nil
		}
		log.Error(err, "error getting secret")
		return ctrl.Result{}, err
	}

	// only handle secrets that are associated with a NodePool
	nodePoolName, annotated := secret.Annotations[nodePoolAnnotation]
	if !annotated {
		return ctrl.Result{}, nil
	}
	log = log.WithValues("nodePool", nodePoolName)

	// only handle secret types that we know about explicitly
	shouldHandle := false
	for _, prefix := range []string{tokenSecretPrefix, ignitionUserDataPrefix} {
		if strings.HasPrefix(secret.Name, prefix) {
			shouldHandle = true
			break
		}
	}
	if !shouldHandle {
		return ctrl.Result{}, nil
	}

	nodePool := &hyperv1.NodePool{}
	if err := r.Client.Get(ctx, supportutil.ParseNamespacedName(nodePoolName), nodePool); err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "error getting nodepool")
		return ctrl.Result{}, err
	} else if apierrors.IsNotFound(err) {
		log.Info("removing secret as nodePool is missing")
		return ctrl.Result{}, r.Client.Delete(ctx, secret)
	}

	hcluster, err := GetHostedClusterByName(ctx, r.Client, nodePool.GetNamespace(), nodePool.Spec.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	shouldKeepOldUserData, err := r.shouldKeepOldUserData(ctx, hcluster)
	if err != nil {
		return ctrl.Result{}, err
	}
	if shouldKeepOldUserData {
		log.V(3).Info("Skipping secretJanitor reconciliation and keeping old user data secret")
		return ctrl.Result{}, nil
	}

	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
	expectedCoreConfigResources := expectedCoreConfigResourcesForHostedCluster(hcluster)
	releaseImage, err := r.getReleaseImage(ctx, hcluster, nodePool.Status.Version, nodePool.Spec.Release.Image)
	if err != nil {
		return ctrl.Result{}, err
	}

	config, _, missingConfigs, err := r.getConfig(ctx, nodePool, expectedCoreConfigResources, controlPlaneNamespace, releaseImage, hcluster)
	if err != nil {
		return ctrl.Result{}, err
	}
	if missingConfigs {
		return ctrl.Result{}, nil
	}
	targetVersion := releaseImage.Version()

	pullSecretName, err := r.getPullSecretName(ctx, hcluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	globalConfig, err := globalConfigString(hcluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	targetPayloadConfigHash := payloadConfigHash(config, targetVersion, pullSecretName, globalConfig)

	// synchronously deleting the ignition token is unsafe; we need to clean up tokens by annotating them to expire
	synchronousCleanup := func(ctx context.Context, c client.Client, secret *corev1.Secret) error {
		return c.Delete(ctx, secret)
	}
	type nodePoolSecret struct {
		expectedName   string
		matchingPrefix string
		cleanup        func(context.Context, client.Client, *corev1.Secret) error
	}
	valid := false
	options := []nodePoolSecret{
		{
			expectedName:   TokenSecret(controlPlaneNamespace, nodePool.Name, targetPayloadConfigHash).Name,
			matchingPrefix: tokenSecretPrefix,
			cleanup: func(ctx context.Context, c client.Client, secret *corev1.Secret) error {
				return setExpirationTimestampOnToken(ctx, c, secret, r.now)
			},
		},
		{
			expectedName:   IgnitionUserDataSecret(controlPlaneNamespace, nodePool.GetName(), targetPayloadConfigHash).Name,
			matchingPrefix: ignitionUserDataPrefix,
			cleanup:        synchronousCleanup,
		},
	}
	cleanup := synchronousCleanup
	var names []string
	for _, option := range options {
		names = append(names, option.expectedName)
		if secret.Name == option.expectedName {
			valid = true
		}
		if strings.HasPrefix(secret.Name, option.matchingPrefix) {
			cleanup = option.cleanup
		}
	}

	if valid {
		return ctrl.Result{}, nil
	}

	log.WithValues("options", names, "valid", valid).Info("removing secret as it does not match the expected set of names")
	return ctrl.Result{}, cleanup(ctx, r.Client, secret)
}

// shouldKeepOldUserData determines if the old user data should be kept.
// For AWS < 4.16, we keep the old userdata Secret so old Machines during rolled out can be deleted.
// Otherwise, deletion fails because of https://github.com/kubernetes-sigs/cluster-api-provider-aws/pull/3805.
// TODO (alberto): Drop this check when support for old versions without the fix is not needed anymore.
func (r *NodePoolReconciler) shouldKeepOldUserData(ctx context.Context, hc *hyperv1.HostedCluster) (bool, error) {
	if hc.Spec.Platform.Type != hyperv1.AWSPlatform {
		return false, nil
	}

	// If there's a current version in status, be conservative and assume that one is the one running CAPA.
	releaseImage := hc.Spec.Release.Image
	if hc.Status.Version != nil {
		if len(hc.Status.Version.History) > 0 {
			releaseImage = hc.Status.Version.History[0].Image
		}
	}

	pullSecretBytes, err := r.getPullSecretBytes(ctx, hc)
	if err != nil {
		return true, fmt.Errorf("failed to get pull secret bytes: %w", err)
	}

	releaseInfo, err := r.ReleaseProvider.Lookup(ctx, releaseImage, pullSecretBytes)
	if err != nil {
		return true, fmt.Errorf("failed to lookup release image: %w", err)
	}
	hostedClusterVersion, err := semver.Parse(releaseInfo.Version())
	if err != nil {
		return true, err
	}

	if hostedClusterVersion.LT(semver.MustParse("4.16.0")) {
		return true, nil
	}

	return false, nil
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

func payloadConfigHash(config, targetVersion, pullSecretName, globalConfig string) string {
	return supportutil.HashSimple(config + targetVersion + pullSecretName + globalConfig)
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

// reconcileMirroredConfigs mirrors configs into
// the HCP namespace, that are needed as an input for certain operators (such as NTO)
func (r *NodePoolReconciler) reconcileMirroredConfigs(ctx context.Context, logr logr.Logger, mirroredConfigs []*MirrorConfig, controlPlaneNamespace string, nodePool *hyperv1.NodePool) error {
	// get configs which already mirrored to the HCP namespace
	existingConfigsList := &corev1.ConfigMapList{}
	if err := r.List(ctx, existingConfigsList, &client.ListOptions{
		Namespace:     controlPlaneNamespace,
		LabelSelector: labels.SelectorFromValidatedSet(labels.Set{mirroredConfigLabel: ""}),
	}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	want := set.Set[string]{}
	for _, mirrorConfig := range mirroredConfigs {
		want.Insert(supportutil.ShortenName(mirrorConfig.Name, nodePool.Name, validation.LabelValueMaxLength))
	}
	have := set.Set[string]{}
	for _, configMap := range existingConfigsList.Items {
		have.Insert(configMap.Name)
	}
	toCreate, toDelete := want.Difference(have), have.Difference(want)
	if len(toCreate) > 0 {
		logr = logr.WithValues("toCreate", toCreate.UnsortedList())
	}
	if len(toDelete) > 0 {
		logr = logr.WithValues("toDelete", toDelete.UnsortedList())
	}
	if len(toCreate) > 0 || len(toDelete) > 0 {
		logr.Info("updating mirrored configs")
	}
	// delete the redundant configs that are no longer part of the nodepool spec
	for i := 0; i < len(existingConfigsList.Items); i++ {
		existingConfig := &existingConfigsList.Items[i]
		if toDelete.Has(existingConfig.Name) {
			_, err := supportutil.DeleteIfNeeded(ctx, r.Client, existingConfig)
			if err != nil {
				return fmt.Errorf("failed to delete ConfigMap %s/%s: %w", existingConfig.Namespace, existingConfig.Name, err)
			}
		}
	}
	// update or create the configs that need to be mirrored into the HCP NS
	for _, mirroredConfig := range mirroredConfigs {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      supportutil.ShortenName(mirroredConfig.Name, nodePool.Name, validation.LabelValueMaxLength),
				Namespace: controlPlaneNamespace},
		}
		if result, err := r.CreateOrUpdate(ctx, r.Client, cm, func() error {
			return mutateMirroredConfig(cm, mirroredConfig, nodePool)
		}); err != nil {
			return fmt.Errorf("failed to reconcile mirrored %s/%s ConfigMap: %w", cm.Namespace, cm.Name, err)
		} else {
			logr.Info("Reconciled ConfigMap", "result", result)
		}
	}
	return nil
}

// SetPerformanceProfileConditions checks for performance profile status updates, and reflects them in the nodepool status conditions
func (r *NodePoolReconciler) SetPerformanceProfileConditions(ctx context.Context, logger logr.Logger, nodePool *hyperv1.NodePool, controlPlaneNamespace string, toDelete bool) error {
	if toDelete {
		performanceProfileConditions := []string{
			hyperv1.NodePoolPerformanceProfileTuningAvailableConditionType,
			hyperv1.NodePoolPerformanceProfileTuningProgressingConditionType,
			hyperv1.NodePoolPerformanceProfileTuningUpgradeableConditionType,
			hyperv1.NodePoolPerformanceProfileTuningDegradedConditionType,
		}
		for _, condition := range performanceProfileConditions {
			removeStatusCondition(&nodePool.Status.Conditions, condition)
		}
		return nil
	}
	// Get performance profile status configmap
	cmList := &corev1.ConfigMapList{}
	if err := r.Client.List(ctx, cmList, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{NodeTuningGeneratedPerformanceProfileStatusLabel: "true"}),
		Namespace:     controlPlaneNamespace,
	}); err != nil {
		return err
	}
	if len(cmList.Items) > 1 {
		return fmt.Errorf("there cannot be more than one PerformanceProfile ConfigMap status per NodePool. found: %d NodePool: %s", len(cmList.Items), nodePool.Name)
	}
	if len(cmList.Items) == 0 {
		// Only log here and do not return an error because it might take sometime for NTO to
		// generate the ConfigMap with the PerformanceProfile status.
		// The creation of the ConfigMap itself triggers the reconciliation loop which eventually calls
		// this flow again.
		logger.Error(nil, "no PerformanceProfile ConfigMap status found", "Namespace", controlPlaneNamespace, "NodePool", nodePool.Name)
		return nil
	}
	performanceProfileStatusConfigMap := cmList.Items[0]
	statusRaw, ok := performanceProfileStatusConfigMap.Data["status"]
	if !ok {
		return fmt.Errorf("status not found in performance profile status configmap")
	}
	status := &performanceprofilev2.PerformanceProfileStatus{}
	if err := yaml.Unmarshal([]byte(statusRaw), status); err != nil {
		return fmt.Errorf("failed to decode the performance profile status: %w", err)
	}

	for _, performanceProfileCondition := range status.Conditions {
		condition := hyperv1.NodePoolCondition{
			Type:               fmt.Sprintf("%s/%s", hyperv1.NodePoolPerformanceProfileTuningConditionTypePrefix, performanceProfileCondition.Type),
			Status:             performanceProfileCondition.Status,
			Reason:             performanceProfileCondition.Reason,
			Message:            performanceProfileCondition.Message,
			ObservedGeneration: nodePool.Generation,
		}
		oldCondition := FindStatusCondition(nodePool.Status.Conditions, condition.Type)

		// Will set the condition only if it was not set previously, or has changed
		if oldCondition == nil || oldCondition.ObservedGeneration != condition.ObservedGeneration {
			SetStatusCondition(&nodePool.Status.Conditions, condition)
		}
	}
	return nil
}
