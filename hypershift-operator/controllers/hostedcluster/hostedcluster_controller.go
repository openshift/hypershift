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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	capiv1 "sigs.k8s.io/cluster-api/api/v1alpha4"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	hyperv1 "openshift.io/hypershift/api/v1alpha1"
)

const (
	finalizer               = "hypershift.openshift.io/finalizer"
	hostedClusterAnnotation = "hypershift.openshift.io/cluster"
	pullSecretName          = "pull-secret"
	sshKeySecretName        = "ssh-key"
	providerCredsSecretName = "provider-creds"
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
			return ctrl.Result{}, fmt.Errorf("failed to get cluster %q: %w", req.NamespacedName, err)
		}
	}

	// Return early if deleted
	if isMissing || !hcluster.DeletionTimestamp.IsZero() {
		if err := r.delete(ctx, req); err != nil {
			r.Log.Error(err, "failed to delete cluster")
			return ctrl.Result{}, err
		}

		if controllerutil.ContainsFinalizer(hcluster, finalizer) {
			controllerutil.RemoveFinalizer(hcluster, finalizer)
			if err := r.Update(ctx, hcluster); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from cluster: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure the cluster has a finalizer for cleanup
	if !controllerutil.ContainsFinalizer(hcluster, finalizer) {
		controllerutil.AddFinalizer(hcluster, finalizer)
		if err := r.Update(ctx, hcluster); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to cluster: %w", err)
		}
	}

	if hcluster.Status.Ready {
		r.Log.Info("Is ready")
		return ctrl.Result{}, nil
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
			return ctrl.Result{}, fmt.Errorf("failed to update version status for hosted cluster: %w", err)
		}
		return ctrl.Result{}, nil
	}

	var infra configv1.Infrastructure
	if err := r.Get(context.Background(), client.ObjectKey{Name: "cluster"}, &infra); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get cluster infra: %w", err)
	}

	targetNamespace := hcluster.GetName()
	r.Log.Info("Creating the target namespace", "namespace", targetNamespace)
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: targetNamespace},
	}
	if err := r.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		return ctrl.Result{}, fmt.Errorf("failed to create target namespace %q: %w", ns.Name, err)
	}

	r.Log.Info("Creating provider creds secret in the target namespace", "namespace", targetNamespace)
	var providerCredsSecret corev1.Secret
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.ProviderCreds.Name}, &providerCredsSecret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get provider creds %s: %w", hcluster.Spec.ProviderCreds.Name, err)
	}
	providerCredsData, hasProviderCredsData := providerCredsSecret.Data["credentials"]
	if !hasProviderCredsData {
		return ctrl.Result{}, fmt.Errorf("provider credentials %s is missing the credentials key", hcluster.Spec.PullSecret.Name)
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
	if err := r.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.PullSecret.Name}, &pullSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get pull secret %s: %w", hcluster.Spec.PullSecret.Name, err)
	}
	pullSecretData, hasPullSecretData := pullSecret.Data[".dockerconfigjson"]
	if !hasPullSecretData {
		return ctrl.Result{}, fmt.Errorf("pull secret %s is missing the .dockerconfigjson key", hcluster.Spec.PullSecret.Name)
	}
	if _, err := createPullSecret(r, targetNamespace, pullSecretData); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create pull secret: %w", err)
	}

	r.Log.Info("Creating ssh key secret in the target namespace", "namespace", targetNamespace)
	var sshKeySecret corev1.Secret
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.Namespace, Name: hcluster.Spec.SSHKey.Name}, &sshKeySecret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get SSH key secret %s: %w", hcluster.Spec.SSHKey.Name, err)
	}
	sshKeyData, hasSSHKeyData := sshKeySecret.Data["id_rsa.pub"]
	if !hasSSHKeyData {
		return ctrl.Result{}, fmt.Errorf("SSH key secret secret %s is missing the id_rsa.pub key", hcluster.Spec.SSHKey.Name)
	}
	targetSSHSecret, err := generateSSHSecret(sshKeyData, targetNamespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to generate ssh secret manifest for target cluster: %w", err)
	}
	if err := r.Create(ctx, targetSSHSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		return ctrl.Result{}, fmt.Errorf("failed to create target ssh secret : %v", err)
	}

	// Install operators
	params := &ClusterParams{
		Namespace:                 targetNamespace,
		ControlPlaneOperatorImage: r.OperatorImage,
	}
	manifests, err := renderControlPlaneManifests(params)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to render hypershift manifests for cluster: %w", err)
	}
	if err := applyManifests(ctx, r, r.Log, targetNamespace, manifests); err != nil {
		return ctrl.Result{}, err
	}

	cluster := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      hcluster.GetName(),
			Annotations: map[string]string{
				hostedClusterAnnotation: namespacedName(hcluster).String(),
			},
		},
		Spec: capiv1.ClusterSpec{
			ControlPlaneEndpoint: capiv1.APIEndpoint{},
			ControlPlaneRef: &corev1.ObjectReference{
				APIVersion: "hypershift.openshift.io/v1alpha1",
				Kind:       "HostedControlPlane",
				Namespace:  targetNamespace,
				Name:       hcluster.GetName(),
			},
			InfrastructureRef: &corev1.ObjectReference{
				APIVersion: "hypershift.openshift.io/v1alpha1",
				Kind:       "ExternalInfraCluster",
				Namespace:  targetNamespace,
				Name:       hcluster.GetName(),
			},
		},
	}

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      hcluster.GetName(),
			Annotations: map[string]string{
				hostedClusterAnnotation: namespacedName(hcluster).String(),
			},
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
			ServiceCIDR:  hcluster.Spec.ServiceCIDR,
			PodCIDR:      hcluster.Spec.PodCIDR,
			ReleaseImage: hcluster.Spec.Release.Image,
		},
	}
	ExternalInfraCluster := &hyperv1.ExternalInfraCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      hcluster.GetName(),
			Annotations: map[string]string{
				hostedClusterAnnotation: namespacedName(hcluster).String(),
			},
		},
		Spec: hyperv1.ExternalInfraClusterSpec{
			ComputeReplicas: hcluster.Spec.InitialComputeReplicas,
			Region:          infra.Status.PlatformStatus.AWS.Region,
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

	if hcp.Status.Version != hcluster.Status.Version.History[0].Version {
		hcluster.Status.Version.History[0].Version = hcp.Status.Version
		if err = r.Status().Update(ctx, hcluster); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update version in hosted cluster status: %w", err)
		}
		return ctrl.Result{}, nil
	}

	var currentCluster capiv1.Cluster
	if err := r.Get(ctx, client.ObjectKeyFromObject(cluster), &currentCluster); err != nil {
		return ctrl.Result{}, err
	}
	r.Log = r.Log.WithValues("cluster", cluster.Name)

	ready := currentCluster.Status.ControlPlaneReady && currentCluster.Status.InfrastructureReady
	if !ready {
		r.Log.Info("Not ready yet.")
		return ctrl.Result{}, nil
	}
	hcluster.Status.Ready = ready
	completionTime := metav1.Now()
	hcluster.Status.Version.History[0].CompletionTime = &completionTime
	hcluster.Status.Version.History[0].State = configv1.CompletedUpdate
	if err := r.Status().Update(ctx, hcluster); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update hosted cluster status: %w", err)
	}

	if hcluster.Spec.InitialComputeReplicas > 0 {
		nodePool := &hyperv1.NodePool{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name:      hcluster.GetName(),
				Namespace: hcluster.GetNamespace(),
			},
			Spec: hyperv1.NodePoolSpec{
				ClusterName: cluster.GetName(),
				NodeCount:   hcluster.Spec.InitialComputeReplicas,
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

func (r *HostedClusterReconciler) delete(ctx context.Context, req ctrl.Request) error {
	targetNamespace := req.Name

	nodePools, err := r.listNodePools(req.Namespace, req.Name)
	if err != nil {
		return fmt.Errorf("failed to get nodePools by cluster name for cluster %q: %w", req.Name, err)
	}

	for key := range nodePools {
		if err := r.Delete(ctx, &nodePools[key]); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete nodePool %q for cluster %q: %w", nodePools[key].GetName(), req.Name, err)
		}
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

func createPullSecret(c client.Client, namespace string, data []byte) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	secret.Namespace = namespace
	secret.Name = pullSecretName
	secret.Data = map[string][]byte{".dockerconfigjson": []byte(data)}
	secret.Type = corev1.SecretTypeDockerConfigJson
	if err := c.Create(context.TODO(), secret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("failed to create pull secret: %w", err)
		}
	}
	return secret, retry.RetryOnConflict(retry.DefaultRetry, func() error {
		sa := &corev1.ServiceAccount{}
		if err := c.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: "default"}, sa); err != nil {
			return err
		}
		sa.ImagePullSecrets = append(sa.ImagePullSecrets, corev1.LocalObjectReference{Name: "pull-secret"})
		if err := c.Update(context.TODO(), sa); err != nil {
			return err
		}
		return nil
	})
}

func generateProviderCredsSecret(data []byte, namespace string) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	secret.Name = providerCredsSecretName
	secret.Namespace = namespace
	secret.Data = map[string][]byte{"credentials": data}
	secret.Type = corev1.SecretTypeOpaque
	return secret, nil
}

func generateSSHSecret(data []byte, namespace string) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	secret.Name = sshKeySecretName
	secret.Namespace = namespace
	secret.Data = map[string][]byte{"id_rsa.pub": data}
	secret.Type = corev1.SecretTypeOpaque
	return secret, nil
}

func namespacedName(obj metav1.Object) types.NamespacedName {
	return types.NamespacedName{
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}
}

func parseNamespacedName(name string) types.NamespacedName {
	parts := strings.SplitN(name, string(types.Separator), 2)
	if len(parts) > 1 {
		return types.NamespacedName{Namespace: parts[0], Name: parts[1]}
	}
	return types.NamespacedName{Name: parts[0]}
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
		{NamespacedName: parseNamespacedName(hostedClusterName)},
	}
}
