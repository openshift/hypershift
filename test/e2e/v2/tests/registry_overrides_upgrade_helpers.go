//go:build e2ev2

package tests

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
)

// registryOverridesManifestAccept lists every manifest media type a release
// payload or component image can have, so the registry returns the stored
// manifest instead of converting it or answering 404.
var registryOverridesManifestAccept = strings.Join([]string{
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
}, ", ")

// registryOverridesUpgradeStart returns the release image digests of the
// upgrade and when it started, from the HostedCluster version history: the
// newest entry must be targetImage and Completed, and the entry before it is
// the release the cluster upgraded from. Both images must be by digest, so
// the caller can look them up in the mirror. The start time is the start of
// the newest entry.
func registryOverridesUpgradeStart(hc *hyperv1.HostedCluster, targetImage string) (from, to string, started time.Time, err error) {
	if hc == nil {
		return "", "", time.Time{}, fmt.Errorf("hostedCluster is nil")
	}
	if hc.Status.Version == nil || len(hc.Status.Version.History) < 2 {
		return "", "", time.Time{}, fmt.Errorf("HC %s/%s version history has fewer than 2 entries: no upgrade recorded", hc.Namespace, hc.Name)
	}
	latest, previous := hc.Status.Version.History[0], hc.Status.Version.History[1]
	switch {
	case latest.Image != targetImage:
		return "", "", time.Time{}, fmt.Errorf("HC %s/%s newest version history entry is %s, want %s", hc.Namespace, hc.Name, latest.Image, targetImage)
	case latest.State != configv1.CompletedUpdate:
		return "", "", time.Time{}, fmt.Errorf("HC %s/%s upgrade to %s is %s, want %s", hc.Namespace, hc.Name, latest.Image, latest.State, configv1.CompletedUpdate)
	case previous.Image == latest.Image:
		return "", "", time.Time{}, fmt.Errorf("HC %s/%s version history has no other release before %s", hc.Namespace, hc.Name, latest.Image)
	case latest.StartedTime.IsZero():
		return "", "", time.Time{}, fmt.Errorf("HC %s/%s upgrade to %s has no start time", hc.Namespace, hc.Name, latest.Image)
	}
	for _, image := range []string{previous.Image, latest.Image} {
		if !strings.Contains(image, "@sha256:") {
			return "", "", time.Time{}, fmt.Errorf("HC %s/%s release image %s is not referenced by digest", hc.Namespace, hc.Name, image)
		}
	}
	return previous.Image, latest.Image, latest.StartedTime.Time, nil
}

// registryOverridesCheckManifest checks that the mirror at host serves the
// manifest of digest in repository, with a HEAD request whose
// Docker-Content-Digest must be digest. client must trust the mirror CA.
func registryOverridesCheckManifest(ctx context.Context, client *http.Client, host, repository, digest string) error {
	url := fmt.Sprintf("https://%s/v2/%s/manifests/%s", host, repository, digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return fmt.Errorf("build request for %s: %w", url, err)
	}
	req.Header.Set("Accept", registryOverridesManifestAccept)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HEAD %s: %w", url, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HEAD %s: status %d", url, resp.StatusCode)
	}
	if got := resp.Header.Get("Docker-Content-Digest"); got != digest {
		return fmt.Errorf("HEAD %s: Docker-Content-Digest %q, want %q", url, got, digest)
	}
	return nil
}

// registryOverridesPullFailures returns, for pods not being deleted, every
// init or regular container that the kubelet currently reports as unable to
// pull its image or as rejecting its image reference.
func registryOverridesPullFailures(pods []corev1.Pod) []string {
	var failures []string
	for i := range pods {
		pod := &pods[i]
		if pod.DeletionTimestamp != nil {
			continue
		}
		statuses := append(slices.Clone(pod.Status.InitContainerStatuses), pod.Status.ContainerStatuses...)
		for j := range statuses {
			status := &statuses[j]
			switch imageMirroringClassifyContainer(status) {
			case imageMirroringContainerPullFailed, imageMirroringContainerImageRejected:
				failures = append(failures, fmt.Sprintf("pod %s/%s container %s: %s: %s", pod.Namespace, pod.Name, status.Name, status.State.Waiting.Reason, status.State.Waiting.Message))
			}
		}
	}
	return failures
}

// registryOverridesEventTime returns when an event was last seen: its last
// series observation, its event time, its last timestamp or its first
// timestamp, in that order of preference.
func registryOverridesEventTime(event *corev1.Event) time.Time {
	switch {
	case event.Series != nil && !event.Series.LastObservedTime.IsZero():
		return event.Series.LastObservedTime.Time
	case !event.EventTime.IsZero():
		return event.EventTime.Time
	case !event.LastTimestamp.IsZero():
		return event.LastTimestamp.Time
	}
	return event.FirstTimestamp.Time
}

// registryOverridesPullFailureEvents returns the kubelet events about pods
// last seen at or after since that report a failed image pull: reason
// "Failed" with a message about pulling an image (the kubelet's
// ErrImagePull), "ErrImagePull", "InvalidImageName", or "BackOff" with a
// message about pulling an image (the kubelet's ImagePullBackOff).
func registryOverridesPullFailureEvents(events []corev1.Event, since time.Time) []string {
	var failures []string
	for i := range events {
		event := &events[i]
		if event.InvolvedObject.Kind != "Pod" || registryOverridesEventTime(event).Before(since) {
			continue
		}
		message := strings.ToLower(event.Message)
		aboutPull := strings.Contains(message, "pull") && strings.Contains(message, "image")
		switch {
		case event.Reason == "ErrImagePull", event.Reason == "InvalidImageName",
			event.Reason == "Failed" && aboutPull,
			event.Reason == "BackOff" && aboutPull:
			failures = append(failures, fmt.Sprintf("pod %s/%s at %s: %s: %s", event.InvolvedObject.Namespace, event.InvolvedObject.Name,
				registryOverridesEventTime(event).UTC().Format(time.RFC3339), event.Reason, event.Message))
		}
	}
	return failures
}
