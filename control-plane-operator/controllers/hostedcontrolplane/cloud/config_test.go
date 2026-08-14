package cloud

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/aws"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/openstack"
)

func TestProviderConfigKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		provider string
		expected string
	}{
		{
			name:     "When provider is AWS, it should return aws.conf",
			provider: aws.Provider,
			expected: "aws.conf",
		},
		{
			name:     "When provider is Azure, it should return cloud.conf",
			provider: azure.Provider,
			expected: "cloud.conf",
		},
		{
			name:     "When provider is OpenStack, it should return the OpenStack cloud config key",
			provider: openstack.Provider,
			expected: openstack.CloudConfigKey,
		},
		{
			name:     "When provider is unknown, it should return empty string",
			provider: "unknown",
			expected: "",
		},
		{
			name:     "When provider is empty string, it should return empty string",
			provider: "",
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			result := ProviderConfigKey(tc.provider)
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}
