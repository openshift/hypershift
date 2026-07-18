package assets

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"text/template"

	hyperapi "github.com/openshift/hypershift/support/api"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed */*.yaml
var manifestsAssets embed.FS

const (
	deploymentManifest  = "deployment.yaml"
	statefulSetManifest = "statefulset.yaml"
	cronJobManifest     = "cronjob.yaml"
	jobManifest         = "job.yaml"
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

func LoadCronJobManifest(componentName string) (*batchv1.CronJob, error) {
	cronJob := &batchv1.CronJob{}
	_, _, err := LoadManifestInto(componentName, cronJobManifest, cronJob)
	if err != nil {
		return nil, err
	}

	return cronJob, nil
}

func LoadJobManifest(componentName string) (*batchv1.Job, error) {
	job := &batchv1.Job{}
	_, _, err := LoadManifestInto(componentName, jobManifest, job)
	if err != nil {
		return nil, err
	}
	return job, nil
}

// LoadManifest decodes the manifest data and load it into a new object.
func LoadManifest(componentName string, fileName string) (client.Object, *schema.GroupVersionKind, error) {
	return LoadManifestInto(componentName, fileName, nil)
}

// LoadManifest decodes the manifest data and load it into the provided 'into' object.
// If 'into' is nil, it will generate and return a new object.
func LoadManifestInto(componentName string, fileName string, into client.Object) (client.Object, *schema.GroupVersionKind, error) {
	filePath := path.Join(componentName, fileName)
	raw, err := manifestsAssets.ReadFile(filePath)
	if err != nil {
		return nil, nil, err
	}

	obj, gvk, err := hyperapi.AllMonitoringYamlSerializer.Decode(raw, nil, into)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load %s manifest: %w", filePath, err)
	}
	return obj.(client.Object), gvk, err
}

// LoadManifestTemplated reads the raw asset bytes, renders them as a Go text/template
// with the given templateData, then decodes the resulting YAML.
// If templateData is nil, it falls through to the standard LoadManifest path.
//
// Safety: templateData values are expected to be validated at the API level
// (e.g., DNS1123 shard names via CEL). This function uses text/template rather
// than html/template because the output is YAML, not HTML. Callers must not
// pass unvalidated user input as template data.
func LoadManifestTemplated(componentName, fileName string, templateData map[string]string) (client.Object, *schema.GroupVersionKind, error) {
	if templateData == nil {
		return LoadManifest(componentName, fileName)
	}

	filePath := path.Join(componentName, fileName)
	raw, err := manifestsAssets.ReadFile(filePath)
	if err != nil {
		return nil, nil, err
	}

	tmpl, err := template.New(filePath).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse template %s: %w", filePath, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, templateData); err != nil {
		return nil, nil, fmt.Errorf("failed to execute template %s: %w", filePath, err)
	}

	obj, gvk, err := hyperapi.AllMonitoringYamlSerializer.Decode(buf.Bytes(), nil, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load templated %s manifest: %w", filePath, err)
	}
	return obj.(client.Object), gvk, err
}

// LoadStatefulSetManifestTemplated loads a statefulset manifest with template rendering.
func LoadStatefulSetManifestTemplated(componentName string, templateData map[string]string) (*appsv1.StatefulSet, error) {
	if templateData == nil {
		return LoadStatefulSetManifest(componentName)
	}

	obj, _, err := LoadManifestTemplated(componentName, statefulSetManifest, templateData)
	if err != nil {
		return nil, err
	}
	sts, ok := obj.(*appsv1.StatefulSet)
	if !ok {
		return nil, fmt.Errorf("expected StatefulSet but got %T", obj)
	}
	return sts, nil
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
		if manifestName == deploymentManifest || manifestName == statefulSetManifest || manifestName == cronJobManifest || manifestName == jobManifest {
			return nil
		}

		return action(manifestName)
	})
}
