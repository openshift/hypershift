package egressfirewall

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func VirtLauncherEgressFirewall(namespace string) *unstructured.Unstructured {
	egressFirewall := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":      "default",
				"namespace": namespace,
			},
		},
	}
	egressFirewall.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "k8s.ovn.org",
		Kind:    "EgressFirewall",
		Version: "v1",
	})
	return egressFirewall
}
