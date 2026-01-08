package util

import (
	"context"
	"testing"
	"unicode/utf8"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"

	imagev1 "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apiversion "k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakekubeclient "k8s.io/client-go/kubernetes/fake"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCompressDecompress(t *testing.T) {
	testCases := []struct {
		name       string
		payload    []byte
		compressed []byte
	}{
		{
			name:       "Text",
			payload:    []byte("The quick brown fox jumps over the lazy dog."),
			compressed: []byte("H4sIAAAAAAAC/wrJSFUoLM1MzlZIKsovz1NIy69QyCrNLShWyC9LLVIoyUhVyEmsqlRIyU/XAwQAAP//6SWQUSwAAAA="),
		},
		{
			name:       "Empty",
			payload:    []byte{},
			compressed: []byte{},
		},
		{
			name:       "Nil",
			payload:    nil,
			compressed: nil,
		},
	}

	t.Parallel()

	for _, tc := range testCases {
		t.Run(tc.name+" Valid Input", func(t *testing.T) {
			testCompressFunc(t, tc.payload, tc.compressed)
			testDecompressFunc(t, tc.payload, tc.compressed)
		})

		// Empty or nil inputs will not produce an error, which is what this test
		// looks for. Instead, they will produce a nil or empty result within
		// their initialized bytes.Buffer object.
		if len(tc.payload) != 0 {
			t.Run(tc.name+" Invalid Input", func(t *testing.T) {
				testDecompressFuncErr(t, tc.payload)
			})
		}
	}
}

func TestConvertRegistryOverridesToCommandLineFlag(t *testing.T) {
	testCases := []struct {
		name              string
		registryOverrides map[string]string
		expectedFlag      string
	}{
		{
			name:         "No registry overrides",
			expectedFlag: "=",
		},
		{
			name: "Registry overrides with single mirrors",
			registryOverrides: map[string]string{
				"registry1": "mirror1.1",
				"registry2": "mirror2.1",
				"registry3": "mirror3.1",
			},
			expectedFlag: "registry1=mirror1.1,registry2=mirror2.1,registry3=mirror3.1",
		},
	}

	t.Parallel()

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			result := ConvertRegistryOverridesToCommandLineFlag(testCase.registryOverrides)
			g.Expect(result).To(Equal(testCase.expectedFlag))
		})
	}
}

func TestConvertOpenShiftImageRegistryOverridesToCommandLineFlag(t *testing.T) {
	testCases := []struct {
		name              string
		registryOverrides map[string][]string
		expectedFlag      string
	}{
		{
			name:         "No registry overrides",
			expectedFlag: "=",
		},
		{
			name: "Registry overrides with single mirrors",
			registryOverrides: map[string][]string{
				"registry1": {
					"mirror1.1",
				},
				"registry2": {
					"mirror2.1",
				},
				"registry3": {
					"mirror3.1",
				},
			},
			expectedFlag: "registry1=mirror1.1,registry2=mirror2.1,registry3=mirror3.1",
		},
		{
			name: "Registry overrides with multiple mirrors",
			registryOverrides: map[string][]string{
				"registry1": {
					"mirror1.1",
					"mirror1.2",
					"mirror1.3",
				},
				"registry2": {
					"mirror2.1",
					"mirror2.2",
				},
				"registry3": {
					"mirror3.1",
				},
			},
			expectedFlag: "registry1=mirror1.1,registry1=mirror1.2,registry1=mirror1.3,registry2=mirror2.1,registry2=mirror2.2,registry3=mirror3.1",
		},
	}

	t.Parallel()

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			result := ConvertOpenShiftImageRegistryOverridesToCommandLineFlag(testCase.registryOverrides)
			g.Expect(result).To(Equal(testCase.expectedFlag))
		})
	}
}

func TestConvertImageRegistryOverrideStringToMap(t *testing.T) {
	testCases := []struct {
		name           string
		expectedOutput map[string][]string
		input          string
	}{
		{
			name:  "Empty string",
			input: "",
		},
		{
			name:  "No registry overrides",
			input: "=",
			//expectedOutput: make(map[string][]string),
		},
		{
			name: "Registry overrides with single mirrors",
			expectedOutput: map[string][]string{
				"registry1": {
					"mirror1.1",
				},
				"registry2": {
					"mirror2.1",
				},
				"registry3": {
					"mirror3.1",
				},
			},

			input: "registry1=mirror1.1,registry2=mirror2.1,registry3=mirror3.1",
		},
		{
			name: "Registry overrides with multiple mirrors",
			expectedOutput: map[string][]string{
				"registry1": {
					"mirror1.1",
					"mirror1.2",
					"mirror1.3",
				},
				"registry2": {
					"mirror2.1",
					"mirror2.2",
				},
				"registry3": {
					"mirror3.1",
				},
			},
			input: "registry1=mirror1.1,registry1=mirror1.2,registry1=mirror1.3,registry2=mirror2.1,registry2=mirror2.2,registry3=mirror3.1",
		},
	}

	t.Parallel()

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			result := ConvertImageRegistryOverrideStringToMap(testCase.input)
			g.Expect(result).To(Equal(testCase.expectedOutput))
		})
	}
}

// Tests that a given input can be expected and encoded without errors.
func testCompressFunc(t *testing.T, payload, expected []byte) {
	t.Helper()

	g := NewWithT(t)

	result, err := CompressAndEncode(payload)
	g.Expect(err).To(BeNil(), "should be no compression errors")
	g.Expect(result).ToNot(BeNil(), "should always return an initialized bytes.Buffer")

	resultBytes := result.Bytes()
	resultString := result.String()

	g.Expect(utf8.Valid(resultBytes)).To(BeTrue(), "expected output should be a valid utf-8 sequence")
	g.Expect(resultBytes).To(Equal(expected), "expected bytes should equal expected")
	g.Expect(resultString).To(Equal(string(expected)), "expected strings should equal expected")
}

// Tests that a given output can be decoded and decompressed without errors.
func testDecompressFunc(t *testing.T, payload, expected []byte) {
	t.Helper()

	g := NewWithT(t)

	result, err := DecodeAndDecompress(expected)
	g.Expect(err).To(BeNil(), "should be no decompression errors")
	g.Expect(result).ToNot(BeNil(), "should always return an initialized bytes.Buffer")

	resultBytes := result.Bytes()
	resultString := result.String()

	g.Expect(resultBytes).To(Equal(payload), "deexpected bytes should equal expected")
	g.Expect(resultString).To(Equal(string(payload)), "deexpected string should equal expected")
}

// Tests that an invalid decompression input (not gzipped and base64-encoded)
// will produce an error.
func testDecompressFuncErr(t *testing.T, payload []byte) {
	out, err := DecodeAndDecompress(payload)

	g := NewWithT(t)
	g.Expect(err).ToNot(BeNil(), "should be an error")
	g.Expect(out).ToNot(BeNil(), "should return an initialized bytes.Buffer")
	g.Expect(out.Bytes()).To(BeNil(), "should be a nil byte slice")
	g.Expect(out.String()).To(BeEmpty(), "should be an empty string")
}

func TestFirstUsableIP(t *testing.T) {
	tests := []struct {
		name    string
		cidr    string
		want    string
		wantErr bool
	}{
		{
			name:    "Given IPv4 CIDR, it should return the first ip of the network range",
			cidr:    "192.168.1.0/24",
			want:    "192.168.1.1",
			wantErr: false,
		},
		{
			name:    "Given IPv6 CIDR, it should return the first ip of the network range",
			cidr:    "2000::/3",
			want:    "2000::1",
			wantErr: false,
		},
		{
			name:    "Given a malformed IPv4 CIDR, it should return empty string and err",
			cidr:    "192.168.1.35.53/24",
			want:    "",
			wantErr: true,
		},
		{
			name:    "Given a malformed IPv6 CIDR, it should return empty string and err",
			cidr:    "2001::44444444444444/17",
			want:    "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FirstUsableIP(tt.cidr)
			if (err != nil) != tt.wantErr {
				t.Errorf("FirstUsableIP() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("FirstUsableIP() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseNodeSelector(t *testing.T) {
	tests := []struct {
		name string
		str  string
		want map[string]string
	}{
		{
			name: "Given a valid node selector string, it should return a map of key value pairs",
			str:  "key1=value1,key2=value2,key3=value3",
			want: map[string]string{
				"key1": "value1",
				"key2": "value2",
				"key3": "value3",
			},
		},
		{
			name: "Given a valid node selector string with empty values, it should return a map of key value pairs",
			str:  "key1=,key2=value2,key3=",
			want: map[string]string{
				"key2": "value2",
			},
		},
		{
			name: "Given a valid node selector string with empty keys, it should return a map of key value pairs",
			str:  "=value1,key2=value2,=value3",
			want: map[string]string{
				"key2": "value2",
			},
		},
		{
			name: "Given a valid node selector string with empty string, it should return an empty map",
			str:  "",
			want: nil,
		},
		{
			name: "Given a valid node selector string with invalid key value pairs, it should return a map of key value pairs",
			str:  "key1=value1,key2,key3=value3",
			want: map[string]string{
				"key1": "value1",
				"key3": "value3",
			},
		},
		{
			name: "Given a valid node selector string with values that include =, it should return a map of key value pairs",
			str:  "key1=value1=one,key2,key3=value3=three",
			want: map[string]string{
				"key1": "value1=one",
				"key3": "value3=three",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			got := ParseNodeSelector(tt.str)
			g.Expect(got).To(Equal(tt.want))
		})
	}
}

func TestSanitizeIgnitionPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		wantErr bool
	}{
		{
			name:    "Simple valid Ignition payload",
			payload: []byte(`{"ignition": {"version": "3.0.0"}}`),
			wantErr: false,
		},
		{
			name:    "More complex valid Ignition payload",
			payload: []byte(`{"ignition":{"version":"3.0.0"},"storage":{"files":[{"path":"/etc/someconfig","mode":420,"contents":{"source":"data:,example%20file%0A"}}]}}`),
			wantErr: false,
		},
		{
			name:    "Simple invalid Ignition payload (missing closing brace)",
			payload: []byte(`{"ignition": {"version": "3.0.0"`),
			wantErr: true,
		},
		{
			name:    "Empty payload",
			payload: []byte(``),
			wantErr: true,
		},
		{
			name:    "Nil payload",
			payload: nil,
			wantErr: true,
		},
	}

	t.Parallel()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			err := SanitizeIgnitionPayload(tt.payload)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestGetMgmtClusterCPUArch(t *testing.T) {
	fakeKubeClient := fakekubeclient.NewSimpleClientset()
	fakeDiscovery, ok := fakeKubeClient.Discovery().(*fakediscovery.FakeDiscovery)

	if !ok {
		t.Fatalf("failed to convert FakeDiscovery")
	}

	// if you want to fake a specific version
	fakeDiscovery.FakedServerVersion = &apiversion.Info{
		Platform: "linux/amd64",
	}

	tests := []struct {
		Name         string
		expectedArch string
		expectedErr  bool
	}{
		{
			Name:         "Nominal use case",
			expectedArch: "amd64",
			expectedErr:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			g := NewWithT(t)
			mgmtClusterArch, err := GetMgmtClusterCPUArch(fakeKubeClient)
			if tc.expectedErr {
				g.Expect(err).ToNot(BeNil())
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(mgmtClusterArch).To(Equal(tc.expectedArch))
			}

		})
	}
}

func TestGetPullSecretBytes(t *testing.T) {
	testCases := []struct {
		name      string
		hc        *hyperv1.HostedCluster
		secret    *corev1.Secret
		expectErr bool
	}{
		{
			name: "HC has right pull secret info; no err",
			hc: &hyperv1.HostedCluster{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "test",
				},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pull-secret",
					Namespace: "test",
				},
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: []byte("mysecret"),
				},
			},
			expectErr: false,
		},
		{
			name: "HC has wrong pull secret name; err",
			hc: &hyperv1.HostedCluster{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "test",
				},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{Name: "pull-secrett"},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pull-secret",
					Namespace: "test",
				},
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: []byte("mysecret"),
				},
			},
			expectErr: true,
		},
		{
			name: "HC has right pull secret name; pull secret missing key; err",
			hc: &hyperv1.HostedCluster{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "test",
				},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pull-secret",
					Namespace: "test",
				},
				Data: map[string][]byte{
					"wrong-key": []byte("mysecret"),
				},
			},
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			objs := []crclient.Object{tc.hc, tc.secret}

			client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objs...).Build()

			pullSecretBytes, err := GetPullSecretBytes(t.Context(), client, tc.hc)
			if !tc.expectErr {
				g.Expect(err).To(BeNil())
				g.Expect(pullSecretBytes).To(Equal(tc.secret.Data[corev1.DockerConfigJsonKey]))
			} else {
				g.Expect(err).To(HaveOccurred())
			}
		})
	}
}

func TestGetImageArchitecture(t *testing.T) {
	pullSecretBytes := []byte("{\"auths\":{\"quay.io\":{\"auth\":\"\",\"email\":\"\"}}}")

	testCases := []struct {
		name                  string
		image                 string
		pullSecretBytes       []byte
		imageMetadataProvider *RegistryClientImageMetadataProvider
		expectedArch          hyperv1.PayloadArchType
		expectErr             bool
	}{
		{
			name:                  "Bad pull secret, cache empty; err",
			image:                 "quay.io/openshift-release-dev/ocp-release:4.16.11-ppc64le",
			pullSecretBytes:       []byte(""),
			imageMetadataProvider: &RegistryClientImageMetadataProvider{},
			expectedArch:          "",
			expectErr:             true,
		},
		{
			name:                  "Get amd64 from amd64 image; no err",
			image:                 "quay.io/openshift-release-dev/ocp-release:4.16.10-x86_64",
			pullSecretBytes:       pullSecretBytes,
			imageMetadataProvider: &RegistryClientImageMetadataProvider{},
			expectedArch:          hyperv1.AMD64,
			expectErr:             false,
		},
		{
			name:                  "Get ppc64le from ppc64le image; no err",
			image:                 "quay.io/openshift-release-dev/ocp-release:4.16.11-ppc64le",
			pullSecretBytes:       pullSecretBytes,
			imageMetadataProvider: &RegistryClientImageMetadataProvider{},
			expectedArch:          hyperv1.PPC64LE,
			expectErr:             false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			arch, err := getImageArchitecture(t.Context(), tc.image, tc.pullSecretBytes, tc.imageMetadataProvider)
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(arch).To(Equal(tc.expectedArch))
			}
		})
	}
}

func TestDetermineHostedClusterPayloadArch(t *testing.T) {
	pullSecretBytes := []byte("{\"auths\":{\"quay.io\":{\"auth\":\"\",\"email\":\"\"}}}")

	testCases := []struct {
		name                  string
		hc                    *hyperv1.HostedCluster
		secret                *corev1.Secret
		imageMetadataProvider *RegistryClientImageMetadataProvider
		expectedPayloadType   hyperv1.PayloadArchType
		expectErr             bool
	}{
		{
			name: "Get amd64 from amd64 image; no err",
			hc: &hyperv1.HostedCluster{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "test",
				},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
					Release: hyperv1.Release{
						Image: "quay.io/openshift-release-dev/ocp-release:4.16.10-x86_64",
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pull-secret",
					Namespace: "test",
				},
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: pullSecretBytes,
				},
			},
			imageMetadataProvider: &RegistryClientImageMetadataProvider{},
			expectedPayloadType:   hyperv1.AMD64,
			expectErr:             false,
		},
		{
			name: "Get multi payload from multi image; no err",
			hc: &hyperv1.HostedCluster{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "test",
				},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
					Release: hyperv1.Release{
						Image: "quay.io/openshift-release-dev/ocp-release:4.16.11-multi",
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pull-secret",
					Namespace: "test",
				},
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: pullSecretBytes,
				},
			},
			imageMetadataProvider: &RegistryClientImageMetadataProvider{},
			expectedPayloadType:   hyperv1.Multi,
			expectErr:             false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			objs := []crclient.Object{tc.hc, tc.secret}

			client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objs...).Build()

			payloadType, err := DetermineHostedClusterPayloadArch(t.Context(), client, tc.hc, tc.imageMetadataProvider)
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(payloadType).To(Equal(tc.expectedPayloadType))
			}
		})
	}
}

func TestIsIPv4CIDR(t *testing.T) {
	tests := []struct {
		input       string
		expected    bool
		expectError bool
	}{
		// Valid IPv4 CIDRs
		{"192.168.1.0/24", true, false},
		{"10.0.0.0/8", true, false},

		// Valid IPv6 CIDRs
		{"2001:db8::/32", false, false},
		{"fd00::/8", false, false},

		// Invalid inputs
		{"invalid", false, true},
		{"192.168.1.1/33", false, true},  // Invalid CIDR prefix
		{"", false, true},                // Empty input
		{"1234::5678::/64", false, true}, // Malformed IP

		// Edge cases
		{"0.0.0.0/0", true, false},
		{"255.255.255.255/32", true, false},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			g := NewWithT(t)
			result, err := IsIPv4CIDR(test.input)
			if test.expectError {
				g.Expect(err).To(HaveOccurred(), "Expected an error for input '%s'", test.input)
			} else {
				g.Expect(err).ToNot(HaveOccurred(), "Did not expect an error for input '%s'", test.input)
			}

			g.Expect(result).To(Equal(test.expected), "Unexpected result for input '%s'", test.input)
		})
	}
}

func TestIsIPv4Address(t *testing.T) {
	tests := []struct {
		input       string
		expected    bool
		expectError bool
	}{
		// Valid IPv4 addresses
		{"192.168.1.1", true, false},
		{"10.0.0.1", true, false},

		// Valid IPv6 addresses
		{"2001:db8::1", false, false},
		{"fd00::1", false, false},

		// Invalid inputs
		{"invalid", false, true},
		{"192.168.1.256", false, true}, // Invalid IPv4 address
		{"", false, true},              // Empty input
		{"1234::5678::1", false, true}, // Malformed IP

		// Edge cases
		{"0.0.0.0", true, false},
		{"255.255.255.255", true, false},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			g := NewWithT(t)
			result, err := IsIPv4Address(test.input)
			if test.expectError {
				g.Expect(err).To(HaveOccurred(), "Expected an error for input '%s'", test.input)
			} else {
				g.Expect(err).ToNot(HaveOccurred(), "Did not expect an error for input '%s'", test.input)
			}

			g.Expect(result).To(Equal(test.expected), "Unexpected result for input '%s'", test.input)
		})
	}
}

func TestRemoveEmptyJSONField(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		field    string
		expected string
	}{
		{
			name:     "Remove empty field from JSON - at the end",
			input:    `{"field1": "value1", "field2": ""}`,
			field:    "field2",
			expected: `{"field1": "value1"}`,
		},
		{
			name:     "Remove empty field from JSON - at the beginning",
			input:    `{"field1": "", "field2": "value2"}`,
			field:    "field1",
			expected: `{"field2": "value2"}`,
		},
		{
			name:     "Remove empty field from JSON - in the middle",
			input:    `{"field1": "value1", "field2": "", "field3": "value3"}`,
			field:    "field2",
			expected: `{"field1": "value1", "field3": "value3"}`,
		},
		{
			name:     "Remove empty field from JSON - without spaces - at the beginning",
			input:    `{"field1":"","field2":"value2"}`,
			field:    "field1",
			expected: `{"field2":"value2"}`,
		},
		{
			name:     "Remove empty field from JSON - without spaces - in the middle",
			input:    `{"field1":"value1","field2":"","field3":"value3"}`,
			field:    "field2",
			expected: `{"field1":"value1","field3":"value3"}`,
		},
		{
			name:     "Remove empty field from JSON - without spaces - at the end",
			input:    `{"field1":"value1","field2":""}`,
			field:    "field2",
			expected: `{"field1":"value1"}`,
		},

		{
			name:     "Keep non-empty field from JSON",
			input:    `{"field1": "value1", "field2": "value2"}`,
			field:    "field2",
			expected: `{"field1": "value1", "field2": "value2"}`,
		},
		{
			name:     "Remove non-existent field from JSON returns the same JSON",
			input:    `{"field1": "value1"}`,
			field:    "field2",
			expected: `{"field1": "value1"}`,
		},
		{
			name:     "Empty JSON returns empty JSON",
			input:    `{}`,
			field:    "field1",
			expected: `{}`,
		},
		{
			name:     "Empty JSON returns empty JSON - empty field",
			input:    `{}`,
			field:    "",
			expected: `{}`,
		},
		{
			name:     "Remove nested empty field from JSON",
			input:    `{"field1": "value1", "field2": {"field3": ""}}`,
			field:    "field3",
			expected: `{"field1": "value1", "field2": {}}`,
		},
		{
			name:     "Remove nested empty field from JSON - in the middle",
			input:    `{"field1": "value1", "field2": {"field3": "value3", "field4": "value4", "field5": ""}}`,
			field:    "field5",
			expected: `{"field1": "value1", "field2": {"field3": "value3", "field4": "value4"}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			result := RemoveEmptyJSONField(test.input, test.field)
			g.Expect(result).To(Equal(test.expected))
		})
	}
}

func TestHostFromURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		wantErr  bool
	}{
		{"http://example.com", "example.com", false},
		{"https://example.com:443", "example.com", false},
		{"http://localhost:8080", "localhost", false},
		{"https://127.0.0.1:9000", "127.0.0.1", false},
		{"ftp://example.org:21", "example.org", false},
		{"http://[::1]:8080", "::1", false},                // IPv6 localhost
		{"http://[2001:db8::1]:443", "2001:db8::1", false}, // IPv6 example
		{"??", "", true},           // Invalid URL
		{"http://:8080", "", true}, // Missing hostname
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			g := NewWithT(t)
			result, err := HostFromURL(tt.input)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestCountAvailableNodes(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name      string
		nodes     []corev1.Node
		expected  int32
		expectErr bool
	}{
		{
			name: "all nodes ready and schedulable",
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					Spec:       corev1.NodeSpec{Unschedulable: false},
					Status: corev1.NodeStatus{
						Conditions: []corev1.NodeCondition{
							{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node2"},
					Spec:       corev1.NodeSpec{Unschedulable: false},
					Status: corev1.NodeStatus{
						Conditions: []corev1.NodeCondition{
							{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
						},
					},
				},
			},
			expected: 2,
		},
		{
			name: "one node cordoned",
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					Spec:       corev1.NodeSpec{Unschedulable: false},
					Status: corev1.NodeStatus{
						Conditions: []corev1.NodeCondition{
							{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node2"},
					Spec:       corev1.NodeSpec{Unschedulable: true}, // cordoned
					Status: corev1.NodeStatus{
						Conditions: []corev1.NodeCondition{
							{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
						},
					},
				},
			},
			expected: 1,
		},
		{
			name: "one node not ready",
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					Spec:       corev1.NodeSpec{Unschedulable: false},
					Status: corev1.NodeStatus{
						Conditions: []corev1.NodeCondition{
							{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node2"},
					Spec:       corev1.NodeSpec{Unschedulable: false},
					Status: corev1.NodeStatus{
						Conditions: []corev1.NodeCondition{
							{Type: corev1.NodeReady, Status: corev1.ConditionFalse}, // not ready
						},
					},
				},
			},
			expected: 1,
		},
		{
			name:     "no nodes",
			nodes:    []corev1.Node{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]crclient.Object, len(tt.nodes))
			for i := range tt.nodes {
				objects[i] = &tt.nodes[i]
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			result, err := CountAvailableNodes(context.Background(), fakeClient)
			if tt.expectErr && err == nil {
				t.Errorf("expected error, got none")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %d available nodes, got %d", tt.expected, result)
			}
		})
	}
}

// testReleaseProvider is a simple fake release provider for testing GetControlPlaneOperatorImage
type testReleaseProvider struct {
	version    string
	components map[string]string
}

func (f *testReleaseProvider) Lookup(_ context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
	releaseImage := &releaseinfo.ReleaseImage{
		ImageStream: &imagev1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Name: f.version},
			Spec:       imagev1.ImageStreamSpec{},
		},
	}
	for name, image := range f.components {
		releaseImage.ImageStream.Spec.Tags = append(releaseImage.ImageStream.Spec.Tags, imagev1.TagReference{
			Name: name,
			From: &corev1.ObjectReference{Name: image},
		})
	}
	return releaseImage, nil
}

func TestGetControlPlaneOperatorImage(t *testing.T) {
	const (
		hoImage            = "quay.io/hypershift/hypershift-operator:latest"
		payloadCPOImage    = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:abc123"
		annotationCPOImage = "quay.io/custom/cpo:v1"
	)

	testCases := []struct {
		name                    string
		version                 string
		hostedClusterAnnotation map[string]string
		payloadHasHypershift    bool
		cpoBinaryExists         bool
		expectedImage           string
	}{
		{
			name:    "When annotation is set it should use annotation image",
			version: "4.20.0",
			hostedClusterAnnotation: map[string]string{
				hyperv1.ControlPlaneOperatorImageAnnotation: annotationCPOImage,
			},
			payloadHasHypershift: true,
			cpoBinaryExists:      true,
			expectedImage:        annotationCPOImage,
		},
		{
			name:                 "When version is 4.20 and CPO binary exists it should use HO image",
			version:              "4.20.0",
			payloadHasHypershift: true,
			cpoBinaryExists:      true,
			expectedImage:        hoImage,
		},
		{
			name:                 "When version is 4.21 and CPO binary exists it should use HO image",
			version:              "4.21.0",
			payloadHasHypershift: true,
			cpoBinaryExists:      true,
			expectedImage:        hoImage,
		},
		{
			name:                 "When version is 4.22 and CPO binary exists it should use HO image",
			version:              "4.22.5",
			payloadHasHypershift: true,
			cpoBinaryExists:      true,
			expectedImage:        hoImage,
		},
		{
			name:                 "When version is 4.20 but CPO binary does not exist it should use payload image",
			version:              "4.20.0",
			payloadHasHypershift: true,
			cpoBinaryExists:      false,
			expectedImage:        payloadCPOImage,
		},
		{
			name:                 "When version is 4.19 with payload hypershift it should use payload image",
			version:              "4.19.0",
			payloadHasHypershift: true,
			cpoBinaryExists:      true,
			expectedImage:        payloadCPOImage,
		},
		{
			name:                 "When version is 4.18 with payload hypershift it should use payload image",
			version:              "4.18.5",
			payloadHasHypershift: true,
			cpoBinaryExists:      true,
			expectedImage:        payloadCPOImage,
		},
		{
			name:                 "When version is 4.14 with payload hypershift it should use payload image",
			version:              "4.14.0",
			payloadHasHypershift: true,
			cpoBinaryExists:      true,
			expectedImage:        payloadCPOImage,
		},
		{
			name:                 "When version is 4.19 without payload hypershift it should fallback to HO image",
			version:              "4.19.0",
			payloadHasHypershift: false,
			cpoBinaryExists:      true,
			expectedImage:        hoImage,
		},
		{
			name:                 "When version is 4.10 without payload hypershift it should fallback to HO image",
			version:              "4.10.0",
			payloadHasHypershift: false,
			cpoBinaryExists:      true,
			expectedImage:        hoImage,
		},
		{
			name:                 "When version is 5.0 and CPO binary exists it should use HO image",
			version:              "5.0.0",
			payloadHasHypershift: true,
			cpoBinaryExists:      true,
			expectedImage:        hoImage,
		},
		{
			name:                 "When version is 4.20.0-rc.1 and CPO binary exists it should use HO image",
			version:              "4.20.0-rc.1",
			payloadHasHypershift: true,
			cpoBinaryExists:      true,
			expectedImage:        hoImage,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			// Set the CPO binary existence for this test case
			cpoBinaryExistsFunc = func() bool { return tc.cpoBinaryExists }
			defer func() { cpoBinaryExistsFunc = nil }()

			components := map[string]string{}
			if tc.payloadHasHypershift {
				components["hypershift"] = payloadCPOImage
			}

			releaseProvider := &testReleaseProvider{
				version:    tc.version,
				components: components,
			}

			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-cluster",
					Namespace:   "test-ns",
					Annotations: tc.hostedClusterAnnotation,
				},
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: "quay.io/openshift-release-dev/ocp-release:4.20.0-x86_64",
					},
				},
			}

			image, err := GetControlPlaneOperatorImage(context.Background(), hc, releaseProvider, hoImage, nil)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(image).To(Equal(tc.expectedImage))
		})
	}
}
