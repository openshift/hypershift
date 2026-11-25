package ignitionpayload

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strconv"
	"strings"

	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Label and annotation keys for SecretBackedStore.
const (
	LabelOwnerNamespace = "hypershift.openshift.io/ignition-payload-owner-namespace"
	LabelOwnerName      = "hypershift.openshift.io/ignition-payload-owner-name"

	// Exact values stored in annotations (no length limit) are the source of truth for
	// equality matching; labels carry a fixed-length digest so List selectors stay effective
	// regardless of value length.
	AnnotationOwnerNamespaceExact = "hypershift.openshift.io/ignition-payload-owner-namespace-exact"
	AnnotationOwnerNameExact      = "hypershift.openshift.io/ignition-payload-owner-name-exact"
	// AnnotationRolloutGeneration records the rollout generation this entry serves; it is the
	// second half of the store key, recovered by ListByOwner for the sweep/cleanup paths.
	AnnotationRolloutGeneration = "hypershift.openshift.io/ignition-payload-rollout-generation"
	// AnnotationConfigHash records the payload-identity hash of the stored bytes, so the
	// generator can skip regeneration when it already matches (idempotent generation).
	AnnotationConfigHash = "hypershift.openshift.io/ignition-payload-config-hash"

	// payloadDataKey holds the gzip-compressed payload bytes. The bearer token is never stored
	// here (or anywhere in the store) — it lives only in the generator-authored userdata Secret.
	payloadDataKey = "payload"
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

// NewSecretBackedStoreForClient wraps an existing client as a SecretBackedStore. It is for callers
// that already hold a suitable client — notably tests, which inject a fake client to seed and read
// payloads through the same code paths production uses. Production generator code must use
// NewSecretBackedStore instead, so its reads and writes go through a dedicated uncached client
// regardless of informer-cache state.
func NewSecretBackedStoreForClient(c client.Client, namespace string) *SecretBackedStore {
	return newSecretBackedStore(c, namespace)
}

// newSecretBackedStore wraps an existing client. It exists so tests can inject a fake
// client; production code must use NewSecretBackedStore to obtain an uncached client.
func newSecretBackedStore(c client.Client, namespace string) *SecretBackedStore {
	return &SecretBackedStore{
		client:    c,
		namespace: namespace,
	}
}

func (s *SecretBackedStore) Put(ctx context.Context, owner OwnerRef, rolloutGeneration int64, configHash string, payload []byte) error {
	// Secret.Data is []byte; the API server base64-encodes it on the wire. We store raw
	// gzip bytes (no extra base64) to maximize headroom under the ~1 MiB object limit.
	compressed, err := util.Compress(payload)
	if err != nil {
		return fmt.Errorf("failed to compress payload: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName(owner, rolloutGeneration),
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
		secret.Annotations[AnnotationRolloutGeneration] = strconv.FormatInt(rolloutGeneration, 10)
		secret.Annotations[AnnotationConfigHash] = configHash

		// Labels carry a fixed-length digest so List selectors work even when the value
		// exceeds the 63-character label limit (owner name/namespace).
		secret.Labels[LabelOwnerNamespace] = labelSafeDigest(owner.Namespace)
		secret.Labels[LabelOwnerName] = labelSafeDigest(owner.Name)

		secret.Data[payloadDataKey] = compressed.Bytes()
		return nil
	})
	return err
}

func (s *SecretBackedStore) Get(ctx context.Context, owner OwnerRef, rolloutGeneration int64) (payload []byte, configHash string, err error) {
	return readPayload(ctx, s.client, s.namespace, owner, rolloutGeneration)
}

// secretBackedReader is the read-only view of the Secret-backed store, satisfying PayloadReader from a
// bare client.Reader. The stateless serving tier uses it with its uncached APIReader so every serve
// observes the latest write (an in-place refresh or a deletion) rather than lagging informer state,
// without needing the write-capable client the full store holds.
type secretBackedReader struct {
	reader    client.Reader
	namespace string
}

// NewSecretBackedReader returns a read-only PayloadReader backed by reader for payloads in namespace.
func NewSecretBackedReader(reader client.Reader, namespace string) PayloadReader {
	return &secretBackedReader{reader: reader, namespace: namespace}
}

func (s *secretBackedReader) Get(ctx context.Context, owner OwnerRef, rolloutGeneration int64) (payload []byte, configHash string, err error) {
	return readPayload(ctx, s.reader, s.namespace, owner, rolloutGeneration)
}

// readPayload reads the payload bytes and recorded configHash for (owner, rolloutGeneration) from
// namespace using any client.Reader. Returns ErrNotFound if the backing Secret does not exist.
func readPayload(ctx context.Context, r client.Reader, namespace string, owner OwnerRef, rolloutGeneration int64) (payload []byte, configHash string, err error) {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: secretName(owner, rolloutGeneration)}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}

	decompressed, err := util.Decompress(secret.Data[payloadDataKey])
	if err != nil {
		return nil, "", fmt.Errorf("failed to decompress payload: %w", err)
	}

	return decompressed.Bytes(), secret.Annotations[AnnotationConfigHash], nil
}

func (s *SecretBackedStore) Delete(ctx context.Context, owner OwnerRef, rolloutGeneration int64) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName(owner, rolloutGeneration),
			Namespace: s.namespace,
		},
	}
	if err := s.client.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (s *SecretBackedStore) ListByOwner(ctx context.Context, owner OwnerRef) (rolloutGenerations []int64, err error) {
	var secrets corev1.SecretList
	selector := client.MatchingLabels{
		LabelOwnerNamespace: labelSafeDigest(owner.Namespace),
		LabelOwnerName:      labelSafeDigest(owner.Name),
	}
	if err := s.client.List(ctx, &secrets, client.InNamespace(s.namespace), selector); err != nil {
		return nil, err
	}

	// Confirm against the exact annotation values (digests could in principle collide).
	result := []int64{}
	for _, sec := range secrets.Items {
		if sec.Annotations[AnnotationOwnerNamespaceExact] != owner.Namespace ||
			sec.Annotations[AnnotationOwnerNameExact] != owner.Name {
			continue
		}
		gen, err := strconv.ParseInt(sec.Annotations[AnnotationRolloutGeneration], 10, 64)
		if err != nil {
			// Skip rather than fail the whole list: a single malformed entry must not hide
			// the parseable generations the sweep/cleanup paths rely on.
			ctrl.LoggerFrom(ctx).Error(err, "skipping store entry with invalid rollout-generation annotation",
				"secret", sec.Name, "value", sec.Annotations[AnnotationRolloutGeneration])
			continue
		}
		result = append(result, gen)
	}

	return result, nil
}

// secretName derives the store Secret name from a non-reversible digest of the rollout identity
// (owner + rolloutGeneration). Deriving it from a digest keeps the name fixed-length and
// DNS-safe regardless of owner-name length, and keeps the owner namespace/name out of the
// resource name (resource names reach audit logs even at the Metadata level).
func secretName(owner OwnerRef, rolloutGeneration int64) string {
	return fmt.Sprintf("ignition-payload-%s", labelSafeDigest(fmt.Sprintf("%s/%s/%d", owner.Namespace, owner.Name, rolloutGeneration)))
}

// labelSafeDigest returns a fixed-length, label- and DNS-name-safe digest of s: the
// lowercase, unpadded base32 encoding of its SHA-256 (52 characters). It is used for
// Secret names and for label values that would otherwise exceed Kubernetes' 63-character
// label limit (owner name/namespace).
func labelSafeDigest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))
}
