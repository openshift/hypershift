package config

import (
	"os"
	"reflect"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
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

func TestSetMultizoneSpreadRequired(t *testing.T) {
	labels := map[string]string{
		"app":                              "etcd",
		hyperv1.ControlPlaneComponentLabel: "etcd",
	}
	cfg := &DeploymentConfig{}
	cfg.SetMultizoneSpread(labels, true)
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

func TestSetMultizoneSpreadPreferred(t *testing.T) {
	labels := map[string]string{
		"app":                              "etcd",
		hyperv1.ControlPlaneComponentLabel: "etcd",
	}
	cfg := &DeploymentConfig{}
	cfg.SetMultizoneSpread(labels, false)
	if cfg.Scheduling.Affinity == nil {
		t.Fatalf("Expecting affinity to be set on config")
	}
	if len(cfg.Scheduling.Affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution) == 0 {
		t.Fatalf("Expecting one required pod antiaffinity rule")
	}
	expectedTerm := corev1.WeightedPodAffinityTerm{
		Weight: 100,
		PodAffinityTerm: corev1.PodAffinityTerm{
			LabelSelector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			TopologyKey: corev1.LabelTopologyZone,
		},
	}
	if !reflect.DeepEqual(expectedTerm, cfg.Scheduling.Affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution[0]) {
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

func TestSetControlPlaneTolerations(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{}
	hcp.Name = "hcp-name"
	hcp.Namespace = "hcp-namespace"
	customTolerations := []corev1.Toleration{
		{
			Key:      "key1",
			Operator: corev1.TolerationOpEqual,
			Value:    "value1",
			Effect:   corev1.TaintEffectNoSchedule,
		},
		{
			Key:      "key2",
			Operator: corev1.TolerationOpEqual,
			Value:    "value2",
			Effect:   corev1.TaintEffectNoSchedule,
		},
	}
	hcp.Spec.Tolerations = append(hcp.Spec.Tolerations, customTolerations...)

	cfg := &DeploymentConfig{}
	cfg.setControlPlaneIsolation(hcp)
	cfg.setAdditionalTolerations(hcp)
	if len(cfg.Scheduling.Tolerations) == 0 {
		t.Fatalf("No tolerations were set")
	}
	expectedDefaultTolerations := []corev1.Toleration{
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

	g := NewGomegaWithT(t)

	g.Expect(cfg.Scheduling.Tolerations).To(ConsistOf(append(expectedDefaultTolerations, customTolerations...)))

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
		"app":                              "test",
		hyperv1.ControlPlaneComponentLabel: "test",
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
			Key:      hyperv1.HostedClusterLabel,
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
								Key:      hyperv1.HostedClusterLabel,
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
			{
				TopologyKey: corev1.LabelHostname,
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

func TestApplyTo(t *testing.T) {
	tests := []struct {
		name     string
		volumes  []corev1.Volume
		expected string
	}{
		{
			name: "if 3 volumes provided and 2 valids for safe annotation, it should only annotate the deployment with these 2 volume names",
			volumes: []corev1.Volume{
				{
					Name: "test-hostpath",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: "test",
						},
					},
				},
				{
					Name: "test-emptydir",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{
							Medium: corev1.StorageMedium("Memory"),
						},
					},
				},
				{
					Name: "test-volume",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "test",
						},
					},
				},
			},
			expected: "test-hostpath,test-emptydir",
		},
		{
			name: "no hostpath or emptydir volumes provided, no safe eviction annotation",
			volumes: []corev1.Volume{
				{
					Name: "test-volume",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "test",
						},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			deployment := &appsv1.Deployment{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Deployment",
					APIVersion: appsv1.SchemeGroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-deployment",
					Namespace: "test-namespace",
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-deployment",
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-container",
								},
							},
							Volumes: tc.volumes,
						},
					},
				},
			}
			dc := &DeploymentConfig{}
			dc.ApplyTo(deployment)

			localVolumeList := make([]string, 0)
			for _, volume := range deployment.Spec.Template.Spec.Volumes {
				// Check the volumes, if they are emptyDir or hostPath, should be annotated
				if volume.EmptyDir != nil || volume.HostPath != nil {
					localVolumeList = append(localVolumeList, volume.Name)
				}
			}

			// After going through all the volumes in the deployment, validates if
			// the annotation value makes sense with the expectations.
			if _, exists := deployment.Spec.Template.ObjectMeta.Annotations[PodSafeToEvictLocalVolumesKey]; exists {
				g.Expect(deployment.Spec.Template.ObjectMeta.Annotations).To(HaveKeyWithValue(PodSafeToEvictLocalVolumesKey, strings.Join(localVolumeList, ",")))
			}
		})
	}
}

func TestSetDefaultSecurityContextForPod(t *testing.T) {
	tests := []struct {
		name                    string
		config                  *DeploymentConfig
		statefulSet             bool
		envUID                  string
		isAroHCP                bool
		expectedUID             *int64
		expectedFSGroup         *int64
		expectNoSecurityContext bool
	}{
		{
			name: "when SetDefaultSecurityContext is false, it should not set any security context",
			config: &DeploymentConfig{
				SetDefaultSecurityContext: false,
			},
			expectNoSecurityContext: true,
		},
		{
			name: "when SetDefaultSecurityContext is true, it should set the default UID for non-statefulset",
			config: &DeploymentConfig{
				SetDefaultSecurityContext: true,
			},
			expectedUID: ptr.To[int64](DefaultSecurityContextUID),
		},
		{
			name: "when SetDefaultSecurityContext is true, it should set the custom UID from env for non-statefulset",
			config: &DeploymentConfig{
				SetDefaultSecurityContext: true,
			},
			envUID:      "2000",
			expectedUID: ptr.To[int64](2000),
		},
		{
			name: "when SetDefaultSecurityContext is true, invalid UID from env falls back to default",
			config: &DeploymentConfig{
				SetDefaultSecurityContext: true,
			},
			envUID:      "invalid",
			expectedUID: ptr.To[int64](DefaultSecurityContextUID),
		},
		{
			name: "when SetDefaultSecurityContext is true, it should not set any security context for statefulset without ARO HCP",
			config: &DeploymentConfig{
				SetDefaultSecurityContext: true,
			},
			statefulSet:             true,
			isAroHCP:                false,
			expectNoSecurityContext: true,
		},
		{
			name: "when SetDefaultSecurityContext is true, it should set the default UID for statefulset with ARO HCP",
			config: &DeploymentConfig{
				SetDefaultSecurityContext: true,
			},
			statefulSet:     true,
			isAroHCP:        true,
			expectedUID:     ptr.To[int64](DefaultSecurityContextUID),
			expectedFSGroup: ptr.To[int64](DefaultSecurityContextUID),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			if tt.envUID != "" {
				os.Setenv(DefaultSecurityContextUIDEnvVar, tt.envUID)
				defer os.Unsetenv(DefaultSecurityContextUIDEnvVar)
			}
			if tt.isAroHCP {
				os.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
				defer os.Unsetenv("MANAGED_SERVICE")
			}

			spec := &corev1.PodSpec{}
			tt.config.setDefaultSecurityContextForPod(spec, tt.statefulSet)

			if tt.expectNoSecurityContext {
				g.Expect(spec.SecurityContext).To(BeNil(), "expected no security context")
				return
			}

			g.Expect(spec.SecurityContext).NotTo(BeNil(), "expected security context to be set")
			g.Expect(spec.SecurityContext.RunAsUser).To(Equal(tt.expectedUID), "unexpected RunAsUser")
			g.Expect(spec.SecurityContext.FSGroup).To(Equal(tt.expectedFSGroup), "unexpected FSGroup")
		})
	}
}
