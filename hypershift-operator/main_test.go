package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// When NewStartCommand is called, it should return a valid cobra command
func TestNewStartCommand(t *testing.T) {
	cmd := NewStartCommand()
	if cmd == nil {
		t.Fatal("NewStartCommand() returned nil")
	}
	if _, ok := any(cmd).(*cobra.Command); !ok {
		t.Fatalf("NewStartCommand() did not return a *cobra.Command, got %T", cmd)
	}
	if cmd.Use != "run" {
		t.Errorf("expected Use='run', got %q", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("expected non-empty Short description")
	}
}
