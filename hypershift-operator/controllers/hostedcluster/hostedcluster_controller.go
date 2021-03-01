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
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	hyperapi "github.com/openshift/hypershift/api"
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

// HostedClusterReconciler reconciles a HostedCluster object
type HostedClusterReconciler struct {
	client.Client

	Log           logr.Logger
	OperatorImage string
}

// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedclusters/status,verbs=get;update;patch

func (r *HostedClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedCluster{}).
		Watches(&source.Kind{Type: &hyperv1.ExternalInfraCluster{}}, handler.EnqueueRequestsFromMapFunc(enqueueParentHostedCluster)).
		Watches(&source.Kind{Type: &hyperv1.HostedControlPlane{}}, handler.EnqueueRequestsFromMapFunc(enqueueParentHostedCluster)).
		Watches(&source.Kind{Type: &capiv1.Cluster{}}, handler.EnqueueRequestsFromMapFunc(enqueueParentHostedCluster)).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
		}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}

func (r *HostedClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Log = ctrl.LoggerFrom(ctx)
	r.Log.Info("Reconciling")

	hcluster := &hyperv1.HostedCluster{}
	isMissing := false
	err := r.Get(ctx, req.NamespacedName, hcluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			isMissing = true
		} else {
			r.Log.Error(err, "failed to get cluster", "cluster", req.NamespacedName)
			return ctrl.Result{}, fmt.Errorf("failed to get cluster %q: %w", req.NamespacedName, err)
		}
	}

	// Return early if deleted
	if isMissing || !hcluster.DeletionTimestamp.IsZero() {
		completed, err := r.delete(ctx, req)
		if err != nil {
			r.Log.Error(err, "failed to delete cluster", "cluster", req.NamespacedName)
			return ctrl.Result{}, err
		}
		if !completed {
			return ctrl.Result{RequeueAfter: clusterDeletionRequeueDuration}, nil
		}

		if controllerutil.ContainsFinalizer(hcluster, finalizer) {
			controllerutil.RemoveFinalizer(hcluster, finalizer)
			if err := r.Update(ctx, hcluster); err != nil {
				r.Log.Error(err, "failed to remove finalizer from cluster", "cluster", req.NamespacedName)
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from cluster: %w", err)
			}
		}
		r.Log.Info("hostedcluster not found, skipping reconcile", "name", req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// Ensure the cluster has a finalizer for cleanup
	if !controllerutil.ContainsFinalizer(hcluster, finalizer) {
		controllerutil.AddFinalizer(hcluster, finalizer)
		if err := r.Update(ctx, hcluster); err != nil {
			r.Log.Error(err, "failed to add finalizer from cluster", "cluster", req.NamespacedName)
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to cluster: %w", err)
		}
	}

	if hcluster.Status.Version == nil {
		hcluster.Status.Version = &hyperv1.ClusterVersionStatus{
			Desired:            hcluster.Spec.Release,
			ObservedGeneration: hcluster.Generation,
			History: []configv1.UpdateHistory{
				{
					State:       configv1.PartialUpdate,
					StartedTime: metav1.Now(),
					Image:       hcluster.Spec.Release.Image,
				},
			},
		}
		if err = r.Status().Update(ctx, hcluster); err != nil {
			r.Log.Error(err, "failed to update version status for hosted cluster", "cluster", req.NamespacedName)
			return ctrl.Result{}, fmt.Errorf("failed to update version status for hosted cluster: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// First, create the hosted cluster namespace itself on which all else depends

	targetNamespace := manifests.HostedControlPlaneNamespace{HostedCluster: hcluster}.Build()
	if err := r.Create(ctx, targetNamespace); err != nil && !apierrors.IsAlreadyExists(err) {
		r.Log.Error(err, "failed to create target namespace", "namespace", targetNamespace.Name)
		return ctrl.Result{}, fmt.Errorf("failed to create target namespace %q: %w", targetNamespace.Name, err)
	}
	r.Log.Info("Created target namespace", "namespace", targetNamespace)

	// Create the shared provider credentials secret

	var providerCredsSecret corev1.Secret
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.ProviderCreds.Name}, &providerCredsSecret)
	if err != nil {
		r.Log.Error(err, "failed to get provider creds", "name", hcluster.Spec.ProviderCreds.Name)
		return ctrl.Result{}, fmt.Errorf("failed to get provider creds %s: %w", hcluster.Spec.ProviderCreds.Name, err)
	}
	providerCredsData, hasProviderCredsData := providerCredsSecret.Data["credentials"]
	if !hasProviderCredsData {
		r.Log.Error(err, "provider credentials is missing the credentials key", "name", providerCredsSecret.Name)
		return ctrl.Result{}, fmt.Errorf("provider credentials %s is missing the credentials key", providerCredsSecret.Name)
	}
	targetProviderCredsSecret := manifests.ProviderCredentials{
		Namespace: targetNamespace,
		Data:      providerCredsData,
	}.Build()
	if err := r.Create(ctx, targetProviderCredsSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		r.Log.Error(err, "failed to generate providerCreds secret")
		return ctrl.Result{}, fmt.Errorf("failed to generate providerCreds secret: %v", err)
	}
	r.Log.Info("Created provider creds secret in the target namespace", "namespace", targetNamespace)

	// Next, roll out the CAPI machinery

	capiManagerClusterRole := clusterapi.ManagerClusterRole{}.Build()
	capiManagerServiceAccount := clusterapi.ManagerServiceAccount{Namespace: targetNamespace}.Build()
	capiManagerClusterRoleBinding := clusterapi.ManagerClusterRoleBinding{
		ClusterRole:    capiManagerClusterRole,
		ServiceAccount: capiManagerServiceAccount,
	}.Build()
	capiManagerRole := clusterapi.ManagerRole{Namespace: targetNamespace}.Build()
	capiManagerRoleBinding := clusterapi.ManagerRoleBinding{
		Role:           capiManagerRole,
		ServiceAccount: capiManagerServiceAccount,
	}.Build()
	capiManagerDeployment := clusterapi.ManagerDeployment{
		Namespace:      targetNamespace,
		Image:          "quay.io/hypershift/cluster-api:hypershift",
		ServiceAccount: capiManagerServiceAccount,
	}.Build()
	capiAwsProviderRole := clusterapi.AWSProviderRole{Namespace: targetNamespace}.Build()
	capiAwsProviderServiceAccount := clusterapi.AWSProviderServiceAccount{Namespace: targetNamespace}.Build()
	capiAwsProviderRoleBinding := clusterapi.AWSProviderRoleBinding{
		Role:           capiAwsProviderRole,
		ServiceAccount: capiAwsProviderServiceAccount,
	}.Build()
	capiAwsProviderDeployment := clusterapi.AWSProviderDeployment{
		Namespace:           targetNamespace,
		Image:               "quay.io/hypershift/cluster-api-provider-aws:master",
		ServiceAccount:      capiAwsProviderServiceAccount,
		ProviderCredentials: targetProviderCredsSecret,
	}.Build()
	capiManagerObjects := []ctrlclient.Object{
		capiManagerClusterRole,
		capiManagerServiceAccount,
		capiManagerClusterRoleBinding,
		capiManagerRole,
		capiManagerRoleBinding,
		capiManagerDeployment,
		capiAwsProviderRole,
		capiAwsProviderServiceAccount,
		capiAwsProviderRoleBinding,
		capiAwsProviderDeployment,
	}

	err = r.applyObjects(ctx, capiManagerObjects...)
	if err != nil {
		r.Log.Error(err, "failed to apply cluster api resources")
		return ctrl.Result{}, err
	}
	r.Log.Info("Created all cluster api resources")

	// Next, role out the control plane itself

	var pullSecret corev1.Secret
	if err := r.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.PullSecret.Name}, &pullSecret); err != nil {
		r.Log.Error(err, "failed to get pull secret", "name", hcluster.Spec.PullSecret.Name)
		return ctrl.Result{}, fmt.Errorf("failed to get pull secret %s: %w", hcluster.Spec.PullSecret.Name, err)
	}
	pullSecretData, hasPullSecretData := pullSecret.Data[".dockerconfigjson"]
	if !hasPullSecretData {
		r.Log.Error(err, "pull secret is missing the .dockerconfigjson key", "name", hcluster.Spec.PullSecret.Name)
		return ctrl.Result{}, fmt.Errorf("pull secret %s is missing the .dockerconfigjson key", hcluster.Spec.PullSecret.Name)
	}
	targetPullSecret := manifests.PullSecret{
		Namespace: targetNamespace,
		Data:      pullSecretData,
	}.Build()
	if err := r.Create(ctx, targetPullSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		r.Log.Error(err, "failed to create target pull secret secret", "name", targetPullSecret.Name)
		return ctrl.Result{}, fmt.Errorf("failed to create target pull secret secret: %v", err)
	}
	r.Log.Info("Created pull secret in the target namespace", "namespace", targetNamespace)

	var sshKeySecret corev1.Secret
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.Namespace, Name: hcluster.Spec.SSHKey.Name}, &sshKeySecret)
	if err != nil {
		r.Log.Error(err, "failed to get SSH key secret", "name", hcluster.Spec.SSHKey.Name)
		return ctrl.Result{}, fmt.Errorf("failed to get SSH key secret %s: %w", hcluster.Spec.SSHKey.Name, err)
	}
	sshKeyData, hasSSHKeyData := sshKeySecret.Data["id_rsa.pub"]
	if !hasSSHKeyData {
		r.Log.Error(err, "SSH key secret secret is missing the id_rsa.pub key", "name", hcluster.Spec.SSHKey.Name)
		return ctrl.Result{}, fmt.Errorf("SSH key secret secret %s is missing the id_rsa.pub key", hcluster.Spec.SSHKey.Name)
	}
	targetSSHSecret := manifests.SSHKey{
		Namespace: targetNamespace,
		Data:      sshKeyData,
	}.Build()
	if err := r.Create(ctx, targetSSHSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		r.Log.Error(err, "failed to create target ssh secret", "name", targetSSHSecret.Name)
		return ctrl.Result{}, fmt.Errorf("failed to create target ssh secret: %v", err)
	}
	r.Log.Info("Created ssh key secret in the target namespace", "namespace", targetNamespace)

	var infra configv1.Infrastructure
	if err := r.Get(context.Background(), client.ObjectKey{Name: "cluster"}, &infra); err != nil {
		r.Log.Error(err, "failed to get cluster infra")
		return ctrl.Result{}, fmt.Errorf("failed to get cluster infra: %w", err)
	}

	controlPlaneOperatorServiceAccount := controlplaneoperator.OperatorServiceAccount{Namespace: targetNamespace}.Build()
	controlPlaneOperatorClusterRole := controlplaneoperator.OperatorClusterRole{}.Build()
	controlPlaneOperatorClusterRoleBinding := controlplaneoperator.OperatorClusterRoleBinding{
		ClusterRole:    controlPlaneOperatorClusterRole,
		ServiceAccount: controlPlaneOperatorServiceAccount,
	}.Build()
	controlPlaneOperatorRole := controlplaneoperator.OperatorRole{Namespace: targetNamespace}.Build()
	controlPlaneOperatorRoleBinding := controlplaneoperator.OperatorRoleBinding{
		Role:           controlPlaneOperatorRole,
		ServiceAccount: controlPlaneOperatorServiceAccount,
	}.Build()
	controlPlaneOperatorDeployment := controlplaneoperator.OperatorDeployment{
		Namespace:      targetNamespace,
		OperatorImage:  r.OperatorImage,
		ServiceAccount: controlPlaneOperatorServiceAccount,
	}.Build()

	controlPlaneObjects := []ctrlclient.Object{
		controlPlaneOperatorServiceAccount,
		controlPlaneOperatorClusterRole,
		controlPlaneOperatorClusterRoleBinding,
		controlPlaneOperatorRole,
		controlPlaneOperatorRoleBinding,
		controlPlaneOperatorDeployment,
	}

	err = r.applyObjects(ctx, controlPlaneObjects...)
	if err != nil {
		r.Log.Error(err, "failed to apply control plane resources")
		return ctrl.Result{}, err
	}
	r.Log.Info("Created all control plane resources")

	// Now create default resources that this controller doesn't reconcile

	capiCluster := controlplaneoperator.CAPICluster{
		Namespace:     targetNamespace,
		HostedCluster: hcluster,
	}.Build()
	// TODO: This will need actively reconciled
	hcp := controlplaneoperator.HostedControlPlane{
		Namespace:           targetNamespace,
		HostedCluster:       hcluster,
		ProviderCredentials: targetProviderCredsSecret,
		PullSecret:          targetPullSecret,
		SSHKey:              targetSSHSecret,
	}.Build()
	eic := controlplaneoperator.ExternalInfraCluster{
		Namespace:     targetNamespace,
		HostedCluster: hcluster,
		InfraConfig:   &infra,
	}.Build()
	createOnlyObjects := []ctrlclient.Object{
		capiCluster,
		hcp,
		eic,
	}
	if hcluster.Spec.InitialComputeReplicas > 0 {
		nodePool := manifests.DefaultNodePool{
			HostedCluster: hcluster,
		}.Build()
		createOnlyObjects = append(createOnlyObjects, nodePool)
	}
	for _, object := range createOnlyObjects {
		name := types.NamespacedName{Namespace: object.GetNamespace(), Name: object.GetName()}
		err := r.Client.Get(ctx, name, object)
		if err == nil {
			// already exists, skip it
			continue
		}
		if !apierrors.IsNotFound(err) {
			r.Log.Error(err, "failed to get object", "name", name)
			return ctrl.Result{}, fmt.Errorf("failed to get object %s: %w", name, err)
		}
		err = r.Client.Create(ctx, object)
		if err != nil {
			r.Log.Error(err, "failed to create object", "name", name)
			return ctrl.Result{}, fmt.Errorf("failed to create object %s: %w", name, err)
		}
	}

	if hcp.Status.Version != hcluster.Status.Version.History[0].Version {
		hcluster.Status.Version.History[0].Version = hcp.Status.Version
		if err = r.Status().Update(ctx, hcluster); err != nil {
			r.Log.Error(err, "failed to update version in hosted cluster status")
			return ctrl.Result{}, fmt.Errorf("failed to update version in hosted cluster status: %w", err)
		}
		r.Log.Info("updated hostedcluster version, requeueing")
		return ctrl.Result{Requeue: true}, nil
	}

	// When the hosted control plane kubeconfig secret is available, copy it to the
	// hostedcluster namespace and update status
	if hcp.Status.KubeConfig != nil {
		var targetKubeConfigSecret corev1.Secret
		targetKubeConfigSecretName := types.NamespacedName{Namespace: targetNamespace.Name, Name: hcp.Status.KubeConfig.Name}
		err := r.Client.Get(ctx, targetKubeConfigSecretName, &targetKubeConfigSecret)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get target kubeconfig secret %q: %w", targetKubeConfigSecretName, err)
		}
		// Build a kubeconfig secret scoped to the hostedcluster's namespace
		// which has the same contents as the target secret.
		// TODO: Leaky abstraction, publish this key through HCP status?
		targetSecretData, ok := targetKubeConfigSecret.Data["value"]
		if !ok {
			return ctrl.Result{}, fmt.Errorf("target kubeconfig secret %q is missing key %q", targetKubeConfigSecretName, "value")
		}
		kubeConfigSecret := manifests.KubeConfigSecret{
			HostedCluster: hcluster,
			Data:          targetSecretData,
		}.Build()
		// Update the hostedcluster's copy of the secret.
		err = r.applyObjects(ctx, kubeConfigSecret)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to apply kubeconfig secret: %w", err)
		}
		// Ensure the hostedcluster has a reference to the secret.
		updatedReference := &corev1.LocalObjectReference{Name: kubeConfigSecret.Name}
		if !equality.Semantic.DeepEqual(hcluster.Status.KubeConfig, updatedReference) {
			hcluster.Status.KubeConfig = updatedReference
			if err = r.Status().Update(ctx, hcluster); err != nil {
				r.Log.Error(err, "failed to update version in hosted cluster status")
				return ctrl.Result{}, fmt.Errorf("failed to update version in hosted cluster status: %w", err)
			}
			r.Log.Info("updated hostedcluster version, requeueing")
			return ctrl.Result{Requeue: true}, nil
		}
	}

	// Roll out an auto scaler once the kubeconfig is available
	if hcp.Status.KubeConfig != nil {
		var targetKubeConfigSecret corev1.Secret
		targetKubeConfigSecretName := types.NamespacedName{Namespace: targetNamespace.Name, Name: hcp.Status.KubeConfig.Name}
		err := r.Client.Get(ctx, targetKubeConfigSecretName, &targetKubeConfigSecret)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get target kubeconfig secret %q: %w", targetKubeConfigSecretName, err)
		}
		autoScalerRole := autoscaler.Role{Namespace: targetNamespace}.Build()
		autoScalerServiceAccount := autoscaler.ServiceAccount{Namespace: targetNamespace}.Build()
		autoScalerRoleBinding := autoscaler.RoleBinding{
			Role:           autoScalerRole,
			ServiceAccount: autoScalerServiceAccount,
		}.Build()
		autoScalerDeployment := autoscaler.Deployment{
			Namespace:        targetNamespace,
			ServiceAccount:   autoScalerServiceAccount,
			Image:            "k8s.gcr.io/autoscaling/cluster-autoscaler:v1.20.0",
			TargetKubeConfig: &targetKubeConfigSecret,
		}.Build()
		autoScalerObjects := []ctrlclient.Object{
			autoScalerRole,
			autoScalerServiceAccount,
			autoScalerRoleBinding,
			autoScalerDeployment,
		}
		err = r.applyObjects(ctx, autoScalerObjects...)
		if err != nil {
			r.Log.Error(err, "failed to apply auto scaler resources")
			return ctrl.Result{}, err
		}
		r.Log.Info("created all autoscaler resources")
	} else {
		// TODO: status?
		r.Log.Info("autoscaler rollout pending kubeconfig availability")
	}

	// Check for readiness and update status
	var currentCluster capiv1.Cluster
	if err := r.Get(ctx, client.ObjectKeyFromObject(capiCluster), &currentCluster); err != nil {
		r.Log.Error(err, "couldn't get CAPI cluster resource", "capiCluster", client.ObjectKeyFromObject(capiCluster))
		return ctrl.Result{}, err
	}
	if !hcluster.Status.Ready {
		r.Log = r.Log.WithValues("cluster", capiCluster.Name)
		ready := currentCluster.Status.ControlPlaneReady && currentCluster.Status.InfrastructureReady
		if !ready {
			r.Log.Info("Not ready yet.")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		hcluster.Status.Ready = ready
		completionTime := metav1.Now()
		hcluster.Status.Version.History[0].CompletionTime = &completionTime
		hcluster.Status.Version.History[0].State = configv1.CompletedUpdate
		if err := r.Status().Update(ctx, hcluster); err != nil {
			r.Log.Error(err, "failed to update hosted cluster status")
			return ctrl.Result{}, fmt.Errorf("failed to update hosted cluster status: %w", err)
		}
	}

	r.Log.Info("Successfully reconciled")
	return ctrl.Result{}, nil
}

func (r *HostedClusterReconciler) applyObjects(ctx context.Context, objects ...ctrlclient.Object) error {
	for i := range objects {
		object := objects[i]
		var objectBytes bytes.Buffer
		err := hyperapi.YamlSerializer.Encode(object, &objectBytes)
		if err != nil {
			return fmt.Errorf("failed to encode object %s %s/%s: %w", object.GetObjectKind().GroupVersionKind().Kind, object.GetNamespace(), object.GetName(), err)
		}
		err = r.Client.Patch(ctx, object, ctrlclient.RawPatch(types.ApplyPatchType, objectBytes.Bytes()), ctrlclient.ForceOwnership, ctrlclient.FieldOwner("hypershift"))
		if err != nil {
			return fmt.Errorf("failed to patch object %s %s/%s: %w", object.GetObjectKind().GroupVersionKind().Kind, object.GetNamespace(), object.GetName(), err)
		}
		r.Log.Info("applied resource", "kind", object.GetObjectKind().GroupVersionKind().Kind, "namespace", object.GetNamespace(), "name", object.GetName())
	}
	return nil
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

func (r *HostedClusterReconciler) delete(ctx context.Context, req ctrl.Request) (bool, error) {
	targetNamespace := req.Name

	nodePools, err := r.listNodePools(req.Namespace, req.Name)
	if err != nil {
		return false, fmt.Errorf("failed to get nodePools by cluster name for cluster %q: %w", req.Name, err)
	}

	for key := range nodePools {
		if err := r.Delete(ctx, &nodePools[key]); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("failed to delete nodePool %q for cluster %q: %w", nodePools[key].GetName(), req.Name, err)
		}
	}

	r.Log.Info("Deleting Cluster", "clusterName", req.Name, "clusterNamespace", targetNamespace)
	cluster := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: targetNamespace,
		},
	}

	if err := r.Delete(ctx, cluster); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("error deleting Cluster: %w", err)
		}
		// The advancing case is when Delete() returns an error that the cluster is not found
	} else {
		r.Log.Info("Waiting for Cluster deletion", "clusterName", req.Name, "clusterNamespace", targetNamespace)
		return false, nil
	}

	r.Log.Info("Deleting target namespace", "namespace", targetNamespace)
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: targetNamespace},
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
