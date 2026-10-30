package ignitionpayload

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"

	"github.com/golang-jwt/jwt/v5"
)

func TestNewJWTMinter(t *testing.T) {
	t.Run("mints a signed token the serving tier can verify, scoped to the owner, with no generation", func(t *testing.T) {
		g := NewWithT(t)
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		g.Expect(err).ToNot(HaveOccurred())
		m := NewJWTMinter(key)
		owner := payloadstore.OwnerRef{Namespace: "hcp", Name: "np-1"}

		tok, err := m.Mint(owner, time.Hour)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(tok).ToNot(BeEmpty())

		// The serving tier verifies signature, audience, issuer, and expiry with only the public key.
		claims := jwt.MapClaims{}
		parsed, err := jwt.ParseWithClaims(tok, claims, func(token *jwt.Token) (any, error) {
			return &key.PublicKey, nil
		}, jwt.WithValidMethods([]string{"ES256"}), jwt.WithAudience(tokenAudience), jwt.WithIssuer(tokenIssuer))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(parsed.Valid).To(BeTrue())

		// The token is scoped to the owner but carries NO rollout generation (selector is unsigned).
		g.Expect(claims["payloadNamespace"]).To(Equal("hcp"))
		g.Expect(claims["payloadName"]).To(Equal("np-1"))
		g.Expect(claims).ToNot(HaveKey("rolloutGeneration"))
	})

	t.Run("an expired token fails verification", func(t *testing.T) {
		g := NewWithT(t)
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		g.Expect(err).ToNot(HaveOccurred())
		m := NewJWTMinter(key)
		// Issue a token that expired well before now (beyond the allowed clock skew).
		m.now = func() time.Time { return time.Now().Add(-24 * time.Hour) }

		tok, err := m.Mint(payloadstore.OwnerRef{Namespace: "hcp", Name: "np-1"}, time.Minute)
		g.Expect(err).ToNot(HaveOccurred())

		_, err = jwt.ParseWithClaims(tok, jwt.MapClaims{}, func(token *jwt.Token) (any, error) {
			return &key.PublicKey, nil
		}, jwt.WithValidMethods([]string{"ES256"}))
		g.Expect(err).To(HaveOccurred(), "an expired token must fail verification")
	})
}

func TestPointerIgnitionConfig(t *testing.T) {
	g := NewWithT(t)
	cfg := pointerIgnitionConfig([]byte("CA"), "tok-abc", "ign.example.com", "np-1", 3)

	g.Expect(cfg.Ignition.Version).To(Equal("3.2.0"))
	g.Expect(cfg.Ignition.Security.TLS.CertificateAuthorities).To(HaveLen(1))
	g.Expect(cfg.Ignition.Config.Merge).To(HaveLen(1))

	merge := cfg.Ignition.Config.Merge[0]
	g.Expect(*merge.Source).To(Equal("https://ign.example.com/ignition/np-1"), "per-payload path carries the CR name")

	headers := map[string]string{}
	for _, h := range merge.HTTPHeaders {
		headers[h.Name] = *h.Value
	}
	g.Expect(headers["Authorization"]).To(Equal("Bearer tok-abc"))
	g.Expect(headers[rolloutGenerationHeader]).To(Equal("3"), "static header selects the rollout generation")
}

func TestLoadECDSAPrivateKey(t *testing.T) {
	g := NewWithT(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	g.Expect(err).ToNot(HaveOccurred())
	der, err := x509.MarshalECPrivateKey(key)
	g.Expect(err).ToNot(HaveOccurred())
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})

	loaded, err := LoadECDSAPrivateKey(pemBytes)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(loaded.Equal(key)).To(BeTrue())

	_, err = LoadECDSAPrivateKey([]byte("not a key"))
	g.Expect(err).To(HaveOccurred())
}
