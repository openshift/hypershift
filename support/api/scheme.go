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

	batchv1 "k8s.io/api/batch/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	kasv1beta1 "k8s.io/apiserver/pkg/apis/apiserver/v1beta1"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capiibm "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiopenstackv1alpha1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha1"
	capiopenstackv1beta1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ipamv1 "sigs.k8s.io/cluster-api/exp/ipam/api/v1beta1"
	karpenterapis "sigs.k8s.io/karpenter/pkg/apis"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"

	orcv1alpha1 "github.com/k-orc/openstack-resource-controller/api/v1alpha1"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

var (
	Scheme              = runtime.NewScheme()
	AllMonitoringScheme = runtime.NewScheme()
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
	// AllMonitoringYamlSerializer allows decoding monitoring resources
	// from either the default coreos group version or the rhobs group version
	AllMonitoringYamlSerializer = json.NewSerializerWithOptions(
		json.DefaultMetaFactory, AllMonitoringScheme, AllMonitoringScheme,
		json.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)
)

func init() {
	schemes := []struct {
		scheme               *runtime.Scheme
		includeAllMonitoring bool
	}{
		{scheme: Scheme, includeAllMonitoring: false},
		{scheme: AllMonitoringScheme, includeAllMonitoring: true},
	}
	for _, sd := range schemes {
		scheme := sd.scheme
		_ = capiaws.AddToScheme(scheme)
		_ = capiibm.AddToScheme(scheme)
		_ = clientgoscheme.AddToScheme(scheme)
		_ = auditv1.AddToScheme(scheme)
		_ = apiregistrationv1.AddToScheme(scheme)
		_ = hyperv1beta1.AddToScheme(scheme)
		_ = schedulingv1alpha1.AddToScheme(scheme)
		_ = certificatesv1alpha1.AddToScheme(scheme)
		_ = capiv1.AddToScheme(scheme)
		_ = ipamv1.AddToScheme(scheme)
		_ = configv1.AddToScheme(scheme)
		_ = securityv1.AddToScheme(scheme)
		_ = operatorv1.AddToScheme(scheme)
		_ = oauthv1.AddToScheme(scheme)
		_ = osinv1.AddToScheme(scheme)
		_ = routev1.AddToScheme(scheme)
		_ = imagev1.AddToScheme(scheme)
		_ = clientgoscheme.AddToScheme(scheme)
		_ = apiextensionsv1.AddToScheme(scheme)
		_ = kasv1beta1.AddToScheme(scheme)
		_ = openshiftcpv1.AddToScheme(scheme)
		_ = v1alpha1.AddToScheme(scheme)
		_ = apiserverconfigv1.AddToScheme(scheme)
		if os.Getenv(rhobsmonitoring.EnvironmentVariable) == "1" {
			_ = rhobsmonitoring.AddToScheme(scheme)
			if sd.includeAllMonitoring {
				_ = prometheusoperatorv1.AddToScheme(scheme)
			}
		} else {
			_ = prometheusoperatorv1.AddToScheme(scheme)
		}
		_ = snapshotv1.AddToScheme(scheme)
		_ = mcfgv1.AddToScheme(scheme)
		_ = cdiv1beta1.AddToScheme(scheme)
		_ = kubevirtv1.AddToScheme(scheme)
		_ = capikubevirt.AddToScheme(scheme)
		_ = capiazure.AddToScheme(scheme)
		_ = agentv1.AddToScheme(scheme)
		_ = machinev1beta1.AddToScheme(scheme)
		_ = capiopenstackv1alpha1.AddToScheme(scheme)
		_ = capiopenstackv1beta1.AddToScheme(scheme)
		_ = secretsstorev1.AddToScheme(scheme)
		_ = kcpv1.AddToScheme(scheme)
		_ = orcv1alpha1.AddToScheme(scheme)
		_ = batchv1.AddToScheme(scheme)
		karpenterGroupVersion := schema.GroupVersion{Group: karpenterapis.Group, Version: "v1"}
		metav1.AddToGroupVersion(scheme, karpenterGroupVersion)
		scheme.AddKnownTypes(karpenterGroupVersion,
			&karpenterv1.NodeClaim{},
			&karpenterv1.NodeClaimList{},
			&karpenterv1.NodePool{},
			&karpenterv1.NodePoolList{},
		)
	}
}
