package infrastatus

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kubeclient "k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"

	configv1 "github.com/openshift/api/config/v1"

	configclient "github.com/openshift/client-go/config/clientset/versioned"
	configlister "github.com/openshift/client-go/config/listers/config/v1"
)

type InfraStatusReconciler struct {
	Source     *configv1.Infrastructure
	Client     configclient.Interface
	KubeClient kubeclient.Interface
	Lister     configlister.InfrastructureLister
	Log        logr.Logger

	m              sync.Mutex
	hasSubresource *bool
}

func (r *InfraStatusReconciler) Reconcile(_ context.Context, req ctrl.Request) (ctrl.Result, error) {
	proceed, err := r.shouldReconcile()
	if err != nil || !proceed {
		return ctrl.Result{}, err
	}
	if req.Name != "cluster" {
		r.Log.Info("Unexpected infrastructure instance name", "name", req.Name)
		return ctrl.Result{}, nil
	}
	r.Log.Info("Reconciling", "name", req.NamespacedName.String())
	existing, err := r.Lister.Get(req.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("cannot fetch infrastructure %s: %v", req.Name, err)
	}
	updated := existing.DeepCopy()
	r.Source.Status.DeepCopyInto(&updated.Status)

	if reflect.DeepEqual(updated.Status, existing.Status) {
		return ctrl.Result{}, nil
	}
	r.Log.Info("Updating infrastructure status")
	_, err = r.Client.ConfigV1().Infrastructures().UpdateStatus(context.TODO(), updated, metav1.UpdateOptions{})
	return ctrl.Result{}, err
}

func (r *InfraStatusReconciler) shouldReconcile() (bool, error) {
	r.m.Lock()
	defer r.m.Unlock()
	if r.hasSubresource != nil {
		return *r.hasSubresource, nil
	}
	resourceList, err := r.KubeClient.Discovery().ServerResourcesForGroupVersion(configv1.GroupVersion.String())
	if err != nil {
		return false, fmt.Errorf("failed to discover resources for %s: %v", configv1.GroupVersion.String(), err)
	}
	result := false
	for _, resource := range resourceList.APIResources {
		if resource.Name == "infrastructures/status" {
			result = true
			break
		}
	}
	r.hasSubresource = &result
	return result, nil
}
