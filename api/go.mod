module github.com/openshift/hypershift/api

go 1.25.7

require (
	github.com/openshift/api v0.0.0-20260416105050-3c6b218b8a80
	k8s.io/api v0.35.3
	k8s.io/apimachinery v0.35.3
	k8s.io/utils v0.0.0-20260210185600-b8788abfbbc2
)

require (
	github.com/fxamacker/cbor/v2 v2.9.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	k8s.io/klog/v2 v2.140.0 // indirect
	k8s.io/kube-openapi v0.35.1 // indirect
	sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v6 v6.4.0 // indirect
)

replace github.com/aws/karpenter-provider-aws => github.com/openshift/aws-karpenter-provider-aws v0.0.0-20260722223016-abcf7d1e3417

replace sigs.k8s.io/karpenter => github.com/openshift/kubernetes-sigs-karpenter v0.0.0-20260721214330-2b8ed744bf1c

replace k8s.io/kube-openapi => k8s.io/kube-openapi v0.0.0-20260127142750-a19766b6e2d4
