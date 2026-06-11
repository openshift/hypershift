package nodepool

import (
	"testing"

	"github.com/blang/semver"
)

func TestGetRHELStream(t *testing.T) {
	testCases := []struct {
		name           string
		specStream     string
		releaseVersion semver.Version
		usesRunc       bool
		expected       string
		expectError    bool
	}{
		{
			name:           "When specStream is rhel-10 and usesRunc is true it should return an error",
			specStream:     "rhel-10",
			releaseVersion: semver.Version{Major: 5, Minor: 0, Patch: 0},
			usesRunc:       true,
			expected:       "",
			expectError:    true,
		},
		{
			name:           "When specStream is rhel-10 and release version is less than 5.0 it should return an error",
			specStream:     "rhel-10",
			releaseVersion: semver.Version{Major: 4, Minor: 18, Patch: 0},
			usesRunc:       false,
			expected:       "",
			expectError:    true,
		},
		{
			name:           "When specStream is rhel-10 and release version is 5.0 or greater it should return rhel-10",
			specStream:     "rhel-10",
			releaseVersion: semver.Version{Major: 5, Minor: 0, Patch: 0},
			usesRunc:       false,
			expected:       "rhel-10",
			expectError:    false,
		},
		{
			name:           "When specStream is rhel-10 and release version is greater than 5.0 it should return rhel-10",
			specStream:     "rhel-10",
			releaseVersion: semver.Version{Major: 5, Minor: 2, Patch: 1},
			usesRunc:       false,
			expected:       "rhel-10",
			expectError:    false,
		},
		{
			name:           "When specStream is rhel-9 and release version is 5.0 or greater it should return rhel-9",
			specStream:     "rhel-9",
			releaseVersion: semver.Version{Major: 5, Minor: 0, Patch: 0},
			usesRunc:       false,
			expected:       "rhel-9",
			expectError:    false,
		},
		{
			name:           "When specStream is rhel-9 and release version is less than 5.0 it should return rhel-9",
			specStream:     "rhel-9",
			releaseVersion: semver.Version{Major: 4, Minor: 17, Patch: 0},
			usesRunc:       false,
			expected:       "rhel-9",
			expectError:    false,
		},
		{
			name:           "When specStream is rhel-9 and usesRunc is true it should return rhel-9",
			specStream:     "rhel-9",
			releaseVersion: semver.Version{Major: 5, Minor: 0, Patch: 0},
			usesRunc:       true,
			expected:       "rhel-9",
			expectError:    false,
		},
		{
			name:           "When specStream is unset and release version is 5.0 or greater and usesRunc is true it should return rhel-9",
			specStream:     "",
			releaseVersion: semver.Version{Major: 5, Minor: 0, Patch: 0},
			usesRunc:       true,
			expected:       "rhel-9",
			expectError:    false,
		},
		{
			name:           "When specStream is unset and release version is 5.0 or greater and usesRunc is false it should return rhel-10",
			specStream:     "",
			releaseVersion: semver.Version{Major: 5, Minor: 0, Patch: 0},
			usesRunc:       false,
			expected:       "rhel-10",
			expectError:    false,
		},
		{
			name:           "When specStream is unset and release version is greater than 5.0 and usesRunc is false it should return rhel-10",
			specStream:     "",
			releaseVersion: semver.Version{Major: 5, Minor: 3, Patch: 0},
			usesRunc:       false,
			expected:       "rhel-10",
			expectError:    false,
		},
		{
			name:           "When specStream is unset and release version is less than 5.0 it should return empty string",
			specStream:     "",
			releaseVersion: semver.Version{Major: 4, Minor: 18, Patch: 0},
			usesRunc:       false,
			expected:       "",
			expectError:    false,
		},
		{
			name:           "When specStream is unset and release version is less than 5.0 and usesRunc is true it should return empty string",
			specStream:     "",
			releaseVersion: semver.Version{Major: 4, Minor: 16, Patch: 5},
			usesRunc:       true,
			expected:       "",
			expectError:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := getRHELStream(tc.specStream, tc.releaseVersion, tc.usesRunc)
			if tc.expectError {
				if err == nil {
					t.Fatalf("expected an error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, result)
			}
		})
	}
}
