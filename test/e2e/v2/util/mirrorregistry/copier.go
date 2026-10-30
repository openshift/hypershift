package mirrorregistry

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultCopyTimeout = 30 * time.Minute
	copyDeadlineMargin = 30 * time.Second
	minCopyDeadline    = 60 * time.Second
	copyPollInterval   = 5 * time.Second
)

type jobCopier struct {
	client       crclient.Client
	image        string
	name         string
	pollInterval time.Duration
}

var _ ImageCopier = (*jobCopier)(nil)

func newJobCopier(client crclient.Client, image, name string) *jobCopier {
	return &jobCopier{client: client, image: image, name: name, pollInterval: copyPollInterval}
}

func jobDeadlineSeconds(ctx context.Context, now time.Time) int64 {
	if deadline, ok := ctx.Deadline(); ok {
		return int64(max(deadline.Sub(now)-copyDeadlineMargin, minCopyDeadline) / time.Second)
	}
	return int64(defaultCopyTimeout / time.Second)
}

func copyResourceName(name string, req CopyRequest) (string, error) {
	// Construct the hash input explicitly so credential bytes never enter it.
	input := struct {
		Namespace string
		Images    []ImageCopy
		Releases  []ReleaseCopy
	}{Namespace: req.Namespace, Images: req.Images, Releases: req.Releases}
	data, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encode copy request: %w", err)
	}
	suffix := fmt.Sprintf("-copy-%x", sha256.Sum256(data))[:16]
	if len(name) > 63-len(suffix) {
		name = strings.TrimRight(name[:63-len(suffix)], "-")
	}
	result := name + suffix
	if problems := validation.IsDNS1123Label(result); len(problems) != 0 {
		return "", fmt.Errorf("invalid copy resource name %q: %s", result, strings.Join(problems, "; "))
	}
	return result, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func copyScript(req CopyRequest, hasPullSecret bool) string {
	var script strings.Builder
	script.WriteString(`set -euo pipefail
cat /etc/pki/tls/certs/ca-bundle.crt /run/copy/ca.crt > /tmp/ca-bundle.pem
export SSL_CERT_FILE=/tmp/ca-bundle.pem
`)
	auth := ""
	if hasPullSecret {
		auth = " -a '/run/copy/.dockerconfigjson'"
	}
	for _, image := range req.Images {
		fmt.Fprintf(&script, "oc image mirror --keep-manifest-list=true%s %s %s\n", auth, shellQuote(image.From), shellQuote(image.To))
	}
	if len(req.Releases) == 0 {
		script.WriteString("printf '%s' '{}' > /tmp/copy-result.json\n")
	} else {
		// JSON escaping is independent of shell quoting. Handle all JSON control
		// characters without requiring jq in the CLI image.
		script.WriteString(`json_quote() {
  local value="$1" char escaped i
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  for ((i=1; i<32; i++)); do
    printf -v char '%b' "$(printf '\\%03o' "$i")"
    printf -v escaped '\\u%04x' "$i"
    value="${value//"$char"/"$escaped"}"
  done
  printf '"%s"' "$value"
}
printf '%s' '{"releaseSourceRepositories":{' > /tmp/copy-result.json
`)
		for i, release := range req.Releases {
			repository, _, _ := strings.Cut(release.From, "@")
			fmt.Fprintf(&script, "oc adm release info%s --pullspecs %s > /tmp/pullspecs\n", auth, shellQuote(release.From))
			script.WriteString("awk '{print $2}' /tmp/pullspecs | grep '@sha256:' | sed 's/@.*//' > /tmp/component-repositories\n")
			fmt.Fprintf(&script, "printf '%%s\\n' %s >> /tmp/component-repositories\n", shellQuote(repository))
			script.WriteString("LC_ALL=C sort -u /tmp/component-repositories > /tmp/repositories\n")
			if i > 0 {
				script.WriteString("printf ',' >> /tmp/copy-result.json\n")
			}
			fmt.Fprintf(&script, "json_quote %s >> /tmp/copy-result.json\n", shellQuote(release.From))
			script.WriteString(`printf ':[' >> /tmp/copy-result.json
separator=''
while IFS= read -r repository; do
  printf '%s' "$separator" >> /tmp/copy-result.json
  json_quote "$repository" >> /tmp/copy-result.json
  separator=','
done < /tmp/repositories
printf ']' >> /tmp/copy-result.json
`)
			fmt.Fprintf(&script, "oc adm release mirror%s --from=%s --to=%s --keep-manifest-list=true --print-mirror-instructions=none\n", auth, shellQuote(release.From), shellQuote(release.ToRepository))
		}
		script.WriteString("printf '}}' >> /tmp/copy-result.json\n")
	}
	script.WriteString(`if [ "$(wc -c < /tmp/copy-result.json)" -gt 4096 ]; then
  echo 'CopyResult exceeds 4096 bytes; refusing to truncate termination message' >&2
  exit 1
fi
cat /tmp/copy-result.json > /dev/termination-log
`)
	return script.String()
}

func copierLabels(prefix string) map[string]string {
	labelName := prefix + "-copier"
	if len(labelName) > 63 {
		labelName = strings.TrimRight(prefix[:56], "-") + "-copier"
		// If truncation reproduces the registry name, make the copier label
		// strictly shorter to keep the Service and Deployment selectors disjoint.
		if labelName == prefix {
			labelName = strings.TrimRight(prefix[:len(prefix)-len("-copier")-1], "-") + "-copier"
		}
	}
	labels := resourceLabels(labelName)
	labels["app.kubernetes.io/component"] = "copier"
	return labels
}

func buildCopyJob(prefix, name, namespace, image string, deadlineSeconds int64, script string, hasPullSecret bool) *batchv1.Job {
	labels := copierLabels(prefix)
	items := []corev1.KeyToPath{{Key: "ca.crt", Path: "ca.crt"}}
	if hasPullSecret {
		items = append(items, corev1.KeyToPath{Key: corev1.DockerConfigJsonKey, Path: corev1.DockerConfigJsonKey})
	}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: batchv1.JobSpec{
			BackoffLimit: ptr.To(int32(0)), ActiveDeadlineSeconds: ptr.To(deadlineSeconds),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: maps.Clone(labels)},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr.To(true),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name: "copy", Image: image, Command: []string{"/bin/bash", "-c", script},
						Env:                    []corev1.EnvVar{{Name: "HOME", Value: "/tmp"}},
						TerminationMessagePath: "/dev/termination-log", TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
						Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("256Mi"),
						}},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
						VolumeMounts: []corev1.VolumeMount{{Name: "copy", MountPath: "/run/copy", ReadOnly: true}},
					}},
					Volumes: []corev1.Volume{{Name: "copy", VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{SecretName: name, Items: items},
					}}},
				},
			},
		},
	}
}

func (c *jobCopier) Copy(ctx context.Context, req CopyRequest) (*CopyResult, error) {
	if len(req.CABundlePEM) == 0 {
		return nil, fmt.Errorf("copy registry CA bundle is required")
	}
	name, err := copyResourceName(c.name, req)
	if err != nil {
		return nil, err
	}
	if err := c.deleteCopyResources(ctx, req.Namespace, name); err != nil {
		return nil, err
	}
	if err := wait.PollUntilContextCancel(ctx, c.pollInterval, true, func(ctx context.Context) (bool, error) {
		err := c.client.Get(ctx, crclient.ObjectKey{Namespace: req.Namespace, Name: name}, &batchv1.Job{})
		return apierrors.IsNotFound(err), copyReadError(ctx, crclient.IgnoreNotFound(err))
	}); err != nil {
		return nil, fmt.Errorf("wait for previous copy Job deletion: %w", err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: req.Namespace, Labels: copierLabels(c.name)},
		Type:       corev1.SecretTypeOpaque, Data: map[string][]byte{"ca.crt": req.CABundlePEM},
	}
	hasPullSecret := len(req.SourcePullSecret) > 0
	if hasPullSecret {
		secret.Data[corev1.DockerConfigJsonKey] = req.SourcePullSecret
	}
	if err := c.client.Create(ctx, secret); err != nil {
		return nil, fmt.Errorf("create copy Secret: %w", err)
	}
	job := buildCopyJob(c.name, name, req.Namespace, c.image, jobDeadlineSeconds(ctx, time.Now()), copyScript(req, hasPullSecret), hasPullSecret)
	if err := c.client.Create(ctx, job); err != nil {
		return nil, fmt.Errorf("create copy Job: %w", err)
	}
	return c.waitForResult(ctx, req.Namespace, name)
}

func (c *jobCopier) deleteCopyResources(ctx context.Context, namespace, name string) error {
	metadata := metav1.ObjectMeta{Namespace: namespace, Name: name}
	if err := c.client.Delete(ctx, &batchv1.Job{ObjectMeta: metadata}, crclient.PropagationPolicy(metav1.DeletePropagationBackground)); crclient.IgnoreNotFound(err) != nil {
		return fmt.Errorf("delete copy Job: %w", err)
	}
	if err := c.client.Delete(ctx, &corev1.Secret{ObjectMeta: metadata}); crclient.IgnoreNotFound(err) != nil {
		return fmt.Errorf("delete copy Secret: %w", err)
	}
	return nil
}

// copyReadError leaves transient read failures to the next poll, but does not
// hide cancellation, missing resources, or permanent errors such as Forbidden.
func copyReadError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var networkError net.Error
	if apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsTooManyRequests(err) ||
		apierrors.IsServiceUnavailable(err) || apierrors.IsInternalError(err) || errors.As(err, &networkError) {
		klog.FromContext(ctx).Error(err, "Transient copier read failure; retrying")
		return nil
	}
	return err
}

// waitForResult relies on the caller's context to bound transient read retries.
// The Job deadline alone cannot bound this wait if the API stays unavailable;
// callers should supply a context deadline to bound that case as well.
func (c *jobCopier) waitForResult(ctx context.Context, namespace, name string) (*CopyResult, error) {
	var result CopyResult
	err := wait.PollUntilContextCancel(ctx, c.pollInterval, true, func(ctx context.Context) (bool, error) {
		// With immediate=true, PollUntilContextCancel invokes the condition
		// even for an already-canceled context. Do not process or clean up a
		// completed Job in that case (fake clients may ignore cancellation).
		if err := ctx.Err(); err != nil {
			return false, err
		}
		job := &batchv1.Job{}
		if err := c.client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: name}, job); err != nil {
			return false, copyReadError(ctx, err)
		}
		var finished *batchv1.JobCondition
		for i := range job.Status.Conditions {
			condition := &job.Status.Conditions[i]
			if condition.Status == corev1.ConditionTrue && (condition.Type == batchv1.JobComplete || condition.Type == batchv1.JobFailed) {
				finished = condition
				break
			}
		}
		if finished == nil {
			return false, nil
		}
		pods := &corev1.PodList{}
		if err := c.client.List(ctx, pods, crclient.InNamespace(namespace), crclient.MatchingLabels{"batch.kubernetes.io/job-name": name}); err != nil {
			return false, copyReadError(ctx, err)
		}
		message := ""
		for _, pod := range pods.Items {
			// Background deletion can leave an old pod behind during a retry.
			if job.UID != "" && !metav1.IsControlledBy(&pod, job) {
				continue
			}
			for _, status := range pod.Status.ContainerStatuses {
				if status.Name == "copy" && status.State.Terminated != nil {
					message = status.State.Terminated.Message
				}
			}
		}
		if finished.Type == batchv1.JobFailed {
			if message == "" {
				// On DeadlineExceeded the controller deletes active pods before
				// setting Failed, so their termination messages may be lost.
				message = "no termination message (pod deleted or message unavailable)"
			}
			return false, fmt.Errorf("copy Job %s/%s failed: %s: %s; termination message: %s", namespace, name, finished.Reason, finished.Message, message)
		}
		if message == "" {
			return false, fmt.Errorf("completed copy Job %s/%s has an empty termination message", namespace, name)
		}
		var decoded *CopyResult
		if err := json.Unmarshal([]byte(message), &decoded); err != nil {
			return false, fmt.Errorf("decode copy Job termination message: %w", err)
		}
		if decoded == nil {
			return false, fmt.Errorf("decode copy Job termination message: expected a JSON object, got null")
		}
		result = *decoded
		return true, nil
	})
	if err != nil {
		if strings.Contains(err.Error(), "HTTP 405") || strings.Contains(err.Error(), "Method not allowed") {
			return nil, fmt.Errorf("registry is read-only: wait for copy Job %s/%s: %w", namespace, name, err)
		}
		return nil, fmt.Errorf("wait for copy Job %s/%s: %w", namespace, name, err)
	}
	if err := c.deleteCopyResources(ctx, namespace, name); err != nil {
		return nil, err
	}
	return &result, nil
}
