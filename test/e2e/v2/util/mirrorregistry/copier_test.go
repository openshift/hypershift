package mirrorregistry

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const (
	copyTestSource             = DefaultRegistryImage
	copyTestDestination        = "mirror-registry.test-mirror-registry.svc:5000/quay.io/libpod/registry"
	copyTestRelease            = "quay.io/openshift-release-dev/ocp-release@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	copyTestReleaseDestination = "mirror-registry.test-mirror-registry.svc:5000/quay.io/openshift-release-dev/ocp-release"
	copyScriptHeader           = `set -euo pipefail
cat /etc/pki/tls/certs/ca-bundle.crt /run/copy/ca.crt > /tmp/ca-bundle.pem
export SSL_CERT_FILE=/tmp/ca-bundle.pem
`
	copyScriptFooter = `if [ "$(wc -c < /tmp/copy-result.json)" -gt 4096 ]; then
  echo 'CopyResult exceeds 4096 bytes; refusing to truncate termination message' >&2
  exit 1
fi
cat /tmp/copy-result.json > /dev/termination-log
`
)

func TestNewJobCopier(t *testing.T) {
	t.Run("When given a client image and name, it should retain them and default the poll interval", func(t *testing.T) {
		client := fake.NewClientBuilder().Build()
		copier := newJobCopier(client, "registry.ci.openshift.org/ocp/4.20:cli", "release-mirror")
		if copier.client != client || copier.image != "registry.ci.openshift.org/ocp/4.20:cli" || copier.name != "release-mirror" {
			t.Fatalf("unexpected copier: %+v", copier)
		}
		if copier.pollInterval != 5*time.Second {
			t.Fatalf("copier poll interval = %v, want 5s", copier.pollInterval)
		}
	})
}

func TestJobDeadlineSeconds(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name     string
		duration time.Duration
		deadline bool
		want     int64
	}{
		{name: "When there is no deadline, it should use thirty minutes", want: 1800},
		{name: "When the deadline is far away, it should subtract the margin and truncate seconds", deadline: true, duration: 10*time.Minute + 999*time.Millisecond, want: 570},
		{name: "When the deadline is closer than the margin, it should use sixty seconds", deadline: true, duration: 10 * time.Second, want: 60},
		{name: "When the deadline has passed, it should use sixty seconds", deadline: true, duration: -time.Minute, want: 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			if tc.deadline {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, now.Add(tc.duration))
				defer cancel()
			}
			if got := jobDeadlineSeconds(ctx, now); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCopyResourceName(t *testing.T) {
	req := CopyRequest{Namespace: DefaultNamespace, Images: []ImageCopy{{From: copyTestSource, To: copyTestDestination}}}
	input := `{"Namespace":"test-mirror-registry","Images":[{"From":"` + copyTestSource + `","To":"` + copyTestDestination + `"}],"Releases":null}`
	suffix := fmt.Sprintf("-copy-%x", sha256.Sum256([]byte(input)))[:16]
	for _, tc := range []struct {
		name    string
		prefix  string
		want    string
		invalid bool
	}{
		{name: "When the prefix is short, it should append the request hash", prefix: DefaultName, want: DefaultName + suffix},
		{name: "When the prefix is long, it should truncate to sixty three characters", prefix: strings.Repeat("a", 63), want: strings.Repeat("a", 47) + suffix},
		{name: "When truncation ends at a hyphen, it should trim the hyphen", prefix: strings.Repeat("a", 46) + "-long-registry-name", want: strings.Repeat("a", 46) + suffix},
		{name: "When the prefix is invalid, it should return an error", prefix: "Mirror_Registry", invalid: true},
		{name: "When the prefix is empty, it should return an error", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := copyResourceName(tc.prefix, req)
			if tc.invalid {
				if err == nil {
					t.Fatal("expected invalid name error")
				}
				return
			}
			if err != nil || got != tc.want || len(validation.IsDNS1123Label(got)) != 0 {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
			withSecrets := req
			withSecrets.CABundlePEM = []byte("private CA data")
			withSecrets.SourcePullSecret = []byte(`{"auths":{"quay.io":{}}}`)
			secretName, err := copyResourceName(tc.prefix, withSecrets)
			if err != nil || secretName != got {
				t.Fatalf("credentials changed name: %q, %v", secretName, err)
			}
			changed := req
			changed.Namespace = "test-mirror-registry-retry"
			other, err := copyResourceName(tc.prefix, changed)
			if err != nil || other == got {
				t.Fatalf("different request did not change name: %q, %v", other, err)
			}
		})
	}
}

func TestShellQuote(t *testing.T) {
	for _, tc := range []struct{ name, value, want string }{
		{name: "When the value is a digest reference, it should single quote it", value: copyTestSource, want: "'" + copyTestSource + "'"},
		{name: "When the value contains a quote spaces and substitution, it should escape without evaluation", value: "quay.io/libpod/registry's $(touch injected)", want: `'quay.io/libpod/registry'"'"'s $(touch injected)'`},
		{name: "When the value is empty, it should produce an empty argument", want: "''"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := shellQuote(tc.value)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			out, err := exec.CommandContext(t.Context(), "/bin/bash", "-c", "printf '%s' "+got).CombinedOutput()
			if err != nil || string(out) != tc.value {
				t.Fatalf("shell round trip: %q, %v", out, err)
			}
		})
	}
}

func TestCopyScript(t *testing.T) {
	releaseGolden := copyScriptHeader + `json_quote() {
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
oc adm release info -a '/run/copy/.dockerconfigjson' --pullspecs '` + copyTestRelease + `' > /tmp/pullspecs
awk '{print $2}' /tmp/pullspecs | grep '@sha256:' | sed 's/@.*//' > /tmp/component-repositories
printf '%s\n' 'quay.io/openshift-release-dev/ocp-release' >> /tmp/component-repositories
LC_ALL=C sort -u /tmp/component-repositories > /tmp/repositories
json_quote '` + copyTestRelease + `' >> /tmp/copy-result.json
printf ':[' >> /tmp/copy-result.json
separator=''
while IFS= read -r repository; do
  printf '%s' "$separator" >> /tmp/copy-result.json
  json_quote "$repository" >> /tmp/copy-result.json
  separator=','
done < /tmp/repositories
printf ']' >> /tmp/copy-result.json
oc adm release mirror -a '/run/copy/.dockerconfigjson' --from='` + copyTestRelease + `' --to='` + copyTestReleaseDestination + `' --keep-manifest-list=true --print-mirror-instructions=none
printf '}}' >> /tmp/copy-result.json
` + copyScriptFooter
	for _, tc := range []struct {
		name string
		req  CopyRequest
		auth bool
		want string
	}{
		{name: "When copying one anonymous image, it should match the validated mirror command", req: CopyRequest{Images: []ImageCopy{{From: copyTestSource, To: copyTestDestination}}}, want: copyScriptHeader + "oc image mirror --keep-manifest-list=true '" + copyTestSource + "' '" + copyTestDestination + "'\nprintf '%s' '{}' > /tmp/copy-result.json\n" + copyScriptFooter},
		{name: "When copying two authenticated images, it should authenticate both copies", auth: true, req: CopyRequest{Images: []ImageCopy{{From: copyTestSource, To: copyTestDestination}, {From: copyTestSource, To: "mirror-registry.test-mirror-registry.svc:5000/mirror-source.hypershift-e2e.invalid/libpod/registry"}}}, want: copyScriptHeader + "oc image mirror --keep-manifest-list=true -a '/run/copy/.dockerconfigjson' '" + copyTestSource + "' '" + copyTestDestination + "'\noc image mirror --keep-manifest-list=true -a '/run/copy/.dockerconfigjson' '" + copyTestSource + "' 'mirror-registry.test-mirror-registry.svc:5000/mirror-source.hypershift-e2e.invalid/libpod/registry'\nprintf '%s' '{}' > /tmp/copy-result.json\n" + copyScriptFooter},
		{name: "When copying a release, it should discover repositories before mirroring", auth: true, req: CopyRequest{Releases: []ReleaseCopy{{From: copyTestRelease, ToRepository: copyTestReleaseDestination}}}, want: releaseGolden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := copyScript(tc.req, tc.auth)
			if got != tc.want {
				t.Fatalf("script mismatch\ngot:\n%s\nwant:\n%s", got, tc.want)
			}
			cmd := exec.CommandContext(t.Context(), "/bin/bash", "-n")
			cmd.Stdin = strings.NewReader(got)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("invalid bash: %s, %v", out, err)
			}
		})
	}
	// Run only a local stub of oc. Rewrite filesystem paths into a temporary
	// directory; these tests cannot connect to a registry or a cluster.
	for _, tc := range []struct {
		name       string
		oversized  bool
		failMirror bool
	}{
		{name: "When component repositories repeat, it should emit sorted unique JSON"},
		{name: "When the result exceeds four KiB, it should fail without writing a termination result", oversized: true},
		{name: "When mirroring fails, it should stop before publishing a result", failMirror: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			pullspecs := "Name: 4.20.0\nPull From: " + copyTestRelease + "\nDigest: sha256:" + strings.Repeat("a", 64) + "\n\nNAME  PULL SPEC\nkube-apiserver quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:" + strings.Repeat("b", 64) + "\ncli quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:" + strings.Repeat("c", 64) + "\n"
			if tc.oversized {
				for i := range 100 {
					pullspecs += fmt.Sprintf("component quay.io/openshift-release-dev/component-%03d@sha256:aaaa\n", i)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, "pullspec-fixture"), []byte(pullspecs), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "ca.crt"), []byte("local fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			stub := "#!/bin/bash\nset -eu\nif [[ \"$*\" == *'release info'* ]]; then cat \"$FIXTURE\"; elif [[ \"$*\" == *'release mirror'* ]]; then echo 'unstable mirror text'; exit \"$MIRROR_EXIT\"; fi\n"
			if err := os.WriteFile(filepath.Join(dir, "oc"), []byte(stub), 0700); err != nil {
				t.Fatal(err)
			}
			script := copyScript(CopyRequest{Releases: []ReleaseCopy{{From: copyTestRelease, ToRepository: copyTestReleaseDestination}}}, false)
			script = strings.NewReplacer("/etc/pki/tls/certs/ca-bundle.crt", shellQuote(filepath.Join(dir, "ca.crt")), "/run/copy/ca.crt", shellQuote(filepath.Join(dir, "ca.crt")), "/dev/termination-log", shellQuote(filepath.Join(dir, "termination-log")), "/tmp/", dir+"/").Replace(script)
			cmd := exec.CommandContext(t.Context(), "/bin/bash", "-c", script)
			mirrorExit := "0"
			if tc.failMirror {
				mirrorExit = "1"
			}
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "FIXTURE="+filepath.Join(dir, "pullspec-fixture"), "MIRROR_EXIT="+mirrorExit)
			out, err := cmd.CombinedOutput()
			if tc.oversized || tc.failMirror {
				if err == nil {
					t.Fatal("expected script failure")
				}
				if tc.oversized && !strings.Contains(string(out), "exceeds 4096 bytes") {
					t.Fatalf("missing size diagnostic: %s", out)
				}
				if _, err := os.Stat(filepath.Join(dir, "termination-log")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unexpected termination result: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("script: %v: %s", err, out)
			}
			data, err := os.ReadFile(filepath.Join(dir, "termination-log"))
			if err != nil {
				t.Fatal(err)
			}
			var result CopyResult
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatalf("invalid JSON %s: %v", data, err)
			}
			want := []string{"quay.io/openshift-release-dev/ocp-release", "quay.io/openshift-release-dev/ocp-v4.0-art-dev"}
			if !reflect.DeepEqual(result.ReleaseSourceRepositories[copyTestRelease], want) {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
}

func TestCopierLabels(t *testing.T) {
	for _, tc := range []struct{ name, prefix string }{
		{name: "When the registry name is short, it should use the copier suffix", prefix: DefaultName},
		{name: "When the registry name is maximum length, it should truncate the copier label", prefix: strings.Repeat("a", 63)},
		{name: "When the registry name already ends in the truncated copier suffix, it should still be disjoint", prefix: strings.Repeat("a", 56) + "-copier"},
		{name: "When a sixty two character registry name ends in the copier suffix, it should still be disjoint", prefix: strings.Repeat("a", 55) + "-copier"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := copierLabels(tc.prefix)
			if len(validation.IsDNS1123Label(got["app.kubernetes.io/name"])) != 0 {
				t.Fatalf("invalid copier name label: %v", got)
			}
			if got[registryPartOfLabel] != registryPartOf || got["app.kubernetes.io/component"] != "copier" {
				t.Fatalf("unexpected copier labels: %v", got)
			}
			opts := Options{Name: tc.prefix}
			selectors := []map[string]string{buildService(opts).Spec.Selector, buildDeployment(opts, "").Spec.Selector.MatchLabels}
			for _, selector := range selectors {
				if labels.SelectorFromSet(selector).Matches(labels.Set(got)) {
					t.Fatalf("copier labels %v match registry selector %v", got, selector)
				}
			}
		})
	}
}

func TestCopyReadError(t *testing.T) {
	for _, tc := range []struct {
		name          string
		err           error
		cancel, retry bool
	}{
		{name: "When the read succeeds, it should return nil"},
		{name: "When the API times out, it should allow a retry", err: apierrors.NewTimeoutError("read timed out", 1), retry: true},
		{name: "When the server times out, it should allow a retry", err: apierrors.NewServerTimeout(schema.GroupResource{Resource: "jobs"}, "get", 1), retry: true},
		{name: "When requests are throttled, it should allow a retry", err: apierrors.NewTooManyRequests("throttled", 1), retry: true},
		{name: "When the API is unavailable, it should allow a retry", err: apierrors.NewServiceUnavailable("unavailable"), retry: true},
		{name: "When the API has an internal error, it should allow a retry", err: apierrors.NewInternalError(errors.New("temporary failure")), retry: true},
		{name: "When a network read fails, it should allow a retry", err: &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset")}, retry: true},
		{name: "When the Job is missing, it should retain the NotFound error", err: apierrors.NewNotFound(schema.GroupResource{Resource: "jobs"}, "mirror-registry-copy-0123456789")},
		{name: "When a read is forbidden, it should retain the error", err: apierrors.NewForbidden(schema.GroupResource{Resource: "jobs"}, "mirror-registry-copy-0123456789", errors.New("denied"))},
		{name: "When the context is canceled during a transient failure, it should return cancellation", err: apierrors.NewServiceUnavailable("unavailable"), cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			got := copyReadError(ctx, tc.err)
			want := tc.err
			if tc.retry {
				want = nil
			}
			if tc.cancel {
				want = context.Canceled
			}
			if !errors.Is(got, want) {
				t.Fatalf("got %v, want %v", got, want)
			}
		})
	}
}

func TestBuildCopyJob(t *testing.T) {
	for _, tc := range []struct {
		name string
		auth bool
	}{
		{name: "When sources are anonymous, it should mount only the CA"},
		{name: "When sources require authentication, it should also mount the pull secret", auth: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := buildCopyJob(DefaultName, "mirror-registry-copy-0123456789", DefaultNamespace, "registry.ci.openshift.org/ocp/4.20:cli", 570, "set -euo pipefail", tc.auth)
			if job.Name != "mirror-registry-copy-0123456789" || job.Namespace != DefaultNamespace || *job.Spec.BackoffLimit != 0 || *job.Spec.ActiveDeadlineSeconds != 570 {
				t.Fatalf("unexpected Job: %+v", job)
			}
			wantLabels := map[string]string{"app.kubernetes.io/name": "mirror-registry-copier", registryPartOfLabel: registryPartOf, "app.kubernetes.io/component": "copier"}
			if !reflect.DeepEqual(job.Labels, wantLabels) || !reflect.DeepEqual(job.Spec.Template.Labels, wantLabels) {
				t.Fatalf("unexpected labels: %+v", job.Labels)
			}
			pod := job.Spec.Template.Spec
			wantSecurity := &corev1.PodSecurityContext{RunAsNonRoot: ptr.To(true), SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}}
			if pod.RestartPolicy != corev1.RestartPolicyNever || !reflect.DeepEqual(pod.SecurityContext, wantSecurity) {
				t.Fatalf("unexpected pod security: %+v", pod)
			}
			if len(pod.Containers) != 1 || len(pod.Volumes) != 1 {
				t.Fatalf("unexpected containers/volumes: %+v", pod)
			}
			container := pod.Containers[0]
			if container.Name != "copy" || container.Image != "registry.ci.openshift.org/ocp/4.20:cli" || !reflect.DeepEqual(container.Command, []string{"/bin/bash", "-c", "set -euo pipefail"}) {
				t.Fatalf("unexpected command: %+v", container)
			}
			if *container.SecurityContext.AllowPrivilegeEscalation || !reflect.DeepEqual(container.SecurityContext.Capabilities.Drop, []corev1.Capability{"ALL"}) || container.SecurityContext.RunAsUser != nil {
				t.Fatalf("unexpected container security: %+v", container.SecurityContext)
			}
			if container.Resources.Requests.Cpu().String() != "100m" || container.Resources.Requests.Memory().String() != "256Mi" || container.TerminationMessagePolicy != corev1.TerminationMessageFallbackToLogsOnError || container.TerminationMessagePath != "/dev/termination-log" {
				t.Fatalf("unexpected resources/termination policy: %+v", container)
			}
			if !reflect.DeepEqual(container.Env, []corev1.EnvVar{{Name: "HOME", Value: "/tmp"}}) || !reflect.DeepEqual(container.VolumeMounts, []corev1.VolumeMount{{Name: "copy", MountPath: "/run/copy", ReadOnly: true}}) {
				t.Fatalf("unexpected env/mounts: %+v", container)
			}
			wantItems := []corev1.KeyToPath{{Key: "ca.crt", Path: "ca.crt"}}
			if tc.auth {
				wantItems = append(wantItems, corev1.KeyToPath{Key: corev1.DockerConfigJsonKey, Path: corev1.DockerConfigJsonKey})
			}
			if pod.Volumes[0].Secret.SecretName != job.Name || !reflect.DeepEqual(pod.Volumes[0].Secret.Items, wantItems) {
				t.Fatalf("unexpected secret volume: %+v", pod.Volumes[0])
			}
			selector := buildService(Options{Name: DefaultName}).Spec.Selector
			if labels.SelectorFromSet(selector).Matches(labels.Set(job.Spec.Template.Labels)) {
				t.Fatal("copier pod matches the registry Service selector")
			}
			job.Spec.Template.Labels["app.kubernetes.io/name"] = "mutated-template"
			if !reflect.DeepEqual(job.Labels, wantLabels) {
				t.Fatal("pod-template labels share a map with Job labels")
			}
		})
	}
}

func TestJobCopierWaitForResult(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		condition                  batchv1.JobConditionType
		reason, message, wantError string
		cancel                     bool
		noPod                      bool
		getError, listError        error
	}{
		{name: "When the Job completes with valid JSON, it should return the result and delete resources", condition: batchv1.JobComplete, message: `{"releaseSourceRepositories":{"quay.io/openshift-release-dev/ocp-release@sha256:aaaa":["quay.io/openshift-release-dev/ocp-release"]}}`},
		{name: "When the Job exceeds its deadline, it should report the reason and logs and retain resources", condition: batchv1.JobFailed, reason: "DeadlineExceeded", message: "copy did not finish", wantError: "DeadlineExceeded"},
		{name: "When pushes return HTTP 405, it should report that the registry is read-only", condition: batchv1.JobFailed, reason: "BackoffLimitExceeded", message: "HTTP 405 Method not allowed", wantError: "registry is read-only:"},
		{name: "When the context is canceled, it should wrap cancellation even for a completed Job and retain resources", condition: batchv1.JobComplete, message: "{}", cancel: true, wantError: "context canceled"},
		{name: "When a Job read times out once, it should retry and return the completed result", condition: batchv1.JobComplete, message: "{}", getError: apierrors.NewTimeoutError("temporary Job read timeout", 1)},
		{name: "When a pod list is throttled once, it should retry and return the completed result", condition: batchv1.JobComplete, message: "{}", listError: apierrors.NewTooManyRequests("temporary pod list throttling", 1)},
		{name: "When a Job read is forbidden, it should return the error and retain resources", getError: apierrors.NewForbidden(schema.GroupResource{Group: "batch", Resource: "jobs"}, "mirror-registry-copy-0123456789", errors.New("denied")), wantError: "forbidden"},
		{name: "When the deadline controller deletes the pod, it should explain the missing termination message", condition: batchv1.JobFailed, reason: "DeadlineExceeded", noPod: true, wantError: "no termination message (pod deleted or message unavailable)"},
		{name: "When the completed pod has no message, it should fail and retain resources", condition: batchv1.JobComplete, wantError: "empty termination message"},
		{name: "When the completed pod message is invalid JSON, it should fail and retain resources", condition: batchv1.JobComplete, message: "unstable oc output", wantError: "decode"},
		{name: "When the completed pod message is null, it should reject it as a result", condition: batchv1.JobComplete, message: "null", wantError: "expected a JSON object"},
		{name: "When the completed Job has no pod, it should fail and retain resources", condition: batchv1.JobComplete, noPod: true, wantError: "empty termination message"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := batchv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			job := buildCopyJob(DefaultName, "mirror-registry-copy-0123456789", DefaultNamespace, "registry.ci.openshift.org/ocp/4.20:cli", 570, "", false)
			job.UID = "current-job"
			job.Status.Conditions = []batchv1.JobCondition{{Type: tc.condition, Status: corev1.ConditionTrue, Reason: tc.reason}}
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: job.Name, Namespace: job.Namespace}}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: job.Name + "-pod", Namespace: job.Namespace, Labels: map[string]string{"batch.kubernetes.io/job-name": job.Name}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: job.Name, UID: job.UID, Controller: ptr.To(true)}}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "copy", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Message: tc.message}}}}}}
			objects := []crclient.Object{job, secret}
			if !tc.noPod {
				objects = append(objects, pod)
			}
			// A pod from a previous retry must not supply the current result.
			stale := pod.DeepCopy()
			stale.Name += "-old"
			stale.OwnerReferences[0].UID = "previous-job"
			stale.Status.ContainerStatuses[0].State.Terminated.Message = "stale output"
			objects = append(objects, stale)
			getAttempts, listAttempts := 0, 0
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, opts ...crclient.GetOption) error {
					getAttempts++
					if getAttempts == 1 && tc.getError != nil {
						return tc.getError
					}
					return client.Get(ctx, key, object, opts...)
				},
				List: func(ctx context.Context, client crclient.WithWatch, list crclient.ObjectList, opts ...crclient.ListOption) error {
					listAttempts++
					if listAttempts == 1 && tc.listError != nil {
						return tc.listError
					}
					return client.List(ctx, list, opts...)
				},
			}).Build()
			copier := newJobCopier(client, "registry.ci.openshift.org/ocp/4.20:cli", DefaultName)
			copier.pollInterval = time.Millisecond
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			result, err := copier.waitForResult(ctx, job.Namespace, job.Name)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("got %v, want %q", err, tc.wantError)
				}
				if tc.condition == batchv1.JobFailed && !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("missing log tail: %v", err)
				}
				if tc.wantError == "registry is read-only:" && !strings.HasPrefix(err.Error(), tc.wantError) {
					t.Fatalf("missing read-only prefix: %v", err)
				}
				if tc.cancel && !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation not wrapped: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				var want CopyResult
				if err := json.Unmarshal([]byte(tc.message), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(result, &want) {
					t.Fatalf("got %+v, want %+v", result, want)
				}
			}
			for _, object := range []crclient.Object{&batchv1.Job{}, &corev1.Secret{}} {
				err := client.Get(t.Context(), crclient.ObjectKeyFromObject(job), object)
				if tc.wantError == "" && !apierrors.IsNotFound(err) {
					t.Fatalf("resource not deleted: %T: %v", object, err)
				}
				if tc.wantError != "" && err != nil {
					t.Fatalf("debug resource not kept: %T: %v", object, err)
				}
			}
		})
	}
}

func TestJobCopierCopy(t *testing.T) {
	for _, tc := range []struct {
		name        string
		auth        bool
		noCA        bool
		createError bool
	}{
		{name: "When copying anonymously, it should replace old resources and clean up successful copies"},
		{name: "When copying with credentials, it should mount the supplied pull secret", auth: true},
		{name: "When the CA is empty, it should fail before creating resources", noCA: true},
		{name: "When Job creation fails, it should retain the Secret for debugging", createError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := batchv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			req := CopyRequest{Namespace: DefaultNamespace, CABundlePEM: []byte("PEM fixture"), Images: []ImageCopy{{From: copyTestSource, To: copyTestDestination}}}
			if tc.auth {
				req.SourcePullSecret = []byte(`{"auths":{"quay.io":{}}}`)
			}
			if tc.noCA {
				req.CABundlePEM = nil
			}
			name, err := copyResourceName(DefaultName, req)
			if err != nil {
				t.Fatal(err)
			}
			oldJob := buildCopyJob(DefaultName, name, req.Namespace, "old-image", 60, "old-script", false)
			oldSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: req.Namespace}, Data: map[string][]byte{"ca.crt": []byte("stale CA")}}
			created := false
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(oldJob, oldSecret).WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, client crclient.WithWatch, object crclient.Object, opts ...crclient.CreateOption) error {
					created = true
					if secret, ok := object.(*corev1.Secret); ok {
						if secret.Type != corev1.SecretTypeOpaque || string(secret.Data["ca.crt"]) != string(req.CABundlePEM) || string(secret.Data[corev1.DockerConfigJsonKey]) != string(req.SourcePullSecret) || secret.Labels["app.kubernetes.io/name"] != DefaultName+"-copier" || secret.Labels["app.kubernetes.io/component"] != "copier" {
							t.Fatalf("unexpected Secret: %+v", secret)
						}
					}
					if job, ok := object.(*batchv1.Job); ok {
						if tc.createError {
							return errors.New("Job creation denied")
						}
						if job.Labels["app.kubernetes.io/name"] != DefaultName+"-copier" || job.Spec.Template.Labels["app.kubernetes.io/name"] != DefaultName+"-copier" {
							t.Fatalf("unexpected prefix labels: %+v", job)
						}
						job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
						pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name + "-pod", Namespace: req.Namespace, Labels: map[string]string{"batch.kubernetes.io/job-name": name}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "copy", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Message: "{}"}}}}}}
						if err := client.Create(ctx, pod); err != nil {
							return err
						}
					}
					return client.Create(ctx, object, opts...)
				},
			}).Build()
			copier := newJobCopier(client, "registry.ci.openshift.org/ocp/4.20:cli", DefaultName)
			copier.pollInterval = time.Millisecond
			_, err = copier.Copy(t.Context(), req)
			if tc.noCA {
				if err == nil || !strings.Contains(err.Error(), "CA bundle is required") || created {
					t.Fatalf("expected CA validation before creation: %v", err)
				}
				return
			}
			if tc.createError {
				if err == nil || !strings.Contains(err.Error(), "Job creation denied") {
					t.Fatalf("expected create error: %v", err)
				}
				if err := client.Get(t.Context(), crclient.ObjectKey{Namespace: req.Namespace, Name: name}, &corev1.Secret{}); err != nil {
					t.Fatalf("Secret not retained: %v", err)
				}
				return
			}
			if err != nil || !created {
				t.Fatalf("copy failed: %v", err)
			}
			for _, object := range []crclient.Object{&batchv1.Job{}, &corev1.Secret{}} {
				if err := client.Get(t.Context(), crclient.ObjectKey{Namespace: req.Namespace, Name: name}, object); !apierrors.IsNotFound(err) {
					t.Fatalf("resource not cleaned up: %T: %v", object, err)
				}
			}
		})
	}
}

func TestJobCopierDeleteCopyResources(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing bool
	}{
		{name: "When resources exist, it should delete both with background Job propagation", existing: true},
		{name: "When resources are absent, it should ignore NotFound"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := batchv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			metadata := metav1.ObjectMeta{Name: "mirror-registry-copy-0123456789", Namespace: DefaultNamespace}
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tc.existing {
				builder = builder.WithObjects(&batchv1.Job{ObjectMeta: metadata}, &corev1.Secret{ObjectMeta: metadata})
			}
			jobDeleted := false
			client := builder.WithInterceptorFuncs(interceptor.Funcs{Delete: func(ctx context.Context, client crclient.WithWatch, object crclient.Object, opts ...crclient.DeleteOption) error {
				if _, ok := object.(*batchv1.Job); ok {
					options := (&crclient.DeleteOptions{}).ApplyOptions(opts)
					if options.PropagationPolicy == nil || *options.PropagationPolicy != metav1.DeletePropagationBackground {
						t.Fatal("Job delete must use Background propagation")
					}
					jobDeleted = true
				}
				return client.Delete(ctx, object, opts...)
			}}).Build()
			if err := newJobCopier(client, "", DefaultName).deleteCopyResources(t.Context(), metadata.Namespace, metadata.Name); err != nil {
				t.Fatal(err)
			}
			if !jobDeleted {
				t.Fatal("Job delete was not attempted")
			}
			for _, object := range []crclient.Object{&batchv1.Job{}, &corev1.Secret{}} {
				if err := client.Get(t.Context(), crclient.ObjectKey{Namespace: metadata.Namespace, Name: metadata.Name}, object); !apierrors.IsNotFound(err) {
					t.Fatalf("resource remains: %T: %v", object, err)
				}
			}
		})
	}
}
