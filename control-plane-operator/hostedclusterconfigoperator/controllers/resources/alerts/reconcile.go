package crd

import (
	_ "embed"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"

	"k8s.io/apimachinery/pkg/runtime"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

var (
	//go:embed assets/apiusage.yaml
	apiUsageYaml []byte
	//go:embed assets/podsecurity-violation.yaml
	podSecurityViolationYaml []byte

	apiUsage             = createRule(apiUsageYaml)
	podSecurityViolation = createRule(podSecurityViolationYaml)
)

func ReconcileApiUsageRule(rule *prometheusoperatorv1.PrometheusRule) error {
	rule.Spec = apiUsage.Spec
	return nil
}

func ReconcilePodSecurityViolationRule(rule *prometheusoperatorv1.PrometheusRule) error {
	rule.Spec = podSecurityViolation.Spec
	return nil
}

func createRule(content []byte) *prometheusoperatorv1.PrometheusRule {
	rule := &prometheusoperatorv1.PrometheusRule{}
	deserializeResource(content, rule)
	return rule
}

func deserializeResource(data []byte, obj runtime.Object) {
	gvks, _, err := api.Scheme.ObjectKinds(obj)
	if err != nil || len(gvks) == 0 {
		panic(fmt.Sprintf("cannot determine gvk of resource: %v", err))
	}
	if _, _, err = api.YamlSerializer.Decode(data, &gvks[0], obj); err != nil {
		panic(fmt.Sprintf("cannot decode resource: %v", err))
	}
}
