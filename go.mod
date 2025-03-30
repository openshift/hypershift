module github.com/openshift/hypershift

go 1.21

toolchain go1.22.9

require (
	github.com/Azure/azure-sdk-for-go v63.4.0+incompatible
	github.com/Azure/go-autorest/autorest v0.11.29
	github.com/Azure/go-autorest/autorest/azure/auth v0.5.11
	github.com/IBM-Cloud/power-go-client v1.2.2
	github.com/IBM/go-sdk-core/v5 v5.17.4
	github.com/IBM/ibm-cos-sdk-go v1.9.4
	github.com/IBM/networking-go-sdk v0.29.0
	github.com/IBM/platform-services-go-sdk v0.31.3
	github.com/IBM/vpc-go-sdk v0.30.0
	github.com/armon/go-socks5 v0.0.0-20160902184237-e75332964ef5
	github.com/aws/aws-sdk-go v1.44.190
	github.com/blang/semver v3.5.1+incompatible
	github.com/clarketm/json v1.14.1
	github.com/coreos/ignition/v2 v2.14.0
	github.com/docker/distribution v2.8.2+incompatible
	github.com/elazarl/goproxy v0.0.0-20240618083138-03be62527ccb
	github.com/evanphx/json-patch/v5 v5.6.0
	github.com/go-logr/logr v1.4.2
	github.com/go-logr/zapr v1.2.3
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da
	github.com/google/go-cmp v0.6.0
	github.com/google/gofuzz v1.2.0
	github.com/google/uuid v1.6.0
	github.com/grpc-ecosystem/go-grpc-prometheus v1.2.0
	github.com/hashicorp/go-uuid v1.0.1
	github.com/kubernetes-csi/external-snapshotter/client/v6 v6.0.1
	github.com/onsi/gomega v1.33.1
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.0.3-0.20211202183452-c5a74bcca799
	github.com/openshift/api v0.0.0-20231025170628-b8a18fdc040d
	github.com/openshift/client-go v0.0.0-20220525160904-9e1acff93e4a
	github.com/openshift/cloud-credential-operator v0.0.0-20220708202639-ef451d260cf6
	github.com/openshift/cluster-api-provider-agent/api v0.0.0-20230918065757-81658c4ddf2f
	github.com/openshift/cluster-node-tuning-operator v0.0.0-20220614214129-2c76314fb3cc
	github.com/openshift/library-go v0.0.0-20220811164604-fef421ec24f4
	github.com/operator-framework/api v0.10.7
	github.com/pkg/errors v0.9.1
	github.com/ppc64le-cloud/powervs-utils v0.0.0-20230306072409-bc42a581099f
	github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring v0.57.0
	github.com/prometheus/client_golang v1.14.0
	github.com/prometheus/client_model v0.3.0
	github.com/prometheus/common v0.37.0
	github.com/spf13/cobra v1.8.0
	github.com/stretchr/testify v1.9.0
	github.com/tombuildsstuff/giovanni v0.18.0
	github.com/vincent-petithory/dataurl v1.0.0
	go.etcd.io/etcd/api/v3 v3.5.10
	go.etcd.io/etcd/client/pkg/v3 v3.5.10
	go.etcd.io/etcd/client/v3 v3.5.10
	go.etcd.io/etcd/server/v3 v3.5.10
	go.etcd.io/etcd/tests/v3 v3.5.10
	go.uber.org/zap v1.25.0
	golang.org/x/crypto v0.31.0
	golang.org/x/exp v0.0.0-20231110203233-9a3e6036ecaa
	golang.org/x/net v0.33.0
	golang.org/x/time v0.5.0
	google.golang.org/grpc v1.58.3
	gopkg.in/go-jose/go-jose.v2 v2.6.3
	gopkg.in/ini.v1 v1.67.0
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.28.3
	k8s.io/apiextensions-apiserver v0.25.0
	k8s.io/apimachinery v0.28.3
	k8s.io/apiserver v0.25.0
	k8s.io/cli-runtime v0.25.0
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/component-base v0.25.0
	k8s.io/klog/v2 v2.130.1
	k8s.io/kube-aggregator v0.24.0
	k8s.io/kube-scheduler v0.23.1
	k8s.io/kubectl v0.25.0
	k8s.io/pod-security-admission v0.23.5
	k8s.io/utils v0.0.0-20240711033017-18e509b52bc8
	kubevirt.io/api v0.58.0
	kubevirt.io/containerized-data-importer-api v1.50.0
	sigs.k8s.io/cluster-api v1.3.2
	sigs.k8s.io/cluster-api-provider-aws/v2 v2.0.2
	sigs.k8s.io/cluster-api-provider-azure v1.6.3
	sigs.k8s.io/cluster-api-provider-ibmcloud v0.2.4
	sigs.k8s.io/cluster-api-provider-kubevirt v0.0.0-00010101000000-000000000000
	sigs.k8s.io/controller-runtime v0.13.1
	sigs.k8s.io/yaml v1.4.0
)

require (
	cloud.google.com/go/compute v1.23.0 // indirect
	cloud.google.com/go/compute/metadata v0.2.3 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20210617225240-d185dfc1b5a1 // indirect
	github.com/Azure/go-autorest v14.2.0+incompatible // indirect
	github.com/Azure/go-autorest/autorest/adal v0.9.23 // indirect
	github.com/Azure/go-autorest/autorest/azure/cli v0.4.5 // indirect
	github.com/Azure/go-autorest/autorest/date v0.3.0 // indirect
	github.com/Azure/go-autorest/autorest/to v0.4.0 // indirect
	github.com/Azure/go-autorest/autorest/validation v0.3.1 // indirect
	github.com/Azure/go-autorest/logger v0.2.1 // indirect
	github.com/Azure/go-autorest/tracing v0.6.0 // indirect
	github.com/MakeNowJust/heredoc v1.0.0 // indirect
	github.com/asaskevich/govalidator v0.0.0-20230301143203-a9d515a09cc2 // indirect
	github.com/benbjohnson/clock v1.3.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/cenkalti/backoff/v4 v4.1.2 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/chai2010/gettext-go v1.0.2 // indirect
	github.com/coreos/go-semver v0.3.0 // indirect
	github.com/coreos/go-systemd/v22 v22.3.2 // indirect
	github.com/coreos/vcontext v0.0.0-20211021162308-f1dbbca7bef4 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dimchansky/utfbom v1.1.1 // indirect
	github.com/docker/go-metrics v0.0.1 // indirect
	github.com/docker/libtrust v0.0.0-20160708172513-aabc10ec26b7 // indirect
	github.com/dustin/go-humanize v1.0.0 // indirect
	github.com/emicklei/go-restful/v3 v3.11.0 // indirect
	github.com/evanphx/json-patch v5.7.0+incompatible // indirect
	github.com/exponent-io/jsonpath v0.0.0-20210407135951-1de76d718b3f // indirect
	github.com/fsnotify/fsnotify v1.7.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.3 // indirect
	github.com/go-errors/errors v1.5.1 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-openapi/analysis v0.21.2 // indirect
	github.com/go-openapi/errors v0.21.0 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.20.2 // indirect
	github.com/go-openapi/loads v0.21.1 // indirect
	github.com/go-openapi/runtime v0.23.0 // indirect
	github.com/go-openapi/spec v0.20.4 // indirect
	github.com/go-openapi/strfmt v0.22.1 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/go-openapi/validate v0.21.0 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.19.0 // indirect
	github.com/gobuffalo/flect v0.3.0 // indirect
	github.com/gofrs/uuid v4.2.0+incompatible // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-jwt/jwt/v4 v4.5.0 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/btree v1.0.1 // indirect
	github.com/google/gnostic v0.6.9 // indirect
	github.com/google/gnostic-models v0.6.9 // indirect
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510 // indirect
	github.com/gorilla/mux v1.8.0 // indirect
	github.com/gorilla/websocket v1.5.0 // indirect
	github.com/gregjones/httpcache v0.0.0-20190611155906-901d90724c79 // indirect
	github.com/grpc-ecosystem/go-grpc-middleware v1.3.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway v1.16.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.7 // indirect
	github.com/imdario/mergo v0.3.13 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/jonboulle/clockwork v0.2.2 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/liggitt/tabwriter v0.0.0-20181228230101-89fcab3d43de // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.2-0.20181231171920-c182affec369 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/go-wordwrap v1.0.0 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/moby/spdystream v0.2.0 // indirect
	github.com/moby/term v0.0.0-20210619224110-3f7ff695adc6 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/monochromegane/go-gitignore v0.0.0-20200626010858-205db1a8cc00 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/oklog/ulid v1.3.1 // indirect
	github.com/openshift/custom-resource-status v1.1.2 // indirect
	github.com/opentracing/opentracing-go v1.2.0 // indirect
	github.com/pborman/uuid v1.2.0 // indirect
	github.com/peterbourgon/diskv v2.0.1+incompatible // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/procfs v0.12.0 // indirect
	github.com/russross/blackfriday v1.5.2 // indirect
	github.com/sirupsen/logrus v1.9.0 // indirect
	github.com/soheilhy/cmux v0.1.5 // indirect
	github.com/spf13/pflag v1.0.6-0.20210604193023-d5e0c0615ace // indirect
	github.com/tmc/grpc-websocket-proxy v0.0.0-20201229170055-e5319fda7802 // indirect
	github.com/xiang90/probing v0.0.0-20190116061207-43a291ad63a2 // indirect
	github.com/xlab/treeprint v1.2.0 // indirect
	go.etcd.io/bbolt v1.3.8 // indirect
	go.etcd.io/etcd/client/v2 v2.305.10 // indirect
	go.etcd.io/etcd/pkg/v3 v3.5.10 // indirect
	go.etcd.io/etcd/raft/v3 v3.5.10 // indirect
	go.mongodb.org/mongo-driver v1.14.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.25.0 // indirect
	go.opentelemetry.io/otel v1.4.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/internal/retry v1.4.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.4.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.4.0 // indirect
	go.opentelemetry.io/otel/sdk v1.4.0 // indirect
	go.opentelemetry.io/otel/trace v1.4.0 // indirect
	go.opentelemetry.io/proto/otlp v0.12.0 // indirect
	go.starlark.net v0.0.0-20231101134539-556fd59b42f6 // indirect
	go.uber.org/goleak v1.2.1 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/oauth2 v0.14.0 // indirect
	golang.org/x/sys v0.28.0 // indirect
	golang.org/x/term v0.27.0 // indirect
	golang.org/x/text v0.21.0 // indirect
	gomodules.xyz/jsonpatch/v2 v2.4.0 // indirect
	google.golang.org/appengine v1.6.8 // indirect
	google.golang.org/genproto v0.0.0-20230726155614-23370e0ffb3e // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20230803162519-f966b187b2e5 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20230803162519-f966b187b2e5 // indirect
	google.golang.org/protobuf v1.35.1 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.0.0 // indirect
	gopkg.in/square/go-jose.v2 v2.5.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/kube-openapi v0.0.0-20250318190949-c8a335a9a2ff // indirect
	kubevirt.io/controller-lifecycle-operator-sdk v0.2.1 // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/kustomize/api v0.12.1 // indirect
	sigs.k8s.io/kustomize/kyaml v0.13.9 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.6.0 // indirect
)

replace (
	k8s.io/api => k8s.io/api v0.25.2
	k8s.io/apimachinery => k8s.io/apimachinery v0.25.2
	k8s.io/client-go => k8s.io/client-go v0.25.2
	k8s.io/kube-openapi => k8s.io/kube-openapi v0.0.0-20250318190949-c8a335a9a2ff
	k8s.io/kubernetes => k8s.io/kubernetes v0.23.3
	kubevirt.io/client-go => kubevirt.io/client-go v0.0.0-00010101000000-000000000000
	kubevirt.io/containerized-data-importer-api => github.com/kubevirt/containerized-data-importer-api v1.41.1-0.20211201033752-05520fb9f18d
	sigs.k8s.io/apiserver-network-proxy/konnectivity-client => sigs.k8s.io/apiserver-network-proxy/konnectivity-client v0.0.24
	sigs.k8s.io/cluster-api-provider-ibmcloud => github.com/openshift/cluster-api-provider-ibmcloud v0.0.0-20230125060612-e69847607d74
	sigs.k8s.io/cluster-api-provider-kubevirt => github.com/openshift/cluster-api-provider-kubevirt v0.0.0-20230126155822-4786167d51b3
)
