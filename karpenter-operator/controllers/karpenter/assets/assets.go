package assets

import (
	"embed"
)

const DefaultKarpenterProviderAWSImage = "public.ecr.aws/karpenter/controller:1.2.3"

const (
	// Karpenter Metrics
	// from: https://github.com/openshift/kubernetes-sigs-karpenter/blob/9ec6578ef19c3d8fdbaeb00d9ea87d8371bdd3d0/pkg/operator/operator.go#L70
	KarpenterBuildInfoMetricName = "karpenter_build_info"

	// Karpenter Operator Metrics
	KarpenterOperatorInfoMetricName = "karpenter_operator_info"
)

//go:embed *.yaml
var f embed.FS

// ReadFile reads and returns the content of the named file.
func ReadFile(name string) ([]byte, error) {
	return f.ReadFile(name)
}
