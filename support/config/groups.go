package config

import (
	"context"
	"crypto/tls"
	"fmt"
	"slices"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	libgocrypto "github.com/openshift/library-go/pkg/crypto"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

// VersionSupportsTLSGroups reports whether the given payload version supports TLS
// group preferences (v5.1.0+). It compares Major/Minor only, so pre-release
// builds (e.g. 5.1.0-ec.3, nightlies) of a supporting release still count as
// supported.
func VersionSupportsTLSGroups(payloadVersion semver.Version) bool {
	supportedOnMajors := payloadVersion.Major > 5
	supportedOnMinors := payloadVersion.Major == 5 && payloadVersion.Minor >= 1
	return supportedOnMajors || supportedOnMinors
}

// TLSGroupsFeatureGateEnabled reads the feature gates from the hosted cluster's
// feature-gate config map and reports whether the TLSGroupPreferences feature
// gate is enabled.
func TLSGroupsFeatureGateEnabled(ctx context.Context, c client.Reader, ns string) (bool, error) {
	featureGates, err := FeatureGatesFromConfigMap(ctx, c, ns)
	if err != nil {
		return false, fmt.Errorf("failed to load feature gates config map: %w", err)
	}
	featureGateParam := fmt.Sprintf("%s=true", features.FeatureGateTLSGroupPreferences)
	return slices.Contains(featureGates, featureGateParam), nil
}

// CurvePreferences resolves a TLS profile's groups to Go curve IDs.
// Unrecognized or non approved (FIPS) groups are logged and skipped.
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

	if libgocrypto.IsFIPSEnabled() {
		approved := libgocrypto.FIPSApprovedTLSGroups(groups)
		if len(approved) != len(groups) {
			ctrl.LoggerFrom(ctx).Info(
				"Ignoring non approved FIPS TLS groups", "groups",
				slices.DeleteFunc(
					slices.Clone(groups),
					func(group configv1.TLSGroup) bool {
						return slices.Contains(approved, group)
					},
				),
			)
		}
		groups = approved
	}

	curveIDs, unrecognizedGroups := libgocrypto.TLSGroupsToCurveIDs(groups)
	if len(unrecognizedGroups) > 0 {
		ctrl.LoggerFrom(ctx).Info("Ignoring unrecognized TLS groups", "groups", unrecognizedGroups)
	}
	return curveIDs, nil
}

// etcdCurveIDs are the curve names accepted by etcd --listen-curve-preferences.
// The set matches openshift/etcd tlsutil.GetCurveID: That helper is not in the
// upstream etcd module this repo vendors (go.etcd.io/etcd v3.7.0), so the map
// stays local until the fork is vendored.
//
// https://github.com/openshift/etcd/blob/7dd2f94050a119e655e9df02e868fb2d2a3de747/client/pkg/tlsutil/curves.go
var etcdCurveIDs = map[string]tls.CurveID{
	tls.CurveP256.String():          tls.CurveP256,
	tls.CurveP384.String():          tls.CurveP384,
	tls.CurveP521.String():          tls.CurveP521,
	tls.X25519.String():             tls.X25519,
	tls.X25519MLKEM768.String():     tls.X25519MLKEM768,
	tls.SecP256r1MLKEM768.String():  tls.SecP256r1MLKEM768,
	tls.SecP384r1MLKEM1024.String(): tls.SecP384r1MLKEM1024,
}

// SupportedEtcdCurvePreferences filters the given curves down to those accepted
// by etcd's --listen-curve-preferences (see etcdCurveIDs), preserving order.
// Curves not supported by etcd are logged and skipped.
func SupportedEtcdCurvePreferences(ctx context.Context, curves []tls.CurveID) []tls.CurveID {
	log := ctrl.LoggerFrom(ctx)
	var supported []tls.CurveID
	for _, curve := range curves {
		if _, ok := etcdCurveIDs[curve.String()]; !ok {
			log.Info("curve is not supported for use with etcd, skipping", "curve", curve.String())
			continue
		}
		supported = append(supported, curve)
	}
	return supported
}
