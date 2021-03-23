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
//go:embed etcd/*
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

// getCustomResourceDefinition unmarshals a CRD from file. Note there's a hack
// here to strip leading YAML document separator which controller-gen creates
// even though there's only one object in the document.
func getCustomResourceDefinition(file string) *apiextensionsv1.CustomResourceDefinition {
	b := getContents(file)
	repaired := bytes.Replace(b, []byte("\n---\n"), []byte(""), 1)
	crd := apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(repaired), 100).Decode(&crd); err != nil {
		panic(err)
	}
	return &crd
}
