package controlplanecomponent

import (
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

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

func projectedCMVolume(volumeName string, cmNames ...string) corev1.Volume {
	v := corev1.Volume{
		Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{},
		},
	}
	for _, name := range cmNames {
		v.VolumeSource.Projected.Sources = append(v.VolumeSource.Projected.Sources, corev1.VolumeProjection{
			ConfigMap: &corev1.ConfigMapProjection{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: name,
				},
			},
		})
	}
	return v
}

func projectedSecretVolume(volumeName string, secretNames ...string) corev1.Volume {
	v := corev1.Volume{
		Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{},
		},
	}
	for _, name := range secretNames {
		v.VolumeSource.Projected.Sources = append(v.VolumeSource.Projected.Sources, corev1.VolumeProjection{
			Secret: &corev1.SecretProjection{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: name,
				},
			},
		})
	}
	return v
}

func TestPodConfigMapNames(t *testing.T) {
	tests := []struct {
		name    string
		podSpec corev1.PodSpec
		expect  []string
	}{
		{
			name: "volumes",
			podSpec: corev1.PodSpec{
				Volumes: []corev1.Volume{cmVolume("cm1"), secretVolume("s1"), cmVolume("cm2")},
			},
			expect: []string{"cm1", "cm2"},
		},
		{
			name: "projected",
			podSpec: corev1.PodSpec{
				Volumes: []corev1.Volume{projectedCMVolume("pcms", "pcm1", "pcm2"), secretVolume("s1")},
			},
			expect: []string{"pcm1", "pcm2"},
		},
		{
			name: "volumes and projected",
			podSpec: corev1.PodSpec{
				Volumes: []corev1.Volume{cmVolume("cm1"), cmVolume("cm2"), projectedCMVolume("pcms", "pcm3", "pcm4"), secretVolume("s1")},
			},
			expect: []string{"cm1", "cm2", "pcm3", "pcm4"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := podConfigMapNames(&test.podSpec)
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
				Volumes: []corev1.Volume{cmVolume("cm1"), secretVolume("s1"), secretVolume("s2")},
			},
			expect: []string{"s1", "s2"},
		},
		{
			name: "projected",
			podSpec: corev1.PodSpec{
				Volumes: []corev1.Volume{projectedCMVolume("pcms", "pcm1", "pcm2"), projectedSecretVolume("pss", "ps1", "ps2"), cmVolume("cm1")},
			},
			expect: []string{"ps2", "ps1"},
		},
		{
			name: "volumes and projected",
			podSpec: corev1.PodSpec{
				Volumes: []corev1.Volume{secretVolume("s1"), secretVolume("s2"), projectedSecretVolume("pss", "ps3"), projectedSecretVolume("pss2", "ps4"), cmVolume("cm1")},
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
