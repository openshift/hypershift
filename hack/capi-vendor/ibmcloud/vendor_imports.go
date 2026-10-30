//go:build tools

package ibmcloud

import (
	_ "sigs.k8s.io/cluster-api-provider-ibmcloud/api/powervs/v1beta2"
	_ "sigs.k8s.io/cluster-api-provider-ibmcloud/api/vpc/v1beta1"
	_ "sigs.k8s.io/cluster-api-provider-ibmcloud/api/vpc/v1beta2"
)
