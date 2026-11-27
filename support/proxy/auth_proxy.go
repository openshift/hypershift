package proxy

import (
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	operatorv1 "github.com/openshift/api/operator/v1"

	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	// AuthProxyTrustedCAConfigMapName is the fixed name of the ConfigMap that
	// holds the auth proxy trusted CA bundle in the control plane namespace.
	// The HyperShift operator copies the user-referenced source bundle under
	// this name, matching the operand's v4-0-config-system-* convention (see
	// the oauth session secrets). A fixed destination name also lets the
	// control plane component machinery exclude the ConfigMap from the pod
	// config hash, so a CA rotation hot-reloads instead of rolling the pod.
	AuthProxyTrustedCAConfigMapName = "v4-0-config-system-auth-proxy-ca"

	// AuthProxyTrustedCAMountPath is the directory the auth proxy trusted CA
	// ConfigMap is mounted at on the external-oidc-webhook container.
	AuthProxyTrustedCAMountPath = "/var/config/system/configmaps/" + AuthProxyTrustedCAConfigMapName

	// AuthProxyTrustedCAFilePath is the file the external-oidc-webhook reads
	// the auth proxy trusted CA bundle from: the ca-bundle.crt key of the
	// mounted ConfigMap directory.
	AuthProxyTrustedCAFilePath = AuthProxyTrustedCAMountPath + "/ca-bundle.crt"
)

// AuthProxyOverride returns the auth-scoped proxy override for the external
// OIDC authentication components: spec.operatorConfiguration.authentication.proxy
// on the HostedControlPlane, or nil when it is unset.
//
// The override is the raw operatorv1.AuthenticationProxyConfig. Its NoProxy is
// a []string; consumers that need a comma-separated string join it themselves
// (the guest Console expects the slice shape and must never receive a joined
// value).
//
// The override is not merged with any other proxy source. When it is unset the
// external-oidc-webhook falls back to the management cluster proxy from the
// control plane operator process environment, so callers must not substitute
// the cluster-wide spec.configuration.proxy here.
func AuthProxyOverride(hcp *hyperv1.HostedControlPlane) *operatorv1.AuthenticationProxyConfig {
	if hcp == nil {
		return nil
	}

	if oc := hcp.Spec.OperatorConfiguration; oc != nil {
		if authProxy := oc.Authentication.Proxy; !isZeroAuthProxy(authProxy) {
			return &authProxy
		}
	}

	return nil
}

// isZeroAuthProxy reports whether the override carries no proxy configuration.
func isZeroAuthProxy(p operatorv1.AuthenticationProxyConfig) bool {
	return p.HTTPProxy == "" && p.HTTPSProxy == ""
}

// AuthProxyNoProxy returns the NO_PROXY value the external-oidc-webhook uses
// on the component-proxy leg: the component's own noProxy entries, the
// hostname defaults in both dot forms, the kube-apiserver Service (the only
// in-HCP endpoint the webhook dials directly, for TokenReview), and the
// caller-supplied internal CIDRs.
//
// Nothing is inherited from the management cluster's NO_PROXY: the component
// leg builds its bypass list from scratch. The service IP is not baked either:
// KUBERNETES_SERVICE_HOST on the control plane operator resolves in the
// management cluster, not in the hosted cluster.
//
// The internal CIDRs are part of the baseline the webhook keeps on both proxy
// legs: internal addresses never traverse a proxy, so they are bypassed even
// when the component proxy replaces the management cluster's. They are
// unreachable from the management-cluster network in the common case, but the
// baseline must not depend on routing assumptions. The caller supplies them
// so both proxy legs share one source.
//
// etcd and the audit webhook Service are absent because the hosted
// external-oidc-webhook runs the oauth-apiserver external-oidc command with
// no etcd and no audit webhook; those endpoints belong to the standalone
// oauth-apiserver shape.
func AuthProxyNoProxy(componentNoProxy []string, internalCIDRs ...string) string {
	set := sets.New(
		"127.0.0.1",
		"localhost",
		"localhost.",
		".svc",
		".svc.",
		".cluster.local",
		".cluster.local.",
		"kube-apiserver",
	)
	set.Insert(componentNoProxy...)
	set.Insert(internalCIDRs...)
	// Sorted so the value is deterministic: it feeds the pod template and a
	// varying order would roll the Deployment on every reconcile.
	return strings.Join(sets.List(set), ",")
}
