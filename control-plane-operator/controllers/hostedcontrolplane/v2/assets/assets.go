package assets

import (
	"embed"
	"fmt"
	"io/fs"
	"path"

	hyperapi "github.com/openshift/hypershift/support/api"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed */*.yaml
var manifestsAssets embed.FS

const (
	deploymentManifest  = "deployment.yaml"
	statefulSetManifest = "statefulset.yaml"
)

func LoadDeploymentManifest(componentName string) (*appsv1.Deployment, error) {
	deploy := &appsv1.Deployment{}
	_, _, err := LoadManifestInto(componentName, deploymentManifest, deploy)
	if err != nil {
		return nil, err
	}

	return deploy, nil
}

func LoadStatefulSetManifest(componentName string) (*appsv1.StatefulSet, error) {
	sts := &appsv1.StatefulSet{}
	_, _, err := LoadManifestInto(componentName, statefulSetManifest, sts)
	if err != nil {
		return nil, err
	}

	return sts, nil
}

// LoadManifest decodes the manifest data and load it into a new object.
func LoadManifest(componentName string, fileName string) (client.Object, *schema.GroupVersionKind, error) {
	return LoadManifestInto(componentName, fileName, nil)
}

// LoadManifest decodes the manifest data and load it into the provided 'into' object.
// If 'into' is nil, it will generate and return a new object.
func LoadManifestInto(componentName string, fileName string, into client.Object) (client.Object, *schema.GroupVersionKind, error) {
	filePath := path.Join(componentName, fileName)
	bytes, err := manifestsAssets.ReadFile(filePath)
	if err != nil {
		return nil, nil, err
	}

	obj, gvk, err := hyperapi.YamlSerializer.Decode(bytes, nil, into)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load %s manifest: %v", filePath, err)
	}
	return obj.(client.Object), gvk, err
}

func ForEachManifest(componentName string, action func(manifestName string) error) error {
	return fs.WalkDir(manifestsAssets, componentName, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		manifestName := d.Name()
		if manifestName == deploymentManifest || manifestName == statefulSetManifest {
			return nil
		}

		return action(manifestName)
	})
}
