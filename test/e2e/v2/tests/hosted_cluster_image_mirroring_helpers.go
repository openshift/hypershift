//go:build e2ev2

package tests

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	imageMirroringIgnitionName = "ignition-config-40-image-content-source"
	imageMirroringWorkerLabel  = "machineconfiguration.openshift.io/role"
	imageMirroringCoreLabel    = "hypershift.openshift.io/core-ignition-config"

	// imageMirroringVirtualSource is the fixture source repository that only
	// exists on the mirror: its ".invalid" host never resolves (RFC 2606), so a
	// successful pull from it can only have been served by an IDMS mirror.
	imageMirroringVirtualSource = "mirror-source.hypershift-e2e.invalid/libpod/registry"
)

// imageMirroringConfigured reports whether the HC opted into image mirroring
// and the mirror fixture Route exists. It only gates the specs; validity of the
// configuration is checked strictly by imageMirroringExpected.
func imageMirroringConfigured(hc *hyperv1.HostedCluster, routeFound bool) bool {
	return hc != nil && len(hc.Spec.ImageContentSources) > 0 && routeFound
}

// Expectations come only from a validated snapshot of HC input, never from a
// producer conversion or either output. Mirror priority and multiplicity matter.
func imageMirroringExpected(hc *hyperv1.HostedCluster, route *routev1.Route) ([]hyperv1.ImageContentSource, error) {
	if hc == nil || len(hc.Spec.ImageContentSources) == 0 {
		return nil, fmt.Errorf("expected nonempty HC imageContentSources from the mirror fixture")
	}
	expected := hc.DeepCopy().Spec.ImageContentSources
	seen := sets.New[string]()
	for _, entry := range expected {
		if strings.TrimSpace(entry.Source) == "" || seen.Has(entry.Source) {
			return nil, fmt.Errorf("blank or duplicate HC source %q", entry.Source)
		}
		seen.Insert(entry.Source)
		if len(entry.Mirrors) == 0 {
			return nil, fmt.Errorf("HC source %q has no mirrors", entry.Source)
		}
		for _, mirror := range entry.Mirrors {
			if strings.TrimSpace(mirror) == "" {
				return nil, fmt.Errorf("HC source %q has a blank mirror", entry.Source)
			}
		}
	}
	if route == nil || strings.TrimSpace(route.Spec.Host) == "" {
		return nil, fmt.Errorf("mirror fixture Route has an empty host")
	}
	for _, source := range []string{"quay.io/libpod/registry", imageMirroringVirtualSource} {
		found := false
		for _, entry := range expected {
			if entry.Source != source {
				continue
			}
			for _, mirror := range entry.Mirrors {
				host, path, ok := strings.Cut(mirror, "/")
				if ok && path != "" && host == route.Spec.Host {
					found = true
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("fixture source %q must have a mirror at exact Route host %q; HC mappings: %+v", source, route.Spec.Host, expected)
		}
	}
	return expected, nil
}

func imageMirroringGetIDMS(ctx context.Context, client crclient.Client) ([]*configv1.ImageDigestMirrorSet, error) {
	object := &configv1.ImageDigestMirrorSet{}
	if err := client.Get(ctx, crclient.ObjectKey{Name: "cluster"}, object); err != nil {
		return nil, err
	}
	return []*configv1.ImageDigestMirrorSet{object}, nil
}

func imageMirroringGetConfigMap(ctx context.Context, client crclient.Client, namespace string) ([]*corev1.ConfigMap, error) {
	if strings.TrimSpace(namespace) == "" {
		return nil, fmt.Errorf("ignition ConfigMap requires a nonempty control plane namespace")
	}
	object := &corev1.ConfigMap{}
	if err := client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: imageMirroringIgnitionName}, object); err != nil {
		return nil, err
	}
	return []*corev1.ConfigMap{object}, nil
}

func imageMirroringCheckIDMS(objects []*configv1.ImageDigestMirrorSet, expected []hyperv1.ImageContentSource) (bool, string, error) {
	if len(objects) != 1 || objects[0] == nil {
		return false, "expected exactly one nonnil ImageDigestMirrorSet/cluster", nil
	}
	object := objects[0]
	if object.Name != "cluster" || object.Namespace != "" {
		return false, fmt.Sprintf("expected cluster-scoped ImageDigestMirrorSet/cluster, got %s/%s", object.Namespace, object.Name), nil
	}
	if object.Labels[imageMirroringWorkerLabel] != "worker" {
		return false, fmt.Sprintf("ImageDigestMirrorSet/cluster expected worker role, got %q", object.Labels[imageMirroringWorkerLabel]), nil
	}
	return imageMirroringCheckMappings(object.Spec.ImageDigestMirrors, expected)
}

func imageMirroringCheckMappings(actual []configv1.ImageDigestMirrors, expected []hyperv1.ImageContentSource) (bool, string, error) {
	mismatch := fmt.Sprintf("IDMS mappings mismatch: expected HC mappings %+v, got %+v (mirror order and multiplicity must match)", expected, actual)
	if len(expected) == 0 || len(actual) != len(expected) {
		return false, mismatch, nil
	}
	bySource := map[string][]string{}
	for _, entry := range expected {
		if strings.TrimSpace(entry.Source) == "" || len(entry.Mirrors) == 0 {
			return false, "invalid expected HC mappings: " + mismatch, nil
		}
		if _, duplicate := bySource[entry.Source]; duplicate {
			return false, "duplicate expected HC source: " + mismatch, nil
		}
		bySource[entry.Source] = entry.Mirrors
	}
	for _, entry := range actual {
		mirrors, ok := bySource[entry.Source]
		if !ok {
			return false, mismatch, nil
		}
		got := make([]string, len(entry.Mirrors))
		for i, mirror := range entry.Mirrors {
			got[i] = string(mirror)
		}
		if !reflect.DeepEqual(got, mirrors) {
			return false, mismatch, nil
		}
		delete(bySource, entry.Source)
	}
	return true, "", nil
}

func imageMirroringCheckConfigMap(objects []*corev1.ConfigMap, namespace string, expected []hyperv1.ImageContentSource) (bool, string, error) {
	if len(objects) != 1 || objects[0] == nil {
		return false, fmt.Sprintf("expected exactly one nonnil ConfigMap %s/%s", namespace, imageMirroringIgnitionName), nil
	}
	object := objects[0]
	identity := fmt.Sprintf("ConfigMap %s/%s", object.Namespace, object.Name)
	if namespace == "" || object.Namespace != namespace || object.Name != imageMirroringIgnitionName {
		return false, identity + " is not the named HCP ignition target", nil
	}
	if object.Labels[imageMirroringCoreLabel] != "true" {
		return false, fmt.Sprintf("%s expected core-ignition-config label true, got %q", identity, object.Labels[imageMirroringCoreLabel]), nil
	}
	if strings.TrimSpace(object.Data["config"]) == "" {
		return false, identity + " has missing/empty data.config", nil
	}
	payload := &configv1.ImageDigestMirrorSet{}
	if err := yaml.Unmarshal([]byte(object.Data["config"]), payload); err != nil {
		return false, fmt.Sprintf("%s data.config YAML decode failed: %v", identity, err), nil
	}
	if payload.APIVersion != "config.openshift.io/v1" || payload.Kind != "ImageDigestMirrorSet" {
		return false, fmt.Sprintf("%s expected config.openshift.io/v1 ImageDigestMirrorSet payload, got %q %q", identity, payload.APIVersion, payload.Kind), nil
	}
	done, reason, err := imageMirroringCheckIDMS([]*configv1.ImageDigestMirrorSet{payload}, expected)
	if !done {
		reason = identity + " decoded payload: " + reason
	}
	return done, reason, err
}
