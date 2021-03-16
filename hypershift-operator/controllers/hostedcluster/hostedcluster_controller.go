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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
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
	controlPlaneProviderCredsSecret := manifests.ProviderCredentials{Namespace: controlPlaneNamespace}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, controlPlaneProviderCredsSecret, func() error {
		hostedClusterProviderCredsData, hasProviderCredsData := hostedClusterProviderCredsSecret.Data["credentials"]
		if !hasProviderCredsData {
			return fmt.Errorf("hostecsluter provider credentials secret %q must have a credentials key", hostedClusterProviderCredsSecret.Name)
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
	controlPlanePullSecret := manifests.PullSecret{Namespace: controlPlaneNamespace}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, controlPlanePullSecret, func() error {
		hostedClusterPullSecretData, hasPullSecretData := hostedClusterPullSecret.Data[".dockerconfigjson"]
		if !hasPullSecretData {
			return fmt.Errorf("hostedcluster pull secret %q must have a .dockerconfigjson key", hostedClusterPullSecret.Name)
		}
		controlPlanePullSecret.Data[".dockerconfigjson"] = hostedClusterPullSecretData
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
		controlPlaneSSHKeySecret = manifests.SSHKey{Namespace: controlPlaneNamespace}.Build()
		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, controlPlaneSSHKeySecret, func() error {
			hostedClusterSSHKeyData, hasSSHKeyData := hostedClusterSSHKeySecret.Data["id_rsa.pub"]
			if !hasSSHKeyData {
				return fmt.Errorf("hostedcluster ssh key secret %q must have a id_rsa.pub key", hostedClusterSSHKeySecret.Name)
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
		nodePool := manifests.DefaultNodePool{
			HostedCluster: hcluster,
		}.Build()
		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, nodePool, NoopReconcile)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile initial node pool: %w", err)
		}
	}

	// Reconcile the CAPI Cluster resource
	capiCluster := controlplaneoperator.CAPICluster{
		Namespace:     controlPlaneNamespace,
		HostedCluster: hcluster,
	}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiCluster, NoopReconcile)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile capi cluster: %w", err)
	}

	// Reconcile the CAPI ExternalInfraCluster
	externalInfraCluster := controlplaneoperator.ExternalInfraCluster{
		Namespace:     controlPlaneNamespace,
		HostedCluster: hcluster,
	}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, externalInfraCluster, NoopReconcile)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile externalinfracluster: %w", err)
	}

	// Reconcile the HostedControlPlane
	hcp := controlplaneoperator.HostedControlPlane{
		Namespace:           controlPlaneNamespace,
		HostedCluster:       hcluster,
		ProviderCredentials: controlPlaneProviderCredsSecret,
		PullSecret:          controlPlanePullSecret,
		SSHKey:              controlPlaneSSHKeySecret,
	}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, hcp, func() error {
		return reconcileHostedControlPlane(hcp, hcluster)
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile hostedcontrolplane: %w", err)
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
		hostedClusterKubeConfigSecret := manifests.KubeConfigSecret{HostedCluster: hcluster}.Build()
		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, hostedClusterKubeConfigSecret, func() error {
			key := hcp.Status.KubeConfig.Key
			controlPlaneKubeConfigData, ok := controlPlaneKubeConfigSecret.Data[key]
			if !ok {
				return fmt.Errorf("controlplane kubeconfig secret %q must have a %q key", client.ObjectKeyFromObject(controlPlaneKubeConfigSecret), key)
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

	rolloutComplete := hcluster.Status.Version != nil &&
		hcluster.Status.Version.History != nil &&
		hcluster.Status.Version.History[0].State == configv1.CompletedUpdate

	// Defer rolling out new control planes until existing rollouts have reached
	// a terminal state.
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

	// Reconcile CAPI manager cluster role
	capiManagerClusterRole := clusterapi.ManagerClusterRole{}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiManagerClusterRole, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager cluster role: %w", err)
	}

	// Reconcile CAPI manager service account
	capiManagerServiceAccount := clusterapi.ManagerServiceAccount{Namespace: controlPlaneNamespace}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiManagerServiceAccount, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager service account: %w", err)
	}

	// Reconcile CAPI manager cluster role binding
	capiManagerClusterRoleBinding := clusterapi.ManagerClusterRoleBinding{
		ClusterRole:    capiManagerClusterRole,
		ServiceAccount: capiManagerServiceAccount,
	}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiManagerClusterRoleBinding, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager cluster role binding: %w", err)
	}

	// Reconcile CAPI manager role
	capiManagerRole := clusterapi.ManagerRole{Namespace: controlPlaneNamespace}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiManagerRole, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager role: %w", err)
	}

	// Reconcile CAPI manager role binding
	capiManagerRoleBinding := clusterapi.ManagerRoleBinding{
		Role:           capiManagerRole,
		ServiceAccount: capiManagerServiceAccount,
	}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiManagerRoleBinding, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager role: %w", err)
	}

	// Reconcile CAPI manager deployment
	capiManagerDeployment := clusterapi.ManagerDeployment{
		Namespace:      controlPlaneNamespace,
		Image:          "quay.io/hypershift/cluster-api:hypershift",
		ServiceAccount: capiManagerServiceAccount,
	}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiManagerDeployment, NoopReconcile)
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
	err = r.Client.Get(ctx, manifests.ProviderCredentialsName(controlPlaneNamespace.Name), providerCredentialsSecret)
	if err != nil {
		return fmt.Errorf("failed to get provider credentials secret: %w", err)
	}

	// Reconcile CAPI AWS provider role
	capiAwsProviderRole := clusterapi.AWSProviderRole{Namespace: controlPlaneNamespace}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiAwsProviderRole, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile capi aws provider role: %w", err)
	}

	// Reconcile CAPI AWS provider service account
	capiAwsProviderServiceAccount := clusterapi.AWSProviderServiceAccount{Namespace: controlPlaneNamespace}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiAwsProviderServiceAccount, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile capi aws provider service account: %w", err)
	}

	// Reconcile CAPI AWS provider role binding
	capiAwsProviderRoleBinding := clusterapi.AWSProviderRoleBinding{
		Role:           capiAwsProviderRole,
		ServiceAccount: capiAwsProviderServiceAccount,
	}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiAwsProviderRoleBinding, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile capi aws provider role binding: %w", err)
	}

	// Reconcile CAPI AWS provider deployment
	capiAwsProviderDeployment := clusterapi.AWSProviderDeployment{
		Namespace:           controlPlaneNamespace,
		Image:               "quay.io/hypershift/cluster-api-provider-aws:master",
		ServiceAccount:      capiAwsProviderServiceAccount,
		ProviderCredentials: providerCredentialsSecret,
	}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, capiAwsProviderDeployment, NoopReconcile)
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
	controlPlaneOperatorServiceAccount := controlplaneoperator.OperatorServiceAccount{
		Namespace: controlPlaneNamespace,
	}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, controlPlaneOperatorServiceAccount, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator service account: %w", err)
	}

	// Reconcile operator cluster role
	controlPlaneOperatorClusterRole := controlplaneoperator.OperatorClusterRole{}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, controlPlaneOperatorClusterRole, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator cluster role: %w", err)
	}

	// Reconcile operator cluster role binding
	controlPlaneOperatorClusterRoleBinding := controlplaneoperator.OperatorClusterRoleBinding{
		ClusterRole:    controlPlaneOperatorClusterRole,
		ServiceAccount: controlPlaneOperatorServiceAccount,
	}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, controlPlaneOperatorClusterRoleBinding, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator clusterrolebinding: %w", err)
	}

	// Reconcile operator role
	controlPlaneOperatorRole := controlplaneoperator.OperatorRole{
		Namespace: controlPlaneNamespace,
	}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, controlPlaneOperatorRole, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator clusterrole: %w", err)
	}

	// Reconcile operator role binding
	controlPlaneOperatorRoleBinding := controlplaneoperator.OperatorRoleBinding{
		Role:           controlPlaneOperatorRole,
		ServiceAccount: controlPlaneOperatorServiceAccount,
	}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, controlPlaneOperatorRoleBinding, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator rolebinding: %w", err)
	}

	// Reconcile operator deployment
	controlPlaneOperatorDeployment := controlplaneoperator.OperatorDeployment{
		Namespace:      controlPlaneNamespace,
		OperatorImage:  r.OperatorImage,
		ServiceAccount: controlPlaneOperatorServiceAccount,
	}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, controlPlaneOperatorDeployment, NoopReconcile)
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
	autoScalerRole := autoscaler.Role{Namespace: controlPlaneNamespace}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, autoScalerRole, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile autoscaler role: %w", err)
	}

	// Reconcile autoscaler service account
	autoScalerServiceAccount := autoscaler.ServiceAccount{Namespace: controlPlaneNamespace}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, autoScalerServiceAccount, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile autoscaler service account: %w", err)
	}

	// Reconcile autoscaler role binding
	autoScalerRoleBinding := autoscaler.RoleBinding{
		Role:           autoScalerRole,
		ServiceAccount: autoScalerServiceAccount,
	}.Build()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, autoScalerRoleBinding, NoopReconcile)
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
		autoScalerDeployment := autoscaler.Deployment{
			Namespace:              controlPlaneNamespace,
			ServiceAccount:         autoScalerServiceAccount,
			Image:                  "k8s.gcr.io/autoscaling/cluster-autoscaler:v1.20.0",
			ControlPlaneKubeConfig: hcpKubeConfigSecret,
		}.Build()
		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, autoScalerDeployment, NoopReconcile)
		if err != nil {
			return fmt.Errorf("failed to reconcile autoscaler deployment: %w", err)
		}
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
