//go:build tools

package ibmcloud

import (
	_ "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta1"
	_ "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
)
