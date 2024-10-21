package conversion

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
	hyperv1alpha1 "github.com/openshift/hypershift/api/hypershift/v1alpha1"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/configrefs"
	"github.com/openshift/hypershift/api/util/ipnet"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/apitesting/fuzzer"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
)

func networkingFuzzer(in *hyperv1alpha1.ClusterNetworking, c fuzz.Continue) {
	c.FuzzNoCustom(in)
	in.MachineCIDR = "10.10.100.0/24"
	in.PodCIDR = "10.10.101.0/24"
	in.ServiceCIDR = "10.10.102.0/24"
	in.MachineNetwork = []hyperv1alpha1.MachineNetworkEntry{
		{
			CIDR: mustParseCIDR(in.MachineCIDR),
		},
	}
	in.ClusterNetwork = []hyperv1alpha1.ClusterNetworkEntry{
		{
			CIDR: mustParseCIDR(in.PodCIDR),
		},
	}
	in.ServiceNetwork = []hyperv1alpha1.ServiceNetworkEntry{
		{
			CIDR: mustParseCIDR(in.ServiceCIDR),
		},
	}
}

func v1beta1NetworkingFuzzer(in *hyperv1beta1.ClusterNetworking, c fuzz.Continue) {
	in.ClusterNetwork = []hyperv1beta1.ClusterNetworkEntry{{CIDR: mustParseCIDR("10.11.100.0/24")}}
	in.MachineNetwork = []hyperv1beta1.MachineNetworkEntry{{CIDR: mustParseCIDR("10.11.101.0/24")}}
	in.ServiceNetwork = []hyperv1beta1.ServiceNetworkEntry{{CIDR: mustParseCIDR("10.11.102.0/24")}}
}

func mustParseCIDR(str string) ipnet.IPNet {
	result, err := ipnet.ParseCIDR(str)
	if err != nil {
		panic(err.Error())
	}
	return *result
}

func v1beta1ConfigFuzzer(in *hyperv1beta1.ClusterConfiguration, c fuzz.Continue) {
	c.FuzzNoCustom(in)
	if in.APIServer != nil && in.APIServer.Audit.Profile == "" {
		in.APIServer.Audit.Profile = configv1.DefaultAuditProfileType
	}
}

func configFuzzer(in *hyperv1alpha1.ClusterConfiguration, c fuzz.Continue) {
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
	err = localSerializer.Encode(obj, b)
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

func secretEncryptionFuzzer(in *hyperv1alpha1.SecretEncryptionSpec, c fuzz.Continue) {
	c.FuzzNoCustom(in)
	in.KMS = &hyperv1alpha1.KMSSpec{
		Provider: hyperv1alpha1.AWS,
		AWS: &hyperv1alpha1.AWSKMSSpec{
			Region: "",
			ActiveKey: hyperv1alpha1.AWSKMSKeyEntry{
				ARN: c.RandString(),
			},
			BackupKey: nil,
			Auth: hyperv1alpha1.AWSKMSAuthSpec{
				Credentials: corev1.LocalObjectReference{
					Name: c.RandString(),
				},
			},
		},
	}
}

func awsRolesRefFuzzer(in *hyperv1alpha1.AWSPlatformSpec, c fuzz.Continue) {
	c.FuzzNoCustom(in)
	roles := []hyperv1alpha1.AWSRoleCredentials{
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
	in.RolesRef = hyperv1alpha1.AWSRolesRef{}
}

func hcpFuzzer(in *hyperv1alpha1.HostedControlPlane, c fuzz.Continue) {
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

func awsEndpointServiceFuzzer(in *hyperv1alpha1.AWSEndpointService, c fuzz.Continue) {
	c.FuzzNoCustom(in)
	in.Status.DNSName = ""
}

func nodePoolFuzzer(in *hyperv1alpha1.NodePool, c fuzz.Continue) {
	c.FuzzNoCustom(in)
	in.Spec.NodeCount = nil
}

func fixupHostedCluster(in runtime.Object) {
	removeTypeMeta(in)
	hc, ok := in.(*hyperv1alpha1.HostedCluster)
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
		hc.Spec.Platform.AWS.RolesRef = hyperv1alpha1.AWSRolesRef{}
	}
	if hc.Spec.SecretEncryption.KMS != nil && hc.Spec.SecretEncryption.KMS.AWS != nil {
		hc.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN = ""
	}
	populateDeprecatedNetworkingFields(&hc.Spec.Networking)
}

func fixupHostedControlPlane(in runtime.Object) {
	removeTypeMeta(in)
	hcp, ok := in.(*hyperv1alpha1.HostedControlPlane)
	if !ok {
		panic(fmt.Sprintf("unexpected convertible type: %T", in))
	}
	if hcp.Spec.Configuration != nil {
		for i, item := range hcp.Spec.Configuration.Items {
			resource, _, err := localSerializer.Decode(item.Raw, nil, nil)
			if err != nil {
				panic(err.Error())
			}
			hcp.Spec.Configuration.Items[i].Raw = serializeResource(resource)
		}
	}
	if hcp.Spec.Platform.AWS != nil {
		hcp.Spec.Platform.AWS.RolesRef = hyperv1alpha1.AWSRolesRef{}
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

func removeTypeMeta(in runtime.Object) {
	in.GetObjectKind().SetGroupVersionKind(schema.GroupVersionKind{})
}

func removeHubTypeMeta(in runtime.Object) {
	in.GetObjectKind().SetGroupVersionKind(schema.GroupVersionKind{})
}

func fixHostedControlPlaneAfterFuzz(in runtime.Object) {
	hc, ok := in.(*hyperv1beta1.HostedControlPlane)
	if !ok {
		panic(fmt.Sprintf("unexpected convertible type: %T", in))
	}
	// Given there is no OpenStack support on alpha we shouldn't fuzz
	// the beta object and expect it to be equal to the non existent
	// OpenStack support on alpha.
	if hc.Spec.Platform.OpenStack != nil {
		hc.Spec.Platform.OpenStack = nil
	}

	// There was no formal support for Azure on alpha so we shouldn't fuzz
	// the beta object and expect it to equal the alpha version.
	if hc.Spec.Platform.Azure != nil {
		hc.Spec.Platform.Azure = nil
	}

	// There was no formal support for Azure on alpha so we shouldn't fuzz
	// the beta object and expect it to equal the alpha version.
	if hc.Spec.SecretEncryption != nil && hc.Spec.SecretEncryption.KMS != nil && hc.Spec.SecretEncryption.KMS.Azure != nil {
		hc.Spec.SecretEncryption.KMS.Azure = nil
	}

	if hc.Spec.Platform.AWS != nil {
		hc.Spec.Platform.AWS.SharedVPC = nil
	}
}

func fixHosterClusterAfterFuzz(in runtime.Object) {
	hc, ok := in.(*hyperv1beta1.HostedCluster)
	if !ok {
		panic(fmt.Sprintf("unexpected convertible type: %T", in))
	}
	// Given there is no OpenStack support on alpha we shouldn't fuzz
	// the beta object and expect it to be equal to the non existent
	// OpenStack support on alpha.
	if hc.Spec.Platform.OpenStack != nil {
		hc.Spec.Platform.OpenStack = nil
	}

	// There was no formal support for Azure on alpha so we shouldn't fuzz
	// the beta object and expect it to equal the alpha version.
	if hc.Spec.Platform.Azure != nil {
		hc.Spec.Platform.Azure = nil
	}

	// There was no formal support for Azure on alpha so we shouldn't fuzz
	// the beta object and expect it to equal the alpha version.
	if hc.Spec.SecretEncryption != nil && hc.Spec.SecretEncryption.KMS != nil && hc.Spec.SecretEncryption.KMS.Azure != nil {
		hc.Spec.SecretEncryption.KMS.Azure = nil
	}

	hc.Status.PayloadArch = ""

	if hc.Spec.Platform.AWS != nil {
		hc.Spec.Platform.AWS.SharedVPC = nil
	}
}

func fixNodePoolAfterFuzz(in runtime.Object) {
	np, ok := in.(*hyperv1beta1.NodePool)
	if !ok {
		panic(fmt.Sprintf("unexpected convertible type: %T", in))
	}
	// Given there is no OpenStack support on alpha we shouldn't fuzz
	// the beta object and expect it to be equal to the non existent
	// OpenStack support on alpha.
	if np.Spec.Platform.OpenStack != nil {
		np.Spec.Platform.OpenStack = nil
	}

	// There was no formal support for Azure on alpha so we shouldn't fuzz
	// the beta object and expect it to equal the alpha version.
	if np.Spec.Platform.Azure != nil {
		np.Spec.Platform.Azure = nil
	}
}

func fixAWSEndpointServiceAfterFuzz(in runtime.Object) {
	epsvc, ok := in.(*hyperv1beta1.AWSEndpointService)
	if !ok {
		panic(fmt.Sprintf("unexpected convertible type: %T", in))
	}
	epsvc.Status.SecurityGroupID = ""
}

func TestFuzzyConversion(t *testing.T) {
	t.Run("for HostedCluster", FuzzTestFunc(FuzzTestFuncInput{
		Hub:                &hyperv1beta1.HostedCluster{},
		HubAfterMutation:   removeHubTypeMeta,
		Spoke:              &hyperv1alpha1.HostedCluster{},
		SpokeAfterMutation: fixupHostedCluster,
		FuzzerFuncs:        []fuzzer.FuzzerFuncs{hostedClusterFuzzerFuncs},
		Scheme:             localScheme,
		HubAfterFuzz:       fixHosterClusterAfterFuzz,
	}))
	t.Run("for NodePool", FuzzTestFunc(FuzzTestFuncInput{
		Hub:                &hyperv1beta1.NodePool{},
		HubAfterMutation:   removeHubTypeMeta,
		Spoke:              &hyperv1alpha1.NodePool{},
		SpokeAfterMutation: removeTypeMeta,
		FuzzerFuncs:        []fuzzer.FuzzerFuncs{NodePoolFuzzerFuncs},
		HubAfterFuzz:       fixNodePoolAfterFuzz,
	}))
	t.Run("for HostedControlPlane", FuzzTestFunc(FuzzTestFuncInput{
		Hub:                &hyperv1beta1.HostedControlPlane{},
		HubAfterMutation:   removeHubTypeMeta,
		Spoke:              &hyperv1alpha1.HostedControlPlane{},
		SpokeAfterMutation: fixupHostedControlPlane,
		FuzzerFuncs:        []fuzzer.FuzzerFuncs{hostedControlPlaneFuzzerFuncs},
		HubAfterFuzz:       fixHostedControlPlaneAfterFuzz,
	}))
	t.Run("for AWSEndpointService", FuzzTestFunc(FuzzTestFuncInput{
		Hub:                &hyperv1beta1.AWSEndpointService{},
		HubAfterMutation:   removeHubTypeMeta,
		Spoke:              &hyperv1alpha1.AWSEndpointService{},
		SpokeAfterMutation: removeTypeMeta,
		FuzzerFuncs:        []fuzzer.FuzzerFuncs{awsEndpointServiceFuzzerFuncs},
		HubAfterFuzz:       fixAWSEndpointServiceAfterFuzz,
	}))
}

func TestConfigurationFieldsToRawExtensions(t *testing.T) {
	config := &hyperv1alpha1.ClusterConfiguration{
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
