package ingressoperator

import (
	_ "embed"
	"strconv"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"

	corev1 "k8s.io/api/core/v1"
)

// Probe the binary in the component image, including overrides and backports.
// HAProxy image selection and its default are one capability: never pass only
// half of the configuration. Keep values in argv rather than shell source, and
// exec the operator so that signals and its exit status are preserved.
// Version-selection images must provide bash and timeout (as the RHEL-based
// ingress images do). A failed or unrecognizable probe must stop startup.
//
//go:embed haproxy-capability.sh
var haproxyCapabilityCommand string

func configureHAProxy(container *corev1.Container, provider imageprovider.ReleaseImageProvider) {
	var args []string
	var defaultVersion, deprecatedVersion string
	// This is the supported HAProxy policy, not an OCP release-version gate.
	// Prefer 3.2 when present; retain 2.8 for payloads that still ship it.
	// A new HAProxy version needs an explicit policy decision here, rather than
	// silently making an unknown image the default for existing routers.
	for _, version := range []struct{ version, image string }{
		{"2.8", "haproxy-router-haproxy28"},
		{"3.2", "haproxy-router-haproxy32"},
	} {
		if image, exists := provider.ImageExist(version.image); exists && image != "" {
			args = append(args, "--haproxy-image="+version.version+"="+image)
			defaultVersion = version.version
			if version.version == "2.8" {
				deprecatedVersion = "2.8"
			}
		}
	}
	// Legacy payloads only provide haproxy-router. Do not record optional
	// versioned images as missing, or pass flags their binaries do not support.
	if len(args) == 0 {
		return
	}
	// Protocol: count, haproxy_args[0..count-1], defaultVersion, deprecatedVersion, originalCmd...
	wrapped := []string{strconv.Itoa(len(args))}
	wrapped = append(wrapped, args...)
	wrapped = append(wrapped, defaultVersion, deprecatedVersion)
	wrapped = append(wrapped, container.Command...)
	wrapped = append(wrapped, container.Args...)
	container.Command = []string{"/bin/bash", "-c", haproxyCapabilityCommand, "haproxy-capability-check"}
	container.Args = wrapped
}
