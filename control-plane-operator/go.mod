module github.com/openshift/hypershift/control-plane-operator

go 1.16

replace github.com/openshift/hypershift/api => ../api

replace github.com/openshift/hypershift/support => ../support

require (
	cloud.google.com/go v0.58.0 // indirect
	github.com/blang/semver v3.5.1+incompatible
	github.com/go-logr/logr v0.4.0
	github.com/google/uuid v1.1.2
	github.com/onsi/gomega v1.11.0
	github.com/openshift/api v0.0.0-20201019163320-c6a5ec25f267
	github.com/openshift/hypershift/api v0.0.0-00010101000000-000000000000
	github.com/openshift/hypershift/support v0.0.0-00010101000000-000000000000
	github.com/pkg/errors v0.9.1
	github.com/spf13/cobra v1.1.1
	github.com/vincent-petithory/dataurl v0.0.0-20191104211930-d1553a71de50
	golang.org/x/crypto v0.0.0-20201002170205-7f63de1d35b0
	gopkg.in/ini.v1 v1.51.0
	k8s.io/api v0.20.2
	k8s.io/apiextensions-apiserver v0.20.2
	k8s.io/apimachinery v0.20.2
	k8s.io/apiserver v0.20.2
	k8s.io/client-go v0.20.2
	k8s.io/component-base v0.20.2
	k8s.io/kube-aggregator v0.20.2
	k8s.io/kube-scheduler v0.20.2
	k8s.io/utils v0.0.0-20210111153108-fddb29f9d009
	sigs.k8s.io/controller-runtime v0.8.2
)
