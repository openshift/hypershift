//go:build tools

package aws

import (
	_ "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta1"
	_ "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	_ "sigs.k8s.io/cluster-api-provider-aws/v2/exp/api/v1beta1"
	_ "sigs.k8s.io/cluster-api-provider-aws/v2/exp/api/v1beta2"
)
