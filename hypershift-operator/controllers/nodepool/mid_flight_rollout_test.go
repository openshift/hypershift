package nodepool

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestExtractHashFromSecretName(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name       string
		secretName string
		expected   string
	}{
		{
			name:       "When given a valid user-data secret name, it should extract the hash",
			secretName: "user-data-my-nodepool-abc123",
			expected:   "abc123",
		},
		{
			name:       "When given a user-data secret with hyphenated nodepool name, it should extract the last segment as hash",
			secretName: "user-data-my-test-nodepool-xyz789",
			expected:   "xyz789",
		},
		{
			name:       "When given a valid token secret name, it should extract the hash",
			secretName: "token-secret-my-nodepool-def456",
			expected:   "def456",
		},
		{
			name:       "When the secret name has too few parts, it should return empty",
			secretName: "user-data",
			expected:   "",
		},
		{
			name:       "When the secret name is empty, it should return empty",
			secretName: "",
			expected:   "",
		},
		{
			name:       "When the secret name has only three parts, it should return empty",
			secretName: "user-data-nodepool",
			expected:   "",
		},
		{
			name:       "When the secret name has exactly four parts, it should extract the hash",
			secretName: "user-data-np-hash123",
			expected:   "hash123",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			result := extractHashFromSecretName(tc.secretName)
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}
