package velerocreds

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestParseDotenv(t *testing.T) {
	tests := []struct {
		name         string
		data         string
		wantClientID string
		wantSecret   string
		wantTenantID string
	}{
		{
			name: "When blob is empty it returns empty fields",
			data: "",
		},
		{
			name:         "When only AZURE_CLIENT_ID is present it extracts it (workload identity shape)",
			data:         "AZURE_SUBSCRIPTION_ID=sub-123\nAZURE_TENANT_ID=tenant-456\nAZURE_CLIENT_ID=client-789\n",
			wantClientID: "client-789",
			wantTenantID: "tenant-456",
		},
		{
			name:         "When client id and secret are present it extracts both (client-secret shape)",
			data:         "AZURE_CLIENT_ID=client-789\nAZURE_CLIENT_SECRET=sp-secret\nAZURE_TENANT_ID=tenant-456\n",
			wantClientID: "client-789",
			wantSecret:   "sp-secret",
			wantTenantID: "tenant-456",
		},
		{
			name:         "When lines use CRLF endings it trims them",
			data:         "AZURE_CLIENT_ID=client-789\r\nAZURE_CLIENT_SECRET=sp-secret\r\nAZURE_TENANT_ID=tenant-456\r\n",
			wantClientID: "client-789",
			wantSecret:   "sp-secret",
			wantTenantID: "tenant-456",
		},
		{
			name:         "When a value is only whitespace it is trimmed to empty",
			data:         "AZURE_CLIENT_ID=   \nAZURE_TENANT_ID=tenant-456\n",
			wantTenantID: "tenant-456",
		},
		{
			name:         "When a value contains '=' it keeps the full value after the first key prefix",
			data:         "AZURE_CLIENT_SECRET=abc==def==\nAZURE_CLIENT_ID=client-789\n",
			wantClientID: "client-789",
			wantSecret:   "abc==def==",
		},
		{
			name:         "When a key is duplicated the last occurrence wins",
			data:         "AZURE_CLIENT_ID=first\nAZURE_CLIENT_ID=second\n",
			wantClientID: "second",
		},
		{
			name:         "When there is no trailing newline it still parses the final line",
			data:         "AZURE_CLIENT_ID=client-789",
			wantClientID: "client-789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			got := ParseDotenv([]byte(tt.data))
			g.Expect(got.ClientID).To(Equal(tt.wantClientID))
			g.Expect(got.ClientSecret).To(Equal(tt.wantSecret))
			g.Expect(got.TenantID).To(Equal(tt.wantTenantID))
		})
	}
}
