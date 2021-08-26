package v1alpha1

// These imports are used to explicitly declare external API dependencies
import (
	_ "github.com/kubernetes-sigs/cluster-api-provider-ibmcloud/api/v1alpha4"
	_ "sigs.k8s.io/cluster-api-provider-aws/api/v1alpha4"
	_ "sigs.k8s.io/cluster-api-provider-aws/exp/api/v1alpha4"
	_ "sigs.k8s.io/cluster-api/api/v1alpha4"
	_ "sigs.k8s.io/cluster-api/exp/addons/api/v1alpha4"
	_ "sigs.k8s.io/cluster-api/exp/api/v1alpha4"
)
