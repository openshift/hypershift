package config

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
)

func TestAppendSupportedTLSArgs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		help       string
		helpExit   string
		helpHang   bool
		want       []string
		wantError  bool
		wantStderr string
		subcommand bool
	}{
		{name: "When no TLS flags are advertised, it should omit all TLS candidates", help: "  --config string\n  --v int"},
		{name: "When only legacy konnectivity ciphers are supported, it should preserve just those ciphers", help: "  -cipher-suites string", want: []string{"--cipher-suites=legacy"}},
		{name: "When basic TLS flags but not curves are supported, it should omit curve preferences", help: "      --tls-min-version string\n      --tls-cipher-suites strings", want: []string{"--tls-min-version=VersionTLS12", "--tls-cipher-suites=cipher"}},
		{name: "When a backported binary supports curves, it should pass them without a release gate", help: "      --tls-curve-preferences strings", want: []string{"--tls-curve-preferences=29,23"}},
		{name: "When all flags are supported, it should preserve all candidates", help: "  --tls-min-version string\n  --tls-cipher-suites strings\n  --tls-curve-preferences strings\n  --cipher-suites strings", want: []string{"--tls-min-version=VersionTLS12", "--tls-cipher-suites=cipher", "--tls-curve-preferences=29,23", "--cipher-suites=legacy"}},
		{name: "When CVO has subcommand help, it should probe start and preserve its runtime subcommand", subcommand: true, help: "  --tls-min-version string", want: []string{"--tls-min-version=VersionTLS12"}},
		{name: "When help mentions a flag only in prose or a longer name, it should not count it as supported", help: "  --config string  mentions --tls-min-version\n  --tls-min-version-other string\n  --tls-cipher-suites-file string"},
		{name: "When a flag has a short alias, it should recognize the long flag", help: "  -t, --tls-min-version string", want: []string{"--tls-min-version=VersionTLS12"}},
		{name: "When help exits one with valid flags, it should launch with supported TLS arguments", help: "  --tls-min-version string", helpExit: "1", want: []string{"--tls-min-version=VersionTLS12"}},
		{name: "When help exits two with single-dash flags, it should launch with supported TLS arguments", help: "  -tls-min-version string", helpExit: "2", want: []string{"--tls-min-version=VersionTLS12"}},
		{name: "When nonzero help advertises only unrelated flags, it should omit unsupported TLS arguments", help: "  -config string", helpExit: "2", wantStderr: "Omitting unsupported TLS flag --tls-min-version"},
		{name: "When help exits nonzero without flag declarations, it should not launch the component", help: "failed to load configuration", helpExit: "1", wantError: true},
		{name: "When help exits nonzero with empty output, it should not launch the component", helpExit: "2", wantError: true},
		{name: "When help is killed after printing valid flags, it should not launch the component", help: "  --tls-min-version string", helpExit: "137", wantError: true, wantStderr: "help probe timed out or was killed"},
		{name: "When help hangs after printing valid flags, it should time out without launching the component", help: "  --tls-min-version string", helpHang: true, wantError: true, wantStderr: "help probe timed out or was killed"},
		{name: "When help output is empty, it should fail instead of assuming unsupported flags", wantError: true},
		{name: "When help output is unexpected, it should fail instead of silently losing TLS configuration", help: "unexpected output", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			binary := filepath.Join(t.TempDir(), "component")
			// Fake image binary: help goes to stderr, as Go's flag package does.
			g.Expect(os.WriteFile(binary, []byte(`#!/bin/bash
if [[ "${1:-}" == start ]]; then shift; fi
if [[ "${1:-}" == --help ]]; then
  printf '%s\n' "$TEST_HELP" >&2
  if [[ "${TEST_HELP_HANG:-}" == true ]]; then exec sleep 60; fi
  exit "${TEST_HELP_EXIT:-0}"
fi
printf '%s\n' "$@"
`), 0700)).To(Succeed())
			container := &corev1.Container{Command: []string{binary}, Args: []string{"--config=a path with spaces", "$(not-a-shell-command)", "--unrelated=value"}}
			helpCommand := []string{"--help"}
			if tc.subcommand {
				container.Args = append([]string{"start"}, container.Args...)
				helpCommand = []string{"start", "--help"}
			}
			g.Expect(AppendSupportedTLSArgs(container, helpCommand, []string{"--tls-min-version=VersionTLS12", "--tls-cipher-suites=cipher", "--tls-curve-preferences=29,23", "--cipher-suites=legacy"})).To(Succeed())
			cmd := exec.CommandContext(t.Context(), container.Command[0], append(container.Command[1:], container.Args...)...)
			cmd.Env = append(os.Environ(), "TEST_HELP="+tc.help, "TEST_HELP_EXIT="+tc.helpExit)
			if tc.helpHang {
				cmd.Env = append(cmd.Env, "TEST_HELP_HANG=true")
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			output, err := cmd.Output()
			if tc.wantStderr != "" {
				g.Expect(stderr.String()).To(ContainSubstring(tc.wantStderr))
			}
			if tc.wantError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(output).To(BeEmpty())
				g.Expect(stderr.String()).To(ContainSubstring("TLS capability detection failed"))
				return
			}
			g.Expect(err).NotTo(HaveOccurred(), stderr.String())
			want := append([]string{"--config=a path with spaces", "$(not-a-shell-command)", "--unrelated=value"}, tc.want...)
			g.Expect(strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")).To(Equal(want))
		})
	}

	t.Run("When there are no TLS candidates, it should leave the original command unchanged", func(t *testing.T) {
		g := NewWithT(t)
		container := &corev1.Container{Command: []string{"component"}, Args: []string{"--existing"}}
		original := container.DeepCopy()
		g.Expect(AppendSupportedTLSArgs(container, nil, nil)).To(Succeed())
		g.Expect(container).To(Equal(original))
	})
	for _, tc := range []struct {
		name      string
		container *corev1.Container
		help      []string
		args      []string
	}{
		{name: "When the container is missing, it should report an error", args: []string{"--tls-min-version=x"}},
		{name: "When the command is missing, it should report an error", container: &corev1.Container{}, help: []string{"component", "--help"}, args: []string{"--tls-min-version=x"}},
		{name: "When the help command is missing, it should report an error", container: &corev1.Container{Command: []string{"component"}}, args: []string{"--tls-min-version=x"}},
		{name: "When an unrelated flag is supplied for filtering, it should reject it", container: &corev1.Container{Command: []string{"component"}}, help: []string{"component", "--help"}, args: []string{"--unrelated=x"}},
		{name: "When a TLS flag lacks a value separator, it should reject it", container: &corev1.Container{Command: []string{"component"}}, help: []string{"component", "--help"}, args: []string{"--tls-min-version"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			NewWithT(t).Expect(AppendSupportedTLSArgs(tc.container, tc.help, tc.args)).NotTo(Succeed())
		})
	}
}
