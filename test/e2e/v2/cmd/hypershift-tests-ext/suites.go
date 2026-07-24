//go:build e2ev2

package main

import (
	"os"
	"path/filepath"
	"strings"

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
	LabelToPool  map[string]string // test label → resource pool name
	PoolCapacity map[string]int    // pool name → capacity
	EnvFunc      func(envInput) (platformEnv []string, suiteEnv map[string][]string)
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
		LabelToPool: map[string]string{
			"self-managed-azure-public": "public",
			"nodepool-lifecycle":        "public",
			"secret-encryption":         "public",
			"control-plane-workloads":   "public",
			"hosted-cluster-security":   "public",

			"self-managed-azure-private": "private",
			"hosted-cluster-compliance":  "private",

			"self-managed-azure-oauth-lb":   "oauth-lb",
			"hosted-cluster-health":         "oauth-lb",
			"hosted-cluster-metrics":        "oauth-lb",
			"hosted-cluster-image-registry": "oauth-lb",

			"nodepool-autoscaling": "autoscaling",

			"external-oidc": "external-oidc",

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
		EnvFunc: func(in envInput) ([]string, map[string][]string) {
			platform := append(
				in.EnvFromFiles(
					"azure_private_nat_subnet_id", "AZURE_PRIVATE_NAT_SUBNET_ID",
					"external_oidc_test_users", "E2E_EXTERNAL_OIDC_TEST_USERS",
				),
				in.EnvFromFilePaths(
					"external_oidc_ca_bundle", "E2E_EXTERNAL_OIDC_CA_BUNDLE_FILE",
				)...,
			)
			var suiteEnv map[string][]string
			if in.ReleaseImage != "" {
				suiteEnv = map[string][]string{
					"hypershift/azure/upgrade": {"E2E_LATEST_RELEASE_IMAGE=" + in.ReleaseImage},
				}
			}
			return platform, suiteEnv
		},
	},
	"aws": {
		Suites: []e.Suite{
			{
				Name:        "hypershift/aws/public",
				Description: "AWS public cluster non-mutating tests",
				Qualifiers:  []string{`labels.exists(l, l=="hosted-cluster-health") || labels.exists(l, l=="control-plane-workloads") || labels.exists(l, l=="hosted-cluster-metrics") || labels.exists(l, l=="hosted-cluster-image-registry") || labels.exists(l, l=="hosted-cluster-compliance") || labels.exists(l, l=="hosted-cluster-ingress") || labels.exists(l, l=="hosted-cluster-dns") || labels.exists(l, l=="hosted-cluster-security") || labels.exists(l, l=="control-plane-pki-operator") || labels.exists(l, l=="hosted-cluster-cpo") || labels.exists(l, l=="hosted-cluster-node-communication")`},
			},
		},
		TestPlan: testPlan{
			Parallel: []string{"hypershift/aws/public"},
		},
		ClusterFiles: map[string]string{
			"hypershift/aws/public": "cluster-name-public",
		},
		LabelToPool: map[string]string{
			"hosted-cluster-health":             "public",
			"control-plane-workloads":           "public",
			"hosted-cluster-metrics":            "public",
			"hosted-cluster-image-registry":     "public",
			"hosted-cluster-compliance":         "public",
			"hosted-cluster-ingress":            "public",
			"hosted-cluster-dns":                "public",
			"hosted-cluster-security":           "public",
			"control-plane-pki-operator":        "public",
			"hosted-cluster-cpo":                "public",
			"hosted-cluster-node-communication": "public",
		},
		PoolCapacity: map[string]int{
			"public": 1,
		},
	},
}

// envInput contains runtime values the entrypoint collects and passes to
// platform-specific EnvFunc implementations.
type envInput struct {
	SharedDir    string
	ReleaseImage string
}

// EnvFromFiles reads SHARED_DIR files and returns env var assignments for
// each file that exists and has non-empty contents. Arguments are variadic
// pairs of (filename, envVar).
func (in envInput) EnvFromFiles(pairs ...string) []string {
	var env []string
	for i := 0; i+1 < len(pairs); i += 2 {
		data, err := os.ReadFile(filepath.Join(in.SharedDir, pairs[i]))
		if err != nil {
			continue
		}
		if v := strings.TrimSpace(string(data)); v != "" {
			env = append(env, pairs[i+1]+"="+v)
		}
	}
	return env
}

// EnvFromFilePaths returns env var assignments where the value is the full
// file path (not the contents). Arguments are variadic pairs of (filename,
// envVar). Only existing files are included.
func (in envInput) EnvFromFilePaths(pairs ...string) []string {
	var env []string
	for i := 0; i+1 < len(pairs); i += 2 {
		path := filepath.Join(in.SharedDir, pairs[i])
		if _, err := os.Stat(path); err == nil {
			env = append(env, pairs[i+1]+"="+path)
		}
	}
	return env
}

func registerPlatformSuites(ext *e.Extension) {
	for _, cfg := range platformConfigs {
		for _, suite := range cfg.Suites {
			ext.AddSuite(suite)
		}
	}
}
