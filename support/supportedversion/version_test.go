package supportedversion

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	g.Expect(Supported()).To(Equal([]string{"4.22", "4.21", "4.20", "4.19", "4.18", "4.17", "4.16", "4.15", "4.14"}))
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
			latestVersionSupported: v("4.21.0"),
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

	// Annotation should override platform-specific minimums as well
	hcAnnotatedROKS := &hyperv1.HostedCluster{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: "annotated-ROKS",
			Annotations: map[string]string{
				hyperv1.SkipReleaseImageValidation: "true",
			},
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.IBMCloudPlatform,
			},
		},
	}
	minVer = GetMinSupportedVersion(hcAnnotatedROKS)
	g.Expect(minVer.String()).To(BeEquivalentTo(semver.MustParse("0.0.0").String()))

	// Verify minimum supported version for Red Hat OpenShift on IBM (ROKS)
	hcROKS := &hyperv1.HostedCluster{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: "ROKS",
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.IBMCloudPlatform,
			},
		},
	}
	minVer = GetMinSupportedVersion(hcROKS)
	g.Expect(minVer.String()).To(BeEquivalentTo(semver.MustParse("4.14.0").String()))

	// Verify minimum supported version for non-ROKS.
	hcAWS := &hyperv1.HostedCluster{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: "AWS",
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
			},
		},
	}
	minVer = GetMinSupportedVersion(hcAWS)
	g.Expect(minVer.String()).To(BeEquivalentTo(semver.MustParse("4.14.0").String()))
}

func TestGetSupportedOCPVersions(t *testing.T) {
	namespace := "hypershift"

	// Define a valid SupportedVersions struct and then marshal it to JSON for type safety.
	// This ensures our test data is coupled to the real data structure. If the SupportedVersions
	// struct is ever refactored, this test will fail to compile, providing an early signal that
	// the test is out of date. It also allows for a clean, type-safe assertion.
	validVersions := SupportedVersions{
		Versions: []string{"4.21", "4.20", "4.19", "4.18", "4.17", "4.16", "4.15", "4.14"},
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
			supportedVersions, serverVersion, err := GetSupportedOCPVersions(t.Context(), namespace, fakeClient, tc.cm)

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

func TestValidateVersionSkew(t *testing.T) {
	v := func(str string) *semver.Version {
		result := semver.MustParse(str)
		return &result
	}

	testCases := []struct {
		name                 string
		hostedClusterVersion *semver.Version
		nodePoolVersion      *semver.Version
		expectError          bool
		expectedErrSubstr    string
	}{
		{
			name:                 "When NodePool version is higher than HostedCluster version, it should return error",
			hostedClusterVersion: v("4.17.0"),
			nodePoolVersion:      v("4.18.0"),
			expectError:          true,
			expectedErrSubstr:    "cannot be higher than the HostedCluster version",
		},
		{
			name:                 "When versions are identical, it should pass validation",
			hostedClusterVersion: v("4.18.0"),
			nodePoolVersion:      v("4.18.0"),
			expectError:          false,
		},
		{
			name:                 "When NodePool is 1 minor version behind, it should pass validation",
			hostedClusterVersion: v("4.18.0"),
			nodePoolVersion:      v("4.17.0"),
			expectError:          false,
		},
		{
			name:                 "When NodePool is 2 minor versions behind, it should pass validation",
			hostedClusterVersion: v("4.18.0"),
			nodePoolVersion:      v("4.16.0"),
			expectError:          false,
		},
		{
			name:                 "When NodePool is exactly 3 minor versions behind (n-3), it should pass validation",
			hostedClusterVersion: v("4.18.0"),
			nodePoolVersion:      v("4.15.0"),
			expectError:          false,
		},
		{
			name:                 "When NodePool is 4 minor versions behind (exceeds n-3), it should return error",
			hostedClusterVersion: v("4.18.0"),
			nodePoolVersion:      v("4.14.0"),
			expectError:          true,
			expectedErrSubstr:    "is less than 4.15, which is the minimum NodePool version compatible with the 4.18 HostedCluster",
		},
		{
			name:                 "When HostedCluster is 4.21 and NodePool is 4.18 (n-3 boundary), it should pass validation",
			hostedClusterVersion: v("4.21.0"),
			nodePoolVersion:      v("4.18.0"),
			expectError:          false,
		},
		{
			name:                 "When HostedCluster is 4.21 and NodePool is 4.17 (exceeds n-3), it should return error",
			hostedClusterVersion: v("4.21.0"),
			nodePoolVersion:      v("4.17.0"),
			expectError:          true,
			expectedErrSubstr:    "is less than 4.18, which is the minimum NodePool version compatible with the 4.21 HostedCluster",
		},
		{
			name:                 "When NodePool version has patch differences but same minor, it should pass validation",
			hostedClusterVersion: v("4.18.5"),
			nodePoolVersion:      v("4.18.0"),
			expectError:          false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			err := ValidateVersionSkew(tc.hostedClusterVersion, tc.nodePoolVersion)

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedErrSubstr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestRetrieveSupportedOCPVersion(t *testing.T) {
	supportedVersionsCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "supported-versions",
			Namespace: "test",
			Labels:    map[string]string{"hypershift.openshift.io/supported-versions": "true"},
		},
		Data: map[string]string{
			"server-version":     "test-server",
			"supported-versions": `{"versions":["4.19", "4.18", "4.17", "4.16", "4.15", "4.14"]}`,
		},
	}

	olderSupportedVersionsCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "supported-versions",
			Namespace: "hypershift",
			Labels:    map[string]string{"hypershift.openshift.io/supported-versions": "true"},
		},
		Data: map[string]string{
			"server-version":     "test-server",
			"supported-versions": `{"versions":["4.18", "4.17", "4.16", "4.15", "4.14"]}`,
		},
	}

	// Mock HTTP server that returns release tags
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := `{
			"name": "4-stable-multi",
			"tags": [
				{
					"name": "4.19.0",
					"pullSpec": "quay.io/openshift-release-dev/ocp-release:4.19.0-multi",
					"downloadURL": "https://example.com/4.19.0"
				},
				{
					"name": "4.18.5",
					"pullSpec": "quay.io/openshift-release-dev/ocp-release:4.18.5-multi",
					"downloadURL": "https://example.com/4.18.5"
				},
				{
					"name": "4.18.0",
					"pullSpec": "quay.io/openshift-release-dev/ocp-release:4.18.0-multi",
					"downloadURL": "https://example.com/4.18.0"
				}
			]
		}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(response))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer mockServer.Close()

	// ConfigMap with unsupported versions (none match what the server returns)
	unsupportedVersionsCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "supported-versions",
			Namespace: "test",
			Labels:    map[string]string{"hypershift.openshift.io/supported-versions": "true"},
		},
		Data: map[string]string{
			"server-version":     "test-server",
			"supported-versions": `{"versions":["4.13", "4.12", "4.11"]}`,
		},
	}

	testCases := []struct {
		name               string
		cm                 *corev1.ConfigMap
		releaseURL         string
		expectErr          bool
		expectedErrMsg     string
		expectedOCPVersion ocpVersion
	}{
		{
			name:       "When latest stable release is supported, expect it to be returned",
			cm:         supportedVersionsCM,
			releaseURL: mockServer.URL + "/api/v1/releasestream/4-stable-multi/tags",
			expectErr:  false,
			expectedOCPVersion: ocpVersion{
				Name:     "4.19.0",
				PullSpec: "quay.io/openshift-release-dev/ocp-release:4.19.0-multi",
			},
		},
		{
			name:           "When no supported release versions match, expect an error",
			cm:             unsupportedVersionsCM,
			releaseURL:     mockServer.URL + "/api/v1/releasestream/4-stable-multi/tags",
			expectErr:      true,
			expectedErrMsg: "failed to find the latest supported OCP version",
		},
		{
			name:           "When the ConfigMap is missing, expect an error",
			cm:             nil,
			releaseURL:     mockServer.URL + "/api/v1/releasestream/4-stable-multi/tags",
			expectErr:      true,
			expectedErrMsg: "failed to get supported OCP versions",
		},
		{
			name:       "When the ConfigMap supports older versions, expect the latest older version to be returned",
			cm:         olderSupportedVersionsCM,
			releaseURL: mockServer.URL + "/api/v1/releasestream/4-stable-multi/tags",
			expectErr:  false,
			expectedOCPVersion: ocpVersion{
				Name:     "4.18",
				PullSpec: "quay.io/openshift-release-dev/ocp-release:4.18",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			scheme := api.Scheme
			g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
			var fakeClient client.Client
			if tc.cm != nil {
				fakeClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.cm).Build()
			} else {
				fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()
			}

			version, err := retrieveSupportedOCPVersion(t.Context(), tc.releaseURL, fakeClient)
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(version.Name).To(ContainSubstring(tc.expectedOCPVersion.Name))
				g.Expect(version.PullSpec).To(ContainSubstring(tc.expectedOCPVersion.PullSpec))
			}
		})
	}
}

func TestGetArchFromStream(t *testing.T) {
	testCases := []struct {
		name          string
		releaseStream string
		expectedArch  string
	}{
		{
			name:          "When stream ends with -multi, it should return multi",
			releaseStream: "4-stable-multi",
			expectedArch:  "multi",
		},
		{
			name:          "When stream ends with -multi (different version), it should return multi",
			releaseStream: "4.19-stable-multi",
			expectedArch:  "multi",
		},
		{
			name:          "When stream ends with -arm64, it should return arm64",
			releaseStream: "4-stable-arm64",
			expectedArch:  "arm64",
		},
		{
			name:          "When stream ends with -ppc64le, it should return ppc64le",
			releaseStream: "4-stable-ppc64le",
			expectedArch:  "ppc64le",
		},
		{
			name:          "When stream ends with -s390x, it should return s390x",
			releaseStream: "4-stable-s390x",
			expectedArch:  "s390x",
		},
		{
			name:          "When stream does not end with recognized suffix, it should return amd64",
			releaseStream: "4-stable",
			expectedArch:  "amd64",
		},
		{
			name:          "When stream is 4-dev-preview, it should return amd64",
			releaseStream: "4-dev-preview",
			expectedArch:  "amd64",
		},
		{
			name:          "When stream is empty, it should return amd64",
			releaseStream: "",
			expectedArch:  "amd64",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := getArchFromStream(tc.releaseStream)
			g.Expect(result).To(Equal(tc.expectedArch))
		})
	}
}

func TestRetrieveSupportedOCPVersionWithRCFiltering(t *testing.T) {
	supportedVersionsCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "supported-versions",
			Namespace: "test",
			Labels:    map[string]string{"hypershift.openshift.io/supported-versions": "true"},
		},
		Data: map[string]string{
			"server-version":     "test-server",
			"supported-versions": `{"versions":["4.19", "4.18", "4.17", "4.16", "4.15", "4.14"]}`,
		},
	}

	// Mock HTTP server that returns release tags with RC versions (simulates /tags endpoint)
	mockServerWithRC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate the scenario from JIRA where RC versions appear before GA versions
		response := `{
			"name": "4-stable-multi",
			"tags": [
				{
					"name": "4.20.0-rc.3",
					"pullSpec": "quay.io/openshift-release-dev/ocp-release:4.20.0-rc.3-multi",
					"downloadURL": "https://example.com/4.20.0-rc.3"
				},
				{
					"name": "4.19.5",
					"pullSpec": "quay.io/openshift-release-dev/ocp-release:4.19.5-multi",
					"downloadURL": "https://example.com/4.19.5"
				},
				{
					"name": "4.19.0",
					"pullSpec": "quay.io/openshift-release-dev/ocp-release:4.19.0-multi",
					"downloadURL": "https://example.com/4.19.0"
				},
				{
					"name": "4.18.8",
					"pullSpec": "quay.io/openshift-release-dev/ocp-release:4.18.8-multi",
					"downloadURL": "https://example.com/4.18.8"
				}
			]
		}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(response))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer mockServerWithRC.Close()

	// Mock HTTP server that returns tags for amd64 stream with RC versions
	mockServerAmd64WithRC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := `{
			"name": "4-stable",
			"tags": [
				{
					"name": "4.21.0-rc.0",
					"pullSpec": "quay.io/openshift-release-dev/ocp-release:4.21.0-rc.0-x86_64",
					"downloadURL": "https://example.com/4.21.0-rc.0"
				},
				{
					"name": "4.20.11",
					"pullSpec": "quay.io/openshift-release-dev/ocp-release:4.20.11-x86_64",
					"downloadURL": "https://example.com/4.20.11"
				},
				{
					"name": "4.19.5",
					"pullSpec": "quay.io/openshift-release-dev/ocp-release:4.19.5-x86_64",
					"downloadURL": "https://example.com/4.19.5"
				}
			]
		}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(response))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer mockServerAmd64WithRC.Close()

	// Mock HTTP server that returns tags for arm64 stream with RC versions
	mockServerArm64WithRC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := `{
			"name": "4-stable-arm64",
			"tags": [
				{
					"name": "4.21.0-rc.2",
					"pullSpec": "quay.io/openshift-release-dev/ocp-release:4.21.0-rc.2-aarch64",
					"downloadURL": "https://example.com/4.21.0-rc.2"
				},
				{
					"name": "4.19.8",
					"pullSpec": "quay.io/openshift-release-dev/ocp-release:4.19.8-aarch64",
					"downloadURL": "https://example.com/4.19.8"
				},
				{
					"name": "4.18.10",
					"pullSpec": "quay.io/openshift-release-dev/ocp-release:4.18.10-aarch64",
					"downloadURL": "https://example.com/4.18.10"
				}
			]
		}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(response))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer mockServerArm64WithRC.Close()

	// Mock HTTP server that returns only RC versions (no GA versions available)
	mockServerOnlyRC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := `{
			"name": "4-stable",
			"tags": [
				{
					"name": "4.21.0-rc.5",
					"pullSpec": "quay.io/openshift-release-dev/ocp-release:4.21.0-rc.5-x86_64",
					"downloadURL": "https://example.com/4.21.0-rc.5"
				},
				{
					"name": "4.21.0-rc.4",
					"pullSpec": "quay.io/openshift-release-dev/ocp-release:4.21.0-rc.4-x86_64",
					"downloadURL": "https://example.com/4.21.0-rc.4"
				}
			]
		}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(response))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer mockServerOnlyRC.Close()

	testCases := []struct {
		name               string
		cm                 *corev1.ConfigMap
		releaseURL         string
		expectErr          bool
		expectedErrMsg     string
		expectedOCPVersion ocpVersion
	}{
		{
			name:       "When multi-arch stream has RC versions, expect latest non-RC supported version",
			cm:         supportedVersionsCM,
			releaseURL: mockServerWithRC.URL,
			expectErr:  false,
			expectedOCPVersion: ocpVersion{
				Name:     "4.19.5",
				PullSpec: "quay.io/openshift-release-dev/ocp-release:4.19.5-multi",
			},
		},
		{
			name:       "When amd64 stream has RC versions, expect latest non-RC supported version",
			cm:         supportedVersionsCM,
			releaseURL: mockServerAmd64WithRC.URL,
			expectErr:  false,
			expectedOCPVersion: ocpVersion{
				Name:     "4.19.5",
				PullSpec: "quay.io/openshift-release-dev/ocp-release:4.19.5-x86_64",
			},
		},
		{
			name:       "When arm64 stream has RC versions, expect latest non-RC supported version",
			cm:         supportedVersionsCM,
			releaseURL: mockServerArm64WithRC.URL,
			expectErr:  false,
			expectedOCPVersion: ocpVersion{
				Name:     "4.19.8",
				PullSpec: "quay.io/openshift-release-dev/ocp-release:4.19.8-aarch64",
			},
		},
		{
			name:           "When stream has only RC versions, expect error",
			cm:             supportedVersionsCM,
			releaseURL:     mockServerOnlyRC.URL,
			expectErr:      true,
			expectedErrMsg: "failed to find the latest supported OCP version",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			scheme := api.Scheme
			g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
			var fakeClient client.Client
			if tc.cm != nil {
				fakeClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.cm).Build()
			} else {
				fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()
			}

			version, err := retrieveSupportedOCPVersion(t.Context(), tc.releaseURL, fakeClient)
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				if tc.expectedErrMsg != "" {
					g.Expect(err.Error()).To(ContainSubstring(tc.expectedErrMsg))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(version.Name).To(Equal(tc.expectedOCPVersion.Name))
				g.Expect(version.PullSpec).To(Equal(tc.expectedOCPVersion.PullSpec))
			}
		})
	}
}

func TestFindLatestSupportedVersionWithSorting(t *testing.T) {
	supportedVersionsCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "supported-versions",
			Namespace: "test",
			Labels:    map[string]string{"hypershift.openshift.io/supported-versions": "true"},
		},
		Data: map[string]string{
			"server-version":     "test-server",
			"supported-versions": `{"versions":["4.19", "4.18", "4.17", "4.16", "4.15", "4.14"]}`,
		},
	}

	testCases := []struct {
		name             string
		tags             string
		expectedVersion  string
		expectedPullSpec string
		expectErr        bool
		expectedErrMsg   string
	}{
		{
			name: "When tags are in random order with oldest first, expect NEWEST supported version",
			tags: `[
				{"name": "4.14.21", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.14.21-multi", "downloadURL": "https://example.com/4.14.21"},
				{"name": "4.19.5", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.19.5-multi", "downloadURL": "https://example.com/4.19.5"},
				{"name": "4.18.3", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.18.3-multi", "downloadURL": "https://example.com/4.18.3"},
				{"name": "4.17.10", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.17.10-multi", "downloadURL": "https://example.com/4.17.10"}
			]`,
			expectedVersion:  "4.19.5",
			expectedPullSpec: "quay.io/openshift-release-dev/ocp-release:4.19.5-multi",
		},
		{
			name: "When tags include RC versions, expect latest non-RC supported version",
			tags: `[
				{"name": "4.20.0-rc.5", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.20.0-rc.5-multi", "downloadURL": "https://example.com/4.20.0-rc.5"},
				{"name": "4.19.5", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.19.5-multi", "downloadURL": "https://example.com/4.19.5"},
				{"name": "4.19.0-rc.2", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.19.0-rc.2-multi", "downloadURL": "https://example.com/4.19.0-rc.2"},
				{"name": "4.18.3", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.18.3-multi", "downloadURL": "https://example.com/4.18.3"}
			]`,
			expectedVersion:  "4.19.5",
			expectedPullSpec: "quay.io/openshift-release-dev/ocp-release:4.19.5-multi",
		},
		{
			name: "When tags are in ascending order, expect NEWEST supported version",
			tags: `[
				{"name": "4.14.21", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.14.21-multi", "downloadURL": "https://example.com/4.14.21"},
				{"name": "4.15.10", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.15.10-multi", "downloadURL": "https://example.com/4.15.10"},
				{"name": "4.16.5", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.16.5-multi", "downloadURL": "https://example.com/4.16.5"},
				{"name": "4.17.3", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.17.3-multi", "downloadURL": "https://example.com/4.17.3"},
				{"name": "4.18.2", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.18.2-multi", "downloadURL": "https://example.com/4.18.2"},
				{"name": "4.19.1", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.19.1-multi", "downloadURL": "https://example.com/4.19.1"}
			]`,
			expectedVersion:  "4.19.1",
			expectedPullSpec: "quay.io/openshift-release-dev/ocp-release:4.19.1-multi",
		},
		{
			name: "When tags are in descending order, expect NEWEST supported version",
			tags: `[
				{"name": "4.19.1", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.19.1-multi", "downloadURL": "https://example.com/4.19.1"},
				{"name": "4.18.2", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.18.2-multi", "downloadURL": "https://example.com/4.18.2"},
				{"name": "4.17.3", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.17.3-multi", "downloadURL": "https://example.com/4.17.3"},
				{"name": "4.16.5", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.16.5-multi", "downloadURL": "https://example.com/4.16.5"}
			]`,
			expectedVersion:  "4.19.1",
			expectedPullSpec: "quay.io/openshift-release-dev/ocp-release:4.19.1-multi",
		},
		{
			name: "When all versions are RC, expect error",
			tags: `[
				{"name": "4.20.0-rc.5", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.20.0-rc.5-multi", "downloadURL": "https://example.com/4.20.0-rc.5"},
				{"name": "4.20.0-rc.4", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.20.0-rc.4-multi", "downloadURL": "https://example.com/4.20.0-rc.4"},
				{"name": "4.19.0-rc.2", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.19.0-rc.2-multi", "downloadURL": "https://example.com/4.19.0-rc.2"}
			]`,
			expectErr:      true,
			expectedErrMsg: "failed to find the latest supported OCP version",
		},
		{
			name: "When RC versions are mixed throughout list, expect latest non-RC supported version",
			tags: `[
				{"name": "4.18.1", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.18.1-multi", "downloadURL": "https://example.com/4.18.1"},
				{"name": "4.20.0-rc.3", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.20.0-rc.3-multi", "downloadURL": "https://example.com/4.20.0-rc.3"},
				{"name": "4.19.8", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.19.8-multi", "downloadURL": "https://example.com/4.19.8"},
				{"name": "4.19.0-rc.1", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.19.0-rc.1-multi", "downloadURL": "https://example.com/4.19.0-rc.1"},
				{"name": "4.17.5", "pullSpec": "quay.io/openshift-release-dev/ocp-release:4.17.5-multi", "downloadURL": "https://example.com/4.17.5"}
			]`,
			expectedVersion:  "4.19.8",
			expectedPullSpec: "quay.io/openshift-release-dev/ocp-release:4.19.8-multi",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response := fmt.Sprintf(`{"name": "4-stable-multi", "tags": %s}`, tc.tags)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, err := w.Write([]byte(response))
				if err != nil {
					t.Fatalf("failed to write response: %v", err)
				}
			}))
			defer mockServer.Close()

			scheme := api.Scheme
			g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(supportedVersionsCM).Build()

			version, err := retrieveSupportedOCPVersion(t.Context(), mockServer.URL, fakeClient)

			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				if tc.expectedErrMsg != "" {
					g.Expect(err.Error()).To(ContainSubstring(tc.expectedErrMsg))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(version.Name).To(Equal(tc.expectedVersion))
				g.Expect(version.PullSpec).To(Equal(tc.expectedPullSpec))
			}
		})
	}
}
