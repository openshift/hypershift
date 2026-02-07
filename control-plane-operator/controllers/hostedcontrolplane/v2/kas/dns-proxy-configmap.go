package kas

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DNSProxyConfigMap creates a ConfigMap containing the CoreDNS Corefile
// for the DNS proxy sidecar that routes Azure vault domains to Azure DNS
// and everything else to the management cluster DNS
func DNSProxyConfigMap(namespace string, managementClusterDNS string) *corev1.ConfigMap {
	corefile := fmt.Sprintf(`# DNS proxy for Azure Key Vault resolution via Swift interface
.:53 {
    # Forward Azure vault domains to Azure DNS (168.63.129.16)
    # These will egress via eth1 (Swift interface) due to default route
    forward vault.azure.net 168.63.129.16 {
        policy sequential
    }
    forward vaultcore.azure.net 168.63.129.16 {
        policy sequential
    }

    # Forward all other queries to management cluster DNS
    # This handles etcd-client, cluster services, and external domains
    forward . %s {
        policy sequential
    }

    # Logging and health
    errors
    log {
        class error
    }
    health {
        lameduck 5s
    }
    ready
    cache 30
    reload
}
`, managementClusterDNS)

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kas-dns-proxy",
			Namespace: namespace,
		},
		Data: map[string]string{
			"Corefile": corefile,
		},
	}
}

// adaptDNSProxyConfigMap adapts the DNS proxy ConfigMap for Swift networking
func adaptDNSProxyConfigMap(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	hcp := cpContext.HCP

	// Get management cluster DNS IP (default for AKS/ARO)
	mgmtClusterDNS := "10.130.0.10"
	if customDNS := hcp.Annotations["hypershift.openshift.io/management-cluster-dns"]; customDNS != "" {
		mgmtClusterDNS = customDNS
	}

	// Generate the Corefile configuration
	corefile := fmt.Sprintf(`# DNS proxy for Azure Key Vault resolution via Swift interface
.:53 {
    # Forward Azure vault domains to Azure DNS (168.63.129.16)
    # These will egress via eth1 (Swift interface) due to default route
    forward vault.azure.net 168.63.129.16 {
        policy sequential
    }
    forward vaultcore.azure.net 168.63.129.16 {
        policy sequential
    }

    # Forward all other queries to management cluster DNS
    # This handles etcd-client, cluster services, and external domains
    forward . %s {
        policy sequential
    }

    # Logging and health
    errors
    log {
        class error
    }
    health {
        lameduck 5s
    }
    ready
    cache 30
    reload
}
`, mgmtClusterDNS)

	cm.Data = map[string]string{
		"Corefile": corefile,
	}

	return nil
}

// enableDNSProxySidecar is a predicate that enables the DNS proxy ConfigMap
// only when Swift networking is enabled for the cluster
func enableDNSProxySidecar(cpContext component.WorkloadContext) bool {
	swiftPodNetworkInstanceCpo := cpContext.HCP.Annotations[hyperv1.SwiftPodNetworkInstanceAnnotationCpo]
	return swiftPodNetworkInstanceCpo != ""
}
