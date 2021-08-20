module github.com/openshift/hypershift/support

go 1.16

require (
	github.com/blang/semver v3.5.1+incompatible
	github.com/docker/distribution v2.6.0-rc.1.0.20180920194744-16128bbac47f+incompatible
	github.com/google/go-cmp v0.5.5
	github.com/onsi/gomega v1.11.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.0.1
	github.com/openshift/api v0.0.0-20201019163320-c6a5ec25f267
	github.com/pkg/errors v0.9.1
	github.com/spf13/cobra v1.1.1
	golang.org/x/net v0.0.0-20201202161906-c7110b5ffcbb
	golang.org/x/time v0.0.0-20200630173020-3af7569d3a1e
	k8s.io/api v0.20.2
	k8s.io/apimachinery v0.20.2
	k8s.io/client-go v0.20.2
	k8s.io/utils v0.0.0-20210111153108-fddb29f9d009
	sigs.k8s.io/controller-runtime v0.8.2
)
