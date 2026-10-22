//go:build e2ev2

package main

func healthWorkerPhase(variant, name string) string {
	switch variant + "/" + name {
	case "managed-dns/managed-dns-common-after",
		"karpenter/karpenter-common-after",
		"karpenter-upgrade/karpenter-upgrade-common-after",
		"public/public-common-after",
		"autoscaling/autoscaling-common-after",
		"external-oidc/external-oidc-common-after",
		"upgrade/upgrade-common-after",
		"upgrade/post-upgrade-health",
		"guest-olm/aws-guest-olm-common-after",
		"trust-bundle/aws-trust-bundle-after",
		"nodepool-dispatch/aws-nodepool-dispatch-after",
		"cilium/aws-cilium-common-after",
		"private/aws-private-common-after",
		"private-route/aws-private-route-common-after",
		"proxy/aws-proxy-common-after",
		"break-glass-ha/aws-break-glass-ha-common-after",
		"custom-config/aws-custom-config-common-after",
		"upgrade-pki-disabled/aws-upgrade-pki-disabled-health",
		"upgrade-pki-disabled/aws-upgrade-pki-disabled-common-after",
		"public/aws-cloud-common-after",
		"karpenter-upgrade-pki-disabled/aws-autonode-upgrade-pki-disabled-health",
		"karpenter-upgrade-pki-disabled/aws-autonode-upgrade-pki-disabled-common-after",
		"cpo-override-latest/cpo-override-latest-common-after",
		"cpo-override-previous/cpo-override-previous-common-after",
		"public/aws-nodepool-major-version-upgrade-health",
		"public/aws-nodepool-cross-major-common-after":
		return "after"
	default:
		return ""
	}
}
