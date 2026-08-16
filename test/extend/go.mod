module github.com/openshift/hypershift/test/extend

go 1.24.4

require (
	cel.dev/expr v0.24.0
	github.com/antlr4-go/antlr/v4 v4.13.1
	github.com/go-logr/logr v1.4.3
	github.com/go-task/slim-sprig/v3 v3.0.0
	github.com/google/cel-go v0.26.1
	github.com/google/go-cmp v0.7.0
	github.com/google/pprof v0.0.0-20250403155104-27863c87afa6
	github.com/inconshreveable/mousetrap v1.1.0
	github.com/onsi/ginkgo/v2 v2.27.2
	github.com/onsi/gomega v1.38.2
	github.com/openshift-eng/openshift-tests-extension v0.0.0-20251113163031-356b66aa5c24
	github.com/pkg/errors v0.9.1
	github.com/spf13/cobra v1.9.1
	github.com/spf13/pflag v1.0.7
	github.com/stoewer/go-strcase v1.3.0
	go.yaml.in/yaml/v3 v3.0.4
	golang.org/x/exp v0.0.0-20250305212735-054e65f0b394
	golang.org/x/net v0.43.0
	golang.org/x/sys v0.35.0
	golang.org/x/text v0.28.0
	golang.org/x/tools v0.36.0
	google.golang.org/genproto/googleapis/api v0.0.0-20250603155806-513f23925822
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250603155806-513f23925822
	google.golang.org/protobuf v1.36.7
)

require (
	github.com/kr/pretty v0.3.1 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	github.com/stretchr/testify v1.11.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
)

replace github.com/onsi/ginkgo/v2 => github.com/openshift/onsi-ginkgo/v2 v2.6.1-0.20251001123353-fd5b1fb35db1
