package reporter

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/openshift/hypershift/contrib/hcp-infra/pkg/scanner"
)

func buildTestInventory() *scanner.Inventory {
	inv := scanner.NewInventory("123456789012", []string{"us-east-1", "us-west-2"}, "tags")
	inv.ScannedAt = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

	// Add orphaned group
	inv.AddResource(scanner.Resource{
		Type:        scanner.ResNATGateway,
		ID:          "nat-0abc123",
		Region:      "us-east-1",
		InfraID:     "test-infra-abc",
		ClusterName: "ci-test-cluster",
		CreatedAt:   time.Now().Add(-16 * 24 * time.Hour),
		MonthlyCost: 32.0,
	})
	inv.AddResource(scanner.Resource{
		Type:    scanner.ResElasticIP,
		ID:      "eipalloc-0def456",
		Region:  "us-east-1",
		InfraID: "test-infra-abc",
		CreatedAt: time.Now().Add(-16 * 24 * time.Hour),
		MonthlyCost: 3.65,
	})

	// Add active group (with VPC)
	inv.AddResource(scanner.Resource{
		Type:        scanner.ResVPC,
		ID:          "vpc-0ghi789",
		Region:      "us-west-2",
		InfraID:     "prod-infra-xyz",
		ClusterName: "prod-cluster",
		CreatedAt:   time.Now().Add(-3 * 24 * time.Hour),
	})

	// Set categories
	for _, g := range inv.Groups {
		if g.InfraID == "test-infra-abc" {
			g.Category = scanner.CategoryConfirmedOrphan
		} else {
			g.Category = scanner.CategoryActive
		}
	}

	return inv
}

func TestTextReporter(t *testing.T) {
	inv := buildTestInventory()
	var buf bytes.Buffer
	rep := &TextReporter{}

	err := rep.Report(&buf, inv)
	if err != nil {
		t.Fatalf("Report() error: %v", err)
	}

	output := buf.String()

	// Check key elements
	checks := []string{
		"HyperShift Infrastructure Report",
		"123456789012",
		"ORPHANED",
		"ACTIVE",
		"test-infra-abc",
		"ci-test-cluster",
		"prod-infra-xyz",
		"16d",
		"~$36",
		"hcp-infra clean",
		"Summary:",
	}

	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output missing expected string %q", check)
		}
	}
}

func TestJSONReporter(t *testing.T) {
	inv := buildTestInventory()
	var buf bytes.Buffer
	rep := &JSONReporter{}

	err := rep.Report(&buf, inv)
	if err != nil {
		t.Fatalf("Report() error: %v", err)
	}

	output := buf.String()

	checks := []string{
		`"account_id"`,
		`"123456789012"`,
		`"infra_id"`,
		`"test-infra-abc"`,
		`"CONFIRMED ORPHAN"`,
	}

	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("JSON output missing expected string %q", check)
		}
	}
}

func TestMarkdownReporter(t *testing.T) {
	inv := buildTestInventory()
	var buf bytes.Buffer
	rep := &MarkdownReporter{}

	err := rep.Report(&buf, inv)
	if err != nil {
		t.Fatalf("Report() error: %v", err)
	}

	output := buf.String()

	checks := []string{
		"# HyperShift Infrastructure Report",
		"| Infra ID |",
		"test-infra-abc",
		"## ORPHANED",
		"## ACTIVE",
		"**Summary:**",
	}

	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("Markdown output missing expected string %q", check)
		}
	}
}

func TestNewReporterFactory(t *testing.T) {
	tests := []struct {
		format   string
		expected string
	}{
		{"text", "*reporter.TextReporter"},
		{"json", "*reporter.JSONReporter"},
		{"markdown", "*reporter.MarkdownReporter"},
		{"", "*reporter.TextReporter"},
		{"unknown", "*reporter.TextReporter"},
	}

	for _, tt := range tests {
		r := New(tt.format)
		if r == nil {
			t.Errorf("New(%q) returned nil", tt.format)
		}
	}
}

func TestDetectModeReport(t *testing.T) {
	inv := buildTestInventory()
	inv.ScanMode = "detect"

	// Add more category types
	inv.AddResource(scanner.Resource{
		Type:    scanner.ResIAMRole,
		ID:      "role-devtest",
		Region:  "global",
		InfraID: "dev-infra-001",
		ClusterName: "dev-test",
		CreatedAt: time.Now().Add(-74 * 24 * time.Hour),
	})
	inv.Groups["dev-infra-001"].Category = scanner.CategoryDeveloperOwned

	var buf bytes.Buffer
	rep := &TextReporter{}

	err := rep.Report(&buf, inv)
	if err != nil {
		t.Fatalf("Report() error: %v", err)
	}

	output := buf.String()

	checks := []string{
		"CONFIRMED ORPHANS",
		"DEVELOPER-OWNED",
		"ACTIVE",
	}

	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("detect mode output missing expected string %q", check)
		}
	}
}
