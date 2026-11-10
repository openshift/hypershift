//go:build e2ev2

package tests

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	registryOverridesUpgradeTestFrom = "quay-proxy.ci.openshift.org/openshift/ci@sha256:8888888888888888888888888888888888888888888888888888888888888888"
	registryOverridesUpgradeTestTo   = "registry.build09.ci.openshift.org/ci-op-abc/release@sha256:9999999999999999999999999999999999999999999999999999999999999999"
)

func registryOverridesUpgradeTestHC(history ...configv1.UpdateHistory) *hyperv1.HostedCluster {
	hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "upgrade"}}
	if history != nil {
		hc.Status.Version = &hyperv1.ClusterVersionStatus{History: history}
	}
	return hc
}

func TestRegistryOverridesUpgradeStart(t *testing.T) {
	started := metav1.NewTime(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))
	completed := configv1.UpdateHistory{Image: registryOverridesUpgradeTestTo, State: configv1.CompletedUpdate, StartedTime: started}
	previous := configv1.UpdateHistory{Image: registryOverridesUpgradeTestFrom, State: configv1.CompletedUpdate}
	for _, tt := range []struct {
		name            string
		hc              *hyperv1.HostedCluster
		expectedFrom    string
		expectedTo      string
		expectedStarted time.Time
		wantErr         string
	}{
		{name: "When the newest entry is the completed target, it should return both images and the start time", hc: registryOverridesUpgradeTestHC(completed, previous), expectedFrom: registryOverridesUpgradeTestFrom, expectedTo: registryOverridesUpgradeTestTo, expectedStarted: started.Time},
		{name: "When the HostedCluster is nil, it should return an error", wantErr: "nil"},
		{name: "When the HostedCluster has no version status, it should return an error", hc: registryOverridesUpgradeTestHC(), wantErr: "fewer than 2"},
		{name: "When the history has only the installed release, it should return an error", hc: registryOverridesUpgradeTestHC(previous), wantErr: "fewer than 2"},
		{name: "When the newest entry is another release, it should return an error", hc: registryOverridesUpgradeTestHC(previous, completed), wantErr: "want " + registryOverridesUpgradeTestTo},
		{name: "When the upgrade is still partial, it should return an error", hc: registryOverridesUpgradeTestHC(configv1.UpdateHistory{Image: registryOverridesUpgradeTestTo, State: configv1.PartialUpdate, StartedTime: started}, previous), wantErr: "is Partial"},
		{name: "When the entry before the target is the same release, it should return an error", hc: registryOverridesUpgradeTestHC(completed, configv1.UpdateHistory{Image: registryOverridesUpgradeTestTo, State: configv1.CompletedUpdate}), wantErr: "no other release"},
		{name: "When the upgrade has no start time, it should return an error", hc: registryOverridesUpgradeTestHC(configv1.UpdateHistory{Image: registryOverridesUpgradeTestTo, State: configv1.CompletedUpdate}, previous), wantErr: "no start time"},
		{name: "When the source release is not by digest, it should return an error", hc: registryOverridesUpgradeTestHC(completed, configv1.UpdateHistory{Image: "quay.io/openshift-release-dev/ocp-release:4.21.0-x86_64", State: configv1.CompletedUpdate}), wantErr: "not referenced by digest"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			target := registryOverridesUpgradeTestTo
			from, to, gotStarted, err := registryOverridesUpgradeStart(tt.hc, target)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want text %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || from != tt.expectedFrom || to != tt.expectedTo || !gotStarted.Equal(tt.expectedStarted) {
				t.Fatalf("got %q, %q, %s, %v; want %q, %q, %s, nil", from, to, gotStarted, err, tt.expectedFrom, tt.expectedTo, tt.expectedStarted)
			}
		})
	}
}

func TestRegistryOverridesCheckManifest(t *testing.T) {
	const (
		repository = "e2e/release"
		digest     = "sha256:9999999999999999999999999999999999999999999999999999999999999999"
	)
	for _, tt := range []struct {
		name    string
		handler http.HandlerFunc
		wantErr string
	}{
		{
			name: "When the mirror serves the manifest with the requested digest, it should succeed",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodHead || r.URL.Path != "/v2/"+repository+"/manifests/"+digest || !strings.Contains(r.Header.Get("Accept"), "application/vnd.oci.image.index.v1+json") {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Docker-Content-Digest", digest)
			},
		},
		{name: "When the mirror does not have the manifest, it should return the status", handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }, wantErr: "status 404"},
		{
			name:    "When the mirror returns another digest, it should return an error",
			handler: func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Docker-Content-Digest", "sha256:0000") },
			wantErr: "Docker-Content-Digest",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewTLSServer(tt.handler)
			defer server.Close()
			caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
			client, err := imageMirroringHTTPClient(caPEM)
			if err != nil {
				t.Fatal(err)
			}
			err = registryOverridesCheckManifest(t.Context(), client, strings.TrimPrefix(server.URL, "https://"), repository, digest)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want text %q", err, tt.wantErr)
			}
		})
	}

	t.Run("When the mirror certificate is not signed by the CA, it should return an error", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Docker-Content-Digest", digest)
		}))
		defer server.Close()
		// Every httptest server shares one certificate, so build an unrelated CA.
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{
			SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "unrelated CA"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
		}
		der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		client, err := imageMirroringHTTPClient(caPEM)
		if err != nil {
			t.Fatal(err)
		}
		if err := registryOverridesCheckManifest(t.Context(), client, strings.TrimPrefix(server.URL, "https://"), repository, digest); err == nil {
			t.Fatal("error = nil, want a TLS error")
		}
	})
}

func registryOverridesUpgradeTestPod(name string, deleting bool, init, regular []corev1.ContainerStatus) corev1.Pod {
	pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-upgrade", Name: name}}
	if deleting {
		now := metav1.Now()
		pod.DeletionTimestamp = &now
	}
	pod.Status.InitContainerStatuses = init
	pod.Status.ContainerStatuses = regular
	return pod
}

func registryOverridesUpgradeTestWaiting(name, reason string) corev1.ContainerStatus {
	return corev1.ContainerStatus{Name: name, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: "msg"}}}
}

func TestRegistryOverridesPullFailures(t *testing.T) {
	running := corev1.ContainerStatus{Name: "ok", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}
	for _, tt := range []struct {
		name string
		pods []corev1.Pod
		want []string
	}{
		{name: "When every container runs, it should report nothing", pods: []corev1.Pod{registryOverridesUpgradeTestPod("a", false, nil, []corev1.ContainerStatus{running})}},
		{
			name: "When containers fail to pull or reject their image, it should report each of them, init containers included",
			pods: []corev1.Pod{registryOverridesUpgradeTestPod("a", false,
				[]corev1.ContainerStatus{registryOverridesUpgradeTestWaiting("init", "ErrImagePull")},
				[]corev1.ContainerStatus{running, registryOverridesUpgradeTestWaiting("main", "ImagePullBackOff"), registryOverridesUpgradeTestWaiting("bad", "InvalidImageName")})},
			want: []string{
				"pod clusters-upgrade/a container init: ErrImagePull: msg",
				"pod clusters-upgrade/a container main: ImagePullBackOff: msg",
				"pod clusters-upgrade/a container bad: InvalidImageName: msg",
			},
		},
		{name: "When a container is still creating or cannot be configured, it should not report it as a pull failure", pods: []corev1.Pod{registryOverridesUpgradeTestPod("a", false, nil, []corev1.ContainerStatus{registryOverridesUpgradeTestWaiting("c", "ContainerCreating"), registryOverridesUpgradeTestWaiting("d", "CreateContainerConfigError")})}},
		{name: "When the failing pod is being deleted, it should ignore it", pods: []corev1.Pod{registryOverridesUpgradeTestPod("a", true, nil, []corev1.ContainerStatus{registryOverridesUpgradeTestWaiting("main", "ImagePullBackOff")})}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := registryOverridesPullFailures(tt.pods); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRegistryOverridesEventTime(t *testing.T) {
	first := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	last := first.Add(time.Hour)
	eventTime := first.Add(2 * time.Hour)
	series := first.Add(3 * time.Hour)
	for _, tt := range []struct {
		name  string
		event corev1.Event
		want  time.Time
	}{
		{name: "When only the first timestamp is set, it should use it", event: corev1.Event{FirstTimestamp: metav1.NewTime(first)}, want: first},
		{name: "When the last timestamp is set, it should prefer it", event: corev1.Event{FirstTimestamp: metav1.NewTime(first), LastTimestamp: metav1.NewTime(last)}, want: last},
		{name: "When the event time is set, it should prefer it", event: corev1.Event{LastTimestamp: metav1.NewTime(last), EventTime: metav1.NewMicroTime(eventTime)}, want: eventTime},
		{name: "When the event has a series, it should prefer its last observation", event: corev1.Event{EventTime: metav1.NewMicroTime(eventTime), Series: &corev1.EventSeries{LastObservedTime: metav1.NewMicroTime(series)}}, want: series},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := registryOverridesEventTime(&tt.event); !got.Equal(tt.want) {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRegistryOverridesPullFailureEvents(t *testing.T) {
	since := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	event := func(kind, reason, message string, at time.Time) corev1.Event {
		return corev1.Event{
			InvolvedObject: corev1.ObjectReference{Kind: kind, Namespace: "clusters-upgrade", Name: "p"},
			Reason:         reason, Message: message, LastTimestamp: metav1.NewTime(at),
		}
	}
	after := since.Add(time.Minute)
	for _, tt := range []struct {
		name   string
		events []corev1.Event
		want   []string
	}{
		{
			name: "When the kubelet reports pull failures after the start, it should report each of them",
			events: []corev1.Event{
				event("Pod", "Failed", `Failed to pull image "m/x@sha256:1": unauthorized`, after),
				event("Pod", "BackOff", `Back-off pulling image "m/x@sha256:1"`, after),
				event("Pod", "ErrImagePull", "x", after),
				event("Pod", "InvalidImageName", "x", after),
			},
			want: []string{
				`pod clusters-upgrade/p at 2026-11-10T12:01:00Z: Failed: Failed to pull image "m/x@sha256:1": unauthorized`,
				`pod clusters-upgrade/p at 2026-11-10T12:01:00Z: BackOff: Back-off pulling image "m/x@sha256:1"`,
				"pod clusters-upgrade/p at 2026-11-10T12:01:00Z: ErrImagePull: x",
				"pod clusters-upgrade/p at 2026-11-10T12:01:00Z: InvalidImageName: x",
			},
		},
		{name: "When the pull failure happened before the start, it should ignore it", events: []corev1.Event{event("Pod", "Failed", `Failed to pull image "x"`, since.Add(-time.Second))}},
		{name: "When an event at the start time reports a pull failure, it should report it", events: []corev1.Event{event("Pod", "ErrImagePull", "x", since)}, want: []string{"pod clusters-upgrade/p at 2026-11-10T12:00:00Z: ErrImagePull: x"}},
		{
			name: "When events are not about pulling images, it should ignore them",
			events: []corev1.Event{
				event("Pod", "BackOff", "Back-off restarting failed container", after),
				event("Pod", "Failed", "Error: container create failed", after),
				event("Pod", "Pulled", `Successfully pulled image "x"`, after),
			},
		},
		{name: "When the event is not about a pod, it should ignore it", events: []corev1.Event{event("Deployment", "Failed", `Failed to pull image "x"`, after)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := registryOverridesPullFailureEvents(tt.events, since); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
