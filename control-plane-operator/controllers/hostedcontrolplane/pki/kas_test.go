package pki

import "testing"

func TestAddBracketsIfIPv6(t *testing.T) {
	tests := []struct {
		name       string
		apiAddress string
		want       string
	}{
		{
			name:       "given ipv4, it should not have brackets",
			apiAddress: "192.168.1.1",
			want:       "192.168.1.1",
		},
		{
			name:       "given an URL, it should not have brackets",
			apiAddress: "https://test.tld:8451",
			want:       "https://test.tld:8451",
		},
		{
			name:       "given another URL sample, it should not have brackets",
			apiAddress: "https://test",
			want:       "https://test",
		},
		{
			name:       "given an URL, it should not have brackets",
			apiAddress: "test.tld:8451",
			want:       "test.tld:8451",
		},
		{
			name:       "given simplified ipv6, it should return URL with brackets",
			apiAddress: "fd00::1",
			want:       "[fd00::1]",
		},
		{
			name:       "given an ipv6, it should return URL with brackets",
			apiAddress: "fd00:0000:0000:0000:0000:0000:1:99",
			want:       "[fd00:0000:0000:0000:0000:0000:1:99]",
		},
		{
			name:       "given wrong ipv6, it should return same URL without brackets",
			apiAddress: "fd00:0000:0000:0000:0000:0000:1:99000:00000000000",
			want:       "fd00:0000:0000:0000:0000:0000:1:99000:00000000000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := addBracketsIfIPv6(tt.apiAddress)
			if got != tt.want {
				t.Errorf("addBracketsIfIPv6() = %v, want %v", got, tt.want)
			}
		})
	}
}
