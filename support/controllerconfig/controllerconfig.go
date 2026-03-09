package controllerconfig

import (
	"encoding/json"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/library-go/pkg/crypto"
	"sigs.k8s.io/yaml"
)

// BuildGenericControllerConfig creates a GenericControllerConfig using the
// provided HostedControlPlane as starting point. In essence this function
// sets what should be globally set across all controllers deployed as part
// of hypershift.
func BuildGenericControllerConfig(hcp *hyperv1beta1.HostedControlPlane) *configv1.GenericControllerConfig {
	mintls, suites := getSecurityProfileCiphers(hcp.Spec.Configuration.GetTLSSecurityProfile())
	return &configv1.GenericControllerConfig{
		ServingInfo: configv1.HTTPServingInfo{
			ServingInfo: configv1.ServingInfo{
				MinTLSVersion: mintls,
				CipherSuites:  suites,
			},
		},
	}
}

// YAMLMarshal is an auxiliary function to marshal GenericControllerConfig
// objects while preserving apiVersion and kind fields.
func YAMLMarshal(config *configv1.GenericControllerConfig) (string, error) {
	asjson, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("json marshaling config: %w", err)
	}

	asmap := map[string]any{}
	if err := json.Unmarshal(asjson, &asmap); err != nil {
		return "", fmt.Errorf("json unmarshaling config: %w", err)
	}

	asmap["apiVersion"] = configv1.GroupVersion.String()
	asmap["kind"] = "GenericControllerConfig"

	data, err := yaml.Marshal(asmap)
	if err != nil {
		return "", fmt.Errorf("yaml marshaling config: %w", err)
	}
	return string(data), nil
}

// getSecurityProfileCiphers extracts the minimum TLS version and cipher suites
// from TLSSecurityProfile object, Converts the ciphers to IANA names as
// supported by Kube ServingInfo config. If profile is nil, returns config
// defined by the Intermediate TLS Profile. XXX this function is almost copy
// and paste of this very same function as found in library-go. We should
// consider export this in there.
func getSecurityProfileCiphers(profile *configv1.TLSSecurityProfile) (string, []string) {
	profileType := crypto.DefaultTLSProfileType
	if profile != nil {
		profileType = profile.Type
	}

	var profileSpec *configv1.TLSProfileSpec
	if profileType == configv1.TLSProfileCustomType && profile.Custom != nil {
		profileSpec = &profile.Custom.TLSProfileSpec
	} else {
		profileSpec = configv1.TLSProfiles[profileType]
	}

	// nothing found / custom type set but no actual custom spec
	if profileSpec == nil {
		profileSpec = configv1.TLSProfiles[crypto.DefaultTLSProfileType]
	}

	// need to remap all Ciphers to their respective IANA names used by Go
	return string(profileSpec.MinTLSVersion), crypto.OpenSSLToIANACipherSuites(profileSpec.Ciphers)
}
