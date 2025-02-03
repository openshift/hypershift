package secretproviderclass

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestFormatSecretProviderClassObject(t *testing.T) {
	testCases := []struct {
		name           string
		certName       string
		objectEncoding string
		expected       string
	}{
		{
			name:           "default",
			certName:       "cert",
			objectEncoding: "base64",
			expected: `
array:
  - |
    objectName: cert
    objectEncoding: base64
    objectType: secret
`,
		},
		{
			name:           "default",
			certName:       "cert",
			objectEncoding: "utf-8",
			expected: `
array:
  - |
    objectName: cert
    objectEncoding: utf-8
    objectType: secret
`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			actual := formatSecretProviderClassObject(tc.certName, tc.objectEncoding)
			g.Expect(actual).To(Equal(tc.expected))
		})
	}
}
