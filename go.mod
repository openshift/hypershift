module github.com/openshift/hypershift

go 1.16

require (
	github.com/aws/aws-sdk-go v1.35.0
	github.com/blang/semver v3.5.1+incompatible
	github.com/go-logr/logr v0.4.0
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/google/go-cmp v0.5.2
	github.com/google/uuid v1.1.2
	github.com/onsi/gomega v1.10.2
	github.com/openshift/api v0.0.0-20201019163320-c6a5ec25f267
	github.com/openshift/client-go v0.0.0-20200929181438-91d71ef2122c
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.6.0
	github.com/spf13/cobra v1.1.1
	github.com/stretchr/testify v1.6.1
	github.com/vincent-petithory/dataurl v0.0.0-20191104211930-d1553a71de50
	golang.org/x/crypto v0.0.0-20201002170205-7f63de1d35b0
	k8s.io/api v0.20.2
	k8s.io/apiextensions-apiserver v0.20.2
	k8s.io/apimachinery v0.20.2
	k8s.io/client-go v0.20.2
	k8s.io/klog/v2 v2.5.0 // indirect
	k8s.io/utils v0.0.0-20210111153108-fddb29f9d009
	sigs.k8s.io/controller-runtime v0.8.2
	sigs.k8s.io/controller-tools v0.5.0
	sigs.k8s.io/structured-merge-diff/v4 v4.0.3 // indirect
)
