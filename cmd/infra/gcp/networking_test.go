package gcp

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/api/googleapi"
)

func TestIsNotFoundError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"When no error occurs, it should return false", nil, false},
		{"When GCP returns 404, it should return true", &googleapi.Error{Code: 404}, true},
		{"When a GCP 404 is wrapped, it should return true", fmt.Errorf("lookup: %w", &googleapi.Error{Code: 404}), true},
		{"When GCP denies access, it should return false", &googleapi.Error{Code: 403}, false},
		{"When a different error occurs, it should return false", errors.New("not found"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNotFoundError(tc.err); got != tc.want {
				t.Fatalf("isNotFoundError(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}
