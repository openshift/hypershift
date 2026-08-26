package controlplanecomponent

import (
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	fuzz "github.com/google/gofuzz"
)

func cmVolume(name string) corev1.Volume {
	return corev1.Volume{
		Name: name,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: name,
				},
			},
		},
	}
}

func secretVolume(name string) corev1.Volume {
	return corev1.Volume{
		Name: name,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: name,
			},
		},
	}
}

func emptyDirVolume(name string) corev1.Volume {
	return corev1.Volume{
		Name: name,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}
}

type projectedVolumeBuilder struct {
	v corev1.Volume
}

func (b *projectedVolumeBuilder) volume() corev1.Volume {
	return b.v
}

func (b *projectedVolumeBuilder) withSecrets(secretNames ...string) *projectedVolumeBuilder {
	for _, name := range secretNames {
		b.v.VolumeSource.Projected.Sources = append(b.v.VolumeSource.Projected.Sources, corev1.VolumeProjection{
			Secret: &corev1.SecretProjection{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: name,
				},
			},
		})
	}
	return b
}

func (b *projectedVolumeBuilder) withConfigMaps(cmNames ...string) *projectedVolumeBuilder {
	for _, name := range cmNames {
		b.v.VolumeSource.Projected.Sources = append(b.v.VolumeSource.Projected.Sources, corev1.VolumeProjection{
			ConfigMap: &corev1.ConfigMapProjection{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: name,
				},
			},
		})
	}
	return b
}

func (b *projectedVolumeBuilder) withDownardAPI(fields ...string) *projectedVolumeBuilder {
	for _, field := range fields {
		b.v.VolumeSource.Projected.Sources = append(b.v.VolumeSource.Projected.Sources, corev1.VolumeProjection{
			DownwardAPI: &corev1.DownwardAPIProjection{
				Items: []corev1.DownwardAPIVolumeFile{
					{
						Path: field,
						FieldRef: &corev1.ObjectFieldSelector{
							APIVersion: "v1",
							FieldPath:  field,
						},
					},
				},
			},
		})
	}
	return b
}

func projectedVolume(volumeName string) *projectedVolumeBuilder {
	return &projectedVolumeBuilder{
		v: corev1.Volume{
			Name: volumeName,
			VolumeSource: corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{},
			},
		},
	}
}

func TestPodConfigMapNames(t *testing.T) {
	tests := []struct {
		name    string
		podSpec corev1.PodSpec
		exclude []string
		expect  []string
	}{
		{
			name: "volumes",
			podSpec: corev1.PodSpec{
				Volumes: []corev1.Volume{cmVolume("cm1"), secretVolume("s1"), cmVolume("cm2"), emptyDirVolume("e1")},
			},
			expect: []string{"cm1", "cm2"},
		},
		{
			name: "projected",
			podSpec: corev1.PodSpec{
				Volumes: []corev1.Volume{
					projectedVolume("proj1").
						withConfigMaps("pcm1", "pcm2").
						withSecrets("ps1", "ps2").
						withDownardAPI("f1", "f2").volume(),
					secretVolume("s1")},
			},
			expect: []string{"pcm1", "pcm2"},
		},
		{
			name: "volumes and projected",
			podSpec: corev1.PodSpec{
				Volumes: []corev1.Volume{cmVolume("cm1"), cmVolume("cm2"),
					projectedVolume("proj1").
						withConfigMaps("pcm3").
						withSecrets("ps2").volume(),
					projectedVolume("proj2").
						withConfigMaps("pcm4").
						withSecrets("ps3").volume(),
					secretVolume("s1")},
			},
			expect: []string{"cm1", "cm2", "pcm3", "pcm4"},
		},
		{
			name: "volumes and projected with exclusions",
			podSpec: corev1.PodSpec{
				Volumes: []corev1.Volume{cmVolume("cm1"), cmVolume("cm2"),
					projectedVolume("proj1").
						withConfigMaps("pcm3").
						withSecrets("ps2").volume(),
					projectedVolume("proj2").
						withConfigMaps("pcm4").
						withSecrets("ps3").volume(),
					secretVolume("s1")},
			},
			exclude: []string{"cm2"},
			expect:  []string{"cm1", "pcm3", "pcm4"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := podConfigMapNames(&test.podSpec, test.exclude)
			sort.StringSlice(result).Sort()
			sort.StringSlice(test.expect).Sort()
			g.Expect(result).To(Equal(test.expect))
		})
	}
}

func TestPodSecretNames(t *testing.T) {
	tests := []struct {
		name    string
		podSpec corev1.PodSpec
		expect  []string
	}{
		{
			name: "volumes",
			podSpec: corev1.PodSpec{
				Volumes: []corev1.Volume{cmVolume("cm1"), secretVolume("s1"), secretVolume("s2"), emptyDirVolume("e3")},
			},
			expect: []string{"s1", "s2"},
		},
		{
			name: "projected",
			podSpec: corev1.PodSpec{
				Volumes: []corev1.Volume{
					projectedVolume("pcms1").
						withConfigMaps("pcm1").
						withSecrets("ps1").volume(),
					projectedVolume("pcms2").
						withConfigMaps("pcm2").
						withSecrets("ps2").
						withDownardAPI("f1").volume(),
					cmVolume("cm1"),
				},
			},
			expect: []string{"ps2", "ps1"},
		},
		{
			name: "volumes and projected",
			podSpec: corev1.PodSpec{
				Volumes: []corev1.Volume{secretVolume("s1"), secretVolume("s2"),
					projectedVolume("pss").withSecrets("ps3").volume(),
					projectedVolume("pss2").withSecrets("ps4").withConfigMaps("pcm1").volume(),
					cmVolume("cm1"),
				},
			},
			expect: []string{"s1", "s2", "ps3", "ps4"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := podSecretNames(&test.podSpec)
			sort.StringSlice(result).Sort()
			sort.StringSlice(test.expect).Sort()
			g.Expect(result).To(Equal(test.expect))
		})
	}
}

func TestComputeResourceHashConsistency(t *testing.T) {
	g := NewGomegaWithT(t)
	for range 10 {
		hashValue := ""
		secretData, configMapData := generateResources()
		for range 100 {
			result, err := computeResourceHash(_resourceKeys(secretData), _resourceKeys(configMapData),
				func(name string) (*corev1.Secret, error) {
					return secretData[name].DeepCopy(), nil
				},
				func(name string) (*corev1.ConfigMap, error) {
					return configMapData[name].DeepCopy(), nil
				})
			g.Expect(err).ToNot(HaveOccurred())
			if hashValue == "" {
				hashValue = result
			} else {
				g.Expect(result).To(Equal(hashValue), "Hash value must remain the same for the same data")
			}
		}
	}
}

func _resourceKeys[T client.Object](resources map[string]T) []string {
	result := make([]string, 0, len(resources))
	for k := range resources {
		result = append(result, k)
	}
	return result
}

func generateResources() (map[string]*corev1.Secret, map[string]*corev1.ConfigMap) {
	f := fuzz.New()
	secrets := map[string]*corev1.Secret{}
	for range 10 {
		secret := &corev1.Secret{}
		f.Fuzz(secret)
		secrets[secret.Name] = secret
	}
	configMaps := map[string]*corev1.ConfigMap{}
	for range 10 {
		cm := &corev1.ConfigMap{}
		f.Fuzz(cm)
		configMaps[cm.Name] = cm
	}
	return secrets, configMaps
}

func TestApplyRequestsOverrides(t *testing.T) {
	tests := []struct {
		name                   string
		annotations            map[string]string
		containers             []corev1.Container
		initContainers         []corev1.Container
		expectedContainers     []corev1.Container
		expectedInitContainers []corev1.Container
	}{
		{
			name: "When overriding cpu and memory it should only update requests",
			annotations: map[string]string{
				"resource-request-override.hypershift.openshift.io/router.router": "cpu=500m,memory=1Gi",
			},
			containers: []corev1.Container{
				{
					Name: "router",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("256Mi"),
						},
					},
				},
			},
			expectedContainers: []corev1.Container{
				{
					Name: "router",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				},
			},
		},
		{
			name: "When overriding aro.openshift.io/swift-nic it should set both requests and limits",
			annotations: map[string]string{
				"resource-request-override.hypershift.openshift.io/router.router": "aro.openshift.io/swift-nic=1",
			},
			containers: []corev1.Container{
				{
					Name: "router",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{},
					},
				},
			},
			expectedContainers: []corev1.Container{
				{
					Name: "router",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							aroSwiftNICResource: resource.MustParse("1"),
						},
						Limits: corev1.ResourceList{
							aroSwiftNICResource: resource.MustParse("1"),
						},
					},
				},
			},
		},
		{
			name: "When overriding mixed resources it should set limits only for swift-nic",
			annotations: map[string]string{
				"resource-request-override.hypershift.openshift.io/router.router": "cpu=500m,aro.openshift.io/swift-nic=1",
			},
			containers: []corev1.Container{
				{
					Name: "router",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("100m"),
						},
					},
				},
			},
			expectedContainers: []corev1.Container{
				{
					Name: "router",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("500m"),
							aroSwiftNICResource: resource.MustParse("1"),
						},
						Limits: corev1.ResourceList{
							aroSwiftNICResource: resource.MustParse("1"),
						},
					},
				},
			},
		},
		{
			name: "When overriding an init container with swift-nic it should set both requests and limits",
			annotations: map[string]string{
				"resource-request-override.hypershift.openshift.io/router.init-router": "aro.openshift.io/swift-nic=2",
			},
			initContainers: []corev1.Container{
				{
					Name: "init-router",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{},
					},
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Name: "init-router",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							aroSwiftNICResource: resource.MustParse("2"),
						},
						Limits: corev1.ResourceList{
							aroSwiftNICResource: resource.MustParse("2"),
						},
					},
				},
			},
		},
		{
			name: "When overriding a container with nil resource requests it should initialize the map and apply overrides",
			annotations: map[string]string{
				"resource-request-override.hypershift.openshift.io/router.router": "cpu=500m,memory=1Gi",
			},
			containers: []corev1.Container{
				{
					Name: "router",
				},
			},
			expectedContainers: []corev1.Container{
				{
					Name: "router",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				},
			},
		},
		{
			name: "When overriding an init container with nil resource requests it should initialize the map and apply overrides",
			annotations: map[string]string{
				"resource-request-override.hypershift.openshift.io/router.wait-for-etcd": "aro.openshift.io/swift-nic=1",
			},
			initContainers: []corev1.Container{
				{
					Name: "wait-for-etcd",
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Name: "wait-for-etcd",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							aroSwiftNICResource: resource.MustParse("1"),
						},
						Limits: corev1.ResourceList{
							aroSwiftNICResource: resource.MustParse("1"),
						},
					},
				},
			},
		},
		{
			name: "When overriding mixed containers and init-containers with nil and non-nil resources it should handle both",
			annotations: map[string]string{
				"resource-request-override.hypershift.openshift.io/router.router":       "cpu=500m,memory=1Gi",
				"resource-request-override.hypershift.openshift.io/router.sidecar":      "cpu=100m",
				"resource-request-override.hypershift.openshift.io/router.init-router":  "cpu=200m",
				"resource-request-override.hypershift.openshift.io/router.wait-for-dns": "aro.openshift.io/swift-nic=1",
			},
			containers: []corev1.Container{
				{
					Name: "router",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("100m"),
						},
					},
				},
				{
					Name: "sidecar",
				},
			},
			initContainers: []corev1.Container{
				{
					Name: "init-router",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("50m"),
						},
					},
				},
				{
					Name: "wait-for-dns",
				},
			},
			expectedContainers: []corev1.Container{
				{
					Name: "router",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				},
				{
					Name: "sidecar",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("100m"),
						},
					},
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Name: "init-router",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("200m"),
						},
					},
				},
				{
					Name: "wait-for-dns",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							aroSwiftNICResource: resource.MustParse("1"),
						},
						Limits: corev1.ResourceList{
							aroSwiftNICResource: resource.MustParse("1"),
						},
					},
				},
			},
		},
		{
			name: "When annotation targets a different deployment it should not apply overrides",
			annotations: map[string]string{
				"resource-request-override.hypershift.openshift.io/kube-apiserver.kube-apiserver": "cpu=500m",
			},
			containers: []corev1.Container{
				{
					Name: "router",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("100m"),
						},
					},
				},
			},
			expectedContainers: []corev1.Container{
				{
					Name: "router",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("100m"),
						},
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			workload := &controlPlaneWorkload[*appsv1.Deployment]{
				name:             "router",
				workloadProvider: &deploymentProvider{},
				ComponentOptions: &testComponent{},
			}
			hcp := &hyperv1.HostedControlPlane{}
			hcp.Annotations = test.annotations

			podTemplate := &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers:     test.containers,
					InitContainers: test.initContainers,
				},
			}

			workload.applyRequestsOverrides(podTemplate, hcp)

			if test.expectedContainers != nil {
				g.Expect(podTemplate.Spec.Containers).To(Equal(test.expectedContainers))
			}
			if test.expectedInitContainers != nil {
				g.Expect(podTemplate.Spec.InitContainers).To(Equal(test.expectedInitContainers))
			}
		})
	}
}

func TestApplyNonOvercommitableResourceLimits(t *testing.T) {
	tests := []struct {
		name           string
		overrides      corev1.ResourceList
		existingLimits corev1.ResourceList
		expectedLimits corev1.ResourceList
	}{
		{
			name: "When overriding aro.openshift.io/swift-nic it should set the limit to the same value",
			overrides: corev1.ResourceList{
				aroSwiftNICResource: resource.MustParse("1"),
			},
			expectedLimits: corev1.ResourceList{
				aroSwiftNICResource: resource.MustParse("1"),
			},
		},
		{
			name: "When overriding standard resources it should not set limits",
			overrides: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
			expectedLimits: nil,
		},
		{
			name: "When overriding a mix of standard and swift-nic resources it should only set limits for swift-nic",
			overrides: corev1.ResourceList{
				corev1.ResourceCPU:  resource.MustParse("500m"),
				aroSwiftNICResource: resource.MustParse("2"),
			},
			existingLimits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
			expectedLimits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("2Gi"),
				aroSwiftNICResource:   resource.MustParse("2"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			container := &corev1.Container{
				Resources: corev1.ResourceRequirements{
					Limits: test.existingLimits,
				},
			}
			applyNonOvercommitableResourceLimits(container, test.overrides)
			g.Expect(container.Resources.Limits).To(Equal(test.expectedLimits))
		})
	}
}

func TestSetDefaultOptions(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := hyperv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add hyperv1 to scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add corev1 to scheme: %v", err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add appsv1 to scheme: %v", err)
	}

	t.Run("When SetDefaultSecurityContext is true it should set RunAsUser and FSGroup", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)

		workload := &controlPlaneWorkload[*appsv1.StatefulSet]{
			name:             "etcd",
			workloadProvider: &statefulSetProvider{},
			ComponentOptions: &testComponent{},
		}
		workloadObject := &appsv1.StatefulSet{}

		err := workload.setDefaultOptions(ControlPlaneContext{
			HCP:                       &hyperv1.HostedControlPlane{},
			SetDefaultSecurityContext: true,
			DefaultSecurityContextUID: int64(1002),
			Client:                    fake.NewClientBuilder().WithScheme(scheme).Build(),
		}, workloadObject, nil, nil)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(workloadObject.Spec.Template.Spec.SecurityContext.RunAsUser).To(Equal(ptr.To(int64(1002))))
		g.Expect(workloadObject.Spec.Template.Spec.SecurityContext.FSGroup).To(Equal(ptr.To(int64(1002))))
	})

	releaseProvider := imageprovider.NewFromImages(map[string]string{
		"hyperkube": "quay.io/test/hyperkube:latest",
	})

	resourceTests := []struct {
		name               string
		annotations        map[string]string
		containerResources corev1.ResourceRequirements
		existingResources  map[string]corev1.ResourceRequirements
		expectedResources  corev1.ResourceRequirements
	}{
		{
			name: "When existing resources have both requests and limits it should fully preserve them for non-capi-provider components",
			containerResources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				},
			},
			existingResources: map[string]corev1.ResourceRequirements{
				"kube-apiserver": {
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("200m"),
						corev1.ResourceMemory: resource.MustParse("1700Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					},
				},
			},
			expectedResources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("200m"),
					corev1.ResourceMemory: resource.MustParse("1700Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
			},
		},
		{
			name: "When no existing resources are set it should keep the manifest defaults",
			containerResources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				},
			},
			existingResources: nil,
			expectedResources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				},
			},
		},
	}

	for _, test := range resourceTests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)

			workload := &controlPlaneWorkload[*appsv1.Deployment]{
				name:             "kube-apiserver",
				workloadProvider: &deploymentProvider{},
				ComponentOptions: &testComponent{},
			}

			deployment := &appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:      "kube-apiserver",
									Image:     "hyperkube",
									Resources: test.containerResources,
								},
							},
						},
					},
				},
			}

			hcp := &hyperv1.HostedControlPlane{}
			hcp.Annotations = test.annotations

			err := workload.setDefaultOptions(ControlPlaneContext{
				HCP:                  hcp,
				Client:               fake.NewClientBuilder().WithScheme(scheme).Build(),
				ReleaseImageProvider: releaseProvider,
			}, deployment, test.existingResources, nil)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(deployment.Spec.Template.Spec.Containers[0].Resources).To(Equal(test.expectedResources))
		})
	}

	annotationTests := []struct {
		name           string
		hcpAnnotations map[string]string
		expectSet      bool
	}{
		{
			name: "When HCP has RestartDateAnnotation it should propagate it to the pod template",
			hcpAnnotations: map[string]string{
				hyperv1.RestartDateAnnotation: "2024-01-15T12:00:00Z",
			},
			expectSet: true,
		},
		{
			name:      "When HCP has no RestartDateAnnotation it should not set it on the pod template",
			expectSet: false,
		},
	}

	for _, test := range annotationTests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)

			workload := &controlPlaneWorkload[*appsv1.Deployment]{
				name:             "kube-apiserver",
				workloadProvider: &deploymentProvider{},
				ComponentOptions: &testComponent{},
			}
			deployment := &appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{Name: "kube-apiserver", Image: "hyperkube"},
							},
						},
					},
				},
			}
			hcp := &hyperv1.HostedControlPlane{}
			hcp.Annotations = test.hcpAnnotations

			err := workload.setDefaultOptions(ControlPlaneContext{
				HCP:                  hcp,
				Client:               fake.NewClientBuilder().WithScheme(scheme).Build(),
				ReleaseImageProvider: releaseProvider,
			}, deployment, nil, nil)
			g.Expect(err).NotTo(HaveOccurred())

			if test.expectSet {
				g.Expect(deployment.Spec.Template.Annotations).To(HaveKeyWithValue(
					hyperv1.RestartDateAnnotation, test.hcpAnnotations[hyperv1.RestartDateAnnotation]))
			} else {
				g.Expect(deployment.Spec.Template.Annotations).NotTo(HaveKey(hyperv1.RestartDateAnnotation))
			}
		})
	}
}

func TestLimitsAlreadyRemoved(t *testing.T) {
	tests := []struct {
		name          string
		annotations   map[string]string
		containerName string
		expected      bool
	}{
		{
			name:          "When annotations are nil it should return false",
			annotations:   nil,
			containerName: "kube-apiserver",
			expected:      false,
		},
		{
			name:          "When annotation is empty it should return false",
			annotations:   map[string]string{limitsRemovedAnnotation: ""},
			containerName: "kube-apiserver",
			expected:      false,
		},
		{
			name:          "When container is listed it should return true",
			annotations:   map[string]string{limitsRemovedAnnotation: "kube-apiserver"},
			containerName: "kube-apiserver",
			expected:      true,
		},
		{
			name:          "When container is listed among others it should return true",
			annotations:   map[string]string{limitsRemovedAnnotation: "sidecar,kube-apiserver,another"},
			containerName: "kube-apiserver",
			expected:      true,
		},
		{
			name:          "When container is not listed it should return false",
			annotations:   map[string]string{limitsRemovedAnnotation: "sidecar,another"},
			containerName: "kube-apiserver",
			expected:      false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := limitsAlreadyRemoved(test.annotations, test.containerName)
			g.Expect(result).To(Equal(test.expected))
		})
	}
}

func TestMarkLimitsRemoved(t *testing.T) {
	tests := []struct {
		name               string
		existingAnnotation string
		containerName      string
		expectedAnnotation string
	}{
		{
			name:               "When annotations are empty it should add the container",
			existingAnnotation: "",
			containerName:      "kube-apiserver",
			expectedAnnotation: "kube-apiserver",
		},
		{
			name:               "When a container already exists it should append the new container",
			existingAnnotation: "sidecar",
			containerName:      "kube-apiserver",
			expectedAnnotation: "sidecar,kube-apiserver",
		},
		{
			name:               "When container is already listed it should not duplicate",
			existingAnnotation: "kube-apiserver",
			containerName:      "kube-apiserver",
			expectedAnnotation: "kube-apiserver",
		},
		{
			name:               "When container is already in a multi-item list it should not duplicate",
			existingAnnotation: "sidecar,kube-apiserver",
			containerName:      "kube-apiserver",
			expectedAnnotation: "sidecar,kube-apiserver",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			deployment := &appsv1.Deployment{}
			if test.existingAnnotation != "" {
				deployment.SetAnnotations(map[string]string{
					limitsRemovedAnnotation: test.existingAnnotation,
				})
			}
			markLimitsRemoved(deployment, test.containerName)
			g.Expect(deployment.GetAnnotations()[limitsRemovedAnnotation]).To(Equal(test.expectedAnnotation))
		})
	}
}

func TestComputeContainerResourcesHash(t *testing.T) {
	g := NewGomegaWithT(t)

	containers := []corev1.Container{
		{
			Name: "kube-apiserver",
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				},
			},
		},
	}

	// Same input should produce the same hash.
	hash1 := computeContainerResourcesHash(containers)
	hash2 := computeContainerResourcesHash(containers)
	g.Expect(hash1).To(Equal(hash2))
	g.Expect(hash1).NotTo(BeEmpty())

	// Different input should produce a different hash.
	containersWithLimits := []corev1.Container{
		{
			Name: "kube-apiserver",
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
			},
		},
	}
	hash3 := computeContainerResourcesHash(containersWithLimits)
	g.Expect(hash3).NotTo(Equal(hash1))

	// Order independence: containers sorted by name.
	containersA := []corev1.Container{
		{Name: "a", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")}}},
		{Name: "b", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")}}},
	}
	containersB := []corev1.Container{
		{Name: "b", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")}}},
		{Name: "a", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")}}},
	}
	g.Expect(computeContainerResourcesHash(containersA)).To(Equal(computeContainerResourcesHash(containersB)))
}

func TestLimitRemovalAndPreservation(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := hyperv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add hyperv1 to scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add corev1 to scheme: %v", err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add appsv1 to scheme: %v", err)
	}

	releaseProvider := imageprovider.NewFromImages(map[string]string{
		"hyperkube":     "quay.io/test/hyperkube:latest",
		"capi-provider": "quay.io/test/capi-provider:latest",
	})

	t.Run("When capi-provider manifest has no limits and existing has limits with no annotation it should remove limits and set annotation", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)

		workload := &controlPlaneWorkload[*appsv1.Deployment]{
			name:             "capi-provider",
			workloadProvider: &deploymentProvider{},
			ComponentOptions: &testComponent{},
		}
		deployment := &appsv1.Deployment{
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "capi-provider",
								Image: "capi-provider",
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("100m"),
										corev1.ResourceMemory: resource.MustParse("256Mi"),
									},
									// No limits in manifest.
								},
							},
						},
					},
				},
			},
		}

		existingResources := map[string]corev1.ResourceRequirements{
			"capi-provider": {
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("200m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
			},
		}

		err := workload.setDefaultOptions(ControlPlaneContext{
			HCP:                  &hyperv1.HostedControlPlane{},
			Client:               fake.NewClientBuilder().WithScheme(scheme).Build(),
			ReleaseImageProvider: releaseProvider,
		}, deployment, existingResources, nil)
		g.Expect(err).NotTo(HaveOccurred())

		// Limits should be removed (manifest resources used).
		container := deployment.Spec.Template.Spec.Containers[0]
		g.Expect(container.Resources.Limits).To(BeNil())
		g.Expect(container.Resources.Requests).To(Equal(corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		}))

		// Annotation should be set on the deployment.
		g.Expect(deployment.GetAnnotations()[limitsRemovedAnnotation]).To(Equal("capi-provider"))
	})

	t.Run("When capi-provider manifest has no limits and existing has limits with annotation set it should preserve user override", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)

		workload := &controlPlaneWorkload[*appsv1.Deployment]{
			name:             "capi-provider",
			workloadProvider: &deploymentProvider{},
			ComponentOptions: &testComponent{},
		}
		deployment := &appsv1.Deployment{
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "capi-provider",
								Image: "capi-provider",
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("100m"),
										corev1.ResourceMemory: resource.MustParse("256Mi"),
									},
									// No limits in manifest.
								},
							},
						},
					},
				},
			},
		}

		existingResources := map[string]corev1.ResourceRequirements{
			"capi-provider": {
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
			},
		}

		// Simulate that limits were already removed before (user re-added them).
		oldAnnotations := map[string]string{
			limitsRemovedAnnotation: "capi-provider",
		}

		err := workload.setDefaultOptions(ControlPlaneContext{
			HCP:                  &hyperv1.HostedControlPlane{},
			Client:               fake.NewClientBuilder().WithScheme(scheme).Build(),
			ReleaseImageProvider: releaseProvider,
		}, deployment, existingResources, oldAnnotations)
		g.Expect(err).NotTo(HaveOccurred())

		// User's override should be preserved.
		container := deployment.Spec.Template.Spec.Containers[0]
		g.Expect(container.Resources).To(Equal(existingResources["capi-provider"]))
	})

	t.Run("When non-capi-provider manifest has no limits and existing has limits it should always preserve existing resources", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)

		workload := &controlPlaneWorkload[*appsv1.Deployment]{
			name:             "kube-apiserver",
			workloadProvider: &deploymentProvider{},
			ComponentOptions: &testComponent{},
		}
		deployment := &appsv1.Deployment{
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "kube-apiserver",
								Image: "hyperkube",
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("100m"),
										corev1.ResourceMemory: resource.MustParse("256Mi"),
									},
									// No limits in manifest.
								},
							},
						},
					},
				},
			},
		}

		existingResources := map[string]corev1.ResourceRequirements{
			"kube-apiserver": {
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("200m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
			},
		}

		err := workload.setDefaultOptions(ControlPlaneContext{
			HCP:                  &hyperv1.HostedControlPlane{},
			Client:               fake.NewClientBuilder().WithScheme(scheme).Build(),
			ReleaseImageProvider: releaseProvider,
		}, deployment, existingResources, nil)
		g.Expect(err).NotTo(HaveOccurred())

		// Non-capi-provider components always preserve existing resources.
		container := deployment.Spec.Template.Spec.Containers[0]
		g.Expect(container.Resources).To(Equal(existingResources["kube-apiserver"]))
	})

	t.Run("When capi-provider manifest has no limits and existing has no limits it should preserve existing resources", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)

		workload := &controlPlaneWorkload[*appsv1.Deployment]{
			name:             "capi-provider",
			workloadProvider: &deploymentProvider{},
			ComponentOptions: &testComponent{},
		}
		deployment := &appsv1.Deployment{
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "capi-provider",
								Image: "capi-provider",
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("100m"),
										corev1.ResourceMemory: resource.MustParse("256Mi"),
									},
								},
							},
						},
					},
				},
			},
		}

		existingResources := map[string]corev1.ResourceRequirements{
			"capi-provider": {
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("200m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
				// No limits on existing either.
			},
		}

		err := workload.setDefaultOptions(ControlPlaneContext{
			HCP:                  &hyperv1.HostedControlPlane{},
			Client:               fake.NewClientBuilder().WithScheme(scheme).Build(),
			ReleaseImageProvider: releaseProvider,
		}, deployment, existingResources, nil)
		g.Expect(err).NotTo(HaveOccurred())

		// Neither has limits, so existing resources are preserved.
		container := deployment.Spec.Template.Spec.Containers[0]
		g.Expect(container.Resources).To(Equal(existingResources["capi-provider"]))
	})

	t.Run("When capi-provider manifest has limits and existing has limits it should preserve existing resources", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)

		workload := &controlPlaneWorkload[*appsv1.Deployment]{
			name:             "capi-provider",
			workloadProvider: &deploymentProvider{},
			ComponentOptions: &testComponent{},
		}
		deployment := &appsv1.Deployment{
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "capi-provider",
								Image: "capi-provider",
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("100m"),
										corev1.ResourceMemory: resource.MustParse("256Mi"),
									},
									Limits: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("1"),
										corev1.ResourceMemory: resource.MustParse("2Gi"),
									},
								},
							},
						},
					},
				},
			},
		}

		existingResources := map[string]corev1.ResourceRequirements{
			"capi-provider": {
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
			},
		}

		err := workload.setDefaultOptions(ControlPlaneContext{
			HCP:                  &hyperv1.HostedControlPlane{},
			Client:               fake.NewClientBuilder().WithScheme(scheme).Build(),
			ReleaseImageProvider: releaseProvider,
		}, deployment, existingResources, nil)
		g.Expect(err).NotTo(HaveOccurred())

		// Both have limits — existing resources should be preserved (current contract).
		container := deployment.Spec.Template.Spec.Containers[0]
		g.Expect(container.Resources).To(Equal(existingResources["capi-provider"]))
	})

	t.Run("When there is no existing deployment it should use manifest resources as-is", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)

		workload := &controlPlaneWorkload[*appsv1.Deployment]{
			name:             "capi-provider",
			workloadProvider: &deploymentProvider{},
			ComponentOptions: &testComponent{},
		}
		manifestResources := corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
		}
		deployment := &appsv1.Deployment{
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:      "capi-provider",
								Image:     "capi-provider",
								Resources: manifestResources,
							},
						},
					},
				},
			},
		}

		err := workload.setDefaultOptions(ControlPlaneContext{
			HCP:                  &hyperv1.HostedControlPlane{},
			Client:               fake.NewClientBuilder().WithScheme(scheme).Build(),
			ReleaseImageProvider: releaseProvider,
		}, deployment, nil, nil)
		g.Expect(err).NotTo(HaveOccurred())

		container := deployment.Spec.Template.Spec.Containers[0]
		g.Expect(container.Resources).To(Equal(manifestResources))
	})

	t.Run("When reconciling it should set container resources hash annotation on pod template", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)

		workload := &controlPlaneWorkload[*appsv1.Deployment]{
			name:             "capi-provider",
			workloadProvider: &deploymentProvider{},
			ComponentOptions: &testComponent{},
		}
		deployment := &appsv1.Deployment{
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "capi-provider",
								Image: "capi-provider",
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("100m"),
									},
								},
							},
						},
					},
				},
			},
		}

		err := workload.setDefaultOptions(ControlPlaneContext{
			HCP:                  &hyperv1.HostedControlPlane{},
			Client:               fake.NewClientBuilder().WithScheme(scheme).Build(),
			ReleaseImageProvider: releaseProvider,
		}, deployment, nil, nil)
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(deployment.Spec.Template.Annotations).To(HaveKey(containerResourcesHashAnnotation))
		g.Expect(deployment.Spec.Template.Annotations[containerResourcesHashAnnotation]).NotTo(BeEmpty())
	})
}
