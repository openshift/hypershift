package ignitionpayload

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Label and annotation keys for SecretBackedStore.
const (
	LabelOwnerNamespace = "hypershift.openshift.io/ignition-payload-owner-namespace"
	LabelOwnerName      = "hypershift.openshift.io/ignition-payload-owner-name"
	LabelIdentityHash   = "hypershift.openshift.io/ignition-payload-identity-hash"
	LabelToken          = "hypershift.openshift.io/ignition-payload-token"

	// Exact values stored in annotations (no length limit).
	AnnotationOwnerNamespaceExact = "hypershift.openshift.io/ignition-payload-owner-namespace-exact"
	AnnotationOwnerNameExact      = "hypershift.openshift.io/ignition-payload-owner-name-exact"
	AnnotationIdentityHashExact   = "hypershift.openshift.io/ignition-payload-identity-hash-exact"

	// maxLabelValueLength is the maximum length of a Kubernetes label value.
	maxLabelValueLength = 63
)

// SecretBackedStore is a Secret-backed PayloadStore.
type SecretBackedStore struct {
	client    client.Client
	namespace string
}

// NewSecretBackedStore creates a new Secret-backed PayloadStore.
func NewSecretBackedStore(c client.Client, namespace string) *SecretBackedStore {
	return &SecretBackedStore{
		client:    c,
		namespace: namespace,
	}
}

func (s *SecretBackedStore) Put(ctx context.Context, owner OwnerRef, token, identityHash string, payload []byte) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName(token),
			Namespace: s.namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, s.client, secret, func() error {
		if secret.Labels == nil {
			secret.Labels = make(map[string]string)
		}
		if secret.Annotations == nil {
			secret.Annotations = make(map[string]string)
		}
		if secret.Data == nil {
			secret.Data = make(map[string][]byte)
		}

		// Always store exact values in annotations.
		secret.Annotations[AnnotationOwnerNamespaceExact] = owner.Namespace
		secret.Annotations[AnnotationOwnerNameExact] = owner.Name
		secret.Annotations[AnnotationIdentityHashExact] = identityHash

		// Store in labels when they fit (≤maxLabelValueLength chars).
		// Clear the label if it doesn't fit to avoid stale values on overwrite.
		if len(owner.Namespace) <= maxLabelValueLength {
			secret.Labels[LabelOwnerNamespace] = owner.Namespace
		} else {
			delete(secret.Labels, LabelOwnerNamespace)
		}
		if len(owner.Name) <= maxLabelValueLength {
			secret.Labels[LabelOwnerName] = owner.Name
		} else {
			delete(secret.Labels, LabelOwnerName)
		}
		if len(identityHash) <= maxLabelValueLength {
			secret.Labels[LabelIdentityHash] = identityHash
		} else {
			delete(secret.Labels, LabelIdentityHash)
		}
		secret.Labels[LabelToken] = token

		secret.Data["payload"] = payload
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

	owner = OwnerRef{
		Namespace: secret.Annotations[AnnotationOwnerNamespaceExact],
		Name:      secret.Annotations[AnnotationOwnerNameExact],
	}
	return secret.Data["payload"], owner, nil
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
	var listOpts []client.ListOption

	// Build label selector: owner namespace (if it fits) + owner name (if it fits).
	selector := client.MatchingLabels{}
	if len(owner.Namespace) <= maxLabelValueLength {
		selector[LabelOwnerNamespace] = owner.Namespace
	}
	if len(owner.Name) <= maxLabelValueLength {
		selector[LabelOwnerName] = owner.Name
	}

	// If identity fits in a label, add it to the selector.
	if len(identityHash) <= maxLabelValueLength {
		selector[LabelIdentityHash] = identityHash
	}

	listOpts = append(listOpts, client.InNamespace(s.namespace), selector)

	if err := s.client.List(ctx, &secrets, listOpts...); err != nil {
		return "", err
	}

	// Filter client-side by exact annotation values.
	for _, sec := range secrets.Items {
		if sec.Annotations[AnnotationOwnerNamespaceExact] == owner.Namespace &&
			sec.Annotations[AnnotationOwnerNameExact] == owner.Name &&
			sec.Annotations[AnnotationIdentityHashExact] == identityHash {
			return sec.Labels[LabelToken], nil
		}
	}

	return "", ErrNotFound
}

func (s *SecretBackedStore) ListByOwner(ctx context.Context, owner OwnerRef) (tokens []string, err error) {
	var secrets corev1.SecretList
	var listOpts []client.ListOption

	// Build label selector: owner namespace (if it fits) + owner name (if it fits).
	selector := client.MatchingLabels{}
	if len(owner.Namespace) <= maxLabelValueLength {
		selector[LabelOwnerNamespace] = owner.Namespace
	}
	if len(owner.Name) <= maxLabelValueLength {
		selector[LabelOwnerName] = owner.Name
	}

	listOpts = append(listOpts, client.InNamespace(s.namespace), selector)

	if err := s.client.List(ctx, &secrets, listOpts...); err != nil {
		return nil, err
	}

	// Filter client-side by exact annotation values.
	result := []string{}
	for _, sec := range secrets.Items {
		if sec.Annotations[AnnotationOwnerNamespaceExact] == owner.Namespace &&
			sec.Annotations[AnnotationOwnerNameExact] == owner.Name {
			result = append(result, sec.Labels[LabelToken])
		}
	}

	return result, nil
}

func secretName(token string) string {
	return fmt.Sprintf("ignition-payload-%s", token)
}
