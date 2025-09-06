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
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cmdutil "github.com/openshift/hypershift/cmd/util"
	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/releaseinfo/registryclient"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/blang/semver"
	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
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

type JSONMapper func(jsonData []byte) []byte

// NewOmitFieldIfEmptyJSONMapper is a JSONMapper that omits the given field
// in case it was empty.
func NewOmitFieldIfEmptyJSONMapper(field string) JSONMapper {
	return func(data []byte) []byte {
		stringData := string(data)
		stringData = RemoveEmptyJSONField(stringData, field)
		return []byte(stringData)
	}
}

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

func UpdateObject[T client.Object](ctx context.Context, c client.Client, obj T, mutate func() error) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			return err
		}

		original := obj.DeepCopyObject().(T)
		if err := mutate(); err != nil {
			return err
		}

		return c.Patch(ctx, obj, client.MergeFrom(original))
	})
}

func DeleteIfNeededWithOptions(ctx context.Context, c client.Client, o client.Object, opts ...client.DeleteOption) (exists bool, err error) {
	if err := c.Get(ctx, client.ObjectKeyFromObject(o), o); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return false, nil
		}
		return false, fmt.Errorf("error getting %T: %w", o, err)
	}
	if o.GetDeletionTimestamp() != nil {
		return true, nil
	}
	if err := c.Delete(ctx, o, opts...); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("error deleting %T: %w", o, err)
	}

	return true, nil
}

func DeleteIfNeededWithPredicate[T client.Object](ctx context.Context, c client.Client, o T, predicate func(T) bool) (exists bool, err error) {
	if err := c.Get(ctx, client.ObjectKeyFromObject(o), o); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return false, nil
		}
		return false, fmt.Errorf("error getting %T: %w", o, err)
	}
	if o.GetDeletionTimestamp() != nil {
		return true, nil
	}
	if !predicate(o) {
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

func DeleteIfNeeded(ctx context.Context, c client.Client, o client.Object) (exists bool, err error) {
	return DeleteIfNeededWithOptions(ctx, c, o)
}

func HCControlPlaneReleaseImage(hcluster *hyperv1.HostedCluster) string {
	if hcluster.Spec.ControlPlaneRelease != nil {
		return hcluster.Spec.ControlPlaneRelease.Image
	}
	return hcluster.Spec.Release.Image
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
			Proxy: http.ProxyFromEnvironment,
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
// The struct is first marshaled to JSON before hashing
func HashStruct(data interface{}) (string, error) {
	return HashStructWithJSONMapper(data, nil)
}

// HashStructWithJSONMapper takes a struct and returns a 32-bit FNV-1a hashed version of the struct as a string after
// The struct is first marshaled to JSON before hashing. You can provide a JSONMapper that transforms the marshaled
// JSON before computing the hash or nil if no transformation is needed.
func HashStructWithJSONMapper(data interface{}, mapper JSONMapper) (string, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	if mapper != nil {
		jsonData = mapper(jsonData)
	}
	return HashBytes(jsonData)
}

// HashBytes takes a byte array and returns a 32-bit FNV-1a hashed version of the byte array as a string
func HashBytes(data []byte) (string, error) {
	hash := fnv.New32a()
	_, err := hash.Write(data)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%08x", hash.Sum32()), nil
}

// RemoveEmptyJSONField removes a field from a given JSON if it's empty regardless of its position
func RemoveEmptyJSONField(stringData string, field string) string {
	pattern := fmt.Sprintf(`,?\s*"%s":\s*""`, regexp.QuoteMeta(field)) // Safely interpolate
	re := regexp.MustCompile(pattern)
	// Replace occurrences
	stringData = re.ReplaceAllString(stringData, "")

	// Trim any remaining leading or trailing commas to keep JSON valid
	stringData = regexp.MustCompile(`\s*,\s*}`).ReplaceAllString(stringData, "}")
	stringData = regexp.MustCompile(`{\s*,\s*`).ReplaceAllString(stringData, "{")
	return stringData
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

		// Skip empty registry or mirror entries
		if registry == "" || mirror == "" {
			continue
		}

		imageRegistryOverrides[registry] = append(imageRegistryOverrides[registry], mirror)
	}

	return imageRegistryOverrides
}

// IsIPv4CIDR checks if the input string is an IPv4 CIDR.
func IsIPv4CIDR(input string) (bool, error) {
	_, ipnet, err := net.ParseCIDR(input)
	if err != nil {
		return false, fmt.Errorf("error parsing input '%s': not a valid CIDR", input)
	}
	if ipnet.IP.To4() != nil {
		return true, nil
	}
	return false, nil
}

// IsIPv4Address checks if the input string is an IPv4 address.
func IsIPv4Address(input string) (bool, error) {
	ip := net.ParseIP(input)
	if ip == nil {
		return false, fmt.Errorf("error parsing input '%s': not a valid IP address", input)
	}
	if ip.To4() != nil {
		return true, nil
	}
	return false, nil
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

func ApplyAWSLoadBalancerTargetNodesAnnotation(svc *corev1.Service, hcp *hyperv1.HostedControlPlane) {
	if hcp.Spec.Platform.Type != hyperv1.AWSPlatform {
		return
	}
	if svc.Annotations == nil {
		svc.Annotations = make(map[string]string)
	}
	selectors, ok := hcp.Annotations[hyperv1.AWSLoadBalancerTargetNodesAnnotation]
	if ok {
		svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-target-node-labels"] = selectors
	}
}

func GetKubeClientSet() (kubeclient.Interface, error) {
	cfg, err := cmdutil.GetConfig()
	if err != nil {
		return nil, err
	}

	kc, err := kubeclient.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	return kc, nil
}

func GetMgmtClusterCPUArch(kc kubeclient.Interface) (string, error) {
	info, err := kc.Discovery().ServerVersion()
	if err != nil {
		return "", err
	}

	platform := info.Platform

	// Split the platform into separate strings, we just want to check the CPU arch
	// The normal structure should be something like 'linux/arm64'
	platformParts := strings.Split(platform, "/")

	// Check we have two parts, so we don't do a nil dereference though this shouldn't happen
	if len(platformParts) != 2 {
		return "", fmt.Errorf("failed to extract the cpu arch from the platform field")
	}

	return platformParts[1], nil
}

// DetermineHostedClusterPayloadArch returns the HostedCluster payload's CPU architecture type
func DetermineHostedClusterPayloadArch(ctx context.Context, c client.Client, hc *hyperv1.HostedCluster, imageMetadataProvider ImageMetadataProvider) (hyperv1.PayloadArchType, error) {
	var pullSecret corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name}, &pullSecret); err != nil {
		return "", fmt.Errorf("failed to get pull secret: %w", err)
	}
	pullSecretBytes, ok := pullSecret.Data[corev1.DockerConfigJsonKey]
	if !ok {
		return "", fmt.Errorf("expected %s key in pull secret", corev1.DockerConfigJsonKey)
	}

	isMultiArchReleaseImage, err := registryclient.IsMultiArchManifestList(ctx, hc.Spec.Release.Image, pullSecretBytes, imageMetadataProvider)
	if err != nil {
		return "", fmt.Errorf("failed to determine if release image multi-arch: %w", err)
	}

	if isMultiArchReleaseImage {
		return hyperv1.Multi, nil
	}

	arch, err := getImageArchitecture(ctx, hc.Spec.Release.Image, pullSecretBytes, imageMetadataProvider)
	if err != nil {
		return "", err
	}
	return arch, nil
}

func getImageArchitecture(ctx context.Context, image string, pullSecretBytes []byte, imageMetadataProvider ImageMetadataProvider) (hyperv1.PayloadArchType, error) {
	imageMetadata, err := imageMetadataProvider.ImageMetadata(ctx, image, pullSecretBytes)
	if err != nil {
		return "", fmt.Errorf("failed to look up image metadata for %s: %w", image, err)
	}

	if imageMetadata != nil && len(imageMetadata.Architecture) > 0 {
		// Uppercase this value since it will be lowercase, but the API expects the arch to be in uppercase
		return hyperv1.ToPayloadArch(imageMetadata.Architecture), nil
	}

	return "", fmt.Errorf("failed to find image CPU architecture for %s", image)
}

// PredicatesForHostedClusterAnnotationScoping returns predicate filters for all event types that will ignore incoming
// event requests for resources in which the parent hostedcluster does not
// match the "scope" annotation specified in the HOSTEDCLUSTERS_SCOPE_ANNOTATION env var.  If not defined or empty, the
// default behavior is to accept all events for hostedclusters that do not have the annotation.
// The ENABLE_HOSTEDCLUSTERS_ANNOTATION_SCOPING env var must also be set to "true" to enable the scoping feature.
func PredicatesForHostedClusterAnnotationScoping(r client.Reader) predicate.Predicate {
	hcAnnotationScopingEnabledEnvVal := os.Getenv(EnableHostedClustersAnnotationScopingEnv)
	hcScopeAnnotationEnvVal := os.Getenv(HostedClustersScopeAnnotationEnv)
	filter := func(obj client.Object) bool {
		if hcAnnotationScopingEnabledEnvVal != "true" {
			return true // process event; the scoping feature has not been enabled via the ENABLE_HOSTEDCLUSTERS_ANNOTATION_SCOPING env var
		}
		hostedClusterScopeAnnotation := getHostedClusterScopeAnnotation(obj, r)
		if hostedClusterScopeAnnotation == "" && hcScopeAnnotationEnvVal == "" {
			return true // process event; both the operator's scope and hostedcluster's scope are empty
		}
		if hostedClusterScopeAnnotation != hcScopeAnnotationEnvVal {
			return false // ignore event; the associated hostedcluster's scope annotation does not match what is defined in HOSTEDCLUSTERS_SCOPE_ANNOTATION
		}
		return true
	}
	return predicate.NewPredicateFuncs(filter)
}

// getHostedClusterScopeAnnotation will extract the "scope" annotation from the hostedcluster resource that owns the specified object.
// Depending on the object type being passed in, slightly different paths will be used to ultimately retrieve the hostedcluster resource containing the annotation.
// If an annotation is not found, an empty string is returned.
func getHostedClusterScopeAnnotation(obj client.Object, r client.Reader) string {
	hostedClusterName := ""
	nodePoolName := ""
	switch obj := obj.(type) {
	case *hyperv1.HostedCluster:
		if obj.GetAnnotations() != nil {
			return obj.GetAnnotations()[HostedClustersScopeAnnotation]
		}
	case *hyperv1.NodePool:
		hostedClusterName = fmt.Sprintf("%s/%s", obj.Namespace, obj.Spec.ClusterName)
	default:
		if obj.GetAnnotations() != nil {
			nodePoolName = obj.GetAnnotations()["hypershift.openshift.io/nodePool"]
			hostedClusterName = obj.GetAnnotations()[HostedClusterAnnotation]
		}
		if nodePoolName != "" {
			namespacedName := ParseNamespacedName(nodePoolName)
			np := &hyperv1.NodePool{}
			err := r.Get(context.Background(), namespacedName, np)
			if err != nil {
				return ""
			}
			hostedClusterName = fmt.Sprintf("%s/%s", np.Namespace, np.Spec.ClusterName)
		}
	}
	if hostedClusterName == "" {
		return ""
	}
	namespacedName := ParseNamespacedName(hostedClusterName)
	hcluster := &hyperv1.HostedCluster{}
	err := r.Get(context.Background(), namespacedName, hcluster)
	if err != nil {
		return ""
	}
	if hcluster.GetAnnotations() != nil {
		return hcluster.GetAnnotations()[HostedClustersScopeAnnotation]
	}
	return ""
}

// SanitizeIgnitionPayload make sure the IgnitionPayload is valid
// and does not contain inconsistencies.
func SanitizeIgnitionPayload(payload []byte) error {
	var jsonPayload ignitionapi.Config

	if err := json.Unmarshal(payload, &jsonPayload); err != nil {
		return fmt.Errorf("error unmarshalling Ignition payload: %v", err)
	}

	return nil
}

func GetPullSecretBytes(ctx context.Context, c client.Client, hc *hyperv1.HostedCluster) ([]byte, error) {
	pullSecret := corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name}, &pullSecret); err != nil {
		return nil, fmt.Errorf("failed to get pull secret: %w", err)
	}

	pullSecretBytes, ok := pullSecret.Data[corev1.DockerConfigJsonKey]
	if !ok {
		return nil, fmt.Errorf("expected %s key in pull secret", corev1.DockerConfigJsonKey)
	}

	return pullSecretBytes, nil
}

// GetControlPlaneOperatorImage resolves the appropriate control plane operator
// image based on the following order of precedence (from most to least
// preferred):
//
//  1. The image specified by the ControlPlaneOperatorImageAnnotation on the
//     HostedCluster resource itself
//  2. The hypershift image specified in the release payload indicated by the
//     HostedCluster's release field
//  3. The hypershift-operator's own image for release versions 4.9 and 4.10
//  4. The registry.ci.openshift.org/hypershift/hypershift:4.8 image for release
//     version 4.8
//
// If no image can be found according to these rules, an error is returned.
func GetControlPlaneOperatorImage(ctx context.Context, hc *hyperv1.HostedCluster, releaseProvider releaseinfo.Provider, hypershiftOperatorImage string, pullSecret []byte) (string, error) {
	if val, ok := hc.Annotations[hyperv1.ControlPlaneOperatorImageAnnotation]; ok {
		return val, nil
	}
	releaseInfo, err := releaseProvider.Lookup(ctx, HCControlPlaneReleaseImage(hc), pullSecret)
	if err != nil {
		return "", err
	}
	version, err := semver.Parse(releaseInfo.Version())
	if err != nil {
		return "", err
	}
	if controlplaneoperatoroverrides.IsOverridesEnabled() {
		overrideImage := controlplaneoperatoroverrides.CPOImage(string(hc.Spec.Platform.Type), version.String())
		if overrideImage != "" {
			return overrideImage, nil
		}
	}

	if hypershiftImage, exists := releaseInfo.ComponentImages()["hypershift"]; exists {
		return hypershiftImage, nil
	}

	if version.Minor < 9 {
		return "", fmt.Errorf("unsupported release image with version %s", version.String())
	}
	return hypershiftOperatorImage, nil
}

// GetControlPlaneOperatorImageLabels resolves the appropriate control plane
// operator image labels based on the following order of precedence (from most
// to least preferred):
//
//  1. The labels specified by the ControlPlaneOperatorImageLabelsAnnotation on the
//     HostedCluster resource itself
//  2. The image labels in the medata of the image as resolved by GetControlPlaneOperatorImage
func GetControlPlaneOperatorImageLabels(ctx context.Context, hc *hyperv1.HostedCluster, controlPlaneOperatorImage string, pullSecret []byte, imageMetadataProvider ImageMetadataProvider) (map[string]string, error) {
	if val, ok := hc.Annotations[hyperv1.ControlPlaneOperatorImageLabelsAnnotation]; ok {
		annotatedLabels := map[string]string{}
		rawLabels := strings.Split(val, ",")
		for i, rawLabel := range rawLabels {
			parts := strings.Split(rawLabel, "=")
			if len(parts) != 2 {
				return nil, fmt.Errorf("hosted cluster %s/%s annotation %d malformed: label %s not in key=value form", hc.Namespace, hc.Name, i, rawLabel)
			}
			annotatedLabels[parts[0]] = parts[1]
		}
		return annotatedLabels, nil
	}

	controlPlaneOperatorImageMetadata, err := imageMetadataProvider.ImageMetadata(ctx, controlPlaneOperatorImage, pullSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to look up image metadata for %s: %w", controlPlaneOperatorImage, err)
	}

	return ImageLabels(controlPlaneOperatorImageMetadata), nil
}

var (
	hasPortRegex = regexp.MustCompile(`:\d{1,5}$`)
)

func HostFromURL(addr string) (string, error) {
	parsedURL, err := url.Parse(addr)
	if err != nil {
		return "", fmt.Errorf("failed to parse URL(%s): %w", addr, err)
	}
	hostPort := parsedURL.Host
	if hostPort == "" {
		return "", fmt.Errorf("missing host/port name in URL(%s)", addr)
	}
	if !hasPortRegex.MatchString(hostPort) {
		return hostPort, nil
	}
	hostName, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return "", fmt.Errorf("failed to split host/port from (%s): %w", hostPort, err)
	}
	if hostName == "" {
		return "", fmt.Errorf("missing host name in URL(%s)", addr)
	}
	return hostName, nil

}

// EnableIfCustomKubeconfig returns true if the hosted control plane has a custom kubeconfig defined
func EnableIfCustomKubeconfig(hcp *hyperv1.HostedControlPlane) bool {
	return len(hcp.Spec.KubeAPIServerDNSName) > 0
}
