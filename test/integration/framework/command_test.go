package framework

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSetupCommandOmitsEnvironmentFromArtifact(t *testing.T) {
	artifactDir := t.TempDir()
	expectedEnv := []string{
		"PASSWORD=secret-password",
		"TOKEN=secret-token",
		"API_KEY=secret-api-key",
		"KUBECONFIG=/tmp/management-kubeconfig",
	}
	cmd := exec.CommandContext(t.Context(), "true")
	cmd.Env = slices.Clone(expectedEnv)

	logFile, configuredCmd, _, err := setupCommand(&Options{ArtifactDir: artifactDir}, "command.log", cmd)
	if err != nil {
		t.Fatalf("setupCommand failed: %v", err)
	}
	if err := logFile.Close(); err != nil {
		t.Fatalf("failed to close command log: %v", err)
	}

	logContents, err := os.ReadFile(filepath.Join(artifactDir, "command.log"))
	if err != nil {
		t.Fatalf("failed to read command log: %v", err)
	}
	logText := string(logContents)
	for _, value := range []string{"secret-password", "secret-token", "secret-api-key", "/tmp/management-kubeconfig"} {
		if strings.Contains(logText, value) {
			t.Errorf("command log exposed environment value %q", value)
		}
	}
	if !strings.Contains(logText, "Command environment omitted from log") {
		t.Error("command log did not explain why environment values were omitted")
	}
	if !slices.Equal(configuredCmd.Env, expectedEnv) {
		t.Errorf("command environment was changed: got %v, want %v", configuredCmd.Env, expectedEnv)
	}
}
