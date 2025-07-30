package controlplanecomponent

import (
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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

func TestSetDefaultOptions(t *testing.T) {
	g := NewGomegaWithT(t)
	scheme := runtime.NewScheme()
	_ = hyperv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	// Test case for etcd SecurityContext.
	controlPlaneWorkload := &controlPlaneWorkload[*appsv1.StatefulSet]{
		name:             "etcd",
		workloadProvider: &statefulSetProvider{},
		ComponentOptions: &testComponent{},
	}
	workloadObject := &appsv1.StatefulSet{}

	err := controlPlaneWorkload.setDefaultOptions(ControlPlaneContext{
		HCP:                       &hyperv1.HostedControlPlane{},
		SetDefaultSecurityContext: true,
		Client:                    fake.NewClientBuilder().WithScheme(scheme).Build(),
	}, workloadObject, nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(workloadObject.Spec.Template.Spec.SecurityContext.RunAsUser).To(Equal(ptr.To(int64(DefaultSecurityContextUser))))
	g.Expect(workloadObject.Spec.Template.Spec.SecurityContext.FSGroup).To(Equal(ptr.To(int64(DefaultSecurityContextUser))))
}
