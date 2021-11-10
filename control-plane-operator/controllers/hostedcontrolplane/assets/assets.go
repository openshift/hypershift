package assets

import "embed"

//go:embed user-manifests-bootstrapper/*
var content embed.FS

func AssetDir(name string) ([]string, error) {
	entries, err := content.ReadDir(name)
	if err != nil {
		panic(err)
	}
	var files []string
	for _, entry := range entries {
		files = append(files, entry.Name())
	}
	return files, nil
}

func MustAsset(name string) []byte {
	b, err := content.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return b
}
