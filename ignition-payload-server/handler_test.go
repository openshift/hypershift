package ignitionpayloadserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/golang-jwt/jwt/v5"
)

const (
	validIgn = `{"ignition":{"version":"3.2.0"}}`
	testNS   = "hcp"
	testName = "np-1"
	testGen  = 2
)

type servedCall struct {
	owner payloadstore.OwnerRef
	gen   int64
}

func testKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	NewWithT(t).Expect(err).ToNot(HaveOccurred())
	return key
}

// mintToken signs a token matching the generator's contract (shared constants), so the server's
// verifier accepts it. Minting via the library keeps the server tests independent of the generator
// package; the mint/verify contract itself is pinned by the generator's round-trip test.
func mintToken(t *testing.T, key *ecdsa.PrivateKey, owner payloadstore.OwnerRef) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": payloadstore.TokenIssuer,
		"aud": payloadstore.TokenAudience,
		"sub": payloadstore.HashOwner(owner),
		"iat": now.Unix(),
		"nbf": now.Add(-time.Minute).Unix(),
		"exp": now.Add(time.Hour).Unix(),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodES256, claims).SignedString(key)
	NewWithT(t).Expect(err).ToNot(HaveOccurred())
	return signed
}

func seedStore(t *testing.T, gen int64) client.Client {
	t.Helper()
	c := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
	s := payloadstore.NewSecretBackedStoreForClient(c, testNS)
	NewWithT(t).Expect(s.Put(context.Background(), payloadstore.OwnerRef{Namespace: testNS, Name: testName}, gen, "cfg-1", []byte(validIgn))).To(Succeed())
	return c
}

func newReq(method, path, token, gen string) *http.Request {
	req := httptest.NewRequestWithContext(context.Background(), method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if gen != "" {
		req.Header.Set(payloadstore.RolloutGenerationHeader, gen)
	}
	return req
}

func doGet(h http.HandlerFunc, path, token, gen string) *httptest.ResponseRecorder {
	return doReq(h, http.MethodGet, path, token, gen)
}

func doReq(h http.HandlerFunc, method, path, token, gen string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h(rec, newReq(method, path, token, gen))
	return rec
}

// doReqWriter drives the handler with a caller-supplied ResponseWriter, used to inject a failing writer.
func doReqWriter(h http.HandlerFunc, w http.ResponseWriter, method, path, token, gen string) {
	h(w, newReq(method, path, token, gen))
}

func TestServe(t *testing.T) {
	g := NewWithT(t)
	key := testKey(t)
	owner := payloadstore.OwnerRef{Namespace: testNS, Name: testName}
	verifier := payloadstore.NewTokenVerifier(&key.PublicKey)
	tok := mintToken(t, key, owner)

	empty := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
	withPayload := seedStore(t, testGen)

	// cached MISS + uncached HIT → 200 read-through (not 511); OnServed receives owner+generation.
	served := make(chan servedCall, 1)
	s := &Server{Namespace: testNS, Verifier: verifier, Cached: empty, Uncached: withPayload,
		OnServed: func(_ context.Context, o payloadstore.OwnerRef, gen int64) {
			served <- servedCall{o, gen}
		}}
	rec := doGet(s.HandleIgnition, "/ignition/"+testName, tok, "2")
	g.Expect(rec.Code).To(Equal(http.StatusOK))
	g.Expect(rec.Body.String()).To(Equal(validIgn))
	g.Eventually(served).Should(Receive(Equal(servedCall{owner, testGen})))

	// cached HIT → 200 (no uncached needed).
	s2 := &Server{Namespace: testNS, Verifier: verifier, Cached: withPayload, Uncached: empty}
	g.Expect(doGet(s2.HandleIgnition, "/ignition/"+testName, tok, "2").Code).To(Equal(http.StatusOK))

	// both miss → 511 so ignition retries.
	s3 := &Server{Namespace: testNS, Verifier: verifier, Cached: empty, Uncached: empty}
	g.Expect(doGet(s3.HandleIgnition, "/ignition/"+testName, tok, "2").Code).To(Equal(http.StatusNetworkAuthenticationRequired))
}

// TestServeMissClassification: a miss for a generation below the retained floor is permanently retired
// and returns 410 (ignition gives up), while a miss for a generation at/above the floor stays 511
// (ignition retries).
func TestServeMissClassification(t *testing.T) {
	g := NewWithT(t)
	key := testKey(t)
	owner := payloadstore.OwnerRef{Namespace: testNS, Name: testName}
	tok := mintToken(t, key, owner)

	// The IgnitionPayload status is the source of truth for the live range: current 3, previous 2 →
	// floor 2. No payload Secret is seeded, so every lookup misses and exercises the classification.
	cr := &hyperv1.IgnitionPayload{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNS, Name: testName},
		Status: hyperv1.IgnitionPayloadStatus{
			Current:  hyperv1.PayloadReference{RolloutGeneration: 3},
			Previous: hyperv1.PayloadReference{RolloutGeneration: 2},
		},
	}
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(cr).Build()
	s := &Server{Namespace: testNS, Verifier: payloadstore.NewTokenVerifier(&key.PublicKey), Cached: c, Uncached: c}

	// below the floor → retired → 410 (ignition gives up); the message names requested and current.
	retired := doGet(s.HandleIgnition, "/ignition/"+testName, tok, "1")
	g.Expect(retired.Code).To(Equal(http.StatusGone))
	g.Expect(retired.Body.String()).To(ContainSubstring("generation 1 has been retired"))
	g.Expect(retired.Body.String()).To(ContainSubstring("current generation is 3"))

	// at/above the floor but not stored → 511 (ignition retries).
	notFound := doGet(s.HandleIgnition, "/ignition/"+testName, tok, "5")
	g.Expect(notFound.Code).To(Equal(http.StatusNetworkAuthenticationRequired))
	g.Expect(notFound.Body.String()).To(ContainSubstring("generation 5 not found"))
	g.Expect(notFound.Body.String()).To(ContainSubstring("current generation is 3"))
}

func TestAuthorization(t *testing.T) {
	g := NewWithT(t)
	key := testKey(t)
	owner := payloadstore.OwnerRef{Namespace: testNS, Name: testName}
	verifier := payloadstore.NewTokenVerifier(&key.PublicKey)
	tok := mintToken(t, key, owner)
	withPayload := seedStore(t, testGen)
	s := &Server{Namespace: testNS, Verifier: verifier, Cached: withPayload, Uncached: withPayload}

	// missing auth → 401.
	g.Expect(doGet(s.HandleIgnition, "/ignition/"+testName, "", "2").Code).To(Equal(http.StatusUnauthorized))

	// token signed by a different key → 401.
	g.Expect(doGet(s.HandleIgnition, "/ignition/"+testName, mintToken(t, testKey(t), owner), "2").Code).To(Equal(http.StatusUnauthorized))

	// token scoped to a different name → 403 (scope does not authorize the path's name).
	otherScope := mintToken(t, key, payloadstore.OwnerRef{Namespace: testNS, Name: "np-2"})
	g.Expect(doGet(s.HandleIgnition, "/ignition/"+testName, otherScope, "2").Code).To(Equal(http.StatusForbidden))

	// missing / non-numeric / non-positive generation header → 400.
	g.Expect(doGet(s.HandleIgnition, "/ignition/"+testName, tok, "").Code).To(Equal(http.StatusBadRequest))
	g.Expect(doGet(s.HandleIgnition, "/ignition/"+testName, tok, "abc").Code).To(Equal(http.StatusBadRequest))
	g.Expect(doGet(s.HandleIgnition, "/ignition/"+testName, tok, "0").Code).To(Equal(http.StatusBadRequest))

	// non-/ignition path and a nameless /ignition/ path → 404.
	g.Expect(doGet(s.HandleIgnition, "/healthz", tok, "2").Code).To(Equal(http.StatusNotFound))
	g.Expect(doGet(s.HandleIgnition, "/ignition/", tok, "2").Code).To(Equal(http.StatusNotFound))
}

// TestOnServedContextHasDeadline: the detached OnServed context must carry a timeout so a slow
// apiserver can't leak unbounded goroutines.
func TestOnServedContextHasDeadline(t *testing.T) {
	g := NewWithT(t)
	key := testKey(t)
	owner := payloadstore.OwnerRef{Namespace: testNS, Name: testName}
	withPayload := seedStore(t, testGen)
	done := make(chan bool, 1)
	s := &Server{Namespace: testNS, Verifier: payloadstore.NewTokenVerifier(&key.PublicKey),
		Cached: withPayload, Uncached: withPayload,
		OnServed: func(ctx context.Context, _ payloadstore.OwnerRef, _ int64) {
			_, hasDeadline := ctx.Deadline()
			done <- hasDeadline
		}}
	g.Expect(doGet(s.HandleIgnition, "/ignition/"+testName, mintToken(t, key, owner), "2").Code).To(Equal(http.StatusOK))
	g.Eventually(done).Should(Receive(BeTrue()))
}

// failingResponseWriter records the status code but fails every body write, simulating a connection
// dropped mid-response.
type failingResponseWriter struct {
	header http.Header
	code   int
}

func (f *failingResponseWriter) Header() http.Header {
	if f.header == nil {
		f.header = http.Header{}
	}
	return f.header
}
func (f *failingResponseWriter) WriteHeader(code int)      { f.code = code }
func (f *failingResponseWriter) Write([]byte) (int, error) { return 0, errors.New("connection reset") }

// TestRejectsNonGET: only GET is served. Other methods get 405 with Allow: GET and never record
// IgnitionReached — a HEAD would otherwise follow the serve path with its body suppressed.
func TestRejectsNonGET(t *testing.T) {
	g := NewWithT(t)
	key := testKey(t)
	owner := payloadstore.OwnerRef{Namespace: testNS, Name: testName}
	withPayload := seedStore(t, testGen)
	served := make(chan servedCall, 1)
	s := &Server{Namespace: testNS, Verifier: payloadstore.NewTokenVerifier(&key.PublicKey),
		Cached: withPayload, Uncached: withPayload,
		OnServed: func(_ context.Context, o payloadstore.OwnerRef, gen int64) { served <- servedCall{o, gen} }}

	tok := mintToken(t, key, owner)
	for _, method := range []string{http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := doReq(s.HandleIgnition, method, "/ignition/"+testName, tok, "2")
		g.Expect(rec.Code).To(Equal(http.StatusMethodNotAllowed))
		g.Expect(rec.Header().Get("Allow")).To(Equal(http.MethodGet))
	}
	g.Consistently(served).ShouldNot(Receive())
}

// TestWriteFailureDoesNotRecordIgnitionReached: when the payload body write fails the node did not
// receive the payload, so the OnServed (IgnitionReached) callback must not fire.
func TestWriteFailureDoesNotRecordIgnitionReached(t *testing.T) {
	g := NewWithT(t)
	key := testKey(t)
	owner := payloadstore.OwnerRef{Namespace: testNS, Name: testName}
	withPayload := seedStore(t, testGen)
	served := make(chan servedCall, 1)
	s := &Server{Namespace: testNS, Verifier: payloadstore.NewTokenVerifier(&key.PublicKey),
		Cached: withPayload, Uncached: withPayload,
		OnServed: func(_ context.Context, o payloadstore.OwnerRef, gen int64) { served <- servedCall{o, gen} }}

	fw := &failingResponseWriter{}
	doReqWriter(s.HandleIgnition, fw, http.MethodGet, "/ignition/"+testName, mintToken(t, key, owner), "2")

	g.Expect(fw.code).To(Equal(http.StatusOK)) // header is written before the body write fails
	g.Consistently(served).ShouldNot(Receive())
}
