package ignitionconsumer

import (
	"context"
	"fmt"
	"sort"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/netutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// configDataKey is the ConfigMap data key holding an ignition config payload. It matches the
// NodePool controller's TokenSecretConfigKey and the generator's configDataKey.
const configDataKey = "config"

// userConfigCopyName is the deterministic, per-CR name of a copied user ConfigMap
// in the HCP namespace. It is namespaced by the CR name so copies for different
// NodePools sharing one HCP namespace never collide, mirroring the NTO mirror
// naming convention. User ConfigMap names are arbitrary, so copies carry the
// userConfigCopyPrefix, which no generated (HAProxy, rolloutGlobalConfig) or
// in-place (core, NTO) ConfigMap name starts with: a user ConfigMap named, e.g.,
// "apiserver-haproxy" can never share a name with a generated one.
func userConfigCopyName(crName, sourceName string) string {
	return netutil.ShortenName(userConfigCopyPrefix+sourceName, crName, validation.DNS1123SubdomainMaxLength)
}

// userConfigCopyPrefix reserves the copied-user-ConfigMap naming domain in the HCP namespace.
const userConfigCopyPrefix = "user-"

// haproxyConfigMapName is the per-CR name of the materialized apiserver-HAProxy
// ConfigMap in the HCP namespace.
func haproxyConfigMapName(crName string) string {
	return netutil.ShortenName("apiserver-haproxy", crName, validation.DNS1123SubdomainMaxLength)
}

// rolloutGlobalConfigMapName is the per-CR name of the authored rolloutGlobalConfig
// ConfigMap in the HCP namespace.
func rolloutGlobalConfigMapName(crName string) string {
	return netutil.ShortenName("rollout-global-config", crName, validation.DNS1123SubdomainMaxLength)
}

// classifyConfigs computes the rollout/mgmt reference lists and the rolloutGlobalConfig
// ConfigMap name deterministically from the source ConfigMaps, WITHOUT touching the
// cluster. Because the projected names are stable functions of the CR name and the source
// names, the full CR spec can be authored in a single write before the ConfigMaps are
// materialized — avoiding an intermediate empty-ref spec that would churn the CR (and,
// once the PayloadController is wired, thrash fleet rollouts).
//
//   - user configs are copied, so they are classified rollout under their copy name;
//   - core and NTO configs are referenced in place (by their own name), classified rollout;
//   - the HAProxy config is classified mgmt, so a management-side bump does not churn the
//     rollout hash;
//   - the rolloutGlobalConfig name is returned separately (it is referenced by the
//     dedicated spec.rolloutGlobalConfig field, not by rolloutConfigMaps).
func classifyConfigs(crName string, userConfigs, coreConfigs, ntoConfigs []corev1.ConfigMap,
) (rolloutRefs, mgmtRefs []hyperv1.ConfigMapReference, rolloutGlobalConfigName string) {
	for i := range userConfigs {
		rolloutRefs = append(rolloutRefs, hyperv1.ConfigMapReference{Name: userConfigCopyName(crName, userConfigs[i].GetName())})
	}
	for i := range coreConfigs {
		rolloutRefs = append(rolloutRefs, hyperv1.ConfigMapReference{Name: coreConfigs[i].GetName()})
	}
	for i := range ntoConfigs {
		rolloutRefs = append(rolloutRefs, hyperv1.ConfigMapReference{Name: ntoConfigs[i].GetName()})
	}
	mgmtRefs = append(mgmtRefs, hyperv1.ConfigMapReference{Name: haproxyConfigMapName(crName)})

	// Deterministic ordering so the authored spec is stable across reconciles.
	sortRefs(rolloutRefs)
	sortRefs(mgmtRefs)
	return rolloutRefs, mgmtRefs, rolloutGlobalConfigMapName(crName)
}

// materializeConfigs creates or updates the CR-owned projected ConfigMaps in hcpNamespace:
// copied user configs, the materialized HAProxy config, and the authored
// rolloutGlobalConfig. Core and NTO configs are referenced in place (already in the HCP
// namespace, owned elsewhere) and are not materialized here. owner is the IgnitionPayload
// CR used as the controller owner of every ConfigMap so they cascade-delete with the CR.
// Names match classifyConfigs exactly.
func materializeConfigs(ctx context.Context, c client.Client, hcpNamespace string, owner *hyperv1.IgnitionPayload,
	userConfigs []corev1.ConfigMap, haproxyRaw string, rolloutGlobalConfig []byte) error {
	crName := owner.GetName()

	for i := range userConfigs {
		src := &userConfigs[i]
		if err := upsertOwnedConfigMap(ctx, c, hcpNamespace, userConfigCopyName(crName, src.GetName()), owner, src.Data); err != nil {
			return err
		}
	}
	if err := upsertOwnedConfigMap(ctx, c, hcpNamespace, rolloutGlobalConfigMapName(crName), owner,
		map[string]string{configDataKey: string(rolloutGlobalConfig)}); err != nil {
		return err
	}
	// HAProxy is always classified mgmt (classifyConfigs always lists it), so materialize it
	// unconditionally to keep the mgmt reference and its ConfigMap consistent.
	if err := upsertOwnedConfigMap(ctx, c, hcpNamespace, haproxyConfigMapName(crName), owner,
		map[string]string{configDataKey: haproxyRaw}); err != nil {
		return err
	}
	return nil
}

func sortRefs(refs []hyperv1.ConfigMapReference) {
	sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
}

// upsertOwnedConfigMap creates or updates a ConfigMap in hcpNamespace with the given
// data and sets owner as its controller reference.
func upsertOwnedConfigMap(ctx context.Context, c client.Client, hcpNamespace, name string,
	owner *hyperv1.IgnitionPayload, data map[string]string) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: hcpNamespace},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, c, cm, func() error {
		if err := controllerutil.SetControllerReference(owner, cm, api.Scheme); err != nil {
			return err
		}
		cm.Data = data
		return nil
	}); err != nil {
		return fmt.Errorf("failed to project ConfigMap %s/%s: %w", hcpNamespace, name, err)
	}
	return nil
}
