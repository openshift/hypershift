package ignition

import "embed"

//go:embed apiserver-haproxy/*
var content embed.FS

func MustAsset(name string) string {
	b, err := content.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return string(b)
}
