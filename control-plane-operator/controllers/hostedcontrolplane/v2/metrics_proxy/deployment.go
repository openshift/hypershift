package metricsproxy

import (
	"fmt"
	"sort"
	"strings"

	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/gmpmonitoring"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	metricsSet := cpContext.MetricsSet
	if fwdSet := cpContext.HCP.Spec.Monitoring.MetricsForwarding.MetricsSet; fwdSet != "" {
		metricsSet = metrics.MetricsSet(fwdSet)
	}
	if metricsSet == "" {
		metricsSet = metrics.MetricsSetAll
	}

	namespace := cpContext.HCP.Namespace

	// Discover cert volumes from the monitors of the selected backend.
	volumes, volumeMounts, err := certVolumesFromMonitors(cpContext, namespace)
	if err != nil {
		return fmt.Errorf("failed to build cert volumes: %w", err)
	}

	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args,
			"--metrics-set", string(metricsSet),
		)
		c.VolumeMounts = append(c.VolumeMounts, volumeMounts...)
	})

	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, volumes...)

	return nil
}

// certRef tracks a unique volume source reference discovered from monitor TLS configs.
type certRef struct {
	name     string // resource name (Secret or ConfigMap)
	isSecret bool
	items    []corev1.KeyToPath // specific key mapping for ConfigMap volumes
}

// certVolumesFromMonitors lists the monitors of the selected backend in the
// namespace (ServiceMonitors and PodMonitors, or GMP PodMonitorings) and collects
// unique Secret and ConfigMap references from their TLS configurations,
// generating corresponding volumes and volume mounts.
func certVolumesFromMonitors(cpContext component.WorkloadContext, namespace string) ([]corev1.Volume, []corev1.VolumeMount, error) {
	// Collect unique cert references. Use a map keyed by resource name to deduplicate.
	refs := make(map[string]*certRef)

	if cpContext.EnableGMPMonitoring {
		if err := collectPodMonitoringCertRefs(cpContext, namespace, refs); err != nil {
			return nil, nil, err
		}
	} else if err := collectCoreOSMonitorCertRefs(cpContext, namespace, refs); err != nil {
		return nil, nil, err
	}

	// Sort by name for deterministic output.
	names := make([]string, 0, len(refs))
	for name := range refs {
		names = append(names, name)
	}
	sort.Strings(names)

	var volumes []corev1.Volume
	var mounts []corev1.VolumeMount

	for _, name := range names {
		ref := refs[name]
		volName := sanitizeVolumeName(name)
		vol := corev1.Volume{
			Name: volName,
		}

		if ref.isSecret {
			vol.VolumeSource.Secret = &corev1.SecretVolumeSource{
				SecretName: name,
				Optional:   ptr.To(true),
			}
		} else {
			vol.VolumeSource.ConfigMap = &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: name,
				},
				Optional: ptr.To(true),
			}
			if len(ref.items) > 0 {
				vol.VolumeSource.ConfigMap.Items = ref.items
			}
		}

		volumes = append(volumes, vol)
		mounts = append(mounts, corev1.VolumeMount{
			Name:      volName,
			MountPath: certBasePath + "/" + name,
		})
	}

	return volumes, mounts, nil
}

// collectCoreOSMonitorCertRefs collects the TLS Secret and ConfigMap
// references of the ServiceMonitors and PodMonitors in the namespace.
func collectCoreOSMonitorCertRefs(cpContext component.WorkloadContext, namespace string, refs map[string]*certRef) error {
	// Collect from ServiceMonitors.
	smList := &prometheusoperatorv1.ServiceMonitorList{}
	if err := cpContext.Client.List(cpContext, smList, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to list ServiceMonitors: %w", err)
	}

	for i := range smList.Items {
		sm := &smList.Items[i]
		if len(sm.Spec.Endpoints) == 0 {
			continue
		}
		ep := sm.Spec.Endpoints[0]
		if ep.TLSConfig == nil {
			continue
		}

		collectSecretOrConfigMapRef(refs, ep.TLSConfig.CA)
		collectSecretOrConfigMapRef(refs, ep.TLSConfig.Cert)
		if ep.TLSConfig.KeySecret != nil {
			refs[ep.TLSConfig.KeySecret.Name] = &certRef{
				name:     ep.TLSConfig.KeySecret.Name,
				isSecret: true,
			}
		}
	}

	// Collect from PodMonitors.
	pmList := &prometheusoperatorv1.PodMonitorList{}
	if err := cpContext.Client.List(cpContext, pmList, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to list PodMonitors: %w", err)
	}

	for i := range pmList.Items {
		pm := &pmList.Items[i]
		if len(pm.Spec.PodMetricsEndpoints) == 0 {
			continue
		}
		ep := pm.Spec.PodMetricsEndpoints[0]
		if ep.TLSConfig == nil {
			continue
		}

		collectSecretOrConfigMapRef(refs, ep.TLSConfig.CA)
		collectSecretOrConfigMapRef(refs, ep.TLSConfig.Cert)
		if ep.TLSConfig.KeySecret != nil {
			refs[ep.TLSConfig.KeySecret.Name] = &certRef{
				name:     ep.TLSConfig.KeySecret.Name,
				isSecret: true,
			}
		}
	}

	return nil
}

// collectPodMonitoringCertRefs collects the TLS Secret references of the GMP
// PodMonitorings in the namespace.
func collectPodMonitoringCertRefs(cpContext component.WorkloadContext, namespace string, refs map[string]*certRef) error {
	pmList := &gmpmonitoring.PodMonitoringList{}
	if err := cpContext.Client.List(cpContext, pmList, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to list PodMonitorings: %w", err)
	}

	for i := range pmList.Items {
		pm := &pmList.Items[i]
		if len(pm.Spec.Endpoints) == 0 {
			continue
		}
		tlsCfg := pm.Spec.Endpoints[0].TLS
		if tlsCfg == nil {
			continue
		}
		for _, ref := range []*gmpmonitoring.SecretSelector{tlsCfg.CA, tlsCfg.Cert, tlsCfg.Key} {
			if ref != nil && ref.Secret != nil {
				refs[ref.Secret.Name] = &certRef{name: ref.Secret.Name, isSecret: true}
			}
		}
	}
	return nil
}

// sanitizeVolumeName converts a resource name into a valid Kubernetes volume
// name by replacing dots with dashes. Kubernetes volume names must conform to
// DNS label rules which do not allow dots.
func sanitizeVolumeName(name string) string {
	return strings.ReplaceAll(name, ".", "-")
}

// collectSecretOrConfigMapRef adds a SecretOrConfigMap reference to the refs map.
func collectSecretOrConfigMapRef(refs map[string]*certRef, ref prometheusoperatorv1.SecretOrConfigMap) {
	if ref.Secret != nil {
		if _, exists := refs[ref.Secret.Name]; !exists {
			refs[ref.Secret.Name] = &certRef{
				name:     ref.Secret.Name,
				isSecret: true,
			}
		}
	}
	if ref.ConfigMap != nil {
		name := ref.ConfigMap.Name
		existing, exists := refs[name]
		if !exists {
			refs[name] = &certRef{
				name: name,
				items: []corev1.KeyToPath{
					{Key: ref.ConfigMap.Key, Path: ref.ConfigMap.Key},
				},
			}
		} else if !existing.isSecret {
			// Add the key if not already present.
			found := false
			for _, item := range existing.items {
				if item.Key == ref.ConfigMap.Key {
					found = true
					break
				}
			}
			if !found {
				existing.items = append(existing.items, corev1.KeyToPath{
					Key: ref.ConfigMap.Key, Path: ref.ConfigMap.Key,
				})
			}
		}
	}
}
