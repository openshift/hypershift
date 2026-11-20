package ignitionpayloadserver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"
	supportutil "github.com/openshift/hypershift/support/util"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ignPathPattern matches GET /ignition/<name>, capturing the IgnitionPayload name (a DNS-1123
// subdomain). The name identifies which CR's payload to serve; the rollout generation comes from a
// separate header, and the bearer token carries only authentication.
var ignPathPattern = regexp.MustCompile(`^/ignition/([a-z0-9]([-a-z0-9.]*[a-z0-9])?)$`)

// onServedTimeout bounds the detached IgnitionReached write so a slow apiserver cannot leak
// goroutines under request load.
const onServedTimeout = 10 * time.Second

// Server serves ignition payloads from the PayloadStore with read-through on cache miss.
//
// Authentication and payload identification are decoupled: the request path (/ignition/<name>) plus
// the rollout-generation header identify which payload to serve, while the bearer token does only
// authentication and authorization (its name-scope must authorize the path's name).
type Server struct {
	// Namespace is the HCP namespace the serving tier runs in; it is the owner namespace for every
	// payload this server resolves.
	Namespace string
	// Verifier validates the presented bearer token statelessly against the public signing key and
	// returns the owner it is scoped to.
	Verifier *payloadstore.TokenVerifier
	// Cached reads via the manager's informer cache (fast path).
	Cached client.Reader
	// Uncached reads directly from the API server (read-through when the cache has not yet hydrated
	// a just-published payload).
	Uncached client.Reader
	// OnServed, if set, is invoked (best-effort, asynchronously) after a successful serve with the
	// owning CR and the served rollout generation, to record IgnitionReached.
	OnServed func(ctx context.Context, owner payloadstore.OwnerRef, rolloutGeneration int64)
}

// HandleIgnition serves GET /ignition/<name>. It resolves the CR name from the path, verifies the
// bearer token authorizes that name, reads the rollout generation from the header, resolves the
// payload from the store (cached, then read-through to the API on a miss), serves the sanitized
// payload, and triggers the OnServed callback.
func (s *Server) HandleIgnition(w http.ResponseWriter, r *http.Request) {
	// Ignition fetches with GET only. Reject other methods explicitly: a HEAD on this path would
	// otherwise follow the serve path (net/http suppresses the body) and record IgnitionReached
	// without the node ever receiving the payload.
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	match := ignPathPattern.FindStringSubmatch(r.URL.Path)
	if match == nil {
		http.NotFound(w, r)
		return
	}
	name := match[1]

	token, ok := bearerToken(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Authenticate the token and confirm its name-scope authorizes the requested path. The owner
	// namespace is always this server's namespace.
	subject, err := s.Verifier.Verify(token)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	// The token carries only a hash of the owner it is scoped to (no cleartext control-plane
	// namespace); authorize by hashing the owner expected for this path and comparing.
	owner := payloadstore.OwnerRef{Namespace: s.Namespace, Name: name}
	if subject != payloadstore.HashOwner(owner) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// The rollout generation is an unsigned selector carried in a static header; it selects which
	// of the owner's retained generations to resolve.
	rolloutGeneration, err := strconv.ParseInt(r.Header.Get(payloadstore.RolloutGenerationHeader), 10, 64)
	if err != nil || rolloutGeneration < 1 {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	payload, err := s.lookup(r.Context(), owner, rolloutGeneration)
	switch {
	case errors.Is(err, payloadstore.ErrNotFound):
		// A requested generation below the owner's retained floor has been retired and (generations
		// only increment) will never be served again, so return 410 to make ignition give up rather
		// than retry forever — ignition retries only on >=500, treating anything below 500 as terminal.
		// Any other miss stays 511 (retry): the generator stores a payload before its generation is
		// ever advertised, so this should not happen, and a node must never fail its boot on a miss we
		// cannot prove is permanent.
		// https://coreos.github.io/ignition/operator-notes/#http-backoff-and-retry
		floor, current, ok := s.retainedGenerationBounds(r.Context(), owner)
		if ok && rolloutGeneration < floor {
			http.Error(w, fmt.Sprintf("ignition payload generation %d has been retired; current generation is %d", rolloutGeneration, current), http.StatusGone)
			return
		}
		if ok {
			http.Error(w, fmt.Sprintf("ignition payload generation %d not found; current generation is %d", rolloutGeneration, current), http.StatusNetworkAuthenticationRequired)
		} else {
			http.Error(w, fmt.Sprintf("ignition payload generation %d not found; no generation currently available", rolloutGeneration), http.StatusNetworkAuthenticationRequired)
		}
		return
	case err != nil:
		log.Printf("failed to read payload: %s", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := supportutil.SanitizeIgnitionPayload(payload); err != nil {
		log.Printf("invalid ignition payload: %s", err)
		http.Error(w, "Invalid ignition payload", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(payload); err != nil {
		// The node did not receive the full payload, so do not record IgnitionReached for this serve;
		// ignition backs off and retries, and a later successful serve records it.
		log.Printf("failed to write payload: %s", err)
		return
	}

	if s.OnServed != nil {
		// Detach from the request context so the IgnitionReached write completes after the response
		// is written, but bound it with a timeout so a slow apiserver can't leak goroutines under
		// request load.
		go func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), onServedTimeout)
			defer cancel()
			s.OnServed(ctx, owner, rolloutGeneration)
		}()
	}
}

// lookup resolves (owner, rolloutGeneration) via the cached reader, falling back to the uncached
// (API) reader on a cache miss so a payload is servable the instant the generator's Put returns.
func (s *Server) lookup(ctx context.Context, owner payloadstore.OwnerRef, rolloutGeneration int64) ([]byte, error) {
	payload, _, err := payloadstore.ReadPayload(ctx, s.Cached, s.Namespace, owner, rolloutGeneration)
	if errors.Is(err, payloadstore.ErrNotFound) {
		payload, _, err = payloadstore.ReadPayload(ctx, s.Uncached, s.Namespace, owner, rolloutGeneration)
	}
	return payload, err
}

// retainedGenerationBounds returns the owner's lowest retained ("floor") and current rollout
// generations from the IgnitionPayload status, read authoritatively (uncached) so cache lag cannot
// misclassify a live generation as retired. The status — not the store backend — is the source of truth
// for which generations are live: the generator retains current + previous, so previous (when present)
// is the floor, else current. ok is false when the CR is unreadable or nothing has been generated yet,
// in which case the caller treats a miss as non-permanent (511) rather than failing a boot.
func (s *Server) retainedGenerationBounds(ctx context.Context, owner payloadstore.OwnerRef) (floor, current int64, ok bool) {
	cr := &hyperv1.IgnitionPayload{}
	if err := s.Uncached.Get(ctx, client.ObjectKey{Namespace: owner.Namespace, Name: owner.Name}, cr); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Printf("failed to read IgnitionPayload %s/%s: %s", owner.Namespace, owner.Name, err)
		}
		return 0, 0, false
	}
	current = cr.Status.Current.RolloutGeneration
	if current == 0 {
		return 0, 0, false
	}
	floor = current
	if cr.Status.Previous.RolloutGeneration > 0 {
		floor = cr.Status.Previous.RolloutGeneration
	}
	return floor, current, true
}

// bearerToken returns the signed JWT from the Authorization header. The token is used as presented
// (it is a compact JWS, not an opaque blob), so there is no decoding step.
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, prefix) {
		return "", false
	}
	token := strings.TrimPrefix(auth, prefix)
	if token == "" {
		return "", false
	}
	return token, true
}
