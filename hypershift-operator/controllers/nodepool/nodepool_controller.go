package nodepool

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strconv"
	"time"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_1/types"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/releaseinfo"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	hyperutil "github.com/openshift/hypershift/hypershift-operator/controllers/util"
	capiv1 "github.com/openshift/hypershift/thirdparty/clusterapi/api/v1alpha4"
	"github.com/openshift/hypershift/thirdparty/clusterapi/util"
	"github.com/openshift/hypershift/thirdparty/clusterapi/util/patch"
	capiaws "github.com/openshift/hypershift/thirdparty/clusterapiprovideraws/v1alpha4"
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
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	k8sutilspointer "k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	finalizer                       = "hypershift.openshift.io/finalizer"
	autoscalerMaxAnnotation         = "cluster.x-k8s.io/cluster-api-autoscaler-node-group-max-size"
	autoscalerMinAnnotation         = "cluster.x-k8s.io/cluster-api-autoscaler-node-group-min-size"
	nodePoolAnnotation              = "hypershift.openshift.io/nodePool"
	nodePoolAnnotationConfig        = "hypershift.openshift.io/nodePoolConfig"
	nodePoolAnnotationConfigVersion = "hypershift.openshift.io/nodePoolConfigVersion"
	TokenSecretReleaseKey           = "release"
	TokenSecretTokenKey             = "token"
	TokenSecretConfigKey            = "config"
	TokenSecretAnnotation           = "hypershift.openshift.io/ignition-config"
)

type NodePoolReconciler struct {
	ctrlclient.Client
	recorder        record.EventRecorder
	Log             logr.Logger
	ReleaseProvider releaseinfo.Provider

	tracer trace.Tracer
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
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
		}).
		// TODO (alberto): Let ConfigMaps referenced by the spec.config and also the core ones to trigger reconciliation.
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

	r.Log = ctrl.LoggerFrom(ctx)
	r.Log.Info("Reconciling")

	// Fetch the nodePool instance
	nodePool := &hyperv1.NodePool{}
	err := r.Client.Get(ctx, req.NamespacedName, nodePool)
	if err != nil {
		if apierrors.IsNotFound(err) {
			r.Log.Info("not found", "request", req.String())
			return ctrl.Result{}, nil
		}
		r.Log.Error(err, "error getting nodePool")
		return ctrl.Result{}, err
	}

	hcluster, err := GetHostedClusterByName(ctx, r.Client, nodePool.GetNamespace(), nodePool.Spec.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Generate mcs manifests for the given release
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name).Name
	// Fixme (alberto): using nodePool.Status.Version here gives a race: if a NodePool is deleted
	// before the targeted version is the one in the status then the tokenSecret and userDataSecret would be leaked.
	tokenSecret := TokenSecret(controlPlaneNamespace, nodePool.Name, nodePool.GetAnnotations()[nodePoolAnnotationConfigVersion])
	userDataSecret := IgnitionUserDataSecret(controlPlaneNamespace, nodePool.GetName(), nodePool.GetAnnotations()[nodePoolAnnotationConfigVersion])
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

		if err := r.Delete(ctx, tokenSecret); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, fmt.Errorf("failed to delete token Secret: %w", err)
		}

		if err := r.Delete(ctx, md); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, fmt.Errorf("failed to delete MachineDeployment: %w", err)
		}

		if err := r.Delete(ctx, userDataSecret); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete ignition userdata Secret: %w", err)
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
		r.Log.Info("Deleted nodePool")
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
		r.Log.Error(err, "Failed to reconcile nodePool")
		r.recorder.Eventf(nodePool, corev1.EventTypeWarning, "ReconcileError", "%v", err)
		if err := patchHelper.Patch(ctx, nodePool); err != nil {
			r.Log.Error(err, "failed to patch")
			return ctrl.Result{}, fmt.Errorf("failed to patch: %w", err)
		}
		return result, err
	}

	span.AddEvent("Updating nodePool")
	if err := patchHelper.Patch(ctx, nodePool); err != nil {
		r.Log.Error(err, "failed to patch")
		return ctrl.Result{}, fmt.Errorf("failed to patch: %w", err)
	}

	r.Log.Info("Successfully reconciled")
	return ctrl.Result{}, nil
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

func (r *NodePoolReconciler) reconcile(ctx context.Context, hcluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool) (ctrl.Result, error) {
	var span trace.Span
	ctx, span = r.tracer.Start(ctx, "update")
	defer span.End()

	nodePool.OwnerReferences = util.EnsureOwnerRef(nodePool.OwnerReferences, metav1.OwnerReference{
		APIVersion: hyperv1.GroupVersion.String(),
		Kind:       "HostedCluster",
		Name:       hcluster.Name,
		UID:        hcluster.UID,
	})

	// Validate input
	if err := validate(nodePool); err != nil {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:    hyperv1.NodePoolAutoscalingEnabledConditionType,
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.NodePoolValidationFailedConditionReason,
			Message: err.Error(),
		})
		return reconcile.Result{}, fmt.Errorf("error validating autoscaling parameters: %w", err)
	}

	if hcluster.Status.IgnitionEndpoint == "" {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.IgnitionEndpointAvailable),
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.IgnitionEndpointMissingReason,
			ObservedGeneration: nodePool.Generation,
		})
		r.Log.Info("Ignition endpoint not available, waiting")
		return reconcile.Result{}, nil
	}

	releaseImage, err := func(ctx context.Context) (*releaseinfo.ReleaseImage, error) {
		ctx, span := r.tracer.Start(ctx, "image-lookup")
		defer span.End()
		lookupCtx, lookupCancel := context.WithTimeout(ctx, 1*time.Minute)
		defer lookupCancel()
		img, err := r.ReleaseProvider.Lookup(lookupCtx, nodePool.Spec.Release.Image)
		if err != nil {
			return nil, fmt.Errorf("failed to look up release image metadata: %w", err)
		}
		return img, nil
	}(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to look up release image metadata: %w", err)
	}
	targetVersion := releaseImage.Version()

	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name).Name

	// Create a hash from nodePool.Spec.Config content and
	// Annotate NodePool with it.
	var compressedConfig []byte
	allConfigPlainText := ""
	if nodePool.Spec.Config != nil {
		for _, config := range nodePool.Spec.Config {
			configConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.Name,
					Namespace: nodePool.Namespace,
				},
			}
			if err := r.Get(ctx, ctrlclient.ObjectKeyFromObject(configConfigMap), configConfigMap); err != nil {
				return reconcile.Result{}, fmt.Errorf("failed to get config ConfigMap: %w", err)
			}

			// TODO (alberto): validate ConfigMap ignition content here?
			allConfigPlainText = allConfigPlainText + "\n---\n" + configConfigMap.Data[TokenSecretConfigKey]
		}
	}
	targetConfigHash := hashStruct(allConfigPlainText)
	targetConfigVersionHash := hashStruct(allConfigPlainText + targetVersion)
	compressedConfig, err = compress([]byte(allConfigPlainText))
	if err != nil {
		return ctrl.Result{}, err
	}

	isUpgrading := isUpgrading(nodePool, targetVersion)
	isUpdatingConfig := isUpdatingConfig(nodePool, targetConfigHash)
	if isUpgrading {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:    hyperv1.NodePoolUpgradingConditionType,
			Status:  metav1.ConditionTrue,
			Reason:  hyperv1.NodePoolAsExpectedConditionReason,
			Message: fmt.Sprintf("Upgrade in progress. Target version: %v", targetVersion),
		})
		r.Log.Info("New nodePool version changed. A new token Secret will be generated",
			"targetVersion", targetVersion)
	}
	if isUpdatingConfig {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:    hyperv1.NodePoolUpdatingConfigConditionType,
			Status:  metav1.ConditionTrue,
			Reason:  hyperv1.NodePoolAsExpectedConditionReason,
			Message: fmt.Sprintf("Updating config in progress"),
		})
		r.Log.Info("New nodePool config changed, A new token Secret will be generated",
			"targetVersion", targetVersion)
	}
	if isUpgrading || isUpdatingConfig {
		// Token Secrets are immutable and follow "prefixName-version-configHash" naming convention
		// Ensure old versioned resources are deleted, i.e token Secret and userdata Secret.
		tokenSecret := TokenSecret(controlPlaneNamespace, nodePool.Name, nodePool.GetAnnotations()[nodePoolAnnotationConfigVersion])
		if err := r.Delete(ctx, tokenSecret); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, fmt.Errorf("failed to delete token Secret: %w", err)
		}

		userDataSecret := IgnitionUserDataSecret(controlPlaneNamespace, nodePool.GetName(), nodePool.GetAnnotations()[nodePoolAnnotationConfigVersion])
		if err := r.Delete(ctx, userDataSecret); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, fmt.Errorf("failed to delete token Secret: %w", err)
		}
	}

	tokenSecret := TokenSecret(controlPlaneNamespace, nodePool.Name, targetConfigVersionHash)
	userDataSecret := IgnitionUserDataSecret(controlPlaneNamespace, nodePool.GetName(), targetConfigVersionHash)

	r.Log.Info("Reconciling token Secret")
	if result, err := controllerutil.CreateOrUpdate(ctx, r.Client, tokenSecret, func() error {
		tokenSecret.Immutable = k8sutilspointer.BoolPtr(true)
		if tokenSecret.Annotations == nil {
			tokenSecret.Annotations = make(map[string]string)
		}
		tokenSecret.Annotations[TokenSecretAnnotation] = "true"
		tokenSecret.Annotations[nodePoolAnnotation] = ctrlclient.ObjectKeyFromObject(nodePool).String()

		if tokenSecret.Data == nil {
			tokenSecret.Data = map[string][]byte{}
			tokenSecret.Data[TokenSecretTokenKey] = []byte(uuid.New().String())
			tokenSecret.Data[TokenSecretReleaseKey] = []byte(nodePool.Spec.Release.Image)
			tokenSecret.Data[TokenSecretConfigKey] = compressedConfig
		}
		return nil
	}); err != nil {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:    string(hyperv1.IgnitionEndpointAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.IgnitionTokenMissingError,
			Message: err.Error(),
		})
		return ctrl.Result{}, err
	} else {
		span.AddEvent("reconciled token Secret", trace.WithAttributes(attribute.String("result", string(result))))
		r.Log.Info("reconciled token Secret", "result", result)
	}

	r.Log.Info("Reconciling userdata Secret")
	caSecret := ignitionserver.IgnitionCACertSecret(controlPlaneNamespace)
	if err := r.Get(ctx, ctrlclient.ObjectKeyFromObject(caSecret), caSecret); err != nil {
		if apierrors.IsNotFound(err) {
			meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
				Type:               string(hyperv1.IgnitionEndpointAvailable),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.IgnitionCACertMissingReason,
				ObservedGeneration: nodePool.Generation,
			})
			r.Log.Info("still waiting for ignition CA cert secret to exist")
			return ctrl.Result{}, nil
		} else {
			return ctrl.Result{}, fmt.Errorf("failed to get ignition CA secret: %w", err)
		}
	}

	if result, err := controllerutil.CreateOrUpdate(ctx, r.Client, userDataSecret, func() error {
		userDataSecret.Immutable = k8sutilspointer.BoolPtr(true)
		if userDataSecret.Annotations == nil {
			userDataSecret.Annotations = make(map[string]string)
		}
		userDataSecret.Annotations[nodePoolAnnotation] = ctrlclient.ObjectKeyFromObject(nodePool).String()

		caCertBytes, hasCACert := caSecret.Data[corev1.TLSCertKey]
		if !hasCACert {
			return fmt.Errorf("ca secret is missing tls.crt key")
		}
		tokenBytes, hasToken := tokenSecret.Data[TokenSecretTokenKey]
		if !hasToken {
			return fmt.Errorf("token secret is missing token key")
		}
		encodedCACert := base64.StdEncoding.EncodeToString(caCertBytes)
		encodedToken := base64.StdEncoding.EncodeToString(tokenBytes)
		ignConfig := ignitionapi.Config{
			Ignition: ignitionapi.Ignition{
				Version: "3.1.0",
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
							Source: k8sutilspointer.StringPtr(fmt.Sprintf("https://%s/ignition", hcluster.Status.IgnitionEndpoint)),
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
		userDataValue, err := json.Marshal(ignConfig)
		if err != nil {
			return fmt.Errorf("failed to marshal ignition config: %w", err)
		}
		userDataSecret.Data = map[string][]byte{
			"disableTemplating": []byte(base64.StdEncoding.EncodeToString([]byte("true"))),
			"value":             userDataValue,
		}
		return nil
	}); err != nil {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:    string(hyperv1.IgnitionEndpointAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.IgnitionUserDataErrorReason,
			Message: err.Error(),
		})
		return ctrl.Result{}, err
	} else {
		span.AddEvent("reconciled ignition user data secret", trace.WithAttributes(attribute.String("result", string(result))))
		r.Log.Info("reconciled ignition user data secret", "result", result)
	}

	switch nodePool.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		var ami string
		switch {
		case len(nodePool.Spec.Platform.AWS.AMI) > 0:
			ami = nodePool.Spec.Platform.AWS.AMI
		default:
			defaultAmi, err := defaultNodePoolAMI(hcluster, releaseImage)
			if err != nil {
				meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
					Type:    hyperv1.NodePoolAMIDiscoveryFailed,
					Status:  metav1.ConditionTrue,
					Reason:  hyperv1.NodePoolValidationFailedConditionReason,
					Message: fmt.Sprintf("Couldn't discover an AMI for release image %q: %s", nodePool.Spec.Release.Image, err),
				})
				return ctrl.Result{}, fmt.Errorf("couldn't discover an AMI for release image: %w", err)
			}
			meta.RemoveStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolAMIDiscoveryFailed)
			ami = defaultAmi
		}

		md := machineDeployment(nodePool, hcluster.Spec.InfraID, controlPlaneNamespace)
		awsMachineTemplate := AWSMachineTemplate(hcluster.Spec.InfraID, ami, nodePool, controlPlaneNamespace)
		mhc := machineHealthCheck(nodePool, controlPlaneNamespace)

		r.Log.Info("Reconciling AWSMachineTemplate")
		// If a change happens to the nodePool AWSNodePoolPlatform we delete the existing awsMachineTemplate,
		// create a new one with a new name
		// and pass it to the machineDeployment. This will trigger a rolling upgrade.
		currentMD := &capiv1.MachineDeployment{}
		if err := r.Get(ctx, ctrlclient.ObjectKeyFromObject(md), currentMD); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to get machineDeployment: %w", err)
		}

		// If the machineDeployment has not been created yet, create new awsMachineTemplate.
		if currentMD.CreationTimestamp.IsZero() {
			r.Log.Info("Creating new AWSMachineTemplate", "AWSMachineTemplate", ctrlclient.ObjectKeyFromObject(awsMachineTemplate).String())
			if result, err := controllerutil.CreateOrPatch(ctx, r.Client, awsMachineTemplate, func() error {
				return nil
			}); err != nil {
				return ctrl.Result{}, fmt.Errorf("error creating new AWSMachineTemplate: %w", err)
			} else {
				span.AddEvent("reconciled aws machinetemplate", trace.WithAttributes(attribute.String("result", string(result))))
			}
		}

		if !currentMD.CreationTimestamp.IsZero() {
			currentAWSMachineTemplate := &capiaws.AWSMachineTemplate{}
			if err := r.Get(ctx, client.ObjectKey{
				Namespace: currentMD.Spec.Template.Spec.InfrastructureRef.Namespace,
				Name:      currentMD.Spec.Template.Spec.InfrastructureRef.Name,
			}, currentAWSMachineTemplate); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}

			if !equality.Semantic.DeepEqual(currentAWSMachineTemplate.Spec.Template.Spec, awsMachineTemplate.Spec.Template.Spec) {
				r.Log.Info("AWS config has changed. This will trigger a rolling upgrade")
				r.Log.Info("Creating new AWSMachineTemplate", "AWSMachineTemplate", ctrlclient.ObjectKeyFromObject(awsMachineTemplate).String())
				// Create new template
				if result, err := controllerutil.CreateOrPatch(ctx, r.Client, awsMachineTemplate, func() error {
					return nil
				}); err != nil {
					return ctrl.Result{}, fmt.Errorf("error creating new AWSMachineTemplate: %w", err)
				} else {
					span.AddEvent("reconciled aws machinetemplate", trace.WithAttributes(attribute.String("result", string(result))))
				}
				// Delete existing template
				r.Log.Info("Deleting existing AWSMachineTemplate", "AWSMachineTemplate", ctrlclient.ObjectKeyFromObject(currentAWSMachineTemplate).String())
				if err := r.Delete(ctx, currentAWSMachineTemplate); err != nil && !apierrors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("error deleting existing AWSMachineTemplate: %w", err)
				} else {
					span.AddEvent("deleted aws machinetemplate", trace.WithAttributes(attribute.String("name", currentAWSMachineTemplate.Name)))
				}
			} else {
				// We pass the existing one to reconcileMachineDeployment.
				awsMachineTemplate = currentAWSMachineTemplate
			}
		}

		r.Log.Info("Reconciling MachineDeployment")
		if result, err := controllerutil.CreateOrPatch(ctx, r.Client, md, func() error {
			return r.reconcileMachineDeployment(
				md, nodePool,
				userDataSecret,
				awsMachineTemplate,
				hcluster.Spec.InfraID,
				targetConfigHash, targetConfigVersionHash,
				releaseImage)
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile machineDeployment %q: %w",
				ctrlclient.ObjectKeyFromObject(md).String(), err)
		} else {
			span.AddEvent("reconciled machinedeployment", trace.WithAttributes(attribute.String("result", string(result))))
		}

		// Reconcile MachineHealthCheck
		if nodePool.Spec.Management.AutoRepair {
			r.Log.Info("Reconciling MachineHealthChecks")
			if result, err := ctrl.CreateOrUpdate(ctx, r.Client, mhc, func() error {
				return r.reconcileMachineHealthCheck(mhc, nodePool, hcluster.Spec.InfraID)
			}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to reconcile machineHealthCheck %q: %w",
					ctrlclient.ObjectKeyFromObject(mhc).String(), err)
			} else {
				span.AddEvent("reconciled machinehealthchecks", trace.WithAttributes(attribute.String("result", string(result))))
			}
			meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
				Type:   hyperv1.NodePoolAutorepairEnabledConditionType,
				Status: metav1.ConditionTrue,
				Reason: hyperv1.NodePoolAsExpectedConditionReason,
			})
		} else {
			if err := r.Client.Delete(ctx, mhc); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			} else {
				span.AddEvent("deleted machinehealthcheck", trace.WithAttributes(attribute.String("name", mhc.Name)))
			}
			meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
				Type:   hyperv1.NodePoolAutorepairEnabledConditionType,
				Status: metav1.ConditionFalse,
				Reason: hyperv1.NodePoolAsExpectedConditionReason,
			})
		}
	}

	return ctrl.Result{}, nil
}

func isUpgrading(nodePool *hyperv1.NodePool, targetVersion string) bool {
	return targetVersion != nodePool.Status.Version
}

func isUpdatingConfig(nodePool *hyperv1.NodePool, newConfigHash string) bool {
	return newConfigHash != nodePool.GetAnnotations()[nodePoolAnnotationConfig]
}

func defaultNodePoolAMI(hcluster *hyperv1.HostedCluster, releaseImage *releaseinfo.ReleaseImage) (string, error) {
	// TODO: The architecture should be specified from the API
	arch, foundArch := releaseImage.StreamMetadata.Architectures["x86_64"]
	if !foundArch {
		return "", fmt.Errorf("couldn't find OS metadata for architecture %q", "x64_64")
	}
	// TODO: Should the region be included in the NodePool platform information?
	region := hcluster.Spec.Platform.AWS.Region
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
		shortName := fmt.Sprintf("%s-%s", prefix, hash(name))
		return shortName[:min(maxLength, len(shortName))]
	}

	prefix := base[0:baseLength]
	// Calculate hash on initial base-suffix string
	return fmt.Sprintf("%s-%s-%s", prefix, hash(base), suffix)
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

// hash calculates the hexadecimal representation (8-chars)
// of the hash of the passed in string using the FNV-a algorithm
func hash(s string) string {
	hash := fnv.New32a()
	hash.Write([]byte(s))
	intHash := hash.Sum32()
	result := fmt.Sprintf("%08x", intHash)
	return result
}

func isAutoscalingEnabled(nodePool *hyperv1.NodePool) bool {
	return nodePool.Spec.AutoScaling != nil
}

func validate(nodePool *hyperv1.NodePool) error {
	if nodePool.Spec.NodeCount != nil && nodePool.Spec.AutoScaling != nil {
		return fmt.Errorf("only one of nodePool.Spec.NodeCount or nodePool.Spec.AutoScaling can be set")
	}

	if nodePool.Spec.AutoScaling != nil {
		max := nodePool.Spec.AutoScaling.Max
		min := nodePool.Spec.AutoScaling.Min

		if max == nil || min == nil {
			return fmt.Errorf("max and min must be not nil. Max: %v, Min: %v", max, min)
		}

		if *max < *min {
			return fmt.Errorf("max must be equal or greater than min. Max: %v, Min: %v", *max, *min)
		}

		if *max == 0 && *min == 0 {
			return fmt.Errorf("max and min must be not zero. Max: %v, Min: %v", *max, *min)
		}
	}

	return nil
}

func (r *NodePoolReconciler) enqueueNodePoolsForHostedCluster(obj ctrlclient.Object) []reconcile.Request {
	var result []reconcile.Request

	hc, ok := obj.(*hyperv1.HostedCluster)
	if !ok {
		panic(fmt.Sprintf("Expected a HostedCluster but got a %T", obj))
	}

	nodePoolList := &hyperv1.NodePoolList{}
	if err := r.List(context.Background(), nodePoolList); err != nil {
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

func enqueueParentNodePool(obj ctrlclient.Object) []reconcile.Request {
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

func (r *NodePoolReconciler) reconcileMachineDeployment(machineDeployment *capiv1.MachineDeployment,
	nodePool *hyperv1.NodePool,
	userDataSecret *corev1.Secret,
	awsMachineTemplate *capiaws.AWSMachineTemplate,
	CAPIClusterName string,
	targetConfigHash, targetConfigVersionHash string,
	releaseImage *releaseinfo.ReleaseImage) error {

	// Set annotations and labels
	if machineDeployment.GetAnnotations() == nil {
		machineDeployment.Annotations = map[string]string{}
	}
	machineDeployment.Annotations[nodePoolAnnotation] = ctrlclient.ObjectKeyFromObject(nodePool).String()
	if machineDeployment.GetLabels() == nil {
		machineDeployment.Labels = map[string]string{}
	}
	machineDeployment.Labels[capiv1.ClusterLabelName] = CAPIClusterName

	// Set upgrade strategy
	resourcesName := generateName(CAPIClusterName, nodePool.Spec.ClusterName, nodePool.GetName())
	machineDeployment.Spec.MinReadySeconds = k8sutilspointer.Int32Ptr(int32(0))

	// This is additional backend validation. API validation/default should
	// prevent this from ever happening.

	// Only upgradeType "Replace" is supported atm.
	if nodePool.Spec.Management.UpgradeType != hyperv1.UpgradeTypeReplace ||
		nodePool.Spec.Management.Replace == nil {
		return fmt.Errorf("this is unsupported. %q upgrade type and a strategy: %q or %q are required",
			hyperv1.UpgradeTypeReplace, hyperv1.UpgradeStrategyRollingUpdate, hyperv1.UpgradeStrategyOnDelete)
	}

	// RollingUpdate strategy requires MaxUnavailable and MaxSurge
	if nodePool.Spec.Management.Replace.Strategy == hyperv1.UpgradeStrategyRollingUpdate &&
		nodePool.Spec.Management.Replace.RollingUpdate == nil {
		return fmt.Errorf("this is unsupported. %q upgrade type with strategy %q require a MaxUnavailable and MaxSurge",
			hyperv1.UpgradeTypeReplace, hyperv1.UpgradeStrategyRollingUpdate)
	}

	machineDeployment.Spec.Strategy = &capiv1.MachineDeploymentStrategy{}
	machineDeployment.Spec.Strategy.Type = capiv1.MachineDeploymentStrategyType(nodePool.Spec.Management.Replace.Strategy)
	if nodePool.Spec.Management.Replace.RollingUpdate != nil {
		machineDeployment.Spec.Strategy.RollingUpdate = &capiv1.MachineRollingUpdateDeployment{
			MaxUnavailable: nodePool.Spec.Management.Replace.RollingUpdate.MaxUnavailable,
			MaxSurge:       nodePool.Spec.Management.Replace.RollingUpdate.MaxSurge,
		}
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
				DataSecretName: k8sutilspointer.StringPtr(userDataSecret.GetName()),
			},
			InfrastructureRef: corev1.ObjectReference{
				Kind:       "AWSMachineTemplate",
				APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha3",
				Namespace:  awsMachineTemplate.GetNamespace(),
				Name:       awsMachineTemplate.GetName(),
			},
			// don't stamp version given by the nodePool
			Version: machineDeployment.Spec.Template.Spec.Version,
		},
	}

	// Propagate version to the machineDeployment.
	targetVersion := releaseImage.Version()
	if targetVersion == nodePool.Status.Version &&
		targetVersion != k8sutilspointer.StringPtrDerefOr(machineDeployment.Spec.Template.Spec.Version, "") {
		// This should never happen by design.
		return fmt.Errorf("unexpected error. NodePool current version does not match machineDeployment version")
	}

	if targetVersion != k8sutilspointer.StringPtrDerefOr(machineDeployment.Spec.Template.Spec.Version, "") {
		r.Log.Info("Propagating new version to the machineDeployment. Starting upgrade",
			"releaseImage", nodePool.Spec.Release.Image, "targetVersion", targetVersion)
		// TODO (alberto): Point to a new InfrastructureRef with the new version AMI
		// https://github.com/openshift/enhancements/pull/201
		machineDeployment.Spec.Template.Spec.Version = &targetVersion
		machineDeployment.Spec.Template.Spec.Bootstrap.DataSecretName = k8sutilspointer.StringPtr(userDataSecret.Name)

		// We return early here during a version upgrade to persist the resource.
		// So in the next reconciling loop we get a new MachineDeployment.Generation
		// and we do a legit MachineDeploymentComplete/MachineDeployment.Status.ObservedGeneration check.
		// If the nodePool is brand new we want to make sure the replica number is set so the machineDeployment controller
		// does not panic.
		if machineDeployment.Spec.Replicas == nil {
			machineDeployment.Spec.Replicas = k8sutilspointer.Int32Ptr(k8sutilspointer.Int32PtrDerefOr(nodePool.Spec.NodeCount, 1))
		}
		return nil
	}

	if MachineDeploymentComplete(machineDeployment) {
		if nodePool.Status.Version != targetVersion {
			nodePool.Status.Version = targetVersion
			r.Log.Info("Upgrade complete", "targetVersion", targetVersion)
			meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
				Type:    hyperv1.NodePoolUpgradingConditionType,
				Status:  metav1.ConditionFalse,
				Reason:  hyperv1.NodePoolAsExpectedConditionReason,
				Message: "",
			})
		}
		if nodePool.Annotations == nil {
			nodePool.Annotations = make(map[string]string)
		}
		nodePool.Annotations[nodePoolAnnotationConfig] = targetConfigHash
		nodePool.Annotations[nodePoolAnnotationConfigVersion] = targetConfigVersionHash
	}

	// Set wanted replicas:
	// If autoscaling is enabled we reconcile min/max annotations and leave replicas untouched.
	if isAutoscalingEnabled(nodePool) {
		r.Log.Info("NodePool autoscaling is enabled",
			"Maximum nodes", *nodePool.Spec.AutoScaling.Max,
			"Minimum nodes", *nodePool.Spec.AutoScaling.Min)

		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:    hyperv1.NodePoolAutoscalingEnabledConditionType,
			Status:  metav1.ConditionTrue,
			Reason:  hyperv1.NodePoolAsExpectedConditionReason,
			Message: fmt.Sprintf("Maximum nodes: %v, Minimum nodes: %v", *nodePool.Spec.AutoScaling.Max, *nodePool.Spec.AutoScaling.Min),
		})

		if !machineDeployment.CreationTimestamp.IsZero() {
			// if autoscaling is enabled and the machineDeployment does not exist yet
			// we start with 1 replica as the autoscaler does not support scaling from zero yet.
			machineDeployment.Spec.Replicas = k8sutilspointer.Int32Ptr(int32(1))
		}
		machineDeployment.Annotations[autoscalerMaxAnnotation] = strconv.Itoa(*nodePool.Spec.AutoScaling.Max)
		machineDeployment.Annotations[autoscalerMinAnnotation] = strconv.Itoa(*nodePool.Spec.AutoScaling.Min)
	}

	// If autoscaling is NOT enabled we reset min/max annotations and reconcile replicas.
	if !isAutoscalingEnabled(nodePool) {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:   hyperv1.NodePoolAutoscalingEnabledConditionType,
			Status: metav1.ConditionFalse,
			Reason: hyperv1.NodePoolAsExpectedConditionReason,
		})

		machineDeployment.Annotations[autoscalerMaxAnnotation] = "0"
		machineDeployment.Annotations[autoscalerMinAnnotation] = "0"
		machineDeployment.Spec.Replicas = k8sutilspointer.Int32Ptr(k8sutilspointer.Int32PtrDerefOr(nodePool.Spec.NodeCount, 0))
	}

	nodePool.Status.NodeCount = int(machineDeployment.Status.AvailableReplicas)
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

func hashStruct(o interface{}) string {
	hash := fnv.New32a()
	hash.Write([]byte(fmt.Sprintf("%v", o)))
	intHash := hash.Sum32()
	return fmt.Sprintf("%08x", intHash)
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
				annotation == ctrlclient.ObjectKeyFromObject(nodePool).String() {
				filtered = append(filtered, awsMachineTemplateList.Items[i])
			}
		}
	}
	return filtered, nil
}
