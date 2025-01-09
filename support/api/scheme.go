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
	capiaws.AddToScheme(Scheme)
	capiibm.AddToScheme(Scheme)
	clientgoscheme.AddToScheme(Scheme)
	auditv1.AddToScheme(Scheme)
	apiregistrationv1.AddToScheme(Scheme)
	hyperv1beta1.AddToScheme(Scheme)
	schedulingv1alpha1.AddToScheme(Scheme)
	certificatesv1alpha1.AddToScheme(Scheme)
	capiv1.AddToScheme(Scheme)
	ipamv1.AddToScheme(Scheme)
	configv1.AddToScheme(Scheme)
	securityv1.AddToScheme(Scheme)
	operatorv1.AddToScheme(Scheme)
	oauthv1.AddToScheme(Scheme)
	osinv1.AddToScheme(Scheme)
	routev1.AddToScheme(Scheme)
	imagev1.AddToScheme(Scheme)
	clientgoscheme.AddToScheme(Scheme)
	apiextensionsv1.AddToScheme(Scheme)
	kasv1beta1.AddToScheme(Scheme)
	openshiftcpv1.AddToScheme(Scheme)
	v1alpha1.AddToScheme(Scheme)
	apiserverconfigv1.AddToScheme(Scheme)
	if os.Getenv(rhobsmonitoring.EnvironmentVariable) == "1" {
		rhobsmonitoring.AddToScheme(Scheme)
	} else {
		prometheusoperatorv1.AddToScheme(Scheme)
	}
	snapshotv1.AddToScheme(Scheme)
	mcfgv1.AddToScheme(Scheme)
	cdiv1beta1.AddToScheme(Scheme)
	kubevirtv1.AddToScheme(Scheme)
	capikubevirt.AddToScheme(Scheme)
	capiazure.AddToScheme(Scheme)
	agentv1.AddToScheme(Scheme)
	capipowervs.AddToScheme(Scheme)
	machinev1beta1.AddToScheme(Scheme)
	capiopenstackv1alpha1.AddToScheme(Scheme)
	capiopenstackv1beta1.AddToScheme(Scheme)
	secretsstorev1.AddToScheme(Scheme)
	kcpv1.AddToScheme(Scheme)
	orcv1alpha1.AddToScheme(Scheme)
}
