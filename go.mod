module openshift.io/hypershift

go 1.15

require (
	github.com/blang/semver v3.5.1+incompatible
	github.com/go-logr/logr v0.2.1
	github.com/google/uuid v1.1.2
	github.com/kevinburke/go-bindata v3.21.0+incompatible
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/openshift/api v0.0.0-20201019163320-c6a5ec25f267
	github.com/openshift/client-go v0.0.0-20200929181438-91d71ef2122c
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.6.0
	github.com/spf13/cobra v1.0.0
	github.com/stretchr/testify v1.5.1
	github.com/vincent-petithory/dataurl v0.0.0-20191104211930-d1553a71de50
	golang.org/x/crypto v0.0.0-20200930160638-afb6bcd081ae
	k8s.io/api v0.19.2
	k8s.io/apiextensions-apiserver v0.19.2
	k8s.io/apimachinery v0.19.2
	k8s.io/client-go v0.19.2
	k8s.io/utils v0.0.0-20200912215256-4140de9c8800
	sigs.k8s.io/cluster-api v0.3.11-0.20201103151415-d87a39c85f87
	sigs.k8s.io/cluster-api-provider-aws v0.6.3
	sigs.k8s.io/controller-runtime v0.7.0-alpha.6.0.20201109223643-114431a4df15
	sigs.k8s.io/controller-tools v0.3.0
)

replace sigs.k8s.io/controller-tools => github.com/vincepri/controller-tools v0.2.0-beta.2.0.20201007210946-d01e24361430
