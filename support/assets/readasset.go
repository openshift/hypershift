package assets

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
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

func ShouldHostedCluster(reader AssetReader, fileName string) *hyperv1.HostedCluster {
	hostedCluster := &hyperv1.HostedCluster{}
	tolerantDeserializeResource(reader, fileName, hostedCluster)
	return hostedCluster
}

func ShouldNodePool(reader AssetReader, fileName string) *hyperv1.NodePool {
	nodePool := &hyperv1.NodePool{}
	tolerantDeserializeResource(reader, fileName, nodePool)
	return nodePool
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

func tolerantDeserializeResource(reader AssetReader, fileName string, obj runtime.Object) {
	data := MustAsset(reader, fileName)
	gvks, _, err := api.Scheme.ObjectKinds(obj)
	if err != nil || len(gvks) == 0 {
		panic(fmt.Sprintf("cannot determine gvk of resource in %s: %v", fileName, err))
	}
	if _, _, err = api.TolerantYAMLSerializer.Decode(data, &gvks[0], obj); err != nil {
		panic(fmt.Sprintf("cannot decode resource in %s: %v", fileName, err))
	}
}
