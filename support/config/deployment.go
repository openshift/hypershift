package config

import (
	"fmt"
	"strings"

	"github.com/blang/semver"

	configv1 "github.com/openshift/api/config/v1"
)

// HACK (main-CPO-on-4.20 experiment): operand-flag version gate.
// The CPO runs exactly one process per HostedControlPlane, so a package-level operand version is
// safe (no cross-HCP concurrency). SetOperandVersion is called once early in the HCP reconcile
// from the resolved control-plane release image. TLSArgs (the operand --tls-min-version/
// --tls-cipher-suites helper, consumed by ~18 components) returns nil below 5.0 because the 4.20
// payload binaries reject those flags ("unknown flag: --tls-min-version") and CrashLoop.
// NOTE: only TLSArgs is gated, NOT MinTLSVersion — etcd calls MinTLSVersion directly for the etcd
// binary's own valid --tls-min-version flag and must keep working.
var (
	operandVersion    semver.Version
	operandVersionSet bool
)

// SetOperandVersion records the resolved control-plane payload version for the (single) HCP this
// CPO process serves.
func SetOperandVersion(v semver.Version) {
	operandVersion = v
	operandVersionSet = true
}

// operandSupportsTLSArgs reports whether the operand payload is new enough to accept the
// --tls-min-version/--tls-cipher-suites operand flags. Fail-open (true) if version was never set,
// to preserve default behavior outside the experiment.
func operandSupportsTLSArgs() bool {
	if !operandVersionSet {
		return true
	}
	return operandVersion.GTE(Version500)
}

// TLSArgs returns TLS-related command-line arguments for a given TLS security profile.
// It returns a slice of strings containing --tls-min-version and --tls-cipher-suites flags
// based on the provided profile. The caller is responsible for appending these to their
// argument list using the spread operator (...).
func TLSArgs(profile *configv1.TLSSecurityProfile) ([]string, error) {
	// HACK (main-CPO-on-4.20): skip operand TLS flags on payloads that don't support them.
	if !operandSupportsTLSArgs() {
		return nil, nil
	}

	var tlsArgs []string
	tlsMinVersion, err := MinTLSVersion(profile)
	if err != nil {
		return nil, fmt.Errorf("failed to get min TLS version: %w", err)
	}

	cipherSuites, err := CipherSuites(profile)
	if err != nil {
		return nil, fmt.Errorf("failed to get cipher suites: %w", err)
	}

	if tlsMinVersion != "" {
		tlsArgs = append(tlsArgs, fmt.Sprintf("--tls-min-version=%s", tlsMinVersion))
	}
	if len(cipherSuites) != 0 {
		tlsArgs = append(tlsArgs, fmt.Sprintf("--tls-cipher-suites=%s", strings.Join(cipherSuites, ",")))
	}
	return tlsArgs, nil
}
