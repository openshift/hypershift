module github.com/openshift/hypershift

go 1.16

require (
	github.com/armon/go-socks5 v0.0.0-20160902184237-e75332964ef5
	github.com/aws/aws-sdk-go v1.40.22
	github.com/blang/semver v3.5.1+incompatible
	github.com/bombsimon/logrusr v1.0.0
	github.com/coreos/ignition/v2 v2.10.1
	github.com/docker/distribution v2.7.1+incompatible
	github.com/docker/libtrust v0.0.0-20160708172513-aabc10ec26b7 // indirect
	github.com/go-logr/logr v0.4.0
	github.com/google/go-cmp v0.5.6
	github.com/google/uuid v1.2.0
	github.com/gorilla/mux v1.8.0 // indirect
	github.com/kubernetes-sigs/cluster-api-provider-ibmcloud v0.0.2-0.20210820075925-77979fb340c7
	github.com/onsi/gomega v1.16.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.0.1
	github.com/openshift/api v0.0.0-20210901140736-d8ed1449662d
	github.com/openshift/client-go v0.0.0-20200929181438-91d71ef2122c
	github.com/pkg/errors v0.9.1
	github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring v0.51.1
	github.com/prometheus/client_golang v1.11.0
	github.com/sirupsen/logrus v1.8.1
	github.com/spf13/cobra v1.2.1
	github.com/stretchr/testify v1.7.0
	github.com/vincent-petithory/dataurl v0.0.0-20191104211930-d1553a71de50
	go.opentelemetry.io/otel v0.20.0
	go.opentelemetry.io/otel/exporters/otlp v0.20.0
	go.opentelemetry.io/otel/sdk v0.20.0
	go.opentelemetry.io/otel/trace v0.20.0
	golang.org/x/crypto v0.0.0-20210817164053-32db794688a5
	golang.org/x/net v0.0.0-20210813160813-60bc85c4be6d
	golang.org/x/time v0.0.0-20210723032227-1f47c861a9ac
	gopkg.in/ini.v1 v1.63.2
	gopkg.in/square/go-jose.v2 v2.5.1
	k8s.io/api v0.22.2
	k8s.io/apiextensions-apiserver v0.22.2
	k8s.io/apimachinery v0.22.2
	k8s.io/apiserver v0.22.2
	k8s.io/client-go v0.22.2
	k8s.io/component-base v0.22.2
	k8s.io/kube-aggregator v0.20.2
	k8s.io/kube-scheduler v0.21.4
	k8s.io/utils v0.0.0-20210930125809-cb0fa318a74b
	sigs.k8s.io/apiserver-network-proxy v0.0.24
	sigs.k8s.io/cluster-api v0.4.2
	sigs.k8s.io/cluster-api-provider-aws v0.7.0
	sigs.k8s.io/controller-runtime v0.10.2
	sigs.k8s.io/yaml v1.3.0
)

replace (
	k8s.io/utils => k8s.io/utils v0.0.0-20210111153108-fddb29f9d009
	sigs.k8s.io/apiserver-network-proxy/konnectivity-client => sigs.k8s.io/apiserver-network-proxy/konnectivity-client v0.0.24
	sigs.k8s.io/cluster-api => sigs.k8s.io/cluster-api v1.0.0
	sigs.k8s.io/controller-tools => sigs.k8s.io/controller-tools v0.5.0
)
