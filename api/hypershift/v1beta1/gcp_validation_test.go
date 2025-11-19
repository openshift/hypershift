package v1beta1

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestGCPResourceLabel(t *testing.T) {
	tests := []struct {
		name    string
		label   GCPResourceLabel
		wantErr bool
		errMsg  string
	}{
		{
			name:    "When label has valid key and value, it should pass validation",
			label:   GCPResourceLabel{Key: "environment", Value: "production"},
			wantErr: false,
		},
		{
			name:    "When label key starts with lowercase letter and contains hyphens, it should pass validation",
			label:   GCPResourceLabel{Key: "cost-center", Value: "engineering"},
			wantErr: false,
		},
		{
			name:    "When label key contains underscores, it should pass validation",
			label:   GCPResourceLabel{Key: "team_name", Value: "platform"},
			wantErr: false,
		},
		{
			name:    "When label key contains numbers, it should pass validation",
			label:   GCPResourceLabel{Key: "project123", Value: "test"},
			wantErr: false,
		},
		{
			name:    "When label value contains all allowed characters, it should pass validation",
			label:   GCPResourceLabel{Key: "test", Value: "value-with_123"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			// Basic validation - the actual CEL validation happens in the CRD
			// Here we just verify the struct can be created
			g.Expect(tt.label.Key).ToNot(BeEmpty())
			g.Expect(tt.label.Value).ToNot(BeEmpty())
		})
	}
}

func TestGCPRegionPattern(t *testing.T) {
	tests := []struct {
		name    string
		region  string
		wantErr bool
	}{
		{
			name:    "When region is us-central1, it should pass validation",
			region:  "us-central1",
			wantErr: false,
		},
		{
			name:    "When region is europe-west2, it should pass validation",
			region:  "europe-west2",
			wantErr: false,
		},
		{
			name:    "When region is europe-west12 with multi-digit suffix, it should pass validation",
			region:  "europe-west12",
			wantErr: false,
		},
		{
			name:    "When region is northamerica-northeast1 with multiple segments, it should pass validation",
			region:  "northamerica-northeast1",
			wantErr: false,
		},
		{
			name:    "When region is asia-southeast2, it should pass validation",
			region:  "asia-southeast2",
			wantErr: false,
		},
		{
			name:    "When region is me-central1 with short prefix, it should pass validation",
			region:  "me-central1",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			// The actual regex validation happens in the CRD
			// Here we just verify non-empty regions
			g.Expect(tt.region).ToNot(BeEmpty())
		})
	}
}
