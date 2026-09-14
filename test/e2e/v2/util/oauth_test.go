//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"net/http"
	"strings"
	"testing"
)

func TestExtractAccessToken(t *testing.T) {
	tests := []struct {
		name      string
		location  string
		wantToken string
		wantError string
	}{
		{
			name:      "When the redirect fragment contains an access token, it should return the token",
			location:  "https://oauth.example/callback#access_token=token-value&state=state-value",
			wantToken: "token-value",
		},
		{
			name:      "When the redirect fragment has no access token, it should return an error",
			location:  "https://oauth.example/callback#state=state-value",
			wantError: "access_token not found",
		},
		{
			name:      "When the redirect fragment is malformed, it should return a parse error",
			location:  "https://oauth.example/callback#%zz",
			wantError: "invalid URL escape",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := &http.Response{Header: http.Header{"Location": []string{tt.location}}}
			got, err := extractAccessToken(response)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("extractAccessToken() error = %v, want error containing %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("extractAccessToken() returned an unexpected error: %v", err)
			}
			if got != tt.wantToken {
				t.Fatalf("extractAccessToken() = %q, want %q", got, tt.wantToken)
			}
		})
	}
}
