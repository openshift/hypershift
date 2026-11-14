package config

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	libgocrypto "github.com/openshift/library-go/pkg/crypto"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/blang/semver"
)

func TestVersionSupportsTLSGroups(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		version  string
		expected bool
	}{
		{name: "When the version is exactly the floor 5.1.0, it should be supported", version: "5.1.0", expected: true},
		{name: "When the minor is above the floor 5.2.0, it should be supported", version: "5.2.0", expected: true},
		{name: "When the major is above the floor 6.0.0, it should be supported", version: "6.0.0", expected: true},
		{name: "When the version is far above the floor 10.3.0, it should be supported", version: "10.3.0", expected: true},
		{name: "When the version is below the floor 5.0.0, it should not be supported", version: "5.0.0", expected: false},
		{name: "When the version is below the floor 5.0.9, it should not be supported", version: "5.0.9", expected: false},
		{name: "When the version is the current GA 4.19.0, it should not be supported", version: "4.19.0", expected: false},
		{name: "When the version is the current GA 4.20.0, it should not be supported", version: "4.20.0", expected: false},
		{name: "When the version is a pre-release ec build of the floor, it should be supported", version: "5.1.0-ec.3", expected: true},
		{name: "When the version is a pre-release nightly build of the floor, it should be supported", version: "5.1.0-0.nightly-2026-01-01", expected: true},
		{name: "When the version is a pre-release rc build of the floor, it should be supported", version: "5.1.0-rc.1", expected: true},
		{name: "When the version is a pre-release build below the floor, it should not be supported", version: "5.0.0-ec.4", expected: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(VersionSupportsTLSGroups(semver.MustParse(tc.version))).To(Equal(tc.expected))
		})
	}
}

func TestTLSGroupsFeatureGateEnabled(t *testing.T) {
	t.Parallel()

	const ns = "test-ns"

	featureGateConfigMap := func(status configv1.FeatureGateStatus) *corev1.ConfigMap {
		manifest, err := json.Marshal(configv1.FeatureGate{Status: status})
		if err != nil {
			t.Fatalf("failed to marshal feature gate: %v", err)
		}
		return &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: FeatureGateConfigMapName, Namespace: ns},
			Data:       map[string]string{FeatureGateConfigKey: string(manifest)},
		}
	}

	enabledStatus := configv1.FeatureGateStatus{
		FeatureGates: []configv1.FeatureGateDetails{{
			Enabled: []configv1.FeatureGateAttributes{{Name: features.FeatureGateTLSGroupPreferences}},
		}},
	}
	disabledStatus := configv1.FeatureGateStatus{
		FeatureGates: []configv1.FeatureGateDetails{{
			Disabled: []configv1.FeatureGateAttributes{{Name: features.FeatureGateTLSGroupPreferences}},
		}},
	}
	otherStatus := configv1.FeatureGateStatus{
		FeatureGates: []configv1.FeatureGateDetails{{
			Enabled:  []configv1.FeatureGateAttributes{{Name: "SomeOtherGate"}},
			Disabled: []configv1.FeatureGateAttributes{{Name: "AnotherGate"}},
		}},
	}

	tests := []struct {
		name        string
		configMap   *corev1.ConfigMap
		expected    bool
		expectError bool
	}{
		{
			name:      "When the gate is enabled, it should report enabled",
			configMap: featureGateConfigMap(enabledStatus),
			expected:  true,
		},
		{
			name:      "When the gate is disabled, it should report not enabled",
			configMap: featureGateConfigMap(disabledStatus),
			expected:  false,
		},
		{
			name:      "When the gate is in neither the enabled nor disabled list, it should report not enabled",
			configMap: featureGateConfigMap(otherStatus),
			expected:  false,
		},
		{
			name:      "When there are no feature gates at all, it should report not enabled",
			configMap: featureGateConfigMap(configv1.FeatureGateStatus{}),
			expected:  false,
		},
		{
			name:        "When the config map is missing, it should return an error",
			configMap:   nil,
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			builder := fake.NewClientBuilder()
			if tc.configMap != nil {
				builder = builder.WithObjects(tc.configMap)
			}

			enabled, err := TLSGroupsFeatureGateEnabled(t.Context(), builder.Build(), ns)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(enabled).To(Equal(tc.expected))
		})
	}
}

func TestSupportedEtcdCurvePreferences(t *testing.T) {
	t.Parallel()

	allSupported := []tls.CurveID{
		tls.CurveP256,
		tls.CurveP384,
		tls.CurveP521,
		tls.X25519,
		tls.X25519MLKEM768,
		tls.SecP256r1MLKEM768,
		tls.SecP384r1MLKEM1024,
	}

	// unsupportedCurve is a CurveID that etcd does not accept; its String()
	// is not a key in the etcd curve set.
	unsupportedCurve := tls.CurveID(9999)

	testCases := []struct {
		name     string
		input    []tls.CurveID
		expected []tls.CurveID
	}{
		{
			name:     "When all curves are supported, it should return all preserving order",
			input:    allSupported,
			expected: allSupported,
		},
		{
			name:     "When some curves are unsupported, it should filter them and preserve order of the rest",
			input:    []tls.CurveID{tls.X25519, unsupportedCurve, tls.CurveP256},
			expected: []tls.CurveID{tls.X25519, tls.CurveP256},
		},
		{
			name:  "When all curves are unsupported, it should return empty",
			input: []tls.CurveID{unsupportedCurve, tls.CurveID(8888)},
		},
		{
			name:  "When input is empty, it should return empty",
			input: []tls.CurveID{},
		},
		{
			name:  "When input is nil, it should return empty",
			input: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			result := SupportedEtcdCurvePreferences(t.Context(), tc.input)
			if len(tc.expected) == 0 {
				g.Expect(result).To(BeEmpty())
				return
			}
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestCurvePreferences(t *testing.T) {
	t.Parallel()
	defaultCurves, _ := libgocrypto.TLSGroupsToCurveIDs(configv1.TLSProfiles[libgocrypto.DefaultTLSProfileType].Groups)
	type testCase struct {
		name          string
		profileType   configv1.TLSProfileType
		groups        []configv1.TLSGroup
		nilCustom     bool
		expected      []tls.CurveID
		ignoredGroups []configv1.TLSGroup
		expectedError string
	}
	tests := []testCase{
		{
			name:     "When TLS profile is nil, it should use the library-go default profile",
			expected: defaultCurves,
		},
		{
			name:        "When Custom profile contains groups, it should preserve supported curve order",
			profileType: configv1.TLSProfileCustomType,
			groups:      []configv1.TLSGroup{configv1.TLSGroupSecP256r1, configv1.TLSGroupX25519},
			expected:    []tls.CurveID{tls.CurveP256, tls.X25519},
		},
		{
			name:        "When Custom profile contains a hybrid group, it should return its curve ID",
			profileType: configv1.TLSProfileCustomType,
			groups:      []configv1.TLSGroup{configv1.TLSGroupX25519MLKEM768},
			expected:    []tls.CurveID{tls.X25519MLKEM768},
		},
		{
			name:        "When Custom profile has no groups, it should return no curve preferences",
			profileType: configv1.TLSProfileCustomType,
		},
		{
			name:          "When Custom profile contains unknown groups, it should log and skip them without error",
			profileType:   configv1.TLSProfileCustomType,
			groups:        []configv1.TLSGroup{configv1.TLSGroupX25519, "unknown", configv1.TLSGroupSecP256r1},
			expected:      []tls.CurveID{tls.X25519, tls.CurveP256},
			ignoredGroups: []configv1.TLSGroup{"unknown"},
		},
		{
			name:          "When Custom profile contains only unknown groups, it should log them and return no curves without error",
			profileType:   configv1.TLSProfileCustomType,
			groups:        []configv1.TLSGroup{"unknown", "other-unknown"},
			ignoredGroups: []configv1.TLSGroup{"unknown", "other-unknown"},
		},
		{
			name:          "When Custom profile has a nil Custom field, it should return an error",
			profileType:   configv1.TLSProfileCustomType,
			nilCustom:     true,
			expectedError: "Custom but Custom field is nil",
		},
	}
	for _, profileType := range []configv1.TLSProfileType{configv1.TLSProfileOldType, configv1.TLSProfileIntermediateType, configv1.TLSProfileModernType} {
		expected, _ := libgocrypto.TLSGroupsToCurveIDs(configv1.TLSProfiles[profileType].Groups)
		tests = append(tests, testCase{
			name:        fmt.Sprintf("When TLS profile is %s, it should use that profile's groups", profileType),
			profileType: profileType,
			expected:    expected,
		})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			var profile *configv1.TLSSecurityProfile
			if tc.profileType != "" {
				profile = &configv1.TLSSecurityProfile{Type: tc.profileType}
				if tc.profileType == configv1.TLSProfileCustomType && !tc.nilCustom {
					profile.Custom = &configv1.CustomTLSProfile{
						TLSProfileSpec: configv1.TLSProfileSpec{Groups: tc.groups},
					}
				}
			}
			var logs bytes.Buffer
			logger := zap.New(zap.WriteTo(&logs), zap.JSONEncoder())
			curves, err := CurvePreferences(ctrl.LoggerInto(context.Background(), logger), profile)
			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.expectedError)))
				g.Expect(curves).To(BeNil())
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(curves).To(Equal(tc.expected))
			if len(tc.ignoredGroups) > 0 {
				g.Expect(logs.String()).To(ContainSubstring("Ignoring unrecognized TLS groups"))
				for _, group := range tc.ignoredGroups {
					g.Expect(logs.String()).To(ContainSubstring(string(group)))
				}
			} else {
				g.Expect(logs.String()).To(BeEmpty())
			}
		})
	}
}
