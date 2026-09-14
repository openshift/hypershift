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
	"errors"
	"net"
	"strings"
	"testing"
)

func TestFindFreeCIDR(t *testing.T) {
	_, searchSpace, err := net.ParseCIDR("10.0.192.0/29")
	if err != nil {
		t.Fatalf("failed to parse search space: %v", err)
	}
	_, occupiedBlock, err := net.ParseCIDR("10.0.192.0/30")
	if err != nil {
		t.Fatalf("failed to parse occupied block: %v", err)
	}

	tests := []struct {
		name      string
		occupied  []*net.IPNet
		wantCIDR  string
		wantError string
	}{
		{
			name:     "When the first candidate overlaps an existing subnet, it should return the next free block",
			occupied: []*net.IPNet{occupiedBlock},
			wantCIDR: "10.0.192.4/30",
		},
		{
			name:      "When every candidate overlaps an existing subnet, it should return an error",
			occupied:  []*net.IPNet{searchSpace},
			wantError: "no free /30 block found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findFreeCIDR(searchSpace, 30, tt.occupied)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("findFreeCIDR() error = %v, want error containing %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("findFreeCIDR() returned an unexpected error: %v", err)
			}
			if got != tt.wantCIDR {
				t.Fatalf("findFreeCIDR() = %q, want %q", got, tt.wantCIDR)
			}
		})
	}
}

func TestGenerateTestCIDRs250(t *testing.T) {
	cidrs := generateTestCIDRs250()
	if len(cidrs) != 250 {
		t.Fatalf("generateTestCIDRs250() returned %d CIDRs, want 250", len(cidrs))
	}
	if cidrs[0] != "250.250.250.1/32" {
		t.Fatalf("first generated CIDR = %q, want %q", cidrs[0], "250.250.250.1/32")
	}
	if cidrs[len(cidrs)-1] != "250.250.250.250/32" {
		t.Fatalf("last generated CIDR = %q, want %q", cidrs[len(cidrs)-1], "250.250.250.250/32")
	}
}

func TestCombineErrors(t *testing.T) {
	first := errors.New("first error")
	second := errors.New("second error")

	tests := []struct {
		name       string
		first      error
		second     error
		want       error
		wantString string
	}{
		{
			name:  "When only the first error exists, it should return the first error",
			first: first,
			want:  first,
		},
		{
			name:   "When only the second error exists, it should return the second error",
			second: second,
			want:   second,
		},
		{
			name:       "When both errors exist, it should include both error messages",
			first:      first,
			second:     second,
			wantString: "first error; second error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := combineErrors(tt.first, tt.second)
			if tt.want != nil {
				if !errors.Is(got, tt.want) {
					t.Fatalf("combineErrors() = %v, want %v", got, tt.want)
				}
				return
			}
			if got == nil || got.Error() != tt.wantString {
				t.Fatalf("combineErrors() = %v, want %q", got, tt.wantString)
			}
			if !errors.Is(got, tt.first) || !errors.Is(got, tt.second) {
				t.Fatalf("combineErrors() = %v, want both source errors to be discoverable", got)
			}
		})
	}
}
