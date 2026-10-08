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

func TestDumpCluster(t *testing.T) {
	tests := []struct {
		name           string
		diagnosticsErr error
	}{
		{
			name: "When platform diagnostics succeed, it should run generic collection afterward",
		},
		{
			name:           "When platform diagnostics fail, it should still run generic collection afterward",
			diagnosticsErr: errors.New("diagnostics unavailable"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			dir := t.TempDir()
			marker := filepath.Join(dir, "generic-called")
			t.Setenv("GENERIC_DUMP_MARKER", marker)
			binary := filepath.Join(dir, "hypershift")
			g.Expect(os.WriteFile(binary, []byte("#!/bin/sh\ntouch \"$GENERIC_DUMP_MARKER\"\n"), 0700)).To(Succeed())

			called := false
			dumpCluster(binary, dir, "hc", "clusters", func(_ context.Context, namespace, name, artifacts string) error {
				called = true
				_, err := os.Stat(marker)
				g.Expect(os.IsNotExist(err)).To(BeTrue(), "generic dump should run after platform diagnostics")
				g.Expect(namespace).To(Equal("clusters"))
				g.Expect(name).To(Equal("hc"))
				g.Expect(artifacts).To(Equal(filepath.Join(dir, "hc")))
				return tt.diagnosticsErr
			})

			g.Expect(called).To(BeTrue(), "platform diagnostics should run")
			_, err := os.Stat(marker)
			g.Expect(err).NotTo(HaveOccurred(), "generic dump should run even if diagnostics fail")
		})
	}
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
