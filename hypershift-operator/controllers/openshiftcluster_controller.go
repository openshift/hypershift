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
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	hyperv1 "openshift.io/hypershift/api/v1alpha1"
	"openshift.io/hypershift/hypershift-operator/releaseinfo"
)

// OpenShiftClusterReconciler reconciles a OpenShiftCluster object
type OpenShiftClusterReconciler struct {
	client.Client
	Log logr.Logger

	ControlPlaneOperatorImage string
}

// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=openshiftclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=openshiftclusters/status,verbs=get;update;patch

func (r *OpenShiftClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	_ = r.Log.WithValues("openshiftcluster", req.NamespacedName)

	var result ctrl.Result

	cluster := &hyperv1.OpenShiftCluster{}
	isMissing := false
	err := r.Get(ctx, req.NamespacedName, cluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			isMissing = true
		} else {
			return result, fmt.Errorf("failed to get cluster %q: %w", req.NamespacedName, err)
		}
	}

	// Return early if deleted
	if isMissing || !cluster.DeletionTimestamp.IsZero() {
		if err := r.delete(ctx, req.Name); err != nil {
			r.Log.Error(err, "failed to delete cluster")
			return result, err
		}
		if sets.NewString(cluster.Finalizers...).Has("hypershift.openshift.io/finalizer") {
			cluster = cluster.DeepCopy()
			cluster.Finalizers = sets.NewString(cluster.Finalizers...).Delete("hypershift.openshift.io/finalizer").List()
			if err := r.Update(ctx, cluster); err != nil {
				return result, fmt.Errorf("failed to remove finalizer from cluster: %w", err)
			}
		}
		return result, nil
	}

	// Ensure the cluster has a finalizer for cleanup
	if !sets.NewString(cluster.Finalizers...).Has("hypershift.openshift.io/finalizer") {
		cluster = cluster.DeepCopy()
		cluster.Finalizers = append(cluster.Finalizers, "hypershift.openshift.io/finalizer")
		if err := r.Update(ctx, cluster); err != nil {
			return result, fmt.Errorf("failed to add finalizer to cluster: %w", err)
		}
	}

	// The images.json key contains a hash of image pullspec to release info
	// obtained from `oc adm release info $image -o json`.
	releaseImages := &corev1.ConfigMap{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: "hypershift", Name: "release-images"}, releaseImages); err != nil {
		result.RequeueAfter = 5 * time.Second
		return result, fmt.Errorf("no release images found")
	}
	imagesJSON, hasImagesJSON := releaseImages.Data["images.json"]
	if !hasImagesJSON {
		return result, fmt.Errorf("no images.json found in release images configmap")
	}
	images := map[string]releaseinfo.ReleaseImageInfo{}
	if err := json.Unmarshal([]byte(imagesJSON), &images); err != nil {
		return result, fmt.Errorf("failed to read images.json: %w", err)
	}
	releaseInfo, hasReleaseInfo := images[cluster.Spec.ReleaseImage]
	if !hasReleaseInfo {
		result.RequeueAfter = 5 * time.Second
		return result, fmt.Errorf("no release info found for image %q", cluster.Spec.ReleaseImage)
	}

	// First, set up infrastructure
	infraStatus, err := r.ensureInfrastructure(ctx, cluster)
	if err != nil {
		r.Log.Error(err, "failed to ensure infrastructure")
		return result, fmt.Errorf("failed to ensure infrastructure: %w", err)
	}

	// Wait for things like LB services to become available
	if !infraStatus.IsReady() {
		result.RequeueAfter = 5 * time.Second
		r.Log.Info("cluster infrastructure is still provisioning, will try again later")
		return result, nil
	}

	// Install the control plane into the infrastructure
	err = r.ensureControlPlane(ctx, cluster, infraStatus, &releaseInfo)
	if err != nil {
		r.Log.Error(err, "failed to ensure control plane")
		return result, fmt.Errorf("failed to ensure control plane: %w", err)
	}

	return ctrl.Result{}, nil
}

func (r *OpenShiftClusterReconciler) delete(ctx context.Context, name string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	if err := r.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete namespace: %w", err)
	}
	r.Log.Info("deleted namespace", "name", name)

	var infra configv1.Infrastructure
	if err := r.Get(ctx, client.ObjectKey{Name: "cluster"}, &infra); err != nil {
		return fmt.Errorf("failed to get cluster infra: %w", err)
	}

	machineSetName := generateMachineSetName(infra.Status.InfrastructureName, name, "worker")
	machineSet := &unstructured.Unstructured{}
	machineSet.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "machine.openshift.io",
		Version: "v1beta1",
		Kind:    "MachineSet",
	})
	machineSet.SetNamespace("openshift-machine-api")
	machineSet.SetName(machineSetName)
	if err := r.Delete(ctx, machineSet); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete machineset %s: %w", machineSetName, err)
	}
	r.Log.Info("deleted machineset", "name", machineSetName)

	machineSetConfig := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-machine-api",
			Name:      fmt.Sprintf("%s-user-data", name),
		},
	}
	if err := r.Delete(ctx, machineSetConfig); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete machineset secret %s: %w", machineSetConfig.Name, err)
	}
	r.Log.Info("deleted machineset secret", "name", machineSetConfig.Name)

	return nil
}

func (r *OpenShiftClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.OpenShiftCluster{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}
