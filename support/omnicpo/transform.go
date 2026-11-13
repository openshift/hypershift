// Package omnicpo declares, as data, how a main-built control-plane-operator adjusts the render
// targets it produces when it drives an older OpenShift control plane (OMNI-CPO mode).
//
// A component renders for main (the newest payload) unconditionally, then registers Transforms
// that adjust a target for the driven (older) control plane. A Transform is generic over its
// target T: a Transform[*appsv1.Deployment] mutates a Deployment, a Transform[*kcpv1.KubeAPIServerConfig]
// mutates the KAS config, and so on. Apply runs the in-range transforms for the driven version and
// is a no-op unless OMNI-CPO is enabled, so a stock CPO renders exactly as before.
//
// This expresses drift as "when driving a control plane in <version range>, do <named mutation>"
// rather than an opaque per-flag boolean or scattered inline version checks. Because main is the
// newest payload, most adjustments strip a flag/image an older operand rejects or swap an image its
// payload lacks; the range type also supports the reverse direction (Between) for something main has
// dropped but a middle band of operands still needs.
package omnicpo

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/blang/semver"
)

// ResolveContext provides the runtime facts a Mutation may need: the driven control plane's version
// and payload image resolution. controlplanecomponent.WorkloadContext satisfies it.
type ResolveContext interface {
	// ControlPlaneVersion returns the driven control plane's payload version.
	ControlPlaneVersion() string
	// GetImage resolves a payload component's image from the driven control plane's payload.
	GetImage(name string) string
}

// Mutation adjusts a render target of type T for a driven control plane. The Deployment helpers
// below build the common ones; a KAS-config or other transform supplies its own.
type Mutation[T any] func(rc ResolveContext, target T) error

// Transform is one declarative drift adjustment to a target of type T: when driving a control plane
// whose version falls in When, do Do. Reason documents why and is included in any error Do returns.
type Transform[T any] struct {
	Reason string
	When   Range
	Do     Mutation[T]
}

// Range is a half-open control-plane version range, compared at MAJOR.MINOR (patch and prerelease
// are ignored, matching config.OmniCPOSupportsVersion). A plane reporting 4.23.0-ec.2 is treated as
// 4.23, so it is not mis-sorted below the 4.23.0 release the way a full-semver comparison would.
type Range struct {
	hasLower bool
	lower    semver.Version // inclusive
	hasUpper bool
	upper    semver.Version // exclusive
}

// Below matches control planes older than v: the half-open range [0, v) at MAJOR.MINOR. Use it to
// strip an operand flag or image introduced at v, which anything below v cannot accept.
func Below(v semver.Version) Range {
	return Range{hasUpper: true, upper: v}
}

// Between matches control planes in [lower, upper) at MAJOR.MINOR. Use it to re-emit something main
// has dropped but a middle band of operands still needs.
func Between(lower, upper semver.Version) Range {
	return Range{hasLower: true, lower: lower, hasUpper: true, upper: upper}
}

// Contains reports whether the given control-plane version string falls in the range, compared at
// MAJOR.MINOR. An unparsable version is never in range, so a Transform does not apply and the main
// render stands (OmniCPOCheckVersion has already rejected such a version at reconcile start).
func (r Range) Contains(version string) bool {
	v, err := semver.Parse(version)
	if err != nil {
		return false
	}
	if r.hasLower && lessMajorMinor(v, r.lower) {
		return false
	}
	if r.hasUpper && !lessMajorMinor(v, r.upper) {
		return false
	}
	return true
}

// lessMajorMinor reports whether a sorts before b comparing only MAJOR then MINOR.
func lessMajorMinor(a, b semver.Version) bool {
	if a.Major != b.Major {
		return a.Major < b.Major
	}
	return a.Minor < b.Minor
}

// Apply runs, in order, each Transform whose range contains the driven control plane's version,
// against target. It is a no-op unless OMNI-CPO is enabled, so a control-plane-operator not deployed
// from main is unaffected and renders exactly as before.
func Apply[T any](rc ResolveContext, target T, transforms []Transform[T]) error {
	if !config.IsOmniCPOEnabled() {
		return nil
	}
	version := rc.ControlPlaneVersion()
	for _, t := range transforms {
		if t.Do == nil || !t.When.Contains(version) {
			continue
		}
		if err := t.Do(rc, target); err != nil {
			return fmt.Errorf("OMNI-CPO transform %q: %w", t.Reason, err)
		}
	}
	return nil
}

// StripArgs removes, from the named container's Args, every element that starts with one of the
// given flag prefixes. For single-element flags like "--config=..." or "--tls-min-version=...".
func StripArgs(container string, prefixes ...string) Mutation[*appsv1.Deployment] {
	return func(_ ResolveContext, d *appsv1.Deployment) error {
		podspec.UpdateContainer(container, d.Spec.Template.Spec.Containers, func(c *corev1.Container) {
			c.Args = podspec.RemoveArgsWithPrefix(c.Args, prefixes...)
		})
		return nil
	}
}

// StripCommandArgs is StripArgs for the container's Command, used by sidecars that carry their flags
// in Command rather than Args.
func StripCommandArgs(container string, prefixes ...string) Mutation[*appsv1.Deployment] {
	return func(_ ResolveContext, d *appsv1.Deployment) error {
		podspec.UpdateContainer(container, d.Spec.Template.Spec.Containers, func(c *corev1.Container) {
			c.Command = podspec.RemoveArgsWithPrefix(c.Command, prefixes...)
		})
		return nil
	}
}

// StripCommandFlags removes, from the named container's Command, each given flag together with the
// element immediately following it (the two-element "--flag value" form).
func StripCommandFlags(container string, flags ...string) Mutation[*appsv1.Deployment] {
	return func(_ ResolveContext, d *appsv1.Deployment) error {
		podspec.UpdateContainer(container, d.Spec.Template.Spec.Containers, func(c *corev1.Container) {
			c.Command = podspec.RemoveFlagsWithValues(c.Command, flags...)
		})
		return nil
	}
}

// StripShellScriptArgLines removes, from the named container's Args, any line containing one of the
// given substrings. For operands whose flags live inside a "-c" shell-script arg.
func StripShellScriptArgLines(container string, substrs ...string) Mutation[*appsv1.Deployment] {
	return func(_ ResolveContext, d *appsv1.Deployment) error {
		podspec.UpdateContainer(container, d.Spec.Template.Spec.Containers, func(c *corev1.Container) {
			c.Args = podspec.RemoveShellScriptArgLines(c.Args, substrs...)
		})
		return nil
	}
}

// StripEnv removes the named environment variables from the named container.
func StripEnv(container string, names ...string) Mutation[*appsv1.Deployment] {
	remove := make(map[string]struct{}, len(names))
	for _, n := range names {
		remove[n] = struct{}{}
	}
	return func(_ ResolveContext, d *appsv1.Deployment) error {
		podspec.UpdateContainer(container, d.Spec.Template.Spec.Containers, func(c *corev1.Container) {
			kept := make([]corev1.EnvVar, 0, len(c.Env))
			for _, e := range c.Env {
				if _, drop := remove[e.Name]; !drop {
					kept = append(kept, e)
				}
			}
			c.Env = kept
		})
		return nil
	}
}

// SetImage sets the named container's image to the given payload component's image, resolved from
// the driven control plane's payload. For an operand whose image name differs across versions.
func SetImage(container string, payloadComponent string) Mutation[*appsv1.Deployment] {
	return func(rc ResolveContext, d *appsv1.Deployment) error {
		image := rc.GetImage(payloadComponent)
		podspec.UpdateContainer(container, d.Spec.Template.Spec.Containers, func(c *corev1.Container) {
			c.Image = image
		})
		return nil
	}
}
