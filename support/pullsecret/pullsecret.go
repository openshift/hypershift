// Package pullsecret provides helpers for combining the HostedCluster pull secret
// with an additional, provider-supplied credential used to reach the registry
// hosting the control plane release image.
//
// This exists to support running a hosted control plane from a private release
// image (for example a CVE-remediation build referenced by
// HostedCluster.spec.controlPlaneRelease) without exposing that credential to the
// data plane. The merged credentials are only ever held in memory at the point of
// a registry call; they are never written back into the cluster's pull-secret
// Secret, which HCCO propagates to the guest cluster and its nodes.
//
// # Scope: release payload lookups only
//
// The credential authenticates the in-process registry calls that operators make
// to read the control plane release payload and the metadata of images listed in
// it: resolving the payload, its component image references, and labels on the
// control plane operator image. It is not handed to the kubelet. Control plane
// Pods keep pulling their images with the cluster pull secret and whatever
// credentials the management cluster nodes already have, so when the component
// images themselves live in the private registry the management cluster must be
// able to pull them independently, for example through a node-level credential
// or a registry mirror configured on the management cluster.
package pullsecret

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/thirdparty/kubernetes/pkg/credentialprovider"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// ControlPlaneReleaseSecretName is the default name of the Secret, in the
	// hypershift-operator's own namespace, holding additional credentials for the
	// registry that hosts the control plane release image. The hypershift-operator
	// syncs a copy of it into each affected control plane namespace under
	// SyncedControlPlaneReleaseSecretName.
	//
	// The Secret is expected to be of type kubernetes.io/dockerconfigjson. Its
	// absence is the normal case and is never an error.
	//
	// A provider that cannot choose the Secret's name, for example because another
	// system generates it, points the hypershift-operator at a different name in
	// the same namespace with "hypershift install
	// --control-plane-release-pull-secret-name". Use SourceSecretKey rather than
	// this constant to locate the Secret. Only the hypershift-operator reads the
	// source Secret; every other consumer reads the synced copy, whose name does
	// not change.
	ControlPlaneReleaseSecretName = "control-plane-release-pull-secret"

	// SyncedControlPlaneReleaseSecretName is the name of the copy the
	// hypershift-operator keeps in the control plane namespace, where the control
	// plane operator, HCCO and other management-side controllers read it.
	//
	// It deliberately differs from ControlPlaneReleaseSecretName. Secrets referenced
	// from HostedCluster.spec.configuration are copied into the same namespace under
	// their original names, so a name shared with the source would let a tenant
	// Secret and the provider credential overwrite each other. Ownership is also
	// tracked with ManagedByLabel, so a Secret this package did not create under the
	// same name is never read, overwritten or deleted.
	SyncedControlPlaneReleaseSecretName = "hypershift-control-plane-release-pull-secret"

	// ManagedByLabel marks the synced copy as owned by the hypershift-operator. The
	// sync only updates or deletes Secrets carrying it, and readers in the control
	// plane namespace reject a Secret without it.
	ManagedByLabel = "hypershift.openshift.io/control-plane-release-pull-secret"

	// VeleroExcludeFromBackupLabel keeps the synced copy out of OADP/Velero
	// backups of the control plane namespace. The provider credential belongs to
	// the management cluster it was configured on; after a restore, the
	// hypershift-operator on the destination recreates the copy from its own
	// ControlPlaneReleaseSecretName.
	VeleroExcludeFromBackupLabel = "velero.io/exclude-from-backup"
)

// SourceSecretKey returns the key of the provider's control plane release pull
// secret in operatorNamespace. name is the Secret name the hypershift-operator was
// installed with; when it is empty the default, ControlPlaneReleaseSecretName, is
// used.
func SourceSecretKey(operatorNamespace, name string) types.NamespacedName {
	if name == "" {
		name = ControlPlaneReleaseSecretName
	}
	return types.NamespacedName{Namespace: operatorNamespace, Name: name}
}

// IsManagedCopy reports whether secret is a control plane release pull secret copy
// created by the hypershift-operator.
func IsManagedCopy(secret *corev1.Secret) bool {
	return secret.Labels[ManagedByLabel] == "true"
}

// Merge returns dockerconfigjson bytes containing the union of the auths in base
// and additional. On conflict the entry from additional wins.
//
// If additional is empty, base is returned unchanged.
//
// # Why additional wins, unlike the global pull secret controller
//
// globalps merges the other way round: the cluster's own credentials win, so a user
// supplied secret cannot override them. Its documentation suggests working around a
// collision by adding a repository scoped entry such as "quay.io/org/repo" alongside
// the host entry "quay.io".
//
// That workaround does not work here. The registry client this credential is handed
// to resolves credentials per registry *host*: the docker auth handler calls
// CredentialStore.Basic with the registry endpoint, so a repository scoped key is
// never matched and is silently ignored. Verified against a private repository on a
// host that also had a host level entry:
//
//	repository scoped key only   -> unauthorized
//	host entry + repository key  -> unauthorized (host entry is the only one consulted)
//	host entry carrying the credential -> succeeds
//
// So the only way to reach a private repository on a registry that already appears in
// the cluster pull secret is for the additional credential to replace the host entry.
// Keeping base precedence would make this package a no-op for exactly the case it
// exists to serve, for example a remediation repository hosted on quay.io.
//
// # Equivalent keys
//
// The registry credential store normalizes auth keys before indexing them: a
// missing scheme is assumed to be https, the scheme is dropped, and a leading /v1/
// or /v2/ path is ignored. "registry.example.com" and
// "https://registry.example.com/v1/" therefore name the same entry, and when both
// survive a merge the store holds two credentials for one key and picks between
// them in map iteration order. Merge compares keys in that normalized form, so an
// additional entry replaces every base entry that the store would treat as the same.
//
// # Scope of the override
//
// The override is narrow: these bytes are used only to resolve the control plane
// release image and the component images listed in that payload, they are never
// persisted, and the data plane continues to resolve its own release with the
// unmodified cluster pull secret. The trade-off to be aware of is that for the
// duration of a control plane release lookup, the provider credential is used for
// every image on that host, including any component image the payload references
// there.
//
// The auth entries are handled as opaque values rather than being decoded into
// credentialprovider.DockerConfigJSON, because that type does not round-trip the
// "auth" field.
func Merge(base, additional []byte) ([]byte, error) {
	if len(additional) == 0 {
		return base, nil
	}

	baseAuths, err := authsOf(base)
	if err != nil {
		return nil, fmt.Errorf("base pull secret: %w", err)
	}
	additionalAuths, err := authsOf(additional)
	if err != nil {
		return nil, fmt.Errorf("additional pull secret: %w", err)
	}

	// Drop every base entry the credential store would treat as the same key as an
	// additional entry, so the additional credential is the only one left for it.
	overridden := make(map[string]bool, len(additionalAuths))
	for key := range additionalAuths {
		overridden[credentialStoreKey(key)] = true
	}
	merged := make(map[string]any, len(baseAuths)+len(additionalAuths))
	for key, entry := range baseAuths {
		if !overridden[credentialStoreKey(key)] {
			merged[key] = entry
		}
	}
	// Additional entries take precedence on conflict. See the doc comment for why
	// this is the opposite of the global pull secret merge.
	maps.Copy(merged, additionalAuths)

	mergedBytes, err := json.Marshal(map[string]any{"auths": merged})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal merged pull secret: %w", err)
	}
	return mergedBytes, nil
}

// credentialStoreKey returns the key the registry credential store indexes an
// auths entry under. It mirrors BasicDockerKeyring.Add in
// support/thirdparty/kubernetes/pkg/credentialprovider. A key that cannot be
// parsed is returned unchanged, which keeps it distinct from every other key; the
// credential store skips such keys anyway.
func credentialStoreKey(key string) string {
	value := key
	if !strings.HasPrefix(value, "https://") && !strings.HasPrefix(value, "http://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return key
	}
	path := parsed.Path
	if strings.HasPrefix(path, "/v2/") || strings.HasPrefix(path, "/v1/") {
		path = path[3:]
	}
	if path == "" || path == "/" {
		return parsed.Host
	}
	return parsed.Host + path
}

// ForControlPlaneRelease returns base merged with the provider's control plane
// release pull secret at source, see SourceSecretKey.
//
// When that Secret does not exist, base is returned unchanged. Callers should only
// invoke this for lookups of the control plane release image; passing the result to
// a data plane lookup would widen the credentials available to it.
func ForControlPlaneRelease(ctx context.Context, c client.Reader, source types.NamespacedName, base []byte) ([]byte, error) {
	additional, err := sourceBytes(ctx, c, source)
	if err != nil {
		return nil, err
	}
	return Merge(base, additional)
}

// ForHostedCluster returns the credentials the hypershift-operator should use when
// resolving hc's control plane release image. The additional credential is read from
// source, the provider Secret in the operator's own namespace, see SourceSecretKey.
//
// When hc does not pin a distinct control plane release, base is returned unchanged:
// the release being resolved is then the data plane release, which must not gain
// access to the additional credential.
func ForHostedCluster(ctx context.Context, c client.Reader, hc *hyperv1.HostedCluster, source types.NamespacedName, base []byte) ([]byte, error) {
	if hc.Spec.ControlPlaneRelease == nil {
		return base, nil
	}
	return ForControlPlaneRelease(ctx, c, source, base)
}

// ForHostedClusterInNamespace is like ForHostedCluster but reads the synced copy of
// the additional credential from controlPlaneNamespace.
//
// Callers pass the control plane namespace explicitly rather than deriving it from
// hc, because some callers (AutoNode) hand in a synthetic HostedCluster whose
// namespace already is the control plane namespace.
//
// Use this from controllers that only run once the control plane namespace exists.
// The hypershift-operator's own HostedCluster reconcile must use ForHostedCluster
// instead, because it resolves the control plane operator image before that
// namespace has been created.
func ForHostedClusterInNamespace(ctx context.Context, c client.Reader, hc *hyperv1.HostedCluster, controlPlaneNamespace string, base []byte) ([]byte, error) {
	if hc.Spec.ControlPlaneRelease == nil {
		return base, nil
	}
	return forSyncedCopy(ctx, c, controlPlaneNamespace, base)
}

// ForHostedControlPlane returns the credentials an in-namespace control plane
// component should use when resolving hcp's control plane release image. The
// additional credential is read from the synced copy in the control plane
// namespace.
//
// When hcp does not pin a distinct control plane release, base is returned unchanged.
func ForHostedControlPlane(ctx context.Context, c client.Reader, hcp *hyperv1.HostedControlPlane, base []byte) ([]byte, error) {
	if hcp.Spec.ControlPlaneReleaseImage == nil {
		return base, nil
	}
	return forSyncedCopy(ctx, c, hcp.Namespace, base)
}

// forSyncedCopy merges base with the synced copy of the credential in
// controlPlaneNamespace.
func forSyncedCopy(ctx context.Context, c client.Reader, controlPlaneNamespace string, base []byte) ([]byte, error) {
	secret, err := getSecret(ctx, c, types.NamespacedName{Namespace: controlPlaneNamespace, Name: SyncedControlPlaneReleaseSecretName})
	if err != nil || secret == nil {
		return base, err
	}
	if !IsManagedCopy(secret) {
		return nil, fmt.Errorf("secret %s/%s is not managed by the hypershift-operator: it lacks the %s=true label", secret.Namespace, secret.Name, ManagedByLabel)
	}
	additional, err := dockerConfigJSON(secret)
	if err != nil {
		return nil, err
	}
	return Merge(base, additional)
}

// sourceBytes reads the dockerconfigjson payload of the provider Secret at source,
// returning nil when the Secret is absent.
func sourceBytes(ctx context.Context, c client.Reader, source types.NamespacedName) ([]byte, error) {
	secret, err := getSecret(ctx, c, source)
	if err != nil || secret == nil {
		return nil, err
	}
	return dockerConfigJSON(secret)
}

// getSecret returns the Secret at key, or nil when it does not exist.
func getSecret(ctx context.Context, c client.Reader, key types.NamespacedName) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	if err := c.Get(ctx, key, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get secret %s: %w", key, err)
	}
	return secret, nil
}

func dockerConfigJSON(secret *corev1.Secret) ([]byte, error) {
	data, ok := secret.Data[corev1.DockerConfigJsonKey]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s is missing the %s key", secret.Namespace, secret.Name, corev1.DockerConfigJsonKey)
	}
	return data, nil
}

// Validate reports an error when dockerConfigJSON cannot be used as a control plane
// release pull secret. It applies the parsing of Merge, which every reader of the
// credential goes through, and that of the registry clients, which decode each entry of
// the merged payload: a single entry they cannot decode fails every lookup made with it.
// A payload that passes is one those readers accept. Kubernetes accepts payloads, such
// as {"auths":[]} or an entry whose auth field is not base64(username:password), that
// they do not.
func Validate(dockerConfigJSON []byte) error {
	if _, err := authsOf(dockerConfigJSON); err != nil {
		return err
	}
	if _, err := credentialprovider.ReadDockerConfigJSONFileFromBytes(dockerConfigJSON); err != nil {
		return fmt.Errorf("invalid auths entry: %w", err)
	}
	return nil
}

// authsOf extracts the "auths" object from a dockerconfigjson payload.
func authsOf(pullSecret []byte) (map[string]any, error) {
	var parsed map[string]any
	if err := json.Unmarshal(pullSecret, &parsed); err != nil {
		return nil, fmt.Errorf("invalid format: %w", err)
	}
	raw, ok := parsed["auths"]
	if !ok {
		return nil, fmt.Errorf("missing %q key", "auths")
	}
	auths, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%q is not an object", "auths")
	}
	return auths, nil
}
