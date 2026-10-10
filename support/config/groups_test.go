package config

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	libgocrypto "github.com/openshift/library-go/pkg/crypto"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

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
