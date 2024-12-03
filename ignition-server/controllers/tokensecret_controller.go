package controllers

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/util"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	TokenSecretReleaseKey                   = "release"
	TokenSecretConfigKey                    = "config"
	TokenSecretTokenKey                     = "token"
	TokenSecretOldTokenKey                  = "old_token"
	TokenSecretPayloadKey                   = "payload"
	TokenSecretMessageKey                   = "message"
	TokenSecretPullSecretHashKey            = "pull-secret-hash"
	TokenSecretHCConfigurationHashKey       = "hc-configuration-hash"
	TokenSecretAdditionalTrustBundleHashKey = "additional-trust-bundle-hash"
	InvalidConfigReason                     = "InvalidConfig"
	TokenSecretReasonKey                    = "reason"
	TokenSecretAnnotation                   = "hypershift.openshift.io/ignition-config"
	TokenSecretNodePoolUpgradeType          = "hypershift.openshift.io/node-pool-upgrade-type"
	TokenSecretTokenGenerationTime          = "hypershift.openshift.io/last-token-generation-time"
	// Set the ttl 1h above the reconcile resync period so every existing
	// token Secret has the chance to rotate their token ID during a reconciliation cycle
	// while the expired ones get eventually garbageCollected.
	// https://github.com/kubernetes-sigs/controller-runtime/blob/1e4d87c9f9e15e4a58bb81909dd787f30ede7693/pkg/cache/cache.go#L118
	ttl = time.Hour * 11
)

var (
	TokenRotationTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "ign_server_token_rotation_total",
	})

	PayloadCacheMissTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "ign_server_payload_cache_miss_total",
	})

	PayloadGenerationSeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "ign_server_payload_generation_seconds",
		Buckets: []float64{5, 15, 30, 45, 60},
	})
)

func init() {
	metrics.Registry.MustRegister(
		TokenRotationTotal,
		PayloadCacheMissTotal,
		PayloadGenerationSeconds,
	)
}

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
	GetPayload(ctx context.Context, payloadImage, config, pullSecretHash, additionalTrustBundleHash, hcConfigurationHash string) ([]byte, error)
}

// TokenSecretReconciler watches token Secrets
// and uses an IgnitionProvider to get a payload out them,
// stores it in the PayloadsStore, and rotates the token ID periodically.
// A token Secret is by contractual convention:
// type: Secret
//
//	  metadata:
//	  annotations:
//		   hypershift.openshift.io/ignition-config: "true"
//		 data:
//	    token: <authz token>
//	    old_token: <authz token>
//	    release: <release image string>
//	    config: |-
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

// isTokenExpired parses an expiration annotation and compares with the current time to see if a token is expired.
// invalid formats are considered expired
func isTokenExpired(logrInstance logr.Logger, tokenAnnotations map[string]string) bool {
	if expirationTimestampRaw, ok := tokenAnnotations[hyperv1.IgnitionServerTokenExpirationTimestampAnnotation]; ok {
		expirationTime, err := time.Parse(time.RFC3339, expirationTimestampRaw)
		if err != nil {
			logrInstance.Error(err, "Invalid format for expiration time")
			logrInstance.Info("Due to invalid expiration format: marking token as expired")
			return true
		}
		return time.Now().After(expirationTime)
	}
	return false
}

// processExpiredToken handles clearing the cache of the expired token(s) and ensuring the token secret is removed
// from the management cluster
func (r *TokenSecretReconciler) processExpiredToken(ctx context.Context, tokenSecret *corev1.Secret) error {
	if oldToken, ok := tokenSecret.Data[TokenSecretOldTokenKey]; ok {
		r.PayloadStore.Delete(string(oldToken))
	}
	if currentToken, ok := tokenSecret.Data[TokenSecretTokenKey]; ok {
		r.PayloadStore.Delete(string(currentToken))
	}
	if err := r.Client.Delete(ctx, tokenSecret); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
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

	// if a token is expired remove it
	if isTokenExpired(log, tokenSecret.Annotations) {
		err := r.processExpiredToken(ctx, tokenSecret)
		return ctrl.Result{}, err
	}

	// Otherwise proceed to generate the payload and cache the content.
	// Rotate the Token if necessary.
	now := time.Now()
	timeLived, err := getTokenTimeLived(tokenSecret, now)
	if err != nil {
		return ctrl.Result{}, err
	}

	token := string(tokenSecret.Data[TokenSecretTokenKey])
	if value, ok := r.PayloadStore.Get(token); ok {
		log.Info("Payload found in cache")

		if tokenNeedRotation(timeLived) {
			log.Info("Rotating token ID")
			if err := r.rotateToken(ctx, tokenSecret, value, now); err != nil {
				return ctrl.Result{}, err
			}
			TokenRotationTotal.Inc()
		}
		return ctrl.Result{RequeueAfter: ttl/2 - durationDeref(timeLived)}, nil
	}

	// If something else rotated the token (e.g. running in HA), we fall back to set the cache value from the old one.
	oldToken, ok := tokenSecret.Data[TokenSecretOldTokenKey]
	if ok {
		if value, ok := r.PayloadStore.Get(string(oldToken)); ok {
			r.PayloadStore.Set(token, value)
			return ctrl.Result{RequeueAfter: ttl/2 - durationDeref(timeLived)}, nil
		}
	}

	releaseImage := string(tokenSecret.Data[TokenSecretReleaseKey])
	compressedConfig := tokenSecret.Data[TokenSecretConfigKey]
	config, err := util.DecodeAndDecompress(compressedConfig)
	if err != nil {
		errWithFullMsg := fmt.Errorf("failed to decode and decompress config: %w", err)
		if hasSameReasonAndMessage(tokenSecret, InvalidConfigReason, errWithFullMsg) {
			return ctrl.Result{}, errWithFullMsg
		}

		patch := tokenSecret.DeepCopy()
		patch.Data[TokenSecretReasonKey] = []byte(InvalidConfigReason)
		patch.Data[TokenSecretMessageKey] = []byte(errWithFullMsg.Error())
		if err := r.Client.Patch(ctx, patch, client.MergeFrom(tokenSecret)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to patch tokenSecret with payload content: %w", err)
		}

		return ctrl.Result{}, err
	}

	PayloadCacheMissTotal.Inc()
	pullSecretHash := string(tokenSecret.Data[TokenSecretPullSecretHashKey])
	hcConfigurationHash := string(tokenSecret.Data[TokenSecretHCConfigurationHashKey])
	additionalTrustBundleHash := string(tokenSecret.Data[TokenSecretAdditionalTrustBundleHashKey])
	payload, err := func() ([]byte, error) {
		start := time.Now()
		payload, err := r.IgnitionProvider.GetPayload(ctx, releaseImage, config.String(), pullSecretHash, additionalTrustBundleHash, hcConfigurationHash)
		if err != nil {
			return nil, fmt.Errorf("error getting ignition payload: %v", err)
		}
		duration := time.Since(start).Round(time.Second).Seconds()
		log.Info("got ignition payload", "duration", duration)
		PayloadGenerationSeconds.Observe(duration)
		return payload, err
	}()
	if err != nil {
		// This patch could flood the API server, so we should only do it when the reason or message is different from the current one.
		// More info here: https://issues.redhat.com/browse/OCPBUGS-42320.
		errWithFullMsg := fmt.Errorf("failed to generate payload: %w", err)
		if hasSameReasonAndMessage(tokenSecret, InvalidConfigReason, errWithFullMsg) {
			return ctrl.Result{}, errWithFullMsg
		}

		patch := tokenSecret.DeepCopy()
		patch.Data[TokenSecretReasonKey] = []byte(InvalidConfigReason)
		patch.Data[TokenSecretMessageKey] = []byte(errWithFullMsg.Error())
		if err := r.Client.Patch(ctx, patch, client.MergeFrom(tokenSecret)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to patch tokenSecret with payload content: %w", err)
		}

		return ctrl.Result{}, err
	}

	log.Info("IgnitionProvider generated payload")
	r.PayloadStore.Set(token, CacheValue{Payload: payload, SecretName: tokenSecret.Name})
	oldToken, ok = tokenSecret.Data[TokenSecretOldTokenKey]
	if ok {
		// If we got here and there's an old token e.g. ignition server pod was restarted, then we set it as well
		// So Machines that were given that token right before the restart can succeed.
		r.PayloadStore.Set(string(oldToken), CacheValue{Payload: payload, SecretName: tokenSecret.Name})
	}

	patch := tokenSecret.DeepCopy()
	patch.Data[TokenSecretPayloadKey] = payload
	if string(hyperv1.UpgradeTypeReplace) == tokenSecret.Annotations[TokenSecretNodePoolUpgradeType] {
		delete(patch.Data, TokenSecretPayloadKey)
	}

	patch.Data[TokenSecretReasonKey] = []byte(hyperv1.AsExpectedReason)
	patch.Data[TokenSecretMessageKey] = []byte("Payload generated successfully")

	if err := r.Client.Patch(ctx, patch, client.MergeFrom(tokenSecret)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to patch tokenSecret with payload content: %w", err)
	}

	return ctrl.Result{RequeueAfter: ttl/2 - durationDeref(timeLived)}, nil
}

func hasSameReasonAndMessage(tokenSecret *corev1.Secret, reason string, message error) bool {
	return string(tokenSecret.Data[TokenSecretReasonKey]) == reason && string(tokenSecret.Data[TokenSecretMessageKey]) == message.Error()
}

// getTokenIDTimeLived returns the duration a from TokenSecretLastUpdatedTokenIDAnnotation til now.
func getTokenTimeLived(tokenSecret *corev1.Secret, now time.Time) (*time.Duration, error) {
	generationTime, ok := tokenSecret.Annotations[TokenSecretTokenGenerationTime]
	if !ok {
		return nil, nil
	}
	generationTimeParsed, err := time.Parse(time.RFC3339Nano, generationTime)
	if err != nil {
		return nil, err
	}
	timeLived := now.Sub(generationTimeParsed)
	return &timeLived, nil
}

// tokenIDNeedRotation returns true if a duration is longer than the ttl/2.
// This is the criteria to trigger a token ID rotation.
func tokenNeedRotation(timeLived *time.Duration) bool {
	if timeLived == nil || *timeLived >= ttl/2 {
		return true
	}

	return false
}

// rotateTokenID generates a new UUID for an existing value:
// Patches the tokenSecret token key with it and stores it on the cache.
func (r *TokenSecretReconciler) rotateToken(ctx context.Context, tokenSecret *corev1.Secret, value CacheValue, rotationTime time.Time) error {
	newToken := uuid.New().String()

	patch := tokenSecret.DeepCopy()
	if patch.Annotations == nil {
		patch.Annotations = make(map[string]string)
	}

	oldToken, ok := tokenSecret.Data[TokenSecretOldTokenKey]
	if ok {
		r.PayloadStore.Delete(string(oldToken))

	}

	patch.Annotations[TokenSecretTokenGenerationTime] = rotationTime.Format(time.RFC3339Nano)
	patch.Data[TokenSecretOldTokenKey] = tokenSecret.Data[TokenSecretTokenKey]
	patch.Data[TokenSecretTokenKey] = []byte(newToken)

	// Set the new token before patching the object. Otherwise, there is a race: if the secret is reconciled
	// before the value is set in the cache the new token would require a new payload generation in that reconciliation.
	r.PayloadStore.Set(newToken, value)

	if err := r.Client.Patch(ctx, patch, client.MergeFrom(tokenSecret)); err != nil {
		// If token patch operation fails, consistently restore the cache.
		// Otherwise, the next reconciliation the token would require a new payload generation because
		// it wouldn't be in the cache anymore.
		r.PayloadStore.Delete(newToken)
		r.PayloadStore.Set(string(oldToken), value)
		return err
	}

	return nil
}

func durationDeref(duration *time.Duration) time.Duration {
	if duration == nil {
		return time.Duration(0)
	}
	return *duration
}
