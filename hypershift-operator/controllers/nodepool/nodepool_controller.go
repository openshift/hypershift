package nodepool

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"time"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/openshift/api/operator/v1alpha1"
	api "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	hyperutil "github.com/openshift/hypershift/hypershift-operator/controllers/util"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/upsert"
	mcfgv1 "github.com/openshift/hypershift/thirdparty/machineconfigoperator/pkg/apis/machineconfiguration.openshift.io/v1"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	serializer "k8s.io/apimachinery/pkg/runtime/serializer/json"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	k8sutilspointer "k8s.io/utils/pointer"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/api/v1beta1"
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
	finalizer                               = "hypershift.openshift.io/finalizer"
	autoscalerMaxAnnotation                 = "cluster.x-k8s.io/cluster-api-autoscaler-node-group-max-size"
	autoscalerMinAnnotation                 = "cluster.x-k8s.io/cluster-api-autoscaler-node-group-min-size"
	nodePoolAnnotation                      = "hypershift.openshift.io/nodePool"
	nodePoolAnnotationCurrentConfig         = "hypershift.openshift.io/nodePoolCurrentConfig"
	nodePoolAnnotationCurrentConfigVersion  = "hypershift.openshift.io/nodePoolCurrentConfigVersion"
	nodePoolAnnotationCurrentProviderConfig = "hypershift.openshift.io/nodePoolCurrentProviderConfig"
	nodePoolCoreIgnitionConfigLabel         = "hypershift.openshift.io/core-ignition-config"
	TokenSecretReleaseKey                   = "release"
	TokenSecretTokenKey                     = "token"
	TokenSecretConfigKey                    = "config"
	TokenSecretAnnotation                   = "hypershift.openshift.io/ignition-config"
)

type NodePoolReconciler struct {
	client.Client
	recorder        record.EventRecorder
	ReleaseProvider releaseinfo.Provider

	tracer trace.Tracer

	upsert.CreateOrUpdateProvider
}

func (r *NodePoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.NodePool{}).
		// We want to reconcile when the HostedCluster IgnitionEndpoint is available.
		Watches(&source.Kind{Type: &hyperv1.HostedCluster{}}, handler.EnqueueRequestsFromMapFunc(r.enqueueNodePoolsForHostedCluster)).
		Watches(&source.Kind{Type: &capiv1.MachineDeployment{}}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool)).
		Watches(&source.Kind{Type: &capiaws.AWSMachineTemplate{}}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool)).
		// We want to reconcile when the user data Secret or the token Secret is unexpectedly changed out of band.
		Watches(&source.Kind{Type: &corev1.Secret{}}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool)).
		// We want to reconcile when the ConfigMaps referenced by the spec.config and also the core ones change.
		Watches(&source.Kind{Type: &corev1.ConfigMap{}}, handler.EnqueueRequestsFromMapFunc(r.enqueueNodePoolsForConfig)).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
		}).
		Build(r)
	if err != nil {
		return errors.Wrap(err, "failed setting up with a controller manager")
	}

	r.recorder = mgr.GetEventRecorderFor("nodepool-controller")
	r.tracer = otel.Tracer("nodepool-controller")

	return nil
}

func (r *NodePoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx = baggage.ContextWithValues(ctx,
		attribute.String("request", req.String()),
	)
	var span trace.Span
	ctx, span = r.tracer.Start(ctx, "reconcile")
	defer span.End()

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
		log.Error(err, "error getting nodePool")
		return ctrl.Result{}, err
	}

	hcluster, err := GetHostedClusterByName(ctx, r.Client, nodePool.GetNamespace(), nodePool.Spec.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name).Name

	md := machineDeployment(nodePool, hcluster.Spec.InfraID, controlPlaneNamespace)
	mhc := machineHealthCheck(nodePool, controlPlaneNamespace)

	if !nodePool.DeletionTimestamp.IsZero() {
		span.AddEvent("Deleting nodePool")
		awsMachineTemplates, err := r.listAWSMachineTemplates(nodePool)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to list AWSMachineTemplates: %w", err)
		}
		for k := range awsMachineTemplates {
			if err := r.Delete(ctx, &awsMachineTemplates[k]); err != nil && !apierrors.IsNotFound(err) {
				return reconcile.Result{}, fmt.Errorf("failed to delete AWSMachineTemplate: %w", err)
			}
		}

		// Delete any secret belonging to this NodePool i.e token Secret and userdata Secret.
		secrets, err := r.listSecrets(nodePool)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to list secrets: %w", err)
		}
		for k := range secrets {
			if err := r.Delete(ctx, &secrets[k]); err != nil && !apierrors.IsNotFound(err) {
				return reconcile.Result{}, fmt.Errorf("failed to delete secret: %w", err)
			}
		}

		if err := r.Delete(ctx, md); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, fmt.Errorf("failed to delete MachineDeployment: %w", err)
		}

		if err := r.Client.Delete(ctx, mhc); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete MachineHealthCheck: %w", err)
		}

		if controllerutil.ContainsFinalizer(nodePool, finalizer) {
			controllerutil.RemoveFinalizer(nodePool, finalizer)
			if err := r.Update(ctx, nodePool); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from NodePool: %w", err)
			}
		}
		log.Info("Deleted nodePool")
		span.AddEvent("Finished deleting nodePool")
		return ctrl.Result{}, nil
	}

	// Ensure the nodePool has a finalizer for cleanup
	if !controllerutil.ContainsFinalizer(nodePool, finalizer) {
		controllerutil.AddFinalizer(nodePool, finalizer)
		if err := r.Update(ctx, nodePool); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to nodePool: %w", err)
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

	span.AddEvent("Updating nodePool")
	if err := patchHelper.Patch(ctx, nodePool); err != nil {
		log.Error(err, "failed to patch")
		return ctrl.Result{}, fmt.Errorf("failed to patch: %w", err)
	}

	log.Info("Successfully reconciled")
	return ctrl.Result{}, nil
}

func (r *NodePoolReconciler) reconcile(ctx context.Context, hcluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	var span trace.Span
	ctx, span = r.tracer.Start(ctx, "update")
	defer span.End()

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

	// 1. - Reconcile conditions according to current state of the world.

	// Validate autoscaling input.
	if err := validateAutoscaling(nodePool); err != nil {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:               hyperv1.NodePoolAutoscalingEnabledConditionType,
			Status:             metav1.ConditionFalse,
			Message:            err.Error(),
			Reason:             hyperv1.NodePoolValidationFailedConditionReason,
			ObservedGeneration: nodePool.Generation,
		})
		// We don't return the error here as reconciling won't solve the input problem.
		// An update event will trigger reconciliation.
		log.Error(err, "validating autoscaling parameters failed")
		return reconcile.Result{}, nil
	}
	if isAutoscalingEnabled(nodePool) {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:               hyperv1.NodePoolAutoscalingEnabledConditionType,
			Status:             metav1.ConditionTrue,
			Reason:             hyperv1.NodePoolAsExpectedConditionReason,
			Message:            fmt.Sprintf("Maximum nodes: %v, Minimum nodes: %v", nodePool.Spec.AutoScaling.Max, nodePool.Spec.AutoScaling.Min),
			ObservedGeneration: nodePool.Generation,
		})
	} else {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:               hyperv1.NodePoolAutoscalingEnabledConditionType,
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.NodePoolAsExpectedConditionReason,
			ObservedGeneration: nodePool.Generation,
		})
	}

	// Validate management input.
	if err := validateManagement(nodePool); err != nil {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:               hyperv1.NodePoolUpdateManagementEnabledConditionType,
			Status:             metav1.ConditionFalse,
			Message:            err.Error(),
			Reason:             hyperv1.NodePoolValidationFailedConditionReason,
			ObservedGeneration: nodePool.Generation,
		})
		// We don't return the error here as reconciling won't solve the input problem.
		// An update event will trigger reconciliation.
		log.Error(err, "validating management parameters failed")
		return reconcile.Result{}, nil
	}
	meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
		Type:               hyperv1.NodePoolUpdateManagementEnabledConditionType,
		Status:             metav1.ConditionTrue,
		Reason:             hyperv1.NodePoolAsExpectedConditionReason,
		ObservedGeneration: nodePool.Generation,
	})

	// Validate IgnitionEndpoint.
	if ignEndpoint == "" {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.IgnitionEndpointAvailable),
			Status:             metav1.ConditionFalse,
			Message:            "Ignition endpoint not available, waiting",
			Reason:             hyperv1.IgnitionEndpointMissingReason,
			ObservedGeneration: nodePool.Generation,
		})
		log.Info("Ignition endpoint not available, waiting")
		return reconcile.Result{}, nil
	}
	RemoveStatusCondition(&nodePool.Status.Conditions, string(hyperv1.IgnitionEndpointAvailable))

	// Validate Ignition CA Secret.
	caSecret := ignitionserver.IgnitionCACertSecret(controlPlaneNamespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(caSecret), caSecret); err != nil {
		if apierrors.IsNotFound(err) {
			meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
				Type:               string(hyperv1.IgnitionEndpointAvailable),
				Status:             metav1.ConditionFalse,
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
	RemoveStatusCondition(&nodePool.Status.Conditions, string(hyperv1.IgnitionEndpointAvailable))

	caCertBytes, hasCACert := caSecret.Data[corev1.TLSCertKey]
	if !hasCACert {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.IgnitionEndpointAvailable),
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.IgnitionCACertMissingReason,
			Message:            "CA Secret is missing tls.crt key",
			ObservedGeneration: nodePool.Generation,
		})
		log.Info("CA Secret is missing tls.crt key")
		return ctrl.Result{}, nil
	}
	RemoveStatusCondition(&nodePool.Status.Conditions, hyperv1.IgnitionCACertMissingReason)

	// Validate and get releaseImage.
	releaseImage, err := r.getReleaseImage(ctx, hcluster, nodePool.Spec.Release.Image)
	if err != nil {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:               hyperv1.NodePoolValidReleaseImageConditionType,
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.NodePoolValidationFailedConditionReason,
			Message:            fmt.Sprintf("Failed to get release image: %v", err.Error()),
			ObservedGeneration: nodePool.Generation,
		})
		return ctrl.Result{}, fmt.Errorf("failed to look up release image metadata: %w", err)
	}
	meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
		Type:               hyperv1.NodePoolValidReleaseImageConditionType,
		Status:             metav1.ConditionTrue,
		Reason:             hyperv1.NodePoolAsExpectedConditionReason,
		Message:            fmt.Sprintf("Using release image: %s", nodePool.Spec.Release.Image),
		ObservedGeneration: nodePool.Generation,
	})

	// Validate platform specific input.
	var ami string
	if nodePool.Spec.Platform.Type == hyperv1.AWSPlatform {
		if hcluster.Spec.Platform.AWS == nil {
			return ctrl.Result{}, fmt.Errorf("the HostedCluster for this NodePool has no .Spec.Platform.AWS, this is unsupported")
		}
		// TODO: Should the region be included in the NodePool platform information?
		ami, err = getAMI(nodePool, hcluster.Spec.Platform.AWS.Region, releaseImage)
		if err != nil {
			meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
				Type:               hyperv1.NodePoolValidAMIConditionType,
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.NodePoolValidationFailedConditionReason,
				Message:            fmt.Sprintf("Couldn't discover an AMI for release image %q: %s", nodePool.Spec.Release.Image, err.Error()),
				ObservedGeneration: nodePool.Generation,
			})
			return ctrl.Result{}, fmt.Errorf("couldn't discover an AMI for release image: %w", err)
		}
	}
	meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
		Type:               hyperv1.NodePoolValidAMIConditionType,
		Status:             metav1.ConditionTrue,
		Reason:             hyperv1.NodePoolAsExpectedConditionReason,
		Message:            fmt.Sprintf("Bootstrap AMI is %q", ami),
		ObservedGeneration: nodePool.Generation,
	})

	// Validate config input.
	// 3 generic core config resoures: fips, ssh and haproxy.
	// TODO (alberto): consider moving the expectedCoreConfigResources check
	// into the token Secret controller so we don't block Machine infra creation on this.
	expectedCoreConfigResources := 3
	if len(hcluster.Spec.ImageContentSources) > 0 {
		// additional core config resource created when image content source specified.
		expectedCoreConfigResources += 1
	}
	config, missingConfigs, err := r.getConfig(ctx, nodePool, expectedCoreConfigResources, controlPlaneNamespace)
	if err != nil {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:               hyperv1.NodePoolConfigValidConfigConditionType,
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.NodePoolValidationFailedConditionReason,
			Message:            err.Error(),
			ObservedGeneration: nodePool.Generation,
		})
		return ctrl.Result{}, fmt.Errorf("failed to get config: %w", err)
	}
	if missingConfigs {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:               hyperv1.NodePoolConfigValidConfigConditionType,
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.NodePoolValidationFailedConditionReason,
			Message:            "Core ignition config has not been created yet",
			ObservedGeneration: nodePool.Generation,
		})
		// We watch configmaps so we will get an event when these get created
		return ctrl.Result{}, nil
	}
	meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
		Type:               hyperv1.NodePoolConfigValidConfigConditionType,
		Status:             metav1.ConditionTrue,
		Reason:             hyperv1.NodePoolAsExpectedConditionReason,
		ObservedGeneration: nodePool.Generation,
	})

	// Check if config needs to be updated.
	targetConfigHash := hashStruct(config)
	isUpdatingConfig := isUpdatingConfig(nodePool, targetConfigHash)
	if isUpdatingConfig {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:               hyperv1.NodePoolUpdatingConfigConditionType,
			Status:             metav1.ConditionTrue,
			Reason:             hyperv1.NodePoolAsExpectedConditionReason,
			Message:            fmt.Sprintf("Updating config in progress. Target config: %s", targetConfigHash),
			ObservedGeneration: nodePool.Generation,
		})
		log.Info("NodePool config is updating",
			"current", nodePool.GetAnnotations()[nodePoolAnnotationCurrentConfig],
			"target", targetConfigHash)
	} else {
		RemoveStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolUpdatingConfigConditionType)
	}

	// Check if version needs to be updated.
	targetVersion := releaseImage.Version()
	isUpdatingVersion := isUpdatingVersion(nodePool, targetVersion)
	if isUpdatingVersion {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:               hyperv1.NodePoolUpdatingVersionConditionType,
			Status:             metav1.ConditionTrue,
			Reason:             hyperv1.NodePoolAsExpectedConditionReason,
			Message:            fmt.Sprintf("Updating version in progress. Target version: %s", targetVersion),
			ObservedGeneration: nodePool.Generation,
		})
		log.Info("NodePool version is updating",
			"current", nodePool.Status.Version, "target", targetVersion)
	} else {
		RemoveStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolUpdatingVersionConditionType)
	}

	// 2. - Reconcile towards expected state of the world.
	targetConfigVersionHash := hashStruct(config + targetVersion)
	compressedConfig, err := compress([]byte(config))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to compress config: %w", err)
	}

	// Token Secrets are immutable and follow "prefixName-configVersionHash" naming convention.
	// Ensure old configVersionHash resources are deleted, i.e token Secret and userdata Secret.
	if isUpdatingVersion || isUpdatingConfig {
		tokenSecret := TokenSecret(controlPlaneNamespace, nodePool.Name, nodePool.GetAnnotations()[nodePoolAnnotationCurrentConfigVersion])
		if err := r.Delete(ctx, tokenSecret); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, fmt.Errorf("failed to delete token Secret: %w", err)
		}

		userDataSecret := IgnitionUserDataSecret(controlPlaneNamespace, nodePool.GetName(), nodePool.GetAnnotations()[nodePoolAnnotationCurrentConfigVersion])
		if err := r.Delete(ctx, userDataSecret); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, fmt.Errorf("failed to delete token Secret: %w", err)
		}
	}

	tokenSecret := TokenSecret(controlPlaneNamespace, nodePool.Name, targetConfigVersionHash)
	if result, err := r.CreateOrUpdate(ctx, r.Client, tokenSecret, func() error {
		return reconcileTokenSecret(tokenSecret, nodePool, compressedConfig)
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile token Secret: %w", err)
	} else {
		log.Info("Reconciled token Secret", "result", result)
		span.AddEvent("reconciled token Secret", trace.WithAttributes(attribute.String("result", string(result))))
	}

	tokenBytes, hasToken := tokenSecret.Data[TokenSecretTokenKey]
	if !hasToken {
		// This should never happen by design.
		return ctrl.Result{}, fmt.Errorf("token secret is missing token key")
	}

	userDataSecret := IgnitionUserDataSecret(controlPlaneNamespace, nodePool.GetName(), targetConfigVersionHash)
	if result, err := r.CreateOrUpdate(ctx, r.Client, userDataSecret, func() error {
		return reconcileUserDataSecret(userDataSecret, nodePool, caCertBytes, tokenBytes, ignEndpoint)
	}); err != nil {
		return ctrl.Result{}, err
	} else {
		log.Info("Reconciled userData Secret", "result", result)
		span.AddEvent("reconciled ignition user data secret", trace.WithAttributes(attribute.String("result", string(result))))
	}

	var machineTemplate client.Object
	switch nodePool.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		machineTemplate, err = r.reconcileAWSMachineTemplate(ctx, hcluster, nodePool, infraID, ami, controlPlaneNamespace)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile AWSMachineTemplate: %w", err)
		}
		span.AddEvent("reconciled awsmachinetemplate", trace.WithAttributes(attribute.String("name", machineTemplate.GetName())))
	case hyperv1.NonePlatform:
		// TODO: When fleshing out platform None design revisit the right semantic to signal this as conditions in a NodePool.
		return ctrl.Result{}, nil
	}

	md := machineDeployment(nodePool, infraID, controlPlaneNamespace)
	if result, err := controllerutil.CreateOrPatch(ctx, r.Client, md, func() error {
		return r.reconcileMachineDeployment(
			log,
			md, nodePool,
			userDataSecret,
			machineTemplate,
			infraID,
			targetVersion, targetConfigHash, targetConfigVersionHash)
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile MachineDeployment %q: %w",
			client.ObjectKeyFromObject(md).String(), err)
	} else {
		log.Info("Reconciled MachineDeployment", "result", result)
		span.AddEvent("reconciled machinedeployment", trace.WithAttributes(attribute.String("result", string(result))))
	}

	mhc := machineHealthCheck(nodePool, controlPlaneNamespace)
	if nodePool.Spec.Management.AutoRepair {
		if result, err := ctrl.CreateOrUpdate(ctx, r.Client, mhc, func() error {
			return r.reconcileMachineHealthCheck(mhc, nodePool, infraID)
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile MachineHealthCheck %q: %w",
				client.ObjectKeyFromObject(mhc).String(), err)
		} else {
			log.Info("Reconciled MachineHealthCheck", "result", result)
			span.AddEvent("reconciled machinehealthchecks", trace.WithAttributes(attribute.String("result", string(result))))
		}
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:               hyperv1.NodePoolAutorepairEnabledConditionType,
			Status:             metav1.ConditionTrue,
			Reason:             hyperv1.NodePoolAsExpectedConditionReason,
			ObservedGeneration: nodePool.Generation,
		})
	} else {
		if err := r.Client.Delete(ctx, mhc); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		} else {
			span.AddEvent("deleted machinehealthcheck", trace.WithAttributes(attribute.String("name", mhc.Name)))
		}
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:               hyperv1.NodePoolAutorepairEnabledConditionType,
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.NodePoolAsExpectedConditionReason,
			ObservedGeneration: nodePool.Generation,
		})
	}

	return ctrl.Result{}, nil
}

func (r NodePoolReconciler) reconcileAWSMachineTemplate(ctx context.Context,
	hostedCluster *hyperv1.HostedCluster,
	nodePool *hyperv1.NodePool,
	infraID string,
	ami string,
	controlPlaneNamespace string,
) (*capiaws.AWSMachineTemplate, error) {

	log := ctrl.LoggerFrom(ctx)
	// Get target template and hash.
	targetAWSMachineTemplate, targetTemplateHash := AWSMachineTemplate(infraID, ami, hostedCluster, nodePool, controlPlaneNamespace)

	// Get current template and hash.
	currentTemplateHash := nodePool.GetAnnotations()[nodePoolAnnotationCurrentProviderConfig]
	currentAWSMachineTemplate := &capiaws.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", nodePool.GetName(), currentTemplateHash),
			Namespace: controlPlaneNamespace,
		},
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(currentAWSMachineTemplate), currentAWSMachineTemplate); err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("error getting existing AWSMachineTemplate: %w", err)
	}

	// Template has not changed, return early.
	// TODO(alberto): can we hash in a deterministic way so we could just compare hashes?
	if equality.Semantic.DeepEqual(currentAWSMachineTemplate.Spec.Template.Spec, targetAWSMachineTemplate.Spec.Template.Spec) {
		return currentAWSMachineTemplate, nil
	}

	// Otherwise create new template.
	log.Info("The AWSMachineTemplate referenced by this NodePool has changed. Creating a new one")
	if err := r.Create(ctx, targetAWSMachineTemplate); err != nil {
		return nil, fmt.Errorf("error creating new AWSMachineTemplate: %w", err)
	}

	// TODO (alberto): Create a mechanism to cleanup old machineTemplates.
	// We can't just delete the old AWSMachineTemplate because
	// this would break the rolling upgrade process since the MachineSet
	// being scaled down is still referencing the old AWSMachineTemplate.
	// May be consider one single template the whole NodePool lifecycle. Modify it in place
	// and trigger rolling update by e.g annotating the machineDeployment.

	// Store new template hash.
	if nodePool.Annotations == nil {
		nodePool.Annotations = make(map[string]string)
	}
	nodePool.Annotations[nodePoolAnnotationCurrentProviderConfig] = targetTemplateHash

	return targetAWSMachineTemplate, nil
}

func reconcileUserDataSecret(userDataSecret *corev1.Secret, nodePool *hyperv1.NodePool, CA, token []byte, ignEndpoint string) error {
	// The token secret controller deletes expired token Secrets.
	// When that happens the NodePool controller reconciles and create a new one.
	// Then it reconciles the userData Secret with the new generated token.
	// Therefore this secret is mutable.
	userDataSecret.Immutable = k8sutilspointer.BoolPtr(false)

	if userDataSecret.Annotations == nil {
		userDataSecret.Annotations = make(map[string]string)
	}
	userDataSecret.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()

	encodedCACert := base64.StdEncoding.EncodeToString(CA)
	encodedToken := base64.StdEncoding.EncodeToString(token)
	ignConfig := ignConfig(encodedCACert, encodedToken, ignEndpoint)
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

func reconcileTokenSecret(tokenSecret *corev1.Secret, nodePool *hyperv1.NodePool, compressedConfig []byte) error {
	tokenSecret.Immutable = k8sutilspointer.BoolPtr(true)
	if tokenSecret.Annotations == nil {
		tokenSecret.Annotations = make(map[string]string)
	}

	tokenSecret.Annotations[TokenSecretAnnotation] = "true"
	tokenSecret.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()

	if tokenSecret.Data == nil {
		tokenSecret.Data = map[string][]byte{}
		tokenSecret.Data[TokenSecretTokenKey] = []byte(uuid.New().String())
		tokenSecret.Data[TokenSecretReleaseKey] = []byte(nodePool.Spec.Release.Image)
		tokenSecret.Data[TokenSecretConfigKey] = compressedConfig
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
	targetConfigHash, targetConfigVersionHash string) error {

	// Set annotations and labels
	if machineDeployment.GetAnnotations() == nil {
		machineDeployment.Annotations = map[string]string{}
	}
	machineDeployment.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
	if machineDeployment.GetLabels() == nil {
		machineDeployment.Labels = map[string]string{}
	}
	machineDeployment.Labels[capiv1.ClusterLabelName] = CAPIClusterName

	resourcesName := generateName(CAPIClusterName, nodePool.Spec.ClusterName, nodePool.GetName())
	machineDeployment.Spec.MinReadySeconds = k8sutilspointer.Int32Ptr(int32(0))

	gvk, err := apiutil.GVKForObject(machineTemplateCR, api.Scheme)
	if err != nil {
		return err
	}

	// Set selector and template
	machineDeployment.Spec.ClusterName = CAPIClusterName
	if machineDeployment.Spec.Selector.MatchLabels == nil {
		machineDeployment.Spec.Selector.MatchLabels = map[string]string{}
	}
	machineDeployment.Spec.Selector.MatchLabels[resourcesName] = resourcesName
	machineDeployment.Spec.Template = capiv1.MachineTemplateSpec{
		ObjectMeta: capiv1.ObjectMeta{
			Labels: map[string]string{
				resourcesName:           resourcesName,
				capiv1.ClusterLabelName: CAPIClusterName,
			},
			// TODO (alberto): drop/expose this annotation at the nodePool API
			Annotations: map[string]string{
				"machine.cluster.x-k8s.io/exclude-node-draining": "true",
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
			Version: machineDeployment.Spec.Template.Spec.Version,
		},
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

	// Propagate version and userData Secret to the machineDeployment.
	if userDataSecret.Name != k8sutilspointer.StringPtrDerefOr(machineDeployment.Spec.Template.Spec.Bootstrap.DataSecretName, "") {
		log.Info("New user data Secret has been generated",
			"current", machineDeployment.Spec.Template.Spec.Bootstrap.DataSecretName,
			"target", userDataSecret.Name)

		if targetVersion != k8sutilspointer.StringPtrDerefOr(machineDeployment.Spec.Template.Spec.Version, "") {
			log.Info("Starting version update: Propagating new version to the MachineDeployment",
				"releaseImage", nodePool.Spec.Release.Image, "target", targetVersion)
		}

		if targetConfigHash != nodePool.Annotations[nodePoolAnnotationCurrentConfig] {
			log.Info("Starting config update: Propagating new config to the MachineDeployment",
				"current", nodePool.Annotations[nodePoolAnnotationCurrentConfig], "target", targetConfigHash)
		}
		machineDeployment.Spec.Template.Spec.Version = &targetVersion
		machineDeployment.Spec.Template.Spec.Bootstrap.DataSecretName = k8sutilspointer.StringPtr(userDataSecret.Name)

		// We return early here during a version/config update to persist the resource with new user data Secret,
		// so in the next reconciling loop we get a new MachineDeployment.Generation
		// and we can do a legit MachineDeploymentComplete/MachineDeployment.Status.ObservedGeneration check.
		// Before persisting, if the NodePool is brand new we want to make sure the replica number is set so the machineDeployment controller
		// does not panic.
		if machineDeployment.Spec.Replicas == nil {
			machineDeployment.Spec.Replicas = k8sutilspointer.Int32Ptr(k8sutilspointer.Int32PtrDerefOr(nodePool.Spec.NodeCount, 0))
		}
		return nil
	}

	// If the MachineDeployment is no processing we know
	// is at the expected version (spec.version) and config (userData Secret) so we reconcile status and annotation.
	if MachineDeploymentComplete(machineDeployment) {
		if nodePool.Status.Version != targetVersion {
			log.Info("Version update complete",
				"previous", nodePool.Status.Version, "new", targetVersion)
			nodePool.Status.Version = targetVersion
		}

		if nodePool.Annotations[nodePoolAnnotationCurrentConfig] != targetConfigHash {
			log.Info("Config update complete",
				"previous", nodePool.Annotations[nodePoolAnnotationCurrentConfig], "new", targetConfigHash)
			nodePool.Annotations[nodePoolAnnotationCurrentConfig] = targetConfigHash
		}
		nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion] = targetConfigVersionHash
	}

	setMachineDeploymentReplicas(nodePool, machineDeployment)

	nodePool.Status.NodeCount = machineDeployment.Status.AvailableReplicas
	return nil
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
			Duration: 10 * time.Minute,
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
		if k8sutilspointer.Int32PtrDerefOr(machineDeployment.Spec.Replicas, 0) == 0 {
			// if autoscaling is enabled and the machineDeployment does not exist yet or it has 0 replicas
			// we set it to 1 replica as the autoscaler does not support scaling from zero yet.
			machineDeployment.Spec.Replicas = k8sutilspointer.Int32Ptr(int32(1))
		}
		machineDeployment.Annotations[autoscalerMaxAnnotation] = strconv.Itoa(int(nodePool.Spec.AutoScaling.Max))
		machineDeployment.Annotations[autoscalerMinAnnotation] = strconv.Itoa(int(nodePool.Spec.AutoScaling.Min))
	}

	// If autoscaling is NOT enabled we reset min/max annotations and reconcile replicas.
	if !isAutoscalingEnabled(nodePool) {
		machineDeployment.Annotations[autoscalerMaxAnnotation] = "0"
		machineDeployment.Annotations[autoscalerMinAnnotation] = "0"
		machineDeployment.Spec.Replicas = k8sutilspointer.Int32Ptr(k8sutilspointer.Int32PtrDerefOr(nodePool.Spec.NodeCount, 0))
	}
}

func getAMI(nodePool *hyperv1.NodePool, region string, releaseImage *releaseinfo.ReleaseImage) (string, error) {
	if nodePool.Spec.Platform.AWS.AMI != "" {
		return nodePool.Spec.Platform.AWS.AMI, nil
	}

	return defaultNodePoolAMI(region, releaseImage)
}

func ignConfig(encodedCACert, encodedToken, endpoint string) ignitionapi.Config {
	return ignitionapi.Config{
		Ignition: ignitionapi.Ignition{
			Version: "3.2.0",
			Security: ignitionapi.Security{
				TLS: ignitionapi.TLS{
					CertificateAuthorities: []ignitionapi.Resource{
						{
							Source: k8sutilspointer.StringPtr(fmt.Sprintf("data:text/plain;base64,%s", encodedCACert)),
						},
					},
				},
			},
			Config: ignitionapi.IgnitionConfig{
				Merge: []ignitionapi.Resource{
					{
						Source: k8sutilspointer.StringPtr(fmt.Sprintf("https://%s/ignition", endpoint)),
						HTTPHeaders: []ignitionapi.HTTPHeader{
							{
								Name:  "Authorization",
								Value: k8sutilspointer.StringPtr(fmt.Sprintf("Bearer %s", encodedToken)),
							},
						},
					},
				},
			},
		},
	}
}

func (r *NodePoolReconciler) getConfig(ctx context.Context, nodePool *hyperv1.NodePool, expectedCoreConfigResources int, controlPlaneResource string) (configsRaw string, missingConfigs bool, err error) {
	var configs []corev1.ConfigMap
	var allConfigPlainText []string
	var errors []error

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

	for _, config := range configs {
		manifest := config.Data[TokenSecretConfigKey]
		if err := validateConfigManifest([]byte(manifest)); err != nil {
			errors = append(errors, fmt.Errorf("configmap %q failed validation: %w", config.Name, err))
			continue
		}

		allConfigPlainText = append(allConfigPlainText, manifest)
	}

	// These configs are the input to a hash func whose output is used as part of the name of the user-data secret,
	// so our output must be deterministic.
	sort.Strings(allConfigPlainText)

	return strings.Join(allConfigPlainText, "\n---\n"), missingConfigs, utilerrors.NewAggregate(errors)
}

// validateManagement does additional backend validation. API validation/default should
// prevent this from ever fail.
func validateManagement(nodePool *hyperv1.NodePool) error {
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
func validateConfigManifest(manifest []byte) error {
	scheme := runtime.NewScheme()
	mcfgv1.Install(scheme)
	v1alpha1.Install(scheme)

	YamlSerializer := serializer.NewSerializerWithOptions(
		serializer.DefaultMetaFactory, scheme, scheme,
		serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)

	cr, _, err := YamlSerializer.Decode(manifest, nil, nil)
	if err != nil {
		return fmt.Errorf("error decoding config: %w", err)
	}

	switch obj := cr.(type) {
	case *mcfgv1.MachineConfig:
	case *v1alpha1.ImageContentSourcePolicy:
	//	TODO (alberto): enable this kinds when they are supported in mcs bootstrap mode
	// since our mcsIgnitionProvider implementation uses bootstrap mode to render the ignition payload out of an input.
	// https://github.com/openshift/machine-config-operator/pull/2547
	//case *mcfgv1.KubeletConfig:
	//case *mcfgv1.ContainerRuntimeConfig:
	default:
		return fmt.Errorf("unsupported config type: %T", obj)
	}

	return nil
}

func (r *NodePoolReconciler) getReleaseImage(ctx context.Context, hostedCluster *hyperv1.HostedCluster, releaseImage string) (*releaseinfo.ReleaseImage, error) {
	pullSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hostedCluster.Namespace, Name: hostedCluster.Spec.PullSecret.Name}, pullSecret); err != nil {
		return nil, fmt.Errorf("cannot get pull secret %s/%s: %w", hostedCluster.Namespace, hostedCluster.Spec.PullSecret.Name, err)
	}
	if _, hasKey := pullSecret.Data[corev1.DockerConfigJsonKey]; !hasKey {
		return nil, fmt.Errorf("pull secret %s/%s missing %q key", pullSecret.Namespace, pullSecret.Name, corev1.DockerConfigJsonKey)
	}
	ReleaseImage, err := func(ctx context.Context) (*releaseinfo.ReleaseImage, error) {
		ctx, span := r.tracer.Start(ctx, "image-lookup")
		defer span.End()
		lookupCtx, lookupCancel := context.WithTimeout(ctx, 1*time.Minute)
		defer lookupCancel()
		img, err := r.ReleaseProvider.Lookup(lookupCtx, releaseImage, pullSecret.Data[corev1.DockerConfigJsonKey])
		if err != nil {
			return nil, fmt.Errorf("failed to look up release image metadata: %w", err)
		}
		return img, nil
	}(ctx)
	return ReleaseImage, err
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
	if nodePool.Spec.NodeCount != nil && nodePool.Spec.AutoScaling != nil {
		return fmt.Errorf("only one of nodePool.Spec.NodeCount or nodePool.Spec.AutoScaling can be set")
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

	// Otherwise reconcile NodePools which are referencing the given ConfigMap.
	for key := range nodePoolList.Items {
		for _, v := range nodePoolList.Items[key].Spec.Config {
			if v.Name == cm.Name {
				result = append(result,
					reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&nodePoolList.Items[key])},
				)
				break
			}
		}
	}

	return result
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
		{NamespacedName: hyperutil.ParseNamespacedName(nodePoolName)},
	}
}

func (r *NodePoolReconciler) listSecrets(nodePool *hyperv1.NodePool) ([]corev1.Secret, error) {
	secretList := &corev1.SecretList{}
	if err := r.List(context.Background(), secretList); err != nil {
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

func (r *NodePoolReconciler) listAWSMachineTemplates(nodePool *hyperv1.NodePool) ([]capiaws.AWSMachineTemplate, error) {
	awsMachineTemplateList := &capiaws.AWSMachineTemplateList{}
	if err := r.List(context.Background(), awsMachineTemplateList); err != nil {
		return nil, fmt.Errorf("failed to list AWSMachineTemplates: %w", err)
	}
	filtered := []capiaws.AWSMachineTemplate{}
	for i, AWSMachineTemplate := range awsMachineTemplateList.Items {
		if AWSMachineTemplate.GetAnnotations() != nil {
			if annotation, ok := AWSMachineTemplate.GetAnnotations()[nodePoolAnnotation]; ok &&
				annotation == client.ObjectKeyFromObject(nodePool).String() {
				filtered = append(filtered, awsMachineTemplateList.Items[i])
			}
		}
	}
	return filtered, nil
}

func compress(content []byte) ([]byte, error) {
	if len(content) == 0 {
		return nil, nil
	}
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	if _, err := gz.Write(content); err != nil {
		return nil, fmt.Errorf("failed to compress content: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("compress closure failure %w", err)
	}
	return b.Bytes(), nil
}

func hashStruct(o interface{}) string {
	hash := fnv.New32a()
	hash.Write([]byte(fmt.Sprintf("%v", o)))
	intHash := hash.Sum32()
	return fmt.Sprintf("%08x", intHash)
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
	if baseLength < 0 {
		prefix := base[0:min(len(base), max(0, maxLength-9))]
		// Calculate hash on initial base-suffix string
		shortName := fmt.Sprintf("%s-%s", prefix, hashStruct(name))
		return shortName[:min(maxLength, len(shortName))]
	}

	prefix := base[0:baseLength]
	// Calculate hash on initial base-suffix string
	return fmt.Sprintf("%s-%s-%s", prefix, hashStruct(base), suffix)
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

// TODO(alberto): user core apimachinery func once we update deps to fetch
// https://github.com/kubernetes/apimachinery/commit/f0c829d684eec47c6334ae69941224e1c70f16d6#diff-e3bc08d3d017ceff366d1c1c6f16c745e576e41f711a482a08f97006d306bf2a
// RemoveStatusCondition removes the corresponding conditionType from conditions.
// conditions must be non-nil.
func RemoveStatusCondition(conditions *[]metav1.Condition, conditionType string) {
	if conditions == nil || len(*conditions) == 0 {
		return
	}

	newConditions := make([]metav1.Condition, 0, len(*conditions)-1)
	for _, condition := range *conditions {
		if condition.Type != conditionType {
			newConditions = append(newConditions, condition)
		}
	}

	*conditions = newConditions
}
