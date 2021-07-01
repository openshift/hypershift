package controllers

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	TokenSecretReleaseKey = "release"
	TokenSecretTokenKey   = "token"
	TokenSecretAnnotation = "hypershift.openshift.io/ignition-config"
)

func NewPayloadStore() *ExpiringCache {
	return &ExpiringCache{
		cache: make(map[string]*entry),
		// Set the ttl 1h above the reconcile resync period so every existing
		// token Secret has the chance to renew their expiry time on the PayloadStore.Get(token) operation
		// while the non exiting ones get eventually garbageCollected.
		// https://github.com/kubernetes-sigs/controller-runtime/blob/1e4d87c9f9e15e4a58bb81909dd787f30ede7693/pkg/cache/cache.go#L118
		ttl:     time.Hour * 11,
		RWMutex: sync.RWMutex{},
	}
}

// IgnitionProvider can build ignition payload contents
// for a given release image.
type IgnitionProvider interface {
	// GetPayload returns the ignition payload contents for
	// the provided release. In the future the list of
	// inputs may expand to incorporate user-provided ignition
	// overlay contents.
	GetPayload(ctx context.Context, payloadImage string) ([]byte, error)
}

// TokenSecretReconciler watches token Secrets
// and uses an IgnitionProvider to get a payload out them
// and stores it in the PayloadsStore.
// A token Secret is by contractual convention:
// type: Secret
//   metadata:
//   annotations:
// 	   hypershift.openshift.io/ignition-config: "true"
//	 data:
//     token: <authz token>
//     release: <release image string>
type TokenSecretReconciler struct {
	client.Client
	IgnitionProvider IgnitionProvider
	PayloadStore     *ExpiringCache
}

func tokenSecretAnnotationPredicate(ctx context.Context) predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			return processIfMatchesAnnotation(ctrl.LoggerFrom(ctx).WithValues("predicate", "updateEvent"), e.ObjectNew, TokenSecretAnnotation)
		},
		CreateFunc: func(e event.CreateEvent) bool {
			return processIfMatchesAnnotation(ctrl.LoggerFrom(ctx).WithValues("predicate", "createEvent"), e.Object, TokenSecretAnnotation)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return processIfMatchesAnnotation(ctrl.LoggerFrom(ctx).WithValues("predicate", "deleteEvent"), e.Object, TokenSecretAnnotation)
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return processIfMatchesAnnotation(ctrl.LoggerFrom(ctx).WithValues("predicate", "genericEvent"), e.Object, TokenSecretAnnotation)
		},
	}
}
func (r *TokenSecretReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	log := ctrl.Log.WithName("Setting up token controller")
	log.Info("SetupWithManager", "ns", os.Getenv("MY_NAMESPACE"))
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}).WithEventFilter(tokenSecretAnnotationPredicate(ctx)).
		Complete(r)
}

func processIfMatchesAnnotation(logger logr.Logger, obj client.Object, annotation string) bool {
	kind := strings.ToLower(obj.GetObjectKind().GroupVersionKind().Kind)
	log := logger.WithValues("namespace", obj.GetNamespace(), kind, obj.GetName())

	if _, ok := obj.GetAnnotations()[annotation]; ok {
		log.V(3).Info("Resource matches annotation, will attempt to map resource")
		return true
	}

	log.V(3).Info("Resource does not match annotation, will not attempt to map resource")
	return false
}

func (r *TokenSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling")

	tokenSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, req.NamespacedName, tokenSecret); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("not found", "request", req.String())
			return ctrl.Result{}, nil
		}
		log.Error(err, "error token Secret")
		return ctrl.Result{}, err
	}

	if !tokenSecret.DeletionTimestamp.IsZero() {
		// When this reconciler watch the deletion event
		// and tries to get the Resource it might already be gone
		// therefore this is unlikely to be reached.
		// This is just a best effort to synchronous cleanup.
		// The PayloadStore expiring mechanism takes care of consistently delete expired entries.
		r.PayloadStore.Delete(string(tokenSecret.Data[TokenSecretTokenKey]))
		return ctrl.Result{}, nil
	}

	token := string(tokenSecret.Data[TokenSecretTokenKey])
	if _, ok := r.PayloadStore.Get(token); ok {
		log.Info("Payload found in cache")
		return ctrl.Result{}, nil
	}

	releaseImage := string(tokenSecret.Data[TokenSecretReleaseKey])
	payload, err := r.IgnitionProvider.GetPayload(ctx, releaseImage)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error getting ignition payload: %v", err)
	}

	log.Info("IgnitionProvider generated payload")
	r.PayloadStore.Set(token, payload)

	return ctrl.Result{}, nil
}
