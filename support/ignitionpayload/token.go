package ignitionpayload

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// The bearer-token contract is shared between the generator (which mints tokens) and the serving
// tier (which verifies them). Both sides reference these constants so the audience, issuer, claim
// names, and selector header can never drift apart. The token authenticates the fetch and
// authorizes the owner (name-scope); it never identifies the payload — the request path and the
// rollout-generation header do — and it is never written to the PayloadStore or to status.
const (
	// TokenIssuer identifies the generator that signed a bearer token.
	TokenIssuer = "ignition-payload-controller"
	// TokenAudience is the audience the serving tier validates a presented token against.
	TokenAudience = "ignition-server"

	// RolloutGenerationHeader is the static HTTP header baked into a node's userData that selects
	// which rollout generation the server resolves. It is a plain (unsigned) request input: the
	// token's name-scope is the authorization boundary, and only the two retained generations
	// resolve to a live store entry, so an unsigned selector introduces no escalation.
	RolloutGenerationHeader = "rollout-generation"

	// ClaimPayloadNamespace and ClaimPayloadName are the custom JWT claims carrying the owner the
	// token is scoped to (the IgnitionPayload CR it may fetch).
	ClaimPayloadNamespace = "payloadNamespace"
	ClaimPayloadName      = "payloadName"

	// DefaultTokenClockSkew is the leeway applied to the time claims so a first-booting node whose
	// clock is wrong before NTP is not rejected.
	DefaultTokenClockSkew = 10 * time.Minute
)

// ErrTokenInvalid is returned by TokenVerifier.Verify for any token that fails signature, issuer,
// audience, time, or scope validation. The serving tier maps it to HTTP 401.
var ErrTokenInvalid = errors.New("invalid ignition token")

// TokenVerifier validates generator-signed bearer tokens statelessly against a public key. It holds
// only the public half of the signing key, so the stateless, horizontally-scaled serving tier can
// authenticate node fetches without a shared secret and without any store lookup.
type TokenVerifier struct {
	key      *ecdsa.PublicKey
	issuer   string
	audience string
	leeway   time.Duration
}

// NewTokenVerifier builds a verifier for ES256 tokens signed by the matching private key.
func NewTokenVerifier(pub *ecdsa.PublicKey) *TokenVerifier {
	return &TokenVerifier{
		key:      pub,
		issuer:   TokenIssuer,
		audience: TokenAudience,
		leeway:   DefaultTokenClockSkew,
	}
}

// Verify validates the token's signature, issuer, audience, and time claims and returns the OwnerRef
// it is scoped to. It deliberately does NOT check the rollout generation: the serving selector is an
// unsigned request input (the rollout-generation header), and the token's name-scope — not the
// generation — is the authorization boundary (see RolloutGenerationHeader). The caller must still
// confirm the returned owner authorizes the requested path.
func (v *TokenVerifier) Verify(tokenString string) (OwnerRef, error) {
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodECDSA); !ok {
			return nil, fmt.Errorf("unexpected signing method %q", t.Header["alg"])
		}
		return v.key, nil
	},
		jwt.WithValidMethods([]string{"ES256"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithLeeway(v.leeway),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return OwnerRef{}, fmt.Errorf("%w: %w", ErrTokenInvalid, err)
	}

	namespace, _ := claims[ClaimPayloadNamespace].(string)
	name, _ := claims[ClaimPayloadName].(string)
	if namespace == "" || name == "" {
		return OwnerRef{}, fmt.Errorf("%w: token is missing its owner scope", ErrTokenInvalid)
	}
	return OwnerRef{Namespace: namespace, Name: name}, nil
}

// LoadECDSAPublicKey parses a PEM-encoded ECDSA public key (the token-verification key the serving
// tier is provisioned with). It accepts a PKIX public key or an X.509 certificate PEM.
func LoadECDSAPublicKey(pemBytes []byte) (*ecdsa.PublicKey, error) {
	key, err := jwt.ParseECPublicKeyFromPEM(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ECDSA token-verification key: %w", err)
	}
	return key, nil
}
