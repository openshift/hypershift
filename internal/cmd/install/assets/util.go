package assets

import (
	"bytes"
	"io"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func mustAssetReader(asset string) io.Reader {
	return bytes.NewReader(MustAsset(asset))
}

func newDeployment(manifest io.Reader) (*appsv1.Deployment, error) {
	o := appsv1.Deployment{}
	if err := yaml.NewYAMLOrJSONDecoder(manifest, 100).Decode(&o); err != nil {
		return nil, err
	}

	return &o, nil
}

func mustCustomResourceDefinition(manifest io.Reader) *apiextensionsv1.CustomResourceDefinition {
	o := apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.NewYAMLOrJSONDecoder(manifest, 100).Decode(&o); err != nil {
		panic(err)
	}
	return &o
}

func newClusterRole(manifest io.Reader) (*rbacv1.ClusterRole, error) {
	o := rbacv1.ClusterRole{}
	if err := yaml.NewYAMLOrJSONDecoder(manifest, 100).Decode(&o); err != nil {
		return nil, err
	}

	return &o, nil
}
