package globalconfig

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	observedConfigKey = "config"
)

type ObservedConfig struct {
	Build   *configv1.Build
	Project *configv1.Project
}

func ReconcileObservedConfig(cm *corev1.ConfigMap, config runtime.Object) error {
	serializedConfig, err := util.SerializeResource(config, api.Scheme)
	if err != nil {
		return fmt.Errorf("cannot serialize config: %w", err)
	}
	cm.Data = map[string]string{observedConfigKey: serializedConfig}
	return nil
}

func deserializeObservedConfig(cm *corev1.ConfigMap, config runtime.Object) error {
	serializedConfig, exists := cm.Data[observedConfigKey]
	if !exists {
		return fmt.Errorf("observed config key not found in configmap")
	}
	return util.DeserializeResource(serializedConfig, config, api.Scheme)
}

// ReadObservedConfig reads global configuration resources from configmaps that
// were created by the hosted-cluster-config-operator from resources inside the
// guest cluster.
func ReadObservedConfig(ctx context.Context, c client.Reader, observedConfig *ObservedConfig, namespace string) error {
	log := ctrl.LoggerFrom(ctx)
	var errs []error
	configs := map[string]struct {
		observed *corev1.ConfigMap
		dest     runtime.Object
	}{
		"project": {
			observed: ObservedProjectConfig(namespace),
			dest:     ProjectConfig(),
		},
		"build": {
			observed: ObservedBuildConfig(namespace),
			dest:     BuildConfig(),
		},
	}

	for _, config := range configs {
		if err := c.Get(ctx, client.ObjectKeyFromObject(config.observed), config.observed); err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			errs = append(errs, err)
			continue
		}

		log.Info("Observed global configuration", "name", config.observed.Name, "resourceVersion", config.observed.ResourceVersion)
		if err := deserializeObservedConfig(config.observed, config.dest); err != nil {
			errs = append(errs, err)
		}
	}

	observedConfig.Build = configs["build"].dest.(*configv1.Build)
	observedConfig.Project = configs["project"].dest.(*configv1.Project)

	return utilerrors.NewAggregate(errs)
}
