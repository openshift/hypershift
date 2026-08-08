package util

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
		"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cmdutil "github.com/openshift/hypershift/cmd/util"
	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/releaseinfo/registryclient"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	kubeclient "k8s.io/client-go/kubernetes"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/blang/semver"
	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
)

// ErrPullSecretUnavailable indicates the pull secret is missing or malformed.
var ErrPullSecretUnavailable = errors.New("pull secret unavailable")

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

// HashConfigMapData hashes the key-value pairs of a ConfigMap's Data field
// deterministically, using null-byte delimiters to prevent key/value collisions.
// Returns an empty string for nil or empty maps.
func HashConfigMapData(data map[string]string) string {
	if len(data) == 0 {
		return ""
	}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte(0)
		b.WriteString(data[k])
		b.WriteByte(0)
	}
	return HashSimple(b.String())
}

// HashSimple takes a value, typically a string, and returns a 32-bit FNV-1a hashed version of the value as a string
func HashSimple(o interface{}) string {
	hash := fnv.New32a()
	_, _ = fmt.Fprintf(hash, "%v", o)
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

func GetKubeClientSet() (kubeclient.Interface, error) {
	return GetKubeClientSetWithKubeconfig("")
}

// GetKubeClientSetWithKubeconfig creates a Kubernetes clientset using the specified kubeconfig
// file path. If kubeconfigPath is empty, it falls back to the default kubeconfig resolution.
func GetKubeClientSetWithKubeconfig(kubeconfigPath string) (kubeclient.Interface, error) {
	cfg, err := cmdutil.GetConfigWithKubeconfig(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes config: %w", err)
	}

	kc, err := kubeclient.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("unable to create kubernetes clientset: %w", err)
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
	pullSecretBytes, err := GetPullSecretBytes(ctx, c, hc)
	if err != nil {
		return "", err
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
	hcAnnotationScopingEnabledEnvVal := os.Getenv(k8sutil.EnableHostedClustersAnnotationScopingEnv)
	hcScopeAnnotationEnvVal := os.Getenv(k8sutil.HostedClustersScopeAnnotationEnv)
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
			return obj.GetAnnotations()[k8sutil.HostedClustersScopeAnnotation]
		}
	case *hyperv1.NodePool:
		hostedClusterName = fmt.Sprintf("%s/%s", obj.Namespace, obj.Spec.ClusterName)
	default:
		if obj.GetAnnotations() != nil {
			nodePoolName = obj.GetAnnotations()["hypershift.openshift.io/nodePool"]
			hostedClusterName = obj.GetAnnotations()[k8sutil.HostedClusterAnnotation]
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
		return hcluster.GetAnnotations()[k8sutil.HostedClustersScopeAnnotation]
	}
	return ""
}

// SanitizeIgnitionPayload make sure the IgnitionPayload is valid
// and does not contain inconsistencies.
func SanitizeIgnitionPayload(payload []byte) error {
	var jsonPayload ignitionapi.Config

	if err := json.Unmarshal(payload, &jsonPayload); err != nil {
		return fmt.Errorf("error unmarshalling Ignition payload: %w", err)
	}

	return nil
}

func GetPullSecretBytes(ctx context.Context, c client.Client, hc *hyperv1.HostedCluster) ([]byte, error) {
	pullSecret := corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name}, &pullSecret); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPullSecretUnavailable, err)
	}

	pullSecretBytes, ok := pullSecret.Data[corev1.DockerConfigJsonKey]
	if !ok {
		return nil, fmt.Errorf("%w: expected %s key in secret %q", ErrPullSecretUnavailable, corev1.DockerConfigJsonKey, hc.Spec.PullSecret.Name)
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

// EnableIfCustomKubeconfig returns true if the hosted control plane has a custom kubeconfig defined
func EnableIfCustomKubeconfig(hcp *hyperv1.HostedControlPlane) bool {
	return len(hcp.Spec.KubeAPIServerDNSName) > 0
}

// CountAvailableNodes counts the number of available nodes in the cluster.
// Available nodes are defined as Ready nodes that are not cordoned (Unschedulable).
func CountAvailableNodes(ctx context.Context, client client.Client) (int32, error) {
	var nodeList corev1.NodeList
	if err := client.List(ctx, &nodeList); err != nil {
		return 0, fmt.Errorf("failed to list nodes: %w", err)
	}

	// Count only available nodes (Ready nodes that are not cordoned)
	availableNodesCount := int32(0)
	for _, node := range nodeList.Items {
		if !node.Spec.Unschedulable {
			for _, condition := range node.Status.Conditions {
				if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
					availableNodesCount++
					break
				}
			}
		}
	}

	return availableNodesCount, nil
}
