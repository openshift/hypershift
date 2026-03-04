package v1beta1

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestToKubeletConfigManifest_WhenKubeletConfigIsNil_ItShouldReturnEmptyString(t *testing.T) {
	var kc *KubeletConfiguration
	manifest, err := kc.ToKubeletConfigManifest("test-name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if manifest != "" {
		t.Errorf("expected empty manifest for nil KubeletConfig, got: %q", manifest)
	}
}

func TestToKubeletConfigManifest_WhenKubeletConfigHasMaxPods_ItShouldIncludeMaxPodsInManifest(t *testing.T) {
	kc := &KubeletConfiguration{
		MaxPods: ptr.To[int32](500),
	}
	manifest, err := kc.ToKubeletConfigManifest("karpenter-kubelet-default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, manifest, "apiVersion: machineconfiguration.openshift.io/v1")
	assertContains(t, manifest, "kind: KubeletConfig")
	assertContains(t, manifest, "name: karpenter-kubelet-default")
	assertContains(t, manifest, `"maxPods":500`)
}

func TestToKubeletConfigManifest_WhenKubeletConfigIsEmpty_ItShouldReturnManifestWithEmptyKubeletConfig(t *testing.T) {
	kc := &KubeletConfiguration{}
	manifest, err := kc.ToKubeletConfigManifest("karpenter-kubelet-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, manifest, "apiVersion: machineconfiguration.openshift.io/v1")
	assertContains(t, manifest, "kind: KubeletConfig")
	assertContains(t, manifest, "name: karpenter-kubelet-test")
	assertContains(t, manifest, `kubeletConfig: {}`)
}

func TestToKubeletConfigManifest_WhenKubeletConfigHasAllFields_ItShouldIncludeAllFieldsInManifest(t *testing.T) {
	kc := &KubeletConfiguration{
		MaxPods:     ptr.To[int32](250),
		PodsPerCore: ptr.To[int32](10),
		SystemReserved: map[string]string{
			"cpu":    "200m",
			"memory": "512Mi",
		},
		KubeReserved: map[string]string{
			"cpu":    "100m",
			"memory": "256Mi",
		},
		EvictionHard: map[string]string{
			"memory.available": "5%",
		},
		EvictionSoft: map[string]string{
			"memory.available": "10%",
		},
		EvictionSoftGracePeriod: map[string]metav1.Duration{
			"memory.available": {Duration: 2 * time.Minute},
		},
		EvictionMaxPodGracePeriod:   ptr.To[int32](90),
		ImageGCHighThresholdPercent: ptr.To[int32](85),
		ImageGCLowThresholdPercent:  ptr.To[int32](80),
		CPUCFSQuota:                 ptr.To(true),
		ClusterDNS:                  []string{"10.96.0.10"},
	}

	manifest, err := kc.ToKubeletConfigManifest("karpenter-kubelet-full")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, manifest, "apiVersion: machineconfiguration.openshift.io/v1")
	assertContains(t, manifest, "kind: KubeletConfig")
	assertContains(t, manifest, "name: karpenter-kubelet-full")
	assertContains(t, manifest, `"maxPods":250`)
	assertContains(t, manifest, `"podsPerCore":10`)
	assertContains(t, manifest, `"evictionMaxPodGracePeriod":90`)
	assertContains(t, manifest, `"imageGCHighThresholdPercent":85`)
	assertContains(t, manifest, `"imageGCLowThresholdPercent":80`)
	assertContains(t, manifest, `"cpuCFSQuota":true`)
	assertContains(t, manifest, `"clusterDNS":["10.96.0.10"]`)
	assertContains(t, manifest, `"memory.available":"5%"`)
	assertContains(t, manifest, `"memory.available":"10%"`)
	// Eviction grace period: 2 minutes = "2m0s"
	assertContains(t, manifest, "2m0s")
}

func TestToKubeletConfigManifest_WhenOnlyPodsPerCoreSet_ItShouldNotIncludeOtherFields(t *testing.T) {
	kc := &KubeletConfiguration{
		PodsPerCore: ptr.To[int32](5),
	}
	manifest, err := kc.ToKubeletConfigManifest("karpenter-kubelet-partial")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, manifest, `"podsPerCore":5`)
	assertNotContains(t, manifest, "maxPods")
	if !strings.Contains(manifest, "kubeletConfig:") {
		t.Errorf("manifest should contain 'kubeletConfig:' key")
	}
}

func TestToKubeletConfigManifestWithTaints_WhenKubeletConfigIsNil_ItShouldReturnEmptyString(t *testing.T) {
	var kc *KubeletConfiguration
	manifest, err := kc.ToKubeletConfigManifestWithTaints("test-name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if manifest != "" {
		t.Errorf("expected empty manifest for nil KubeletConfig, got: %q", manifest)
	}
}

func TestToKubeletConfigManifestWithTaints_WhenKubeletConfigHasMaxPods_ItShouldIncludeMaxPodsAndTaint(t *testing.T) {
	kc := &KubeletConfiguration{
		MaxPods: ptr.To[int32](427),
	}
	manifest, err := kc.ToKubeletConfigManifestWithTaints("karpenter-kubelet-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, manifest, "apiVersion: machineconfiguration.openshift.io/v1")
	assertContains(t, manifest, "kind: KubeletConfig")
	assertContains(t, manifest, "name: karpenter-kubelet-test")
	assertContains(t, manifest, `"maxPods":427`)
	assertContains(t, manifest, `"registerWithTaints"`)
	assertContains(t, manifest, `"karpenter.sh/unregistered"`)
	assertContains(t, manifest, `"NoExecute"`)
}

func TestToKubeletConfigManifestWithTaints_WhenKubeletConfigIsEmpty_ItShouldIncludeTaintOnly(t *testing.T) {
	kc := &KubeletConfiguration{}
	manifest, err := kc.ToKubeletConfigManifestWithTaints("karpenter-kubelet-empty")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, manifest, "apiVersion: machineconfiguration.openshift.io/v1")
	assertContains(t, manifest, "kind: KubeletConfig")
	assertContains(t, manifest, "name: karpenter-kubelet-empty")
	assertContains(t, manifest, `"registerWithTaints"`)
	assertContains(t, manifest, `"karpenter.sh/unregistered"`)
	assertContains(t, manifest, `"true"`)
	assertContains(t, manifest, `"NoExecute"`)
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected manifest to contain %q\nfull manifest:\n%s", substr, s)
	}
}

func assertNotContains(t *testing.T, s, substr string) {
	t.Helper()
	if strings.Contains(s, substr) {
		t.Errorf("expected manifest NOT to contain %q\nfull manifest:\n%s", substr, s)
	}
}
