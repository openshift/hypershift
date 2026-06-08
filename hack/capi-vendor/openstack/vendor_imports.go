//go:build tools

package openstack

import (
	_ "sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha1"
	_ "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
)
