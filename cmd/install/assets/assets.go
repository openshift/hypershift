package assets

import (
	"bytes"
	"embed"
	"io"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

//go:embed cluster-api/*
//go:embed hypershift-operator/*
var content embed.FS

func getContents(file string) []byte {
	f, err := content.Open(file)
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			panic(err)
		}
	}()
	b, err := io.ReadAll(f)
	if err != nil {
		panic(err)
	}
	return b
}

func getCustomResourceDefinition(file string) *apiextensionsv1.CustomResourceDefinition {
	b := getContents(file)
	o := apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(b), 100).Decode(&o); err != nil {
		panic(err)
	}
	return &o
}
