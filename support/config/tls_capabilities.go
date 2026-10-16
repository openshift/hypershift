package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// tlsCapabilityCommand runs inside the component image, so capabilities always
// describe the binary being started, including image overrides and backports.
// Use argv, not interpolated shell source, for commands and flag values. exec
// preserves the component's exit status and delivery of termination signals.
const tlsCapabilityCommand = `set -euo pipefail
help_count="$1"
shift
help_command=("${@:1:help_count}")
shift "$help_count"
tls_count="$1"
shift
tls_args=("${@:1:tls_count}")
shift "$tls_count"

help_exit=0
help_output=$(timeout --signal=KILL 30s "${help_command[@]}" 2>&1) || help_exit=$?
if [[ $help_exit -eq 137 ]]; then
  echo "TLS capability detection failed: help probe timed out or was killed for ${help_command[0]}" >&2
  exit 1
fi
declare -A supported_flags=()
while IFS= read -r line; do
  if [[ "$line" =~ ^[[:space:]]*(-[[:alnum:]],?[[:space:]]+)?--?([[:alnum:]][[:alnum:]-]*)([[:space:]=]|$) ]]; then
    supported_flags["${BASH_REMATCH[2]}"]=1
  fi
done <<< "$help_output"
if [[ ${#supported_flags[@]} -eq 0 ]]; then
  echo "TLS capability detection failed: no flag declarations in help for ${help_command[0]}" >&2
  exit 1
fi
selected=()
for arg in "${tls_args[@]}"; do
  flag="${arg%%=*}"
  flag="${flag#--}"
  if [[ -n "${supported_flags[$flag]:-}" ]]; then
    selected+=("$arg")
  else
    echo "Omitting unsupported TLS flag --$flag for ${help_command[0]}" >&2
  fi
done
exec "$@" "${selected[@]}"
`

// AppendSupportedTLSArgs wraps a container's explicit command with a startup
// capability check. helpArgs (for example, start --help) are appended to the
// container's original command to discover its flags. Only advertised TLS flags
// are appended to the original argv. The image must provide bash and timeout,
// as the affected RHEL component images do. Nonzero help exits are accepted if
// the output contains flag declarations; killed probes and unrecognized output
// stop startup rather than silently dropping TLS configuration. Unsupported
// flags are omitted with a diagnostic, leaving the binary's built-in defaults;
// this does not guarantee enforcement of the requested TLS policy.
//
// CVO, konnectivity-server, and machine-approver use this check because their
// payload binaries have demonstrated TLS flag incompatibilities with newer CPO
// overrides. Their images are resolved through ReleaseImageProvider by the
// component framework, not UserReleaseImageProvider. This is a targeted guard
// for those known incompatibilities, not a guarantee that other TLS consumers
// cannot experience skew when the CPO image is overridden.
func AppendSupportedTLSArgs(container *corev1.Container, helpArgs, tlsArgs []string) error {
	if len(tlsArgs) == 0 {
		return nil
	}
	if container == nil || len(container.Command) == 0 || len(helpArgs) == 0 {
		return fmt.Errorf("TLS capability detection requires explicit component and help commands")
	}
	for _, arg := range tlsArgs {
		flag, _, hasValue := strings.Cut(arg, "=")
		// Keep in sync with TLSArgs, kas.getTLSArgs, and the curve-preference
		// arguments appended by the machine-approver adapter.
		switch flag {
		case "--tls-min-version", "--tls-cipher-suites", "--tls-curve-preferences", "--cipher-suites":
			if hasValue {
				continue
			}
		}
		return fmt.Errorf("invalid TLS capability argument %q", arg)
	}
	helpCommand := slices.Concat(container.Command, helpArgs)
	args := []string{strconv.Itoa(len(helpCommand))}
	args = append(args, helpCommand...)
	args = append(args, strconv.Itoa(len(tlsArgs)))
	args = append(args, tlsArgs...)
	args = append(args, container.Command...)
	args = append(args, container.Args...)
	container.Command = []string{"/bin/bash", "-c", tlsCapabilityCommand, "tls-capability-check"}
	container.Args = args
	return nil
}
