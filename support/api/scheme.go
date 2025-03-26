package api

import (
	"os"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	"github.com/openshift/hypershift/support/rhobsmonitoring"

	configv1 "github.com/openshift/api/config/v1"
	imagev1 "github.com/openshift/api/image/v1"
	kcpv1 "github.com/openshift/api/kubecontrolplane/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	oauthv1 "github.com/openshift/api/oauth/v1"
	openshiftcpv1 "github.com/openshift/api/openshiftcontrolplane/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/api/operator/v1alpha1"
	osinv1 "github.com/openshift/api/osin/v1"
	routev1 "github.com/openshift/api/route/v1"
	securityv1 "github.com/openshift/api/security/v1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	kasv1beta1 "k8s.io/apiserver/pkg/apis/apiserver/v1beta1"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capiibm "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	capipowervs "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiopenstackv1alpha1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha1"
	capiopenstackv1beta1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ipamv1 "sigs.k8s.io/cluster-api/exp/ipam/api/v1beta1"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"

	orcv1alpha1 "github.com/k-orc/openstack-resource-controller/api/v1alpha1"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
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
	JsonSerializer = json.NewSerializerWithOptions(
		json.DefaultMetaFactory, Scheme, Scheme,
		json.SerializerOptions{Yaml: false, Pretty: true, Strict: true},
	)
	TolerantYAMLSerializer = json.NewSerializerWithOptions(
		json.DefaultMetaFactory, Scheme, Scheme,
		json.SerializerOptions{Yaml: true, Pretty: true, Strict: false},
	)
)

func init() {
	_ = capiaws.AddToScheme(Scheme)
	_ = capiibm.AddToScheme(Scheme)
	_ = clientgoscheme.AddToScheme(Scheme)
	_ = auditv1.AddToScheme(Scheme)
	_ = apiregistrationv1.AddToScheme(Scheme)
	_ = hyperv1beta1.AddToScheme(Scheme)
	_ = schedulingv1alpha1.AddToScheme(Scheme)
	_ = certificatesv1alpha1.AddToScheme(Scheme)
	_ = capiv1.AddToScheme(Scheme)
	_ = ipamv1.AddToScheme(Scheme)
	_ = configv1.AddToScheme(Scheme)
	_ = securityv1.AddToScheme(Scheme)
	_ = operatorv1.AddToScheme(Scheme)
	_ = oauthv1.AddToScheme(Scheme)
	_ = osinv1.AddToScheme(Scheme)
	_ = routev1.AddToScheme(Scheme)
	_ = imagev1.AddToScheme(Scheme)
	_ = clientgoscheme.AddToScheme(Scheme)
	_ = apiextensionsv1.AddToScheme(Scheme)
	_ = kasv1beta1.AddToScheme(Scheme)
	_ = openshiftcpv1.AddToScheme(Scheme)
	_ = v1alpha1.AddToScheme(Scheme)
	_ = apiserverconfigv1.AddToScheme(Scheme)
	if os.Getenv(rhobsmonitoring.EnvironmentVariable) == "1" {
		_ = rhobsmonitoring.AddToScheme(Scheme)
	} else {
		_ = prometheusoperatorv1.AddToScheme(Scheme)
	}
	_ = snapshotv1.AddToScheme(Scheme)
	_ = mcfgv1.AddToScheme(Scheme)
	_ = cdiv1beta1.AddToScheme(Scheme)
	_ = kubevirtv1.AddToScheme(Scheme)
	_ = capikubevirt.AddToScheme(Scheme)
	_ = capiazure.AddToScheme(Scheme)
	_ = agentv1.AddToScheme(Scheme)
	_ = capipowervs.AddToScheme(Scheme)
	_ = machinev1beta1.AddToScheme(Scheme)
	_ = capiopenstackv1alpha1.AddToScheme(Scheme)
	_ = capiopenstackv1beta1.AddToScheme(Scheme)
	_ = secretsstorev1.AddToScheme(Scheme)
	_ = kcpv1.AddToScheme(Scheme)
	_ = orcv1alpha1.AddToScheme(Scheme)
}
