package assets

import (
	"fmt"

	"github.com/openshift/hypershift/support/api"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type AssetReader func(name string) ([]byte, error)

func MustAsset(reader AssetReader, name string) []byte {
	b, err := reader(name)
	if err != nil {
		panic(err)
	}
	return b
}

func MustCRD(reader AssetReader, fileName string) *apiextensionsv1.CustomResourceDefinition {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	deserializeResource(reader, fileName, crd)
	return crd
}

func deserializeResource(reader AssetReader, fileName string, obj runtime.Object) {
	data := MustAsset(reader, fileName)
	gvks, _, err := api.Scheme.ObjectKinds(obj)
	if err != nil || len(gvks) == 0 {
		panic(fmt.Sprintf("cannot determine gvk of resource in %s: %v", fileName, err))
	}
	if _, _, err = api.YamlSerializer.Decode(data, &gvks[0], obj); err != nil {
		panic(fmt.Sprintf("cannot decode resource in %s: %v", fileName, err))
	}
}
