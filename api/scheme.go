package api

import (
	agentv1 "github.com/eranco74/cluster-api-provider-agent/api/v1alpha1"
	capiibm "github.com/kubernetes-sigs/cluster-api-provider-ibmcloud/api/v1alpha4"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"
	securityv1 "github.com/openshift/api/security/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	kasv1beta1 "k8s.io/apiserver/pkg/apis/apiserver/v1beta1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var (
	Scheme = runtime.NewScheme()
	// TODO: Even though an object typer is specified here, serialized objects
	// are not always getting their TypeMeta set unless explicitly initialized
	// on the variable declarations.
	// Investigate https://github.com/kubernetes/cli-runtime/blob/master/pkg/printers/typesetter.go
	// as a possible solution.
	// See also: https://github.com/openshift/hive/blob/master/contrib/pkg/createcluster/create.go#L937-L954
	YamlSerializer = json.NewSerializerWithOptions(
		json.DefaultMetaFactory, Scheme, Scheme,
		json.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)
)

func init() {
	capiaws.AddToScheme(Scheme)
	capiibm.AddToScheme(Scheme)
	clientgoscheme.AddToScheme(Scheme)
	hyperv1.AddToScheme(Scheme)
	capiv1.AddToScheme(Scheme)
	configv1.AddToScheme(Scheme)
	operatorv1.AddToScheme(Scheme)
	securityv1.AddToScheme(Scheme)
	routev1.AddToScheme(Scheme)
	rbacv1.AddToScheme(Scheme)
	corev1.AddToScheme(Scheme)
	apiextensionsv1.AddToScheme(Scheme)
	kasv1beta1.AddToScheme(Scheme)
	prometheusoperatorv1.AddToScheme(Scheme)
	agentv1.AddToScheme(Scheme)
}
