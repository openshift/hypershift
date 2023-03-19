package api

import (
	"os"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	configv1 "github.com/openshift/api/config/v1"
	imagev1 "github.com/openshift/api/image/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"
	securityv1 "github.com/openshift/api/security/v1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1alpha1"
	hyperv1alpha1 "github.com/openshift/hypershift/api/v1alpha1"
	hyperv1beta1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/support/rhobsmonitoring"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	kasv1beta1 "k8s.io/apiserver/pkg/apis/apiserver/v1beta1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capiibm "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var (
	// InstallScheme only should be used when installing the HyperShift Operator.
	// The only current difference is that prometheusoperatorv1's GVKs must always be used, regardless of whether
	// RHOBS monitoring is enabled.
	// Ref: https://issues.redhat.com/browse/OCPBUGS-8713
	InstallScheme = runtime.NewScheme()
	Scheme        = runtime.NewScheme()
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
	JsonSerializer = json.NewSerializerWithOptions(
		json.DefaultMetaFactory, Scheme, Scheme,
		json.SerializerOptions{Yaml: false, Pretty: true, Strict: true},
	)
)

func init() {
	capiaws.AddToScheme(InstallScheme)
	capiibm.AddToScheme(InstallScheme)
	clientgoscheme.AddToScheme(InstallScheme)
	hyperv1alpha1.AddToScheme(InstallScheme)
	hyperv1beta1.AddToScheme(InstallScheme)
	capiv1.AddToScheme(InstallScheme)
	configv1.AddToScheme(InstallScheme)
	operatorv1.AddToScheme(InstallScheme)
	securityv1.AddToScheme(InstallScheme)
	routev1.AddToScheme(InstallScheme)
	rbacv1.AddToScheme(InstallScheme)
	corev1.AddToScheme(InstallScheme)
	apiextensionsv1.AddToScheme(InstallScheme)
	kasv1beta1.AddToScheme(InstallScheme)
	prometheusoperatorv1.AddToScheme(InstallScheme)
	agentv1.AddToScheme(InstallScheme)
	capikubevirt.AddToScheme(InstallScheme)
	capiazure.AddToScheme(InstallScheme)
	snapshotv1.AddToScheme(InstallScheme)
	imagev1.AddToScheme(InstallScheme)

	capiaws.AddToScheme(Scheme)
	capiibm.AddToScheme(Scheme)
	clientgoscheme.AddToScheme(Scheme)
	hyperv1alpha1.AddToScheme(Scheme)
	hyperv1beta1.AddToScheme(Scheme)
	capiv1.AddToScheme(Scheme)
	configv1.AddToScheme(Scheme)
	operatorv1.AddToScheme(Scheme)
	securityv1.AddToScheme(Scheme)
	routev1.AddToScheme(Scheme)
	rbacv1.AddToScheme(Scheme)
	corev1.AddToScheme(Scheme)
	apiextensionsv1.AddToScheme(Scheme)
	kasv1beta1.AddToScheme(Scheme)
	if os.Getenv(rhobsmonitoring.EnvironmentVariable) == "1" {
		rhobsmonitoring.AddToScheme(Scheme)
	} else {
		prometheusoperatorv1.AddToScheme(Scheme)
	}
	agentv1.AddToScheme(Scheme)
	capikubevirt.AddToScheme(Scheme)
	capiazure.AddToScheme(Scheme)
	snapshotv1.AddToScheme(Scheme)
	imagev1.AddToScheme(Scheme)
}
