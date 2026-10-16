package dump

import "testing"

func TestNewCommand(t *testing.T) {
	t.Run("When creating the dump command, it should register the cluster dump command", func(t *testing.T) {
		cmd := NewCommand()
		commands := cmd.Commands()
		if len(commands) != 1 {
			t.Fatalf("expected one child command, got %d", len(commands))
		}
		if commands[0].Use != "cluster" {
			t.Fatalf("expected child command Use to be %q, got %q", "cluster", commands[0].Use)
		}
	})
}
