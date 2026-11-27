package console

import (
	"slices"

	operatorv1 "github.com/openshift/api/operator/v1"
)

// DesiredConsoleAuthProxy returns the console-scoped auth proxy the guest
// Console operator CR must carry for the given auth-scoped proxy configuration.
//
// It returns the value instead of mutating the CR because the caller needs the
// desired value after the write too: the API server returns the stored object
// into the written one, so a pruned field leaves nothing to compare against.
//
// The value applies ONLY to console-operator's OIDC login clients (discovery,
// JWKS retrieval, code exchange, token refresh); no other guest component reads
// spec.authProxy. guestTrustedCAName is the name of the CA bundle ConfigMap
// already copied into the guest openshift-config namespace, or empty when the
// auth proxy configuration carries no trustedCA.
//
// The configuration's NoProxy is a []string and is copied as-is; it must NOT be
// joined into a comma-separated string (that is the shape expected by
// configv1.ProxySpec, not by operatorv1.ConsoleAuthProxyConfig).
func DesiredConsoleAuthProxy(authProxyConfig operatorv1.AuthenticationProxyConfig, guestTrustedCAName string) operatorv1.ConsoleAuthProxyConfig {
	desired := operatorv1.ConsoleAuthProxyConfig{
		HTTPProxy:  authProxyConfig.HTTPProxy,
		HTTPSProxy: authProxyConfig.HTTPSProxy,
		NoProxy:    slices.Clone(authProxyConfig.NoProxy),
	}
	if guestTrustedCAName != "" {
		desired.TrustedCA = operatorv1.ConsoleAuthProxyTrustedCAConfigMapReference{
			Name: guestTrustedCAName,
		}
	}
	return desired
}

// ClearConsoleAuthProxy removes any previously-set console auth proxy so that
// console-operator falls back to the cluster-wide proxy.
func ClearConsoleAuthProxy(console *operatorv1.Console) {
	console.Spec.AuthProxy = operatorv1.ConsoleAuthProxyConfig{}
}
