package api

// These imports are used to explicitly declare external API dependencies
import (
	_ "sigs.k8s.io/cluster-api/api/addons/v1beta1"
	_ "sigs.k8s.io/cluster-api/api/core/v1beta1"
)
