package aws

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestAWSKMSCredsSecret(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		namespace string
	}{
		{
			name:      "When AWSKMSCredsSecret is called, it should return a secret named kms-creds",
			namespace: "test-namespace",
		},
		{
			name:      "When AWSKMSCredsSecret is called with a namespace, it should set the namespace correctly",
			namespace: "my-control-plane",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			secret := AWSKMSCredsSecret(tc.namespace)
			g.Expect(secret).ToNot(BeNil())
			g.Expect(secret.Name).To(Equal("kms-creds"))
			g.Expect(secret.Namespace).To(Equal(tc.namespace))
		})
	}
}
