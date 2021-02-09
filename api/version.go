package api

var (
	// TODO: Is there any way to meaningfully bake this through ldflags?
	HyperShiftImage = "registry.ci.openshift.org/hypershift/hypershift:latest"

	// OCPReleaseImage is the latest compatible OCP release, used for defaulting.
	// This can and should be baked into the binary through ldflags, although
	// the version committed here should be kept up to date with major releases.
	OCPReleaseImage = "quay.io/openshift-release-dev/ocp-release:4.7.0-fc.3-x86_64"
)
