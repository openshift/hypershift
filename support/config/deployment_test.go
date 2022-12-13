package config

import (
	"reflect"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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

func TestSetReleaseImageAnnotation(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{}
	hcp.Spec.ReleaseImage = "quay.io/openshift-release-dev/ocp-release:4.8.26-x86_64"
	hcp.Annotations = map[string]string{
		hyperv1.ReleaseImageAnnotation: hcp.Spec.ReleaseImage,
	}
	cfg := &DeploymentConfig{}
	cfg.SetReleaseImageAnnotation(hcp.Spec.ReleaseImage)
	if cfg.AdditionalAnnotations == nil {
		t.Fatalf("Expecting additional annotations to be set")
	}
	value, isPresent := cfg.AdditionalAnnotations[hyperv1.ReleaseImageAnnotation]
	if !isPresent {
		t.Fatalf("Expected restart date annotation is not present")
	}
	if value != hcp.Spec.ReleaseImage {
		t.Fatalf("Expected annotation value not set")
	}
}

func TestSetMultizoneSpread(t *testing.T) {
	labels := map[string]string{
		"app":                         "etcd",
		hyperv1.ControlPlaneComponent: "etcd",
	}
	cfg := &DeploymentConfig{}
	cfg.setMultizoneSpread(labels)
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
	cfg.setColocation(hcp)

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
	cfg.setControlPlaneIsolation(hcp)
	if len(cfg.Scheduling.Tolerations) == 0 {
		t.Fatalf("No tolerations were set")
	}
	expectedTolerations := []corev1.Toleration{
		{
			Key:      controlPlaneLabelTolerationKey,
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

func TestSetLocation(t *testing.T) {
	expectedNodeSelector := map[string]string{
		"role.kubernetes.io/infra": "",
	}
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "name",
			Namespace: "namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			NodeSelector: expectedNodeSelector,
		},
		Status: hyperv1.HostedControlPlaneStatus{},
	}

	cfg := &DeploymentConfig{
		Replicas: 2,
	}
	labels := map[string]string{
		"app":                         "test",
		hyperv1.ControlPlaneComponent: "test",
	}

	g := NewGomegaWithT(t)
	cfg.setLocation(hcp, labels)

	expected := &DeploymentConfig{
		Scheduling: Scheduling{
			NodeSelector: expectedNodeSelector,
		},
	}

	// setNodeSelector expectations.
	g.Expect(expected.Scheduling.NodeSelector).To(BeEquivalentTo(cfg.Scheduling.NodeSelector))

	// setControlPlaneIsolation expectations. NodeAffinity.
	expected.Scheduling.Tolerations = []corev1.Toleration{
		{
			Key:      controlPlaneLabelTolerationKey,
			Operator: corev1.TolerationOpEqual,
			Value:    "true",
			Effect:   corev1.TaintEffectNoSchedule,
		},
		{
			Key:      clusterLabelTolerationKey,
			Operator: corev1.TolerationOpEqual,
			Value:    hcp.Namespace,
			Effect:   corev1.TaintEffectNoSchedule,
		},
	}

	expected.Scheduling.Affinity = &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: nil,
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{
				{
					Weight: 50,
					Preference: corev1.NodeSelectorTerm{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{
								Key:      controlPlaneLabelTolerationKey,
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
								Key:      clusterLabelTolerationKey,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{hcp.Namespace},
							},
						},
					},
				},
			},
		},
		PodAntiAffinity: nil,
	}
	g.Expect(expected.Scheduling.Tolerations).To(BeEquivalentTo(cfg.Scheduling.Tolerations))
	g.Expect(expected.Scheduling.Affinity.NodeAffinity).To(BeEquivalentTo(cfg.Scheduling.Affinity.NodeAffinity))

	// setColocation expectations. PodAffinity.
	expected.AdditionalLabels = AdditionalLabels{
		colocationLabelKey: hcp.Namespace,
	}
	expected.Scheduling.Affinity.PodAffinity = &corev1.PodAffinity{
		PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
			{
				PodAffinityTerm: corev1.PodAffinityTerm{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{colocationLabelKey: hcp.Namespace},
					},
					TopologyKey: corev1.LabelHostname,
				},
				Weight: 100,
			},
		},
	}
	g.Expect(expected.Scheduling.Affinity.PodAffinity).To(BeEquivalentTo(cfg.Scheduling.Affinity.PodAffinity))
	g.Expect(expected.AdditionalLabels).To(BeEquivalentTo(cfg.AdditionalLabels))

	// setMultizoneSpread expectations. PodAntiAffinity.
	expected.Scheduling.Affinity.PodAntiAffinity = &corev1.PodAntiAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
			{
				TopologyKey: corev1.LabelTopologyZone,
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: labels,
				},
			},
		},
	}
	g.Expect(expected.Scheduling.Affinity.PodAntiAffinity).To(BeEquivalentTo(cfg.Scheduling.Affinity.PodAntiAffinity))
}

func TestResourceRequestOverrides(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		expected    ResourceOverrides
	}{
		{
			name:     "no annotations",
			expected: ResourceOverrides{},
		},
		{
			name: "override memory",
			annotations: map[string]string{
				"random": "foobar",
				"resource-request-override.hypershift.openshift.io/my-deployment.main-container": "memory=1Gi",
			},
			expected: ResourceOverrides{
				"my-deployment": ResourcesSpec{
					"main-container": corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				},
			},
		},
		{
			name: "override memory and cpu",
			annotations: map[string]string{
				"random": "foobar",
				"resource-request-override.hypershift.openshift.io/my-deployment.main-container": "memory=1Gi,cpu=500m",
			},
			expected: ResourceOverrides{
				"my-deployment": ResourcesSpec{
					"main-container": corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("1Gi"),
							corev1.ResourceCPU:    resource.MustParse("500m"),
						},
					},
				},
			},
		},
		{
			name: "overrides for different containers",
			annotations: map[string]string{
				"random": "foobar",
				"resource-request-override.hypershift.openshift.io/my-deployment.main-container": "memory=1Gi",
				"resource-request-override.hypershift.openshift.io/my-deployment.init-container": "cpu=500m",
			},
			expected: ResourceOverrides{
				"my-deployment": ResourcesSpec{
					"main-container": corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
					"init-container": corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("500m"),
						},
					},
				},
			},
		},
		{
			name: "overrides for different deployments",
			annotations: map[string]string{
				"random": "foobar",
				"resource-request-override.hypershift.openshift.io/my-deployment.main-container":     "memory=1Gi",
				"resource-request-override.hypershift.openshift.io/my-deployment.init-container":     "cpu=500m",
				"resource-request-override.hypershift.openshift.io/second-deployment.main-container": "cpu=300m,memory=500Mi",
			},
			expected: ResourceOverrides{
				"my-deployment": ResourcesSpec{
					"main-container": corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
					"init-container": corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("500m"),
						},
					},
				},
				"second-deployment": ResourcesSpec{
					"main-container": corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("500Mi"),
							corev1.ResourceCPU:    resource.MustParse("300m"),
						},
					},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			hcp := &hyperv1.HostedControlPlane{}
			hcp.Annotations = test.annotations
			actual := resourceRequestOverrides(hcp)
			g.Expect(actual).To(Equal(test.expected))
		})
	}
}
