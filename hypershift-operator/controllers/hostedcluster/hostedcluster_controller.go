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
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/manifests/autoscaler"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/manifests/clusterapi"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/manifests/controlplaneoperator"
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

type realClock struct{}

func (_ realClock) Now() time.Time { return time.Now() }

type Clock interface {
	Now() time.Time
}

// HostedClusterReconciler reconciles a HostedCluster object
type HostedClusterReconciler struct {
	client.Client

	Log           logr.Logger
	OperatorImage string
	Clock
}

// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedclusters/status,verbs=get;update;patch

func (r *HostedClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Clock == nil {
		r.Clock = realClock{}
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
		kubeConfigSecret := &corev1.Secret{}
		err := r.Client.Get(ctx, manifests.KubeConfigSecretName(hcluster.Namespace, hcluster.Name), kubeConfigSecret)
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
		controlPlaneNamespaceName := manifests.HostedControlPlaneNamespaceName(hcluster.Namespace, hcluster.Name)
		hcp := &hyperv1.HostedControlPlane{}
		err := r.Client.Get(ctx, controlplaneoperator.HostedControlPlaneName(controlPlaneNamespaceName.Name, hcluster.Name), hcp)
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
		controlPlaneNamespaceName := manifests.HostedControlPlaneNamespaceName(hcluster.Namespace, hcluster.Name)
		hcp := &hyperv1.HostedControlPlane{}
		err := r.Client.Get(ctx, controlplaneoperator.HostedControlPlaneName(controlPlaneNamespaceName.Name, hcluster.Name), hcp)
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
	controlPlaneNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: manifests.HostedControlPlaneNamespaceName(hcluster.Namespace, hcluster.Name).Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, controlPlaneNamespace, NoopReconcile)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile namespace: %w", err)
	}

	// Reconcile the shared provider credentials secret by resolving the reference
	// from the HostedCluster and syncing the secret in the control plane namespace.
	var hostedClusterProviderCredsSecret corev1.Secret
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.ProviderCreds.Name}, &hostedClusterProviderCredsSecret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get provider creds %s: %w", hcluster.Spec.ProviderCreds.Name, err)
	}
	controlPlaneProviderCredsSecretName := controlplaneoperator.ProviderCredentialsName(controlPlaneNamespace.Name)
	controlPlaneProviderCredsSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneProviderCredsSecretName.Namespace,
			Name:      controlPlaneProviderCredsSecretName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &controlPlaneProviderCredsSecret, func() error {
		hostedClusterProviderCredsData, hasProviderCredsData := hostedClusterProviderCredsSecret.Data["credentials"]
		if !hasProviderCredsData {
			return fmt.Errorf("hostedcluster provider credentials secret %q must have a credentials key", hostedClusterProviderCredsSecret.Name)
		}
		controlPlaneProviderCredsSecret.Type = corev1.SecretTypeOpaque
		if controlPlaneProviderCredsSecret.Data == nil {
			controlPlaneProviderCredsSecret.Data = map[string][]byte{}
		}
		controlPlaneProviderCredsSecret.Data["credentials"] = hostedClusterProviderCredsData
		return nil
	})

	// Reconcile the HostedControlPlane pull secret by resolving the source secret
	// reference from the HostedCluster and syncing the secret in the control plane namespace.
	var hostedClusterPullSecret corev1.Secret
	if err := r.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.PullSecret.Name}, &hostedClusterPullSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get pull secret %s: %w", hcluster.Spec.PullSecret.Name, err)
	}
	controlPlaneSecretName := controlplaneoperator.PullSecretName(controlPlaneNamespace.Name)
	controlPlanePullSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneSecretName.Namespace,
			Name:      controlPlaneSecretName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &controlPlanePullSecret, func() error {
		hostedClusterPullSecretData, hasPullSecretData := hostedClusterPullSecret.Data[".dockerconfigjson"]
		if !hasPullSecretData {
			return fmt.Errorf("hostedcluster pull secret %q must have a .dockerconfigjson key", hostedClusterPullSecret.Name)
		}
		controlPlanePullSecret.Type = corev1.SecretTypeDockerConfigJson
		if controlPlanePullSecret.Data == nil {
			controlPlanePullSecret.Data = map[string][]byte{}
		}
		controlPlanePullSecret.Data[".dockerconfigjson"] = hostedClusterPullSecretData
		return nil
	})

	// Reconcile the HostedControlPlane signing key by resolving the source secret
	// reference from the HostedCluster and syncing the secret in the control plane namespace.
	var hostedClusterSigningKeySecret corev1.Secret
	if err := r.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.SigningKey.Name}, &hostedClusterSigningKeySecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get signing key %s: %w", hcluster.Spec.SigningKey.Name, err)
	}
	controlPlaneSigningKeySecretName := controlplaneoperator.SigningKeyName(controlPlaneNamespace.Name)
	controlPlaneSigningKeySecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneSigningKeySecretName.Namespace,
			Name:      controlPlaneSigningKeySecretName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &controlPlaneSigningKeySecret, func() error {
		hostedClusterSigningKeySecretData, hasSigningKeyData := hostedClusterSigningKeySecret.Data["key"]
		if !hasSigningKeyData {
			return fmt.Errorf("hostedcluster signing key %q must have a key key", hostedClusterSigningKeySecret.Name)
		}
		controlPlaneSigningKeySecret.Type = corev1.SecretTypeOpaque
		if controlPlaneSigningKeySecret.Data == nil {
			controlPlaneSigningKeySecret.Data = map[string][]byte{}
		}
		controlPlaneSigningKeySecret.Data["key"] = hostedClusterSigningKeySecretData
		return nil
	})

	// Reconcile the HostedControlPlane SSH secret by resolving the source secret reference
	// from the HostedCluster and syncing the secret in the control plane namespace.
	var controlPlaneSSHKeySecret *corev1.Secret
	if len(hcluster.Spec.SSHKey.Name) > 0 {
		var hostedClusterSSHKeySecret corev1.Secret
		err = r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.Namespace, Name: hcluster.Spec.SSHKey.Name}, &hostedClusterSSHKeySecret)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get hostedcluster SSH key secret %s: %w", hcluster.Spec.SSHKey.Name, err)
		}
		controlPlaneSSHKeySecretName := controlplaneoperator.SSHKeyName(controlPlaneNamespace.Name)
		controlPlaneSSHKeySecret := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: controlPlaneSSHKeySecretName.Namespace,
				Name:      controlPlaneSSHKeySecretName.Name,
			},
		}
		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &controlPlaneSSHKeySecret, func() error {
			hostedClusterSSHKeyData, hasSSHKeyData := hostedClusterSSHKeySecret.Data["id_rsa.pub"]
			if !hasSSHKeyData {
				return fmt.Errorf("hostedcluster ssh key secret %q must have a id_rsa.pub key", hostedClusterSSHKeySecret.Name)
			}
			controlPlaneSSHKeySecret.Type = corev1.SecretTypeOpaque
			if controlPlaneSSHKeySecret.Data == nil {
				controlPlaneSSHKeySecret.Data = map[string][]byte{}
			}
			controlPlaneSSHKeySecret.Data["id_rsa.pub"] = hostedClusterSSHKeyData
			return nil
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile controlplane ssh secret: %w", err)
		}
	} else {
		controlPlaneSSHKeySecret = nil
	}

	// Reconcile the default node pool
	// TODO: Is this really a good idea to have on the API? If you want an initial
	// node pool, create it through whatever user-oriented tool is consuming the
	// API.
	if hcluster.Spec.InitialComputeReplicas > 0 {
		nodePoolName := manifests.DefaultNodePoolName(hcluster.Namespace, hcluster.Name)
		nodePool := hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: nodePoolName.Namespace,
				Name:      nodePoolName.Name,
			},
		}
		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &nodePool, func() error {
			return reconcileDefaultNodePool(&nodePool, hcluster)
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile initial node pool: %w", err)
		}
	}

	// Reconcile the CAPI ExternalInfraCluster
	externalInfraClusterName := controlplaneoperator.ExternalInfraClusterName(controlPlaneNamespace.Name, hcluster.Name)
	externalInfraCluster := hyperv1.ExternalInfraCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: externalInfraClusterName.Namespace,
			Name:      externalInfraClusterName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &externalInfraCluster, func() error {
		return reconcileExternalInfraCluster(&externalInfraCluster, hcluster)
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile externalinfracluster: %w", err)
	}

	// Reconcile the HostedControlPlane
	hcpName := controlplaneoperator.HostedControlPlaneName(controlPlaneNamespace.Name, hcluster.Name)
	hcp := hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hcpName.Namespace,
			Name:      hcpName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &hcp, func() error {
		return reconcileHostedControlPlane(&hcp, hcluster, &controlPlaneProviderCredsSecret, &controlPlanePullSecret, &controlPlaneSigningKeySecret, controlPlaneSSHKeySecret)
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile hostedcontrolplane: %w", err)
	}

	// Reconcile the CAPI Cluster resource
	capiClusterName := controlplaneoperator.CAPIClusterName(controlPlaneNamespace.Name, hcluster.Spec.InfraID)
	capiCluster := capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: capiClusterName.Namespace,
			Name:      capiClusterName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &capiCluster, func() error {
		return reconcileCAPICluster(&capiCluster, hcluster, &hcp, &externalInfraCluster)
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile capi cluster: %w", err)
	}

	// Reconcile the HostedControlPlane kubeconfig if one is reported
	if hcp.Status.KubeConfig != nil {
		controlPlaneKubeConfigSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hcp.Namespace,
				Name:      hcp.Status.KubeConfig.Name,
			},
		}
		err := r.Client.Get(ctx, client.ObjectKeyFromObject(controlPlaneKubeConfigSecret), controlPlaneKubeConfigSecret)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get controlplane kubeconfig secret %q: %w", client.ObjectKeyFromObject(controlPlaneKubeConfigSecret), err)
		}
		hostedClusterKubeConfigSecretName := manifests.KubeConfigSecretName(hcluster.Namespace, hcluster.Name)
		hostedClusterKubeConfigSecret := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hostedClusterKubeConfigSecretName.Namespace,
				Name:      hostedClusterKubeConfigSecretName.Name,
			},
		}
		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &hostedClusterKubeConfigSecret, func() error {
			key := hcp.Status.KubeConfig.Key
			controlPlaneKubeConfigData, ok := controlPlaneKubeConfigSecret.Data[key]
			if !ok {
				return fmt.Errorf("controlplane kubeconfig secret %q must have a %q key", client.ObjectKeyFromObject(controlPlaneKubeConfigSecret), key)
			}
			hostedClusterKubeConfigSecret.Type = corev1.SecretTypeOpaque
			if hostedClusterKubeConfigSecret.Data == nil {
				hostedClusterKubeConfigSecret.Data = map[string][]byte{}
			}
			hostedClusterKubeConfigSecret.Data["kubeconfig"] = controlPlaneKubeConfigData
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
	err = r.reconcileAutoscaler(ctx, hcluster, &hcp)
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
func reconcileHostedControlPlane(hcp *hyperv1.HostedControlPlane, hcluster *hyperv1.HostedCluster, providerCreds *corev1.Secret, pullSecret *corev1.Secret, signingKey *corev1.Secret, sshKey *corev1.Secret) error {
	// Always initialize the HostedControlPlane with an image matching
	// the HostedCluster.
	if hcp.ObjectMeta.CreationTimestamp.IsZero() {
		hcp.Spec.ReleaseImage = hcluster.Spec.Release.Image
	}

	hcp.Annotations = map[string]string{
		hostedClusterAnnotation: ctrlclient.ObjectKeyFromObject(hcluster).String(),
	}

	if providerCreds != nil {
		hcp.Spec.ProviderCreds = corev1.LocalObjectReference{
			Name: providerCreds.Name,
		}
	}

	if pullSecret != nil {
		hcp.Spec.PullSecret = corev1.LocalObjectReference{
			Name: pullSecret.Name,
		}
	}

	if signingKey != nil {
		hcp.Spec.SigningKey = corev1.LocalObjectReference{
			Name: signingKey.Name,
		}
	}

	if sshKey != nil {
		hcp.Spec.SSHKey = corev1.LocalObjectReference{
			Name: sshKey.Name,
		}
	}

	hcp.Spec.IssuerURL = hcluster.Spec.IssuerURL
	hcp.Spec.ServiceCIDR = hcluster.Spec.Networking.ServiceCIDR
	hcp.Spec.PodCIDR = hcluster.Spec.Networking.PodCIDR
	hcp.Spec.MachineCIDR = hcluster.Spec.Networking.MachineCIDR
	hcp.Spec.InfraID = hcluster.Spec.InfraID
	hcp.Spec.Platform = hcluster.Spec.Platform
	hcp.Spec.KubeConfig = &hyperv1.KubeconfigSecretRef{
		Name: fmt.Sprintf("%s-kubeconfig", hcluster.Spec.InfraID),
		Key:  "value",
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
	controlPlaneNamespace := &corev1.Namespace{}
	err := r.Client.Get(ctx, manifests.HostedControlPlaneNamespaceName(hcluster.Namespace, hcluster.Name), controlPlaneNamespace)
	if err != nil {
		return fmt.Errorf("failed to get control plane namespace: %w", err)
	}

	// Reconcile CAPI manager service account
	capiManagerServiceAccountName := clusterapi.CAPIManagerServiceAccountName(controlPlaneNamespace.Name)
	capiManagerServiceAccount := corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: capiManagerServiceAccountName.Namespace,
			Name:      capiManagerServiceAccountName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &capiManagerServiceAccount, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager service account: %w", err)
	}

	// Reconcile CAPI manager cluster role
	capiManagerClusterRoleName := clusterapi.CAPIManagerClusterRoleName(controlPlaneNamespace.Name)
	capiManagerClusterRole := rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: capiManagerClusterRoleName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &capiManagerClusterRole, func() error {
		return reconcileCAPIManagerClusterRole(&capiManagerClusterRole)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager cluster role: %w", err)
	}

	// Reconcile CAPI manager cluster role binding
	capiManagerClusterRoleBindingName := clusterapi.CAPIManagerClusterRoleBindingName(controlPlaneNamespace.Name)
	capiManagerClusterRoleBinding := rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: capiManagerClusterRoleBindingName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &capiManagerClusterRoleBinding, func() error {
		return reconcileCAPIManagerClusterRoleBinding(&capiManagerClusterRoleBinding, &capiManagerClusterRole, &capiManagerServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager cluster role binding: %w", err)
	}

	// Reconcile CAPI manager role
	capiManagerRoleName := clusterapi.CAPIManagerRoleName(controlPlaneNamespace.Name)
	capiManagerRole := rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: capiManagerRoleName.Namespace,
			Name:      capiManagerRoleName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &capiManagerRole, func() error {
		return reconcileCAPIManagerRole(&capiManagerRole)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager role: %w", err)
	}

	// Reconcile CAPI manager role binding
	capiManagerRoleBindingName := clusterapi.CAPIManagerRoleBindingName(controlPlaneNamespace.Name)
	capiManagerRoleBinding := rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: capiManagerRoleBindingName.Namespace,
			Name:      capiManagerRoleBindingName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &capiManagerRoleBinding, func() error {
		return reconcileCAPIManagerRoleBinding(&capiManagerRoleBinding, &capiManagerRole, &capiManagerServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager role: %w", err)
	}

	// Reconcile CAPI manager deployment
	capiManagerDeploymentName := clusterapi.ClusterAPIManagerDeploymentName(controlPlaneNamespace.Name)
	capiManagerDeployment := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: capiManagerDeploymentName.Namespace,
			Name:      capiManagerDeploymentName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &capiManagerDeployment, func() error {
		return reconcileCAPIManagerDeployment(&capiManagerDeployment, &capiManagerServiceAccount, "quay.io/hypershift/cluster-api:hypershift")
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager deployment: %w", err)
	}

	return nil
}

// reconcileCAPIAWSProvider orchestrates reconciliation of the CAPI AWS provider
// components.
func (r *HostedClusterReconciler) reconcileCAPIAWSProvider(ctx context.Context, hcluster *hyperv1.HostedCluster) error {
	controlPlaneNamespace := &corev1.Namespace{}
	err := r.Client.Get(ctx, manifests.HostedControlPlaneNamespaceName(hcluster.Namespace, hcluster.Name), controlPlaneNamespace)
	if err != nil {
		return fmt.Errorf("failed to get control plane namespace: %w", err)
	}

	providerCredentialsSecret := &corev1.Secret{}
	err = r.Client.Get(ctx, controlplaneoperator.ProviderCredentialsName(controlPlaneNamespace.Name), providerCredentialsSecret)
	if err != nil {
		return fmt.Errorf("failed to get provider credentials secret: %w", err)
	}

	// Reconcile CAPI AWS provider role
	capiAwsProviderRoleName := clusterapi.CAPIAWSProviderRoleName(controlPlaneNamespace.Name)
	capiAwsProviderRole := rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: capiAwsProviderRoleName.Namespace,
			Name:      capiAwsProviderRoleName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &capiAwsProviderRole, func() error {
		return reconcileCAPIAWSProviderRole(&capiAwsProviderRole)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi aws provider role: %w", err)
	}

	// Reconcile CAPI AWS provider service account
	capiAwsProviderServiceAccountName := clusterapi.CAPIAWSProviderServiceAccountName(controlPlaneNamespace.Name)
	capiAwsProviderServiceAccount := corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: capiAwsProviderServiceAccountName.Namespace,
			Name:      capiAwsProviderServiceAccountName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &capiAwsProviderServiceAccount, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile capi aws provider service account: %w", err)
	}

	// Reconcile CAPI AWS provider role binding
	capiAwsProviderRoleBindingName := clusterapi.CAPIAWSProviderRoleBindingName(controlPlaneNamespace.Name)
	capiAwsProviderRoleBinding := rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: capiAwsProviderRoleBindingName.Namespace,
			Name:      capiAwsProviderRoleBindingName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &capiAwsProviderRoleBinding, func() error {
		return reconcileCAPIAWSProviderRoleBinding(&capiAwsProviderRoleBinding, &capiAwsProviderRole, &capiAwsProviderServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi aws provider role binding: %w", err)
	}

	// Reconcile CAPI AWS provider deployment
	capiAwsProviderDeploymentName := clusterapi.CAPIAWSProviderDeploymentName(controlPlaneNamespace.Name)
	capiAwsProviderDeployment := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: capiAwsProviderDeploymentName.Namespace,
			Name:      capiAwsProviderDeploymentName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &capiAwsProviderDeployment, func() error {
		return reconcileCAPIAWSProviderDeployment(&capiAwsProviderDeployment, &capiAwsProviderServiceAccount, providerCredentialsSecret, "quay.io/hypershift/cluster-api-provider-aws:master")
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi aws provider deployment: %w", err)
	}

	return nil
}

// reconcileControlPlaneOperator orchestrates reconciliation of the control plane
// operator components.
func (r *HostedClusterReconciler) reconcileControlPlaneOperator(ctx context.Context, hcluster *hyperv1.HostedCluster) error {
	controlPlaneNamespace := &corev1.Namespace{}
	err := r.Client.Get(ctx, manifests.HostedControlPlaneNamespaceName(hcluster.Namespace, hcluster.Name), controlPlaneNamespace)
	if err != nil {
		return fmt.Errorf("failed to get control plane namespace: %w", err)
	}

	// Reconcile operator service account
	controlPlaneOperatorServiceAccountName := controlplaneoperator.OperatorServiceAccountName(controlPlaneNamespace.Name)
	controlPlaneOperatorServiceAccount := corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorServiceAccountName.Namespace,
			Name:      controlPlaneOperatorServiceAccountName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &controlPlaneOperatorServiceAccount, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator service account: %w", err)
	}

	// Reconcile operator cluster role
	controlPlaneOperatorClusterRoleName := controlplaneoperator.OperatorClusterRoleName()
	controlPlaneOperatorClusterRole := rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: controlPlaneOperatorClusterRoleName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &controlPlaneOperatorClusterRole, func() error {
		return reconcileControlPlaneOperatorClusterRole(&controlPlaneOperatorClusterRole)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator cluster role: %w", err)
	}

	// Reconcile operator cluster role binding
	controlPlaneOperatorClusterRoleBindingName := controlplaneoperator.OperatorClusterRoleBindingName(controlPlaneNamespace.Name)
	controlPlaneOperatorClusterRoleBinding := rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: controlPlaneOperatorClusterRoleBindingName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &controlPlaneOperatorClusterRoleBinding, func() error {
		return reconcileControlPlaneOperatorClusterRoleBinding(&controlPlaneOperatorClusterRoleBinding, &controlPlaneOperatorClusterRole, &controlPlaneOperatorServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator clusterrolebinding: %w", err)
	}

	// Reconcile operator role
	controlPlaneOperatorRoleName := controlplaneoperator.OperatorRoleName(controlPlaneNamespace.Name)
	controlPlaneOperatorRole := rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorRoleName.Namespace,
			Name:      controlPlaneOperatorRoleName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &controlPlaneOperatorRole, func() error {
		return reconcileControlPlaneOperatorRole(&controlPlaneOperatorRole)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator clusterrole: %w", err)
	}

	// Reconcile operator role binding
	controlPlaneOperatorRoleBindingName := controlplaneoperator.OperatorRoleBindingName(controlPlaneNamespace.Name)
	controlPlaneOperatorRoleBinding := rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorRoleBindingName.Namespace,
			Name:      controlPlaneOperatorRoleBindingName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &controlPlaneOperatorRoleBinding, func() error {
		return reconcileControlPlaneOperatorRoleBinding(&controlPlaneOperatorRoleBinding, &controlPlaneOperatorRole, &controlPlaneOperatorServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator rolebinding: %w", err)
	}

	// Reconcile operator deployment
	controlPlaneOperatorDeploymentName := controlplaneoperator.OperatorDeploymentName(controlPlaneNamespace.Name)
	controlPlaneOperatorDeployment := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorDeploymentName.Namespace,
			Name:      controlPlaneOperatorDeploymentName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &controlPlaneOperatorDeployment, func() error {
		return reconcileControlPlaneOperatorDeployment(&controlPlaneOperatorDeployment, r.OperatorImage, &controlPlaneOperatorServiceAccount)
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
	controlPlaneNamespace := &corev1.Namespace{}
	err := r.Client.Get(ctx, manifests.HostedControlPlaneNamespaceName(hcluster.Namespace, hcluster.Name), controlPlaneNamespace)
	if err != nil {
		return fmt.Errorf("failed to get control plane namespace: %w", err)
	}

	// Reconcile autoscaler role
	autoScalerRoleName := autoscaler.AutoScalerRoleName(controlPlaneNamespace.Name)
	autoScalerRole := rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: autoScalerRoleName.Namespace,
			Name:      autoScalerRoleName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &autoScalerRole, func() error {
		return reconcileAutoScalerRole(&autoScalerRole)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile autoscaler role: %w", err)
	}

	// Reconcile autoscaler service account
	autoScalerServiceAccountName := autoscaler.AutoScalerServiceAccountName(controlPlaneNamespace.Name)
	autoScalerServiceAccount := corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: autoScalerServiceAccountName.Namespace,
			Name:      autoScalerServiceAccountName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &autoScalerServiceAccount, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile autoscaler service account: %w", err)
	}

	// Reconcile autoscaler role binding
	autoScalerRoleBindingName := autoscaler.AutoScalerRoleBindingName(controlPlaneNamespace.Name)
	autoScalerRoleBinding := rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: autoScalerRoleBindingName.Namespace,
			Name:      autoScalerRoleBindingName.Name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &autoScalerRoleBinding, func() error {
		return reconcileAutoScalerRoleBinding(&autoScalerRoleBinding, &autoScalerRole, &autoScalerServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile autoscaler role binding: %w", err)
	}

	// The deployment depends on the kubeconfig being reported.
	if hcp.Status.KubeConfig != nil {
		// Resolve the kubeconfig secret from the hostedcontrolplane which the
		// autoscaler is deployed alongside of.
		hcpKubeConfigSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hcp.Namespace,
				Name:      hcp.Status.KubeConfig.Name,
			},
		}
		err = r.Client.Get(ctx, client.ObjectKeyFromObject(hcpKubeConfigSecret), hcpKubeConfigSecret)
		if err != nil {
			return fmt.Errorf("failed to get hosted controlplane kubeconfig secret %q: %w", hcpKubeConfigSecret.Name, err)
		}

		// Reconcile autoscaler deployment
		autoScalerDeploymentName := autoscaler.AutoScalerDeploymentName(controlPlaneNamespace.Name)
		autoScalerDeployment := appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: autoScalerDeploymentName.Namespace,
				Name:      autoScalerDeploymentName.Name,
			},
		}
		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &autoScalerDeployment, func() error {
			return reconcileAutoScalerDeployment(&autoScalerDeployment, &autoScalerServiceAccount, hcpKubeConfigSecret, "k8s.gcr.io/autoscaling/cluster-autoscaler:v1.20.0")
		})
		if err != nil {
			return fmt.Errorf("failed to reconcile autoscaler deployment: %w", err)
		}
	}

	return nil
}

func reconcileDefaultNodePool(nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster) error {
	// This is a create-only resource, so never update it after it has been created.
	if !nodePool.CreationTimestamp.IsZero() {
		return nil
	}
	nodePool.Spec = hyperv1.NodePoolSpec{
		ClusterName: hcluster.GetName(),
		NodeCount:   k8sutilspointer.Int32Ptr(int32(hcluster.Spec.InitialComputeReplicas)),
	}
	nodePool.Status = hyperv1.NodePoolStatus{}
	if hcluster.Spec.Platform.AWS != nil {
		nodePool.Spec.Platform.AWS = hcluster.Spec.Platform.AWS.NodePoolDefaults
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

	eic.Spec.ComputeReplicas = hcluster.Spec.InitialComputeReplicas

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

func reconcileCAPIAWSProviderDeployment(deployment *appsv1.Deployment, sa *corev1.ServiceAccount, providerCreds *corev1.Secret, image string) error {
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
								SecretName: providerCreds.Name,
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

func reconcileAutoScalerDeployment(deployment *appsv1.Deployment, sa *corev1.ServiceAccount, hcpKubeConfigSecret *corev1.Secret, image string) error {
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
								SecretName: hcpKubeConfigSecret.Name,
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
						Args: []string{
							"--cloud-provider=clusterapi",
							"--node-group-auto-discovery=clusterapi:namespace=$(MY_NAMESPACE)",
							"--kubeconfig=/mnt/kubeconfig/target-kubeconfig",
							"--clusterapi-cloud-config-authoritative",
							"--alsologtostderr",
							"--v=4",
						},
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
func computeClusterVersionStatus(clock Clock, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) *hyperv1.ClusterVersionStatus {
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
	controlPlaneNamespace := manifests.HostedControlPlaneNamespaceName(req.Namespace, req.Name).Name

	nodePools, err := r.listNodePools(req.Namespace, req.Name)
	if err != nil {
		return false, fmt.Errorf("failed to get nodePools by cluster name for cluster %q: %w", req.Name, err)
	}

	for key := range nodePools {
		if err := r.Delete(ctx, &nodePools[key]); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("failed to delete nodePool %q for cluster %q: %w", nodePools[key].GetName(), req.Name, err)
		}
	}

	if len(hc.Spec.InfraID) > 0 {
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
