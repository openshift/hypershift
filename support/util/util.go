package util

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	kubeclient "k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	// DebugDeploymentsAnnotation contains a comma separated list of deployment names which should always be scaled to 0
	// for development.
	DebugDeploymentsAnnotation               = "hypershift.openshift.io/debug-deployments"
	EnableHostedClustersAnnotationScopingEnv = "ENABLE_HOSTEDCLUSTERS_ANNOTATION_SCOPING"
	HostedClustersScopeAnnotationEnv         = "HOSTEDCLUSTERS_SCOPE_ANNOTATION"
	HostedClustersScopeAnnotation            = "hypershift.openshift.io/scope"
	HostedClusterAnnotation                  = "hypershift.openshift.io/cluster"
)

// ParseNamespacedName expects a string with the format "namespace/name"
// and returns the proper types.NamespacedName.
// This is useful when watching a CR annotated with the format above to requeue the CR
// described in the annotation.
func ParseNamespacedName(name string) types.NamespacedName {
	parts := strings.SplitN(name, string(types.Separator), 2)
	if len(parts) > 1 {
		return types.NamespacedName{Namespace: parts[0], Name: parts[1]}
	}
	return types.NamespacedName{Name: parts[0]}
}

// CopyConfigMap copies the .Data field of configMap `source` into configmap `cm`
func CopyConfigMap(cm, source *corev1.ConfigMap) {
	cm.Data = map[string]string{}
	for k, v := range source.Data {
		cm.Data[k] = v
	}
}

func DeleteIfNeeded(ctx context.Context, c client.Client, o client.Object) (exists bool, err error) {
	if err := c.Get(ctx, client.ObjectKeyFromObject(o), o); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("error getting %T: %w", o, err)
	}
	if o.GetDeletionTimestamp() != nil {
		return true, nil
	}
	if err := c.Delete(ctx, o); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("error deleting %T: %w", o, err)
	}

	return true, nil
}

func HCPControlPlaneReleaseImage(hcp *hyperv1.HostedControlPlane) string {
	if hcp.Spec.ControlPlaneReleaseImage != nil {
		return *hcp.Spec.ControlPlaneReleaseImage
	}
	return hcp.Spec.ReleaseImage
}

// CompressAndEncode compresses and base-64 encodes a given byte array. Ideal for loading an
// arbitrary byte array into a ConfigMap or Secret.
func CompressAndEncode(payload []byte) (*bytes.Buffer, error) {
	out := bytes.NewBuffer(nil)

	if len(payload) == 0 {
		return out, nil
	}

	// We need to base64-encode our gzipped data, so we can marshal it in and out
	// of a string since ConfigMaps and Secrets expect a textual representation.
	base64Enc := base64.NewEncoder(base64.StdEncoding, out)
	defer base64Enc.Close()

	err := compress(bytes.NewBuffer(payload), base64Enc)
	if err != nil {
		return nil, fmt.Errorf("could not compress and encode payload: %w", err)
	}

	err = base64Enc.Close()
	if err != nil {
		return nil, fmt.Errorf("could not close base64 encoder: %w", err)
	}

	return out, err
}

// Compress compresses a given byte array.
func Compress(payload []byte) (*bytes.Buffer, error) {
	in := bytes.NewBuffer(payload)
	out := bytes.NewBuffer(nil)

	if len(payload) == 0 {
		return out, nil
	}

	err := compress(in, out)
	return out, err
}

// DecodeAndDecompress decompresses and base-64 decodes a given byte array. Ideal for consuming a
// gzipped / base64-encoded byte array from a ConfigMap or Secret.
func DecodeAndDecompress(payload []byte) (*bytes.Buffer, error) {
	if len(payload) == 0 {
		return bytes.NewBuffer(nil), nil
	}

	base64Dec := base64.NewDecoder(base64.StdEncoding, bytes.NewReader(payload))

	return decompress(base64Dec)
}

// Compresses a given io.Reader to a given io.Writer
func compress(r io.Reader, w io.Writer) error {
	gz, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return fmt.Errorf("could not initialize gzip writer: %w", err)
	}

	defer gz.Close()

	if _, err := io.Copy(gz, r); err != nil {
		return fmt.Errorf("could not compress payload: %w", err)
	}

	if err := gz.Close(); err != nil {
		return fmt.Errorf("could not close gzipwriter: %w", err)
	}

	return nil
}

// Decompresses a given io.Reader.
func decompress(r io.Reader) (*bytes.Buffer, error) {
	gz, err := gzip.NewReader(r)

	if err != nil {
		return bytes.NewBuffer(nil), fmt.Errorf("could not initialize gzip reader: %w", err)
	}

	defer gz.Close()

	data, err := io.ReadAll(gz)
	if err != nil {
		return bytes.NewBuffer(nil), fmt.Errorf("could not decompress payload: %w", err)
	}

	return bytes.NewBuffer(data), nil
}

// ResolveDNSHostname receives a hostname string and tries to resolve it.
// Returns error if the host can't be resolved.
func ResolveDNSHostname(ctx context.Context, hostName string) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	ips, err := net.DefaultResolver.LookupIPAddr(timeoutCtx, hostName)
	if err == nil && len(ips) == 0 {
		err = fmt.Errorf("couldn't resolve %s", hostName)
	}

	return err
}

// InsecureHTTPClient return a http.Client which skips server certificate verification
func InsecureHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
}

// HashSimple takes a value, typically a string, and returns a 32-bit FNV-1a hashed version of the value as a string
func HashSimple(o interface{}) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(fmt.Sprintf("%v", o)))
	intHash := hash.Sum32()
	return fmt.Sprintf("%08x", intHash)
}

// HashStruct takes a struct and returns a 32-bit FNV-1a hashed version of the struct as a string
// The struct is first marshalled to JSON before hashing
func HashStruct(data interface{}) (string, error) {
	hash := fnv.New32a()
	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	_, err = hash.Write(jsonData)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%08x", hash.Sum32()), nil
}

// ConvertRegistryOverridesToCommandLineFlag converts a map of registry sources and their mirrors into a string
func ConvertRegistryOverridesToCommandLineFlag(registryOverrides map[string]string) string {
	var commandLineFlagArray []string
	for registrySource, registryReplacement := range registryOverrides {
		commandLineFlagArray = append(commandLineFlagArray, fmt.Sprintf("%s=%s", registrySource, registryReplacement))
	}
	if len(commandLineFlagArray) > 0 {
		sort.Strings(commandLineFlagArray)
		return strings.Join(commandLineFlagArray, ",")
	}
	// this is the equivalent of null on a StringToString command line variable.
	return "="
}

// ConvertOpenShiftImageRegistryOverridesToCommandLineFlag converts a map of image registry sources and their mirrors into a string
func ConvertOpenShiftImageRegistryOverridesToCommandLineFlag(registryOverrides map[string][]string) string {
	var commandLineFlagArray []string
	var sortedRegistrySources []string

	for k := range registryOverrides {
		sortedRegistrySources = append(sortedRegistrySources, k)
	}
	sort.Strings(sortedRegistrySources)

	for _, registrySource := range sortedRegistrySources {
		registryReplacements := registryOverrides[registrySource]
		for _, registryReplacement := range registryReplacements {
			commandLineFlagArray = append(commandLineFlagArray, fmt.Sprintf("%s=%s", registrySource, registryReplacement))
		}
	}
	if len(commandLineFlagArray) > 0 {
		return strings.Join(commandLineFlagArray, ",")
	}
	// this is the equivalent of null on a StringToString command line variable.
	return "="
}

// ConvertImageRegistryOverrideStringToMap translates the environment variable containing registry source to mirror
// mappings back to a map[string]string structure that can be ingested by the registry image content policies release provider
func ConvertImageRegistryOverrideStringToMap(envVar string) map[string][]string {
	registryMirrorPair := strings.Split(envVar, ",")

	if (len(registryMirrorPair) == 1 && registryMirrorPair[0] == "") || envVar == "=" {
		return nil
	}

	imageRegistryOverrides := make(map[string][]string)

	for _, pair := range registryMirrorPair {
		registryMirror := strings.SplitN(pair, "=", 2)
		if len(registryMirror) != 2 {
			continue
		}
		registry := registryMirror[0]
		mirror := registryMirror[1]

		imageRegistryOverrides[registry] = append(imageRegistryOverrides[registry], mirror)
	}

	return imageRegistryOverrides
}

// IsIPv4 function parse the CIDR and get the IPNet struct if the IPNet.IP cannot be converted to 4bytes format,
// the function returns nil, if it's an IPv6 it will return nil.
func IsIPv4(cidr string) (bool, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return false, fmt.Errorf("error validating the incoming CIDR %s: %v", cidr, err)
	}

	if ipnet.IP.To4() != nil {
		return true, nil
	} else {
		return false, nil
	}
}

// FirstUsableIP returns the first usable IP in both, IPv4 and IPv6 stacks.
func FirstUsableIP(cidr string) (string, error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("error validating the incoming CIDR %s: %w", cidr, err)
	}
	ip := ipNet.IP
	ip[len(ipNet.IP)-1]++
	return ip.String(), nil
}

// ParseNodeSelector parses a comma separated string of key=value pairs into a map
func ParseNodeSelector(str string) map[string]string {
	if len(str) == 0 {
		return nil
	}
	parts := strings.Split(str, ",")
	result := make(map[string]string)
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		if len(kv[0]) == 0 || len(kv[1]) == 0 {
			continue
		}
		result[kv[0]] = kv[1]
	}
	return result
}

func ApplyAWSLoadBalancerSubnetsAnnotation(svc *corev1.Service, hcp *hyperv1.HostedControlPlane) {
	if hcp.Spec.Platform.Type != hyperv1.AWSPlatform {
		return
	}
	if svc.Annotations == nil {
		svc.Annotations = make(map[string]string)
	}
	subnets, ok := hcp.Annotations[hyperv1.AWSLoadBalancerSubnetsAnnotation]
	if ok {
		svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-subnets"] = subnets
	}
}

func DoesMgmtClusterAndNodePoolCPUArchMatch(mgmtClusterCPUArch, nodePoolArch string) error {
	if mgmtClusterCPUArch != nodePoolArch {
		return fmt.Errorf("multi-arch hosted cluster is not enabled and management cluster and nodepool cpu arches do not match - management cluster cpu arch: %s, nodepool cpu arch: %s", mgmtClusterCPUArch, nodePoolArch)
	}

	return nil
}

func GetMgmtClusterCPUArch(ctx context.Context) (string, error) {
	cfg, err := util.GetConfig()
	if err != nil {
		return "", err
	}

	kc, err := kubeclient.NewForConfig(cfg)
	if err != nil {
		return "", err
	}

	// Get the API version in JSON format
	versionJSON, err := kc.RESTClient().Get().AbsPath("/version").DoRaw(ctx)
	if err != nil {
		return "", err
	}

	// Unmarshal the version JSON so we can extract the platform field
	var data map[string]interface{}
	if err = json.Unmarshal(versionJSON, &data); err != nil {
		return "", err
	}

	//Extract the platform field
	platform, ok := data["platform"].(string)
	if !ok {
		return "", fmt.Errorf("failed to extract the platform info from the version JSON")
	}

	// Split the platform into separate strings, we just want to check the CPU arch
	// The normal structure should be something like 'linux/arm64'
	platformParts := strings.Split(platform, "/")

	// Check we have two parts, so we don't do a nil dereference though this shouldn't happen
	if len(platformParts) != 2 {
		return "", fmt.Errorf("failed to extract the cpu arch from the platform field")
	}

	return platformParts[1], nil
}

// PredicatesForHostedClusterAnnotationScoping returns predicate filters for all event types that will ignore incoming
// event requests for hostedcluster resources that do not match the "scope" annotation
// specified in the HOSTEDCLUSTERS_SCOPE_ANNOTATION env var.  If not defined or empty, the default behavior is to accept all events for hostedclusters that do not have the annotation.
// The ENABLE_HOSTEDCLUSTERS_ANNOTATION_SCOPING env var must also be set to "true" to enable the scoping feature.
func PredicatesForHostedClusterAnnotationScoping() predicate.Predicate {
	hcAnnotationScopingEnabledEnvVal := os.Getenv(EnableHostedClustersAnnotationScopingEnv)
	hcScopeAnnotationEnvVal := os.Getenv(HostedClustersScopeAnnotationEnv)
	filter := func(obj client.Object) bool {
		if hcAnnotationScopingEnabledEnvVal != "true" {
			return true
		}
		hostedClusterScopeAnnotation := ""
		if obj.GetAnnotations() != nil {
			hostedClusterScopeAnnotation = obj.GetAnnotations()[HostedClustersScopeAnnotation]
		}
		if hostedClusterScopeAnnotation == "" && hcScopeAnnotationEnvVal == "" {
			return true
		}
		if hostedClusterScopeAnnotation != hcScopeAnnotationEnvVal {
			return false // ignore event; the hostedcluster has a scope annotation that does not match what is defined in HOSTEDCLUSTERS_SCOPE_ANNOTATION
		}
		return true
	}
	return predicate.NewPredicateFuncs(filter)
}

// PredicatesForHostedClusterChildResourcesAnnotationScoping returns predicate filters for all event types that will ignore incoming
// event requests for resources in which the parent hostedcluster does not
// match the "scope" annotation specified in the HOSTEDCLUSTERS_SCOPE_ANNOTATION env var.  If not defined or empty, the
// default behavior is to accept all events for hostedclusters that do not have the annotation.
// The ENABLE_HOSTEDCLUSTERS_ANNOTATION_SCOPING env var must also be set to "true" to enable the scoping feature.
func PredicatesForHostedClusterChildResourcesAnnotationScoping(r client.Reader) predicate.Predicate {
	hcAnnotationScopingEnabledEnvVal := os.Getenv(EnableHostedClustersAnnotationScopingEnv)
	hcScopeAnnotationEnvVal := os.Getenv(HostedClustersScopeAnnotationEnv)
	filter := func(obj client.Object) bool {
		if hcAnnotationScopingEnabledEnvVal != "true" {
			return true
		}
		hostedClusterName := ""
		if obj.GetAnnotations() != nil {
			hostedClusterName = obj.GetAnnotations()[HostedClusterAnnotation]
		}
		if hostedClusterName == "" {
			return true
		}
		namespacedName := ParseNamespacedName(hostedClusterName)
		hcluster := &hyperv1.HostedCluster{}
		err := r.Get(context.Background(), namespacedName, hcluster)
		if err != nil {
			return true
		}
		hostedClusterScopeAnnotation := ""
		if hcluster.GetAnnotations() != nil {
			hostedClusterScopeAnnotation = hcluster.GetAnnotations()[HostedClustersScopeAnnotation]
		}
		if hostedClusterScopeAnnotation == "" && hcScopeAnnotationEnvVal == "" {
			return true
		}
		if hostedClusterScopeAnnotation != hcScopeAnnotationEnvVal {
			return false // ignore event; the parent hostedcluster's scope annotation does not match what is defined in HOSTEDCLUSTERS_SCOPE_ANNOTATION
		}
		return true
	}
	return predicate.NewPredicateFuncs(filter)
}

// PredicatesForNodepoolAnnotationScoping returns predicate filters for all event types that will ignore incoming
// event requests for nodepool resources in which their owning hostedcluster resource doesn't have a scope annotation
// that matches what is specified in the HOSTEDCLUSTERS_SCOPE_ANNOTATION env var.  If not defined or empty, the default behavior
// is to accept all nodepool events in which the owning hostedcluster resource does not have a corresponding scope annotation defined.
// The ENABLE_HOSTEDCLUSTERS_ANNOTATION_SCOPING env var must also be set to "true" to enable the scoping feature.
func PredicatesForNodepoolAnnotationScoping(r client.Reader) predicate.Predicate {
	hcAnnotationScopingEnabledEnvVal := os.Getenv(EnableHostedClustersAnnotationScopingEnv)
	hcScopeAnnotationEnvVal := os.Getenv(HostedClustersScopeAnnotationEnv)
	filter := func(obj client.Object) bool {
		if hcAnnotationScopingEnabledEnvVal != "true" {
			return true
		}

		np, ok := obj.(*hyperv1.NodePool)
		if !ok {
			return true
		}

		// use the Cluster Name from the nodepool spec to get the owning hostedcluster object
		hostedClusterName := ""
		if np.Spec.ClusterName != "" {
			hostedClusterName = np.Spec.ClusterName
		}
		if hostedClusterName == "" {
			return true
		}
		namespacedName := ParseNamespacedName(fmt.Sprintf("%s/%s", np.Namespace, hostedClusterName))
		hcluster := &hyperv1.HostedCluster{}
		err := r.Get(context.Background(), namespacedName, hcluster)
		if err != nil {
			return true
		}

		hostedClusterScopeAnnotation := ""
		if hcluster.GetAnnotations() != nil {
			hostedClusterScopeAnnotation = hcluster.GetAnnotations()[HostedClustersScopeAnnotation]
		}
		if hostedClusterScopeAnnotation == "" && hcScopeAnnotationEnvVal == "" {
			return true
		}
		if hostedClusterScopeAnnotation != hcScopeAnnotationEnvVal {
			return false // ignore event; the associated hostedcluster's scope annotation does not match what is defined in HOSTEDCLUSTERS_SCOPE_ANNOTATION
		}
		return true
	}
	return predicate.NewPredicateFuncs(filter)
}

// PredicatesForNodepoolChildResourcesAnnotationScoping returns predicate filters for all event types that will ignore incoming
// event requests for resources in which the parent hostedcluster does not
// match the "scope" annotation specified in the HOSTEDCLUSTERS_SCOPE_ANNOTATION env var.  If not defined or empty, the
// default behavior is to accept all events for hostedclusters that do not have the annotation.
// The ENABLE_HOSTEDCLUSTERS_ANNOTATION_SCOPING env var must also be set to "true" to enable the scoping feature.
func PredicatesForNodepoolChildResourcesAnnotationScoping(r client.Reader) predicate.Predicate {
	hcAnnotationScopingEnabledEnvVal := os.Getenv(EnableHostedClustersAnnotationScopingEnv)
	hcScopeAnnotationEnvVal := os.Getenv(HostedClustersScopeAnnotationEnv)
	filter := func(obj client.Object) bool {
		if hcAnnotationScopingEnabledEnvVal != "true" {
			return true
		}

		// use the object's "nodePool" annotation to retrieve the parent nodepool object
		nodePoolName := ""
		if obj.GetAnnotations() != nil {
			nodePoolName = obj.GetAnnotations()["hypershift.openshift.io/nodePool"]
		}
		if nodePoolName == "" {
			return true
		}
		namespacedName := ParseNamespacedName(nodePoolName)
		np := &hyperv1.NodePool{}
		err := r.Get(context.Background(), namespacedName, np)
		if err != nil {
			return true
		}

		// use the Cluster Name from the nodepool spec to get the owning hostedcluster object
		hostedClusterName := ""
		if np.Spec.ClusterName != "" {
			hostedClusterName = np.Spec.ClusterName
		}
		if hostedClusterName == "" {
			return true
		}
		namespacedName = ParseNamespacedName(fmt.Sprintf("%s/%s", np.Namespace, hostedClusterName))
		hcluster := &hyperv1.HostedCluster{}
		err = r.Get(context.Background(), namespacedName, hcluster)
		if err != nil {
			return true
		}

		hostedClusterScopeAnnotation := ""
		if hcluster.GetAnnotations() != nil {
			hostedClusterScopeAnnotation = hcluster.GetAnnotations()[HostedClustersScopeAnnotation]
		}
		if hostedClusterScopeAnnotation == "" && hcScopeAnnotationEnvVal == "" {
			return true
		}
		if hostedClusterScopeAnnotation != hcScopeAnnotationEnvVal {
			return false // ignore event; the associated hostedcluster's scope annotation does not match what is defined in HOSTEDCLUSTERS_SCOPE_ANNOTATION
		}
		return true
	}
	return predicate.NewPredicateFuncs(filter)
}
