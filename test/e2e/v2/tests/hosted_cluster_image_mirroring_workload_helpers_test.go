//go:build e2ev2

package tests

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/util/mirrorregistry"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// imageMirroringTestManifestList is the verbatim manifest list of
// quay.io/libpod/registry:2.8.2 (skopeo inspect --raw). Its sha256 is the
// digest pinned in mirrorregistry.DefaultRegistryImage.
const imageMirroringTestManifestList = `{"manifests":[{"digest":"sha256:da1fbcd13a7ddc77d0d964a5c5c4cb707b5d440a028b0b42fe574b9e99077e27","mediaType":"application\/vnd.docker.distribution.manifest.v2+json","platform":{"architecture":"amd64","os":"linux"},"size":1363},{"digest":"sha256:ea32c4ea1ffa10d85c25b55e3e775a72013e685bf80f8da79ec5c5ddc4a4a63f","mediaType":"application\/vnd.docker.distribution.manifest.v2+json","platform":{"architecture":"arm","os":"linux","variant":"v6"},"size":1363},{"digest":"sha256:d72af0ec30bf3a5531f2d4dc4d0893f11b40cfd0a5dcbcbdcc19adde233847d2","mediaType":"application\/vnd.docker.distribution.manifest.v2+json","platform":{"architecture":"arm","os":"linux","variant":"v7"},"size":1363},{"digest":"sha256:fab2c6a4bb4acf3ee3ae611943e2596a73e9adb4da74432b1bcd686539f7f56f","mediaType":"application\/vnd.docker.distribution.manifest.v2+json","platform":{"architecture":"arm64","os":"linux","variant":"v8"},"size":1363},{"digest":"sha256:a353b4af55af90d0d30511d2191edb9da38f174390ce91e2329fa1839b6c2408","mediaType":"application\/vnd.docker.distribution.manifest.v2+json","platform":{"architecture":"ppc64le","os":"linux"},"size":1363},{"digest":"sha256:4cab339812e3bfa9e2242292f46ca2a99e804a4604d29f342f2b935381ebad3c","mediaType":"application\/vnd.docker.distribution.manifest.v2+json","platform":{"architecture":"s390x","os":"linux"},"size":1363}],"mediaType":"application\/vnd.docker.distribution.manifest.list.v2+json","schemaVersion":2}`

const (
	imageMirroringTestListDigest  = "sha256:e642e8604d305a3b82c8c1807b5df7a1a84cc650d57a60f9c5c2b78efec54b3f"
	imageMirroringTestAMD64Digest = "sha256:da1fbcd13a7ddc77d0d964a5c5c4cb707b5d440a028b0b42fe574b9e99077e27"
	imageMirroringTestARM64Digest = "sha256:fab2c6a4bb4acf3ee3ae611943e2596a73e9adb4da74432b1bcd686539f7f56f"
)

func TestImageMirroringVirtualMirror(t *testing.T) {
	tests := []struct {
		name      string
		change    func([]hyperv1.ImageContentSource) []hyperv1.ImageContentSource
		routeHost string
		wantHost  string
		wantPath  string
		wantErr   string
	}{
		{name: "When the virtual source has a mirror at the Route host, it should return its host and path", routeHost: "mirror.apps.ci.hypershift.dev", wantHost: "mirror.apps.ci.hypershift.dev", wantPath: "libpod/registry"},
		{name: "When the virtual source lists another mirror first, it should select the one at the Route host", routeHost: "mirror.apps.ci.hypershift.dev", wantHost: "mirror.apps.ci.hypershift.dev", wantPath: "libpod/registry", change: func(ics []hyperv1.ImageContentSource) []hyperv1.ImageContentSource {
			ics[1].Mirrors = append([]string{"backup.apps.ci.hypershift.dev/libpod/registry"}, ics[1].Mirrors...)
			return ics
		}},
		{name: "When the Route host is empty, it should return an error", routeHost: " ", wantErr: "Route host"},
		{name: "When no mirror is at the Route host, it should return an error", routeHost: "other.apps.ci.hypershift.dev", wantErr: "no mirror at Route host"},
		{name: "When a mirror only shares the host prefix, it should return an error", routeHost: "mirror.apps.ci.hypershift.dev", wantErr: "no mirror at Route host", change: func(ics []hyperv1.ImageContentSource) []hyperv1.ImageContentSource {
			ics[1].Mirrors = []string{"mirror.apps.ci.hypershift.dev.evil/libpod/registry"}
			return ics
		}},
		{name: "When the mirror has no repository path, it should return an error", routeHost: "mirror.apps.ci.hypershift.dev", wantErr: "no mirror at Route host", change: func(ics []hyperv1.ImageContentSource) []hyperv1.ImageContentSource {
			ics[1].Mirrors = []string{"mirror.apps.ci.hypershift.dev"}
			return ics
		}},
		{name: "When the virtual source is missing, it should return an error", routeHost: "mirror.apps.ci.hypershift.dev", wantErr: "no entry for virtual source", change: func(ics []hyperv1.ImageContentSource) []hyperv1.ImageContentSource {
			return ics[:1]
		}},
		{name: "When the sources are nil, it should return an error", routeHost: "mirror.apps.ci.hypershift.dev", wantErr: "no entry for virtual source", change: func([]hyperv1.ImageContentSource) []hyperv1.ImageContentSource {
			return nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ics := imageMirroringTestHC.DeepCopy().Spec.ImageContentSources
			if tt.change != nil {
				ics = tt.change(ics)
			}
			host, path, err := imageMirroringVirtualMirror(ics, tt.routeHost)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil || host != tt.wantHost || path != tt.wantPath {
				t.Fatalf("got %q %q %v, want %q %q", host, path, err, tt.wantHost, tt.wantPath)
			}
		})
	}
	t.Run("When the virtual source constant is inspected, it should use an unresolvable .invalid host", func(t *testing.T) {
		host, _, _ := strings.Cut(imageMirroringVirtualSource, "/")
		if !strings.HasSuffix(host, ".invalid") {
			t.Fatalf("virtual source host %q must end in .invalid", host)
		}
	})
}

func TestImageMirroringDigestRef(t *testing.T) {
	tests := []struct {
		name       string
		repository string
		image      string
		wantRef    string
		wantErr    string
	}{
		{name: "When the image is the pinned fixture image, it should build a by-digest reference on the repository", repository: imageMirroringVirtualSource, image: mirrorregistry.DefaultRegistryImage, wantRef: imageMirroringVirtualSource + "@" + imageMirroringTestListDigest},
		{name: "When the repository host has a port, it should accept it", repository: "registry.ci.local:5000/libpod/registry", image: mirrorregistry.DefaultRegistryImage, wantRef: "registry.ci.local:5000/libpod/registry@" + imageMirroringTestListDigest},
		{name: "When the image has no digest, it should return an error", repository: imageMirroringVirtualSource, image: "quay.io/libpod/registry:2.8.2", wantErr: "sha256 digest"},
		{name: "When the digest is malformed, it should return an error", repository: imageMirroringVirtualSource, image: "quay.io/libpod/registry@sha256:E642", wantErr: "sha256 digest"},
		{name: "When the repository is blank, it should return an error", repository: " ", image: mirrorregistry.DefaultRegistryImage, wantErr: "nonblank"},
		{name: "When the repository has a digest, it should return an error", repository: imageMirroringVirtualSource + "@" + imageMirroringTestListDigest, image: mirrorregistry.DefaultRegistryImage, wantErr: "digest"},
		{name: "When the repository has a tag, it should return an error", repository: imageMirroringVirtualSource + ":2.8.2", image: mirrorregistry.DefaultRegistryImage, wantErr: "tag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, digest, err := imageMirroringDigestRef(tt.repository, tt.image)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil || ref != tt.wantRef || digest != imageMirroringTestListDigest {
				t.Fatalf("got %q %q %v, want %q", ref, digest, err, tt.wantRef)
			}
		})
	}
}

func TestImageMirroringImageIDDigest(t *testing.T) {
	tests := []struct {
		name    string
		imageID string
		want    string
		wantErr bool
	}{
		{name: "When the imageID is a CRI-O repository digest, it should return the digest", imageID: imageMirroringVirtualSource + "@" + imageMirroringTestAMD64Digest, want: imageMirroringTestAMD64Digest},
		{name: "When the imageID has the docker-pullable prefix, it should return the digest", imageID: "docker-pullable://" + imageMirroringVirtualSource + "@" + imageMirroringTestAMD64Digest, want: imageMirroringTestAMD64Digest},
		{name: "When the imageID is a bare image ID, it should return an error", imageID: imageMirroringTestAMD64Digest, wantErr: true},
		{name: "When the imageID is empty, it should return an error", imageID: "", wantErr: true},
		{name: "When the imageID digest is malformed, it should return an error", imageID: imageMirroringVirtualSource + "@sha256:abc", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := imageMirroringImageIDDigest(tt.imageID)
			if tt.wantErr != (err != nil) || got != tt.want {
				t.Fatalf("got %q %v, want %q (error %v)", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestImageMirroringTrustBundle(t *testing.T) {
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "image-mirroring"},
		Spec:       hyperv1.HostedClusterSpec{AdditionalTrustBundle: &corev1.LocalObjectReference{Name: "user-ca-bundle"}},
	}
	bundle := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "user-ca-bundle"}, Data: map[string]string{"ca-bundle.crt": "-----BEGIN CERTIFICATE-----\n"}}
	tests := []struct {
		name    string
		hc      *hyperv1.HostedCluster
		objects []runtime.Object
		want    string
		wantErr string
	}{
		{name: "When the HC references a populated ConfigMap, it should return its CA bundle", hc: hc, objects: []runtime.Object{bundle}, want: "-----BEGIN CERTIFICATE-----\n"},
		{name: "When the HC is nil, it should return an error", hc: nil, wantErr: "additionalTrustBundle"},
		{name: "When the HC has no trust bundle, it should return an error", hc: &hyperv1.HostedCluster{}, wantErr: "additionalTrustBundle"},
		{name: "When the ConfigMap is missing, it should return an error", hc: hc, wantErr: "clusters/user-ca-bundle"},
		{name: "When the ConfigMap key is empty, it should return an error", hc: hc, objects: []runtime.Object{&corev1.ConfigMap{ObjectMeta: bundle.ObjectMeta}}, wantErr: "empty ca-bundle.crt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().WithRuntimeObjects(tt.objects...).Build()
			got, err := imageMirroringTrustBundle(t.Context(), client, tt.hc)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil || string(got) != tt.want {
				t.Fatalf("got %q %v, want %q", got, err, tt.want)
			}
		})
	}
}

func TestImageMirroringHTTPClient(t *testing.T) {
	t.Run("When the trust bundle has no PEM certificate, it should return an error", func(t *testing.T) {
		if _, err := imageMirroringHTTPClient([]byte("not a certificate")); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("When the trust bundle has a certificate, it should return a client with a timeout", func(t *testing.T) {
		server := httptest.NewTLSServer(http.NotFoundHandler())
		defer server.Close()
		client, err := imageMirroringHTTPClient(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
		if err != nil || client.Timeout == 0 {
			t.Fatalf("got %+v %v", client, err)
		}
	})
}

func TestImageMirroringFetchManifest(t *testing.T) {
	const path = "/v2/mirror-source.hypershift-e2e.invalid/libpod/registry/manifests/" + imageMirroringTestListDigest
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.Error(w, `{"errors":[{"code":"MANIFEST_UNKNOWN"}]}`, http.StatusNotFound)
			return
		}
		accept := r.Header.Get("Accept")
		if !strings.Contains(accept, imageMirroringManifestListType) || !strings.Contains(accept, imageMirroringOCIIndexType) {
			http.Error(w, "missing list media types in Accept: "+accept, http.StatusNotAcceptable)
			return
		}
		_, _ = w.Write([]byte(imageMirroringTestManifestList))
	})
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	caOf := func(s *httptest.Server) []byte {
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw})
	}
	host := strings.TrimPrefix(server.URL, "https://")

	tests := []struct {
		name       string
		ca         []byte
		repository string
		wantErr    string
	}{
		{name: "When the mirror serves the list with the trusted CA, it should return the exact body", ca: caOf(server), repository: imageMirroringVirtualSource},
		{name: "When the manifest is unknown, it should return the status and body", ca: caOf(server), repository: "libpod/other", wantErr: "status 404"},
		{name: "When the server certificate is not signed by the trust bundle, it should return a TLS error", ca: imageMirroringTestUnrelatedCA(t), repository: imageMirroringVirtualSource, wantErr: "certificate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := imageMirroringHTTPClient(tt.ca)
			if err != nil {
				t.Fatal(err)
			}
			body, err := imageMirroringFetchManifest(t.Context(), client, host, tt.repository, imageMirroringTestListDigest)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil || string(body) != imageMirroringTestManifestList {
				t.Fatalf("got %d bytes, %v", len(body), err)
			}
		})
	}
}

// imageMirroringTestUnrelatedCA returns a self-signed CA that did not sign
// the httptest certificate (all httptest servers share one certificate).
func imageMirroringTestUnrelatedCA(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "unrelated-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestImageMirroringPlatformDigest(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		digest  string
		os      string
		arch    string
		want    string
		wantErr string
	}{
		{name: "When the node is linux/amd64, it should return the amd64 manifest digest", body: imageMirroringTestManifestList, digest: imageMirroringTestListDigest, os: "linux", arch: "amd64", want: imageMirroringTestAMD64Digest},
		{name: "When the node is linux/arm64, it should return the arm64 manifest digest", body: imageMirroringTestManifestList, digest: imageMirroringTestListDigest, os: "linux", arch: "arm64", want: imageMirroringTestARM64Digest},
		{name: "When the body does not hash to the requested digest, it should return an error", body: imageMirroringTestManifestList + " ", digest: imageMirroringTestListDigest, os: "linux", arch: "amd64", wantErr: "does not match"},
		{name: "When several entries match the platform, it should return an error", body: imageMirroringTestManifestList, digest: imageMirroringTestListDigest, os: "linux", arch: "arm", wantErr: "2 entries"},
		{name: "When no entry matches the platform, it should return an error", body: imageMirroringTestManifestList, digest: imageMirroringTestListDigest, os: "windows", arch: "amd64", wantErr: "0 entries"},
		{name: "When the manifest is not a list, it should return an error", body: `{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json"}`, os: "linux", arch: "amd64", wantErr: "not a nonempty manifest list"},
		{name: "When the body is not JSON, it should return an error", body: "not json", os: "linux", arch: "amd64", wantErr: "decode"},
		{name: "When the matching entry digest is malformed, it should return an error", body: `{"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"digest":"sha256:abc","platform":{"os":"linux","architecture":"amd64"}}]}`, os: "linux", arch: "amd64", wantErr: "malformed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			digest := tt.digest
			if digest == "" {
				digest = imageMirroringTestSHA256(tt.body)
			}
			got, err := imageMirroringPlatformDigest([]byte(tt.body), digest, tt.os, tt.arch)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q %v, want %q", got, err, tt.want)
			}
		})
	}
	t.Run("When the test manifest list is hashed, it should match the pinned fixture image digest", func(t *testing.T) {
		if _, digest, _ := strings.Cut(mirrorregistry.DefaultRegistryImage, "@"); imageMirroringTestSHA256(imageMirroringTestManifestList) != digest {
			t.Fatalf("test manifest list does not hash to %s", digest)
		}
	})
}

func imageMirroringTestSHA256(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestImageMirroringReadyLinuxNodes(t *testing.T) {
	node := func(os string, ready corev1.ConditionStatus) corev1.Node {
		return corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{corev1.LabelOSStable: os}},
			Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}}},
		}
	}
	tests := []struct {
		name  string
		nodes []corev1.Node
		want  int
	}{
		{name: "When nodes are Ready linux nodes, it should count them", nodes: []corev1.Node{node("linux", corev1.ConditionTrue), node("linux", corev1.ConditionTrue)}, want: 2},
		{name: "When a linux node is not Ready, it should not count it", nodes: []corev1.Node{node("linux", corev1.ConditionFalse), node("linux", corev1.ConditionUnknown)}, want: 0},
		{name: "When a Ready node is not linux, it should not count it", nodes: []corev1.Node{node("windows", corev1.ConditionTrue), {}}, want: 0},
		{name: "When there are no nodes, it should return zero", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := imageMirroringReadyLinuxNodes(tt.nodes); got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestImageMirroringWorkloadPod(t *testing.T) {
	t.Run("When a workload pod is built, it should be a restricted one-shot pod that always pulls the image", func(t *testing.T) {
		pod := imageMirroringWorkloadPod("e2e-image-mirroring-x", "by-digest", imageMirroringVirtualSource+"@"+imageMirroringTestListDigest)
		if pod.Namespace != "e2e-image-mirroring-x" || pod.Name != "by-digest" || pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
			t.Fatalf("unexpected identity or restart policy: %+v", pod.ObjectMeta)
		}
		if pod.Spec.NodeSelector[corev1.LabelOSStable] != "linux" || pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
			t.Fatalf("expected linux node selector and no service account token: %+v", pod.Spec)
		}
		psc := pod.Spec.SecurityContext
		if psc == nil || psc.RunAsNonRoot == nil || !*psc.RunAsNonRoot || psc.RunAsUser != nil || psc.SeccompProfile == nil || psc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
			t.Fatalf("pod security context is not restricted: %+v", psc)
		}
		if len(pod.Spec.Containers) != 1 {
			t.Fatalf("expected one container, got %d", len(pod.Spec.Containers))
		}
		c := pod.Spec.Containers[0]
		if c.Name != imageMirroringWorkloadContainer || c.Image != imageMirroringVirtualSource+"@"+imageMirroringTestListDigest || c.ImagePullPolicy != corev1.PullAlways {
			t.Fatalf("unexpected container image settings: %+v", c)
		}
		if !reflect.DeepEqual(c.Command, []string{"/bin/registry", "--version"}) {
			t.Fatalf("unexpected command %v", c.Command)
		}
		csc := c.SecurityContext
		if csc == nil || csc.AllowPrivilegeEscalation == nil || *csc.AllowPrivilegeEscalation || csc.Capabilities == nil || !reflect.DeepEqual(csc.Capabilities.Drop, []corev1.Capability{"ALL"}) {
			t.Fatalf("container security context is not restricted: %+v", csc)
		}
	})
}

func imageMirroringTestPod(phase corev1.PodPhase, state *corev1.ContainerState) *corev1.Pod {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "e2e-image-mirroring-x", Name: "pod"}, Status: corev1.PodStatus{Phase: phase}}
	if state != nil {
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: imageMirroringWorkloadContainer, State: *state}}
	}
	return pod
}

func imageMirroringWaitingState(reason, message string) *corev1.ContainerState {
	return &corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: message}}
}

func imageMirroringTerminatedState(exitCode int32) *corev1.ContainerState {
	return &corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "Completed", ExitCode: exitCode}}
}

func imageMirroringRunningState() *corev1.ContainerState {
	return &corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}
}

func TestImageMirroringClassifyContainer(t *testing.T) {
	status := func(state *corev1.ContainerState) *corev1.ContainerStatus {
		return &corev1.ContainerStatus{Name: imageMirroringWorkloadContainer, State: *state}
	}
	tests := []struct {
		name   string
		status *corev1.ContainerStatus
		want   imageMirroringContainerOutcome
	}{
		{name: "When there is no status, it should be pending", status: nil, want: imageMirroringContainerPending},
		{name: "When the status has no state, it should be pending", status: &corev1.ContainerStatus{}, want: imageMirroringContainerPending},
		{name: "When the container is creating, it should be pending", status: status(imageMirroringWaitingState("ContainerCreating", "")), want: imageMirroringContainerPending},
		{name: "When the image pull fails, it should be a pull failure", status: status(imageMirroringWaitingState("ErrImagePull", "")), want: imageMirroringContainerPullFailed},
		{name: "When the image pull backs off, it should be a pull failure", status: status(imageMirroringWaitingState("ImagePullBackOff", "")), want: imageMirroringContainerPullFailed},
		{name: "When the image name is invalid, it should be an image rejection", status: status(imageMirroringWaitingState("InvalidImageName", "")), want: imageMirroringContainerImageRejected},
		{name: "When the pull policy forbids pulling a missing image, it should be an image rejection", status: status(imageMirroringWaitingState("ErrImageNeverPull", "")), want: imageMirroringContainerImageRejected},
		{name: "When the container config cannot be created, it should be a create failure", status: status(imageMirroringWaitingState("CreateContainerConfigError", "")), want: imageMirroringContainerCreateFailed},
		{name: "When the container cannot be created, it should be a create failure", status: status(imageMirroringWaitingState("CreateContainerError", "")), want: imageMirroringContainerCreateFailed},
		{name: "When the container is running, it should be running", status: status(imageMirroringRunningState()), want: imageMirroringContainerRunning},
		{name: "When the container exited 0, it should have succeeded", status: status(imageMirroringTerminatedState(0)), want: imageMirroringContainerSucceeded},
		{name: "When the container exited nonzero, it should have failed", status: status(imageMirroringTerminatedState(2)), want: imageMirroringContainerFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := imageMirroringClassifyContainer(tt.status); got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestImageMirroringCheckPulledPod(t *testing.T) {
	unschedulable := imageMirroringTestPod(corev1.PodPending, nil)
	unschedulable.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable", Message: "0/1 nodes are available"}}
	otherContainer := imageMirroringTestPod(corev1.PodPending, nil)
	otherContainer.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "other", State: *imageMirroringTerminatedState(0)}}

	tests := []struct {
		name       string
		pod        *corev1.Pod
		wantDone   bool
		wantReason string
		wantErr    string
	}{
		{name: "When the container exited 0 and the pod Succeeded, it should be done", pod: imageMirroringTestPod(corev1.PodSucceeded, imageMirroringTerminatedState(0)), wantDone: true},
		{name: "When the pod is Pending without container statuses, it should keep polling", pod: imageMirroringTestPod(corev1.PodPending, nil), wantReason: "has no status"},
		{name: "When the pod is unschedulable, it should keep polling and report the reason", pod: unschedulable, wantReason: "Unschedulable: 0/1 nodes are available"},
		{name: "When only another container has a status, it should keep polling", pod: otherContainer, wantReason: "has no status"},
		{name: "When the container is creating, it should keep polling", pod: imageMirroringTestPod(corev1.PodPending, imageMirroringWaitingState("ContainerCreating", "")), wantReason: "ContainerCreating"},
		{name: "When the image pull fails, it should keep polling and report the message", pod: imageMirroringTestPod(corev1.PodPending, imageMirroringWaitingState("ErrImagePull", "no such host")), wantReason: "ErrImagePull: no such host"},
		{name: "When the image pull backs off, it should keep polling", pod: imageMirroringTestPod(corev1.PodPending, imageMirroringWaitingState("ImagePullBackOff", "")), wantReason: "ImagePullBackOff"},
		{name: "When the container is running, it should keep polling", pod: imageMirroringTestPod(corev1.PodRunning, imageMirroringRunningState()), wantReason: "running"},
		{name: "When the container exited 0 but the pod has not Succeeded yet, it should keep polling", pod: imageMirroringTestPod(corev1.PodRunning, imageMirroringTerminatedState(0)), wantReason: "exitCode=0"},
		{name: "When the container config cannot be created, it should fail terminally", pod: imageMirroringTestPod(corev1.PodPending, imageMirroringWaitingState("CreateContainerConfigError", "container has runAsNonRoot and image will run as root")), wantErr: "CreateContainerConfigError"},
		{name: "When the image name is invalid, it should fail terminally", pod: imageMirroringTestPod(corev1.PodPending, imageMirroringWaitingState("InvalidImageName", "")), wantErr: "InvalidImageName"},
		{name: "When the container exits non-zero, it should fail terminally", pod: imageMirroringTestPod(corev1.PodRunning, imageMirroringTerminatedState(2)), wantErr: "exitCode=2"},
		{name: "When the pod Failed, it should fail terminally", pod: imageMirroringTestPod(corev1.PodFailed, nil), wantErr: "Failed"},
		{name: "When the pod is nil, it should fail terminally", pod: nil, wantErr: "nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done, reason, err := imageMirroringCheckPulledPod(tt.pod)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || done {
					t.Fatalf("expected terminal error containing %q, got %v %v", tt.wantErr, done, err)
				}
				return
			}
			if err != nil || done != tt.wantDone || !strings.Contains(reason, tt.wantReason) || !strings.Contains(reason, "e2e-image-mirroring-x/pod") {
				t.Fatalf("got %v %q %v, want %v containing %q", done, reason, err, tt.wantDone, tt.wantReason)
			}
		})
	}
}

func TestImageMirroringPodState(t *testing.T) {
	t.Run("When the pod is nil, it should not panic and report no container status", func(t *testing.T) {
		if got := imageMirroringContainerStatus(nil); got != nil {
			t.Fatalf("expected nil status, got %+v", got)
		}
		if got := imageMirroringPodSummary(nil); !strings.Contains(got, "<nil>") {
			t.Fatalf("expected a nil pod summary, got %q", got)
		}
	})
	t.Run("When the workload container has a status, it should return that status", func(t *testing.T) {
		pod := imageMirroringTestPod(corev1.PodRunning, imageMirroringRunningState())
		if got := imageMirroringContainerStatus(pod); got == nil || got.Name != imageMirroringWorkloadContainer {
			t.Fatalf("expected the %q status, got %+v", imageMirroringWorkloadContainer, got)
		}
	})
}

func TestImageMirroringCheckImageID(t *testing.T) {
	withImageID := func(imageID string) *corev1.Pod {
		pod := imageMirroringTestPod(corev1.PodSucceeded, imageMirroringTerminatedState(0))
		pod.Status.ContainerStatuses[0].ImageID = imageID
		return pod
	}
	accepted := []string{imageMirroringTestAMD64Digest, imageMirroringTestListDigest}
	tests := []struct {
		name     string
		pod      *corev1.Pod
		accepted []string
		wantErr  string
	}{
		{name: "When the imageID has the per-platform digest, it should succeed", pod: withImageID(imageMirroringVirtualSource + "@" + imageMirroringTestAMD64Digest), accepted: accepted},
		{name: "When the imageID has the manifest list digest, it should succeed", pod: withImageID(imageMirroringVirtualSource + "@" + imageMirroringTestListDigest), accepted: accepted},
		{name: "When the imageID has another platform digest, it should return an error", pod: withImageID(imageMirroringVirtualSource + "@" + imageMirroringTestARM64Digest), accepted: accepted, wantErr: "not one of the mirror digests"},
		{name: "When the imageID is a bare image ID, it should return an error", pod: withImageID(imageMirroringTestAMD64Digest), accepted: accepted, wantErr: "manifest reference"},
		{name: "When the container has no status, it should return an error", pod: imageMirroringTestPod(corev1.PodSucceeded, nil), accepted: accepted, wantErr: "no status"},
		{name: "When no digests are accepted, it should return an error", pod: withImageID(imageMirroringVirtualSource + "@" + imageMirroringTestAMD64Digest), wantErr: "no accepted digests"},
		{name: "When the pod is nil, it should return an error", pod: nil, accepted: accepted, wantErr: "nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := imageMirroringCheckImageID(tt.pod, tt.accepted)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
