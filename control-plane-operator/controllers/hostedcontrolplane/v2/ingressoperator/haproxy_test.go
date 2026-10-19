package ingressoperator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"

	corev1 "k8s.io/api/core/v1"

	"github.com/spf13/pflag"
)

func TestConfigureHAProxy(t *testing.T) {
	t.Parallel()
	const image28 = "quay.io/openshift-release-dev/haproxy-router:2.8"
	const image32 = "quay.io/openshift-release-dev/haproxy-router:3.2"
	for _, tc := range []struct {
		name   string
		images map[string]string
		prefix []string
	}{
		{
			name: "When no images are available, it should leave the command and arguments unchanged",
		},
		{
			name:   "When only the legacy router is available, it should leave the command and arguments unchanged",
			images: map[string]string{"haproxy-router": image28},
		},
		{
			name:   "When a versioned image is empty, it should leave the command and arguments unchanged",
			images: map[string]string{"haproxy-router-haproxy32": ""},
		},
		{
			name:   "When both versions are available, it should encode both images and prefer 3.2",
			images: map[string]string{"haproxy-router-haproxy28": image28, "haproxy-router-haproxy32": image32},
			prefix: []string{"2", "--haproxy-image=2.8=" + image28, "--haproxy-image=3.2=" + image32, "3.2", "2.8"},
		},
		{
			name:   "When only 3.2 is available, it should encode an empty deprecated version",
			images: map[string]string{"haproxy-router-haproxy32": image32},
			prefix: []string{"1", "--haproxy-image=3.2=" + image32, "3.2", ""},
		},
		{
			name:   "When only 2.8 is available, it should encode 2.8 as default and deprecated",
			images: map[string]string{"haproxy-router-haproxy28": image28},
			prefix: []string{"1", "--haproxy-image=2.8=" + image28, "2.8", "2.8"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			container := &corev1.Container{
				Command: []string{"ingress-operator", "start"},
				Args:    []string{"--image=$(IMAGE)", "--namespace=openshift-ingress-operator"},
			}
			original := container.DeepCopy()
			provider := imageprovider.NewFromImages(tc.images)
			configureHAProxy(container, provider)
			g.Expect(provider.GetMissingImages()).To(BeEmpty())
			if tc.prefix == nil {
				g.Expect(container).To(Equal(original))
				return
			}
			g.Expect(container.Command).To(Equal([]string{"/bin/bash", "-c", haproxyCapabilityCommand, "haproxy-capability-check"}))
			wantArgs := append(tc.prefix, original.Command...)
			wantArgs = append(wantArgs, original.Args...)
			g.Expect(container.Args).To(Equal(wantArgs))
		})
	}
}

// TestHAProxyCapabilityCommand supplements the pure Go argv tests by executing
// the embedded script. It requires /bin/bash (4+) and GNU timeout on the host,
// matching the requirements of the version-selection ingress images.
func TestHAProxyCapabilityCommand(t *testing.T) {
	t.Parallel()
	const image28 = "quay.io/openshift-release-dev/haproxy-router:2.8"
	const image32 = "quay.io/openshift-release-dev/haproxy-router:3.2"
	const legacyHelp = "Flags:\n  -i, --image string  router image\n"
	const selectionHelp = legacyHelp + "      --haproxy-image stringToString  images\n      --default-haproxy-version string  default\n"
	const modernHelp = selectionHelp + "      --deprecated-haproxy-version strings  deprecated\n"
	for _, tc := range []struct {
		name         string
		images       map[string]string
		help         string
		helpExit     string
		operatorExit string
		wantArgs     []string
		wantError    string
	}{
		{
			name:   "When only the legacy router is available, it should run without probing or version flags",
			images: map[string]string{"haproxy-router": image28}, helpExit: "1",
		},
		{
			name:   "When images are empty, it should not request optional images or add version flags",
			images: map[string]string{"haproxy-router-haproxy32": ""}, helpExit: "1",
		},
		{
			name:   "When both versions are available, it should prefer 3.2 and deprecate 2.8",
			images: map[string]string{"haproxy-router-haproxy28": image28, "haproxy-router-haproxy32": image32}, help: modernHelp,
			wantArgs: []string{"--haproxy-image=2.8=" + image28, "--haproxy-image=3.2=" + image32, "--default-haproxy-version=3.2", "--deprecated-haproxy-version=2.8"},
		},
		{
			name:   "When 2.8 is absent, it should clear the binary's deprecated version default",
			images: map[string]string{"haproxy-router-haproxy32": image32}, help: modernHelp,
			wantArgs: []string{"--haproxy-image=3.2=" + image32, "--default-haproxy-version=3.2", "--deprecated-haproxy-version="},
		},
		{
			name:   "When only 2.8 is available, it should use an available default",
			images: map[string]string{"haproxy-router-haproxy28": image28}, help: modernHelp,
			wantArgs: []string{"--haproxy-image=2.8=" + image28, "--default-haproxy-version=2.8", "--deprecated-haproxy-version=2.8"},
		},
		{
			name:   "When the binary lacks the deprecation flag, it should preserve image selection without that flag",
			images: map[string]string{"haproxy-router-haproxy32": image32}, help: selectionHelp,
			wantArgs: []string{"--haproxy-image=3.2=" + image32, "--default-haproxy-version=3.2"},
		},
		{
			name:   "When an overridden binary lacks version selection, it should use the legacy image",
			images: map[string]string{"haproxy-router-haproxy32": image32}, help: legacyHelp,
		},
		{
			name:   "When help mentions a flag only in prose, it should not treat it as supported",
			images: map[string]string{"haproxy-router-haproxy32": image32}, help: legacyHelp + "Use --haproxy-image with newer releases\n",
		},
		{
			name:   "When only half of the selection contract is supported, it should fail with a diagnostic",
			images: map[string]string{"haproxy-router-haproxy32": image32}, help: legacyHelp + "      --haproxy-image stringToString images\n",
			wantError: "--haproxy-image requires --default-haproxy-version",
		},
		{
			name:   "When the help probe fails, it should not silently discard configuration",
			images: map[string]string{"haproxy-router-haproxy32": image32}, help: modernHelp, helpExit: "1",
			wantError: "HAProxy capability detection failed",
		},
		{
			name:   "When help is unrecognizable, it should fail with a diagnostic",
			images: map[string]string{"haproxy-router-haproxy32": image32}, help: "unexpected output",
			wantError: "unrecognized ingress-operator help",
		},
		{
			name:   "When image values contain shell syntax, it should pass them literally",
			images: map[string]string{"haproxy-router-haproxy32": "$(exit 77); 'quoted' value"}, help: selectionHelp,
			wantArgs: []string{"--haproxy-image=3.2=$(exit 77); 'quoted' value", "--default-haproxy-version=3.2"},
		},
		{
			name:   "When the operator exits unsuccessfully, it should preserve its exit status",
			images: map[string]string{"haproxy-router-haproxy32": image32}, help: modernHelp, operatorExit: "7",
			wantError: "exit status 7",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			binary := filepath.Join(t.TempDir(), "ingress-operator")
			g.Expect(os.WriteFile(binary, []byte(`#!/bin/bash
if [[ "$2" == "--help" ]]; then
  printf '%s\n' "$FAKE_HELP"
  exit "${HELP_EXIT:-0}"
fi
printf '%s\n' "$@"
exit "${OPERATOR_EXIT:-0}"
`), 0700)).To(Succeed())
			base := []string{"start", "--image=" + image28, "--namespace=openshift-ingress-operator"}
			container := &corev1.Container{Command: []string{binary, "start"}, Args: base[1:]}
			provider := imageprovider.NewFromImages(tc.images)
			configureHAProxy(container, provider)
			g.Expect(provider.GetMissingImages()).To(BeEmpty())
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, container.Command[0], append(container.Command[1:], container.Args...)...)
			cmd.Env = append(os.Environ(), "FAKE_HELP="+tc.help, "HELP_EXIT="+tc.helpExit, "OPERATOR_EXIT="+tc.operatorExit)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			output, err := cmd.Output()
			if tc.wantError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error() + stderr.String()).To(ContainSubstring(tc.wantError))
				return
			}
			g.Expect(err).NotTo(HaveOccurred(), stderr.String())
			actualArgs := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
			g.Expect(actualArgs).To(Equal(append(base, tc.wantArgs...)))
			if tc.help == modernHelp {
				// Match cmd/ingress-operator/start.go: the deprecated StringSlice
				// defaults to 2.8, and every deprecated version must have an image.
				// In particular, --deprecated-haproxy-version= must clear that
				// default for a 3.2-only payload, not produce an empty version.
				flags := pflag.NewFlagSet("ingress-operator", pflag.ContinueOnError)
				flags.String("image", "", "")
				flags.String("namespace", "", "")
				images := flags.StringToString("haproxy-image", nil, "")
				defaultVersion := flags.String("default-haproxy-version", "", "")
				deprecatedVersions := flags.StringSlice("deprecated-haproxy-version", []string{"2.8"}, "")
				g.Expect(flags.Parse(actualArgs[1:])).To(Succeed())
				g.Expect(*images).To(HaveKey(*defaultVersion))
				for _, version := range *deprecatedVersions {
					g.Expect(*images).To(HaveKey(version))
				}
				if _, has28 := (*images)["2.8"]; !has28 {
					g.Expect(*deprecatedVersions).To(BeEmpty())
				}
			}
		})
	}
}
