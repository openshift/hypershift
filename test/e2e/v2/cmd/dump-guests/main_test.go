//go:build e2ev2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"
)

func TestDumpClusters(t *testing.T) {
	tests := []struct {
		name    string
		entries []lifecycle.ClusterEntry
		specs   []lifecycle.ClusterSpec
		want    map[string][]string
	}{
		{
			name: "When manifest variants have distinct dump args, it should use each matching spec and omit uncreated variants",
			entries: []lifecycle.ClusterEntry{
				{Variant: "private", Name: "private-hc", Namespace: "clusters"},
				{Variant: "public", Name: "public-hc", Namespace: "clusters"},
			},
			specs: []lifecycle.ClusterSpec{
				{Variant: "public", DumpArgs: []string{"--azure-creds=/etc/azure/public.json"}},
				{Variant: "private", DumpArgs: []string{"--azure-creds=/etc/azure/private.json"}},
				{Variant: "upgrade", DumpArgs: []string{"--azure-creds=/etc/azure/upgrade.json"}},
			},
			want: map[string][]string{
				"private-hc": {"--azure-creds=/etc/azure/private.json"},
				"public-hc":  {"--azure-creds=/etc/azure/public.json"},
			},
		},
		{
			name:    "When an AWS spec has no dump args, it should still collect generic diagnostics",
			entries: []lifecycle.ClusterEntry{{Variant: "public", Name: "aws-hc", Namespace: "clusters"}},
			specs:   []lifecycle.ClusterSpec{{Variant: "public"}},
			want:    map[string][]string{"aws-hc": nil},
		},
		{
			name:    "When a manifest variant has no spec, it should still collect generic diagnostics",
			entries: []lifecycle.ClusterEntry{{Variant: "unknown", Name: "unknown-hc", Namespace: "clusters"}},
			specs:   []lifecycle.ClusterSpec{{Variant: "public", DumpArgs: []string{"--azure-creds=/etc/azure/public.json"}}},
			want:    map[string][]string{"unknown-hc": nil},
		},
		{
			name: "When the manifest is empty, it should not invoke the dump CLI",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			dir := t.TempDir()
			argsDir := t.TempDir()
			t.Setenv("DUMP_ARGS_DIR", argsDir)
			binary := filepath.Join(dir, "hypershift")
			script := "#!/bin/sh\nfor arg in \"$@\"; do\ncase \"$arg\" in --name=*) name=${arg#--name=} ;; esac\ndone\nprintf '%s\\n' \"$@\" > \"$DUMP_ARGS_DIR/$name\"\n"
			g.Expect(os.WriteFile(binary, []byte(script), 0700)).To(Succeed())

			dumpClusters(binary, dir, tt.entries, tt.specs)

			files, err := os.ReadDir(argsDir)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(files).To(HaveLen(len(tt.want)))
			for name, extraArgs := range tt.want {
				data, err := os.ReadFile(filepath.Join(argsDir, name))
				g.Expect(err).NotTo(HaveOccurred())
				want := append([]string{
					"dump", "cluster",
					"--artifact-dir=" + filepath.Join(dir, name),
					"--dump-guest-cluster=true",
					"--name=" + name,
					"--namespace=clusters",
				}, extraArgs...)
				g.Expect(strings.Split(strings.TrimSpace(string(data)), "\n")).To(Equal(want))
			}
		})
	}
}

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
