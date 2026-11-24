package assets

import (
	"embed"
	"fmt"
	"path"

	hyperapi "github.com/openshift/hypershift/support/api"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// gmpMonitoringAssets holds the Google Managed Prometheus monitors for the
// resources `hypershift install` builds in Go. They are generated from the
// CoreOS monitors by hack/gmp-config-gen.
//
//go:embed gmpmonitoring/*/gmp-podmonitoring.yaml
var gmpMonitoringAssets embed.FS

const gmpPodMonitoringManifest = "gmp-podmonitoring.yaml"

// LoadGMPPodMonitoring loads the generated GMP PodMonitoring for an install-time
// component and places it in namespace. The asset carries a placeholder
// namespace because `hypershift install` can deploy into any namespace.
func LoadGMPPodMonitoring(component, namespace string) (crclient.Object, error) {
	filePath := path.Join("gmpmonitoring", component, gmpPodMonitoringManifest)
	raw, err := gmpMonitoringAssets.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", filePath, err)
	}

	obj, _, err := hyperapi.AllMonitoringYamlSerializer.Decode(raw, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decode %s: %w", filePath, err)
	}
	object, ok := obj.(crclient.Object)
	if !ok {
		return nil, fmt.Errorf("%s decoded to %T, which is not a Kubernetes object", filePath, obj)
	}
	object.SetNamespace(namespace)
	return object, nil
}
