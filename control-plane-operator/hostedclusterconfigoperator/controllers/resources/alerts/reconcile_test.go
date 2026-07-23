package crd

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func TestReconcileApiUsageRule(t *testing.T) {
	tests := []struct {
		name         string
		validateRule func(t *testing.T, rule *prometheusoperatorv1.PrometheusRule)
	}{
		{
			name: "When reconciling api usage rule, it should populate the spec with two alert rules",
			validateRule: func(t *testing.T, rule *prometheusoperatorv1.PrometheusRule) {
				if len(rule.Spec.Groups) != 1 {
					t.Fatalf("expected 1 group, got %d", len(rule.Spec.Groups))
				}
				group := rule.Spec.Groups[0]
				if group.Name != "pre-release-lifecycle" {
					t.Errorf("expected group name 'pre-release-lifecycle', got %q", group.Name)
				}
				if len(group.Rules) != 2 {
					t.Fatalf("expected 2 rules, got %d", len(group.Rules))
				}
			},
		},
		{
			name: "When reconciling api usage rule, it should reference k8s 1.37 in APIRemovedInNextReleaseInUse",
			validateRule: func(t *testing.T, rule *prometheusoperatorv1.PrometheusRule) {
				expr := string(rule.Spec.Groups[0].Rules[0].Expr.StrVal)
				if !strings.Contains(expr, `removed_release="1.37"`) {
					t.Errorf("APIRemovedInNextReleaseInUse should filter on removed_release 1.37, got expr: %s", expr)
				}
			},
		},
		{
			name: "When reconciling api usage rule, it should reference k8s 1.37 and 1.38 in APIRemovedInNextEUSReleaseInUse",
			validateRule: func(t *testing.T, rule *prometheusoperatorv1.PrometheusRule) {
				expr := string(rule.Spec.Groups[0].Rules[1].Expr.StrVal)
				if !strings.Contains(expr, `removed_release=~"1.3[78]"`) {
					t.Errorf("APIRemovedInNextEUSReleaseInUse should filter on removed_release 1.37 and 1.38, got expr: %s", expr)
				}
			},
		},
		{
			name: "When reconciling api usage rule, it should include removed_release label in group by clause",
			validateRule: func(t *testing.T, rule *prometheusoperatorv1.PrometheusRule) {
				for i, alertRule := range rule.Spec.Groups[0].Rules {
					expr := string(alertRule.Expr.StrVal)
					if !strings.Contains(expr, "group by (group,version,resource,removed_release)") {
						t.Errorf("rule %d (%s) should include removed_release in group by clause, got expr: %s", i, alertRule.Alert, expr)
					}
				}
			},
		},
		{
			name: "When reconciling api usage rule, it should include kubernetes version in description annotations",
			validateRule: func(t *testing.T, rule *prometheusoperatorv1.PrometheusRule) {
				for i, alertRule := range rule.Spec.Groups[0].Rules {
					desc := alertRule.Annotations["description"]
					if !strings.Contains(desc, "{{ $labels.removed_release }}") {
						t.Errorf("rule %d (%s) description should reference removed_release label, got: %s", i, alertRule.Alert, desc)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := &prometheusoperatorv1.PrometheusRule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "api-usage",
					Namespace: "openshift-kube-apiserver",
				},
			}
			if err := ReconcileApiUsageRule(rule); err != nil {
				t.Fatalf("ReconcileApiUsageRule returned error: %v", err)
			}
			tt.validateRule(t, rule)
		})
	}
}
