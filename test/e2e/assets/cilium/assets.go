//go:build e2ev2

package cilium

import "embed"

// Manifests contains the versioned Cilium operator fixture manifests.
//
//go:embed v1.15.1/cluster-network-*.yaml
var Manifests embed.FS
