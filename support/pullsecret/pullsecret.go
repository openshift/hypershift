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
package pullsecret

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/pkg/manifests"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ControlPlaneReleaseSecretName is the well-known name of the Secret holding
// additional credentials for the registry that hosts the control plane release
// image. The hypershift-operator reads it from its own namespace and syncs a copy
// into each affected control plane namespace, where the control plane operator and
// the hosted cluster config operator read it.
//
// The Secret is expected to be of type kubernetes.io/dockerconfigjson. Its absence
// is the normal case and is never an error.
const ControlPlaneReleaseSecretName = "control-plane-release-pull-secret"

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

	merged := make(map[string]any, len(baseAuths)+len(additionalAuths))
	maps.Copy(merged, baseAuths)
	// Additional entries are applied last so they take precedence on conflict. See
	// the doc comment for why this is the opposite of the global pull secret merge.
	maps.Copy(merged, additionalAuths)

	mergedBytes, err := json.Marshal(map[string]any{"auths": merged})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal merged pull secret: %w", err)
	}
	return mergedBytes, nil
}

// ForControlPlaneRelease returns base merged with the additional control plane
// release pull secret held in the given namespace.
//
// When that Secret does not exist, base is returned unchanged. Callers should only
// invoke this for lookups of the control plane release image; passing the result to
// a data plane lookup would widen the credentials available to it.
func ForControlPlaneRelease(ctx context.Context, c client.Client, namespace string, base []byte) ([]byte, error) {
	additional, err := additionalBytes(ctx, c, namespace)
	if err != nil {
		return nil, err
	}
	return Merge(base, additional)
}

// ForHostedCluster returns the credentials the hypershift-operator should use when
// resolving hc's control plane release image. The additional credential is read from
// the operator's own namespace.
//
// When hc does not pin a distinct control plane release, base is returned unchanged:
// the release being resolved is then the data plane release, which must not gain
// access to the additional credential.
func ForHostedCluster(ctx context.Context, c client.Client, hc *hyperv1.HostedCluster, operatorNamespace string, base []byte) ([]byte, error) {
	if hc.Spec.ControlPlaneRelease == nil {
		return base, nil
	}
	return ForControlPlaneRelease(ctx, c, operatorNamespace, base)
}

// ForHostedClusterControlPlaneNamespace is like ForHostedCluster but reads the
// additional credential from the cluster's control plane namespace, where the
// hypershift-operator syncs a copy of it.
//
// Use this from controllers that only run once the control plane namespace exists.
// The hypershift-operator's own HostedCluster reconcile must use ForHostedCluster
// instead, because it resolves the control plane operator image before that
// namespace has been created.
func ForHostedClusterControlPlaneNamespace(ctx context.Context, c client.Client, hc *hyperv1.HostedCluster, base []byte) ([]byte, error) {
	if hc.Spec.ControlPlaneRelease == nil {
		return base, nil
	}
	namespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
	return ForControlPlaneRelease(ctx, c, namespace, base)
}

// ForHostedControlPlane returns the credentials an in-namespace control plane
// component should use when resolving hcp's control plane release image. The
// additional credential is read from the control plane namespace, where the
// hypershift-operator syncs it.
//
// When hcp does not pin a distinct control plane release, base is returned unchanged.
func ForHostedControlPlane(ctx context.Context, c client.Client, hcp *hyperv1.HostedControlPlane, base []byte) ([]byte, error) {
	if hcp.Spec.ControlPlaneReleaseImage == nil {
		return base, nil
	}
	return ForControlPlaneRelease(ctx, c, hcp.Namespace, base)
}

// additionalBytes reads the dockerconfigjson payload of the well-known Secret in
// namespace, returning nil when the Secret is absent.
func additionalBytes(ctx context.Context, c client.Client, namespace string) ([]byte, error) {
	secret := &corev1.Secret{}
	key := types.NamespacedName{Namespace: namespace, Name: ControlPlaneReleaseSecretName}
	if err := c.Get(ctx, key, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get secret %s: %w", key, err)
	}

	data, ok := secret.Data[corev1.DockerConfigJsonKey]
	if !ok {
		return nil, fmt.Errorf("secret %s is missing the %s key", key, corev1.DockerConfigJsonKey)
	}
	return data, nil
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
