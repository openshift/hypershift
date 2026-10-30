package ignitionpayloadserver

import (
	"context"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"
	supportutil "github.com/openshift/hypershift/support/util"

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
	scoped, err := s.Verifier.Verify(token)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	owner := payloadstore.OwnerRef{Namespace: s.Namespace, Name: name}
	if scoped != owner {
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
		// 511 so ignition backs off and retries while the payload propagates (or until the
		// consumer re-points at a live generation).
		// https://coreos.github.io/ignition/operator-notes/#http-backoff-and-retry
		http.Error(w, "Payload not found", http.StatusNetworkAuthenticationRequired)
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
	_, _ = w.Write(payload)

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
