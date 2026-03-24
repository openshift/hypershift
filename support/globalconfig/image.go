package globalconfig

import (
	"regexp"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// validRegistryPattern matches valid registry scopes: hostname[:port][/path].
// Sourced from openshift/machine-config-operator sourceRegex.
var validRegistryPattern = regexp.MustCompile(`^\*(?:\.(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]))+$|^((?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9])(?:(?:\.(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]))+)?(?::[0-9]+)?)(?:(?:/[a-z0-9]+(?:(?:(?:[._]|__|[-]*)[a-z0-9]+)+)?)+)?$`)

func ImageConfig() *configv1.Image {
	return &configv1.Image{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ReconcileImageConfig(cfg *configv1.Image, hcp *hyperv1.HostedControlPlane) {
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.Image != nil {
		cfg.Spec = *hcp.Spec.Configuration.Image
	}
}

func ReconcileImageConfigFromHostedCluster(cfg *configv1.Image, hc *hyperv1.HostedCluster) {
	if hc.Spec.Configuration != nil && hc.Spec.Configuration.Image != nil {
		cfg.Spec = *hc.Spec.Configuration.Image
	}
}

// ValidateRegistrySources validates that all entries in blockedRegistries,
// allowedRegistries, and insecureRegistries are valid registry scopes
func ValidateRegistrySources(registrySources *configv1.RegistrySources, fldPath *field.Path) field.ErrorList {
	var errs field.ErrorList
	if registrySources == nil {
		return errs
	}

	for i, reg := range registrySources.BlockedRegistries {
		if !validRegistryPattern.MatchString(reg) {
			errs = append(errs, field.Invalid(fldPath.Child("blockedRegistries").Index(i), reg,
				"must be a valid registry hostname[:port][/path] without tags or digests"))
		}
	}
	for i, reg := range registrySources.AllowedRegistries {
		if !validRegistryPattern.MatchString(reg) {
			errs = append(errs, field.Invalid(fldPath.Child("allowedRegistries").Index(i), reg,
				"must be a valid registry hostname[:port][/path] without tags or digests"))
		}
	}
	for i, reg := range registrySources.InsecureRegistries {
		if !validRegistryPattern.MatchString(reg) {
			errs = append(errs, field.Invalid(fldPath.Child("insecureRegistries").Index(i), reg,
				"must be a valid registry hostname[:port][/path] without tags or digests"))
		}
	}

	return errs
}
