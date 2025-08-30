package api

// These imports are used to explicitly declare external API dependencies
import (
	_ "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta1"
	_ "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	_ "sigs.k8s.io/cluster-api-provider-aws/v2/exp/api/v1beta1"
	_ "sigs.k8s.io/cluster-api-provider-aws/v2/exp/api/v1beta2"
	_ "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta1"
	_ "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	_ "sigs.k8s.io/cluster-api/api/addons/v1beta1"
	_ "sigs.k8s.io/cluster-api/api/v1beta1"
	_ "sigs.k8s.io/cluster-api/exp/api/v1beta1"
)
