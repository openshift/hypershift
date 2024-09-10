package controlplanecomponent

import (
	"embed"
	"path"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

//go:embed assets/*
var manifestsAssets embed.FS

func LoadDeploymentManifest(componentName string) (*appsv1.Deployment, error) {
	filePath := path.Join("assets", componentName, "deployment.yaml")
	bytes, err := manifestsAssets.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	deploy := &appsv1.Deployment{}
	err = yaml.Unmarshal(bytes, deploy)
	if err != nil {
		return nil, err
	}

	return deploy, nil
}
