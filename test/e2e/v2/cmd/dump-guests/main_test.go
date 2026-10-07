//go:build e2ev2

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

func TestDumpClusterWithDiagnostics(t *testing.T) {
	t.Run("When Azure diagnostics fail, it should still run generic collection afterward", func(t *testing.T) {
		dir := t.TempDir()
		marker := filepath.Join(dir, "generic-called")
		t.Setenv("GENERIC_DUMP_MARKER", marker)
		binary := filepath.Join(dir, "hypershift")
		if err := os.WriteFile(binary, []byte("#!/bin/sh\ntouch \"$GENERIC_DUMP_MARKER\"\nexit 1\n"), 0700); err != nil {
			t.Fatal(err)
		}
		called := false
		dumpClusterWithDiagnostics(binary, dir, "hc", "clusters", "/etc/azure/credentials.json", func(_ context.Context, namespace, name, credentials, artifacts, kubeconfig string) error {
			called = true
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Error("generic dump ran before Azure collection")
			}
			if namespace != "clusters" || name != "hc" || credentials != "/etc/azure/credentials.json" || artifacts != filepath.Join(dir, "hc") {
				t.Error("Azure collector received incorrect cluster configuration")
			}
			return errors.New("Azure unavailable")
		})
		if !called {
			t.Fatal("Azure collection was skipped")
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("Azure failure prevented generic collection: %v", err)
		}
	})
}

func TestDumpClusterArgs(t *testing.T) {
	t.Run("When building generic dump arguments, it should omit Azure-specific credentials", func(t *testing.T) {
		g := NewWithT(t)
		args := dumpClusterArgs("/artifacts/hc", "hc", "clusters-hc")

		g.Expect(args).To(ConsistOf(
			"dump",
			"cluster",
			"--artifact-dir=/artifacts/hc",
			"--dump-guest-cluster=true",
			"--name=hc",
			"--namespace=clusters-hc",
		))
	})
}
