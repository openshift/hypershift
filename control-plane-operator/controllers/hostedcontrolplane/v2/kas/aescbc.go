package kas

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"hash/fnv"

	"github.com/openshift/hypershift/support/api"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
)

const aescbcKeyNamePrefix = "key"

func generateAESCBCEncryptionConfig(activeKey []byte, backupKey []byte) ([]byte, error) {
	var providerConfiguration []v1.ProviderConfiguration
	var keyList []v1.Key
	if len(activeKey) == 0 {
		return nil, fmt.Errorf("active key is empty")
	}
	hasher := fnv.New32()
	_, err := hasher.Write(activeKey)
	if err != nil {
		return nil, err
	}
	keyList = append(keyList, v1.Key{
		Name:   fmt.Sprintf("%s-%d", aescbcKeyNamePrefix, hasher.Sum32()),
		Secret: base64.StdEncoding.EncodeToString(activeKey),
	})
	if len(backupKey) > 0 {
		hasher = fnv.New32()
		_, err := hasher.Write(backupKey)
		if err != nil {
			return nil, err
		}
		keyList = append(keyList, v1.Key{
			Name:   fmt.Sprintf("%s-%d", aescbcKeyNamePrefix, hasher.Sum32()),
			Secret: base64.StdEncoding.EncodeToString(backupKey),
		})
	}
	providerConfiguration = append(providerConfiguration, v1.ProviderConfiguration{
		AESCBC: &v1.AESConfiguration{
			Keys: keyList,
		},
	})
	providerConfiguration = append(providerConfiguration, v1.ProviderConfiguration{
		Identity: &v1.IdentityConfiguration{},
	})
	encryptionConfig := v1.EncryptionConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1.SchemeGroupVersion.String(),
			Kind:       encryptionConfigurationKind,
		},
		Resources: []v1.ResourceConfiguration{
			{
				Resources: []string{"secrets"},
				Providers: providerConfiguration,
			},
		},
	}
	bufferInstance := bytes.NewBuffer([]byte{})
	err = api.YamlSerializer.Encode(&encryptionConfig, bufferInstance)
	if err != nil {
		return nil, err
	}
	return bufferInstance.Bytes(), nil
}
