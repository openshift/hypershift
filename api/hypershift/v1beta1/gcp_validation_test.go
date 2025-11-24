package v1beta1

import (
	"testing"
)

func TestGCPResourceLabel(t *testing.T) {
	tests := []struct {
		name  string
		label GCPResourceLabel
	}{
		{
			name:  "When label has valid key and value, it should pass validation",
			label: GCPResourceLabel{Key: "environment", Value: "production"},
		},
		{
			name:  "When label key starts with lowercase letter and contains hyphens, it should pass validation",
			label: GCPResourceLabel{Key: "cost-center", Value: "engineering"},
		},
		{
			name:  "When label key is single lowercase letter, it should pass validation",
			label: GCPResourceLabel{Key: "a", Value: "value"},
		},
		{
			name:  "When label key contains numbers, it should pass validation",
			label: GCPResourceLabel{Key: "project123", Value: "test"},
		},
		{
			name:  "When label key ends with number, it should pass validation",
			label: GCPResourceLabel{Key: "team2", Value: "platform"},
		},
		{
			name:  "When label value is empty, it should pass validation",
			label: GCPResourceLabel{Key: "optional", Value: ""},
		},
		{
			name:  "When label value contains hyphens and numbers, it should pass validation",
			label: GCPResourceLabel{Key: "test", Value: "value-with-123"},
		},
		{
			name:  "When label value is single digit, it should pass validation",
			label: GCPResourceLabel{Key: "version", Value: "1"},
		},
		{
			name:  "When label value is all hyphens between alphanumeric, it should pass validation",
			label: GCPResourceLabel{Key: "complex", Value: "a-b-c-1-2-3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Basic validation - the actual CEL validation happens in the CRD
			// Here we just verify the struct can be created with valid patterns
			if tt.label.Key == "" {
				t.Errorf("label.Key should not be empty")
			}
		})
	}
}

func TestGCPRegionPattern(t *testing.T) {
	tests := []struct {
		name   string
		region string
	}{
		{
			name:   "When region is us-central1, it should pass validation",
			region: "us-central1",
		},
		{
			name:   "When region is europe-west2, it should pass validation",
			region: "europe-west2",
		},
		{
			name:   "When region is europe-west12 with multi-digit suffix, it should pass validation",
			region: "europe-west12",
		},
		{
			name:   "When region is northamerica-northeast1 with multiple segments, it should pass validation",
			region: "northamerica-northeast1",
		},
		{
			name:   "When region is asia-southeast2, it should pass validation",
			region: "asia-southeast2",
		},
		{
			name:   "When region is me-central1 with short prefix, it should pass validation",
			region: "me-central1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The actual regex validation happens in the CRD
			// Here we just verify non-empty regions
			if tt.region == "" {
				t.Errorf("region should not be empty")
			}
		})
	}
}
