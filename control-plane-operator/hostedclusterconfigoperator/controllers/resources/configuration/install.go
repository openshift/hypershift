package configuration

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
)

// ReconcileInstallMetadata reconciles the install metadata ConfigMap, recording
// the invoker and, when the version is not yet set, the release version.
//
// Callers must provide a callable componentVersions accessor whenever initial
// version population can occur. The lookup is deliberately lazy: an existing
// "version" key avoids calling the accessor. A nil accessor is not valid when
// that key is absent; no eager lookup or alternative nil/error policy is provided.
func ReconcileInstallMetadata(ctx context.Context, hosted HostedCluster, componentVersions func() (map[string]string, error)) error {
	cm := manifests.InstallConfigMap()
	if _, err := hosted.upsert(ctx, cm, func() error {
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data["invoker"] = "hypershift"
		if _, hasVersion := cm.Data["version"]; !hasVersion {
			versions, err := componentVersions()
			if err != nil {
				return fmt.Errorf("failed to look up component versions: %w", err)
			}
			cm.Data["version"] = versions["release"]
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile install configmap: %w", err)
	}
	return nil
}
