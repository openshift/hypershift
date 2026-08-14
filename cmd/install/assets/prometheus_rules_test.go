package assets

import (
	"testing"

	. "github.com/onsi/gomega"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func TestPrometheusRuleSpec(t *testing.T) {
	tests := map[string]struct {
		testCase func(*GomegaWithT)
	}{
		"When prometheusRuleSpec is called, it should return a valid PrometheusRuleSpec": {
			testCase: func(g *GomegaWithT) {
				spec := prometheusRuleSpec()
				g.Expect(spec.Groups).ToNot(BeEmpty(), "spec should contain rule groups")
			},
		},
		"When prometheusRuleSpec is called, it should contain hypershift rules group": {
			testCase: func(g *GomegaWithT) {
				spec := prometheusRuleSpec()
				g.Expect(spec.Groups).To(HaveLen(1), "should contain exactly one rule group")

				group := spec.Groups[0]
				g.Expect(group.Name).To(Equal("hypershift.rules"))
				g.Expect(group.Interval).ToNot(BeNil())
				g.Expect(string(*group.Interval)).To(Equal("30s"))
			},
		},
		"When prometheusRuleSpec is called, it should contain expected recording rules": {
			testCase: func(g *GomegaWithT) {
				spec := prometheusRuleSpec()
				group := spec.Groups[0]

				// Verify we have the expected number of rules
				g.Expect(group.Rules).To(HaveLen(16), "should contain all expected recording rules")

				// Check specific recording rules exist
				ruleNames := make([]string, len(group.Rules))
				for i, rule := range group.Rules {
					g.Expect(rule.Record).ToNot(BeEmpty(), "all rules should have record names")
					g.Expect(rule.Expr.String()).ToNot(BeEmpty(), "all rules should have expressions")
					ruleNames[i] = rule.Record
				}

				expectedRules := []string{
					"hypershift:apiserver_request_total:read",
					"hypershift:apiserver_request_total:write",
					"hypershift:apiserver_request_total:client",
					"hypershift:apiserver_request_aborts_total",
					"hypershift:controlplane:component_api_requests_total",
					"hypershift:controlplane:component_memory_usage",
					"hypershift:controlplane:component_memory_rss",
					"hypershift:controlplane:component_memory_request",
					"hypershift:controlplane:ign_payload_generation_seconds_p90",
					"hypershift:controlplane:component_cpu_usage_seconds",
					"hypershift:controlplane:component_cpu_request",
					"hypershift:operator:component_api_requests_total",
					"platform:hypershift_hostedclusters:max",
					"platform:hypershift_nodepools:max",
					"cluster_name:hypershift_nodepools_size:sum",
					"cluster_name:hypershift_nodepools_available_replicas:sum",
				}

				for _, expectedRule := range expectedRules {
					g.Expect(ruleNames).To(ContainElement(expectedRule), "should contain rule: %s", expectedRule)
				}
			},
		},
		"When prometheusRuleSpec is called, it should have valid apiserver read rule": {
			testCase: func(g *GomegaWithT) {
				spec := prometheusRuleSpec()
				group := spec.Groups[0]

				var readRule *prometheusoperatorv1.Rule
				for _, rule := range group.Rules {
					if rule.Record == "hypershift:apiserver_request_total:read" {
						readRule = &rule
						break
					}
				}

				g.Expect(readRule).ToNot(BeNil(), "should find read rule")
				g.Expect(readRule.Expr.String()).To(ContainSubstring("verb=~\"LIST|GET|WATCH\""))
				g.Expect(readRule.Expr.String()).To(ContainSubstring("rate(apiserver_request_total"))
			},
		},
		"When prometheusRuleSpec is called, it should have valid apiserver write rule": {
			testCase: func(g *GomegaWithT) {
				spec := prometheusRuleSpec()
				group := spec.Groups[0]

				var writeRule *prometheusoperatorv1.Rule
				for _, rule := range group.Rules {
					if rule.Record == "hypershift:apiserver_request_total:write" {
						writeRule = &rule
						break
					}
				}

				g.Expect(writeRule).ToNot(BeNil(), "should find write rule")
				g.Expect(writeRule.Expr.String()).To(ContainSubstring("verb=~\"POST|PUT|PATCH|UPDATE|DELETE|APPLY\""))
				g.Expect(writeRule.Expr.String()).To(ContainSubstring("rate(apiserver_request_total"))
			},
		},
		"When prometheusRuleSpec is called, it should have valid component memory usage rule": {
			testCase: func(g *GomegaWithT) {
				spec := prometheusRuleSpec()
				group := spec.Groups[0]

				var memoryRule *prometheusoperatorv1.Rule
				for _, rule := range group.Rules {
					if rule.Record == "hypershift:controlplane:component_memory_usage" {
						memoryRule = &rule
						break
					}
				}

				g.Expect(memoryRule).ToNot(BeNil(), "should find memory usage rule")
				g.Expect(memoryRule.Expr.String()).To(ContainSubstring("container_memory_usage_bytes"))
				g.Expect(memoryRule.Expr.String()).To(ContainSubstring("label_hypershift_openshift_io_control_plane_component"))
			},
		},
		"When prometheusRuleSpec is called, it should have valid platform hostedclusters rule": {
			testCase: func(g *GomegaWithT) {
				spec := prometheusRuleSpec()
				group := spec.Groups[0]

				var platformRule *prometheusoperatorv1.Rule
				for _, rule := range group.Rules {
					if rule.Record == "platform:hypershift_hostedclusters:max" {
						platformRule = &rule
						break
					}
				}

				g.Expect(platformRule).ToNot(BeNil(), "should find platform hostedclusters rule")
				g.Expect(platformRule.Expr.String()).To(ContainSubstring("max by(platform)"))
				g.Expect(platformRule.Expr.String()).To(ContainSubstring("hypershift_hostedclusters"))
			},
		},
		"When prometheusRuleSpec is called, it should have cluster name aggregation rules": {
			testCase: func(g *GomegaWithT) {
				spec := prometheusRuleSpec()
				group := spec.Groups[0]

				clusterRules := []string{
					"cluster_name:hypershift_nodepools_size:sum",
					"cluster_name:hypershift_nodepools_available_replicas:sum",
				}

				ruleNames := make([]string, len(group.Rules))
				for i, rule := range group.Rules {
					ruleNames[i] = rule.Record
				}

				for _, expectedRule := range clusterRules {
					g.Expect(ruleNames).To(ContainElement(expectedRule), "should contain cluster aggregation rule: %s", expectedRule)
				}
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			test.testCase(g)
		})
	}
}

func TestGetPrometheusRuleSpec(t *testing.T) {
	tests := map[string]struct {
		testCase func(*GomegaWithT)
	}{
		"When getPrometheusRuleSpec is called with valid YAML file, it should parse successfully": {
			testCase: func(g *GomegaWithT) {
				spec := getPrometheusRuleSpec(recordingRules, "recordingrules/hypershift.yaml")

				g.Expect(spec.Groups).ToNot(BeEmpty(), "should parse groups from YAML")
				g.Expect(spec.Groups[0].Name).To(Equal("hypershift.rules"))
				g.Expect(spec.Groups[0].Rules).ToNot(BeEmpty(), "should parse rules from YAML")
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			test.testCase(g)
		})
	}
}

func TestGetContents(t *testing.T) {
	tests := map[string]struct {
		testCase func(*GomegaWithT)
	}{
		"When getContents is called with valid file, it should return file content": {
			testCase: func(g *GomegaWithT) {
				content := getContents(recordingRules, "recordingrules/hypershift.yaml")

				g.Expect(content).ToNot(BeEmpty(), "should return file content")
				g.Expect(string(content)).To(ContainSubstring("groups:"), "should contain YAML groups key")
				g.Expect(string(content)).To(ContainSubstring("hypershift.rules"), "should contain rule group name")
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			test.testCase(g)
		})
	}
}
