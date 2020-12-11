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

	configv1 "github.com/openshift/api/config/v1"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	hyperv1 "openshift.io/hypershift/api/v1alpha1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1alpha4"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	finalizer = "hypershift.openshift.io/finalizer"
)

// OpenShiftClusterReconciler reconciles a OpenShiftCluster object
type OpenShiftClusterReconciler struct {
	client.Client
	Log   logr.Logger
	Infra *configv1.Infrastructure
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
		if err := r.delete(ctx, req.Name, req.Namespace); err != nil {
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

	cluster := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ocluster.GetNamespace(),
			Name:      ocluster.GetName(),
		},
		Spec: capiv1.ClusterSpec{
			ControlPlaneEndpoint: capiv1.APIEndpoint{},
			ControlPlaneRef: &corev1.ObjectReference{
				APIVersion: "hypershift.openshift.io/v1alpha1",
				Kind:       "HostedControlPlane",
				Namespace:  ocluster.GetNamespace(),
				Name:       ocluster.GetName(),
			},
			InfrastructureRef: &corev1.ObjectReference{
				APIVersion: "hypershift.openshift.io/v1alpha1",
				Kind:       "ExternalInfraCluster",
				Namespace:  ocluster.GetNamespace(),
				Name:       ocluster.GetName(),
			},
		},
	}
	patchHelper, err := patch.NewHelper(ocluster, r.Client)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to init patch helper: %w", err)
	}
	if err := ctrl.SetControllerReference(ocluster, cluster, r.Client.Scheme()); err != nil {
		return ctrl.Result{}, err
	}

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ocluster.GetNamespace(),
			Name:      ocluster.GetName(),
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			PullSecret:   ocluster.Spec.PullSecret,
			ServiceCIDR:  ocluster.Spec.ServiceCIDR,
			PodCIDR:      ocluster.Spec.PodCIDR,
			SSHKey:       ocluster.Spec.SSHKey,
			ReleaseImage: ocluster.Spec.Release.Image,
		},
	}
	ExternalInfraCluster := &hyperv1.ExternalInfraCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ocluster.GetNamespace(),
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

	r.Log.Info("Successfully reconciled")
	return ctrl.Result{}, nil
}

func (r *OpenShiftClusterReconciler) delete(ctx context.Context, name, namespace string) error {
	cluster := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	if err := r.Delete(ctx, cluster); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete cluster: %w", err)
	}
	r.Log.Info("Deleted cluster", "name", name)

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
