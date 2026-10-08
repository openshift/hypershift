//go:build e2ev2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
)

func TestDumpCluster(t *testing.T) {
	t.Run("When Azure dump args are provided, it should forward them to the dump CLI", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		argsFile := filepath.Join(dir, "args")
		t.Setenv("DUMP_ARGS_FILE", argsFile)
		binary := filepath.Join(dir, "hypershift")
		g.Expect(os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DUMP_ARGS_FILE\"\n"), 0700)).To(Succeed())

		dumpCluster(binary, dir, "hc", "clusters", []string{"--azure-creds=/etc/azure/credentials.json"})

		data, err := os.ReadFile(argsFile)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(strings.Split(strings.TrimSpace(string(data)), "\n")).To(Equal([]string{
			"dump", "cluster",
			"--artifact-dir=" + filepath.Join(dir, "hc"),
			"--dump-guest-cluster=true",
			"--name=hc",
			"--namespace=clusters",
			"--azure-creds=/etc/azure/credentials.json",
		}))
	})
}

func TestDumpClusterArgs(t *testing.T) {
	t.Run("When AWS dump args are empty, it should omit Azure credentials", func(t *testing.T) {
		g := NewWithT(t)
		args := dumpClusterArgs("/artifacts/hc", "hc", "clusters-hc", nil)

		g.Expect(args).To(Equal([]string{
			"dump",
			"cluster",
			"--artifact-dir=/artifacts/hc",
			"--dump-guest-cluster=true",
			"--name=hc",
			"--namespace=clusters-hc",
		}))
	})
}
