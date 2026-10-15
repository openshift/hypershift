package ignitionpayload

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"

	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Label and annotation keys for SecretBackedStore.
const (
	LabelOwnerNamespace = "hypershift.openshift.io/ignition-payload-owner-namespace"
	LabelOwnerName      = "hypershift.openshift.io/ignition-payload-owner-name"
	LabelIdentityHash   = "hypershift.openshift.io/ignition-payload-identity-hash"

	// Exact values stored in annotations (no length limit) are the source of truth for
	// equality matching; labels carry a fixed-length digest so List selectors stay
	// effective regardless of value length.
	AnnotationOwnerNamespaceExact = "hypershift.openshift.io/ignition-payload-owner-namespace-exact"
	AnnotationOwnerNameExact      = "hypershift.openshift.io/ignition-payload-owner-name-exact"
	AnnotationIdentityHashExact   = "hypershift.openshift.io/ignition-payload-identity-hash-exact"

	// payloadDataKey holds the gzip-compressed payload bytes.
	payloadDataKey = "payload"
	// tokenDataKey holds the opaque bearer token. The token is kept in Secret data
	// (never in the Secret name or a label) so it does not reach audit logs.
	tokenDataKey = "token"
)

// SecretBackedStore is a Secret-backed PayloadStore.
type SecretBackedStore struct {
	client    client.Client
	namespace string
}

// NewSecretBackedStore creates a Secret-backed PayloadStore. It builds its own uncached
// client from cfg so reads go straight to the API server regardless of any informer
// cache state — the stateless, scaled serving tier must observe payloads the generator
// just wrote, and the store also performs writes (so a read-only API reader is
// insufficient).
func NewSecretBackedStore(cfg *rest.Config, namespace string) (*SecretBackedStore, error) {
	c, err := client.New(cfg, client.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to build uncached client for payload store: %w", err)
	}
	return newSecretBackedStore(c, namespace), nil
}

// newSecretBackedStore wraps an existing client. It exists so tests can inject a fake
// client; production code must use NewSecretBackedStore to obtain an uncached client.
func newSecretBackedStore(c client.Client, namespace string) *SecretBackedStore {
	return &SecretBackedStore{
		client:    c,
		namespace: namespace,
	}
}

func (s *SecretBackedStore) Put(ctx context.Context, owner OwnerRef, token, identityHash string, payload []byte) error {
	// Secret.Data is []byte; the API server base64-encodes it on the wire. We store raw
	// gzip bytes (no extra base64) to maximize headroom under the ~1 MiB object limit.
	compressed, err := util.Compress(payload)
	if err != nil {
		return fmt.Errorf("failed to compress payload: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName(token),
			Namespace: s.namespace,
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, s.client, secret, func() error {
		if secret.Labels == nil {
			secret.Labels = make(map[string]string)
		}
		if secret.Annotations == nil {
			secret.Annotations = make(map[string]string)
		}
		if secret.Data == nil {
			secret.Data = make(map[string][]byte)
		}

		// Exact values in annotations are the source of truth for equality matching.
		secret.Annotations[AnnotationOwnerNamespaceExact] = owner.Namespace
		secret.Annotations[AnnotationOwnerNameExact] = owner.Name
		secret.Annotations[AnnotationIdentityHashExact] = identityHash

		// Labels carry a fixed-length digest so List selectors work even when the value
		// exceeds the 63-character label limit (owner name, identity hash).
		secret.Labels[LabelOwnerNamespace] = labelSafeDigest(owner.Namespace)
		secret.Labels[LabelOwnerName] = labelSafeDigest(owner.Name)
		secret.Labels[LabelIdentityHash] = labelSafeDigest(identityHash)

		// The token is a bearer secret: keep it only in Secret data.
		secret.Data[tokenDataKey] = []byte(token)
		secret.Data[payloadDataKey] = compressed.Bytes()
		return nil
	})
	return err
}

func (s *SecretBackedStore) Get(ctx context.Context, token string) (payload []byte, owner OwnerRef, err error) {
	secret := &corev1.Secret{}
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: secretName(token)}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, OwnerRef{}, ErrNotFound
		}
		return nil, OwnerRef{}, err
	}

	decompressed, err := util.Decompress(secret.Data[payloadDataKey])
	if err != nil {
		return nil, OwnerRef{}, fmt.Errorf("failed to decompress payload: %w", err)
	}

	owner = OwnerRef{
		Namespace: secret.Annotations[AnnotationOwnerNamespaceExact],
		Name:      secret.Annotations[AnnotationOwnerNameExact],
	}
	return decompressed.Bytes(), owner, nil
}

func (s *SecretBackedStore) Delete(ctx context.Context, token string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName(token),
			Namespace: s.namespace,
		},
	}
	if err := s.client.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (s *SecretBackedStore) FindByIdentity(ctx context.Context, owner OwnerRef, identityHash string) (token string, err error) {
	var secrets corev1.SecretList
	selector := client.MatchingLabels{
		LabelOwnerNamespace: labelSafeDigest(owner.Namespace),
		LabelOwnerName:      labelSafeDigest(owner.Name),
		LabelIdentityHash:   labelSafeDigest(identityHash),
	}
	if err := s.client.List(ctx, &secrets, client.InNamespace(s.namespace), selector); err != nil {
		return "", err
	}

	// Confirm against the exact annotation values (digests could in principle collide).
	for _, sec := range secrets.Items {
		if sec.Annotations[AnnotationOwnerNamespaceExact] == owner.Namespace &&
			sec.Annotations[AnnotationOwnerNameExact] == owner.Name &&
			sec.Annotations[AnnotationIdentityHashExact] == identityHash {
			return string(sec.Data[tokenDataKey]), nil
		}
	}

	return "", ErrNotFound
}

func (s *SecretBackedStore) ListByOwner(ctx context.Context, owner OwnerRef) (tokens []string, err error) {
	var secrets corev1.SecretList
	selector := client.MatchingLabels{
		LabelOwnerNamespace: labelSafeDigest(owner.Namespace),
		LabelOwnerName:      labelSafeDigest(owner.Name),
	}
	if err := s.client.List(ctx, &secrets, client.InNamespace(s.namespace), selector); err != nil {
		return nil, err
	}

	// Confirm against the exact annotation values (digests could in principle collide).
	result := []string{}
	for _, sec := range secrets.Items {
		if sec.Annotations[AnnotationOwnerNamespaceExact] == owner.Namespace &&
			sec.Annotations[AnnotationOwnerNameExact] == owner.Name {
			result = append(result, string(sec.Data[tokenDataKey]))
		}
	}

	return result, nil
}

func secretName(token string) string {
	// Derive the name from a non-reversible digest of the token so the bearer token
	// never appears in a resource name (resource names reach audit logs even at the
	// Metadata level). Hashing also makes Get robust to arbitrary/untrusted input: any
	// token hashes to a valid name, so a bad token yields ErrNotFound rather than an
	// "invalid resource name" API error.
	return fmt.Sprintf("ignition-payload-%s", labelSafeDigest(token))
}

// labelSafeDigest returns a fixed-length, label- and DNS-name-safe digest of s: the
// lowercase, unpadded base32 encoding of its SHA-256 (52 characters). It is used for
// Secret names and for label values that would otherwise exceed Kubernetes' 63-character
// label limit (owner name/namespace, identity hash) or leak a secret (the token, in the
// Secret name).
func labelSafeDigest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))
}
