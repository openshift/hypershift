package secretencryption

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDecodeEncryptionConfiguration(t *testing.T) {
	t.Parallel()

	t.Run("When given valid YAML it should parse correctly", func(t *testing.T) {
		g := NewWithT(t)
		yaml := `apiVersion: apiserver.config.k8s.io/v1
kind: EncryptionConfiguration
resources:
  - resources:
      - secrets
    providers:
      - kms:
          name: my-kms
          apiVersion: v2
          endpoint: unix:///tmp/kms.sock
      - identity: {}
`
		cfg, err := DecodeEncryptionConfiguration([]byte(yaml))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cfg.Resources).To(HaveLen(1))
		g.Expect(cfg.Resources[0].Providers).To(HaveLen(2))
		g.Expect(cfg.Resources[0].Providers[0].KMS.Name).To(Equal("my-kms"))
	})

	t.Run("When given empty bytes it should return an empty config", func(t *testing.T) {
		g := NewWithT(t)
		cfg, err := DecodeEncryptionConfiguration([]byte{})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cfg.Resources).To(BeEmpty())
	})

	t.Run("When given malformed YAML it should return an error", func(t *testing.T) {
		g := NewWithT(t)
		_, err := DecodeEncryptionConfiguration([]byte("not: valid: yaml: {{"))
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("When given YAML with wrong kind it should return an error", func(t *testing.T) {
		g := NewWithT(t)
		yaml := `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
`
		_, err := DecodeEncryptionConfiguration([]byte(yaml))
		g.Expect(err).To(HaveOccurred())
	})
}

func kmsConfig(providers ...apiserverv1.ProviderConfiguration) *apiserverv1.EncryptionConfiguration {
	return &apiserverv1.EncryptionConfiguration{
		Resources: []apiserverv1.ResourceConfiguration{
			{Providers: providers},
		},
	}
}

func kmsProvider(name string) apiserverv1.ProviderConfiguration {
	return apiserverv1.ProviderConfiguration{
		KMS: &apiserverv1.KMSConfiguration{Name: name, APIVersion: "v2"},
	}
}

func identityProvider() apiserverv1.ProviderConfiguration {
	return apiserverv1.ProviderConfiguration{
		Identity: &apiserverv1.IdentityConfiguration{},
	}
}

func aescbcProvider(keys ...apiserverv1.Key) apiserverv1.ProviderConfiguration {
	return apiserverv1.ProviderConfiguration{
		AESCBC: &apiserverv1.AESConfiguration{Keys: keys},
	}
}

func aescbcKey(name string) apiserverv1.Key {
	return apiserverv1.Key{Name: name, Secret: "dW51c2Vk"}
}

func TestEncryptionConfigHash(t *testing.T) {
	t.Parallel()

	t.Run("When config bytes are empty it should return an empty hash", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(EncryptionConfigHash(nil)).To(BeEmpty())
	})

	t.Run("When config bytes are provided it should return a stable sha256 hash", func(t *testing.T) {
		g := NewWithT(t)
		data := []byte("apiVersion: apiserver.config.k8s.io/v1\nkind: EncryptionConfiguration\n")
		hash := EncryptionConfigHash(data)
		g.Expect(hash).To(HavePrefix("sha256:"))
		g.Expect(EncryptionConfigHash(data)).To(Equal(hash))
	})
}

func TestKASDeploymentConvergedWithEncryptionConfig(t *testing.T) {
	t.Parallel()

	configBytes := []byte("config")
	configHash := EncryptionConfigHash(configBytes)
	replicas := int32(1)

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	kasLabels := map[string]string{"app": "kube-apiserver"}

	readyDeployment := func() *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kube-apiserver",
				Namespace: "clusters-test",
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: kasLabels},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							EncryptionConfigHashAnnotation: configHash,
						},
					},
				},
			},
			Status: appsv1.DeploymentStatus{
				Replicas:          1,
				UpdatedReplicas:   1,
				ReadyReplicas:     1,
				AvailableReplicas: 1,
			},
		}
	}

	t.Run("When deployment is ready, hash matches, and no terminating pods it should report converged", func(t *testing.T) {
		g := NewWithT(t)
		deployment := readyDeployment()
		activePod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kube-apiserver-abc123",
				Namespace: "clusters-test",
				Labels:    kasLabels,
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(activePod).Build()
		g.Expect(KASDeploymentConvergedWithEncryptionConfig(t.Context(), c, deployment, configHash)).To(BeTrue())
	})

	t.Run("When deployment is ready but hash mismatches it should not report converged", func(t *testing.T) {
		g := NewWithT(t)
		deployment := readyDeployment()
		deployment.Spec.Template.Annotations = nil
		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		g.Expect(KASDeploymentConvergedWithEncryptionConfig(t.Context(), c, deployment, configHash)).To(BeFalse())
	})

	t.Run("When deployment is ready and hash matches but a terminating pod exists it should not report converged", func(t *testing.T) {
		g := NewWithT(t)
		deployment := readyDeployment()
		now := metav1.NewTime(time.Now())
		terminatingPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "kube-apiserver-old",
				Namespace:         "clusters-test",
				Labels:            kasLabels,
				DeletionTimestamp: &now,
				Finalizers:        []string{"test-finalizer"},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		}
		activePod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kube-apiserver-new",
				Namespace: "clusters-test",
				Labels:    kasLabels,
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(terminatingPod, activePod).Build()
		g.Expect(KASDeploymentConvergedWithEncryptionConfig(t.Context(), c, deployment, configHash)).To(BeFalse())
	})

	t.Run("When expected config hash is empty it should not report converged", func(t *testing.T) {
		g := NewWithT(t)
		deployment := readyDeployment()
		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		g.Expect(KASDeploymentConvergedWithEncryptionConfig(t.Context(), c, deployment, "")).To(BeFalse())
	})
}

func TestFindKeyRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cfg        *apiserverv1.EncryptionConfiguration
		targetName string
		encType    hyperv1.SecretEncryptionType
		expected   TargetKeyRole
	}{
		{
			name:       "When config is nil it should return TargetKeyAbsent",
			cfg:        nil,
			targetName: "target",
			encType:    hyperv1.KMS,
			expected:   TargetKeyAbsent,
		},
		{
			name:       "When config has no resources it should return TargetKeyAbsent",
			cfg:        &apiserverv1.EncryptionConfiguration{},
			targetName: "target",
			encType:    hyperv1.KMS,
			expected:   TargetKeyAbsent,
		},
		{
			name:       "When KMS target key is the first provider it should return TargetKeyWrite",
			cfg:        kmsConfig(kmsProvider("target-key"), kmsProvider("old-key"), identityProvider()),
			targetName: "target-key",
			encType:    hyperv1.KMS,
			expected:   TargetKeyWrite,
		},
		{
			name:       "When KMS target key is the second provider it should return TargetKeyReadOnly",
			cfg:        kmsConfig(kmsProvider("old-key"), kmsProvider("target-key"), identityProvider()),
			targetName: "target-key",
			encType:    hyperv1.KMS,
			expected:   TargetKeyReadOnly,
		},
		{
			name:       "When KMS target key is not in config it should return TargetKeyAbsent",
			cfg:        kmsConfig(kmsProvider("old-key"), identityProvider()),
			targetName: "target-key",
			encType:    hyperv1.KMS,
			expected:   TargetKeyAbsent,
		},
		{
			name:       "When KMS target key is the only provider it should return TargetKeyWrite",
			cfg:        kmsConfig(kmsProvider("target-key"), identityProvider()),
			targetName: "target-key",
			encType:    hyperv1.KMS,
			expected:   TargetKeyWrite,
		},
		{
			name:       "When KMS has identity before KMS providers it should still find write correctly",
			cfg:        kmsConfig(identityProvider(), kmsProvider("target-key"), kmsProvider("old-key")),
			targetName: "target-key",
			encType:    hyperv1.KMS,
			expected:   TargetKeyWrite,
		},
		{
			name:       "When AESCBC target key is the first key it should return TargetKeyWrite",
			cfg:        kmsConfig(aescbcProvider(aescbcKey("target-key"), aescbcKey("old-key")), identityProvider()),
			targetName: "target-key",
			encType:    hyperv1.AESCBC,
			expected:   TargetKeyWrite,
		},
		{
			name:       "When AESCBC target key is the second key it should return TargetKeyReadOnly",
			cfg:        kmsConfig(aescbcProvider(aescbcKey("old-key"), aescbcKey("target-key")), identityProvider()),
			targetName: "target-key",
			encType:    hyperv1.AESCBC,
			expected:   TargetKeyReadOnly,
		},
		{
			name:       "When AESCBC target key is not present it should return TargetKeyAbsent",
			cfg:        kmsConfig(aescbcProvider(aescbcKey("old-key")), identityProvider()),
			targetName: "target-key",
			encType:    hyperv1.AESCBC,
			expected:   TargetKeyAbsent,
		},
		{
			name:       "When AESCBC target key is the only key it should return TargetKeyWrite",
			cfg:        kmsConfig(aescbcProvider(aescbcKey("target-key")), identityProvider()),
			targetName: "target-key",
			encType:    hyperv1.AESCBC,
			expected:   TargetKeyWrite,
		},
		{
			name:       "When encryption type is unrecognized it should return TargetKeyAbsent",
			cfg:        kmsConfig(kmsProvider("target-key")),
			targetName: "target-key",
			encType:    hyperv1.SecretEncryptionType("unknown"),
			expected:   TargetKeyAbsent,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(FindKeyRole(tc.cfg, tc.targetName, tc.encType)).To(Equal(tc.expected))
		})
	}
}

func TestShouldPromoteTargetKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		cfg          *apiserverv1.EncryptionConfiguration
		targetName   string
		encType      hyperv1.SecretEncryptionType
		kasConverged bool
		expected     bool
	}{
		{
			name:         "When target key is absent it should not promote",
			cfg:          kmsConfig(kmsProvider("old-key"), identityProvider()),
			targetName:   "target-key",
			encType:      hyperv1.KMS,
			kasConverged: true,
			expected:     false,
		},
		{
			name:         "When target key is already write it should promote regardless of convergence",
			cfg:          kmsConfig(kmsProvider("target-key"), kmsProvider("old-key"), identityProvider()),
			targetName:   "target-key",
			encType:      hyperv1.KMS,
			kasConverged: false,
			expected:     true,
		},
		{
			name:         "When target key is read-only and KAS is converged it should promote",
			cfg:          kmsConfig(kmsProvider("old-key"), kmsProvider("target-key"), identityProvider()),
			targetName:   "target-key",
			encType:      hyperv1.KMS,
			kasConverged: true,
			expected:     true,
		},
		{
			name:         "When target key is read-only and KAS is not converged it should not promote",
			cfg:          kmsConfig(kmsProvider("old-key"), kmsProvider("target-key"), identityProvider()),
			targetName:   "target-key",
			encType:      hyperv1.KMS,
			kasConverged: false,
			expected:     false,
		},
		{
			name:         "When config is nil it should not promote",
			cfg:          nil,
			targetName:   "target-key",
			encType:      hyperv1.KMS,
			kasConverged: true,
			expected:     false,
		},
		{
			name:         "When AESCBC target key is write it should promote",
			cfg:          kmsConfig(aescbcProvider(aescbcKey("target-key"), aescbcKey("old-key")), identityProvider()),
			targetName:   "target-key",
			encType:      hyperv1.AESCBC,
			kasConverged: false,
			expected:     true,
		},
		{
			name:         "When AESCBC target key is read-only and KAS converged it should promote",
			cfg:          kmsConfig(aescbcProvider(aescbcKey("old-key"), aescbcKey("target-key")), identityProvider()),
			targetName:   "target-key",
			encType:      hyperv1.AESCBC,
			kasConverged: true,
			expected:     true,
		},
		{
			name:         "When AESCBC target key is read-only and KAS not converged it should not promote",
			cfg:          kmsConfig(aescbcProvider(aescbcKey("old-key"), aescbcKey("target-key")), identityProvider()),
			targetName:   "target-key",
			encType:      hyperv1.AESCBC,
			kasConverged: false,
			expected:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(ShouldPromoteTargetKey(tc.cfg, tc.targetName, tc.encType, tc.kasConverged)).To(Equal(tc.expected))
		})
	}
}
