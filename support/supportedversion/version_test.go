package supportedversion

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/supportedversion"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/blang/semver"
)

func TestSupportedVersions(t *testing.T) {
	g := NewGomegaWithT(t)
	g.Expect(Supported()).To(Equal([]string{"4.19", "4.18", "4.17", "4.16", "4.15", "4.14"}))
}

func TestIsValidReleaseVersion(t *testing.T) {
	v := func(str string) *semver.Version {
		result := semver.MustParse(str)
		return &result
	}
	testCases := []struct {
		name                   string
		currentVersion         *semver.Version
		nextVersion            *semver.Version
		latestVersionSupported *semver.Version
		minVersionSupported    *semver.Version
		networkType            hyperv1.NetworkType
		expectError            bool
		platform               hyperv1.PlatformType
	}{
		{
			name:                   "Releases before 4.14 are not supported",
			currentVersion:         v("4.8.0"),
			nextVersion:            v("4.7.0"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.13.0"),
			expectError:            true,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:           "versions > LatestSupportedVersion are not supported",
			currentVersion: v("4.15.0"),
			nextVersion: &semver.Version{
				Major: LatestSupportedVersion.Major,
				Minor: LatestSupportedVersion.Minor + 1,
			},
			latestVersionSupported: v("4.20.0"),
			minVersionSupported:    v("4.14.0"),
			expectError:            true,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "y-stream downgrade is not supported",
			currentVersion:         v("4.10.0"),
			nextVersion:            v("4.9.0"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			expectError:            true,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "y-stream upgrade is not for OpenShiftSDN",
			currentVersion:         v("4.10.0"),
			nextVersion:            v("4.11.0"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			networkType:            hyperv1.OpenShiftSDN,
			expectError:            true,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "the latest HostedCluster version supported by this Operator is 4.12.0",
			currentVersion:         v("4.12.0"),
			nextVersion:            v("4.14.0"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			expectError:            true,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "the minimum HostedCluster version supported by this Operator is 4.10.0",
			currentVersion:         v("4.9.0"),
			nextVersion:            v("4.9.0"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			expectError:            true,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "Valid",
			currentVersion:         v("4.11.0"),
			nextVersion:            v("4.11.1"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			expectError:            false,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "When going to minimum should be valid",
			currentVersion:         v("4.9.0"),
			nextVersion:            v("4.10.0"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			expectError:            false,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "Valid when going to minimum with a dev tag",
			currentVersion:         v("4.9.0"),
			nextVersion:            v("4.10.0-nightly-something"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			expectError:            false,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "Invalid when installing with OpenShiftSDN and version > 4.10",
			currentVersion:         nil,
			nextVersion:            v("4.11.5"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			networkType:            hyperv1.OpenShiftSDN,
			expectError:            true,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "Valid when installing with OpenShift SDN and version <= 4.10",
			currentVersion:         nil,
			nextVersion:            v("4.10.3"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			networkType:            hyperv1.OpenShiftSDN,
			expectError:            false,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "Invalid when installing with OVNKubernetes and version < 4.11",
			currentVersion:         nil,
			nextVersion:            v("4.10.5"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			networkType:            hyperv1.OVNKubernetes,
			expectError:            true,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "Valid when installing with OVNKubernetes and version >= 4.11",
			currentVersion:         nil,
			nextVersion:            v("4.11.1"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			networkType:            hyperv1.OVNKubernetes,
			expectError:            false,
			platform:               hyperv1.NonePlatform,
		},
		{
			name:                   "Valid when installing with OpenShift SDN and version >= 4.11 with PowerVS platform",
			currentVersion:         nil,
			nextVersion:            v("4.11.0"),
			latestVersionSupported: v("4.12.0"),
			minVersionSupported:    v("4.10.0"),
			networkType:            hyperv1.OpenShiftSDN,
			expectError:            false,
			platform:               hyperv1.PowerVSPlatform,
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := IsValidReleaseVersion(test.nextVersion, test.currentVersion, test.latestVersionSupported, test.minVersionSupported, test.networkType, test.platform)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
		})
	}

}

func TestGetMinSupportedVersion(t *testing.T) {
	g := NewGomegaWithT(t)

	hc := &hyperv1.HostedCluster{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: "",
			Annotations: map[string]string{
				hyperv1.SkipReleaseImageValidation: "true",
			},
		},
	}
	minVer := GetMinSupportedVersion(hc)
	g.Expect(minVer.String()).To(BeEquivalentTo(semver.MustParse("0.0.0").String()))
}

func TestGetSupportedOCPVersions(t *testing.T) {
	namespace := "hypershift"

	// Define a valid SupportedVersions struct and then marshal it to JSON for type safety.
	// This ensures our test data is coupled to the real data structure. If the SupportedVersions
	// struct is ever refactored, this test will fail to compile, providing an early signal that
	// the test is out of date. It also allows for a clean, type-safe assertion.
	validVersions := SupportedVersions{
		Versions: []string{"4.20", "4.19", "4.18", "4.17", "4.16", "4.15", "4.14"},
	}
	validVersionsJSON, err := json.Marshal(validVersions)
	if err != nil {
		t.Fatalf("failed to marshal valid versions: %v", err)
	}

	baseCM := supportedversion.ConfigMap(namespace)

	testCases := []struct {
		name                  string
		cm                    *corev1.ConfigMap
		expectErr             bool
		expectedErrMsg        string
		expectedVersions      SupportedVersions
		expectedServerVersion string
	}{
		{
			name: "When the ConfigMap is valid, expect versions to be returned successfully",
			cm: &corev1.ConfigMap{
				ObjectMeta: baseCM.ObjectMeta,
				Data: map[string]string{
					config.ConfigMapVersionsKey:      string(validVersionsJSON),
					config.ConfigMapServerVersionKey: "test-server-version",
				},
			},
			expectErr:             false,
			expectedVersions:      validVersions,
			expectedServerVersion: "test-server-version",
		},
		{
			name:           "When the ConfigMap is not found, expect an error",
			cm:             nil, // No configmap will be added to the client
			expectErr:      true,
			expectedErrMsg: "failed to find supported versions on the server",
		},
		{
			name: "When the server-version key is missing, expect an error",
			cm: &corev1.ConfigMap{
				ObjectMeta: baseCM.ObjectMeta,
				Data:       map[string]string{config.ConfigMapVersionsKey: string(validVersionsJSON)},
			},
			expectErr:      true,
			expectedErrMsg: "the server did not advertise its HyperShift version",
		},
		{
			name: "When the supported-versions key is missing, expect an error",
			cm: &corev1.ConfigMap{
				ObjectMeta: baseCM.ObjectMeta,
				Data:       map[string]string{config.ConfigMapServerVersionKey: "test-server-version"},
			},
			expectErr:      true,
			expectedErrMsg: "the server did not advertise supported OCP versions",
		},
		{
			name: "When the supported-versions JSON is malformed, expect an error",
			cm: &corev1.ConfigMap{
				ObjectMeta: baseCM.ObjectMeta,
				Data: map[string]string{
					config.ConfigMapVersionsKey:      `{"versions": "not-an-array"}`,
					config.ConfigMapServerVersionKey: "test-server-version",
				},
			},
			expectErr:      true,
			expectedErrMsg: "failed to parse supported versions on the server",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			// Setup fake client
			scheme := api.Scheme
			g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
			var fakeClient client.Client
			if tc.cm != nil {
				fakeClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.cm).Build()
			} else {
				fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()
			}

			// Execute the function
			supportedVersions, serverVersion, err := GetSupportedOCPVersions(context.Background(), namespace, fakeClient)

			// Assert results
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedErrMsg))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(supportedVersions).To(Equal(tc.expectedVersions))
				g.Expect(serverVersion).To(Equal(tc.expectedServerVersion))
			}
		})
	}
}
