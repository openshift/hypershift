package etcd

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	etcdutil "github.com/openshift/hypershift/support/etcd"
)

// MemberClientEndpoints returns the per-member client URLs of the default
// (unsharded) etcd cluster, using the stable StatefulSet pod DNS names on the
// etcd-discovery headless service (e.g. https://etcd-0.etcd-discovery.<ns>.svc:2379).
//
// Clients configured with the single etcd-client ClusterIP service URL pin their
// one long-lived connection to a single member, so a member that stays reachable
// on the client port but cannot reach quorum keeps serving errors while etcd
// quorum remains intact (OCPBUGS-139923). Configuring one endpoint per member
// lets the etcd client fail over to another member, which is also how standalone
// OpenShift configures its kube-apiserver.
//
// The replica count mirrors the etcd StatefulSet (component.DefaultReplicas),
// and the *.etcd-discovery.<ns>.svc wildcard in the etcd server certificate
// SANs covers these names, so no PKI changes are needed.
func MemberClientEndpoints(hcp *hyperv1.HostedControlPlane) []string {
	replicas := component.DefaultReplicas(hcp, &etcd{}, ComponentName)
	discoveryService := etcdutil.DiscoveryServiceName(ComponentName)
	endpoints := make([]string, 0, replicas)
	for i := range replicas {
		endpoints = append(endpoints, fmt.Sprintf("https://etcd-%d.%s.%s.svc:2379", i, discoveryService, hcp.Namespace))
	}
	return endpoints
}
