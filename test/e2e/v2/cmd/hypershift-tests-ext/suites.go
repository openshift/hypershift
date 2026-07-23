//go:build e2ev2

package main

import (
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
)

type testPlan struct {
	Parallel   []string
	Sequential [][]string
}

type platformConfig struct {
	Suites       []e.Suite
	TestPlan     testPlan
	ClusterFiles map[string]string // suite name → filename in SHARED_DIR containing the cluster name
}

var platformConfigs = map[string]platformConfig{
	"test": {
		Suites: []e.Suite{
			{
				Name:          "hypershift/test/parallel",
				Description:   "Test platform multi-pool suite for OTE scheduler validation",
				Qualifiers:    []string{`labels.exists(l, l=="test-pool-a") || labels.exists(l, l=="test-pool-b")`},
				ResourcePools: map[string]int{"pool-a": 1, "pool-b": 1},
			},
			{
				Name:        "hypershift/test/pool-a",
				Description: "Test platform pool-a suite (per-pool, mirrors azure pattern)",
				Qualifiers:  []string{`labels.exists(l, l=="test-pool-a")`},
			},
			{
				Name:        "hypershift/test/pool-b",
				Description: "Test platform pool-b suite (per-pool, mirrors azure pattern)",
				Qualifiers:  []string{`labels.exists(l, l=="test-pool-b")`},
			},
			{
				Name:          "hypershift/test/step-1",
				Description:   "Test platform sequential step 1",
				Parallelism:   1,
				Qualifiers:    []string{`labels.exists(l, l=="test-step-1")`},
				ResourcePools: map[string]int{"pool-seq": 1},
			},
			{
				Name:          "hypershift/test/step-2",
				Description:   "Test platform sequential step 2",
				Parallelism:   1,
				Qualifiers:    []string{`labels.exists(l, l=="test-step-2")`},
				ResourcePools: map[string]int{"pool-seq": 1},
			},
		},
		TestPlan: testPlan{
			Parallel: []string{"hypershift/test/pool-a", "hypershift/test/pool-b"},
			Sequential: [][]string{
				{"hypershift/test/step-1", "hypershift/test/step-2"},
			},
		},
		ClusterFiles: map[string]string{
			"hypershift/test/pool-a": "cluster-name-a",
			"hypershift/test/pool-b": "cluster-name-b",
			"hypershift/test/step-1": "cluster-name-seq",
			"hypershift/test/step-2": "cluster-name-seq",
		},
	},
	"azure": {
		Suites: []e.Suite{
			{
				Name:        "hypershift/azure/public",
				Description: "Azure public cluster tests",
				Qualifiers:  []string{`labels.exists(l, l=="self-managed-azure-public") || labels.exists(l, l=="nodepool-lifecycle") || labels.exists(l, l=="secret-encryption") || labels.exists(l, l=="control-plane-workloads") || labels.exists(l, l=="hosted-cluster-security")`},
			},
			{
				Name:        "hypershift/azure/private",
				Description: "Azure private cluster tests",
				Qualifiers:  []string{`labels.exists(l, l=="self-managed-azure-private") || labels.exists(l, l=="hosted-cluster-compliance")`},
			},
			{
				Name:        "hypershift/azure/oauth-lb",
				Description: "Azure OAuth LB cluster tests",
				Qualifiers:  []string{`labels.exists(l, l=="self-managed-azure-oauth-lb") || labels.exists(l, l=="hosted-cluster-health") || labels.exists(l, l=="hosted-cluster-metrics") || labels.exists(l, l=="hosted-cluster-image-registry")`},
			},
			{
				Name:        "hypershift/azure/autoscaling",
				Description: "Azure autoscaling cluster tests",
				Qualifiers:  []string{`labels.exists(l, l=="nodepool-autoscaling")`},
			},
			{
				Name:        "hypershift/azure/external-oidc",
				Description: "Azure external OIDC cluster tests",
				Qualifiers:  []string{`labels.exists(l, l=="external-oidc")`},
			},
			{
				Name:        "hypershift/azure/upgrade",
				Description: "Sequential control plane upgrade tests",
				Parallelism: 1,
				Qualifiers:  []string{`labels.exists(l, l=="control-plane-upgrade")`},
			},
			{
				Name:        "hypershift/azure/chaos",
				Description: "Sequential etcd chaos tests (runs after upgrade)",
				Parallelism: 1,
				Qualifiers:  []string{`labels.exists(l, l=="etcd-chaos")`},
			},
		},
		TestPlan: testPlan{
			Parallel:   []string{"hypershift/azure/public", "hypershift/azure/private", "hypershift/azure/oauth-lb", "hypershift/azure/autoscaling", "hypershift/azure/external-oidc"},
			Sequential: [][]string{{"hypershift/azure/upgrade", "hypershift/azure/chaos"}},
		},
		ClusterFiles: map[string]string{
			"hypershift/azure/public":        "cluster-name-public",
			"hypershift/azure/private":       "cluster-name-private",
			"hypershift/azure/oauth-lb":      "cluster-name-oauth-lb",
			"hypershift/azure/autoscaling":   "cluster-name-autoscaling",
			"hypershift/azure/external-oidc": "cluster-name-external-oidc",
			"hypershift/azure/upgrade":       "cluster-name-upgrade",
			"hypershift/azure/chaos":         "cluster-name-upgrade",
		},
	},
	"aws": {
		Suites: []e.Suite{
			{
				Name:        "hypershift/aws/public",
				Description: "AWS public cluster non-mutating tests",
				Qualifiers:  []string{`labels.exists(l, l=="hosted-cluster-health") || labels.exists(l, l=="control-plane-workloads") || labels.exists(l, l=="hosted-cluster-metrics") || labels.exists(l, l=="hosted-cluster-image-registry")`},
			},
		},
		TestPlan: testPlan{
			Parallel: []string{"hypershift/aws/public"},
		},
		ClusterFiles: map[string]string{
			"hypershift/aws/public": "cluster-name-public",
		},
	},
}

func registerPlatformSuites(ext *e.Extension) {
	for _, cfg := range platformConfigs {
		for _, suite := range cfg.Suites {
			ext.AddSuite(suite)
		}
	}
}
