package configuration

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/support/globalconfig"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ReconcileObserved(ctx context.Context, hosted HostedCluster, controlPlane ControlPlane, params ObservedConfigParams) []error {
	var errs []error
	configs := []struct {
		source     client.Object
		observedCM *corev1.ConfigMap
	}{
		{
			source:     globalconfig.BuildConfig(),
			observedCM: globalconfig.ObservedBuildConfig(params.Namespace),
		},
		{
			source:     globalconfig.ProjectConfig(),
			observedCM: globalconfig.ObservedProjectConfig(params.Namespace),
		},
	}

	ownerRef := params.OwnerRef
	for _, cfg := range configs {
		err := func() error {
			sourceConfig := cfg.source
			if err := hosted.get(ctx, client.ObjectKeyFromObject(sourceConfig), sourceConfig); err != nil {
				if apierrors.IsNotFound(err) {
					sourceConfig = nil
				} else {
					return fmt.Errorf("cannot get config (%s): %w", sourceConfig.GetName(), err)
				}
			}
			observedConfig := cfg.observedCM
			if sourceConfig == nil {
				if err := controlPlane.delete(ctx, observedConfig); err != nil && !apierrors.IsNotFound(err) {
					return fmt.Errorf("cannot delete observed config: %w", err)
				}
				return nil
			}
			if _, err := controlPlane.upsert(ctx, observedConfig, func() error {
				ownerRef.ApplyTo(observedConfig)
				return globalconfig.ReconcileObservedConfig(observedConfig, sourceConfig)
			}); err != nil {
				return err
			}
			return nil
		}()
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs

}
