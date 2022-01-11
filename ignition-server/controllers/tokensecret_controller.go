package controllers

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io/ioutil"
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
	TokenSecretConfigKey  = "config"
	TokenSecretTokenKey   = "token"
	TokenSecretAnnotation = "hypershift.openshift.io/ignition-config"
	// Set the ttl 1h above the reconcile resync period so every existing
	// token Secret has the chance to renew their expiry time on the PayloadStore.Get(token) operation
	// while the non existing ones get eventually garbageCollected.
	// https://github.com/kubernetes-sigs/controller-runtime/blob/1e4d87c9f9e15e4a58bb81909dd787f30ede7693/pkg/cache/cache.go#L118
	ttl = time.Hour * 11
)

func NewPayloadStore() *ExpiringCache {
	return &ExpiringCache{
		cache:   make(map[string]*entry),
		ttl:     ttl,
		RWMutex: sync.RWMutex{},
	}
}

// IgnitionProvider can build ignition payload contents
// for a given release image.
type IgnitionProvider interface {
	// GetPayload returns the ignition payload content for
	// the provided release image and a config string containing 0..N MachineConfig yaml definitions.
	GetPayload(ctx context.Context, payloadImage, config string) ([]byte, error)
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
//     config: |-
type TokenSecretReconciler struct {
	client.Client
	IgnitionProvider IgnitionProvider
	PayloadStore     *ExpiringCache
}

func tokenSecretAnnotationPredicate(ctx context.Context) predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			return processIfMatchesAnnotation(ctrl.LoggerFrom(ctx).WithValues("predicate", "updateEvent"), e.ObjectNew)
		},
		CreateFunc: func(e event.CreateEvent) bool {
			return processIfMatchesAnnotation(ctrl.LoggerFrom(ctx).WithValues("predicate", "createEvent"), e.Object)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return processIfMatchesAnnotation(ctrl.LoggerFrom(ctx).WithValues("predicate", "deleteEvent"), e.Object)
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return processIfMatchesAnnotation(ctrl.LoggerFrom(ctx).WithValues("predicate", "genericEvent"), e.Object)
		},
	}
}
func (r *TokenSecretReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	log := ctrl.Log.WithName("secret-token-controller")
	log.Info("SetupWithManager", "ns", os.Getenv("MY_NAMESPACE"))
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}).WithEventFilter(tokenSecretAnnotationPredicate(ctx)).
		Complete(r)
}

func processIfMatchesAnnotation(logger logr.Logger, obj client.Object) bool {
	kind := strings.ToLower(obj.GetObjectKind().GroupVersionKind().Kind)
	log := logger.WithValues("namespace", obj.GetNamespace(), kind, obj.GetName())

	if _, ok := obj.GetAnnotations()[TokenSecretAnnotation]; ok {
		log.V(3).Info("Resource matches annotation, will attempt to map resource")
		return true
	}

	log.V(3).Info("Resource does not match annotation, will not attempt to map resource")
	return false
}

func (r *TokenSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling")

	// The controller maintains an internal cache of tokens to payloads and the
	// secrets from which they are derived. Although cache entries have a TTL and
	// should expire naturally after that time, we also actively purge entries
	// from the cache if we can detect that a secret with associated tokens has
	// disappeared. The deletion handling code is just to reconcile the internal
	// token cache with reality in a more timely fashion than TTL alone.
	secretDeleted := false
	tokenSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, req.NamespacedName, tokenSecret); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Token Secret not found", "request", req.String())
			secretDeleted = true
		} else {
			return ctrl.Result{}, err
		}
	} else {
		if !tokenSecret.DeletionTimestamp.IsZero() {
			secretDeleted = true
		}
	}

	if secretDeleted {
		// If the secret was deleted, ensure that any corresponding tokens for that
		// secret are purged from the cache and return as there's nothing to do.
		for _, k := range r.PayloadStore.Keys() {
			v, ok := r.PayloadStore.Get(k)
			if ok && v.SecretName == req.Name {
				log.Info("Deleting token for missing secret", "tokenSecret", req.Name)
				r.PayloadStore.Delete(k)
			}
		}
		return ctrl.Result{}, nil
	}

	// If the tokenSecret is older than ttl then purge its token from the cache
	// and delete the secret.
	timeLived := time.Since(tokenSecret.CreationTimestamp.Time)
	if timeLived >= ttl {
		log.Info("Deleting expired token", "tokenSecret", client.ObjectKeyFromObject(tokenSecret))
		r.PayloadStore.Delete(string(tokenSecret.Data[TokenSecretTokenKey]))
		if err := r.Delete(ctx, tokenSecret); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to delete tokenSecret: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// Otherwise proceed to generate the payload and cache the content.
	token := string(tokenSecret.Data[TokenSecretTokenKey])
	if _, ok := r.PayloadStore.Get(token); ok {
		log.Info("Payload found in cache")
		return ctrl.Result{RequeueAfter: ttl - timeLived}, nil
	}

	releaseImage := string(tokenSecret.Data[TokenSecretReleaseKey])
	compressedConfig := tokenSecret.Data[TokenSecretConfigKey]
	config, err := decompress(compressedConfig)
	if err != nil {
		return ctrl.Result{}, err
	}

	payload, err := r.IgnitionProvider.GetPayload(ctx, releaseImage, string(config))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error getting ignition payload: %v", err)
	}

	log.Info("IgnitionProvider generated payload")
	r.PayloadStore.Set(token, CacheValue{Payload: payload, SecretName: tokenSecret.Name})
	return ctrl.Result{RequeueAfter: ttl - timeLived}, nil
}

func decompress(content []byte) ([]byte, error) {
	if len(content) == 0 {
		return nil, nil
	}
	gr, err := gzip.NewReader(bytes.NewBuffer(content))
	if err != nil {
		return nil, fmt.Errorf("failed to uncompress content: %w", err)
	}
	defer gr.Close()
	data, err := ioutil.ReadAll(gr)
	if err != nil {
		return nil, fmt.Errorf("failed to read content: %w", err)
	}
	return data, nil
}
