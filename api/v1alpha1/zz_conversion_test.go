package v1alpha1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	fuzz "github.com/google/gofuzz"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/util/configrefs"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/support/conversiontest"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/apitesting/fuzzer"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/controller-runtime/pkg/conversion"
)

func networkingFuzzer(in *ClusterNetworking, c fuzz.Continue) {
	c.FuzzNoCustom(in)
	in.MachineCIDR = "10.10.100.0/24"
	in.PodCIDR = "10.10.101.0/24"
	in.ServiceCIDR = "10.10.102.0/24"
	in.MachineNetwork = []MachineNetworkEntry{
		{
			CIDR: mustParseCIDR(in.MachineCIDR),
		},
	}
	in.ClusterNetwork = []ClusterNetworkEntry{
		{
			CIDR: mustParseCIDR(in.PodCIDR),
		},
	}
	in.ServiceNetwork = []ServiceNetworkEntry{
		{
			CIDR: mustParseCIDR(in.ServiceCIDR),
		},
	}
}

func v1beta1NetworkingFuzzer(in *v1beta1.ClusterNetworking, c fuzz.Continue) {
	in.ClusterNetwork = []v1beta1.ClusterNetworkEntry{{CIDR: mustParseCIDR("10.11.100.0/24")}}
	in.MachineNetwork = []v1beta1.MachineNetworkEntry{{CIDR: mustParseCIDR("10.11.101.0/24")}}
	in.ServiceNetwork = []v1beta1.ServiceNetworkEntry{{CIDR: mustParseCIDR("10.11.102.0/24")}}
}

func mustParseCIDR(str string) ipnet.IPNet {
	result, err := ipnet.ParseCIDR(str)
	if err != nil {
		panic(err.Error())
	}
	return *result
}

func v1beta1ConfigFuzzer(in *v1beta1.ClusterConfiguration, c fuzz.Continue) {
	c.FuzzNoCustom(in)
	if in.APIServer != nil && in.APIServer.Audit.Profile == "" {
		in.APIServer.Audit.Profile = configv1.DefaultAuditProfileType
	}
}

func configFuzzer(in *ClusterConfiguration, c fuzz.Continue) {
	in.Items = nil
	if randomBool() {
		in.APIServer = &configv1.APIServerSpec{}
		c.Fuzz(in.APIServer)
		if in.APIServer.Audit.Profile == "" {
			in.APIServer.Audit.Profile = configv1.DefaultAuditProfileType
		}
		in.Items = append(in.Items, runtime.RawExtension{
			Raw: serializeResource(&configv1.APIServer{Spec: *in.APIServer}),
		})
	}
	if randomBool() {
		in.Authentication = &configv1.AuthenticationSpec{}
		c.Fuzz(in.Authentication)
		in.Items = append(in.Items, runtime.RawExtension{
			Raw: serializeResource(&configv1.Authentication{Spec: *in.Authentication}),
		})
	}
	if randomBool() {
		in.FeatureGate = &configv1.FeatureGateSpec{}
		c.Fuzz(in.FeatureGate)
		in.Items = append(in.Items, runtime.RawExtension{
			Raw: serializeResource(&configv1.FeatureGate{Spec: *in.FeatureGate}),
		})
	}
	if randomBool() {
		in.Image = &configv1.ImageSpec{}
		c.Fuzz(in.Image)
		in.Items = append(in.Items, runtime.RawExtension{
			Raw: serializeResource(&configv1.Image{Spec: *in.Image}),
		})
	}
	if randomBool() {
		in.Ingress = &configv1.IngressSpec{}
		c.Fuzz(in.Ingress)
		in.Items = append(in.Items, runtime.RawExtension{
			Raw: serializeResource(&configv1.Ingress{Spec: *in.Ingress}),
		})
	}
	if randomBool() {
		in.Network = &configv1.NetworkSpec{}
		c.Fuzz(in.Network)
		in.Items = append(in.Items, runtime.RawExtension{
			Raw: serializeResource(&configv1.Network{Spec: *in.Network}),
		})
	}
	if randomBool() {
		in.OAuth = &configv1.OAuthSpec{}
		c.Fuzz(in.OAuth)
		in.Items = append(in.Items, runtime.RawExtension{
			Raw: serializeResource(&configv1.OAuth{Spec: *in.OAuth}),
		})
	}
	if randomBool() {
		in.Scheduler = &configv1.SchedulerSpec{}
		c.Fuzz(in.Scheduler)
		in.Items = append(in.Items, runtime.RawExtension{
			Raw: serializeResource(&configv1.Scheduler{Spec: *in.Scheduler}),
		})
	}
	if randomBool() {
		in.Proxy = &configv1.ProxySpec{}
		c.Fuzz(in.Proxy)
		in.Items = append(in.Items, runtime.RawExtension{
			Raw: serializeResource(&configv1.Proxy{Spec: *in.Proxy}),
		})
	}
	configMapRefs := []corev1.LocalObjectReference{}
	for _, ref := range configrefs.ConfigMapRefs(in) {
		configMapRefs = append(configMapRefs, corev1.LocalObjectReference{
			Name: ref,
		})
	}
	in.ConfigMapRefs = configMapRefs
	secretRefs := []corev1.LocalObjectReference{}
	for _, ref := range configrefs.SecretRefs(in) {
		secretRefs = append(secretRefs, corev1.LocalObjectReference{
			Name: ref,
		})
	}
	in.SecretRefs = secretRefs
}

func serializeResource(obj runtime.Object) []byte {
	b := &bytes.Buffer{}
	gvks, _, err := localScheme.ObjectKinds(obj)
	if err != nil {
		panic(err.Error())
	}
	if len(gvks) == 0 {
		panic(fmt.Sprintf("did not find gvk for %T", obj))
	}
	obj.GetObjectKind().SetGroupVersionKind(gvks[0])
	err = serializer.Encode(obj, b)
	if err != nil {
		panic(err.Error())
	}

	// Remove the status part of the serialized resource. We only have
	// spec to begin with and status causes incompatibilities with previous
	// versions of the CPO
	unstructuredObject := &unstructured.Unstructured{}
	if _, _, err := unstructured.UnstructuredJSONScheme.Decode(b.Bytes(), nil, unstructuredObject); err != nil {
		return nil
	}
	unstructured.RemoveNestedField(unstructuredObject.Object, "status")
	b = &bytes.Buffer{}
	if err := unstructured.UnstructuredJSONScheme.Encode(unstructuredObject, b); err != nil {
		return nil
	}

	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

func randomBool() bool {
	rand.Seed(time.Now().UnixNano())
	return rand.Intn(2) == 1
}

func secretEncryptionFuzzer(in *SecretEncryptionSpec, c fuzz.Continue) {
	c.FuzzNoCustom(in)
	in.KMS = &KMSSpec{
		Provider: AWS,
		AWS: &AWSKMSSpec{
			Region: "",
			ActiveKey: AWSKMSKeyEntry{
				ARN: c.RandString(),
			},
			BackupKey: nil,
			Auth: AWSKMSAuthSpec{
				Credentials: corev1.LocalObjectReference{
					Name: c.RandString(),
				},
			},
		},
	}
}

func awsRolesRefFuzzer(in *AWSPlatformSpec, c fuzz.Continue) {
	c.FuzzNoCustom(in)
	roles := []AWSRoleCredentials{
		{
			ARN:       c.RandString(),
			Namespace: "openshift-image-registry",
			Name:      "installer-cloud-credentials",
		},
		{
			ARN:       c.RandString(),
			Namespace: "openshift-ingress-operator",
			Name:      "cloud-credentials",
		},
		{
			ARN:       c.RandString(),
			Namespace: "openshift-cloud-network-config-controller",
			Name:      "cloud-credentials",
		},
		{
			ARN:       c.RandString(),
			Namespace: "openshift-cluster-csi-drivers",
			Name:      "ebs-cloud-credentials",
		},
	}
	sort.SliceStable(roles, func(i, j int) bool {
		return roles[i].Namespace < roles[j].Namespace
	})
	in.Roles = roles
	in.KubeCloudControllerCreds = corev1.LocalObjectReference{
		Name: c.RandString(),
	}
	in.ControlPlaneOperatorCreds = corev1.LocalObjectReference{
		Name: c.RandString(),
	}
	in.NodePoolManagementCreds = corev1.LocalObjectReference{
		Name: c.RandString(),
	}
	in.RolesRef = AWSRolesRef{}
}

func hcpFuzzer(in *HostedControlPlane, c fuzz.Continue) {
	c.FuzzNoCustom(in)
	in.Spec.ServiceCIDR = in.Spec.Networking.ServiceCIDR
	in.Spec.PodCIDR = in.Spec.Networking.PodCIDR
	in.Spec.MachineCIDR = in.Spec.Networking.MachineCIDR
	in.Spec.NetworkType = in.Spec.Networking.NetworkType
	if in.Spec.Networking.APIServer != nil {
		in.Spec.APIPort = in.Spec.Networking.APIServer.Port
		in.Spec.APIAdvertiseAddress = in.Spec.Networking.APIServer.AdvertiseAddress
		in.Spec.APIAllowedCIDRBlocks = in.Spec.Networking.APIServer.AllowedCIDRBlocks
	} else {
		in.Spec.APIPort = nil
		in.Spec.APIAdvertiseAddress = nil
		in.Spec.APIAllowedCIDRBlocks = nil
	}
}

func awsEndpointServiceFuzzer(in *AWSEndpointService, c fuzz.Continue) {
	c.FuzzNoCustom(in)
	in.Status.DNSName = ""
}

func nodePoolFuzzer(in *NodePool, c fuzz.Continue) {
	c.FuzzNoCustom(in)
	in.Spec.NodeCount = nil
}

func fixupHostedCluster(in conversion.Convertible) {
	removeTypeMeta(in)
	hc, ok := in.(*HostedCluster)
	if !ok {
		panic(fmt.Sprintf("unexpected convertible type: %T", in))
	}
	if hc.Spec.Configuration != nil {
		err := populateDeprecatedGlobalConfig(hc.Spec.Configuration)
		if err != nil {
			panic(err.Error())
		}
	}
	if hc.Spec.Platform.AWS != nil {
		populateDeprecatedAWSRoles(hc.Spec.Platform.AWS)
		hc.Spec.Platform.AWS.RolesRef = AWSRolesRef{}
	}
	if hc.Spec.SecretEncryption.KMS != nil && hc.Spec.SecretEncryption.KMS.AWS != nil {
		hc.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN = ""
	}
	populateDeprecatedNetworkingFields(&hc.Spec.Networking)
}

func fixupHostedControlPlane(in conversion.Convertible) {
	removeTypeMeta(in)
	hcp, ok := in.(*HostedControlPlane)
	if !ok {
		panic(fmt.Sprintf("unexpected convertible type: %T", in))
	}
	if hcp.Spec.Configuration != nil {
		for i, item := range hcp.Spec.Configuration.Items {
			resource, _, err := serializer.Decode(item.Raw, nil, nil)
			if err != nil {
				panic(err.Error())
			}
			hcp.Spec.Configuration.Items[i].Raw = serializeResource(resource)
		}
	}
	if hcp.Spec.Platform.AWS != nil {
		hcp.Spec.Platform.AWS.RolesRef = AWSRolesRef{}
		roles := hcp.Spec.Platform.AWS.Roles
		sort.SliceStable(roles, func(i, j int) bool {
			return roles[i].Namespace < roles[j].Namespace
		})
		hcp.Spec.Platform.AWS.Roles = roles
	}

	if hcp.Spec.SecretEncryption.KMS != nil && hcp.Spec.SecretEncryption.KMS.AWS != nil {
		hcp.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN = ""
	}
}

func hostedClusterFuzzerFuncs(_ runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		awsRolesRefFuzzer,
		secretEncryptionFuzzer,
		configFuzzer,
		networkingFuzzer,
		v1beta1ConfigFuzzer,
		v1beta1NetworkingFuzzer,
	}
}

func hostedControlPlaneFuzzerFuncs(_ runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		hcpFuzzer,
		awsRolesRefFuzzer,
		secretEncryptionFuzzer,
		configFuzzer,
		networkingFuzzer,
		v1beta1NetworkingFuzzer,
		v1beta1ConfigFuzzer,
	}
}

func awsEndpointServiceFuzzerFuncs(_ runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		awsEndpointServiceFuzzer,
	}
}

func NodePoolFuzzerFuncs(_ runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		nodePoolFuzzer,
	}
}

func removeTypeMeta(in conversion.Convertible) {
	in.GetObjectKind().SetGroupVersionKind(schema.GroupVersionKind{})
}

func removeHubTypeMeta(in conversion.Hub) {
	in.GetObjectKind().SetGroupVersionKind(schema.GroupVersionKind{})
}

func TestFuzzyConversion(t *testing.T) {
	t.Run("for HostedCluster", conversiontest.FuzzTestFunc(conversiontest.FuzzTestFuncInput{
		Hub:                &v1beta1.HostedCluster{},
		HubAfterMutation:   removeHubTypeMeta,
		Spoke:              &HostedCluster{},
		SpokeAfterMutation: fixupHostedCluster,
		FuzzerFuncs:        []fuzzer.FuzzerFuncs{hostedClusterFuzzerFuncs},
		Scheme:             localScheme,
	}))
	t.Run("for NodePool", conversiontest.FuzzTestFunc(conversiontest.FuzzTestFuncInput{
		Hub:                &v1beta1.NodePool{},
		HubAfterMutation:   removeHubTypeMeta,
		Spoke:              &NodePool{},
		SpokeAfterMutation: removeTypeMeta,
		FuzzerFuncs:        []fuzzer.FuzzerFuncs{NodePoolFuzzerFuncs},
	}))
	t.Run("for HostedControlPlane", conversiontest.FuzzTestFunc(conversiontest.FuzzTestFuncInput{
		Hub:                &v1beta1.HostedControlPlane{},
		HubAfterMutation:   removeHubTypeMeta,
		Spoke:              &HostedControlPlane{},
		SpokeAfterMutation: fixupHostedControlPlane,
		FuzzerFuncs:        []fuzzer.FuzzerFuncs{hostedControlPlaneFuzzerFuncs},
	}))
	t.Run("for AWSEndpointService", conversiontest.FuzzTestFunc(conversiontest.FuzzTestFuncInput{
		Hub:                &v1beta1.AWSEndpointService{},
		HubAfterMutation:   removeHubTypeMeta,
		Spoke:              &AWSEndpointService{},
		SpokeAfterMutation: removeTypeMeta,
		FuzzerFuncs:        []fuzzer.FuzzerFuncs{awsEndpointServiceFuzzerFuncs},
	}))
}

func TestConfigurationFieldsToRawExtensions(t *testing.T) {
	config := &ClusterConfiguration{
		Ingress: &configv1.IngressSpec{Domain: "example.com"},
		Proxy:   &configv1.ProxySpec{HTTPProxy: "http://10.0.136.57:3128", HTTPSProxy: "http://10.0.136.57:3128"},
	}
	result, err := configurationFieldsToRawExtensions(config)
	if err != nil {
		t.Fatalf("configurationFieldsToRawExtensions: %v", err)
	}

	// Check that serialized resources do not contain a status section
	for i, rawExt := range result {
		unstructuredObj := &unstructured.Unstructured{}
		_, _, err := unstructured.UnstructuredJSONScheme.Decode(rawExt.Raw, nil, unstructuredObj)
		if err != nil {
			t.Fatalf("unexpected decode error: %v", err)
		}
		_, exists, err := unstructured.NestedFieldNoCopy(unstructuredObj.Object, "status")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exists {
			t.Errorf("status field exists for resource %d", i)
		}
	}

	serialized, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var roundtripped []runtime.RawExtension
	if err := json.Unmarshal(serialized, &roundtripped); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	// CreateOrUpdate does a naive DeepEqual which can not deal with custom unmarshallers, so make
	// sure the output matches a roundtripped result.
	if diff := cmp.Diff(result, roundtripped); diff != "" {
		t.Errorf("output does not match a json-roundtripped version: %s", diff)
	}

	var ingress configv1.Ingress
	if err := json.Unmarshal(result[0].Raw, &ingress); err != nil {
		t.Fatalf("failed to unmarshal raw data: %v", err)
	}
	if ingress.APIVersion == "" || ingress.Kind == "" {
		t.Errorf("rawObject has no apiVersion or kind set: %+v", ingress.ObjectMeta)
	}
	if ingress.Spec.Domain != "example.com" {
		t.Errorf("ingress does not have expected domain: %q", ingress.Spec.Domain)
	}

	var proxy configv1.Proxy
	if err := json.Unmarshal(result[1].Raw, &proxy); err != nil {
		t.Fatalf("failed to unmarshal raw data: %v", err)
	}
	if proxy.APIVersion == "" || proxy.Kind == "" {
		t.Errorf("rawObject has no apiVersion or kind set: %+v", proxy.ObjectMeta)
	}
	if proxy.Spec.HTTPProxy != "http://10.0.136.57:3128" {
		t.Errorf("proxy does not have expected HTTPProxy: %q", proxy.Spec.HTTPProxy)
	}

}
