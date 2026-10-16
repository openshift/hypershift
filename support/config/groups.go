package config

import (
	"context"
	"crypto/tls"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	libgocrypto "github.com/openshift/library-go/pkg/crypto"

	ctrl "sigs.k8s.io/controller-runtime"
)

// CurvePreferences resolves a TLS profile's groups to Go curve IDs.
// Unrecognized groups are logged and skipped.
func CurvePreferences(ctx context.Context, profile *configv1.TLSSecurityProfile) ([]tls.CurveID, error) {
	if profile == nil {
		profile = &configv1.TLSSecurityProfile{Type: libgocrypto.DefaultTLSProfileType}
	}
	var groups []configv1.TLSGroup
	if profile.Type == configv1.TLSProfileCustomType {
		if profile.Custom == nil {
			return nil, fmt.Errorf("TLS profile type is Custom but Custom field is nil")
		}
		groups = profile.Custom.Groups
	} else {
		groups = configv1.TLSProfiles[profile.Type].Groups
	}
	curveIDs, unrecognizedGroups := libgocrypto.TLSGroupsToCurveIDs(groups)
	if len(unrecognizedGroups) > 0 {
		ctrl.LoggerFrom(ctx).Info("Ignoring unrecognized TLS groups", "groups", unrecognizedGroups)
	}
	return curveIDs, nil
}
