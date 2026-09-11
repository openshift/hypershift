package velerocreds

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestParseDotenv(t *testing.T) {
	tests := []struct {
		name          string
		data          string
		wantClientID  string
		wantSecret    string
		wantTenantID  string
		wantCloudName string
	}{
		{
			name:          "When blob is empty it returns empty fields with default cloud",
			data:          "",
			wantCloudName: DefaultCloudName,
		},
		{
			name:          "When only AZURE_CLIENT_ID is present it extracts it (workload identity shape)",
			data:          "AZURE_SUBSCRIPTION_ID=sub-123\nAZURE_TENANT_ID=tenant-456\nAZURE_CLIENT_ID=client-789\n",
			wantClientID:  "client-789",
			wantTenantID:  "tenant-456",
			wantCloudName: DefaultCloudName,
		},
		{
			name:          "When client id and secret are present it extracts both (client-secret shape)",
			data:          "AZURE_CLIENT_ID=client-789\nAZURE_CLIENT_SECRET=sp-secret\nAZURE_TENANT_ID=tenant-456\n",
			wantClientID:  "client-789",
			wantSecret:    "sp-secret",
			wantTenantID:  "tenant-456",
			wantCloudName: DefaultCloudName,
		},
		{
			name:          "When AZURE_CLOUD_NAME is set it overrides the default",
			data:          "AZURE_CLIENT_ID=client-789\nAZURE_CLOUD_NAME=AzureUSGovernmentCloud\n",
			wantClientID:  "client-789",
			wantCloudName: "AzureUSGovernmentCloud",
		},
		{
			name:          "When AZURE_CLOUD_NAME is empty it keeps the default",
			data:          "AZURE_CLIENT_ID=client-789\nAZURE_CLOUD_NAME=\n",
			wantClientID:  "client-789",
			wantCloudName: DefaultCloudName,
		},
		{
			name:          "When lines use CRLF endings it trims them",
			data:          "AZURE_CLIENT_ID=client-789\r\nAZURE_CLIENT_SECRET=sp-secret\r\nAZURE_TENANT_ID=tenant-456\r\n",
			wantClientID:  "client-789",
			wantSecret:    "sp-secret",
			wantTenantID:  "tenant-456",
			wantCloudName: DefaultCloudName,
		},
		{
			name:          "When a value is only whitespace it is trimmed to empty",
			data:          "AZURE_CLIENT_ID=   \nAZURE_TENANT_ID=tenant-456\n",
			wantTenantID:  "tenant-456",
			wantCloudName: DefaultCloudName,
		},
		{
			name:          "When a value contains '=' it keeps the full value after the first key prefix",
			data:          "AZURE_CLIENT_SECRET=abc==def==\nAZURE_CLIENT_ID=client-789\n",
			wantClientID:  "client-789",
			wantSecret:    "abc==def==",
			wantCloudName: DefaultCloudName,
		},
		{
			name:          "When a key is duplicated the last occurrence wins",
			data:          "AZURE_CLIENT_ID=first\nAZURE_CLIENT_ID=second\n",
			wantClientID:  "second",
			wantCloudName: DefaultCloudName,
		},
		{
			name:          "When there is no trailing newline it still parses the final line",
			data:          "AZURE_CLIENT_ID=client-789",
			wantClientID:  "client-789",
			wantCloudName: DefaultCloudName,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			got := ParseDotenv([]byte(tt.data))
			g.Expect(got.ClientID).To(Equal(tt.wantClientID))
			g.Expect(got.ClientSecret).To(Equal(tt.wantSecret))
			g.Expect(got.TenantID).To(Equal(tt.wantTenantID))
			g.Expect(got.CloudName).To(Equal(tt.wantCloudName))
		})
	}
}
