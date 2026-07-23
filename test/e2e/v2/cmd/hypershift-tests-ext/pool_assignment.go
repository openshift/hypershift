//go:build e2ev2

package main

import (
	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
)

type poolMapping struct {
	LabelToPool  map[string]string
	PoolCapacity map[string]int
}

// platformPools defines the label→pool mapping and pool capacities for each
// platform. Each entry mirrors the TestMatrix in the corresponding
// lifecycle/<platform>.go file. Adding a new platform means adding an entry
// here and registering its suites in registerPlatformSuites.
var platformPools = map[string]poolMapping{
	"test": {
		LabelToPool: map[string]string{
			"test-pool-a": "pool-a",
			"test-pool-b": "pool-b",
			"test-step-1": "pool-seq",
			"test-step-2": "pool-seq",
		},
		PoolCapacity: map[string]int{
			"pool-a":   1,
			"pool-b":   1,
			"pool-seq": 1,
		},
	},
	"azure": {
		LabelToPool: map[string]string{
			// public cluster
			"self-managed-azure-public": "public",
			"nodepool-lifecycle":        "public",
			"secret-encryption":         "public",
			"control-plane-workloads":   "public",
			"hosted-cluster-security":   "public",

			// private cluster
			"self-managed-azure-private": "private",
			"hosted-cluster-compliance":  "private",

			// oauth-lb cluster
			"self-managed-azure-oauth-lb":   "oauth-lb",
			"hosted-cluster-health":         "oauth-lb",
			"hosted-cluster-metrics":        "oauth-lb",
			"hosted-cluster-image-registry": "oauth-lb",

			// autoscaling cluster
			"nodepool-autoscaling": "autoscaling",

			// external-oidc cluster
			"external-oidc": "external-oidc",

			// upgrade cluster (shared by upgrade and chaos)
			"control-plane-upgrade": "upgrade",
			"etcd-chaos":            "upgrade",
		},
		PoolCapacity: map[string]int{
			"public":        1,
			"private":       1,
			"oauth-lb":      1,
			"autoscaling":   1,
			"external-oidc": 1,
			"upgrade":       1,
		},
	},
}

func assignPoolsFromLabels(specs et.ExtensionTestSpecs, platform string) {
	mapping, ok := platformPools[platform]
	if !ok {
		return
	}

	specs.Walk(func(spec *et.ExtensionTestSpec) {
		for label := range spec.Labels {
			if pool, found := mapping.LabelToPool[label]; found {
				if spec.Resources.ResourcePools == nil {
					spec.Resources.ResourcePools = make(map[string]int)
				}
				spec.Resources.ResourcePools[pool] = 1
				return
			}
		}
	})
}
