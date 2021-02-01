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

package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	hyperv1 "openshift.io/hypershift/api/v1alpha1"
	"openshift.io/hypershift/hypershift-operator/releaseinfo"
	hypershiftcp "openshift.io/hypershift/hypershift-operator/render/controlplane/hypershift"
	capiv1 "sigs.k8s.io/cluster-api/api/v1alpha4"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	finalizer               = "hypershift.openshift.io/finalizer"
	pullSecretName          = "pull-secret"
	sshKeySecretName        = "ssh-key"
	providerCredsSecretName = "provider-creds"
)

// OpenShiftClusterReconciler reconciles a OpenShiftCluster object
type OpenShiftClusterReconciler struct {
	client.Client
	Log             logr.Logger
	ReleaseProvider releaseinfo.Provider
	Infra           *configv1.Infrastructure
}

// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=openshiftclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=openshiftclusters/status,verbs=get;update;patch

func (r *OpenShiftClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Log = ctrl.LoggerFrom(ctx)
	r.Log.Info("Reconciling")

	ocluster := &hyperv1.OpenShiftCluster{}
	isMissing := false
	err := r.Get(ctx, req.NamespacedName, ocluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			isMissing = true
		} else {
			return ctrl.Result{}, fmt.Errorf("failed to get cluster %q: %w", req.NamespacedName, err)
		}
	}

	// Return early if deleted
	if isMissing || !ocluster.DeletionTimestamp.IsZero() {
		if err := r.delete(ctx, req); err != nil {
			r.Log.Error(err, "failed to delete cluster")
			return ctrl.Result{}, err
		}

		if controllerutil.ContainsFinalizer(ocluster, finalizer) {
			controllerutil.RemoveFinalizer(ocluster, finalizer)
			if err := r.Update(ctx, ocluster); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from cluster: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure the cluster has a finalizer for cleanup
	if !controllerutil.ContainsFinalizer(ocluster, finalizer) {
		controllerutil.AddFinalizer(ocluster, finalizer)
		if err := r.Update(ctx, ocluster); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to cluster: %w", err)
		}
	}

	if ocluster.Status.Ready {
		r.Log.Info("Is ready")
		return ctrl.Result{}, nil
	}

	targetNamespace := ocluster.GetName()
	r.Log.Info("Creating the target namespace", "namespace", targetNamespace)
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: targetNamespace},
	}
	if err := r.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		return ctrl.Result{}, fmt.Errorf("failed to create target namespace %q: %w", ns.Name, err)
	}

	r.Log.Info("Creating provider creds secret in the target namespace", "namespace", targetNamespace)
	var providerCredsSecret corev1.Secret
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: ocluster.GetNamespace(), Name: ocluster.Spec.ProviderCreds.Name}, &providerCredsSecret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get provider creds %s: %w", ocluster.Spec.ProviderCreds.Name, err)
	}
	providerCredsData, hasProviderCredsData := providerCredsSecret.Data["credentials"]
	if !hasProviderCredsData {
		return ctrl.Result{}, fmt.Errorf("provider credentials %s is missing the credentials key", ocluster.Spec.PullSecret.Name)
	}
	targetProviderCredsSecret, err := generateProviderCredsSecret(providerCredsData, targetNamespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create providerCreds secret manifest for target cluster: %w", err)
	}
	if err := r.Create(ctx, targetProviderCredsSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		return ctrl.Result{}, fmt.Errorf("failed to generate providerCreds secret: %v", err)
	}

	r.Log.Info("Creating pull secret in the target namespace", "namespace", targetNamespace)
	var pullSecret corev1.Secret
	if err := r.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: ocluster.GetNamespace(), Name: ocluster.Spec.PullSecret.Name}, &pullSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get pull secret %s: %w", ocluster.Spec.PullSecret.Name, err)
	}
	pullSecretData, hasPullSecretData := pullSecret.Data[".dockerconfigjson"]
	if !hasPullSecretData {
		return ctrl.Result{}, fmt.Errorf("pull secret %s is missing the .dockerconfigjson key", ocluster.Spec.PullSecret.Name)
	}
	if _, err := createPullSecret(r, targetNamespace, pullSecretData); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create pull secret: %w", err)
	}

	r.Log.Info("Creating ssh key secret in the target namespace", "namespace", targetNamespace)
	var sshKeySecret corev1.Secret
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: ocluster.Namespace, Name: ocluster.Spec.SSHKey.Name}, &sshKeySecret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get SSH key secret %s: %w", ocluster.Spec.SSHKey.Name, err)
	}
	sshKeyData, hasSSHKeyData := sshKeySecret.Data["id_rsa.pub"]
	if !hasSSHKeyData {
		return ctrl.Result{}, fmt.Errorf("SSH key secret secret %s is missing the id_rsa.pub key", ocluster.Spec.SSHKey.Name)
	}
	targetSSHSecret, err := generateSSHSecret(sshKeyData, targetNamespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to generate ssh secret manifest for target cluster: %w", err)
	}
	if err := r.Create(ctx, targetSSHSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		return ctrl.Result{}, fmt.Errorf("failed to create target ssh secret : %v", err)
	}

	// run CAPI controllers
	params := hypershiftcp.NewClusterParams()
	params.Namespace = targetNamespace
	releaseImage, err := r.ReleaseProvider.Lookup(ctx, ocluster.Spec.Release.Image)
	manifests, err := hypershiftcp.RenderCAPIManifests(params, releaseImage, nil, nil)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to render hypershift manifests for cluster: %w", err)
	}
	if err := applyManifests(ctx, r, r.Log, targetNamespace, manifests); err != nil {
		return ctrl.Result{}, err
	}

	cluster := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      ocluster.GetName(),
		},
		Spec: capiv1.ClusterSpec{
			ControlPlaneEndpoint: capiv1.APIEndpoint{},
			ControlPlaneRef: &corev1.ObjectReference{
				APIVersion: "hypershift.openshift.io/v1alpha1",
				Kind:       "HostedControlPlane",
				Namespace:  targetNamespace,
				Name:       ocluster.GetName(),
			},
			InfrastructureRef: &corev1.ObjectReference{
				APIVersion: "hypershift.openshift.io/v1alpha1",
				Kind:       "ExternalInfraCluster",
				Namespace:  targetNamespace,
				Name:       ocluster.GetName(),
			},
		},
	}
	patchHelper, err := patch.NewHelper(ocluster, r.Client)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to init patch helper: %w", err)
	}

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      ocluster.GetName(),
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			ProviderCreds: corev1.LocalObjectReference{
				Name: providerCredsSecretName,
			},
			PullSecret: corev1.LocalObjectReference{
				Name: pullSecretName,
			},
			SSHKey: corev1.LocalObjectReference{
				Name: sshKeySecretName,
			},
			ServiceCIDR:  ocluster.Spec.ServiceCIDR,
			PodCIDR:      ocluster.Spec.PodCIDR,
			ReleaseImage: ocluster.Spec.Release.Image,
		},
	}
	ExternalInfraCluster := &hyperv1.ExternalInfraCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      ocluster.GetName(),
		},
		Spec: hyperv1.ExternalInfraClusterSpec{
			ComputeReplicas: ocluster.Spec.InitialComputeReplicas,
			Region:          r.Infra.Status.PlatformStatus.AWS.Region,
		},
	}

	if _, err := ctrl.CreateOrUpdate(ctx, r.Client, cluster, func() error { return nil }); err != nil {
		return ctrl.Result{}, err
	}

	if _, err := ctrl.CreateOrUpdate(ctx, r.Client, hcp, func() error { return nil }); err != nil {
		return ctrl.Result{}, err
	}

	if _, err := ctrl.CreateOrUpdate(ctx, r.Client, ExternalInfraCluster, func() error { return nil }); err != nil {
		return ctrl.Result{}, err
	}

	var currentCluster capiv1.Cluster
	if err := r.Get(ctx, client.ObjectKeyFromObject(cluster), &currentCluster); err != nil {
		return ctrl.Result{}, err
	}
	r.Log = r.Log.WithValues("cluster", cluster.Name)

	ready := currentCluster.Status.ControlPlaneReady && currentCluster.Status.InfrastructureReady
	if !ready {
		r.Log.Info("Not ready yet. Requeueing")
		return ctrl.Result{Requeue: true}, nil
	}
	ocluster.Status.Ready = ready
	if err := patchHelper.Patch(ctx, ocluster); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to patch: %w", err)
	}

	if ocluster.Spec.InitialComputeReplicas > 0 {
		nodePool := &hyperv1.NodePool{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name:      ocluster.GetName(),
				Namespace: ocluster.GetNamespace(),
			},
			Spec: hyperv1.NodePoolSpec{
				ClusterName: cluster.GetName(),
				NodeCount:   ocluster.Spec.InitialComputeReplicas,
				Platform: hyperv1.NodePoolPlatform{
					AWS: &hyperv1.AWSNodePoolPlatform{
						InstanceType: "m5.large",
					},
				},
			},
			Status: hyperv1.NodePoolStatus{},
		}
		if err := r.Create(ctx, nodePool); err != nil && !apierrors.IsAlreadyExists(err) {
			return reconcile.Result{}, fmt.Errorf("failed to create nodepool: %w", err)
		}
	}

	r.Log.Info("Successfully reconciled")
	return ctrl.Result{}, nil
}

func (r *OpenShiftClusterReconciler) delete(ctx context.Context, req ctrl.Request) error {
	targetNamespace := req.Name

	r.Log.Info("Deleting default nodePool", "name", req.Name)
	defaultNodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
		},
	}

	if err := r.Delete(ctx, defaultNodePool); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete defaultNodePool: %w", err)
	}

	r.Log.Info("Deleting cluster", "name", req.Name, "namespace", targetNamespace)
	cluster := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: targetNamespace,
		},
	}

	if err := waitForDeletion(ctx, r.Log, r, cluster); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete cluster: %w", err)
	}

	r.Log.Info("Deleting target namespace", "namespace", targetNamespace)
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: targetNamespace},
	}
	if err := r.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete namespace: %w", err)
	}
	return nil
}

func (r *OpenShiftClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	var infra configv1.Infrastructure
	if err := mgr.GetAPIReader().Get(context.Background(), client.ObjectKey{Name: "cluster"}, &infra); err != nil {
		return fmt.Errorf("failed to get cluster infra: %w", err)
	}
	r.Infra = &infra

	return ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.OpenShiftCluster{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
		}).
		Complete(r)
}

func waitForDeletion(ctx context.Context, log logr.Logger, c client.Client, obj client.Object) error {
	log.WithValues("name", obj.GetName(),
		"namespace", obj.GetNamespace(), "kind", obj.GetObjectKind())
	log.Info("Deleting")

	if err := c.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	if err := wait.PollInfinite(5*time.Second, func() (done bool, err error) {
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			log.Error(err, "error getting")
		}
		log.Info("still exists")
		return false, nil
	}); err != nil {
		return err
	}
	return nil
}
