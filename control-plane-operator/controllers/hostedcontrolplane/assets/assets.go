package assets

import "embed"

//go:embed apiserver-haproxy/*
//go:embed cluster-bootstrap/*
//go:embed cluster-version-operator/*
//go:embed hosted-cluster-config-operator/*
//go:embed ignition-configs/*
//go:embed install-config/*
//go:embed kube-scheduler/*
//go:embed machine-config-server/*
//go:embed oauth-apiserver/*
//go:embed oauth-openshift/*
//go:embed openshift-apiserver/*
//go:embed openshift-controller-manager/*
//go:embed registry/*
//go:embed roks-metrics/*
//go:embed user-manifests-bootstrapper/*
//go:embed olm/*
//go:embed dnsmasq/*
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
