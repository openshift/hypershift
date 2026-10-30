//go:build e2ev2

package tests

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	imageMirroringWorkloadContainer = "registry"
	imageMirroringTrustBundleKey    = "ca-bundle.crt"
	imageMirroringManifestMaxBytes  = 4 << 20
	imageMirroringManifestListType  = "application/vnd.docker.distribution.manifest.list.v2+json"
	imageMirroringOCIIndexType      = "application/vnd.oci.image.index.v1+json"
	imageMirroringHTTPClientTimeout = 30 * time.Second
)

var imageMirroringDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// imageMirroringVirtualMirror returns the host and repository path of the
// virtual source's mirror at the exact fixture Route host, as declared in the
// validated HC imageContentSources.
func imageMirroringVirtualMirror(expected []hyperv1.ImageContentSource, routeHost string) (string, string, error) {
	sourceHost, _, _ := strings.Cut(imageMirroringVirtualSource, "/")
	if !strings.HasSuffix(sourceHost, ".invalid") {
		return "", "", fmt.Errorf("virtual source %q must use an unresolvable .invalid host", imageMirroringVirtualSource)
	}
	if strings.TrimSpace(routeHost) == "" {
		return "", "", fmt.Errorf("mirror fixture Route host must not be empty")
	}
	for _, entry := range expected {
		if entry.Source != imageMirroringVirtualSource {
			continue
		}
		for _, mirror := range entry.Mirrors {
			host, path, ok := strings.Cut(mirror, "/")
			if ok && path != "" && host == routeHost {
				return host, path, nil
			}
		}
		return "", "", fmt.Errorf("HC source %q has no mirror at Route host %q: %v", entry.Source, routeHost, entry.Mirrors)
	}
	return "", "", fmt.Errorf("HC imageContentSources have no entry for virtual source %q: %+v", imageMirroringVirtualSource, expected)
}

// imageMirroringDigestRef returns repository@digest, taking the digest from a
// by-digest image reference.
func imageMirroringDigestRef(repository, image string) (string, string, error) {
	if strings.TrimSpace(repository) == "" || strings.Contains(repository, "@") {
		return "", "", fmt.Errorf("repository %q must be nonblank and must not contain a digest", repository)
	}
	if lastSegment := repository[strings.LastIndex(repository, "/")+1:]; strings.Contains(lastSegment, ":") {
		return "", "", fmt.Errorf("repository %q must not contain a tag", repository)
	}
	_, digest, ok := strings.Cut(image, "@")
	if !ok || !imageMirroringDigestPattern.MatchString(digest) {
		return "", "", fmt.Errorf("image %q must be referenced by a sha256 digest", image)
	}
	return repository + "@" + digest, digest, nil
}

// imageMirroringImageIDDigest extracts the manifest digest from a container
// status imageID ("repo@sha256:..." or "docker-pullable://repo@sha256:...").
// A bare image ID is rejected because it is not a manifest digest.
func imageMirroringImageIDDigest(imageID string) (string, error) {
	ref := strings.TrimPrefix(imageID, "docker-pullable://")
	at := strings.LastIndex(ref, "@")
	if at <= 0 || !imageMirroringDigestPattern.MatchString(ref[at+1:]) {
		return "", fmt.Errorf("imageID %q is not a repository@sha256 manifest reference", imageID)
	}
	return ref[at+1:], nil
}

// imageMirroringTrustBundle reads the CA bundle referenced by the HC
// additionalTrustBundle from the HC namespace.
func imageMirroringTrustBundle(ctx context.Context, client crclient.Client, hc *hyperv1.HostedCluster) ([]byte, error) {
	if hc == nil || hc.Spec.AdditionalTrustBundle == nil || strings.TrimSpace(hc.Spec.AdditionalTrustBundle.Name) == "" {
		return nil, fmt.Errorf("HC must reference an additionalTrustBundle ConfigMap")
	}
	key := crclient.ObjectKey{Namespace: hc.Namespace, Name: hc.Spec.AdditionalTrustBundle.Name}
	cm := &corev1.ConfigMap{}
	if err := client.Get(ctx, key, cm); err != nil {
		return nil, fmt.Errorf("read additionalTrustBundle ConfigMap %s: %w", key, err)
	}
	bundle := cm.Data[imageMirroringTrustBundleKey]
	if strings.TrimSpace(bundle) == "" {
		return nil, fmt.Errorf("additionalTrustBundle ConfigMap %s has an empty %s", key, imageMirroringTrustBundleKey)
	}
	return []byte(bundle), nil
}

// imageMirroringHTTPClient returns an HTTPS client that trusts only caPEM.
func imageMirroringHTTPClient(caPEM []byte) (*http.Client, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("trust bundle contains no PEM certificates")
	}
	return &http.Client{
		Timeout: imageMirroringHTTPClientTimeout,
		Transport: &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}, nil
}

// imageMirroringFetchManifest reads a manifest list or index by digest from the
// mirror's registry API. The body is returned unverified.
func imageMirroringFetchManifest(ctx context.Context, client *http.Client, host, repository, digest string) ([]byte, error) {
	url := fmt.Sprintf("https://%s/v2/%s/manifests/%s", host, repository, digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", url, err)
	}
	req.Header.Set("Accept", imageMirroringManifestListType+", "+imageMirroringOCIIndexType)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, imageMirroringManifestMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d: %.512s", url, resp.StatusCode, body)
	}
	if len(body) > imageMirroringManifestMaxBytes {
		return nil, fmt.Errorf("GET %s: manifest exceeds %d bytes", url, imageMirroringManifestMaxBytes)
	}
	return body, nil
}

// imageMirroringPlatformDigest verifies that body is the manifest list or
// index identified by listDigest and returns the digest of its single entry
// for os/architecture.
func imageMirroringPlatformDigest(body []byte, listDigest, os, architecture string) (string, error) {
	sum := sha256.Sum256(body)
	if got := "sha256:" + hex.EncodeToString(sum[:]); got != listDigest {
		return "", fmt.Errorf("mirror manifest digest %s does not match requested digest %s", got, listDigest)
	}
	var list struct {
		MediaType string `json:"mediaType"`
		Manifests []struct {
			Digest   string `json:"digest"`
			Platform *struct {
				OS           string `json:"os"`
				Architecture string `json:"architecture"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return "", fmt.Errorf("decode manifest list %s: %w", listDigest, err)
	}
	if (list.MediaType != imageMirroringManifestListType && list.MediaType != imageMirroringOCIIndexType && list.MediaType != "") || len(list.Manifests) == 0 {
		return "", fmt.Errorf("manifest %s is not a nonempty manifest list or index (mediaType %q)", listDigest, list.MediaType)
	}
	var found []string
	for _, m := range list.Manifests {
		if m.Platform != nil && m.Platform.OS == os && m.Platform.Architecture == architecture {
			found = append(found, m.Digest)
		}
	}
	if len(found) != 1 {
		return "", fmt.Errorf("manifest list %s has %d entries for %s/%s, expected exactly one", listDigest, len(found), os, architecture)
	}
	if !imageMirroringDigestPattern.MatchString(found[0]) {
		return "", fmt.Errorf("manifest list %s has a malformed digest %q for %s/%s", listDigest, found[0], os, architecture)
	}
	return found[0], nil
}

// imageMirroringReadyLinuxNodes counts Ready nodes labeled kubernetes.io/os=linux.
func imageMirroringReadyLinuxNodes(nodes []corev1.Node) int {
	count := 0
	for _, node := range nodes {
		if node.Labels[corev1.LabelOSStable] != "linux" {
			continue
		}
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				count++
				break
			}
		}
	}
	return count
}

// imageMirroringWorkloadPod returns a restricted-compliant one-shot pod that
// always pulls image and runs the registry binary's version command.
func imageMirroringWorkloadPod(namespace, name, image string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: corev1.PodSpec{
			RestartPolicy:                 corev1.RestartPolicyNever,
			NodeSelector:                  map[string]string{corev1.LabelOSStable: "linux"},
			AutomountServiceAccountToken:  ptr.To(false),
			TerminationGracePeriodSeconds: ptr.To[int64](5),
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   ptr.To(true),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name:            imageMirroringWorkloadContainer,
				Image:           image,
				ImagePullPolicy: corev1.PullAlways,
				Command:         []string{"/bin/registry", "--version"},
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("16Mi"),
				}},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: ptr.To(false),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
			}},
		},
	}
}

// imageMirroringContainerStatus returns the workload container status, or nil
// when the pod is nil or has no status for that container.
func imageMirroringContainerStatus(pod *corev1.Pod) *corev1.ContainerStatus {
	if pod == nil {
		return nil
	}
	for i := range pod.Status.ContainerStatuses {
		if pod.Status.ContainerStatuses[i].Name == imageMirroringWorkloadContainer {
			return &pod.Status.ContainerStatuses[i]
		}
	}
	return nil
}

// imageMirroringPodSummary renders the pod state used in predicate reasons.
func imageMirroringPodSummary(pod *corev1.Pod) string {
	if pod == nil {
		return "pod <nil>"
	}
	summary := fmt.Sprintf("pod %s/%s phase=%q", pod.Namespace, pod.Name, pod.Status.Phase)
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status != corev1.ConditionTrue {
			summary += fmt.Sprintf(" unscheduled=%s: %s", cond.Reason, cond.Message)
		}
	}
	status := imageMirroringContainerStatus(pod)
	switch {
	case status == nil:
		summary += fmt.Sprintf(" container %q has no status", imageMirroringWorkloadContainer)
	case status.State.Waiting != nil:
		summary += fmt.Sprintf(" waiting=%s: %s", status.State.Waiting.Reason, status.State.Waiting.Message)
	case status.State.Terminated != nil:
		summary += fmt.Sprintf(" terminated=%s exitCode=%d: %s", status.State.Terminated.Reason, status.State.Terminated.ExitCode, status.State.Terminated.Message)
	case status.State.Running != nil:
		summary += " running"
	}
	return summary
}

// imageMirroringCheckPulledPod is done when the one-shot container exited 0
// and the pod Succeeded. Failures that retries cannot fix are terminal errors.
func imageMirroringCheckPulledPod(pod *corev1.Pod) (bool, string, error) {
	if pod == nil {
		return false, "", fmt.Errorf("pod must not be nil")
	}
	summary := imageMirroringPodSummary(pod)
	if pod.Status.Phase == corev1.PodFailed {
		return false, "", fmt.Errorf("%s", summary)
	}
	status := imageMirroringContainerStatus(pod)
	if status == nil {
		return false, summary, nil
	}
	if waiting := status.State.Waiting; waiting != nil {
		switch waiting.Reason {
		case "CreateContainerConfigError", "CreateContainerError", "InvalidImageName", "ErrImageNeverPull":
			return false, "", fmt.Errorf("%s", summary)
		}
		return false, summary, nil
	}
	if terminated := status.State.Terminated; terminated != nil {
		if terminated.ExitCode != 0 {
			return false, "", fmt.Errorf("%s", summary)
		}
		if pod.Status.Phase == corev1.PodSucceeded {
			return true, summary, nil
		}
	}
	return false, summary, nil
}

// imageMirroringCheckImageID verifies that the container imageID digest is one
// of the accepted manifest digests.
func imageMirroringCheckImageID(pod *corev1.Pod, accepted []string) error {
	if pod == nil {
		return fmt.Errorf("pod must not be nil")
	}
	if len(accepted) == 0 {
		return fmt.Errorf("no accepted digests for pod %s/%s", pod.Namespace, pod.Name)
	}
	status := imageMirroringContainerStatus(pod)
	if status == nil {
		return fmt.Errorf("pod %s/%s has no status for container %q", pod.Namespace, pod.Name, imageMirroringWorkloadContainer)
	}
	digest, err := imageMirroringImageIDDigest(status.ImageID)
	if err != nil {
		return fmt.Errorf("pod %s/%s: %w", pod.Namespace, pod.Name, err)
	}
	for _, want := range accepted {
		if digest == want {
			return nil
		}
	}
	return fmt.Errorf("pod %s/%s imageID %q digest is not one of the mirror digests %v", pod.Namespace, pod.Name, status.ImageID, accepted)
}
