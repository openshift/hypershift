package static

import "embed"

//go:embed 4.7/*

var content embed.FS

func MustAsset(name string) []byte {
	b, err := content.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return b
}
