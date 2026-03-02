package metricsproxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sync"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const tokenCacheTTL = 5 * time.Minute

type cachedAuthResult struct {
	tokenHash string
	expiry    time.Time
}

type TokenAuthenticator struct {
	authorizedSAs []string
	clientset     kubernetes.Interface

	// cache stores one entry per SA username. The map is bounded by the
	// number of entries in authorizedSAs (typically 1-2).
	mu    sync.RWMutex
	cache map[string]cachedAuthResult
}

func NewTokenAuthenticator(kasClient kubernetes.Interface, authorizedSAs []string) *TokenAuthenticator {
	return &TokenAuthenticator{
		authorizedSAs: authorizedSAs,
		clientset:     kasClient,
		cache:         make(map[string]cachedAuthResult, len(authorizedSAs)),
	}
}

func (a *TokenAuthenticator) Authenticate(ctx context.Context, token string) (bool, error) {
	hash := hashToken(token)

	// Check if any cached SA entry matches this token hash and is still valid.
	a.mu.RLock()
	for _, entry := range a.cache {
		if entry.tokenHash == hash && time.Now().Before(entry.expiry) {
			a.mu.RUnlock()
			return true, nil
		}
	}
	a.mu.RUnlock()

	review, err := a.clientset.AuthenticationV1().TokenReviews().Create(ctx, &authenticationv1.TokenReview{
		Spec: authenticationv1.TokenReviewSpec{
			Token: token,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return false, fmt.Errorf("token review request failed: %w", err)
	}

	if !review.Status.Authenticated {
		return false, nil
	}

	username := review.Status.User.Username
	authorized := slices.Contains(a.authorizedSAs, username)

	// Only cache results for authorized SAs. This replaces any previous
	// token for this SA (e.g. after token rotation).
	if authorized {
		a.mu.Lock()
		a.cache[username] = cachedAuthResult{
			tokenHash: hash,
			expiry:    time.Now().Add(tokenCacheTTL),
		}
		a.mu.Unlock()
	}

	return authorized, nil
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
