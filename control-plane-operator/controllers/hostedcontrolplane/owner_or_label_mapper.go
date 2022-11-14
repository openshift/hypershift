package hostedcontrolplane

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

// EnqueueForOwnerOrLabel is a superset of EnqueueRequestForOwner -- if the object has an
// appropriate OwnerReference, the owner is enqueued -- that also enqueues an object in
// the same namespace with a name indicated by the
type EnqueueForOwnerOrLabel struct {
	handler.EnqueueRequestForOwner
}

// Create is called in response to an create event - e.g. Pod Creation.
func (e *EnqueueForOwnerOrLabel) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	e.EnqueueRequestForOwner.Create(evt, q)
	enqueueForLabel(evt.Object, q)
}

// Update is called in response to an update event -  e.g. Pod Updated.
func (e *EnqueueForOwnerOrLabel) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	e.EnqueueRequestForOwner.Update(evt, q)
	enqueueForLabel(evt.ObjectOld, q)
	enqueueForLabel(evt.ObjectNew, q)
}

// Delete is called in response to a delete event - e.g. Pod Deleted.
func (e *EnqueueForOwnerOrLabel) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	e.EnqueueRequestForOwner.Delete(evt, q)
	enqueueForLabel(evt.Object, q)
}

// Generic is called in response to an event of an unknown type or a synthetic event triggered as a cron or
// external trigger request - e.g. reconcile Autoscaling, or a Webhook.
func (e *EnqueueForOwnerOrLabel) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
	e.EnqueueRequestForOwner.Generic(evt, q)
	enqueueForLabel(evt.Object, q)
}

var _ handler.EventHandler = &EnqueueForOwnerOrLabel{}

func enqueueForLabel(obj metav1.Object, q workqueue.RateLimitingInterface) {
	labels := obj.GetLabels()
	if labels == nil {
		return
	}
	if hcpName := labels[hyperv1.EnableKonnectivityLabel]; hcpName != "" {
		q.Add(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: obj.GetNamespace(),
				Name:      hcpName,
			},
		})
	}
}
