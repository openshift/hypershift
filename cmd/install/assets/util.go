package assets

import (
	"bytes"
	"io"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func mustAssetReader(asset string) io.Reader {
	return bytes.NewReader(MustAsset(asset))
}

func mustCustomResourceDefinition(manifest io.Reader) *apiextensionsv1.CustomResourceDefinition {
	o := apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.NewYAMLOrJSONDecoder(manifest, 100).Decode(&o); err != nil {
		panic(err)
	}
	return &o
}
