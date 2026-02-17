package primaryudn

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// EnsureUserDefinedNetwork ensures a UserDefinedNetwork CR exists in the given namespace.
// This is used for Primary UDN hosted clusters to avoid relying on imperative setup scripts.
func EnsureUserDefinedNetwork(ctx context.Context, c client.Client, namespace, name, subnetCIDR string) error {
	if name == "" {
		return nil
	}
	if subnetCIDR == "" {
		return fmt.Errorf("primary UDN subnet is required")
	}

	udn := &unstructured.Unstructured{}
	udn.SetAPIVersion("k8s.ovn.org/v1")
	udn.SetKind("UserDefinedNetwork")
	udn.SetNamespace(namespace)
	udn.SetName(name)

	_, err := controllerutil.CreateOrUpdate(ctx, c, udn, func() error {
		udn.Object["spec"] = map[string]any{
			"topology": "Layer2",
			"layer2": map[string]any{
				"role":    "Primary",
				"subnets": []any{subnetCIDR},
				"ipam": map[string]any{
					"mode":      "Enabled",
					"lifecycle": "Persistent",
				},
			},
		}
		return nil
	})
	return err
}
