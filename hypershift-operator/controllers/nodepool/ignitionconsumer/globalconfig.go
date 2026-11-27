package ignitionconsumer

import (
	"bytes"
	"encoding/json"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/backwardcompat"
	"github.com/openshift/hypershift/support/globalconfig"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/blang/semver"
)

// rolloutGlobalConfigData returns the canonical, user-set subset of the hosted
// cluster's global configuration (proxy + image +, for release >=4.23.0, the
// APIServer TLS security profile), WITHOUT the computed Status defaults. It is
// the content the NodePool controller writes into the CR-owned rolloutGlobalConfig
// ConfigMap and the PayloadController feeds into the rollout hash.
//
// It deliberately uses globalconfig.ReconcileProxyConfig (spec-only) rather than
// the WithStatus variant used by the payload-identity config string: the computed
// Status NoProxy derives from networking/platform, and folding it in would churn
// the rollout hash on changes that do not alter the user's intent.
func rolloutGlobalConfigData(hc *hyperv1.HostedCluster, releaseVersion string) ([]byte, error) {
	proxy := globalconfig.ProxyConfig()
	globalconfig.ReconcileProxyConfig(proxy, hc.Spec.Configuration)

	image := globalconfig.ImageConfig()
	globalconfig.ReconcileImageConfigFromHostedCluster(image, hc)

	buf := bytes.NewBuffer(nil)

	proxyBytes, err := api.CompatibleJSONEncode(proxy)
	if err != nil {
		return nil, fmt.Errorf("failed to encode proxy global config: %w", err)
	}
	buf.Write(proxyBytes)

	imageBytes, err := api.CompatibleJSONEncode(image)
	if err != nil {
		return nil, fmt.Errorf("failed to encode image global config: %w", err)
	}
	buf.Write(imageBytes)

	if err := appendTLSSecurityProfile(buf, hc, releaseVersion); err != nil {
		return nil, err
	}

	// Keep the on-the-wire bytes backward compatible with older CPOs, matching
	// globalConfigString so a NodePool does not roll purely on operator upgrade.
	return []byte(backwardcompat.GetBackwardCompatibleConfigString(buf.String())), nil
}

// appendTLSSecurityProfile mirrors conditionallyAddToGlobalConfigString: only
// the APIServer TLSSecurityProfile affects worker configuration, and only from
// v4.23.0 onward.
func appendTLSSecurityProfile(buf *bytes.Buffer, hc *hyperv1.HostedCluster, releaseVersion string) error {
	version, err := semver.Parse(releaseVersion)
	if err != nil {
		return fmt.Errorf("failed to parse release version %q: %w", releaseVersion, err)
	}
	version.Pre = nil

	if version.LT(semver.MustParse("4.23.0")) {
		return nil
	}

	var tlsProfile *configv1.TLSSecurityProfile
	if hc.Spec.Configuration != nil && hc.Spec.Configuration.APIServer != nil {
		tlsProfile = hc.Spec.Configuration.APIServer.TLSSecurityProfile
	}
	tlsBytes, err := json.Marshal(tlsProfile)
	if err != nil {
		return fmt.Errorf("failed to encode TLS security profile: %w", err)
	}
	buf.Write(tlsBytes)
	buf.WriteByte('\n')
	return nil
}
