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
