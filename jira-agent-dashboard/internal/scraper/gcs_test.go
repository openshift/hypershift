package scraper

import (
	"testing"
)

func TestParseBuildLog(t *testing.T) {
	buildLog := `=== HyperShift Jira Agent Process ===
Applying Gangway override: JIRA_AGENT_ISSUE_KEY=OCPBUGS-79071
Cloning ai-helpers repository...
Cloning HyperShift repository...
Setting up Claude commands...
Processing: OCPBUGS-79071
Running: jira-solve OCPBUGS-79071 origin --ci
{"type":"system","subtype":"init","cwd":"/tmp/hypershift","session_id":"abc123"}
{"type":"assistant","message":{"content":[{"type":"text","text":"Working on fix..."}]}}
{"type":"result","subtype":"success","is_error":false,"duration_ms":1728853,"num_turns":44,"total_cost_usd":2.44697175}
Phase 1 tokens: {
  "total_cost_usd": 2.44697175,
  "duration_ms": 1728853,
  "num_turns": 44,
  "input_tokens": 421,
  "output_tokens": 13647,
  "cache_read_input_tokens": 1735821,
  "cache_creation_input_tokens": 197725,
  "model": "claude-opus-4-6"
}
Phase 1 duration: 1740s
✅ Phase 1 (jira-solve) completed for OCPBUGS-79071
Phase 2: Pre-commit quality review for OCPBUGS-79071
{"type":"result","subtype":"success","is_error":false,"duration_ms":214676}
Phase 2 tokens: {
  "total_cost_usd": 2.0867235,
  "duration_ms": 214676,
  "num_turns": 25,
  "input_tokens": 107,
  "output_tokens": 6106,
  "cache_read_input_tokens": 256221,
  "cache_creation_input_tokens": 45294,
  "model": "claude-opus-4-6"
}
Phase 2 duration: 215s
✅ Phase 2 (pre-commit review) completed for OCPBUGS-79071
Phase 3: Addressing review findings for OCPBUGS-79071
Phase 3 tokens: {
  "total_cost_usd": 1.2345,
  "duration_ms": 371000,
  "num_turns": 15,
  "input_tokens": 200,
  "output_tokens": 5000,
  "cache_read_input_tokens": 100000,
  "cache_creation_input_tokens": 30000,
  "model": "claude-opus-4-6"
}
✅ Phase 3 (address review) completed for OCPBUGS-79071
Phase 3 duration: 371s
Phase 4: Creating Pull Request for OCPBUGS-79071
Phase 4 tokens: {
  "total_cost_usd": 0.17865225,
  "duration_ms": 28853,
  "num_turns": 7,
  "input_tokens": 9,
  "output_tokens": 1290,
  "cache_read_input_tokens": 62527,
  "cache_creation_input_tokens": 18415,
  "model": "claude-opus-4-6"
}
Phase 4 duration: 29s
✅ PR created: https://github.com/openshift/hypershift/pull/8030
=== Processing Summary ===
Processed: 1
Failed: 0
==========================`

	result, err := ParseBuildLog([]byte(buildLog))
	if err != nil {
		t.Fatalf("ParseBuildLog returned error: %v", err)
	}

	if result.IssueKey != "OCPBUGS-79071" {
		t.Errorf("IssueKey = %q, want %q", result.IssueKey, "OCPBUGS-79071")
	}

	if result.PRURL != "https://github.com/openshift/hypershift/pull/8030" {
		t.Errorf("PRURL = %q, want %q", result.PRURL, "https://github.com/openshift/hypershift/pull/8030")
	}

	if len(result.Phases) != 4 {
		t.Fatalf("got %d phases, want 4", len(result.Phases))
	}

	// Phase 1: solve
	p1 := result.Phases[0]
	if p1.PhaseName != "solve" {
		t.Errorf("Phase 1 name = %q, want %q", p1.PhaseName, "solve")
	}
	if p1.TotalCostUSD != 2.44697175 {
		t.Errorf("Phase 1 cost = %f, want 2.44697175", p1.TotalCostUSD)
	}
	if p1.DurationMs != 1728853 {
		t.Errorf("Phase 1 duration = %d, want 1728853", p1.DurationMs)
	}
	if p1.NumTurns != 44 {
		t.Errorf("Phase 1 turns = %d, want 44", p1.NumTurns)
	}
	if p1.Model != "claude-opus-4-6" {
		t.Errorf("Phase 1 model = %q, want %q", p1.Model, "claude-opus-4-6")
	}
	if p1.InputTokens != 421 {
		t.Errorf("Phase 1 input_tokens = %d, want 421", p1.InputTokens)
	}
	if p1.OutputTokens != 13647 {
		t.Errorf("Phase 1 output_tokens = %d, want 13647", p1.OutputTokens)
	}
	if p1.CacheReadInputTokens != 1735821 {
		t.Errorf("Phase 1 cache_read = %d, want 1735821", p1.CacheReadInputTokens)
	}
	if p1.CacheCreationInputTokens != 197725 {
		t.Errorf("Phase 1 cache_creation = %d, want 197725", p1.CacheCreationInputTokens)
	}

	// Phase 2: review
	p2 := result.Phases[1]
	if p2.PhaseName != "review" {
		t.Errorf("Phase 2 name = %q, want %q", p2.PhaseName, "review")
	}
	if p2.TotalCostUSD != 2.0867235 {
		t.Errorf("Phase 2 cost = %f, want 2.0867235", p2.TotalCostUSD)
	}

	// Phase 3: fix
	p3 := result.Phases[2]
	if p3.PhaseName != "fix" {
		t.Errorf("Phase 3 name = %q, want %q", p3.PhaseName, "fix")
	}

	// Phase 4: pr
	p4 := result.Phases[3]
	if p4.PhaseName != "pr" {
		t.Errorf("Phase 4 name = %q, want %q", p4.PhaseName, "pr")
	}
	if p4.TotalCostUSD != 0.17865225 {
		t.Errorf("Phase 4 cost = %f, want 0.17865225", p4.TotalCostUSD)
	}
}

func TestParseBuildLogNoIssue(t *testing.T) {
	buildLog := `=== HyperShift Jira Agent Process ===
Jira search returned 0 result(s)
No issues found matching criteria`

	_, err := ParseBuildLog([]byte(buildLog))
	if err == nil {
		t.Error("expected error for build log with no issue key, got nil")
	}
}

func TestParseBuildLogNoPR(t *testing.T) {
	buildLog := `Processing: OCPBUGS-12345
Phase 1 tokens: {
  "total_cost_usd": 1.0,
  "duration_ms": 100000,
  "num_turns": 10,
  "input_tokens": 100,
  "output_tokens": 200,
  "cache_read_input_tokens": 300,
  "cache_creation_input_tokens": 400,
  "model": "claude-opus-4-6"
}
Phase 1 duration: 100s
✅ Phase 1 (jira-solve) completed for OCPBUGS-12345`

	result, err := ParseBuildLog([]byte(buildLog))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IssueKey != "OCPBUGS-12345" {
		t.Errorf("IssueKey = %q, want %q", result.IssueKey, "OCPBUGS-12345")
	}
	if result.PRURL != "" {
		t.Errorf("PRURL = %q, want empty", result.PRURL)
	}
	if len(result.Phases) != 1 {
		t.Errorf("got %d phases, want 1", len(result.Phases))
	}
}

func TestParseBuildLogPRLineFormat(t *testing.T) {
	// Early build logs use "   PR: https://..." instead of "PR created: https://..."
	buildLog := `=== HyperShift Jira Agent Process ===
Processing: OCPBUGS-74498
   PR: https://github.com/openshift/hypershift/pull/7620
Phase 1 tokens: {
  "total_cost_usd": 1.5,
  "duration_ms": 500000,
  "num_turns": 20,
  "input_tokens": 100,
  "output_tokens": 5000,
  "cache_read_input_tokens": 100000,
  "cache_creation_input_tokens": 50000,
  "model": "claude-opus-4-5"
}
Phase 1 duration: 500s
Phase 1 (jira-solve) completed for OCPBUGS-74498`

	result, err := ParseBuildLog([]byte(buildLog))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IssueKey != "OCPBUGS-74498" {
		t.Errorf("IssueKey = %q, want %q", result.IssueKey, "OCPBUGS-74498")
	}
	if result.PRURL != "https://github.com/openshift/hypershift/pull/7620" {
		t.Errorf("PRURL = %q, want %q", result.PRURL, "https://github.com/openshift/hypershift/pull/7620")
	}
}

func TestParseBuildLogPhaseNameFallback(t *testing.T) {
	// When the "Phase N (name) completed" line comes AFTER the token block,
	// the parser should fall back to the default phase number mapping.
	buildLog := `Processing: OCPBUGS-99999
Phase 1 tokens: {
  "total_cost_usd": 0.5,
  "duration_ms": 50000,
  "num_turns": 5,
  "input_tokens": 10,
  "output_tokens": 20,
  "cache_read_input_tokens": 30,
  "cache_creation_input_tokens": 40,
  "model": "claude-opus-4-6"
}
Phase 1 duration: 50s`

	result, err := ParseBuildLog([]byte(buildLog))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Phases) != 1 {
		t.Fatalf("got %d phases, want 1", len(result.Phases))
	}
	if result.Phases[0].PhaseName != "solve" {
		t.Errorf("Phase 1 fallback name = %q, want %q", result.Phases[0].PhaseName, "solve")
	}
}
