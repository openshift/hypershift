package assets

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed *
var manifestsAssets embed.FS

const (
	deploymentManifest  = "deployment.yaml"
	statefulSetManifest = "statefulset.yaml"
)

type ManifestReader interface {
	LoadDeploymentManifest(componentName string) (*appsv1.Deployment, error)
	LoadStatefulSetManifest(componentName string) (*appsv1.StatefulSet, error)

	LoadManifest(componentName string, fileName string) (client.Object, *schema.GroupVersionKind, error)
	LoadManifestInto(componentName string, fileName string, into client.Object) (client.Object, *schema.GroupVersionKind, error)

	ForEachManifest(componentName string, action func(manifestName string) error) error
}

var _ ManifestReader = &componentsManifestReader{}

type componentsManifestReader struct {
	platform hyperv1.PlatformType
}

func NewManifestReader(platform hyperv1.PlatformType) ManifestReader {
	return &componentsManifestReader{
		platform: platform,
	}
}

func (r *componentsManifestReader) LoadDeploymentManifest(componentName string) (*appsv1.Deployment, error) {
	deploy := &appsv1.Deployment{}
	_, _, err := r.LoadManifestInto(componentName, deploymentManifest, deploy)
	if err != nil {
		return nil, err
	}

	return deploy, nil
}

func (r *componentsManifestReader) LoadStatefulSetManifest(componentName string) (*appsv1.StatefulSet, error) {
	sts := &appsv1.StatefulSet{}
	_, _, err := r.LoadManifestInto(componentName, statefulSetManifest, sts)
	if err != nil {
		return nil, err
	}

	return sts, nil
}

// LoadManifest decodes the manifest data and load it into a new object.
func (r *componentsManifestReader) LoadManifest(componentName string, fileName string) (client.Object, *schema.GroupVersionKind, error) {
	return r.LoadManifestInto(componentName, fileName, nil)
}

// LoadManifest decodes the manifest data and load it into the provided 'into' object.
// If 'into' is nil, it will generate and return a new object.
func (r *componentsManifestReader) LoadManifestInto(componentName string, fileName string, into client.Object) (client.Object, *schema.GroupVersionKind, error) {
	// try to load platform specific manifest first.
	filePath := path.Join(componentName, strings.ToLower(string(r.platform)), fileName)
	bytes, err := manifestsAssets.ReadFile(filePath)
	if err != nil {
		// platform specific manifest doesn't exist, read the manifest from the component's root dir.
		filePath = path.Join(componentName, fileName)
		bytes, err = manifestsAssets.ReadFile(filePath)
		if err != nil {
			return nil, nil, err
		}
	}

	obj, gvk, err := hyperapi.YamlSerializer.Decode(bytes, nil, into)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load %s manifest: %v", filePath, err)
	}
	return obj.(client.Object), gvk, err
}

func (r *componentsManifestReader) ForEachManifest(componentName string, action func(manifestName string) error) error {
	return fs.WalkDir(manifestsAssets, componentName, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() != componentName && !strings.EqualFold(d.Name(), string(r.platform)) {
				// skip other platforms manifests.
				return fs.SkipDir
			}
			return nil
		}
		manifestName := d.Name()
		if manifestName == deploymentManifest || manifestName == statefulSetManifest {
			return nil
		}

		return action(manifestName)
	})
}
