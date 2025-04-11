package util

import (
	"testing"
	"unicode/utf8"

	. "github.com/onsi/gomega"
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

func TestDoesMgmtClusterAndNodePoolCPUArchMatch(t *testing.T) {
	tests := []struct {
		name           string
		mgmtClusterCPU string
		nodePoolCPU    string
		wantErr        bool
	}{
		{
			name:           "Mgmt cluster cpu and nodepool cpu don't match",
			mgmtClusterCPU: "arm64",
			nodePoolCPU:    "amd64",
			wantErr:        true,
		},
		{
			name:           "Mgmt cluster cpu and nodepool cpu match",
			mgmtClusterCPU: "arm64",
			nodePoolCPU:    "arm64",
			wantErr:        false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			err := DoesMgmtClusterAndNodePoolCPUArchMatch(tc.mgmtClusterCPU, tc.nodePoolCPU)
			if tc.wantErr {
				g.Expect(err).ToNot(BeNil())
			} else {
				g.Expect(err).To(BeNil())
			}
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
