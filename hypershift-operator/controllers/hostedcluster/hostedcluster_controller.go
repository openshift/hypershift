/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package hostedcluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/apimachinery/pkg/util/intstr"
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

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/autoscaler"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/clusterapi"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	hyperutil "github.com/openshift/hypershift/hypershift-operator/controllers/util"
	capiv1 "github.com/openshift/hypershift/thirdparty/clusterapi/api/v1alpha4"
)

const (
	finalizer                      = "hypershift.openshift.io/finalizer"
	hostedClusterAnnotation        = "hypershift.openshift.io/cluster"
	clusterDeletionRequeueDuration = time.Duration(5 * time.Second)
)

// NoopReconcile is just a default mutation function that does nothing.
var NoopReconcile controllerutil.MutateFn = func() error { return nil }

// HostedClusterReconciler reconciles a HostedCluster object
type HostedClusterReconciler struct {
	client.Client

	Log           logr.Logger
	OperatorImage string
	Clock         clock.Clock
}

// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedclusters/status,verbs=get;update;patch

func (r *HostedClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Clock == nil {
		r.Clock = clock.RealClock{}
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedCluster{}).
		Watches(&source.Kind{Type: &hyperv1.ExternalInfraCluster{}}, handler.EnqueueRequestsFromMapFunc(enqueueParentHostedCluster)).
		Watches(&source.Kind{Type: &hyperv1.HostedControlPlane{}}, handler.EnqueueRequestsFromMapFunc(enqueueParentHostedCluster)).
		Watches(&source.Kind{Type: &capiv1.Cluster{}}, handler.EnqueueRequestsFromMapFunc(enqueueParentHostedCluster)).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
		}).
		Complete(r)
}

func (r *HostedClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Log = ctrl.LoggerFrom(ctx)
	r.Log.Info("reconciling")

	// Look up the HostedCluster instance to reconcile
	hcluster := &hyperv1.HostedCluster{}
	isMissing := false
	err := r.Get(ctx, req.NamespacedName, hcluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			isMissing = true
		} else {
			return ctrl.Result{}, fmt.Errorf("failed to get cluster %q: %w", req.NamespacedName, err)
		}
	}

	// If deleted or missing, clean up and return early.
	// TODO: This should be incorporated with status/reconcile
	if isMissing || !hcluster.DeletionTimestamp.IsZero() {
		// Keep trying to delete until we know it's safe to finalize.
		completed, err := r.delete(ctx, req, hcluster)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete cluster: %w", err)
		}
		if !completed {
			r.Log.Info("hostedcluster is still deleting", "name", req.NamespacedName)
			return ctrl.Result{RequeueAfter: clusterDeletionRequeueDuration}, nil
		}
		r.Log.Info("finished deleting hostedcluster", "name", req.NamespacedName)
		// Now we can remove the finalizer.
		if controllerutil.ContainsFinalizer(hcluster, finalizer) {
			controllerutil.RemoveFinalizer(hcluster, finalizer)
			if err := r.Update(ctx, hcluster); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from cluster: %w", err)
			}
			r.Log.Info("hostedcluster was finalized", "name", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, nil
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

	// Set version status
	{
		controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
		hcp := controlplaneoperator.HostedControlPlane(controlPlaneNamespace.Name, hcluster.Name)
		err := r.Client.Get(ctx, client.ObjectKeyFromObject(hcp), hcp)
		if err != nil {
			if apierrors.IsNotFound(err) {
				hcp = nil
			} else {
				return ctrl.Result{}, fmt.Errorf("failed to get hostedcontrolplane: %w", err)
			}
		}
		hcluster.Status.Version = computeClusterVersionStatus(r.Clock, hcluster, hcp)
	}

	// Set the Available condition
	{
		controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
		hcp := controlplaneoperator.HostedControlPlane(controlPlaneNamespace.Name, hcluster.Name)
		err := r.Client.Get(ctx, client.ObjectKeyFromObject(hcp), hcp)
		if err != nil {
			if apierrors.IsNotFound(err) {
				hcp = nil
			} else {
				return ctrl.Result{}, fmt.Errorf("failed to get hostedcontrolplane: %w", err)
			}
		}
		meta.SetStatusCondition(&hcluster.Status.Conditions, computeHostedClusterAvailability(hcluster, hcp))
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
	if !controllerutil.ContainsFinalizer(hcluster, finalizer) {
		controllerutil.AddFinalizer(hcluster, finalizer)
		if err := r.Update(ctx, hcluster); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to cluster: %w", err)
		}
	}

	// Reconcile the hosted cluster namespace
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, controlPlaneNamespace, NoopReconcile)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile namespace: %w", err)
	}

	// Reconcile the platform provider cloud controller credentials secret by resolving
	// the reference from the HostedCluster and syncing the secret in the control
	// plane namespace.
	switch hcluster.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		var src corev1.Secret
		err = r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.Platform.AWS.KubeCloudControllerCreds.Name}, &src)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get provider creds %s: %w", hcluster.Spec.Platform.AWS.KubeCloudControllerCreds.Name, err)
		}
		dest := manifests.AWSKubeCloudControllerCreds(controlPlaneNamespace.Name)
		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, dest, func() error {
			srcData, srcHasData := src.Data["credentials"]
			if !srcHasData {
				return fmt.Errorf("hostedcluster provider credentials secret %q must have a credentials key", src.Name)
			}
			dest.Type = corev1.SecretTypeOpaque
			if dest.Data == nil {
				dest.Data = map[string][]byte{}
			}
			dest.Data["credentials"] = srcData
			return nil
		})
	}

	// Reconcile the platform provider node pool management credentials secret by
	// resolving  the reference from the HostedCluster and syncing the secret in
	// the control plane namespace.
	switch hcluster.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		var src corev1.Secret
		err = r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.Platform.AWS.NodePoolManagementCreds.Name}, &src)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get node pool provider creds %s: %w", hcluster.Spec.Platform.AWS.NodePoolManagementCreds.Name, err)
		}
		dest := manifests.AWSNodePoolManagementCreds(controlPlaneNamespace.Name)
		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, dest, func() error {
			srcData, srcHasData := src.Data["credentials"]
			if !srcHasData {
				return fmt.Errorf("node pool provider credentials secret %q is missing credentials key", src.Name)
			}
			dest.Type = corev1.SecretTypeOpaque
			if dest.Data == nil {
				dest.Data = map[string][]byte{}
			}
			dest.Data["credentials"] = srcData
			return nil
		})
	}

	// Reconcile the HostedControlPlane pull secret by resolving the source secret
	// reference from the HostedCluster and syncing the secret in the control plane namespace.
	{
		var src corev1.Secret
		if err := r.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.PullSecret.Name}, &src); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get pull secret %s: %w", hcluster.Spec.PullSecret.Name, err)
		}
		dst := controlplaneoperator.PullSecret(controlPlaneNamespace.Name)
		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, dst, func() error {
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
	}

	// Reconcile the HostedControlPlane signing key by resolving the source secret
	// reference from the HostedCluster and syncing the secret in the control plane namespace.
	{
		var src corev1.Secret
		if err := r.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.SigningKey.Name}, &src); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get signing key %s: %w", hcluster.Spec.SigningKey.Name, err)
		}
		dest := controlplaneoperator.SigningKey(controlPlaneNamespace.Name)
		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, dest, func() error {
			srcData, srcHasData := src.Data["key"]
			if !srcHasData {
				return fmt.Errorf("hostedcluster signing key %q must have a key key", src.Name)
			}
			dest.Type = corev1.SecretTypeOpaque
			if dest.Data == nil {
				dest.Data = map[string][]byte{}
			}
			dest.Data["key"] = srcData
			return nil
		})
	}

	// Reconcile the HostedControlPlane SSH secret by resolving the source secret reference
	// from the HostedCluster and syncing the secret in the control plane namespace.
	if len(hcluster.Spec.SSHKey.Name) > 0 {
		var src corev1.Secret
		err = r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.Namespace, Name: hcluster.Spec.SSHKey.Name}, &src)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get hostedcluster SSH key secret %s: %w", hcluster.Spec.SSHKey.Name, err)
		}
		dest := controlplaneoperator.SSHKey(controlPlaneNamespace.Name)
		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, dest, func() error {
			srcData, srcHasData := src.Data["id_rsa.pub"]
			if !srcHasData {
				return fmt.Errorf("hostedcluster ssh key secret %q must have a id_rsa.pub key", src.Name)
			}
			dest.Type = corev1.SecretTypeOpaque
			if dest.Data == nil {
				dest.Data = map[string][]byte{}
			}
			dest.Data["id_rsa.pub"] = srcData
			return nil
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile controlplane ssh secret: %w", err)
		}
	}

	// Reconcile the CAPI ExternalInfraCluster
	externalInfraCluster := controlplaneoperator.ExternalInfraCluster(controlPlaneNamespace.Name, hcluster.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, externalInfraCluster, func() error {
		return reconcileExternalInfraCluster(externalInfraCluster, hcluster)
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile externalinfracluster: %w", err)
	}

	// Reconcile the HostedControlPlane
	hcp := controlplaneoperator.HostedControlPlane(controlPlaneNamespace.Name, hcluster.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, hcp, func() error {
		return reconcileHostedControlPlane(hcp, hcluster)
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile hostedcontrolplane: %w", err)
	}

	// Reconcile the CAPI Cluster resource
	capiCluster := controlplaneoperator.CAPICluster(controlPlaneNamespace.Name, hcluster.Spec.InfraID)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiCluster, func() error {
		return reconcileCAPICluster(capiCluster, hcluster, hcp, externalInfraCluster)
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile capi cluster: %w", err)
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
		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, dest, func() error {
			key := hcp.Status.KubeConfig.Key
			srcData, srcHasData := src.Data[key]
			if !srcHasData {
				return fmt.Errorf("controlplane kubeconfig secret %q must have a %q key", client.ObjectKeyFromObject(src), key)
			}
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

	// Reconcile the CAPI manager components
	err = r.reconcileCAPIManager(ctx, hcluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile capi manager: %w", err)
	}

	// Reconcile the CAPI AWS provider components
	err = r.reconcileCAPIAWSProvider(ctx, hcluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile capi aws provider: %w", err)
	}

	// Reconcile the autoscaler
	err = r.reconcileAutoscaler(ctx, hcluster, hcp)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile autoscaler: %w", err)
	}

	// Reconcile the control plane operator
	err = r.reconcileControlPlaneOperator(ctx, hcluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile control plane operator: %w", err)
	}

	r.Log.Info("successfully reconciled")
	return ctrl.Result{}, nil
}

// reconcileHostedControlPlane reconciles the given HostedControlPlane, which
// will be mutated.
func reconcileHostedControlPlane(hcp *hyperv1.HostedControlPlane, hcluster *hyperv1.HostedCluster) error {
	// Always initialize the HostedControlPlane with an image matching
	// the HostedCluster.
	if hcp.ObjectMeta.CreationTimestamp.IsZero() {
		hcp.Spec.ReleaseImage = hcluster.Spec.Release.Image
	}

	hcp.Annotations = map[string]string{
		hostedClusterAnnotation: ctrlclient.ObjectKeyFromObject(hcluster).String(),
	}

	hcp.Spec.PullSecret = corev1.LocalObjectReference{Name: controlplaneoperator.PullSecret(hcp.Namespace).Name}
	hcp.Spec.SigningKey = corev1.LocalObjectReference{Name: controlplaneoperator.SigningKey(hcp.Namespace).Name}
	if len(hcluster.Spec.SSHKey.Name) > 0 {
		hcp.Spec.SSHKey = corev1.LocalObjectReference{Name: controlplaneoperator.SSHKey(hcp.Namespace).Name}
	}
	hcp.Spec.IssuerURL = hcluster.Spec.IssuerURL
	hcp.Spec.ServiceCIDR = hcluster.Spec.Networking.ServiceCIDR
	hcp.Spec.PodCIDR = hcluster.Spec.Networking.PodCIDR
	hcp.Spec.MachineCIDR = hcluster.Spec.Networking.MachineCIDR
	hcp.Spec.InfraID = hcluster.Spec.InfraID
	hcp.Spec.DNS = hcluster.Spec.DNS
	hcp.Spec.Services = hcluster.Spec.Services
	hcp.Spec.ControllerAvailabilityPolicy = hcluster.Spec.ControllerAvailabilityPolicy

	switch hcluster.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		hcp.Spec.Platform.Type = hyperv1.AWSPlatform
		hcp.Spec.Platform.AWS = hcluster.Spec.Platform.AWS.DeepCopy()
		hcp.Spec.Platform.AWS.KubeCloudControllerCreds = corev1.LocalObjectReference{
			Name: manifests.AWSKubeCloudControllerCreds(hcp.Namespace).Name,
		}
		// TODO: Not actually used by the control plane operator...
		hcp.Spec.Platform.AWS.NodePoolManagementCreds = corev1.LocalObjectReference{
			Name: manifests.AWSNodePoolManagementCreds(hcp.Namespace).Name,
		}
	case hyperv1.NonePlatform:
		hcp.Spec.Platform.Type = hyperv1.NonePlatform
	case hyperv1.IBMCloudPlatform:
		hcp.Spec.Platform.Type = hyperv1.IBMCloudPlatform
	}

	// Only update release image (triggering a new rollout) after existing rollouts
	// have reached a terminal state.
	rolloutComplete := hcluster.Status.Version != nil &&
		hcluster.Status.Version.History != nil &&
		hcluster.Status.Version.History[0].State == configv1.CompletedUpdate
	if rolloutComplete {
		hcp.Spec.ReleaseImage = hcluster.Spec.Release.Image
	}

	return nil
}

// reconcileCAPIManager orchestrates orchestrates of  all CAPI manager components.
func (r *HostedClusterReconciler) reconcileCAPIManager(ctx context.Context, hcluster *hyperv1.HostedCluster) error {
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
	err := r.Client.Get(ctx, client.ObjectKeyFromObject(controlPlaneNamespace), controlPlaneNamespace)
	if err != nil {
		return fmt.Errorf("failed to get control plane namespace: %w", err)
	}

	// Reconcile CAPI manager service account
	capiManagerServiceAccount := clusterapi.CAPIManagerServiceAccount(controlPlaneNamespace.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiManagerServiceAccount, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager service account: %w", err)
	}

	// Reconcile CAPI manager cluster role
	capiManagerClusterRole := clusterapi.CAPIManagerClusterRole(controlPlaneNamespace.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiManagerClusterRole, func() error {
		return reconcileCAPIManagerClusterRole(capiManagerClusterRole)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager cluster role: %w", err)
	}

	// Reconcile CAPI manager cluster role binding
	capiManagerClusterRoleBinding := clusterapi.CAPIManagerClusterRoleBinding(controlPlaneNamespace.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiManagerClusterRoleBinding, func() error {
		return reconcileCAPIManagerClusterRoleBinding(capiManagerClusterRoleBinding, capiManagerClusterRole, capiManagerServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager cluster role binding: %w", err)
	}

	// Reconcile CAPI manager role
	capiManagerRole := clusterapi.CAPIManagerRole(controlPlaneNamespace.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiManagerRole, func() error {
		return reconcileCAPIManagerRole(capiManagerRole)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager role: %w", err)
	}

	// Reconcile CAPI manager role binding
	capiManagerRoleBinding := clusterapi.CAPIManagerRoleBinding(controlPlaneNamespace.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiManagerRoleBinding, func() error {
		return reconcileCAPIManagerRoleBinding(capiManagerRoleBinding, capiManagerRole, capiManagerServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager role: %w", err)
	}

	// Reconcile CAPI manager deployment
	capiManagerDeployment := clusterapi.ClusterAPIManagerDeployment(controlPlaneNamespace.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiManagerDeployment, func() error {
		return reconcileCAPIManagerDeployment(capiManagerDeployment, capiManagerServiceAccount, "quay.io/hypershift/cluster-api:hypershift")
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager deployment: %w", err)
	}

	return nil
}

// reconcileCAPIAWSProvider orchestrates reconciliation of the CAPI AWS provider
// components.
func (r *HostedClusterReconciler) reconcileCAPIAWSProvider(ctx context.Context, hcluster *hyperv1.HostedCluster) error {
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
	err := r.Client.Get(ctx, client.ObjectKeyFromObject(controlPlaneNamespace), controlPlaneNamespace)
	if err != nil {
		return fmt.Errorf("failed to get control plane namespace: %w", err)
	}

	// Reconcile CAPI AWS provider role
	capiAwsProviderRole := clusterapi.CAPIAWSProviderRole(controlPlaneNamespace.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiAwsProviderRole, func() error {
		return reconcileCAPIAWSProviderRole(capiAwsProviderRole)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi aws provider role: %w", err)
	}

	// Reconcile CAPI AWS provider service account
	capiAwsProviderServiceAccount := clusterapi.CAPIAWSProviderServiceAccount(controlPlaneNamespace.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiAwsProviderServiceAccount, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile capi aws provider service account: %w", err)
	}

	// Reconcile CAPI AWS provider role binding
	capiAwsProviderRoleBinding := clusterapi.CAPIAWSProviderRoleBinding(controlPlaneNamespace.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiAwsProviderRoleBinding, func() error {
		return reconcileCAPIAWSProviderRoleBinding(capiAwsProviderRoleBinding, capiAwsProviderRole, capiAwsProviderServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi aws provider role binding: %w", err)
	}

	// Reconcile CAPI AWS provider deployment
	capiAwsProviderDeployment := clusterapi.CAPIAWSProviderDeployment(controlPlaneNamespace.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiAwsProviderDeployment, func() error {
		return reconcileCAPIAWSProviderDeployment(capiAwsProviderDeployment, capiAwsProviderServiceAccount, "quay.io/hypershift/cluster-api-provider-aws:master")
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi aws provider deployment: %w", err)
	}

	return nil
}

// reconcileControlPlaneOperator orchestrates reconciliation of the control plane
// operator components.
func (r *HostedClusterReconciler) reconcileControlPlaneOperator(ctx context.Context, hcluster *hyperv1.HostedCluster) error {
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
	err := r.Client.Get(ctx, client.ObjectKeyFromObject(controlPlaneNamespace), controlPlaneNamespace)
	if err != nil {
		return fmt.Errorf("failed to get control plane namespace: %w", err)
	}

	// Reconcile operator service account
	controlPlaneOperatorServiceAccount := controlplaneoperator.OperatorServiceAccount(controlPlaneNamespace.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, controlPlaneOperatorServiceAccount, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator service account: %w", err)
	}

	// Reconcile operator cluster role
	controlPlaneOperatorClusterRole := controlplaneoperator.OperatorClusterRole()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, controlPlaneOperatorClusterRole, func() error {
		return reconcileControlPlaneOperatorClusterRole(controlPlaneOperatorClusterRole)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator cluster role: %w", err)
	}

	// Reconcile operator cluster role binding
	controlPlaneOperatorClusterRoleBinding := controlplaneoperator.OperatorClusterRoleBinding(controlPlaneNamespace.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, controlPlaneOperatorClusterRoleBinding, func() error {
		return reconcileControlPlaneOperatorClusterRoleBinding(controlPlaneOperatorClusterRoleBinding, controlPlaneOperatorClusterRole, controlPlaneOperatorServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator clusterrolebinding: %w", err)
	}

	// Reconcile operator role
	controlPlaneOperatorRole := controlplaneoperator.OperatorRole(controlPlaneNamespace.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, controlPlaneOperatorRole, func() error {
		return reconcileControlPlaneOperatorRole(controlPlaneOperatorRole)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator clusterrole: %w", err)
	}

	// Reconcile operator role binding
	controlPlaneOperatorRoleBinding := controlplaneoperator.OperatorRoleBinding(controlPlaneNamespace.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, controlPlaneOperatorRoleBinding, func() error {
		return reconcileControlPlaneOperatorRoleBinding(controlPlaneOperatorRoleBinding, controlPlaneOperatorRole, controlPlaneOperatorServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator rolebinding: %w", err)
	}

	// Reconcile operator deployment
	controlPlaneOperatorDeployment := controlplaneoperator.OperatorDeployment(controlPlaneNamespace.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, controlPlaneOperatorDeployment, func() error {
		return reconcileControlPlaneOperatorDeployment(controlPlaneOperatorDeployment, r.OperatorImage, controlPlaneOperatorServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator deployment: %w", err)
	}

	return nil
}

// reconcileAutoscaler orchestrates reconciliation of autoscaler components using
// both the HostedCluster and the HostedControlPlane which the autoscaler takes
// inputs from.
func (r *HostedClusterReconciler) reconcileAutoscaler(ctx context.Context, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) error {
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
	err := r.Client.Get(ctx, client.ObjectKeyFromObject(controlPlaneNamespace), controlPlaneNamespace)
	if err != nil {
		return fmt.Errorf("failed to get control plane namespace: %w", err)
	}

	// Reconcile autoscaler role
	autoScalerRole := autoscaler.AutoScalerRole(controlPlaneNamespace.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, autoScalerRole, func() error {
		return reconcileAutoScalerRole(autoScalerRole)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile autoscaler role: %w", err)
	}

	// Reconcile autoscaler service account
	autoScalerServiceAccount := autoscaler.AutoScalerServiceAccount(controlPlaneNamespace.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, autoScalerServiceAccount, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile autoscaler service account: %w", err)
	}

	// Reconcile autoscaler role binding
	autoScalerRoleBinding := autoscaler.AutoScalerRoleBinding(controlPlaneNamespace.Name)
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, autoScalerRoleBinding, func() error {
		return reconcileAutoScalerRoleBinding(autoScalerRoleBinding, autoScalerRole, autoScalerServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile autoscaler role binding: %w", err)
	}

	// The deployment depends on the kubeconfig being reported.
	if hcp.Status.KubeConfig != nil {
		// Resolve the kubeconfig secret for CAPI which the
		// autoscaler is deployed alongside of.
		capiKubeConfigSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hcp.Namespace,
				Name:      fmt.Sprintf("%s-kubeconfig", hcluster.Spec.InfraID),
			},
		}
		err = r.Client.Get(ctx, client.ObjectKeyFromObject(capiKubeConfigSecret), capiKubeConfigSecret)
		if err != nil {
			return fmt.Errorf("failed to get hosted controlplane kubeconfig secret %q: %w", capiKubeConfigSecret.Name, err)
		}

		// Reconcile autoscaler deployment
		autoScalerDeployment := autoscaler.AutoScalerDeployment(controlPlaneNamespace.Name)
		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, autoScalerDeployment, func() error {
			return reconcileAutoScalerDeployment(autoScalerDeployment, autoScalerServiceAccount, capiKubeConfigSecret, "k8s.gcr.io/autoscaling/cluster-autoscaler:v1.20.0", hcluster.Spec.Autoscaling)
		})
		if err != nil {
			return fmt.Errorf("failed to reconcile autoscaler deployment: %w", err)
		}
	}

	return nil
}

func reconcileControlPlaneOperatorDeployment(deployment *appsv1.Deployment, image string, sa *corev1.ServiceAccount) error {
	deployment.Spec = appsv1.DeploymentSpec{
		Replicas: k8sutilspointer.Int32Ptr(1),
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"name": "control-plane-operator",
			},
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"name": "control-plane-operator",
				},
			},
			Spec: corev1.PodSpec{
				ServiceAccountName: sa.Name,
				Containers: []corev1.Container{
					{
						Name:            "control-plane-operator",
						Image:           image,
						ImagePullPolicy: corev1.PullAlways,
						Env: []corev1.EnvVar{
							{
								Name: "MY_NAMESPACE",
								ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{
										FieldPath: "metadata.namespace",
									},
								},
							},
						},
						Command: []string{"/usr/bin/control-plane-operator"},
						Args:    []string{"run", "--namespace", "$(MY_NAMESPACE)", "--deployment-name", "control-plane-operator"},
					},
				},
			},
		},
	}
	return nil
}

func reconcileControlPlaneOperatorClusterRole(role *rbacv1.ClusterRole) error {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"apiextensions.k8s.io"},
			Resources: []string{"customresourcedefinitions"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"config.openshift.io"},
			Resources: []string{"*"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"operator.openshift.io"},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"security.openshift.io"},
			Resources: []string{"securitycontextconstraints"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"rbac.authorization.k8s.io"},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
	}
	return nil
}

func reconcileControlPlaneOperatorClusterRoleBinding(binding *rbacv1.ClusterRoleBinding, role *rbacv1.ClusterRole, sa *corev1.ServiceAccount) error {
	binding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "ClusterRole",
		Name:     role.Name,
	}
	binding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      sa.Name,
			Namespace: sa.Namespace,
		},
	}
	return nil
}

func reconcileControlPlaneOperatorRole(role *rbacv1.Role) error {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"hypershift.openshift.io"},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{
				"bootstrap.cluster.x-k8s.io",
				"controlplane.cluster.x-k8s.io",
				"infrastructure.cluster.x-k8s.io",
				"machines.cluster.x-k8s.io",
				"exp.infrastructure.cluster.x-k8s.io",
				"addons.cluster.x-k8s.io",
				"exp.cluster.x-k8s.io",
				"cluster.x-k8s.io",
			},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"route.openshift.io"},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{
				"events",
				"configmaps",
				"pods",
				"pods/log",
				"secrets",
				"nodes",
				"serviceaccounts",
				"services",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{"apps"},
			Resources: []string{"deployments"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"etcd.database.coreos.com"},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"machine.openshift.io"},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
	}
	return nil
}

func reconcileControlPlaneOperatorRoleBinding(binding *rbacv1.RoleBinding, role *rbacv1.Role, sa *corev1.ServiceAccount) error {
	binding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     role.Name,
	}

	binding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      sa.Name,
			Namespace: sa.Namespace,
		},
	}

	return nil
}

func reconcileExternalInfraCluster(eic *hyperv1.ExternalInfraCluster, hcluster *hyperv1.HostedCluster) error {
	// We only create this resource once and then let CAPI own it
	if !eic.CreationTimestamp.IsZero() {
		return nil
	}

	eic.Annotations = map[string]string{
		hostedClusterAnnotation: ctrlclient.ObjectKeyFromObject(hcluster).String(),
	}

	if hcluster.Spec.Platform.AWS != nil {
		eic.Spec.Region = hcluster.Spec.Platform.AWS.Region
	}

	return nil
}

func reconcileCAPICluster(cluster *capiv1.Cluster, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane, eic *hyperv1.ExternalInfraCluster) error {
	// We only create this resource once and then let CAPI own it
	if !cluster.CreationTimestamp.IsZero() {
		return nil
	}

	cluster.Annotations = map[string]string{
		hostedClusterAnnotation: ctrlclient.ObjectKeyFromObject(hcluster).String(),
	}

	cluster.Spec = capiv1.ClusterSpec{
		ControlPlaneEndpoint: capiv1.APIEndpoint{},
		ControlPlaneRef: &corev1.ObjectReference{
			APIVersion: "hypershift.openshift.io/v1alpha1",
			Kind:       "HostedControlPlane",
			Namespace:  hcp.Namespace,
			Name:       hcp.Name,
		},
		InfrastructureRef: &corev1.ObjectReference{
			APIVersion: "hypershift.openshift.io/v1alpha1",
			Kind:       "ExternalInfraCluster",
			Namespace:  eic.Namespace,
			Name:       eic.Name,
		},
	}

	return nil
}

func reconcileCAPIManagerDeployment(deployment *appsv1.Deployment, sa *corev1.ServiceAccount, image string) error {
	deployment.Spec = appsv1.DeploymentSpec{
		Replicas: k8sutilspointer.Int32Ptr(1),
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"name": "cluster-api",
			},
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"name": "cluster-api",
				},
			},
			Spec: corev1.PodSpec{
				ServiceAccountName: sa.Name,
				Containers: []corev1.Container{
					{
						Name:            "manager",
						Image:           image,
						ImagePullPolicy: corev1.PullAlways,
						Env: []corev1.EnvVar{
							{
								Name: "MY_NAMESPACE",
								ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{
										FieldPath: "metadata.namespace",
									},
								},
							},
						},
						Command: []string{"/manager"},
						Args:    []string{"--namespace", "$(MY_NAMESPACE)", "--alsologtostderr", "--v=4"},
					},
				},
			},
		},
	}
	return nil
}

func reconcileCAPIManagerClusterRole(role *rbacv1.ClusterRole) error {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"apiextensions.k8s.io"},
			Resources: []string{"customresourcedefinitions"},
			Verbs:     []string{"get", "list", "watch"},
		},
	}
	return nil
}

func reconcileCAPIManagerClusterRoleBinding(binding *rbacv1.ClusterRoleBinding, role *rbacv1.ClusterRole, sa *corev1.ServiceAccount) error {
	binding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "ClusterRole",
		Name:     role.Name,
	}

	binding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      sa.Name,
			Namespace: sa.Namespace,
		},
	}
	return nil
}

func reconcileCAPIManagerRole(role *rbacv1.Role) error {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{
				"bootstrap.cluster.x-k8s.io",
				"controlplane.cluster.x-k8s.io",
				"infrastructure.cluster.x-k8s.io",
				"machines.cluster.x-k8s.io",
				"exp.infrastructure.cluster.x-k8s.io",
				"addons.cluster.x-k8s.io",
				"exp.cluster.x-k8s.io",
				"cluster.x-k8s.io",
			},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"hypershift.openshift.io"},
			Resources: []string{
				"hostedcontrolplanes",
				"hostedcontrolplanes/status",
				"externalinfraclusters",
				"externalinfraclusters/status",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{
				"configmaps",
				"events",
				"nodes",
				"secrets",
			},
			Verbs: []string{"*"},
		},
	}
	return nil
}

func reconcileCAPIManagerRoleBinding(binding *rbacv1.RoleBinding, role *rbacv1.Role, sa *corev1.ServiceAccount) error {
	binding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     role.Name,
	}

	binding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      sa.Name,
			Namespace: sa.Namespace,
		},
	}

	return nil
}

func reconcileCAPIAWSProviderDeployment(deployment *appsv1.Deployment, sa *corev1.ServiceAccount, image string) error {
	deployment.Spec = appsv1.DeploymentSpec{
		Replicas: k8sutilspointer.Int32Ptr(1),
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"control-plane": "capa-controller-manager",
			},
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"control-plane": "capa-controller-manager",
				},
			},
			Spec: corev1.PodSpec{
				ServiceAccountName:            sa.Name,
				TerminationGracePeriodSeconds: k8sutilspointer.Int64Ptr(10),
				Tolerations: []corev1.Toleration{
					{
						Key:    "node-role.kubernetes.io/master",
						Effect: corev1.TaintEffectNoSchedule,
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "credentials",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName: manifests.AWSNodePoolManagementCreds(deployment.Namespace).Name,
							},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Name:            "manager",
						Image:           image,
						ImagePullPolicy: corev1.PullAlways,
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "credentials",
								MountPath: "/home/.aws",
							},
						},
						Env: []corev1.EnvVar{
							{
								Name: "MY_NAMESPACE",
								ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{
										FieldPath: "metadata.namespace",
									},
								},
							},
							{
								Name:  "AWS_SHARED_CREDENTIALS_FILE",
								Value: "/home/.aws/credentials",
							},
						},
						Command: []string{"/manager"},
						Args:    []string{"--namespace", "$(MY_NAMESPACE)", "--alsologtostderr", "--v=4"},
						Ports: []corev1.ContainerPort{
							{
								Name:          "healthz",
								ContainerPort: 9440,
								Protocol:      corev1.ProtocolTCP,
							},
						},
						LivenessProbe: &corev1.Probe{
							Handler: corev1.Handler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/healthz",
									Port: intstr.FromString("healthz"),
								},
							},
						},
						ReadinessProbe: &corev1.Probe{
							Handler: corev1.Handler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/readyz",
									Port: intstr.FromString("healthz"),
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

func reconcileCAPIAWSProviderRole(role *rbacv1.Role) error {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{
				"events",
				"secrets",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{
				"bootstrap.cluster.x-k8s.io",
				"controlplane.cluster.x-k8s.io",
				"infrastructure.cluster.x-k8s.io",
				"machines.cluster.x-k8s.io",
				"exp.infrastructure.cluster.x-k8s.io",
				"addons.cluster.x-k8s.io",
				"exp.cluster.x-k8s.io",
				"cluster.x-k8s.io",
			},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"hypershift.openshift.io"},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
	}
	return nil
}

func reconcileCAPIAWSProviderRoleBinding(binding *rbacv1.RoleBinding, role *rbacv1.Role, sa *corev1.ServiceAccount) error {
	binding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     role.Name,
	}

	binding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      sa.Name,
			Namespace: sa.Namespace,
		},
	}
	return nil
}

func reconcileAutoScalerDeployment(deployment *appsv1.Deployment, sa *corev1.ServiceAccount, kubeConfigSecret *corev1.Secret, image string, options hyperv1.ClusterAutoscaling) error {
	args := []string{
		"--cloud-provider=clusterapi",
		"--node-group-auto-discovery=clusterapi:namespace=$(MY_NAMESPACE)",
		"--kubeconfig=/mnt/kubeconfig/target-kubeconfig",
		"--clusterapi-cloud-config-authoritative",
		"--alsologtostderr",
		"--v=4",
	}

	// TODO if the options for the cluster autoscaler continues to grow, we should take inspiration
	// from the cluster-autoscaler-operator and create some utility functions for these assignments.
	if options.MaxNodesTotal != nil {
		arg := fmt.Sprintf("%s=%d", "--max-nodes-total", *options.MaxNodesTotal)
		args = append(args, arg)
	}

	if options.MaxPodGracePeriod != nil {
		arg := fmt.Sprintf("%s=%d", "--max-graceful-termination-sec", *options.MaxPodGracePeriod)
		args = append(args, arg)
	}

	if options.MaxNodeProvisionTime != "" {
		arg := fmt.Sprintf("%s=%s", "--max-node-provision-time", options.MaxNodeProvisionTime)
		args = append(args, arg)
	}

	if options.PodPriorityThreshold != nil {
		arg := fmt.Sprintf("%s=%d", "--expendable-pods-priority-cutoff", *options.PodPriorityThreshold)
		args = append(args, arg)
	}

	deployment.Spec = appsv1.DeploymentSpec{
		Replicas: k8sutilspointer.Int32Ptr(1),
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app": "cluster-autoscaler",
			},
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": "cluster-autoscaler",
				},
			},
			Spec: corev1.PodSpec{
				ServiceAccountName:            sa.Name,
				TerminationGracePeriodSeconds: k8sutilspointer.Int64Ptr(10),
				Tolerations: []corev1.Toleration{
					{
						Key:    "node-role.kubernetes.io/master",
						Effect: corev1.TaintEffectNoSchedule,
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "target-kubeconfig",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName: kubeConfigSecret.Name,
								Items: []corev1.KeyToPath{
									{
										// TODO: should the key be published on status?
										Key:  "value",
										Path: "target-kubeconfig",
									},
								},
							},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Name:            "cluster-autoscaler",
						Image:           image,
						ImagePullPolicy: corev1.PullAlways,
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "target-kubeconfig",
								MountPath: "/mnt/kubeconfig",
							},
						},
						Env: []corev1.EnvVar{
							{
								Name: "MY_NAMESPACE",
								ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{
										FieldPath: "metadata.namespace",
									},
								},
							},
						},
						Command: []string{"/cluster-autoscaler"},
						Args:    args,
					},
				},
			},
		},
	}

	return nil
}

func reconcileAutoScalerRole(role *rbacv1.Role) error {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"apiextensions.k8s.io"},
			Resources: []string{"customresourcedefinitions"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"cluster.x-k8s.io"},
			Resources: []string{
				"machinedeployments",
				"machinedeployments/scale",
				"machines",
				"machinesets",
				"machinesets/scale",
			},
			Verbs: []string{"*"},
		},
	}
	return nil
}

func reconcileAutoScalerRoleBinding(binding *rbacv1.RoleBinding, role *rbacv1.Role, sa *corev1.ServiceAccount) error {
	binding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     role.Name,
	}

	binding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      sa.Name,
			Namespace: sa.Namespace,
		},
	}

	return nil
}

// computeClusterVersionStatus determines the ClusterVersionStatus of the
// given HostedCluster and returns it.
func computeClusterVersionStatus(clock clock.Clock, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) *hyperv1.ClusterVersionStatus {
	// If there's no history, rebuild it from scratch.
	if hcluster.Status.Version == nil || len(hcluster.Status.Version.History) == 0 {
		return &hyperv1.ClusterVersionStatus{
			Desired:            hcluster.Spec.Release,
			ObservedGeneration: hcluster.Generation,
			History: []configv1.UpdateHistory{
				{
					State:       configv1.PartialUpdate,
					Image:       hcluster.Spec.Release.Image,
					StartedTime: metav1.NewTime(clock.Now()),
				},
			},
		}
	}

	// Reconcile the current version with the latest resource states.
	version := hcluster.Status.Version.DeepCopy()

	// If the hosted control plane doesn't exist, there's no way to assess the
	// rollout so return early.
	if hcp == nil {
		return version
	}

	// If a rollout is in progress, we need to wait before updating.
	// TODO: This is a potentially weak check. Conditions checks don't seem
	// quite right because the intent here is to identify a terminal rollout
	// state. For now it assumes when status.releaseImage matches, that rollout
	// is definitely done.
	hcpRolloutComplete := hcp.Spec.ReleaseImage == hcp.Status.ReleaseImage
	if !hcpRolloutComplete {
		return version
	}

	// The rollout is complete, so update the current history entry
	version.History[0].State = configv1.CompletedUpdate
	version.History[0].Version = hcp.Status.Version
	if hcp.Status.LastReleaseImageTransitionTime != nil {
		version.History[0].CompletionTime = hcp.Status.LastReleaseImageTransitionTime.DeepCopy()
	}

	// If a new rollout is needed, update the desired version and prepend a new
	// partial history entry to unblock rollouts.
	rolloutNeeded := hcluster.Spec.Release.Image != hcluster.Status.Version.Desired.Image
	if rolloutNeeded {
		version.Desired.Image = hcluster.Spec.Release.Image
		version.ObservedGeneration = hcluster.Generation
		// TODO: leaky
		version.History = append([]configv1.UpdateHistory{
			{
				State:       configv1.PartialUpdate,
				Image:       hcluster.Spec.Release.Image,
				StartedTime: metav1.NewTime(clock.Now()),
			},
		}, version.History...)
	}

	return version
}

// computeHostedClusterAvailability determines the Available condition for the
// given HostedCluster and returns it.
func computeHostedClusterAvailability(hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) metav1.Condition {
	// Determine whether the hosted control plane is available.
	hcpAvailable := false
	if hcp != nil {
		for _, cond := range hcp.Status.Conditions {
			if cond.Type == hyperv1.Available && cond.Status == hyperv1.ConditionTrue {
				hcpAvailable = true
				break
			}
		}
	}

	// Determine whether the kubeconfig is available.
	// TODO: is it a good idea to compute hc status based on other field within
	// the same resource like this? does it imply an ordering requirement that
	// kubeconfig status must come before availability status? would extracting
	// the kubeconfig as an argument help by making that dependency explicit?
	kubeConfigAvailable := hcluster.Status.KubeConfig != nil

	switch {
	case hcpAvailable && kubeConfigAvailable:
		return metav1.Condition{
			Type:               string(hyperv1.Available),
			Status:             metav1.ConditionTrue,
			ObservedGeneration: hcluster.Generation,
			Reason:             "HostedClusterIsAvailable",
		}
	default:
		var messages []string
		if !hcpAvailable {
			messages = append(messages, "the hosted control plane is unavailable")
		}
		if !kubeConfigAvailable {
			messages = append(messages, "the hosted control plane kubeconfig is unavailable")
		}
		return metav1.Condition{
			Type:               string(hyperv1.Available),
			Status:             metav1.ConditionFalse,
			ObservedGeneration: hcluster.Generation,
			Reason:             "HostedClusterIsUnavailable",
			Message:            strings.Join(messages, "; "),
		}
	}
}

func (r *HostedClusterReconciler) listNodePools(clusterNamespace, clusterName string) ([]hyperv1.NodePool, error) {
	nodePoolList := &hyperv1.NodePoolList{}
	if err := r.Client.List(
		context.TODO(),
		nodePoolList,
	); err != nil {
		return nil, fmt.Errorf("failed getting nodePool list: %v", err)
	}
	// TODO: do a label association or something
	filtered := []hyperv1.NodePool{}
	for i, nodePool := range nodePoolList.Items {
		if nodePool.Namespace == clusterNamespace && nodePool.Spec.ClusterName == clusterName {
			filtered = append(filtered, nodePoolList.Items[i])
		}
	}
	return filtered, nil
}

func (r *HostedClusterReconciler) delete(ctx context.Context, req ctrl.Request, hc *hyperv1.HostedCluster) (bool, error) {
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(req.Namespace, req.Name).Name

	nodePools, err := r.listNodePools(req.Namespace, req.Name)
	if err != nil {
		return false, fmt.Errorf("failed to get nodePools by cluster name for cluster %q: %w", req.Name, err)
	}

	for key := range nodePools {
		if err := r.Delete(ctx, &nodePools[key]); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("failed to delete nodePool %q for cluster %q: %w", nodePools[key].GetName(), req.Name, err)
		}
	}

	if hc != nil && len(hc.Spec.InfraID) > 0 {
		r.Log.Info("Deleting Cluster", "clusterName", hc.Spec.InfraID, "clusterNamespace", controlPlaneNamespace)
		cluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hc.Spec.InfraID,
				Namespace: controlPlaneNamespace,
			},
		}

		if err := r.Delete(ctx, cluster); err != nil {
			if !apierrors.IsNotFound(err) {
				return false, fmt.Errorf("error deleting Cluster: %w", err)
			}
			// The advancing case is when Delete() returns an error that the cluster is not found
		} else {
			r.Log.Info("Waiting for Cluster deletion", "clusterName", hc.Spec.InfraID, "clusterNamespace", controlPlaneNamespace)
			return false, nil
		}
	}

	r.Log.Info("Deleting controlplane namespace", "namespace", controlPlaneNamespace)
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: controlPlaneNamespace},
	}
	if err := r.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("failed to delete namespace: %w", err)
	}
	return true, nil
}

func enqueueParentHostedCluster(obj ctrlclient.Object) []reconcile.Request {
	var hostedClusterName string
	if obj.GetAnnotations() != nil {
		hostedClusterName = obj.GetAnnotations()[hostedClusterAnnotation]
	}
	if hostedClusterName == "" {
		return []reconcile.Request{}
	}
	return []reconcile.Request{
		{NamespacedName: hyperutil.ParseNamespacedName(hostedClusterName)},
	}
}
