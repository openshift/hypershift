package config

import (
	"fmt"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/api"
)

func ExtractConfig(hcp *hyperv1.HostedControlPlane, config client.Object) error {
	gvks, _, err := api.Scheme.ObjectKinds(config)
	if err != nil || len(gvks) == 0 {
		return fmt.Errorf("cannot determine object kind for %T: %w", config, err)
	}
	kind := gvks[0].Kind
	for _, cfg := range hcp.Spec.Configs {
		if cfg.Kind == kind {
			gvk := config.GetObjectKind().GroupVersionKind()
			_, _, err := api.YamlSerializer.Decode(cfg.Content.Raw, &gvk, config)
			if err != nil {
				return fmt.Errorf("cannot decode raw config of kind %s: %w", kind, err)
			}
			break
		}
	}
	return nil
}

func ExtractConfigs(hcp *hyperv1.HostedControlPlane, configs []client.Object) error {
	errs := []error{}
	for _, cfg := range configs {
		err := ExtractConfig(hcp, cfg)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return utilerrors.NewAggregate(errs)
}
