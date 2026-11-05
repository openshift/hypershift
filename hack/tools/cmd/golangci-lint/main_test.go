package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNativeLinters exercises the executable and both registrations together.
func TestNativeLinters(t *testing.T) {
	t.Setenv("CGO_ENABLED", "0")
	t.Setenv("GOWORK", "off")
	binary := filepath.Join(t.TempDir(), "golangci-lint")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CGO-disabled executable: %v\n%s", err, output)
	}
	fixture := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":        "module example.com/native-linters\n\ngo 1.26.0\n",
		"types.go":      "package fixture\n\ntype Config struct { Enabled bool `json:\"enabled\"` }\n",
		"types_test.go": "package fixture\n\nimport \"testing\"\n\nfunc Test_bad(t *testing.T) {}\n",
		".golangci.yml": `version: "2"
linters:
  default: none
  enable: [hypershiftlinter, kubeapilinter]
  settings:
    custom:
      hypershiftlinter:
        type: module
        settings:
          analyzers:
            enable: [testfuncname]
      kubeapilinter:
        type: module
        settings:
          linters:
            enable: [nobools]
            disable: ['*']
`,
	} {
		if err := os.WriteFile(filepath.Join(fixture, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := exec.Command(binary, "run", "--config", ".golangci.yml", "./...")
	run.Dir = fixture
	output, err := run.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("expected findings from both custom linters, got %v\n%s", err, output)
	}
	for _, want := range []string{"testfuncname:", "(hypershiftlinter)", "nobools:", "(kubeapilinter)"} {
		if !strings.Contains(string(output), want) {
			t.Errorf("missing %q in diagnostics:\n%s", want, output)
		}
	}
	for name, content := range map[string]string{
		"types.go":      "package fixture\n\ntype Config struct { Enabled string `json:\"enabled\"` }\n",
		"types_test.go": "package fixture\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n",
	} {
		if err := os.WriteFile(filepath.Join(fixture, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	clean := exec.Command(binary, "run", "--config", ".golangci.yml", "./...")
	clean.Dir = fixture
	if output, err := clean.CombinedOutput(); err != nil {
		t.Fatalf("corrected fixture should pass: %v\n%s", err, output)
	}
}
