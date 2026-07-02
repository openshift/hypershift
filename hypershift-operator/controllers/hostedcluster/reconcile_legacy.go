package hostedcluster

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/configrefs"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform"
	hcmetrics "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/metrics"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	controlplanepkioperatormanifests "github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplanepkioperator"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/netutil"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/secretproviderclass"
	"github.com/openshift/hypershift/support/upsert"
	hyperutil "github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/blang/semver"
	"github.com/go-logr/logr"
)

// reconcileLegacy is the original reconcile implementation preserved for
// rollback safety. Activated by setting HYPERSHIFT_RECONCILE_LEGACY=1.
//
//nolint:gocyclo
func (r *HostedClusterReconciler) reconcileLegacy(ctx context.Context, req ctrl.Request, log logr.Logger, hcluster *hyperv1.HostedCluster) (ctrl.Result, error) {
	controlPlaneNamespace := manifests.HostedControlPlaneNamespaceObject(hcluster.Namespace, hcluster.Name)
	hcp := controlplaneoperator.HostedControlPlane(controlPlaneNamespace.Name, hcluster.Name)
	err := r.Client.Get(ctx, client.ObjectKeyFromObject(hcp), hcp)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to get hostedcontrolplane: %w", err)
		} else {
			hcp = nil
		}
	}

	// Bubble up ValidIdentityProvider condition from the hostedControlPlane.
	// We set this condition even if the HC is being deleted. Otherwise, a hostedCluster with a conflicted identity provider
	// would fail to complete deletion forever with no clear signal for consumers.
	if hcluster.Spec.Platform.Type == hyperv1.AWSPlatform {
		updated := false
		var validIdentityProviderCondition *metav1.Condition
		if hcp != nil {
			validIdentityProviderCondition = meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidAWSIdentityProvider))
		}

		// condition not found in HCP or HCP has been deleted
		if validIdentityProviderCondition == nil {
			updated = meta.SetStatusCondition(&hcluster.Status.Conditions, metav1.Condition{
				Type:               string(hyperv1.ValidAWSIdentityProvider),
				Status:             metav1.ConditionUnknown,
				Reason:             hyperv1.StatusUnknownReason,
				ObservedGeneration: hcluster.Generation,
			})
		} else {
			validIdentityProviderCondition.ObservedGeneration = hcluster.Generation
			updated = meta.SetStatusCondition(&hcluster.Status.Conditions, *validIdentityProviderCondition)
		}

		if updated {
			// Persist status updates
			if err := r.Client.Status().Update(ctx, hcluster); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
			}
		}
	}

	// Bubble up AWSDefaultSecurityGroupDeleted condition from the hostedControlPlane to report blocking objects on deletion.
	if condition, changed := computeAWSDefaultSGDeletedCondition(hcluster, hcp); changed {
		meta.SetStatusCondition(&hcluster.Status.Conditions, *condition)
		if err := r.Client.Status().Update(ctx, hcluster); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
		}
	}

	// Bubble up CloudResourcesDestroyed condition from the hostedControlPlane.
	// We set this condition even if the HC is being deleted, so we can construct SLIs for deletion times.
	{
		if hcp != nil && !hcp.DeletionTimestamp.IsZero() {
			freshCondition := &metav1.Condition{
				Type:               string(hyperv1.CloudResourcesDestroyed),
				Status:             metav1.ConditionUnknown,
				Reason:             hyperv1.StatusUnknownReason,
				ObservedGeneration: hcluster.Generation,
			}

			cloudResourcesDestroyedCondition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.CloudResourcesDestroyed))
			if cloudResourcesDestroyedCondition != nil {
				freshCondition = cloudResourcesDestroyedCondition
			}

			oldCondition := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.CloudResourcesDestroyed))
			if oldCondition == nil || oldCondition.Message != freshCondition.Message {
				freshCondition.ObservedGeneration = hcluster.Generation
				meta.SetStatusCondition(&hcluster.Status.Conditions, *freshCondition)
				// Persist status updates
				if err := r.Client.Status().Update(ctx, hcluster); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
				}
			}
		}
	}

	var hcDestroyGracePeriod time.Duration

	if gracePeriodString := hcluster.Annotations[hyperv1.HCDestroyGracePeriodAnnotation]; len(gracePeriodString) > 0 {
		hcDestroyGracePeriod, err = time.ParseDuration(gracePeriodString)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to parse %s annotation: %w", hyperv1.HCDestroyGracePeriodAnnotation, err)
		}
	}

	// If deleted, clean up and return early.
	if !hcluster.DeletionTimestamp.IsZero() {
		// This new condition is necessary for OCM personnel to report any cloud dangling objects to the user.
		// The grace period is customizable using an annotation called HCDestroyGracePeriodAnnotation. It's a time.Duration annotation.
		// This annotation will create a new condition called HostedClusterDestroyed which in conjunction with CloudResourcesDestroyed
		// a SRE could determine if there are dangling objects once the HostedCluster is deleted. These cloud dangling objects will remain
		// in AWS, and SRE will report them to the final user.
		hostedClusterDestroyedCondition := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.HostedClusterDestroyed))
		if hostedClusterDestroyedCondition == nil || hostedClusterDestroyedCondition.Status != metav1.ConditionTrue {
			// Keep trying to delete until we know it's safe to finalize.
			completed, err := r.delete(ctx, hcluster)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete hostedcluster: %w", err)
			}
			if !completed {
				log.Info("hostedcluster is still deleting", "name", req.NamespacedName)
				return ctrl.Result{RequeueAfter: clusterDeletionRequeueDuration}, nil
			}
		}

		// Once the deletion has occurred, we need to clean up cluster-wide resources
		selector := client.MatchingLabelsSelector{Selector: labels.SelectorFromSet(labels.Set{
			controlplanepkioperatormanifests.OwningHostedClusterNamespaceLabel: hcluster.Namespace,
			controlplanepkioperatormanifests.OwningHostedClusterNameLabel:      hcluster.Name,
		})}
		var crs rbacv1.ClusterRoleList
		if err := r.List(ctx, &crs, selector); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to list cluster roles: %w", err)
		}
		if len(crs.Items) > 0 {
			if err := r.DeleteAllOf(ctx, &rbacv1.ClusterRole{}, selector); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete cluster roles: %w", err)
			}
		}
		var crbs rbacv1.ClusterRoleBindingList
		if err := r.List(ctx, &crbs, selector); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to list cluster role bindings: %w", err)
		}
		if len(crbs.Items) > 0 {
			if err := r.DeleteAllOf(ctx, &rbacv1.ClusterRoleBinding{}, selector); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete cluster role bindings: %w", err)
			}
		}

		// Remove any referenced resource annotations for this hosted cluster from secrets and configmaps
		deleteReferencedResourceAnnotation := func(obj client.Object) error {
			annotations := obj.GetAnnotations()
			if annotations == nil {
				return nil
			}
			key := referencedResourceAnnotationPrefix + hcluster.Name
			if _, ok := annotations[key]; !ok {
				return nil
			}
			delete(annotations, key)
			obj.SetAnnotations(annotations)
			if err := r.Update(ctx, obj); err != nil {
				return err
			}
			return nil
		}

		var secretList corev1.SecretList
		if err := r.List(ctx, &secretList, client.InNamespace(hcluster.Namespace)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to list secrets: %w", err)
		}
		for _, secret := range secretList.Items {
			if err := deleteReferencedResourceAnnotation(&secret); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete referenced resource annotation on secret: %w", err)
			}
		}

		var configmapList corev1.ConfigMapList
		if err := r.List(ctx, &configmapList, client.InNamespace(hcluster.Namespace)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to list configmaps: %w", err)
		}
		for _, configmap := range configmapList.Items {
			if err := deleteReferencedResourceAnnotation(&configmap); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete referenced resource annotation on configmap: %w", err)
			}
		}

		if hcDestroyGracePeriod > 0 {
			if hostedClusterDestroyedCondition == nil || hostedClusterDestroyedCondition.Status != metav1.ConditionTrue {
				hostedClusterDestroyedCondition = &metav1.Condition{
					Type:               string(hyperv1.HostedClusterDestroyed),
					Status:             metav1.ConditionTrue,
					Message:            fmt.Sprintf("Grace period set: %v", hcDestroyGracePeriod),
					Reason:             hyperv1.WaitingForGracePeriodReason,
					LastTransitionTime: metav1.NewTime(r.Clock.Now()),
					ObservedGeneration: hcluster.Generation,
				}

				meta.SetStatusCondition(&hcluster.Status.Conditions, *hostedClusterDestroyedCondition)
				if err := r.Client.Status().Update(ctx, hcluster); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
				}
				log.Info("Waiting for grace period", "gracePeriod", hcDestroyGracePeriod)
				return ctrl.Result{RequeueAfter: hcDestroyGracePeriod}, nil
			}

			elapsed := r.Clock.Since(hostedClusterDestroyedCondition.LastTransitionTime.Time)
			if elapsed < hcDestroyGracePeriod {
				log.Info("Waiting for grace period", "gracePeriod", hcDestroyGracePeriod)
				return ctrl.Result{RequeueAfter: hcDestroyGracePeriod - elapsed}, nil
			}
			log.Info("grace period finished", "gracePeriod", hcDestroyGracePeriod)
		}

		// Now we can remove the finalizer.
		if controllerutil.ContainsFinalizer(hcluster, HostedClusterFinalizer) {
			controllerutil.RemoveFinalizer(hcluster, HostedClusterFinalizer)
			if err := r.Update(ctx, hcluster); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from hostedcluster: %w", err)
			}
		}

		log.Info("Deleted hostedcluster", "name", req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// Part zero: fix up conversion
	originalSpec := hcluster.Spec.DeepCopy()

	// Reconcile converted AWS roles.
	if hcluster.Spec.Platform.AWS != nil {
		if err := r.dereferenceAWSRoles(ctx, hcluster.Name, &hcluster.Spec.Platform.AWS.RolesRef, hcluster.Namespace); err != nil {
			return ctrl.Result{}, err
		}
	}
	if hcluster.Spec.SecretEncryption != nil && hcluster.Spec.SecretEncryption.KMS != nil && hcluster.Spec.SecretEncryption.KMS.AWS != nil {
		if strings.HasPrefix(hcluster.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN, "arn-from-secret::") {
			secretName := strings.TrimPrefix(hcluster.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN, "arn-from-secret::")
			arn, err := r.getARNFromSecret(ctx, hcluster.Name, secretName, hcluster.Namespace)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to get ARN from secret %s/%s: %w", hcluster.Namespace, secretName, err)
			}
			hcluster.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN = arn
		}
	}

	createOrUpdate := r.createOrUpdate(req)

	// Reconcile platform defaults
	if err := r.reconcilePlatformDefaultSettings(ctx, hcluster, createOrUpdate, log); err != nil {
		return ctrl.Result{}, err
	}

	// Update fields if required.
	if !equality.Semantic.DeepEqual(&hcluster.Spec, originalSpec) {
		log.Info("Updating deprecated fields for hosted cluster")
		return ctrl.Result{}, r.Client.Update(ctx, hcluster)
	}

	// Part one: update status

	// Set kubeconfig status
	{
		kubeConfigSecret := manifests.KubeConfigSecret(hcluster.Namespace, hcluster.Name)
		err := r.Client.Get(ctx, client.ObjectKeyFromObject(kubeConfigSecret), kubeConfigSecret)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to reconcile kubeconfig secret: %w", err)
			}
		} else {
			hcluster.Status.KubeConfig = &corev1.LocalObjectReference{Name: kubeConfigSecret.Name}
		}
	}

	// Reconcile the ICSP/IDMS from the management cluster
	err = r.RegistryProvider.Reconcile(ctx, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	releaseProvider := r.RegistryProvider.GetReleaseProvider()
	registryClientImageMetadataProvider := r.RegistryProvider.GetMetadataProvider()

	pullSecretBytes, err := hyperutil.GetPullSecretBytes(ctx, r.Client, hcluster)
	if err != nil {
		log.Error(err, "failed to get pull secret")
		// Ensure even with a bad pull secret we process pod placement decisions
		// before returning the pull secret error for requeue.
		hcp = controlplaneoperator.HostedControlPlane(controlPlaneNamespace.Name, hcluster.Name)
		isAutoscalingNeeded, autoscaleErr := r.isAutoscalingNeeded(ctx, hcluster)
		if autoscaleErr != nil {
			log.Error(autoscaleErr, "failed to determine if autoscaler is needed during pull secret recovery, defaulting to true")
			isAutoscalingNeeded = true
		}
		isAWSNodeTerminationHandlerNeeded, nthErr := r.isAWSNodeTerminationHandlerNeeded(ctx, hcluster)
		if nthErr != nil {
			log.Error(nthErr, "failed to determine if AWS node termination handler is needed during pull secret recovery, defaulting to true")
			isAWSNodeTerminationHandlerNeeded = true
		}
		_, hcpErr := createOrUpdate(ctx, r.Client, hcp, func() error {
			// Skip cert annotation resolution during pull secret recovery — it requires
			// the pull secret to resolve the CPO image, which is unavailable here.
			return reconcileHostedControlPlane(hcp, hcluster, isAutoscalingNeeded, isAWSNodeTerminationHandlerNeeded,
				func() (map[string]string, error) { return nil, nil })
		})
		if hcpErr != nil {
			log.Error(hcpErr, "failed to reconcile hostedcontrolplane during pull secret recovery")
		}
		return ctrl.Result{}, fmt.Errorf("failed to get pull secret: %w", err)
	}

	controlPlaneOperatorImage, err := hyperutil.GetControlPlaneOperatorImage(ctx, hcluster, releaseProvider, r.HypershiftOperatorImage, pullSecretBytes)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get controlPlaneOperatorImage: %w", err)
	}
	controlPlaneOperatorImageLabels, err := hyperutil.GetControlPlaneOperatorImageLabels(ctx, hcluster, controlPlaneOperatorImage, pullSecretBytes, registryClientImageMetadataProvider)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get controlPlaneOperatorImageLabels: %w", err)
	}

	_, cpoSupportsKASCustomKubeconfig := controlPlaneOperatorImageLabels[controlPlaneOperatorSupportsKASCustomKubeconfigLabel]

	if cpoSupportsKASCustomKubeconfig {
		if len(hcluster.Spec.KubeAPIServerDNSName) > 0 {
			CustomKubeconfigSecret := manifests.KubeConfigExternalSecret(hcluster.Namespace, hcluster.Name)
			err := r.Client.Get(ctx, client.ObjectKeyFromObject(CustomKubeconfigSecret), CustomKubeconfigSecret)
			if err != nil {
				if !apierrors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("failed to reconcile external kubeconfig secret: %w", err)
				}
			} else {
				hcluster.Status.CustomKubeconfig = &corev1.LocalObjectReference{Name: CustomKubeconfigSecret.Name}
			}
		}
	}

	// Set kubeadminPassword status
	{
		explicitOauthConfig := hcluster.Spec.Configuration != nil && hcluster.Spec.Configuration.OAuth != nil
		if explicitOauthConfig {
			hcluster.Status.KubeadminPassword = nil
		} else {
			kubeadminPasswordSecret := manifests.KubeadminPasswordSecret(hcluster.Namespace, hcluster.Name)
			err := r.Client.Get(ctx, client.ObjectKeyFromObject(kubeadminPasswordSecret), kubeadminPasswordSecret)
			if err != nil {
				if !apierrors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("failed to reconcile kubeadmin password secret: %w", err)
				}
			} else {
				hcluster.Status.KubeadminPassword = &corev1.LocalObjectReference{Name: kubeadminPasswordSecret.Name}
			}
		}
	}

	// Set version status
	hcluster.Status.Version = computeClusterVersionStatus(r.Clock, hcluster, hcp)

	// Copy the CVO conditions from the HCP.
	hcpCVOConditions := map[hyperv1.ConditionType]*metav1.Condition{
		hyperv1.ClusterVersionSucceeding:       nil,
		hyperv1.ClusterVersionProgressing:      nil,
		hyperv1.ClusterVersionReleaseAccepted:  nil,
		hyperv1.ClusterVersionRetrievedUpdates: nil,
		hyperv1.ClusterVersionUpgradeable:      nil,
		hyperv1.ClusterVersionAvailable:        nil,
	}
	if hcp != nil {
		hcpCVOConditions = map[hyperv1.ConditionType]*metav1.Condition{
			hyperv1.ClusterVersionSucceeding:       meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionFailing)),
			hyperv1.ClusterVersionProgressing:      meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionProgressing)),
			hyperv1.ClusterVersionReleaseAccepted:  meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionReleaseAccepted)),
			hyperv1.ClusterVersionRetrievedUpdates: meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionRetrievedUpdates)),
			hyperv1.ClusterVersionUpgradeable:      meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionUpgradeable)),
			hyperv1.ClusterVersionAvailable:        meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionAvailable)),
		}
	}

	for conditionType := range hcpCVOConditions {
		var hcCVOCondition *metav1.Condition
		// Set unknown status.
		var unknownStatusMessage string
		if hcpCVOConditions[conditionType] == nil {
			unknownStatusMessage = "Condition not found in the CVO."
		}

		hcCVOCondition = &metav1.Condition{
			Type:               string(conditionType),
			Status:             metav1.ConditionUnknown,
			Reason:             hyperv1.StatusUnknownReason,
			Message:            unknownStatusMessage,
			ObservedGeneration: hcluster.Generation,
		}

		if hcp != nil && hcpCVOConditions[conditionType] != nil {
			// Bubble up info from HCP.
			hcCVOCondition = hcpCVOConditions[conditionType]
			hcCVOCondition.ObservedGeneration = hcluster.Generation

			// Inverse ClusterVersionFailing condition into ClusterVersionSucceeding
			// So consumers e.g. UI can categorize as good (True) / bad (False).
			if conditionType == hyperv1.ClusterVersionSucceeding {
				hcCVOCondition.Type = string(hyperv1.ClusterVersionSucceeding)
				var status metav1.ConditionStatus
				switch hcpCVOConditions[conditionType].Status {
				case metav1.ConditionTrue:
					status = metav1.ConditionFalse
				case metav1.ConditionFalse:
					status = metav1.ConditionTrue
				}
				hcCVOCondition.Status = status
			}
		}

		if hcCVOCondition.Type == string(hyperv1.ClusterVersionRetrievedUpdates) && hcCVOCondition.Reason == hyperv1.StatusUnknownReason {
			// until all HostedControlPlane controllers understand how to propagate this condition, avoid bothering folks with unknown status in HostedCluster conditions.
			meta.RemoveStatusCondition(&hcluster.Status.Conditions, string(hyperv1.ClusterVersionRetrievedUpdates))
			continue
		}

		meta.SetStatusCondition(&hcluster.Status.Conditions, *hcCVOCondition)
	}

	// Copy the Degraded condition on the hostedcontrolplane
	{
		condition := &metav1.Condition{
			Type:               string(hyperv1.HostedClusterDegraded),
			Status:             metav1.ConditionUnknown,
			Reason:             hyperv1.StatusUnknownReason,
			Message:            "The hosted control plane is not found",
			ObservedGeneration: hcluster.Generation,
		}
		if hcp != nil {
			degradedCondition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.HostedControlPlaneDegraded))
			if degradedCondition != nil {
				condition = degradedCondition
				condition.Type = string(hyperv1.HostedClusterDegraded)
				if condition.Status == metav1.ConditionFalse {
					condition.Message = "The hosted cluster is not degraded"
				}
			}
		}
		condition.ObservedGeneration = hcluster.Generation
		meta.SetStatusCondition(&hcluster.Status.Conditions, *condition)
	}

	// Copy the ValidKubeVirtInfraNetworkMTU condition from the HostedControlPlane
	if hcluster.Spec.Platform.Type == hyperv1.KubevirtPlatform {
		if hcp != nil {
			validMtuCondCreated := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidKubeVirtInfraNetworkMTU))
			if validMtuCondCreated != nil {
				validMtuCondCreated.ObservedGeneration = hcluster.Generation
				meta.SetStatusCondition(&hcluster.Status.Conditions, *validMtuCondCreated)
			}
		}
		if err := r.syncKVLiveMigratableCondition(ctx, hcluster); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update condition: %w", err)
		}
	}

	// Copy conditions from hostedcontrolplane
	{
		hcpConditions := []hyperv1.ConditionType{
			hyperv1.EtcdAvailable,
			hyperv1.KubeAPIServerAvailable,
			hyperv1.InfrastructureReady,
			hyperv1.ExternalDNSReachable,
			hyperv1.ValidHostedControlPlaneConfiguration,
			hyperv1.ValidReleaseInfo,
			hyperv1.ValidIDPConfiguration,
			hyperv1.HostedClusterRestoredFromBackup,
			hyperv1.DataPlaneConnectionAvailable,
			hyperv1.ControlPlaneConnectionAvailable,
			hyperv1.EtcdBackupSucceeded,
		}

		for _, conditionType := range hcpConditions {
			condition := &metav1.Condition{
				Type:               string(conditionType),
				Status:             metav1.ConditionUnknown,
				Reason:             hyperv1.StatusUnknownReason,
				Message:            "The hosted control plane is not found",
				ObservedGeneration: hcluster.Generation,
			}
			if hcp != nil {
				hcpCondition := meta.FindStatusCondition(hcp.Status.Conditions, string(conditionType))
				if hcpCondition != nil {
					condition = hcpCondition
				} else {
					condition.Message = "Condition not found in the HCP"
				}
			}
			condition.ObservedGeneration = hcluster.Generation
			meta.SetStatusCondition(&hcluster.Status.Conditions, *condition)
		}
	}

	// Copy the platform status from the hostedcontrolplane
	if hcp != nil {
		hcluster.Status.Platform = hcp.Status.Platform
		hcluster.Status.AutoNode = hcp.Status.AutoNode
	}

	// Copy the secret encryption status from the hostedcontrolplane
	if hcp != nil {
		hcp.Status.SecretEncryption.DeepCopyInto(&hcluster.Status.SecretEncryption)
	}

	// Copy the control plane version status from the hostedcontrolplane
	propagateControlPlaneVersion(hcluster, hcp)

	// Set the AutoNodeEnabled condition reflecting both spec intent and actual component rollout progress.
	autoNodeCondition, autoNodeProgressing := r.reconcileAutoNodeEnabledCondition(ctx, hcluster, controlPlaneNamespace.Name)
	meta.SetStatusCondition(&hcluster.Status.Conditions, autoNodeCondition)

	// Copy the AWSDefaultSecurityGroupCreated condition from the hostedcontrolplane
	if hcluster.Spec.Platform.Type == hyperv1.AWSPlatform {
		if hcp != nil {
			sgCreated := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.AWSDefaultSecurityGroupCreated))
			if sgCreated != nil {
				sgCreated.ObservedGeneration = hcluster.Generation
				meta.SetStatusCondition(&hcluster.Status.Conditions, *sgCreated)
			}

			validKMSConfig := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidAWSKMSConfig))
			if validKMSConfig != nil {
				validKMSConfig.ObservedGeneration = hcluster.Generation
				meta.SetStatusCondition(&hcluster.Status.Conditions, *validKMSConfig)
			}
		}
	}

	if hcluster.Spec.Platform.Type == hyperv1.AzurePlatform {
		if hcp != nil {
			validKMSConfig := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidAzureKMSConfig))
			if validKMSConfig != nil {
				validKMSConfig.ObservedGeneration = hcluster.Generation
				meta.SetStatusCondition(&hcluster.Status.Conditions, *validKMSConfig)
			}
		}
	}

	// Copy the EtcdDataEncryptionUpToDate condition from the HostedControlPlane
	if hcp != nil {
		encryptionCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))
		if encryptionCond != nil {
			encryptionCond.ObservedGeneration = hcluster.Generation
			meta.SetStatusCondition(&hcluster.Status.Conditions, *encryptionCond)
		}
	}

	// Reconcile unmanaged etcd client tls secret validation error status. Note only update status on validation error case to
	// provide clear status to the user on the resource without having to look at operator logs.
	{
		if hcluster.Spec.Etcd.ManagementType == hyperv1.Unmanaged {
			unmanagedEtcdTLSClientSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: hcluster.GetNamespace(),
					Name:      hcluster.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name,
				},
			}
			if err := r.Client.Get(ctx, client.ObjectKeyFromObject(unmanagedEtcdTLSClientSecret), unmanagedEtcdTLSClientSecret); err != nil {
				if apierrors.IsNotFound(err) {
					unmanagedEtcdTLSClientSecret = nil
				} else {
					return ctrl.Result{}, fmt.Errorf("failed to get unmanaged etcd tls secret: %w", err)
				}
			}
			meta.SetStatusCondition(&hcluster.Status.Conditions, computeUnmanagedEtcdAvailability(hcluster, unmanagedEtcdTLSClientSecret))
		}
	}

	// Set the Available condition
	// TODO: This is really setting something that could be more granular like
	// HostedControlPlaneAvailable, and then the HostedCluster high-level Available
	// condition could be computed as a function of the granular ThingAvailable
	// conditions (so that it could incorporate e.g. HostedControlPlane and IgnitionServer
	// availability in the ultimate HostedCluster Available condition)
	{
		availableCondition := computeHostedClusterAvailability(hcluster, hcp)
		_, isHasBeenAvailableAnnotationSet := hcluster.Annotations[hcmetrics.HasBeenAvailableAnnotation]

		meta.SetStatusCondition(&hcluster.Status.Conditions, availableCondition)

		if availableCondition.Status == metav1.ConditionTrue && !isHasBeenAvailableAnnotationSet {
			original := hcluster.DeepCopy()

			if hcluster.Annotations == nil {
				hcluster.Annotations = make(map[string]string)
			}

			hcluster.Annotations[hcmetrics.HasBeenAvailableAnnotation] = "true"

			if err := r.Patch(ctx, hcluster, client.MergeFromWithOptions(original)); err != nil {
				return ctrl.Result{}, fmt.Errorf("cannot patch hosted cluster with has been available annotation: %w", err)
			}
		}
	}

	// Copy AWSEndpointAvailable and AWSEndpointServiceAvailable conditions from the AWSEndpointServices.
	if hcluster.Spec.Platform.Type == hyperv1.AWSPlatform {
		hcpNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
		var awsEndpointServiceList hyperv1.AWSEndpointServiceList
		if err := r.List(ctx, &awsEndpointServiceList, &client.ListOptions{Namespace: hcpNamespace}); err != nil {
			condition := metav1.Condition{
				Type:    string(hyperv1.AWSEndpointAvailable),
				Status:  metav1.ConditionUnknown,
				Reason:  hyperv1.NotFoundReason,
				Message: fmt.Sprintf("error listing awsendpointservices in namespace %s: %v", hcpNamespace, err),
			}
			meta.SetStatusCondition(&hcluster.Status.Conditions, condition)
		} else {
			meta.SetStatusCondition(&hcluster.Status.Conditions, computeAWSEndpointServiceCondition(awsEndpointServiceList, hyperv1.AWSEndpointAvailable))
			meta.SetStatusCondition(&hcluster.Status.Conditions, computeAWSEndpointServiceCondition(awsEndpointServiceList, hyperv1.AWSEndpointServiceAvailable))
		}
	}

	// Copy GCPEndpointAvailable and GCPServiceAttachmentAvailable conditions from the GCPPrivateServiceConnect resources.
	if hcluster.Spec.Platform.Type == hyperv1.GCPPlatform {
		hcpNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
		var gcpPSCList hyperv1.GCPPrivateServiceConnectList
		if err := r.List(ctx, &gcpPSCList, &client.ListOptions{Namespace: hcpNamespace}); err != nil {
			condition := metav1.Condition{
				Type:    string(hyperv1.GCPEndpointAvailable),
				Status:  metav1.ConditionUnknown,
				Reason:  hyperv1.NotFoundReason,
				Message: fmt.Sprintf("error listing GCPPrivateServiceConnect in namespace %s: %v", hcpNamespace, err),
			}
			meta.SetStatusCondition(&hcluster.Status.Conditions, condition)
		} else {
			meta.SetStatusCondition(&hcluster.Status.Conditions, computeGCPPSCCondition(gcpPSCList, hyperv1.GCPEndpointAvailable))
			meta.SetStatusCondition(&hcluster.Status.Conditions, computeGCPPSCCondition(gcpPSCList, hyperv1.GCPServiceAttachmentAvailable))
		}
	}

	// Copy Azure Private Link conditions from the AzurePrivateLinkService resources.
	// ARO HCP uses Swift networking, not Private Link Services.
	if hcluster.Spec.Platform.Type == hyperv1.AzurePlatform && !netutil.UseSwiftNetworkingHC(hcluster) {
		hcpNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
		var azPLSList hyperv1.AzurePrivateLinkServiceList
		if err := r.List(ctx, &azPLSList, &client.ListOptions{Namespace: hcpNamespace}); err != nil {
			condition := metav1.Condition{
				Type:    string(hyperv1.AzurePrivateLinkServiceAvailable),
				Status:  metav1.ConditionUnknown,
				Reason:  hyperv1.NotFoundReason,
				Message: fmt.Sprintf("error listing AzurePrivateLinkService in namespace %s: %v", hcpNamespace, err),
			}
			meta.SetStatusCondition(&hcluster.Status.Conditions, condition)
		} else if len(azPLSList.Items) > 0 {
			meta.SetStatusCondition(&hcluster.Status.Conditions, computeAzurePLSCondition(azPLSList, hyperv1.AzurePrivateLinkServiceAvailable))
			meta.SetStatusCondition(&hcluster.Status.Conditions, computeAzurePLSCondition(azPLSList, hyperv1.AzurePLSCreated))
			meta.SetStatusCondition(&hcluster.Status.Conditions, computeAzurePLSCondition(azPLSList, hyperv1.AzureInternalLoadBalancerAvailable))
			meta.SetStatusCondition(&hcluster.Status.Conditions, computeAzurePLSCondition(azPLSList, hyperv1.AzurePrivateEndpointAvailable))
			meta.SetStatusCondition(&hcluster.Status.Conditions, computeAzurePLSCondition(azPLSList, hyperv1.AzurePrivateDNSAvailable))
		}
	}

	// Set ValidConfiguration condition
	{
		condition := metav1.Condition{
			Type:               string(hyperv1.ValidHostedClusterConfiguration),
			ObservedGeneration: hcluster.Generation,
		}
		if err := r.validateConfigAndClusterCapabilities(ctx, hcluster); err != nil {
			condition.Status = metav1.ConditionFalse
			condition.Message = err.Error()
			condition.Reason = hyperv1.InvalidConfigurationReason
		} else {
			condition.Status = metav1.ConditionTrue
			condition.Message = "Configuration passes validation"
			condition.Reason = hyperv1.AsExpectedReason
		}
		meta.SetStatusCondition(&hcluster.Status.Conditions, condition)
	}

	// Set SupportedHostedCluster condition
	{
		condition := metav1.Condition{
			Type:               string(hyperv1.SupportedHostedCluster),
			ObservedGeneration: hcluster.Generation,
		}
		if err := r.validateHostedClusterSupport(hcluster); err != nil {
			condition.Status = metav1.ConditionFalse
			condition.Message = err.Error()
			condition.Reason = hyperv1.UnsupportedHostedClusterReason
		} else {
			condition.Status = metav1.ConditionTrue
			condition.Message = "HostedCluster is supported by operator configuration"
			condition.Reason = hyperv1.AsExpectedReason
		}
		meta.SetStatusCondition(&hcluster.Status.Conditions, condition)
	}

	// Set ValidProxyConfiguration condition
	{
		condition := metav1.Condition{
			Type:               string(hyperv1.ValidProxyConfiguration),
			ObservedGeneration: hcluster.Generation,
		}
		if hcluster.Spec.Configuration != nil && hcluster.Spec.Configuration.Proxy != nil && hcluster.Spec.Configuration.Proxy.TrustedCA.Name != "" {
			if err := r.validateProxyConfiguration(ctx, hcluster); err != nil {
				condition.Status = metav1.ConditionFalse
				condition.Message = fmt.Sprintf("Proxy CA bundle is invalid for HostedCluster %s/%s: %v", hcluster.Namespace, hcluster.Name, err)
				condition.Reason = hyperv1.ProxyCABundleInvalidReason
				log.Info("Proxy CA bundle validation failed", "namespace", hcluster.Namespace, "name", hcluster.Name, "error", err)
			} else {
				condition.Status = metav1.ConditionTrue
				condition.Message = "Proxy CA bundle is valid"
				condition.Reason = hyperv1.AsExpectedReason
			}
		} else {
			// No proxy configured or no CA bundle
			condition.Status = metav1.ConditionTrue
			condition.Message = "No proxy CA bundle configured"
			condition.Reason = hyperv1.AsExpectedReason
		}
		meta.SetStatusCondition(&hcluster.Status.Conditions, condition)
	}

	// Set Ignition Server endpoint
	{
		serviceStrategy := servicePublishingStrategyByType(hcluster, hyperv1.Ignition)
		if serviceStrategy == nil {
			// We don't return the error here as reconciling won't solve the input problem.
			// An update event will trigger reconciliation.
			log.Error(fmt.Errorf("ignition server service strategy not specified"), "")
			return ctrl.Result{}, nil
		}
		switch serviceStrategy.Type {
		case hyperv1.Route:
			if serviceStrategy.Route != nil && serviceStrategy.Route.Hostname != "" {
				hcluster.Status.IgnitionEndpoint = serviceStrategy.Route.Hostname
			} else {
				ignitionServerRoute := ignitionserver.Route(controlPlaneNamespace.GetName())
				if err := r.Client.Get(ctx, client.ObjectKeyFromObject(ignitionServerRoute), ignitionServerRoute); err != nil {
					if !apierrors.IsNotFound(err) {
						return ctrl.Result{}, fmt.Errorf("failed to get ignitionServerRoute: %w", err)
					}
				}
				if ignitionServerRoute.Spec.Host != "" {
					hcluster.Status.IgnitionEndpoint = ignitionServerRoute.Spec.Host
				}
			}
		case hyperv1.NodePort:
			if serviceStrategy.NodePort == nil {
				// We don't return the error here as reconciling won't solve the input problem.
				// An update event will trigger reconciliation.
				log.Error(fmt.Errorf("nodeport metadata not specified for ignition service"), "")
				return ctrl.Result{}, nil
			}
			ignitionService := ignitionserver.ProxyService(controlPlaneNamespace.GetName())
			if err = r.Client.Get(ctx, client.ObjectKeyFromObject(ignitionService), ignitionService); err != nil {
				if !apierrors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("failed to get ignition proxy service: %w", err)
				} else {
					// ignition-server-proxy service not found, possible IBM platform or older CPO that doesn't create the service
					ignitionService = ignitionserver.Service(controlPlaneNamespace.GetName())
					if err = r.Client.Get(ctx, client.ObjectKeyFromObject(ignitionService), ignitionService); err != nil {
						if !apierrors.IsNotFound(err) {
							return ctrl.Result{}, fmt.Errorf("failed to get ignition service: %w", err)
						}
					}
				}
			}
			if err == nil && serviceFirstNodePortAvailable(ignitionService) {
				hcluster.Status.IgnitionEndpoint = fmt.Sprintf("%s:%d", serviceStrategy.NodePort.Address, ignitionService.Spec.Ports[0].NodePort)
			}
		default:
			// We don't return the error here as reconciling won't solve the input problem.
			// An update event will trigger reconciliation.
			log.Error(fmt.Errorf("unknown service strategy type for ignition service: %s", serviceStrategy.Type), "")
			return ctrl.Result{}, nil
		}
	}

	// Set the Control Plane and OAuth endpoints URL
	{
		if hcp != nil {
			hcluster.Status.ControlPlaneEndpoint = hcp.Status.ControlPlaneEndpoint

			// TODO: (cewong) Remove this hack when we no longer need to support HostedControlPlanes that report
			// the wrong port for the route strategy.
			if isAPIServerRoute(hcluster) {
				hcluster.Status.ControlPlaneEndpoint.Port = 443
			}
			hcluster.Status.OAuthCallbackURLTemplate = hcp.Status.OAuthCallbackURLTemplate
		}
	}

	// Set the ignition server availability condition by checking its deployment.
	{
		// Assume the server is unavailable unless proven otherwise.
		newCondition := metav1.Condition{
			Type:   string(hyperv1.IgnitionEndpointAvailable),
			Status: metav1.ConditionUnknown,
			Reason: hyperv1.StatusUnknownReason,
		}
		// Check to ensure the deployment exists and is available.
		deployment := ignitionserver.Deployment(controlPlaneNamespace.Name)
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(deployment), deployment); err != nil {
			if apierrors.IsNotFound(err) {
				newCondition = metav1.Condition{
					Type:    string(hyperv1.IgnitionEndpointAvailable),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.NotFoundReason,
					Message: "Ignition server deployment not found",
				}
			} else {
				return ctrl.Result{}, fmt.Errorf("failed to get ignition server deployment: %w", err)
			}
		} else {
			// Assume the deployment is unavailable until proven otherwise.
			newCondition = metav1.Condition{
				Type:    string(hyperv1.IgnitionEndpointAvailable),
				Status:  metav1.ConditionFalse,
				Reason:  hyperv1.WaitingForAvailableReason,
				Message: "Ignition server deployment is not yet available",
			}
			for _, cond := range deployment.Status.Conditions {
				if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
					newCondition = metav1.Condition{
						Type:    string(hyperv1.IgnitionEndpointAvailable),
						Status:  metav1.ConditionTrue,
						Reason:  hyperv1.AsExpectedReason,
						Message: "Ignition server deployment is available",
					}
					break
				}
			}
		}
		newCondition.ObservedGeneration = hcluster.Generation
		meta.SetStatusCondition(&hcluster.Status.Conditions, newCondition)
	}
	meta.SetStatusCondition(&hcluster.Status.Conditions, hyperutil.GenerateReconciliationActiveCondition(hcluster.Spec.PausedUntil, hcluster.Generation))

	// Set ValidReleaseImage condition
	{
		condition := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.ValidReleaseImage))

		// This check can be expensive looking up release image versions
		// (hopefully they are cached).  Skip if we have already observed for
		// this generation.
		if condition == nil || condition.ObservedGeneration != hcluster.Generation || condition.Status != metav1.ConditionTrue {
			condition := metav1.Condition{
				Type:               string(hyperv1.ValidReleaseImage),
				ObservedGeneration: hcluster.Generation,
			}
			err := r.validateReleaseImage(ctx, hcluster, releaseProvider)
			if err != nil {
				condition.Status = metav1.ConditionFalse
				condition.Message = err.Error()

				if apierrors.IsNotFound(err) {
					condition.Reason = hyperv1.SecretNotFoundReason
				} else {
					condition.Reason = hyperv1.InvalidImageReason
				}
			} else {
				condition.Status = metav1.ConditionTrue
				condition.Message = "Release image is valid"
				condition.Reason = hyperv1.AsExpectedReason
			}
			meta.SetStatusCondition(&hcluster.Status.Conditions, condition)
		}
	}

	// Set HostedCluster payload arch
	payloadArch, err := hyperutil.DetermineHostedClusterPayloadArch(ctx, r.Client, hcluster, registryClientImageMetadataProvider)
	if err != nil {
		condition := metav1.Condition{
			Type:               string(hyperv1.ValidReleaseImage),
			ObservedGeneration: hcluster.Generation,
		}
		condition.Status = metav1.ConditionFalse
		condition.Message = err.Error()
		condition.Reason = hyperv1.PayloadArchNotFoundReason
		meta.SetStatusCondition(&hcluster.Status.Conditions, condition)

		return ctrl.Result{}, err
	}

	hcluster.Status.PayloadArch = payloadArch

	releaseImage, err := r.lookupReleaseImage(ctx, hcluster, releaseProvider)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to lookup release image: %w", err)
	}
	// Set Progressing condition
	{
		condition := metav1.Condition{
			Type:               string(hyperv1.HostedClusterProgressing),
			ObservedGeneration: hcluster.Generation,
			Status:             metav1.ConditionFalse,
			Message:            "HostedCluster is at expected version",
			Reason:             hyperv1.AsExpectedReason,
		}
		refWithDigest := func() (string, error) {
			_, ref, err := registryClientImageMetadataProvider.GetDigest(ctx, hcluster.Spec.Release.Image, pullSecretBytes)
			if err != nil {
				return "", err
			}
			return ref.String(), nil
		}

		progressing, err := isProgressing(hcluster, releaseImage, refWithDigest)
		if err != nil {
			condition.Status = metav1.ConditionFalse
			condition.Message = err.Error()
			condition.Reason = hyperv1.BlockedReason
		}
		if progressing {
			condition.Status = metav1.ConditionTrue
			condition.Message = "HostedCluster is deploying, upgrading, or reconfiguring"
			condition.Reason = "Progressing"
		}
		meta.SetStatusCondition(&hcluster.Status.Conditions, condition)
	}

	// Copy the configuration status from the hostedcontrolplane
	if hcp != nil {
		hcluster.Status.Configuration = hcp.Status.Configuration
	}

	// Persist status updates
	if err := r.Client.Status().Update(ctx, hcluster); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	// Part two: reconcile the state of the world

	// Ensure the cluster has a finalizer for cleanup and update right away.
	if !controllerutil.ContainsFinalizer(hcluster, HostedClusterFinalizer) {
		controllerutil.AddFinalizer(hcluster, HostedClusterFinalizer)
		if err := r.Update(ctx, hcluster); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to cluster: %w", err)
		}
	}

	// Ensure the CAPI deployments have finalizers
	if err := r.reconcileCAPIFinalizers(ctx, hcluster, false); err != nil {
		return ctrl.Result{}, fmt.Errorf("error adding finalizers to CAPI deployments: %w", err)
	}

	// if paused: ensure associated HostedControlPlane (if it exists) is also paused and stop reconciliation
	if isPaused, duration := hyperutil.IsReconciliationPaused(log, hcluster.Spec.PausedUntil); isPaused {
		if err := pauseHostedControlPlane(ctx, r.Client, hcp, hcluster.Spec.PausedUntil); err != nil {
			return ctrl.Result{}, err
		}
		if err := pauseCAPICluster(ctx, r.Client, hcluster, true); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Reconciliation paused", "name", req.NamespacedName, "pausedUntil", *hcluster.Spec.PausedUntil)
		return ctrl.Result{RequeueAfter: duration}, nil
	}

	if err := r.defaultClusterIDsIfNeeded(ctx, hcluster); err != nil {
		return ctrl.Result{}, err
	}

	if err = r.reconcileCLISecrets(ctx, createOrUpdate, hcluster); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile the CLI secrets: %w", err)
	}

	// Set the infraID as Tag on all created AWS
	if err := r.reconcileAWSResourceTags(ctx, hcluster); err != nil {
		return ctrl.Result{}, err
	}

	// Block here if the cluster configuration does not pass validation
	{
		validConfig := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.ValidHostedClusterConfiguration))
		if validConfig != nil && validConfig.Status == metav1.ConditionFalse {
			// an error should be returned here because the ValidHostedClusterConfiguration status may be transient
			return ctrl.Result{}, fmt.Errorf("configuration is invalid: %s", validConfig.Message)
		}
		supportedHostedCluster := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.SupportedHostedCluster))
		if supportedHostedCluster != nil && supportedHostedCluster.Status == metav1.ConditionFalse {
			log.Error(fmt.Errorf("not supported by operator configuration"), "reconciliation is blocked", "message", supportedHostedCluster.Message)
			return ctrl.Result{}, nil
		}
		validReleaseImage := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.ValidReleaseImage))
		if validReleaseImage != nil && validReleaseImage.Status == metav1.ConditionFalse {
			if validReleaseImage.Reason == hyperv1.SecretNotFoundReason {
				return ctrl.Result{}, fmt.Errorf("%s", validReleaseImage.Message)
			}
			log.Error(fmt.Errorf("release image is invalid"), "reconciliation is blocked", "message", validReleaseImage.Message)
			return ctrl.Result{}, nil
		}
		upgrading, msg, err := isUpgrading(hcluster, releaseImage)
		if upgrading {
			if err != nil {
				log.Error(err, "reconciliation is blocked", "message", validReleaseImage.Message)
				return ctrl.Result{}, nil
			}
			if msg != "" {
				log.Info(msg)
			}
		}
	}

	cpoHasUtilities := false
	if _, hasLabel := controlPlaneOperatorImageLabels[controlPlaneOperatorSubcommandsLabel]; hasLabel {
		cpoHasUtilities = true
	}
	utilitiesImage := controlPlaneOperatorImage
	if !cpoHasUtilities {
		utilitiesImage = r.HypershiftOperatorImage
	}

	_, controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel := controlPlaneOperatorImageLabels[controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel]
	_, controlPlanePKIOperatorSignsCSRs := controlPlaneOperatorImageLabels[controlPlanePKIOperatorSignsCSRsLabel]
	_, useRestrictedPSA := controlPlaneOperatorImageLabels[useRestrictedPodSecurityLabel]
	_, defaultToControlPlaneV2 := controlPlaneOperatorImageLabels[defaultToControlPlaneV2Label]

	// Reconcile the hosted cluster namespace
	_, err = createOrUpdate(ctx, r.Client, controlPlaneNamespace, func() error {
		if controlPlaneNamespace.Labels == nil {
			controlPlaneNamespace.Labels = make(map[string]string)
		}
		controlPlaneNamespace.Labels[ControlPlaneNamespaceLabelKey] = "true"

		// Set pod security labels on HCP namespace
		psaOverride := hcluster.Annotations[hyperv1.PodSecurityAdmissionLabelOverrideAnnotation]
		if psaOverride != "" {
			controlPlaneNamespace.Labels["pod-security.kubernetes.io/enforce"] = psaOverride
			controlPlaneNamespace.Labels["pod-security.kubernetes.io/audit"] = psaOverride
			controlPlaneNamespace.Labels["pod-security.kubernetes.io/warn"] = psaOverride
		} else if useRestrictedPSA {
			controlPlaneNamespace.Labels["pod-security.kubernetes.io/enforce"] = "restricted"
			controlPlaneNamespace.Labels["pod-security.kubernetes.io/audit"] = "restricted"
			controlPlaneNamespace.Labels["pod-security.kubernetes.io/warn"] = "restricted"
		} else {
			controlPlaneNamespace.Labels["pod-security.kubernetes.io/enforce"] = "privileged"
			controlPlaneNamespace.Labels["pod-security.kubernetes.io/audit"] = "privileged"
			controlPlaneNamespace.Labels["pod-security.kubernetes.io/warn"] = "privileged"
		}
		controlPlaneNamespace.Labels["security.openshift.io/scc.podSecurityLabelSync"] = "false"

		// Enable monitoring for hosted control plane namespaces
		if r.EnableOCPClusterMonitoring {
			controlPlaneNamespace.Labels["openshift.io/cluster-monitoring"] = "true"
		}

		if r.SetDefaultSecurityContext {
			// Only set the SecurtyContext UID annotation if it's not already set.
			_, ok := controlPlaneNamespace.Annotations[DefaultSecurityContextUIDAnnnotation]
			if !ok {
				uid, err := getNextAvailableSecurityContextUID(ctx, r.Client)
				if err != nil {
					return fmt.Errorf("failed to get next available SecurityContext UID: %w", err)
				}
				if controlPlaneNamespace.Annotations == nil {
					controlPlaneNamespace.Annotations = make(map[string]string)
				}
				controlPlaneNamespace.Annotations[DefaultSecurityContextUIDAnnnotation] = strconv.FormatInt(uid, 10)
			}
		}

		// Enable observability operator monitoring
		metrics.EnableOBOMonitoring(controlPlaneNamespace)

		propagateAzureResourceIDAnnotation(hcluster, controlPlaneNamespace)

		return nil
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile namespace: %w", err)
	}

	p, err := platform.GetPlatform(ctx, hcluster, releaseProvider, utilitiesImage, pullSecretBytes)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Reconcile Platform specifics.
	{
		if err := p.ReconcileCredentials(ctx, r.Client, createOrUpdate, hcluster, controlPlaneNamespace.Name); err != nil {
			meta.SetStatusCondition(&hcluster.Status.Conditions, metav1.Condition{
				Type:               string(hyperv1.PlatformCredentialsFound),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.PlatformCredentialsNotFoundReason,
				ObservedGeneration: hcluster.Generation,
				Message:            err.Error(),
			})
			if statusErr := r.Client.Status().Update(ctx, hcluster); statusErr != nil {
				return ctrl.Result{}, fmt.Errorf("failed to reconcile platform credentials: %w, failed to update status: %w", err, statusErr)
			}
			return ctrl.Result{}, fmt.Errorf("failed to reconcile platform credentials: %w", err)
		}
		if !meta.IsStatusConditionTrue(hcluster.Status.Conditions, string(hyperv1.PlatformCredentialsFound)) {
			meta.SetStatusCondition(&hcluster.Status.Conditions, metav1.Condition{
				Type:               string(hyperv1.PlatformCredentialsFound),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1.AsExpectedReason,
				ObservedGeneration: hcluster.Generation,
				Message:            "Required platform credentials are found",
			})
			if statusErr := r.Client.Status().Update(ctx, hcluster); statusErr != nil {
				return ctrl.Result{}, fmt.Errorf("failed to reconcile platform credentials: %w, failed to update status: %w", err, statusErr)
			}
		}
	}

	// Set the HostedCluster restored from backup condition
	{
		if _, exists := hcluster.Annotations[hyperv1.HostedClusterRestoredFromBackupAnnotation]; exists {
			freshCondition := &metav1.Condition{
				Type:               string(hyperv1.HostedClusterRestoredFromBackup),
				Reason:             hyperv1.RecoveryFinishedReason,
				Status:             metav1.ConditionUnknown,
				ObservedGeneration: hcluster.Generation,
			}

			if hcp != nil {
				hostedClusterRestoredFromBackupCondition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.HostedClusterRestoredFromBackup))
				if hostedClusterRestoredFromBackupCondition != nil {
					freshCondition = hostedClusterRestoredFromBackupCondition
				}
			}

			oldCondition := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.HostedClusterRestoredFromBackup))

			// Preserve previous status if we can no longer determine the status
			if oldCondition != nil && freshCondition.Status == metav1.ConditionUnknown {
				freshCondition.Status = oldCondition.Status
			}

			// If the condition is not set, or the status is different, set the condition
			if oldCondition == nil || oldCondition.Status != freshCondition.Status {
				freshCondition.ObservedGeneration = hcluster.Generation
			}

			// If the condition is true, delete the hc annotation. It will be eventually bubbled down to the hcp.
			if freshCondition.Status == metav1.ConditionTrue {
				hclusterAnnotations := hcluster.GetAnnotations()
				delete(hclusterAnnotations, hyperv1.HostedClusterRestoredFromBackupAnnotation)
				hcluster.SetAnnotations(hclusterAnnotations)
				if err := r.Client.Update(ctx, hcluster); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to remove annotations %v: %w", string(hyperv1.HostedClusterRestoredFromBackup), err)
				}
			}

			// Persist status updates
			meta.SetStatusCondition(&hcluster.Status.Conditions, *freshCondition)
			if err := r.Client.Status().Update(ctx, hcluster); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update status %v: %w", string(hyperv1.HostedClusterRestoredFromBackup), err)
			}
		}
	}

	// Reconcile the HostedControlPlane pull secret by resolving the source secret
	// reference from the HostedCluster and syncing the secret in the control plane namespace.
	{
		var src corev1.Secret
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.PullSecret.Name}, &src); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get pull secret %s: %w", hcluster.Spec.PullSecret.Name, err)
		}
		if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcluster.Name, &src); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set referenced resource annotation: %w", err)
		}
		dst := controlplaneoperator.PullSecret(controlPlaneNamespace.Name)
		_, err = createOrUpdate(ctx, r.Client, dst, func() error {
			srcData, srcHasData := src.Data[".dockerconfigjson"]
			if !srcHasData {
				return fmt.Errorf("hostedcluster pull secret %q must have a .dockerconfigjson key", src.Name)
			}
			dst.Type = corev1.SecretTypeDockerConfigJson
			if dst.Data == nil {
				dst.Data = map[string][]byte{}
			}
			dst.Data[".dockerconfigjson"] = srcData
			return nil
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile pull secret: %w", err)
		}
	}

	// Reconcile the HostedControlPlane Secret Encryption Info
	if hcluster.Spec.SecretEncryption != nil {
		log.Info("Reconciling secret encryption configuration")
		switch hcluster.Spec.SecretEncryption.Type {
		case hyperv1.AESCBC:
			if hcluster.Spec.SecretEncryption.AESCBC == nil || len(hcluster.Spec.SecretEncryption.AESCBC.ActiveKey.Name) == 0 {
				log.Error(fmt.Errorf("aescbc metadata  is nil"), "")
				// don't return error here as reconciling won't fix input error
				return ctrl.Result{}, nil
			}
			var src corev1.Secret
			if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.SecretEncryption.AESCBC.ActiveKey.Name}, &src); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to get active aescbc secret %s: %w", hcluster.Spec.SecretEncryption.AESCBC.ActiveKey.Name, err)
			}
			if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcluster.Name, &src); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set referenced resource annotation: %w", err)
			}
			if _, ok := src.Data[hyperv1.AESCBCKeySecretKey]; !ok {
				log.Error(fmt.Errorf("no key field %s specified for aescbc active key secret", hyperv1.AESCBCKeySecretKey), "")
				// don't return error here as reconciling won't fix input error
				return ctrl.Result{}, nil
			}
			hostedControlPlaneActiveKeySecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: controlPlaneNamespace.Name,
					Name:      src.Name,
				},
			}
			_, err = createOrUpdate(ctx, r.Client, hostedControlPlaneActiveKeySecret, func() error {
				if hostedControlPlaneActiveKeySecret.Data == nil {
					hostedControlPlaneActiveKeySecret.Data = map[string][]byte{}
				}
				hostedControlPlaneActiveKeySecret.Data[hyperv1.AESCBCKeySecretKey] = src.Data[hyperv1.AESCBCKeySecretKey]
				hostedControlPlaneActiveKeySecret.Type = corev1.SecretTypeOpaque
				return nil
			})
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed reconciling aescbc active key: %w", err)
			}
			if hcluster.Spec.SecretEncryption.AESCBC.BackupKey != nil && len(hcluster.Spec.SecretEncryption.AESCBC.BackupKey.Name) > 0 { //nolint:staticcheck
				var src corev1.Secret
				if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.SecretEncryption.AESCBC.BackupKey.Name}, &src); err != nil { //nolint:staticcheck
					return ctrl.Result{}, fmt.Errorf("failed to get backup aescbc secret %s: %w", hcluster.Spec.SecretEncryption.AESCBC.BackupKey.Name, err) //nolint:staticcheck
				}
				if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcluster.Name, &src); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to set referenced resource annotation: %w", err)
				}
				if _, ok := src.Data[hyperv1.AESCBCKeySecretKey]; !ok {
					log.Error(fmt.Errorf("no key field %s specified for aescbc backup key secret", hyperv1.AESCBCKeySecretKey), "")
					// don't return error here as reconciling won't fix input error
					return ctrl.Result{}, nil
				}
				hostedControlPlaneBackupKeySecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: controlPlaneNamespace.Name,
						Name:      src.Name,
					},
				}
				_, err = createOrUpdate(ctx, r.Client, hostedControlPlaneBackupKeySecret, func() error {
					if hostedControlPlaneBackupKeySecret.Data == nil {
						hostedControlPlaneBackupKeySecret.Data = map[string][]byte{}
					}
					hostedControlPlaneBackupKeySecret.Data[hyperv1.AESCBCKeySecretKey] = src.Data[hyperv1.AESCBCKeySecretKey]
					hostedControlPlaneBackupKeySecret.Type = corev1.SecretTypeOpaque
					return nil
				})
				if err != nil {
					return ctrl.Result{}, fmt.Errorf("failed reconciling aescbc backup key: %w", err)
				}
			}
		case hyperv1.KMS:
			if hcluster.Spec.SecretEncryption.KMS == nil {
				log.Error(fmt.Errorf("kms metadata nil"), "")
				// don't return error here as reconciling won't fix input error
				return ctrl.Result{}, nil
			}
			if err := p.ReconcileSecretEncryption(ctx, r.Client, createOrUpdate,
				hcluster,
				controlPlaneNamespace.Name); err != nil {
				return ctrl.Result{}, err
			}
		default:
			log.Error(fmt.Errorf("unsupported encryption type %s", hcluster.Spec.SecretEncryption.Type), "")
			// don't return error here as reconciling won't fix input error
			return ctrl.Result{}, nil
		}
	}

	// Reconcile the HostedControlPlane audit webhook config if specified
	// reference from the HostedCluster and syncing the secret in the control plane namespace.
	{
		if hcluster.Spec.AuditWebhook != nil && len(hcluster.Spec.AuditWebhook.Name) > 0 {
			var src corev1.Secret
			if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.AuditWebhook.Name}, &src); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to get audit webhook config %s: %w", hcluster.Spec.AuditWebhook.Name, err)
			}
			if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcluster.Name, &src); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set referenced resource annotation: %w", err)
			}
			configData, ok := src.Data[hyperv1.AuditWebhookKubeconfigKey]
			if !ok {
				return ctrl.Result{}, fmt.Errorf("audit webhook secret does not contain key %s", hyperv1.AuditWebhookKubeconfigKey)
			}

			hostedControlPlaneAuditWebhookSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: controlPlaneNamespace.Name,
					Name:      src.Name,
				},
			}
			_, err = createOrUpdate(ctx, r.Client, hostedControlPlaneAuditWebhookSecret, func() error {
				if hostedControlPlaneAuditWebhookSecret.Data == nil {
					hostedControlPlaneAuditWebhookSecret.Data = map[string][]byte{}
				}
				hostedControlPlaneAuditWebhookSecret.Data[hyperv1.AuditWebhookKubeconfigKey] = configData
				hostedControlPlaneAuditWebhookSecret.Type = corev1.SecretTypeOpaque
				return nil
			})
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed reconciling audit webhook secret: %w", err)
			}
		}
	}

	// Reconcile the HostedControlPlane SSH secret by resolving the source secret reference
	// from the HostedCluster and syncing the secret in the control plane namespace.
	if len(hcluster.Spec.SSHKey.Name) > 0 {
		var src corev1.Secret
		err = r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.Namespace, Name: hcluster.Spec.SSHKey.Name}, &src)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get hostedcluster SSHKey secret %s: %w", hcluster.Spec.SSHKey.Name, err)
		}
		if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcluster.Name, &src); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set referenced resource annotation: %w", err)
		}
		dest := controlplaneoperator.SSHKey(controlPlaneNamespace.Name)
		_, err = createOrUpdate(ctx, r.Client, dest, func() error {
			srcData, srcHasData := src.Data["id_rsa.pub"]
			if !srcHasData {
				return fmt.Errorf("hostedcluster SSHKey secret %q must have a id_rsa.pub key", src.Name)
			}
			dest.Type = corev1.SecretTypeOpaque
			if dest.Data == nil {
				dest.Data = map[string][]byte{}
			}
			dest.Data["id_rsa.pub"] = srcData
			return nil
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile controlplane SSHKey secret: %w", err)
		}
	}

	// Reconcile the HostedControlPlane AdditionalTrustBundle ConfigMap by resolving the source reference
	// from the HostedCluster and syncing the CM in the control plane namespace.
	if err := r.reconcileAdditionalTrustBundle(ctx, hcluster, createOrUpdate, controlPlaneNamespace.Name); err != nil {
		return ctrl.Result{}, err
	}

	// Reconcile the service account signing key if set
	if hcluster.Spec.ServiceAccountSigningKey != nil {
		if err := r.reconcileServiceAccountSigningKey(ctx, hcluster, controlPlaneNamespace.Name, createOrUpdate); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile service account signing key: %w", err)
		}
	}

	// Reconcile etcd client MTLS secret if the control plane is using an unmanaged etcd cluster
	if hcluster.Spec.Etcd.ManagementType == hyperv1.Unmanaged {
		unmanagedEtcdTLSClientSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hcluster.GetNamespace(),
				Name:      hcluster.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name,
			},
		}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(unmanagedEtcdTLSClientSecret), unmanagedEtcdTLSClientSecret); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get unmanaged etcd tls secret: %w", err)
		}
		if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcluster.Name, unmanagedEtcdTLSClientSecret); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set referenced resource annotation: %w", err)
		}
		hostedControlPlaneEtcdClientSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: controlPlaneNamespace.Name,
				Name:      hcluster.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name,
			},
		}
		if result, err := createOrUpdate(ctx, r.Client, hostedControlPlaneEtcdClientSecret, func() error {
			if hostedControlPlaneEtcdClientSecret.Data == nil {
				hostedControlPlaneEtcdClientSecret.Data = map[string][]byte{}
			}
			hostedControlPlaneEtcdClientSecret.Data = unmanagedEtcdTLSClientSecret.Data
			hostedControlPlaneEtcdClientSecret.Type = corev1.SecretTypeOpaque
			return nil
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed reconciling etcd client secret: %w", err)
		} else {
			log.Info("reconciled etcd client mtls secret to control plane namespace", "result", result)
		}
	}

	// Reconcile the ETCD member recovery
	var requeueAfter *time.Duration
	if r.EnableEtcdRecovery &&
		hcluster.Spec.Etcd.ManagementType == hyperv1.Managed &&
		hcluster.Spec.ControllerAvailabilityPolicy == hyperv1.HighlyAvailable {
		var err error
		if requeueAfter, err = r.reconcileETCDMemberRecovery(ctx, hcluster, createOrUpdate); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to perform etcd member recovery: %w", err)
		}
	}

	// Reconcile global config related configmaps and secrets
	{
		if hcluster.Spec.Configuration != nil {
			configMapRefs := configrefs.ConfigMapRefs(hcluster.Spec.Configuration)
			for _, configMapRef := range configMapRefs {
				sourceCM := &corev1.ConfigMap{}
				if err := r.Get(ctx, client.ObjectKey{Namespace: hcluster.Namespace, Name: configMapRef}, sourceCM); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to get referenced configmap %s/%s: %w", hcluster.Namespace, configMapRef, err)
				}
				if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcluster.Name, sourceCM); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to set referenced resource annotation: %w", err)
				}
				destCM := &corev1.ConfigMap{}
				destCM.Name = sourceCM.Name
				destCM.Namespace = controlPlaneNamespace.Name
				if _, err := createOrUpdate(ctx, r.Client, destCM, func() error {
					destCM.Annotations = sourceCM.Annotations
					destCM.Labels = sourceCM.Labels
					destCM.Data = sourceCM.Data
					destCM.BinaryData = sourceCM.BinaryData
					destCM.Immutable = sourceCM.Immutable
					return nil
				}); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to reconcile referenced config map %s/%s: %w", destCM.Namespace, destCM.Name, err)
				}
			}
			secretRefs := configrefs.SecretRefs(hcluster.Spec.Configuration)
			for _, secretRef := range secretRefs {
				sourceSecret := &corev1.Secret{}
				if err := r.Get(ctx, client.ObjectKey{Namespace: hcluster.Namespace, Name: secretRef}, sourceSecret); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to get referenced secret %s/%s: %w", hcluster.Namespace, secretRef, err)
				}
				if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcluster.Name, sourceSecret); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to set referenced resource annotation: %w", err)
				}
				if err := ensureHostedResourcesAreEmpty(ctx, r.Client, hcluster, sourceSecret); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to validate referenced secret %s/%s: %w", hcluster.Namespace, secretRef, err)
				}
				destSecret := &corev1.Secret{}
				destSecret.Name = sourceSecret.Name
				destSecret.Namespace = controlPlaneNamespace.Name
				if _, err := createOrUpdate(ctx, r.Client, destSecret, func() error {
					destSecret.Annotations = sourceSecret.Annotations
					destSecret.Labels = sourceSecret.Labels
					destSecret.Data = sourceSecret.Data
					destSecret.Immutable = sourceSecret.Immutable
					destSecret.Type = sourceSecret.Type
					return nil
				}); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to reconcile secret %s/%s: %w", destSecret.Namespace, destSecret.Name, err)
				}
			}
		}
	}

	// Get release image version
	var releaseImageVersion semver.Version
	releaseImageVersion, err = semver.Parse(releaseImage.Version())
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to parse release image version: %w", err)
	}

	// Reconcile the HostedControlPlane
	isAutoscalingNeeded, err := r.isAutoscalingNeeded(ctx, hcluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to determine if autoscaler is needed: %w", err)
	}
	isAWSNodeTerminationHandlerNeeded, err := r.isAWSNodeTerminationHandlerNeeded(ctx, hcluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to determine if AWS node termination handler is needed: %w", err)
	}
	hcp = controlplaneoperator.HostedControlPlane(controlPlaneNamespace.Name, hcluster.Name)
	_, err = createOrUpdate(ctx, r.Client, hcp, func() error {
		return reconcileHostedControlPlane(hcp, hcluster, isAutoscalingNeeded, isAWSNodeTerminationHandlerNeeded,
			annotationsForCertRenewal(log,
				hcp,
				shouldCheckForStaleCerts(hcluster, defaultToControlPlaneV2),
				r.kasServingCertHashFromSecret(ctx, hcp),
				r.kasServingCertHashFromEndpoint(ctx, kasHostAndPortFromHCP(hcp))))
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile hostedcontrolplane: %w", err)
	}

	// Reconcile CAPI Infra CR.
	infraCR, err := p.ReconcileCAPIInfraCR(ctx, r.Client, createOrUpdate,
		hcluster,
		controlPlaneNamespace.Name,
		hcp.Status.ControlPlaneEndpoint)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Reconcile cluster prometheus RBAC resources if enabled
	if r.EnableOCPClusterMonitoring {
		if err := r.reconcileClusterPrometheusRBAC(ctx, createOrUpdate, hcp.Namespace); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile RBAC for OCP cluster prometheus: %w", err)
		}
	}

	// Reconcile the CAPI Cluster resource
	// In the None platform case, there is no CAPI provider/resources so infraCR is nil
	if infraCR != nil {
		capiCluster := controlplaneoperator.CAPICluster(controlPlaneNamespace.Name, hcluster.Spec.InfraID)
		_, err = createOrUpdate(ctx, r.Client, capiCluster, func() error {
			return reconcileCAPICluster(capiCluster, hcluster, hcp, infraCR)
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile capi cluster: %w", err)
		}
	}

	// Reconcile the monitoring dashboard if configured
	if r.MonitoringDashboards {
		if err := r.reconcileMonitoringDashboard(ctx, createOrUpdate, hcluster); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile monitoring dashboard: %w", err)
		}
	}

	// Reconcile the HostedControlPlane kubeconfig if one is reported
	if hcp.Status.KubeConfig != nil {
		src := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hcp.Namespace,
				Name:      hcp.Status.KubeConfig.Name,
			},
		}
		err := r.Client.Get(ctx, client.ObjectKeyFromObject(src), src)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get controlplane kubeconfig secret %q: %w", client.ObjectKeyFromObject(src), err)
		}
		dest := manifests.KubeConfigSecret(hcluster.Namespace, hcluster.Name)
		_, err = createOrUpdate(ctx, r.Client, dest, func() error {
			key := hcp.Status.KubeConfig.Key
			srcData, srcHasData := src.Data[key]
			if !srcHasData {
				return fmt.Errorf("controlplane kubeconfig secret %q must have a %q key", client.ObjectKeyFromObject(src), key)
			}
			dest.Labels = hcluster.Labels
			dest.Type = corev1.SecretTypeOpaque
			if dest.Data == nil {
				dest.Data = map[string][]byte{}
			}
			dest.Data["kubeconfig"] = srcData
			dest.SetOwnerReferences([]metav1.OwnerReference{{
				APIVersion: hyperv1.GroupVersion.String(),
				Kind:       "HostedCluster",
				Name:       hcluster.Name,
				UID:        hcluster.UID,
			}})
			return nil
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile hostedcluster kubeconfig secret: %w", err)
		}
	}

	if cpoSupportsKASCustomKubeconfig {
		// Reconcile the HostedControlPlane external kubeconfig if one is reported
		if len(hcp.Spec.KubeAPIServerDNSName) > 0 {
			requeue, err := r.reconcileCustomExternalKubeconfig(ctx, createOrUpdate, hcp, hcluster)
			if err != nil {
				return ctrl.Result{}, err
			}
			if requeue != nil {
				requeueAfter = requeue
			}
		} else {
			// Delete the custom external kubeconfig secret if it exists and the external name is not set
			if hcluster.Status.CustomKubeconfig != nil {
				customKubeconfig := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: hcluster.Namespace,
						Name:      hcluster.Status.CustomKubeconfig.Name,
					},
				}
				if _, err := k8sutil.DeleteIfNeeded(ctx, r.Client, customKubeconfig); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to delete custom external kubeconfig secret %q: %w", client.ObjectKeyFromObject(customKubeconfig), err)
				}
				hcluster.Status.CustomKubeconfig = nil
			}
		}
	}

	// Reconcile the HostedControlPlane kubeadminPassword
	if hcp.Status.KubeadminPassword != nil {
		src := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hcp.Namespace,
				Name:      hcp.Status.KubeadminPassword.Name,
			},
		}
		err := r.Client.Get(ctx, client.ObjectKeyFromObject(src), src)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get controlplane kubeadmin password secret %q: %w", client.ObjectKeyFromObject(src), err)
		}
		dest := manifests.KubeadminPasswordSecret(hcluster.Namespace, hcluster.Name)
		_, err = createOrUpdate(ctx, r.Client, dest, func() error {
			dest.Type = corev1.SecretTypeOpaque
			dest.Data = map[string][]byte{}
			for k, v := range src.Data {
				dest.Data[k] = v
			}
			dest.SetOwnerReferences([]metav1.OwnerReference{{
				APIVersion: hyperv1.GroupVersion.String(),
				Kind:       "HostedCluster",
				Name:       hcluster.Name,
				UID:        hcluster.UID,
			}})
			return nil
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile hostedcluster kubeconfig secret: %w", err)
		}
	} else {
		KubeadminPasswordSecret := manifests.KubeadminPasswordSecret(hcluster.Namespace, hcluster.Name)
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(KubeadminPasswordSecret), KubeadminPasswordSecret); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to get hostedcluster kubeadmin password secret %q: %w", client.ObjectKeyFromObject(KubeadminPasswordSecret), err)
			}
		} else {
			if err := r.Client.Delete(ctx, KubeadminPasswordSecret); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete hostedcluster kubeadmin password secret %q: %w", client.ObjectKeyFromObject(KubeadminPasswordSecret), err)
			}
		}
	}

	if _, err := r.defaultIngressDomain(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to determine default ingress domain: %w", err)
	}

	// Reconcile SRE metrics config
	if err := r.reconcileSREMetricsConfig(ctx, createOrUpdate, hcp); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile SRE metrics config: %w", err)
	}

	err = r.reconcileOpenShiftTrustedCAs(ctx, hcp)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile OpenShift trusted CAs: %w", err)
	}

	imageProvider := imageprovider.New(releaseImage)
	imageProvider.ComponentImages()["token-minter"] = utilitiesImage
	imageProvider.ComponentImages()[podspec.AvailabilityProberImageName] = utilitiesImage

	securityContextUID := controlplanecomponent.DefaultSecurityContextUID
	if r.SetDefaultSecurityContext {
		securityContextUID, err = strconv.ParseInt(controlPlaneNamespace.Annotations[DefaultSecurityContextUIDAnnnotation], 10, 64)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to parse SecurityContext UID: %w", err)
		}
	}

	metricsSet := r.effectiveMetricsSet(hcluster.Spec.Monitoring)
	cpContext := controlplanecomponent.ControlPlaneContext{
		Context:                   ctx,
		Client:                    r.Client,
		ApplyProvider:             upsert.NewApplyProvider(r.EnableCIDebugOutput),
		HCP:                       hcp,
		SetDefaultSecurityContext: r.SetDefaultSecurityContext,
		DefaultSecurityContextUID: securityContextUID,
		EnableCIDebugOutput:       r.EnableCIDebugOutput,
		MetricsSet:                metricsSet,
		ReleaseImageProvider:      imageProvider,
		OmitOwnerReference:        true,
	}

	// Reconcile the control plane operator
	err = r.reconcileControlPlaneOperator(cpContext, createOrUpdate, hcluster, controlPlaneOperatorImage, utilitiesImage, cpoHasUtilities, r.CertRotationScale, releaseImageVersion, releaseProvider)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile control plane operator: %w", err)
	}

	// Reconcile the CAPI manager components
	err = r.reconcileCAPIManager(cpContext, createOrUpdate, hcluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile capi manager: %w", err)
	}

	// Reconcile the CAPI provider components
	if err = r.reconcileCAPIProvider(cpContext, hcluster, hcp, p); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile capi provider: %w", err)
	}

	if _, pkiDisabled := hcp.Annotations[hyperv1.DisablePKIReconciliationAnnotation]; controlPlanePKIOperatorSignsCSRs && !pkiDisabled {
		// Reconcile the control plane PKI operator RBAC - the CPO does not have rights to do this itself
		err = r.reconcileControlPlanePKIOperatorRBAC(ctx, createOrUpdate, hcluster)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile control plane PKI operator RBAC: %w", err)
		}
	}

	// Reconcile the network policies
	if err = r.reconcileNetworkPolicies(ctx, log, createOrUpdate, hcluster, hcp, releaseImageVersion, controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile network policies: %w", err)
	}

	// Reconcile platform specific items
	switch hcluster.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		if hcluster.Spec.Platform.Kubevirt != nil && hcluster.Spec.Platform.Kubevirt.Credentials != nil {
			if err := r.Client.Status().Update(ctx, hcluster); err != nil {
				if apierrors.IsConflict(err) {
					return ctrl.Result{Requeue: true}, nil
				}
				return ctrl.Result{}, fmt.Errorf("failed to update status after network policy RBAC check: %w", err)
			}
		}
		err = r.reconcileKubevirtCSIClusterRBAC(ctx, createOrUpdate, hcluster)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile kubevirt CSI cluster wide RBAC: %w", err)
		}
	case hyperv1.AWSPlatform:
		if err := r.reconcileOIDCDocumentsWithStatus(ctx, hcluster, func() error {
			return r.reconcileAWSOIDCDocuments(ctx, log, hcluster, hcp)
		}); err != nil {
			return ctrl.Result{}, err
		}
	case hyperv1.AzurePlatform:
		if azureutil.IsAroHCPByHC(hcluster) {
			// Reconcile CPO SecretProviderClass CR
			cpoSecretProviderClass := cpomanifests.ManagedAzureSecretProviderClass(config.ManagedAzureCPOSecretProviderClassName, hcp.Namespace)
			if _, err = createOrUpdate(ctx, r, cpoSecretProviderClass, func() error {
				secretproviderclass.ReconcileManagedAzureSecretProviderClass(cpoSecretProviderClass, hcp, hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.ControlPlaneOperator)
				return nil
			}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to reconcile control plane operator secret provider class: %w", err)
			}

			// Reconcile CAPZ SecretProviderClass CR
			nodepoolMgmtSecretProviderClass := cpomanifests.ManagedAzureSecretProviderClass(config.ManagedAzureNodePoolMgmtSecretProviderClassName, hcp.Namespace)
			if _, err = createOrUpdate(ctx, r, nodepoolMgmtSecretProviderClass, func() error {
				secretproviderclass.ReconcileManagedAzureSecretProviderClass(nodepoolMgmtSecretProviderClass, hcp, hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.NodePoolManagement)
				return nil
			}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to reconcile nodepool management secret provider class: %w", err)
			}
		}

		if hcluster.Spec.SecretEncryption != nil && hcluster.Spec.SecretEncryption.KMS != nil {
			if azureutil.IsAroHCPByHC(hcluster) {
				// Reconcile KMS SecretProviderClass CR
				kmsSecretProviderClass := cpomanifests.ManagedAzureSecretProviderClass(config.ManagedAzureKMSSecretProviderClassName, hcp.Namespace)
				if _, err := createOrUpdate(ctx, r, kmsSecretProviderClass, func() error {
					secretproviderclass.ReconcileManagedAzureSecretProviderClass(kmsSecretProviderClass, hcp, hcp.Spec.SecretEncryption.KMS.Azure.KMS)
					return nil
				}); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to reconcile KMS SecretProviderClass: %w", err)
				}
			}
		}
	case hyperv1.GCPPlatform:
		if err := r.reconcileOIDCDocumentsWithStatus(ctx, hcluster, func() error {
			return r.reconcileGCPOIDCDocuments(ctx, log, hcluster, hcp)
		}); err != nil {
			return ctrl.Result{}, err
		}
	}

	if err := r.reconcileKarpenterOperator(cpContext, hcluster, r.HypershiftOperatorImage, controlPlaneOperatorImage); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile karpenter operator: %w", err)
	}

	if pubEndpointRequeue, err := r.reconcilePublicEndpointExposedCondition(ctx, hcluster); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile PublicEndpointExposed condition: %w", err)
	} else if pubEndpointRequeue != nil && (requeueAfter == nil || *pubEndpointRequeue < *requeueAfter) {
		requeueAfter = pubEndpointRequeue
	}

	log.Info("successfully reconciled")
	result := ctrl.Result{}
	if requeueAfter != nil {
		result.RequeueAfter = *requeueAfter
	}
	if autoNodeProgressing {
		autoNodeRequeue := 15 * time.Second
		if result.RequeueAfter == 0 || autoNodeRequeue < result.RequeueAfter {
			result.RequeueAfter = autoNodeRequeue
		}
	}
	return result, nil
}
