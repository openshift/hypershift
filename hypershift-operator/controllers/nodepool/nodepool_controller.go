package nodepool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strconv"
	"time"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_1/types"
	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/releaseinfo"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	hyperutil "github.com/openshift/hypershift/hypershift-operator/controllers/util"
	capiv1 "github.com/openshift/hypershift/thirdparty/clusterapi/api/v1alpha4"
	"github.com/openshift/hypershift/thirdparty/clusterapi/util"
	"github.com/openshift/hypershift/thirdparty/clusterapi/util/patch"
	capiaws "github.com/openshift/hypershift/thirdparty/clusterapiprovideraws/v1alpha4"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
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
	finalizer                = "hypershift.openshift.io/finalizer"
	autoscalerMaxAnnotation  = "cluster.x-k8s.io/cluster-api-autoscaler-node-group-max-size"
	autoscalerMinAnnotation  = "cluster.x-k8s.io/cluster-api-autoscaler-node-group-min-size"
	nodePoolAnnotation       = "hypershift.openshift.io/nodePool"
	payloadVersionAnnotation = "hypershift.openshift.io/payloadVersion"
)

type NodePoolReconciler struct {
	ctrlclient.Client
	recorder        record.EventRecorder
	Log             logr.Logger
	ReleaseProvider releaseinfo.Provider
}

func (r *NodePoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.NodePool{}).
		// We want to reconcile when the HostedCluster IgnitionEndpoint is available.
		Watches(&source.Kind{Type: &hyperv1.HostedCluster{}}, handler.EnqueueRequestsFromMapFunc(r.enqueueNodePoolsForHostedCluster)).
		Watches(&source.Kind{Type: &capiv1.MachineDeployment{}}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool)).
		Watches(&source.Kind{Type: &capiaws.AWSMachineTemplate{}}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool)).
		// We want to reconcile when the MCS deployment finishes rolling out a new release version
		Watches(&source.Kind{Type: &appsv1.Deployment{}}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool)).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
		}).
		Build(r)
	if err != nil {
		return errors.Wrap(err, "failed setting up with a controller manager")
	}

	r.recorder = mgr.GetEventRecorderFor("nodepool-controller")

	return nil
}

func (r *NodePoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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
	mcsServiceAccount := MachineConfigServerServiceAccount(controlPlaneNamespace, nodePool.GetName())
	mcsRoleBinding := MachineConfigServerRoleBinding(controlPlaneNamespace, nodePool.GetName())
	mcsService := MachineConfigServerService(controlPlaneNamespace, nodePool.GetName())
	mcsDeployment := MachineConfigServerDeployment(controlPlaneNamespace, nodePool.GetName())
	userDataSecret := MachineConfigServerUserDataSecret(controlPlaneNamespace, nodePool.GetName())

	md := machineDeployment(nodePool, hcluster.Spec.InfraID, controlPlaneNamespace)
	mhc := machineHealthCheck(nodePool, controlPlaneNamespace)

	if !nodePool.DeletionTimestamp.IsZero() {
		awsMachineTemplates, err := r.listAWSMachineTemplates(nodePool)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to list AWSMachineTemplates: %w", err)
		}
		for k := range awsMachineTemplates {
			if err := r.Delete(ctx, &awsMachineTemplates[k]); err != nil && !apierrors.IsNotFound(err) {
				return reconcile.Result{}, fmt.Errorf("failed to delete AWSMachineTemplate: %w", err)
			}
		}
		if err := r.Delete(ctx, md); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, fmt.Errorf("failed to delete MachineDeployment: %w", err)
		}

		if err := r.Delete(ctx, mcsServiceAccount); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete MCS ServiceAccount: %w", err)
		}

		if err := r.Delete(ctx, mcsRoleBinding); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete MCS RoleBinding: %w", err)
		}

		if err := r.Delete(ctx, mcsService); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete MCS Service: %w", err)
		}

		if err := r.Delete(ctx, mcsDeployment); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete MCS Deployment: %w", err)
		}

		if err := r.Delete(ctx, userDataSecret); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete MCS Userdata Secret: %w", err)
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

	if err := patchHelper.Patch(ctx, nodePool); err != nil {
		r.Log.Error(err, "failed to patch")
		return ctrl.Result{}, fmt.Errorf("failed to patch: %w", err)
	}

	r.Log.Info("Successfully reconciled")
	return ctrl.Result{}, nil
}

func (r *NodePoolReconciler) reconcile(ctx context.Context, hcluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool) (ctrl.Result, error) {
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

	lookupCtx, lookupCancel := context.WithTimeout(ctx, 1*time.Minute)
	defer lookupCancel()
	releaseImage, err := r.ReleaseProvider.Lookup(lookupCtx, nodePool.Spec.Release.Image)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to look up release image metadata: %w", err)
	}
	targetVersion := releaseImage.Version()

	if isUpgrading(nodePool, targetVersion) {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:    hyperv1.NodePoolUpgradingConditionType,
			Status:  metav1.ConditionTrue,
			Reason:  hyperv1.NodePoolAsExpectedConditionReason,
			Message: fmt.Sprintf("Upgrade in progress. Target version: %v", targetVersion),
		})
		r.Log.Info("New nodePool version set. Upgrading", "targetVersion", targetVersion)
	}

	// Reconcile machineConfigServer for the given nodePool release
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name).Name
	mcsServiceAccount := MachineConfigServerServiceAccount(controlPlaneNamespace, nodePool.GetName())
	mcsRoleBinding := MachineConfigServerRoleBinding(controlPlaneNamespace, nodePool.GetName())
	mcsService := MachineConfigServerService(controlPlaneNamespace, nodePool.GetName())
	mcsDeployment := MachineConfigServerDeployment(controlPlaneNamespace, nodePool.GetName())
	userDataSecret := MachineConfigServerUserDataSecret(controlPlaneNamespace, nodePool.GetName())

	r.Log.Info("Reconciling MCS ServiceAccount")
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, mcsServiceAccount, func() error {
		mcsServiceAccount.ImagePullSecrets = []corev1.LocalObjectReference{
			{Name: controlplaneoperator.PullSecret(mcsServiceAccount.Namespace).Name},
		}
		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	r.Log.Info("Reconciling MCS RoleBinding")
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, mcsRoleBinding, func() error {
		mcsRoleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "edit",
		}
		mcsRoleBinding.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      mcsServiceAccount.Name,
				Namespace: mcsServiceAccount.Namespace,
			},
		}
		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	r.Log.Info("Reconciling MCS Deployment")
	if result, err := controllerutil.CreateOrUpdate(ctx, r.Client, mcsDeployment, func() error {
		return reconcileMCSDeployment(nodePool, mcsDeployment, mcsServiceAccount, releaseImage)
	}); err != nil {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.IgnitionEndpointAvailable),
			Status:             metav1.ConditionFalse,
			ObservedGeneration: nodePool.Generation,
			Reason:             hyperv1.MachineConfigServerDeploymentUnavailableReason,
			Message:            err.Error(),
		})
		return ctrl.Result{}, err
	} else {
		newCondition := metav1.Condition{
			Type:   string(hyperv1.IgnitionEndpointAvailable),
			Status: metav1.ConditionFalse,
			Reason: hyperv1.MachineConfigServerDeploymentUnavailableReason,
		}
		for _, cond := range mcsDeployment.Status.Conditions {
			if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
				newCondition = metav1.Condition{
					Type:   string(hyperv1.IgnitionEndpointAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.MachineConfigServerDeploymentAsExpected,
				}
				break
			}
		}
		newCondition.ObservedGeneration = nodePool.Generation
		meta.SetStatusCondition(&hcluster.Status.Conditions, newCondition)
		r.Log.Info("reconciled MCS deployment", "result", result)
	}

	r.Log.Info("Reconciling MCS Service")
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, mcsService, func() error {
		mcsService.Spec.Ports = []corev1.ServicePort{
			{
				Name:       "http",
				Protocol:   corev1.ProtocolTCP,
				Port:       80,
				TargetPort: intstr.FromInt(8080),
			},
		}

		mcsService.Spec.Selector = MachineConfigServerServiceSelector(nodePool.GetName())

		mcsService.Spec.Type = corev1.ServiceTypeClusterIP
		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
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
	tokenSecret := ignitionserver.IgnitionTokenSecret(controlPlaneNamespace)
	if err := r.Get(ctx, ctrlclient.ObjectKeyFromObject(tokenSecret), tokenSecret); err != nil {
		if apierrors.IsNotFound(err) {
			meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
				Type:               string(hyperv1.IgnitionEndpointAvailable),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.IgnitionTokenMissingReason,
				ObservedGeneration: nodePool.Generation,
			})
			r.Log.Info("still waiting for ignition token secret to exist")
			return ctrl.Result{}, nil
		} else {
			return ctrl.Result{}, fmt.Errorf("failed to get token secret: %w", err)
		}
	}

	if result, err := controllerutil.CreateOrUpdate(ctx, r.Client, userDataSecret, func() error {
		caCertBytes, hasCACert := caSecret.Data[corev1.TLSCertKey]
		if !hasCACert {
			return fmt.Errorf("ca secret is missing tls.crt key")
		}
		tokenBytes, hasToken := tokenSecret.Data[ignitionserver.TokenSecretKey]
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
							Source: k8sutilspointer.StringPtr(fmt.Sprintf("https://%s/ignition/%s", hcluster.Status.IgnitionEndpoint, nodePool.Name)),
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
			Reason:  hyperv1.IgnitionPayloadErrorReason,
			Message: err.Error(),
		})
		return ctrl.Result{}, err
	} else {
		r.Log.Info("reconciled ignition user data secret", "result", result)
	}

	if mcsDeployment.GetLabels()[payloadVersionAnnotation] == targetVersion || !DeploymentComplete(mcsDeployment) {
		r.Log.Info("machineConfigServer version does not match nodePool target version yet, waiting")
		return ctrl.Result{}, nil
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
			if _, err := controllerutil.CreateOrPatch(ctx, r.Client, awsMachineTemplate, func() error {
				return nil
			}); err != nil {
				return ctrl.Result{}, fmt.Errorf("error creating new AWSMachineTemplate: %w", err)
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
				if _, err := controllerutil.CreateOrPatch(ctx, r.Client, awsMachineTemplate, func() error {
					return nil
				}); err != nil {
					return ctrl.Result{}, fmt.Errorf("error creating new AWSMachineTemplate: %w", err)
				}
				// Delete existing template
				r.Log.Info("Deleting existing AWSMachineTemplate", "AWSMachineTemplate", ctrlclient.ObjectKeyFromObject(currentAWSMachineTemplate).String())
				if err := r.Delete(ctx, currentAWSMachineTemplate); err != nil && !apierrors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("error deleting existing AWSMachineTemplate: %w", err)
				}
			} else {
				// We pass the existing one to reconcileMachineDeployment.
				awsMachineTemplate = currentAWSMachineTemplate
			}
		}

		r.Log.Info("Reconciling MachineDeployment")
		if _, err := controllerutil.CreateOrPatch(ctx, r.Client, md, func() error {
			return r.reconcileMachineDeployment(
				md, nodePool,
				mcsDeployment, userDataSecret,
				awsMachineTemplate,
				hcluster.Spec.InfraID, releaseImage)
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile machineDeployment %q: %w",
				ctrlclient.ObjectKeyFromObject(md).String(), err)
		}

		// Reconcile MachineHealthCheck
		if nodePool.Spec.Management.AutoRepair {
			r.Log.Info("Reconciling MachineHealthChecks")
			if _, err := ctrl.CreateOrUpdate(ctx, r.Client, mhc, func() error {
				return r.reconcileMachineHealthCheck(mhc, nodePool, hcluster.Spec.InfraID)
			}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to reconcile machineHealthCheck %q: %w",
					ctrlclient.ObjectKeyFromObject(mhc).String(), err)
			}
			meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
				Type:   hyperv1.NodePoolAutorepairEnabledConditionType,
				Status: metav1.ConditionTrue,
				Reason: hyperv1.NodePoolAsExpectedConditionReason,
			})
		} else {
			if err := r.Client.Delete(ctx, mhc); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
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

// DeploymentComplete considers a deployment to be complete once all of its desired replicas
// are updated and available, and no old machines are running.
func MachineDeploymentComplete(deployment *capiv1.MachineDeployment) bool {
	newStatus := &deployment.Status
	return newStatus.UpdatedReplicas == *(deployment.Spec.Replicas) &&
		newStatus.Replicas == *(deployment.Spec.Replicas) &&
		newStatus.AvailableReplicas == *(deployment.Spec.Replicas) &&
		newStatus.ObservedGeneration >= deployment.Generation
}

func DeploymentComplete(deployment *appsv1.Deployment) bool {
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
	mcsDeployment *appsv1.Deployment,
	userDataSecret *corev1.Secret,
	awsMachineTemplate *capiaws.AWSMachineTemplate,
	CAPIClusterName string,
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
	maxUnavailable := intstr.FromInt(nodePool.Spec.Management.MaxUnavailable)
	maxSurge := intstr.FromInt(nodePool.Spec.Management.MaxSurge)
	machineDeployment.Spec.MinReadySeconds = k8sutilspointer.Int32Ptr(int32(0))
	machineDeployment.Spec.Strategy = &capiv1.MachineDeploymentStrategy{
		Type: capiv1.RollingUpdateMachineDeploymentStrategyType,
		RollingUpdate: &capiv1.MachineRollingUpdateDeployment{
			MaxUnavailable: &maxUnavailable,
			MaxSurge:       &maxSurge,
		},
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

	if targetVersion != mcsDeployment.GetAnnotations()[payloadVersionAnnotation] {
		// This should never happen by design.
		return fmt.Errorf("unexpected error. NodePool target version: %v, has no machineConfigServer available: %v", targetVersion, mcsDeployment.GetAnnotations()[payloadVersionAnnotation])
	}

	if targetVersion != k8sutilspointer.StringPtrDerefOr(machineDeployment.Spec.Template.Spec.Version, "") {
		r.Log.Info("Propagating new version to the machineDeployment. Starting upgrade",
			"releaseImage", nodePool.Spec.Release.Image, "targetVersion", targetVersion)
		// TODO (alberto): Point to a new InfrastructureRef with the new version AMI
		// https://github.com/openshift/enhancements/pull/201
		machineDeployment.Spec.Template.Spec.Version = &targetVersion

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

func reconcileMCSDeployment(nodePool *hyperv1.NodePool, deployment *appsv1.Deployment, sa *corev1.ServiceAccount, releaseImage *releaseinfo.ReleaseImage) error {
	images := releaseImage.ComponentImages()
	targetVersion := releaseImage.Version()
	// We use the nodePool name as the label to be selected by the Service.
	appName := nodePool.GetName()

	bootstrapArgs := fmt.Sprintf(`
mkdir -p /mcc-manifests/bootstrap/manifests
mkdir -p /mcc-manifests/manifests
exec machine-config-operator bootstrap \
--root-ca=/assets/manifests/root-ca.crt \
--kube-ca=/assets/manifests/combined-ca.crt \
--machine-config-operator-image=%s \
--machine-config-oscontent-image=%s \
--infra-image=%s \
--keepalived-image=%s \
--coredns-image=%s \
--mdns-publisher-image=%s \
--haproxy-image=%s \
--baremetal-runtimecfg-image=%s \
--infra-config-file=/assets/manifests/cluster-infrastructure-02-config.yaml \
--network-config-file=/assets/manifests/cluster-network-02-config.yaml \
--proxy-config-file=/assets/manifests/cluster-proxy-01-config.yaml \
--config-file=/assets/manifests/install-config.yaml \
--dns-config-file=/assets/manifests/cluster-dns-02-config.yaml \
--dest-dir=/mcc-manifests \
--pull-secret=/assets/manifests/pull-secret.yaml

# Use our own version of configpools that swap master and workers
mv /mcc-manifests/bootstrap/manifests /mcc-manifests/bootstrap/manifests.tmp
mkdir /mcc-manifests/bootstrap/manifests
cp /mcc-manifests/bootstrap/manifests.tmp/* /mcc-manifests/bootstrap/manifests/
cp /assets/manifests/*.machineconfigpool.yaml /mcc-manifests/bootstrap/manifests/`,
		images["machine-config-operator"],
		images["machine-os-content"],
		images["pod"],
		images["keepalived-ipfailover"],
		images["coredns"],
		images["mdns-publisher"],
		images["haproxy-router"],
		images["baremetal-runtimecfg"],
	)

	customMachineConfigArg := `
cat <<"EOF" > "./copy-ignition-config.sh"
#!/bin/bash
name="${1}"
oc get cm ${name} -n "${NAMESPACE}" -o jsonpath='{ .data.data }' > "/mcc-manifests/bootstrap/manifests/${name/#ignition-config-//}.yaml"
EOF
chmod +x ./copy-ignition-config.sh
oc get cm -l ignition-config="true" -n "${NAMESPACE}" --no-headers | awk '{ print $1 }' | xargs -n1 ./copy-ignition-config.sh`

	if deployment.Annotations == nil {
		deployment.Annotations = make(map[string]string)
	}
	deployment.Annotations[payloadVersionAnnotation] = targetVersion
	deployment.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()

	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = make(map[string]string)
	}
	deployment.Spec.Template.Annotations[payloadVersionAnnotation] = targetVersion

	deployment.Spec = appsv1.DeploymentSpec{
		Replicas: k8sutilspointer.Int32Ptr(1),
		Selector: &metav1.LabelSelector{
			MatchLabels: MachineConfigServerServiceSelector(appName),
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: MachineConfigServerServiceSelector(appName),
			},
			Spec: corev1.PodSpec{
				ServiceAccountName:            sa.Name,
				TerminationGracePeriodSeconds: k8sutilspointer.Int64Ptr(10),
				Tolerations: []corev1.Toleration{
					{
						Key:      "multi-az-worker",
						Operator: "Equal",
						Value:    "true",
						Effect:   "NoSchedule",
					},
				},
				InitContainers: []corev1.Container{
					{
						Image: images["machine-config-operator"],
						Name:  "machine-config-operator-bootstrap",
						Command: []string{
							"/bin/bash",
						},
						Args: []string{
							"-c",
							bootstrapArgs,
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "mcc-manifests",
								MountPath: "/mcc-manifests",
							},
							{
								Name:      "config",
								MountPath: "/assets/manifests",
							},
						},
					},
					{
						Image:           images["cli"],
						ImagePullPolicy: corev1.PullIfNotPresent,
						Name:            "inject-custom-machine-configs",
						Env: []corev1.EnvVar{
							{
								Name: "NAMESPACE",
								ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{
										FieldPath: "metadata.namespace",
									},
								},
							},
						},
						WorkingDir: "/tmp",
						Command: []string{
							"/usr/bin/bash",
						},
						Args: []string{
							"-c",
							customMachineConfigArg,
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "mcc-manifests",
								MountPath: "/mcc-manifests",
							},
						},
					},
					{
						Image:           images["machine-config-operator"],
						ImagePullPolicy: corev1.PullIfNotPresent,
						Name:            "machine-config-controller-bootstrap",
						Command: []string{
							"/usr/bin/machine-config-controller",
						},
						Args: []string{
							"bootstrap",
							"--manifest-dir=/mcc-manifests/bootstrap/manifests",
							"--pull-secret=/mcc-manifests/bootstrap/manifests/machineconfigcontroller-pull-secret",
							"--dest-dir=/mcs-manifests",
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "mcc-manifests",
								MountPath: "/mcc-manifests",
							},
							{
								Name:      "mcs-manifests",
								MountPath: "/mcs-manifests",
							},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Image:           images["machine-config-operator"],
						ImagePullPolicy: corev1.PullIfNotPresent,
						Name:            "machine-config-server",
						Command: []string{
							"/usr/bin/machine-config-server",
						},
						Args: []string{
							"bootstrap",
							"--bootstrap-kubeconfig=/etc/openshift/kubeconfig",
							"--secure-port=8443",
							"--insecure-port=8080",
						},
						Ports: []corev1.ContainerPort{
							{
								Name:          "http",
								ContainerPort: 8080,
								Protocol:      corev1.ProtocolTCP,
							},
							{
								Name:          "https",
								ContainerPort: 8443,
								Protocol:      corev1.ProtocolTCP,
							},
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "kubeconfig",
								ReadOnly:  true,
								MountPath: "/etc/openshift",
							},
							{
								Name:      "mcs-manifests",
								MountPath: "/etc/mcs/bootstrap",
							},
							{
								Name:      "mcc-manifests",
								MountPath: "/etc/mcc/bootstrap",
							},
							{
								Name:      "mcs-tls",
								MountPath: "/etc/ssl/mcs",
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "kubeconfig",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName: "machine-config-server-kubeconfig",
							},
						},
					},
					{
						Name: "mcs-tls",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName: "machine-config-server",
							},
						},
					},
					{
						Name: "mcs-manifests",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
					{
						Name: "mcc-manifests",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
					{
						Name: "config",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "machine-config-server",
								},
							},
						},
					},
				},
			},
		},
	}
	return nil
}

func MachineConfigServerServiceSelector(machineConfigServerName string) map[string]string {
	return map[string]string{
		"app": fmt.Sprintf("machine-config-server-%s", machineConfigServerName),
	}
}
