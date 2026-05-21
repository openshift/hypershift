package secretencryption

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
)

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
