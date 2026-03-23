package scraper

import (
	"testing"
)

func TestParseProcessedIssues(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []ProcessedIssue
		wantErr bool
	}{
		{
			name:  "single issue",
			input: "OCPBUGS-1234 2025-03-20T10:30:00Z https://github.com/openshift/hypershift/pull/100 success",
			want: []ProcessedIssue{
				{IssueKey: "OCPBUGS-1234", Timestamp: "2025-03-20T10:30:00Z", PRURL: "https://github.com/openshift/hypershift/pull/100", Status: "success"},
			},
		},
		{
			name: "multiple issues",
			input: `OCPBUGS-1234 2025-03-20T10:30:00Z https://github.com/openshift/hypershift/pull/100 success
OCPBUGS-5678 2025-03-20T11:00:00Z https://github.com/openshift/hypershift/pull/101 failure`,
			want: []ProcessedIssue{
				{IssueKey: "OCPBUGS-1234", Timestamp: "2025-03-20T10:30:00Z", PRURL: "https://github.com/openshift/hypershift/pull/100", Status: "success"},
				{IssueKey: "OCPBUGS-5678", Timestamp: "2025-03-20T11:00:00Z", PRURL: "https://github.com/openshift/hypershift/pull/101", Status: "failure"},
			},
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name:  "whitespace only",
			input: "   \n\n  ",
			want:  nil,
		},
		{
			name:    "malformed line with missing fields",
			input:   "OCPBUGS-1234 2025-03-20T10:30:00Z",
			wantErr: true,
		},
		{
			name: "blank lines are skipped",
			input: `OCPBUGS-1234 2025-03-20T10:30:00Z https://github.com/openshift/hypershift/pull/100 success

OCPBUGS-5678 2025-03-20T11:00:00Z https://github.com/openshift/hypershift/pull/101 failure`,
			want: []ProcessedIssue{
				{IssueKey: "OCPBUGS-1234", Timestamp: "2025-03-20T10:30:00Z", PRURL: "https://github.com/openshift/hypershift/pull/100", Status: "success"},
				{IssueKey: "OCPBUGS-5678", Timestamp: "2025-03-20T11:00:00Z", PRURL: "https://github.com/openshift/hypershift/pull/101", Status: "failure"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseProcessedIssues([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseProcessedIssues() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ParseProcessedIssues() returned %d issues, want %d", len(got), len(tt.want))
			}
			for i, g := range got {
				w := tt.want[i]
				if g.IssueKey != w.IssueKey || g.Timestamp != w.Timestamp || g.PRURL != w.PRURL || g.Status != w.Status {
					t.Errorf("issue[%d] = %+v, want %+v", i, g, w)
				}
			}
		})
	}
}

func TestParseTokensJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    TokenUsage
		wantErr bool
	}{
		{
			name: "valid tokens JSON",
			input: `{
				"total_cost_usd": 0.42,
				"duration_ms": 15000,
				"num_turns": 5,
				"input_tokens": 10000,
				"output_tokens": 2000,
				"cache_read_input_tokens": 5000,
				"cache_creation_input_tokens": 3000,
				"model": "claude-sonnet-4-20250514"
			}`,
			want: TokenUsage{
				TotalCostUSD:             0.42,
				DurationMs:               15000,
				NumTurns:                 5,
				InputTokens:              10000,
				OutputTokens:             2000,
				CacheReadInputTokens:     5000,
				CacheCreationInputTokens: 3000,
				Model:                    "claude-sonnet-4-20250514",
			},
		},
		{
			name: "partial fields",
			input: `{
				"total_cost_usd": 0.10,
				"input_tokens": 5000,
				"output_tokens": 1000,
				"model": "claude-sonnet-4-20250514"
			}`,
			want: TokenUsage{
				TotalCostUSD: 0.10,
				InputTokens:  5000,
				OutputTokens: 1000,
				Model:        "claude-sonnet-4-20250514",
			},
		},
		{
			name:    "invalid JSON",
			input:   `{not valid json}`,
			wantErr: true,
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTokensJSON([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseTokensJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.TotalCostUSD != tt.want.TotalCostUSD ||
				got.DurationMs != tt.want.DurationMs ||
				got.NumTurns != tt.want.NumTurns ||
				got.InputTokens != tt.want.InputTokens ||
				got.OutputTokens != tt.want.OutputTokens ||
				got.CacheReadInputTokens != tt.want.CacheReadInputTokens ||
				got.CacheCreationInputTokens != tt.want.CacheCreationInputTokens ||
				got.Model != tt.want.Model {
				t.Errorf("ParseTokensJSON() = %+v, want %+v", *got, tt.want)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		{
			name:  "simple integer",
			input: "120",
			want:  120,
		},
		{
			name:  "with trailing newline",
			input: "45\n",
			want:  45,
		},
		{
			name:  "with surrounding whitespace",
			input: "  300  \n",
			want:  300,
		},
		{
			name:    "non-numeric",
			input:   "abc",
			wantErr: true,
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
		{
			name:  "zero",
			input: "0",
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDuration([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseDuration() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got != tt.want {
				t.Errorf("ParseDuration() = %d, want %d", got, tt.want)
			}
		})
	}
}
