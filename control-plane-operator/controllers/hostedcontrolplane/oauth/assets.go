package oauth

import (
	"bytes"
	"embed"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

//go:embed files/*
var f embed.FS

var (
	oauthPolicy = mustConfigmapData("audit-policy.yaml", "audit.yaml")
)

func mustAsset(file string) []byte {
	data, err := f.ReadFile("files/" + file)
	if err != nil {
		panic(err)
	}

	return data
}

func mustConfigmapData(file string, key string) string {
	fileBytes := mustAsset(file)
	cm := &corev1.ConfigMap{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(fileBytes), 500).Decode(&cm); err != nil {
		panic(err)
	}

	return cm.Data[key]
}
