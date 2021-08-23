package config

import (
	"reflect"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSetRestartAnnotation(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{}
	testDate := "01-01-2020"
	hcp.Annotations = map[string]string{
		hyperv1.RestartDateAnnotation: testDate,
	}
	cfg := &DeploymentConfig{}
	cfg.SetRestartAnnotation(hcp.ObjectMeta)
	if cfg.AdditionalAnnotations == nil {
		t.Fatalf("Expecting additional annotations to be set")
	}
	value, isPresent := cfg.AdditionalAnnotations[hyperv1.RestartDateAnnotation]
	if !isPresent {
		t.Fatalf("Expected restart date annotation is not present")
	}
	if value != testDate {
		t.Fatalf("Expected annotation value not set")
	}
}

func TestSetMultizoneSpread(t *testing.T) {
	labels := map[string]string{"app": "etcd"}
	cfg := &DeploymentConfig{}
	cfg.SetMultizoneSpread(labels)
	if cfg.Scheduling.Affinity == nil {
		t.Fatalf("Expecting affinity to be set on config")
	}
	if len(cfg.Scheduling.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution) == 0 {
		t.Fatalf("Expecting one required pod antiaffinity rule")
	}
	expectedTerm := corev1.PodAffinityTerm{
		LabelSelector: &metav1.LabelSelector{
			MatchLabels: labels,
		},
		TopologyKey: corev1.LabelTopologyZone,
	}
	if !reflect.DeepEqual(expectedTerm, cfg.Scheduling.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0]) {
		t.Fatalf("Unexpected anti-affinity term")
	}
}

func TestSetColocation(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{}
	hcp.Name = "hcp-name"
	hcp.Namespace = "hcp-namespace"
	cfg := &DeploymentConfig{}
	cfg.SetColocation(hcp)

	if cfg.Scheduling.Affinity == nil {
		t.Fatalf("expecting affinity to be set on config")
	}
	if len(cfg.Scheduling.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution) == 0 {
		t.Fatalf("expecting pod affinity rule")
	}
	expectedTerm := corev1.WeightedPodAffinityTerm{
		PodAffinityTerm: corev1.PodAffinityTerm{
			LabelSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"hypershift.openshift.io/hosted-control-plane": "hcp-namespace"},
			},
			TopologyKey: corev1.LabelHostname,
		},
		Weight: 100,
	}
	if !reflect.DeepEqual(expectedTerm, cfg.Scheduling.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution[0]) {
		t.Fatalf("Unexpected affinity term %#v", cfg.Scheduling.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution[0])
	}
	expectedLabels := AdditionalLabels{
		"hypershift.openshift.io/hosted-control-plane": "hcp-namespace",
	}
	if !reflect.DeepEqual(expectedLabels, cfg.AdditionalLabels) {
		t.Fatalf("Unexpected additional labels")
	}
}

func TestSetControlPlaneIsolation(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{}
	hcp.Name = "hcp-name"
	hcp.Namespace = "hcp-namespace"
	cfg := &DeploymentConfig{}
	cfg.SetControlPlaneIsolation(hcp)
	if len(cfg.Scheduling.Tolerations) == 0 {
		t.Fatalf("No tolerations were set")
	}
	expectedTolerations := []corev1.Toleration{
		{
			Key:      controlPlaneWorkloadTolerationKey,
			Operator: corev1.TolerationOpEqual,
			Value:    "true",
			Effect:   corev1.TaintEffectNoSchedule,
		},
		{
			Key:      "hypershift.openshift.io/cluster",
			Operator: corev1.TolerationOpEqual,
			Value:    "hcp-namespace",
			Effect:   corev1.TaintEffectNoSchedule,
		},
	}
	if !reflect.DeepEqual(expectedTolerations, cfg.Scheduling.Tolerations) {
		t.Fatalf("unexpected tolerations")
	}
	expectedAffinityRules := []corev1.PreferredSchedulingTerm{
		{
			Weight: 50,
			Preference: corev1.NodeSelectorTerm{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{
						Key:      "hypershift.openshift.io/control-plane",
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"true"},
					},
				},
			},
		},
		{
			Weight: 100,
			Preference: corev1.NodeSelectorTerm{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{
						Key:      "hypershift.openshift.io/cluster",
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"hcp-namespace"},
					},
				},
			},
		},
	}
	if !reflect.DeepEqual(expectedAffinityRules, cfg.Scheduling.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution) {
		t.Fatalf("unexpected node affinity rules")
	}
}
