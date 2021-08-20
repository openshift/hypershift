module github.com/openshift/hypershift

go 1.16

replace github.com/openshift/hypershift/api => ./api

require (
	github.com/aws/aws-sdk-go v1.35.0
	github.com/blang/semver v3.5.1+incompatible
	github.com/bombsimon/logrusr v1.0.0
	github.com/coreos/ignition/v2 v2.10.1
	github.com/docker/distribution v2.6.0-rc.1.0.20180920194744-16128bbac47f+incompatible
	github.com/docker/go-metrics v0.0.1 // indirect
	github.com/docker/libtrust v0.0.0-20160708172513-aabc10ec26b7 // indirect
	github.com/go-logr/logr v0.4.0
	github.com/google/go-cmp v0.5.5
	github.com/google/uuid v1.1.2
	github.com/gorilla/mux v1.8.0 // indirect
	github.com/onsi/gomega v1.11.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.0.1
	github.com/openshift/api v0.0.0-20201019163320-c6a5ec25f267
	github.com/openshift/client-go v0.0.0-20200929181438-91d71ef2122c
	github.com/openshift/hypershift/api v0.0.0-00010101000000-000000000000
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.6.0
	github.com/spf13/cobra v1.1.1
	github.com/stretchr/testify v1.7.0
	github.com/vincent-petithory/dataurl v0.0.0-20191104211930-d1553a71de50 // indirect
	go.opentelemetry.io/otel v0.19.0
	go.opentelemetry.io/otel/exporters/otlp v0.19.0
	go.opentelemetry.io/otel/sdk v0.19.0
	go.opentelemetry.io/otel/trace v0.19.0
	golang.org/x/net v0.0.0-20201202161906-c7110b5ffcbb
	golang.org/x/time v0.0.0-20200630173020-3af7569d3a1e
	gopkg.in/square/go-jose.v2 v2.3.1
	k8s.io/api v0.20.2
	k8s.io/apiextensions-apiserver v0.20.2
	k8s.io/apimachinery v0.20.2
	k8s.io/client-go v0.20.2
	k8s.io/utils v0.0.0-20210111153108-fddb29f9d009
	sigs.k8s.io/controller-runtime v0.8.2
	sigs.k8s.io/controller-tools v0.5.0
	sigs.k8s.io/yaml v1.2.0
)
