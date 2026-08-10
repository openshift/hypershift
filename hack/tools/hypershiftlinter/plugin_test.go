package hypershiftlinter

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
)

func TestBuildAnalyzersRejectsUnknownTopLevelField(t *testing.T) {
	raw := map[string]any{
		"analyzers": map[string]any{"enable": []any{"vacuouspass"}},
		"unknown":   true,
	}

	if _, err := BuildAnalyzers(raw); err == nil {
		t.Fatal("expected error for unknown top-level field, got nil")
	}
}

func TestBuildAnalyzersRejectsUnknownNestedField(t *testing.T) {
	// "enbale" is a typo for "enable" — must not be silently ignored, otherwise
	// every analyzer would be enabled instead of just the intended one.
	raw := map[string]any{
		"analyzers": map[string]any{"enbale": []any{"vacuouspass"}},
	}

	if _, err := BuildAnalyzers(raw); err == nil {
		t.Fatal("expected error for unknown nested field, got nil")
	}
}

func TestBuildAnalyzersAcceptsValidSettings(t *testing.T) {
	raw := map[string]any{
		"analyzers": map[string]any{"enable": []any{"vacuouspass"}},
	}

	got, err := BuildAnalyzers(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "vacuouspass" {
		t.Fatalf("expected only the vacuouspass analyzer, got %v", analyzerNames(got))
	}
}

func TestBuildAnalyzersNilSettingsEnablesAll(t *testing.T) {
	got, err := BuildAnalyzers(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(AllAnalyzers()) {
		t.Fatalf("expected all analyzers, got %d of %d: %s",
			len(got), len(AllAnalyzers()), strings.Join(analyzerNames(got), ", "))
	}
}

func analyzerNames(analyzers []*analysis.Analyzer) []string {
	names := make([]string, 0, len(analyzers))
	for _, a := range analyzers {
		names = append(names, a.Name)
	}
	return names
}
