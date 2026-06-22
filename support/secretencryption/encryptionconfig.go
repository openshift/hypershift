package secretencryption

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
)

const (
	// EncryptionConfigurationKey is the data key used in the Secret that holds the EncryptionConfiguration YAML.
	EncryptionConfigurationKey = "config.yaml"

	// EncryptionConfigurationKind is the Kind used in EncryptionConfiguration manifests.
	EncryptionConfigurationKind = "EncryptionConfiguration"
)

var (
	encScheme   = runtime.NewScheme()
	yamlDecoder runtime.Decoder
)

func init() {
	_ = apiserverv1.AddToScheme(encScheme)
	yamlDecoder = json.NewSerializerWithOptions(json.DefaultMetaFactory, encScheme, encScheme, json.SerializerOptions{Yaml: true})
}

// DecodeEncryptionConfiguration parses raw YAML bytes into an EncryptionConfiguration.
func DecodeEncryptionConfiguration(data []byte) (*apiserverv1.EncryptionConfiguration, error) {
	cfg := &apiserverv1.EncryptionConfiguration{}
	gvks, _, err := encScheme.ObjectKinds(cfg)
	if err != nil || len(gvks) == 0 {
		return nil, fmt.Errorf("cannot determine gvk: %w", err)
	}
	if _, _, err := yamlDecoder.Decode(data, &gvks[0], cfg); err != nil {
		return nil, fmt.Errorf("cannot decode EncryptionConfiguration: %w", err)
	}
	return cfg, nil
}

// TargetKeyRole represents where the target key appears in the EncryptionConfiguration.
type TargetKeyRole int

const (
	TargetKeyAbsent   TargetKeyRole = iota // target key not in config
	TargetKeyReadOnly                      // target key is a read-only provider (not first)
	TargetKeyWrite                         // target key is the write provider (first)
)

// FindKeyRole locates the target key name in the EncryptionConfiguration and
// returns its role. For KMS, each key is a separate provider entry; the first
// KMS provider is the write key. For AESCBC, keys are entries inside a single
// AESCBC provider; the first key is the write key.
func FindKeyRole(cfg *apiserverv1.EncryptionConfiguration, targetName string, encType hyperv1.SecretEncryptionType) TargetKeyRole {
	if cfg == nil || len(cfg.Resources) == 0 {
		return TargetKeyAbsent
	}
	providers := cfg.Resources[0].Providers

	switch encType {
	case hyperv1.KMS:
		kmsIndex := -1
		firstKMSIndex := -1
		for i, p := range providers {
			if p.KMS != nil {
				if firstKMSIndex == -1 {
					firstKMSIndex = i
				}
				if p.KMS.Name == targetName {
					kmsIndex = i
					break
				}
			}
		}
		if kmsIndex == -1 {
			return TargetKeyAbsent
		}
		if kmsIndex == firstKMSIndex {
			return TargetKeyWrite
		}
		return TargetKeyReadOnly

	case hyperv1.AESCBC:
		for _, p := range providers {
			if p.AESCBC != nil {
				for j, key := range p.AESCBC.Keys {
					if key.Name == targetName {
						if j == 0 {
							return TargetKeyWrite
						}
						return TargetKeyReadOnly
					}
				}
			}
		}
		return TargetKeyAbsent
	}

	return TargetKeyAbsent
}

// ShouldPromoteTargetKey determines whether the target key should be promoted
// to write provider based on the current EncryptionConfiguration and KAS
// convergence state.
//
// Returns true when the target key should be the write key (WritePromote/Migrating stage).
// Returns false when the old key should remain the write key (ReadOnlyDeploy stage).
func ShouldPromoteTargetKey(cfg *apiserverv1.EncryptionConfiguration, targetName string, encType hyperv1.SecretEncryptionType, kasConverged bool) bool {
	role := FindKeyRole(cfg, targetName, encType)
	return role == TargetKeyWrite || (role == TargetKeyReadOnly && kasConverged)
}
