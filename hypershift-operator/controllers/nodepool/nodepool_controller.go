package nodepool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/blang/semver"
	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/operator/v1alpha1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1alpha1"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/kubevirt"
	ignserver "github.com/openshift/hypershift/ignition-server/controllers"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/supportedversion"
	"github.com/openshift/hypershift/support/upsert"
	supportutil "github.com/openshift/hypershift/support/util"
	mcfgv1 "github.com/openshift/hypershift/thirdparty/machineconfigoperator/pkg/apis/machineconfiguration.openshift.io/v1"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	serializer "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	k8sutilspointer "k8s.io/utils/pointer"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capipowervs "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
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

	nodePoolAnnotationPlatformMachineTemplate = "hypershift.openshift.io/nodePoolPlatformMachineTemplate"
	nodePoolAnnotationTaints                  = "hypershift.openshift.io/nodePoolTaints"
	nodePoolCoreIgnitionConfigLabel           = "hypershift.openshift.io/core-ignition-config"
	TokenSecretTokenGenerationTime            = "hypershift.openshift.io/last-token-generation-time"
	TokenSecretReleaseKey                     = "release"
	TokenSecretTokenKey                       = "token"
	TokenSecretPullSecretHashKey              = "pull-secret-hash"
	TokenSecretConfigKey                      = "config"
	TokenSecretAnnotation                     = "hypershift.openshift.io/ignition-config"
	TokenSecretIgnitionReachedAnnotation      = "hypershift.openshift.io/ignition-reached"
	TokenSecretNodePoolUpgradeType            = "hypershift.openshift.io/node-pool-upgrade-type"

	tuningConfigKey                = "tuning"
	tuningConfigMapLabel           = "hypershift.openshift.io/tuned-config"
	nodeTuningGeneratedConfigLabel = "hypershift.openshift.io/nto-generated-machine-config"

	controlPlaneOperatorManagesDecompressAndDecodeConfig = "io.openshift.hypershift.control-plane-operator-manages.decompress-decode-config"

	controlPlaneOperatorCreatesDefaultAWSSecurityGroup = "io.openshift.hypershift.control-plane-operator-creates-aws-sg"

	labelManagedPrefix = "managed.hypershift.openshift.io"
)

type NodePoolReconciler struct {
	client.Client
	recorder        record.EventRecorder
	ReleaseProvider releaseinfo.Provider
	controller      controller.Controller
	upsert.CreateOrUpdateProvider
	HypershiftOperatorImage string
	ImageMetadataProvider   supportutil.ImageMetadataProvider
}

type NotReadyError struct {
	error
}

type CPOCapabilities struct {
	DecompressAndDecodeConfig     bool
	CreateDefaultAWSSecurityGroup bool
}

func (r *NodePoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	controller, err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.NodePool{}).
		// We want to reconcile when the HostedCluster IgnitionEndpoint is available.
		Watches(&source.Kind{Type: &hyperv1.HostedCluster{}}, handler.EnqueueRequestsFromMapFunc(r.enqueueNodePoolsForHostedCluster)).
		Watches(&source.Kind{Type: &capiv1.MachineDeployment{}}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool)).
		Watches(&source.Kind{Type: &capiv1.MachineSet{}}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool)).
		Watches(&source.Kind{Type: &capiaws.AWSMachineTemplate{}}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool)).
		Watches(&source.Kind{Type: &agentv1.AgentMachineTemplate{}}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool)).
		Watches(&source.Kind{Type: &capiazure.AzureMachineTemplate{}}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool)).
		// We want to reconcile when the user data Secret or the token Secret is unexpectedly changed out of band.
		Watches(&source.Kind{Type: &corev1.Secret{}}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool)).
		// We want to reconcile when the ConfigMaps referenced by the spec.config and also the core ones change.
		Watches(&source.Kind{Type: &corev1.ConfigMap{}}, handler.EnqueueRequestsFromMapFunc(r.enqueueNodePoolsForConfig)).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 10,
		}).
		Build(r)
	if err != nil {
		return errors.Wrap(err, "failed setting up with a controller manager")
	}

	r.controller = controller
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

	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(nodePool.Namespace, nodePool.Spec.ClusterName).Name

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

	// Get HostedCluster deps.
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name).Name
	ignEndpoint := hcluster.Status.IgnitionEndpoint
	infraID := hcluster.Spec.InfraID
	if err := validateInfraID(infraID); err != nil {
		// We don't return the error here as reconciling won't solve the input problem.
		// An update event will trigger reconciliation.
		// TODO (alberto): consider this an condition failure reason when revisiting conditions.
		log.Error(err, "Invalid infraID, waiting.")
		return ctrl.Result{}, nil
	}

	// 1. - Reconcile conditions according to current state of the world.
	proxy := globalconfig.ProxyConfig()
	globalconfig.ReconcileProxyConfigWithStatusFromHostedCluster(proxy, hcluster)

	// Validate autoscaling input.
	if err := validateAutoscaling(nodePool); err != nil {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolAutoscalingEnabledConditionType,
			Status:             corev1.ConditionFalse,
			Message:            err.Error(),
			Reason:             hyperv1.NodePoolValidationFailedReason,
			ObservedGeneration: nodePool.Generation,
		})
		// We don't return the error here as reconciling won't solve the input problem.
		// An update event will trigger reconciliation.
		log.Error(err, "validating autoscaling parameters failed")
		return ctrl.Result{}, nil
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
			ami, err = defaultNodePoolAMI(hcluster.Spec.Platform.AWS.Region, releaseImage)
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

		if len(nodePool.Spec.Platform.AWS.SecurityGroups) == 0 &&
			(hcluster.Status.Platform == nil || hcluster.Status.Platform.AWS == nil || hcluster.Status.Platform.AWS.DefaultWorkerSecurityGroupID == "") {
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
				Message:            "NodePool has a security group",
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

	// Validate KubeVirt platform specific input
	var kubevirtBootImage string
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

		kubevirtBootImage, err = kubevirt.GetImage(nodePool, releaseImage)
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
			Message:            fmt.Sprintf("Bootstrap KubeVirt Image is %q", kubevirtBootImage),
			ObservedGeneration: nodePool.Generation,
		})
	}

	// Validate config input.
	// 3 generic core config resources: fips, ssh and haproxy.
	// TODO (alberto): consider moving the expectedCoreConfigResources check
	// into the token Secret controller so we don't block Machine infra creation on this.
	expectedCoreConfigResources := 3
	if len(hcluster.Spec.ImageContentSources) > 0 {
		// additional core config resource created when image content source specified.
		expectedCoreConfigResources += 1
	}
	config, missingConfigs, err := r.getConfig(ctx, nodePool, expectedCoreConfigResources, controlPlaneNamespace, releaseImage, hcluster)
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
	SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolValidMachineConfigConditionType,
		Status:             corev1.ConditionTrue,
		Reason:             hyperv1.AsExpectedReason,
		ObservedGeneration: nodePool.Generation,
	})

	// Initialize NodePool annotations
	if nodePool.Annotations == nil {
		nodePool.Annotations = make(map[string]string)
	}

	// Retrieve pull secret name to check for changes when config is checked for updates
	pullSecretName, err := r.getPullSecretName(ctx, hcluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Check if config needs to be updated.
	targetConfigHash := supportutil.HashStruct(config + pullSecretName)
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
		removeStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolUpdatingConfigConditionType)
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
		removeStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolUpdatingVersionConditionType)
	}

	// Signal ignition payload generation
	targetPayloadConfigHash := supportutil.HashStruct(config + targetVersion + pullSecretName)
	tokenSecret := TokenSecret(controlPlaneNamespace, nodePool.Name, targetPayloadConfigHash)
	condition, err := r.createValidGeneratedPayloadCondition(ctx, tokenSecret, nodePool.Generation)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error setting ValidGeneratedPayload condition: %w", err)
	}
	SetStatusCondition(&nodePool.Status.Conditions, *condition)

	reachedIgnitionEndpointCondition, err := r.createReachedIgnitionEndpointCondition(ctx, tokenSecret, nodePool.Generation)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error setting IgnitionReached condition: %w", err)
	}
	SetStatusCondition(&nodePool.Status.Conditions, *reachedIgnitionEndpointCondition)

	// Validate tuningConfig input.
	tuningConfig, err := r.getTuningConfig(ctx, nodePool)
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

	tuningConfigMap := TuningConfigMap(controlPlaneNamespace, nodePool.Name)
	if tuningConfig == "" {
		err = r.Get(ctx, client.ObjectKeyFromObject(tuningConfigMap), tuningConfigMap)
		if err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to get tuningConfig ConfigMap: %w", err)
		}
		if err == nil {
			if err := r.Delete(ctx, tuningConfigMap); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to delete tuningConfig ConfigMap with no Tuneds defined: %w", err)
			}
		}
	} else {
		if result, err := r.CreateOrUpdate(ctx, r.Client, tuningConfigMap, func() error {
			return reconcileTuningConfigMap(tuningConfigMap, nodePool, tuningConfig)
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile Tuned ConfigMap: %w", err)
		} else {
			log.Info("Reconciled Tuned ConfigMap", "result", result)
		}
	}

	// Set AllMachinesReadyCondition.
	// Get all Machines for NodePool.
	machines, err := r.getMachinesForNodePool(nodePool)
	if err != nil {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolAllMachinesReadyConditionType,
			Status:             corev1.ConditionUnknown,
			Reason:             hyperv1.NodePoolFailedToGetReason,
			Message:            err.Error(),
			ObservedGeneration: nodePool.Generation,
		})
		return ctrl.Result{}, fmt.Errorf("failed to get Machines: %w", err)
	}

	status := corev1.ConditionTrue
	reason := hyperv1.AsExpectedReason
	var message string

	if len(machines) < 1 {
		status = corev1.ConditionFalse
		reason = hyperv1.NodePoolNotFoundReason
		message = "No Machines are created"
	}

	// Aggregate conditions.
	// TODO (alberto): consider bubbling failureReason / failureMessage.
	// This a rudimentary approach which aggregates every Machine, until
	// https://github.com/kubernetes-sigs/cluster-api/pull/6218 and
	// https://github.com/kubernetes-sigs/cluster-api/pull/6025
	// are solved.
	// Eventually we should solve this in CAPI to make it available in MachineDeployments / MachineSets
	// with a consumable "Reason" and an aggregated "Message".
	for _, machine := range machines {
		condition := findCAPIStatusCondition(machine.Status.Conditions, capiv1.ReadyCondition)
		if condition != nil && condition.Status != corev1.ConditionTrue {
			status = corev1.ConditionFalse
			reason = condition.Reason
			// We append the reason as part of the higher Message, since the message is meaningless.
			// This is how a CAPI condition looks like in AWS for an instance deleted out of band failure.
			//	- lastTransitionTime: "2022-11-28T15:14:28Z"
			//		message: 1 of 2 completed
			//		reason: InstanceTerminated
			//		severity: Error
			//		status: "False"
			//		type: Ready
			message = message + fmt.Sprintf("Machine %s: %s\n", machine.Name, condition.Reason)
		}
	}

	if status == corev1.ConditionTrue {
		message = hyperv1.AllIsWellMessage
	}

	SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolAllMachinesReadyConditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: nodePool.Generation,
	})

	// Set AllNodesHealthyCondition.
	status = corev1.ConditionTrue
	reason = hyperv1.AsExpectedReason
	message = ""

	if len(machines) < 1 {
		status = corev1.ConditionFalse
		reason = hyperv1.NodePoolNotFoundReason
		message = "No Machines are created"
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

	SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolAllNodesHealthyConditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: nodePool.Generation,
	})

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
			if err := setExpirationTimestampOnToken(ctx, r.Client, tokenSecret); err != nil && !apierrors.IsNotFound(err) {
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
	if result, err := r.CreateOrUpdate(ctx, r.Client, tokenSecret, func() error {
		return reconcileTokenSecret(tokenSecret, nodePool, compressedConfig.Bytes(), pullSecretBytes)
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
			return r.reconcileMachineHealthCheck(mhc, nodePool, infraID)
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
	//FIXME: In future we may want to use the spec field instead
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
	//FIXME: In future we may want to use the spec field instead
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
	if err = r.ensureMachineDeletion(nodePool); err != nil {
		return err
	}

	// Delete all secrets related to the NodePool
	if err := r.deleteNodePoolSecrets(ctx, nodePool); err != nil {
		return fmt.Errorf("failed to delete NodePool secrets: %w", err)
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

// reconcileTuningConfigMap inserts the Tuned object manifest in tuningConfig into ConfigMap tuningConfigMap.
// This is used to mirror the Tuned object manifest into the control plane namespace, for the Node
// Tuning Operator to mirror and reconcile in the hosted cluster.
func reconcileTuningConfigMap(tuningConfigMap *corev1.ConfigMap, nodePool *hyperv1.NodePool, tuningConfig string) error {
	tuningConfigMap.Immutable = k8sutilspointer.Bool(false)
	if tuningConfigMap.Annotations == nil {
		tuningConfigMap.Annotations = make(map[string]string)
	}
	if tuningConfigMap.Labels == nil {
		tuningConfigMap.Labels = make(map[string]string)
	}

	tuningConfigMap.Labels[tuningConfigMapLabel] = "true"
	tuningConfigMap.Annotations[nodePoolAnnotation] = nodePool.GetName()
	tuningConfigMap.Labels[nodePoolAnnotation] = nodePool.GetName()

	if tuningConfigMap.Data == nil {
		tuningConfigMap.Data = map[string]string{}
	}
	tuningConfigMap.Data[tuningConfigKey] = tuningConfig

	return nil
}

func reconcileTokenSecret(tokenSecret *corev1.Secret, nodePool *hyperv1.NodePool, compressedConfig []byte, pullSecret []byte) error {
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
		tokenSecret.Data[TokenSecretPullSecretHashKey] = []byte(supportutil.HashStruct(pullSecret))
	}
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
	machineDeployment.Labels[capiv1.ClusterLabelName] = CAPIClusterName

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
	machineDeployment.Spec.Selector.MatchLabels[capiv1.ClusterLabelName] = CAPIClusterName
	machineDeployment.Spec.Template = capiv1.MachineTemplateSpec{
		ObjectMeta: capiv1.ObjectMeta{
			Labels: map[string]string{
				resourcesName:           resourcesName,
				capiv1.ClusterLabelName: CAPIClusterName,
			},
			// Annotations here propagate down to Machines
			// https://cluster-api.sigs.k8s.io/developer/architecture/controllers/metadata-propagation.html#machinedeployment.
			Annotations: map[string]string{
				// TODO (alberto): Use conditions to signal an in progress rolling upgrade
				// similar to what we do with nodePoolAnnotationCurrentConfig
				nodePoolAnnotationPlatformMachineTemplate: machineTemplateSpecJSON, // This will trigger a deployment rolling upgrade when its value changes.
				nodePoolAnnotation:                        client.ObjectKeyFromObject(nodePool).String(),
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
				Name:       machineTemplateCR.GetName(),
			},
			// Keep current version for later check.
			Version:          machineDeployment.Spec.Template.Spec.Version,
			NodeDrainTimeout: nodePool.Spec.NodeDrainTimeout,
		},
	}

	// Propagate labels.
	for k, v := range nodePool.Spec.NodeLabels {
		// Propagated managed labels down to Machines with a known hardcoded prefix
		// so the CPO HCCO Node controller can recognize them and apply them to Nodes.
		labelKey := fmt.Sprintf("%s.%s", labelManagedPrefix, k)
		machineDeployment.Spec.Template.Labels[labelKey] = v
	}

	// Propagate taints.
	taintsInJSON, err := taintsToJSON(nodePool.Spec.Taints)
	if err != nil {
		return err
	}
	machineDeployment.Spec.Template.Annotations[nodePoolAnnotationTaints] = taintsInJSON

	// Set strategy
	machineDeployment.Spec.Strategy = &capiv1.MachineDeploymentStrategy{}
	machineDeployment.Spec.Strategy.Type = capiv1.MachineDeploymentStrategyType(nodePool.Spec.Management.Replace.Strategy)
	if nodePool.Spec.Management.Replace.RollingUpdate != nil {
		machineDeployment.Spec.Strategy.RollingUpdate = &capiv1.MachineRollingUpdateDeployment{
			MaxUnavailable: nodePool.Spec.Management.Replace.RollingUpdate.MaxUnavailable,
			MaxSurge:       nodePool.Spec.Management.Replace.RollingUpdate.MaxSurge,
		}
	}

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

		// We return early here during a version/config update to persist the resource with new user data Secret,
		// so in the next reconciling loop we get a new MachineDeployment.Generation
		// and we can do a legit MachineDeploymentComplete/MachineDeployment.Status.ObservedGeneration check.
		// Before persisting, if the NodePool is brand new we want to make sure the replica number is set so the machineDeployment controller
		// does not panic.
		if machineDeployment.Spec.Replicas == nil {
			setMachineDeploymentReplicas(nodePool, machineDeployment)
		}
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
	}

	setMachineDeploymentReplicas(nodePool, machineDeployment)

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

func (r *NodePoolReconciler) reconcileMachineHealthCheck(mhc *capiv1.MachineHealthCheck,
	nodePool *hyperv1.NodePool,
	CAPIClusterName string) error {
	// Opinionated spec based on
	// https://github.com/openshift/managed-cluster-config/blob/14d4255ec75dc263ffd3d897dfccc725cb2b7072/deploy/osd-machine-api/011-machine-api.srep-worker-healthcheck.MachineHealthCheck.yaml
	// TODO (alberto): possibly expose this config at the nodePool API.
	maxUnhealthy := intstr.FromInt(2)
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
					Duration: 8 * time.Minute,
				},
			},
			{
				Type:   corev1.NodeReady,
				Status: corev1.ConditionUnknown,
				Timeout: metav1.Duration{
					Duration: 8 * time.Minute,
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
) (configsRaw string, missingConfigs bool, err error) {
	var configs []corev1.ConfigMap
	var allConfigPlainText []string
	var errors []error

	isHAProxyIgnitionConfigManaged, cpoImage, err := r.isHAProxyIgnitionConfigManaged(ctx, hcluster)
	if err != nil {
		return "", false, fmt.Errorf("failed to check if we manage haproxy ignition config: %w", err)
	}
	if isHAProxyIgnitionConfigManaged {
		oldHAProxyIgnitionConfig := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneResource, Name: "ignition-config-apiserver-haproxy"},
		}
		err := r.Client.Get(ctx, client.ObjectKeyFromObject(oldHAProxyIgnitionConfig), oldHAProxyIgnitionConfig)
		if err != nil && !apierrors.IsNotFound(err) {
			return "", false, fmt.Errorf("failed to get CPO-managed haproxy ignition config: %w", err)
		}
		if err == nil {
			if err := r.Client.Delete(ctx, oldHAProxyIgnitionConfig); err != nil && !apierrors.IsNotFound(err) {
				return "", false, fmt.Errorf("failed to delete the CPO-managed haproxy ignition config: %w", err)
			}
		}
		expectedCoreConfigResources--

		haproxyIgnitionConfig, missing, err := r.reconcileHAProxyIgnitionConfig(ctx, releaseImage.ComponentImages(), hcluster, cpoImage)
		if err != nil {
			return "", false, fmt.Errorf("failed to generate haporoxy ignition config: %w", err)
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

	for _, config := range configs {
		manifestRaw := config.Data[TokenSecretConfigKey]
		manifest, err := defaultAndValidateConfigManifest([]byte(manifestRaw))
		if err != nil {
			errors = append(errors, fmt.Errorf("configmap %q failed validation: %w", config.Name, err))
			continue
		}

		allConfigPlainText = append(allConfigPlainText, string(manifest))
	}

	// These configs are the input to a hash func whose output is used as part of the name of the user-data secret,
	// so our output must be deterministic.
	sort.Strings(allConfigPlainText)

	return strings.Join(allConfigPlainText, "\n---\n"), missingConfigs, utilerrors.NewAggregate(errors)
}

func (r *NodePoolReconciler) getTuningConfig(ctx context.Context,
	nodePool *hyperv1.NodePool,
) (configsRaw string, err error) {
	var configs []corev1.ConfigMap
	var allConfigPlainText []string
	var errors []error

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
		manifest, err := validateTuningConfigManifest([]byte(manifestRaw))
		if err != nil {
			errors = append(errors, fmt.Errorf("configmap %q failed validation: %w", config.Name, err))
			continue
		}

		allConfigPlainText = append(allConfigPlainText, string(manifest))
	}

	// Keep output deterministic to avoid unnecessary no-op changes to Tuned ConfigMap
	sort.Strings(allConfigPlainText)
	return strings.Join(allConfigPlainText, "\n---\n"), utilerrors.NewAggregate(errors)
}

func validateTuningConfigManifest(manifest []byte) ([]byte, error) {
	scheme := runtime.NewScheme()
	tunedv1.AddToScheme(scheme)

	yamlSerializer := serializer.NewSerializerWithOptions(
		serializer.DefaultMetaFactory, scheme, scheme,
		serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)

	cr, _, err := yamlSerializer.Decode(manifest, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("error decoding config: %w", err)
	}

	switch obj := cr.(type) {
	case *tunedv1.Tuned:
		buff := bytes.Buffer{}
		if err := yamlSerializer.Encode(obj, &buff); err != nil {
			return nil, fmt.Errorf("failed to encode Tuned object: %w", err)
		}
		manifest = buff.Bytes()

	default:
		return nil, fmt.Errorf("unsupported tuningConfig object type: %T", obj)
	}

	return manifest, err
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

func defaultAndValidateConfigManifest(manifest []byte) ([]byte, error) {
	scheme := runtime.NewScheme()
	mcfgv1.Install(scheme)
	v1alpha1.Install(scheme)

	YamlSerializer := serializer.NewSerializerWithOptions(
		serializer.DefaultMetaFactory, scheme, scheme,
		serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)

	cr, _, err := YamlSerializer.Decode(manifest, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("error decoding config: %w", err)
	}

	switch obj := cr.(type) {
	case *mcfgv1.MachineConfig:
		if obj.Labels == nil {
			obj.Labels = map[string]string{}
		}
		obj.Labels["machineconfiguration.openshift.io/role"] = "worker"
		buff := bytes.Buffer{}
		if err := YamlSerializer.Encode(obj, &buff); err != nil {
			return nil, fmt.Errorf("failed to encode config after defaulting it: %w", err)
		}
		manifest = buff.Bytes()
	case *v1alpha1.ImageContentSourcePolicy:
	case *mcfgv1.KubeletConfig:
	case *mcfgv1.ContainerRuntimeConfig:
	default:
		return nil, fmt.Errorf("unsupported config type: %T", obj)
	}

	return manifest, err
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

	minSupportedVersion := supportedversion.MinSupportedVersion
	if hostedCluster.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		//IBM Cloud is allowed to manage 4.9 clusters
		minSupportedVersion = semver.MustParse("4.9.0")
	}

	releaseInfo, err := r.ReleaseProvider.Lookup(ctx, hostedCluster.Spec.Release.Image, pullSecretBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup release image: %w", err)
	}
	hostedClusterVersion, err := semver.Parse(releaseInfo.Version())
	if err != nil {
		return nil, err
	}

	return ReleaseImage, supportedversion.IsValidReleaseVersion(&wantedVersion, &currentVersionParsed, &hostedClusterVersion, &minSupportedVersion, hostedCluster.Spec.Networking.NetworkType, hostedCluster.Spec.Platform.Type)
}

func isUpdatingVersion(nodePool *hyperv1.NodePool, targetVersion string) bool {
	return targetVersion != nodePool.Status.Version
}

func isUpdatingConfig(nodePool *hyperv1.NodePool, targetConfigHash string) bool {
	return targetConfigHash != nodePool.GetAnnotations()[nodePoolAnnotationCurrentConfig]
}

func isAutoscalingEnabled(nodePool *hyperv1.NodePool) bool {
	return nodePool.Spec.AutoScaling != nil
}

func validateAutoscaling(nodePool *hyperv1.NodePool) error {
	if nodePool.Spec.Replicas != nil && nodePool.Spec.AutoScaling != nil {
		return fmt.Errorf("only one of nodePool.Spec.Replicas or nodePool.Spec.AutoScaling can be set")
	}

	if nodePool.Spec.AutoScaling != nil {
		max := nodePool.Spec.AutoScaling.Max
		min := nodePool.Spec.AutoScaling.Min

		if max < min {
			return fmt.Errorf("max must be equal or greater than min. Max: %v, Min: %v", max, min)
		}

		if max == 0 || min == 0 {
			return fmt.Errorf("max and min must be not zero. Max: %v, Min: %v", max, min)
		}
	}

	return nil
}

func defaultNodePoolAMI(region string, releaseImage *releaseinfo.ReleaseImage) (string, error) {
	// TODO: The architecture should be specified from the API
	arch, foundArch := releaseImage.StreamMetadata.Architectures["x86_64"]
	if !foundArch {
		return "", fmt.Errorf("couldn't find OS metadata for architecture %q", "x64_64")
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

func (r *NodePoolReconciler) enqueueNodePoolsForHostedCluster(obj client.Object) []reconcile.Request {
	var result []reconcile.Request

	hc, ok := obj.(*hyperv1.HostedCluster)
	if !ok {
		panic(fmt.Sprintf("Expected a HostedCluster but got a %T", obj))
	}

	nodePoolList := &hyperv1.NodePoolList{}
	if err := r.List(context.Background(), nodePoolList, client.InNamespace(hc.Namespace)); err != nil {
		ctrl.LoggerFrom(context.Background()).Error(err, "Failed to list nodePools")
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

func (r *NodePoolReconciler) enqueueNodePoolsForConfig(obj client.Object) []reconcile.Request {
	var result []reconcile.Request

	cm, ok := obj.(*corev1.ConfigMap)
	if !ok {
		panic(fmt.Sprintf("Expected a ConfigMap but got a %T", obj))
	}

	// Get all NodePools in the ConfigMap Namespace.
	nodePoolList := &hyperv1.NodePoolList{}
	if err := r.List(context.Background(), nodePoolList, client.InNamespace(cm.Namespace)); err != nil {
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
	if _, ok := obj.GetLabels()[tuningConfigMapLabel]; ok {
		return enqueueParentNodePool(obj)
	}

	// Check if the ConfigMap is generated by an operator in the control plane namespace
	// corresponding to this nodepool.
	if _, ok := obj.GetLabels()[nodeTuningGeneratedConfigLabel]; ok {
		nodePoolName := obj.GetLabels()[hyperv1.NodePoolLabel]
		nodePoolNamespacedName, err := r.getNodePoolNamespacedName(nodePoolName, obj.GetNamespace())
		if err != nil {
			return result
		}
		obj.SetAnnotations(map[string]string{
			nodePoolAnnotation: nodePoolNamespacedName.String(),
		})
		return enqueueParentNodePool(obj)
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

func enqueueParentNodePool(obj client.Object) []reconcile.Request {
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
		shortName := fmt.Sprintf("%s-%s", prefix, supportutil.HashStruct(name))
		return shortName[:min(maxLength, len(shortName))]
	}

	prefix := base[0:baseLength]
	// Calculate hash on initial base-suffix string
	return fmt.Sprintf("%s-%s-%s", prefix, supportutil.HashStruct(base), suffix)
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
	infraID, ami, powervsBootImage, kubevirtBootImage string, defaultSG bool) (client.Object, func(object client.Object) error, string, error) {
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
		machineTemplateSpec = kubevirt.MachineTemplateSpec(kubevirtBootImage, nodePool, hcluster)
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
		template = &capiazure.AzureMachineTemplate{}
		mutateTemplate = func(object client.Object) error {
			o, _ := object.(*capiazure.AzureMachineTemplate)
			spec, err := azureMachineTemplateSpec(hcluster, nodePool, o.Spec)
			if err != nil {
				return err
			}
			o.Spec = *spec
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
	default:
		// TODO(alberto): Consider signal in a condition.
		return nil, nil, "", fmt.Errorf("unsupported platform type: %s", nodePool.Spec.Platform.Type)
	}
	template.SetNamespace(manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name).Name)
	template.SetName(nodePool.GetName())

	machineTemplateSpecJSON, err := json.Marshal(machineTemplateSpec)
	if err != nil {
		return nil, nil, "", err
	}

	return template, mutateTemplate, string(machineTemplateSpecJSON), nil
}

func validateInfraID(infraID string) error {
	if infraID == "" {
		return fmt.Errorf("infraID can't be empty")
	}
	return nil
}

func setExpirationTimestampOnToken(ctx context.Context, c client.Client, tokenSecret *corev1.Secret) error {
	// this should be a reasonable value to allow all in flight provisions to complete.
	timeUntilExpiry := 2 * time.Hour
	if tokenSecret.Annotations == nil {
		tokenSecret.Annotations = map[string]string{}
	}
	tokenSecret.Annotations[hyperv1.IgnitionServerTokenExpirationTimestampAnnotation] = time.Now().Add(timeUntilExpiry).Format(time.RFC3339)
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
func (r *NodePoolReconciler) ensureMachineDeletion(nodePool *hyperv1.NodePool) error {
	machines, err := r.getMachinesForNodePool(nodePool)
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
func (r *NodePoolReconciler) getMachinesForNodePool(nodePool *hyperv1.NodePool) ([]capiv1.Machine, error) {
	machines := capiv1.MachineList{}
	controlPlaneNamespace := fmt.Sprintf("%s-%s", nodePool.Namespace, strings.ReplaceAll(nodePool.Spec.ClusterName, ".", "-"))

	if err := r.List(context.Background(), &machines, &client.ListOptions{Namespace: controlPlaneNamespace}); err != nil {
		return nil, fmt.Errorf("failed to list Machines: %w", err)
	}

	// Filter out only machines belonging to deleted NodePool
	var machinesForNodePool []capiv1.Machine
	for i, machine := range machines.Items {
		if machine.Annotations[nodePoolAnnotation] == client.ObjectKeyFromObject(nodePool).String() {
			machinesForNodePool = append(machinesForNodePool, machines.Items[i])
		}
	}

	return machinesForNodePool, nil
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
	pullSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hostedCluster.Namespace, Name: hostedCluster.Spec.PullSecret.Name}, pullSecret); err != nil {
		return "", fmt.Errorf("cannot get pull secret %s/%s: %w", hostedCluster.Namespace, hostedCluster.Spec.PullSecret.Name, err)
	}
	if _, hasKey := pullSecret.Data[corev1.DockerConfigJsonKey]; !hasKey {
		return "", fmt.Errorf("pull secret %s/%s missing %q key when retrieving pull secret name", pullSecret.Namespace, pullSecret.Name, corev1.DockerConfigJsonKey)
	}
	return pullSecret.Name, nil
}
