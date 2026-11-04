//go:build e2ev2

package tests

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/util/mirrorregistry"

	imagev1 "github.com/openshift/api/image/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Test inputs are written by hand, independently of the fixture and of the
// HyperShift override code.
const (
	registryOverridesTestHost   = "mirror-registry-test-mirror-registry-ho.apps.mgmt.example.com"
	registryOverridesTestSource = "quay-proxy.ci.openshift.org/openshift/ci"
	registryOverridesTestMirror = registryOverridesTestHost + "/openshift/ci"
	registryOverridesTestDigest = "@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	registryOverridesTestHO     = "registry.build01.ci.openshift.org/ci-op-abc/pipeline@sha256:1111111111111111111111111111111111111111111111111111111111111111"
)

func registryOverridesTestOverrides() map[string]string {
	return map[string]string{
		registryOverridesTestSource:                           registryOverridesTestMirror,
		"registry.build01.ci.openshift.org/ci-op-abc/release": registryOverridesTestHost + "/ci-op-abc/release",
	}
}

func registryOverridesTestDeployment(name string, images ...string) appsv1.Deployment {
	d := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-hc", Name: name, Generation: 2},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To(int32(2))},
		Status:     appsv1.DeploymentStatus{ObservedGeneration: 2, Replicas: 2, UpdatedReplicas: 2, AvailableReplicas: 2},
	}
	// The first container is the primary one, named after the Deployment as
	// in the control plane assets; the others are sidecars.
	for i, image := range images {
		containerName := name
		if i > 0 {
			containerName = name + "-sidecar-" + string(rune('a'+i))
		}
		d.Spec.Template.Spec.Containers = append(d.Spec.Template.Spec.Containers, corev1.Container{Name: containerName, Image: image})
	}
	return d
}

func registryOverridesTestStatefulSet(name string, images ...string) appsv1.StatefulSet {
	s := appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-hc", Name: name, Generation: 3},
		Spec:       appsv1.StatefulSetSpec{Replicas: ptr.To(int32(3))},
		Status:     appsv1.StatefulSetStatus{ObservedGeneration: 3, UpdatedReplicas: 3, ReadyReplicas: 3, CurrentRevision: "r2", UpdateRevision: "r2"},
	}
	for _, image := range images {
		s.Spec.Template.Spec.Containers = append(s.Spec.Template.Spec.Containers, corev1.Container{Name: name, Image: image})
	}
	return s
}

func TestRegistryOverridesLoadCA(t *testing.T) {
	dir := t.TempDir()
	write := func(name, data string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cert, key, empty := write("ca.crt", "CERT"), write("ca.key", "KEY"), write("empty", "")
	missing := filepath.Join(dir, "missing")
	for _, tt := range []struct {
		name      string
		cert, key string
		wantNil   bool
		wantError string
	}{
		{name: "When both paths are empty, it should return no CA so the caller skips", wantNil: true},
		{name: "When both files exist, it should return the key pair", cert: cert, key: key},
		{name: "When only the certificate path is set, it should fail", cert: cert, wantError: "both"},
		{name: "When only the key path is set, it should fail", key: key, wantError: "both"},
		{name: "When the certificate file is missing, it should fail", cert: missing, key: key, wantError: "certificate"},
		{name: "When the key file is missing, it should fail", cert: cert, key: missing, wantError: "key"},
		{name: "When a file is empty, it should fail", cert: cert, key: empty, wantError: "empty"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := registryOverridesLoadCA(tt.cert, tt.key)
			switch {
			case tt.wantError != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("want error containing %q, got %v", tt.wantError, err)
				}
			case err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantNil && got != nil:
				t.Fatalf("want nil CA, got %+v", got)
			case !tt.wantNil && (got == nil || string(got.CertPEM) != "CERT" || string(got.KeyPEM) != "KEY"):
				t.Fatalf("want CERT/KEY, got %+v", got)
			}
		})
	}
}

func TestRegistryOverridesParseArg(t *testing.T) {
	for _, tt := range []struct {
		name      string
		value     string
		want      map[string]string
		wantError bool
	}{
		{name: "When the value is empty, it should return no overrides", value: "", want: map[string]string{}},
		{name: "When the value is the empty-map marker, it should return no overrides", value: "=", want: map[string]string{}},
		{name: "When the value has two pairs, it should return both", value: "a.io/x=m.io/x,b.io/y=m.io/y", want: map[string]string{"a.io/x": "m.io/x", "b.io/y": "m.io/y"}},
		{name: "When a pair has no separator, it should fail", value: "a.io/x", wantError: true},
		{name: "When a pair has an empty target, it should fail", value: "a.io/x=", wantError: true},
		{name: "When a pair has an empty source, it should fail", value: "=m.io/x", wantError: true},
		{name: "When a source repeats, it should fail", value: "a.io/x=m.io/x,a.io/x=m.io/y", wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := registryOverridesParseArg(tt.value)
			if tt.wantError {
				if err == nil {
					t.Fatalf("want error, got %v", got)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestRegistryOverridesFormatArg(t *testing.T) {
	t.Run("When there are several overrides, it should sort the pairs", func(t *testing.T) {
		got := registryOverridesFormatArg(map[string]string{"b.io/y": "m.io/y", "a.io/x": "m.io/x"})
		if got != "a.io/x=m.io/x,b.io/y=m.io/y" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("When formatted and parsed again, it should round-trip", func(t *testing.T) {
		want := registryOverridesTestOverrides()
		got, err := registryOverridesParseArg(registryOverridesFormatArg(want))
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, %v; want %v", got, err, want)
		}
	})
}

func TestRegistryOverridesFromResult(t *testing.T) {
	valid := func() *mirrorregistry.Result {
		return &mirrorregistry.Result{Host: registryOverridesTestHost, ImageContentSources: []hyperv1.ImageContentSource{
			{Source: registryOverridesTestSource, Mirrors: []string{registryOverridesTestMirror}},
			{Source: "registry.build01.ci.openshift.org/ci-op-abc/release", Mirrors: []string{registryOverridesTestHost + "/ci-op-abc/release"}},
		}}
	}
	for _, tt := range []struct {
		name      string
		change    func(*mirrorregistry.Result) *mirrorregistry.Result
		wantError string
	}{
		{name: "When every source has one mirror on the host, it should map sources to mirrors"},
		{name: "When the result is nil, it should fail", change: func(*mirrorregistry.Result) *mirrorregistry.Result { return nil }, wantError: "no host"},
		{name: "When the host is empty, it should fail", change: func(r *mirrorregistry.Result) *mirrorregistry.Result { r.Host = ""; return r }, wantError: "no host"},
		{name: "When there are no image content sources, it should fail", change: func(r *mirrorregistry.Result) *mirrorregistry.Result { r.ImageContentSources = nil; return r }, wantError: "no image content sources"},
		{name: "When a source has two mirrors, it should fail", change: func(r *mirrorregistry.Result) *mirrorregistry.Result {
			r.ImageContentSources[0].Mirrors = append(r.ImageContentSources[0].Mirrors, registryOverridesTestHost+"/other")
			return r
		}, wantError: "exactly 1"},
		{name: "When a mirror is on another host, it should fail", change: func(r *mirrorregistry.Result) *mirrorregistry.Result {
			r.ImageContentSources[0].Mirrors[0] = registryOverridesTestHost + ".evil/openshift/ci"
			return r
		}, wantError: "not a repository"},
		{name: "When a source repeats, it should fail", change: func(r *mirrorregistry.Result) *mirrorregistry.Result {
			r.ImageContentSources[1].Source = registryOverridesTestSource
			return r
		}, wantError: "duplicate"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := valid()
			if tt.change != nil {
				result = tt.change(result)
			}
			got, err := registryOverridesFromResult(result)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("want error containing %q, got %v", tt.wantError, err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, registryOverridesTestOverrides()) {
				t.Fatalf("got %v, %v", got, err)
			}
		})
	}
}

func TestRegistryOverridesReleaseImages(t *testing.T) {
	hc := func(name, release string) hyperv1.HostedCluster {
		return hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: name}, Spec: hyperv1.HostedClusterSpec{Release: hyperv1.Release{Image: release}}}
	}
	a, b := "registry.example.com/ocp/release"+registryOverridesTestDigest, "quay.io/openshift/ci@sha256:2222222222222222222222222222222222222222222222222222222222222222"
	withCP := hc("cp", a)
	withCP.Spec.ControlPlaneRelease = &hyperv1.Release{Image: b}
	for _, tt := range []struct {
		name      string
		hcs       []hyperv1.HostedCluster
		want      []string
		wantError string
	}{
		{name: "When there are no HostedClusters, it should fail", wantError: "no HostedClusters"},
		{name: "When two HostedClusters share a release, it should return it once", hcs: []hyperv1.HostedCluster{hc("x", a), hc("y", a)}, want: []string{a}},
		{name: "When a HostedCluster has a control plane release, it should include it, sorted", hcs: []hyperv1.HostedCluster{withCP}, want: []string{b, a}},
		{name: "When a release is referenced by tag, it should fail", hcs: []hyperv1.HostedCluster{hc("x", "quay.io/openshift-release-dev/ocp-release:4.20.0-x86_64")}, wantError: "not referenced by digest"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := registryOverridesReleaseImages(tt.hcs)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("want error containing %q, got %v", tt.wantError, err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestRegistryOverridesOperatorContainer(t *testing.T) {
	deployment := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
		Containers: []corev1.Container{{Name: "sidecar"}, {Name: "operator", Args: []string{"run"}}},
	}}}}
	t.Run("When the operator container exists, it should return a pointer into the Deployment", func(t *testing.T) {
		got, err := registryOverridesOperatorContainer(deployment)
		if err != nil || got.Name != "operator" {
			t.Fatalf("got %+v, %v", got, err)
		}
		got.Args = append(got.Args, "--x")
		if len(deployment.Spec.Template.Spec.Containers[1].Args) != 2 {
			t.Fatal("container is not the Deployment's own")
		}
	})
	t.Run("When the operator container is missing, it should fail", func(t *testing.T) {
		if _, err := registryOverridesOperatorContainer(&appsv1.Deployment{}); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("When the Deployment is nil, it should fail without panic", func(t *testing.T) {
		if _, err := registryOverridesOperatorContainer(nil); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestRegistryOverridesArgValue(t *testing.T) {
	for _, tt := range []struct {
		name      string
		args      []string
		want      string
		wantFound bool
		wantError bool
	}{
		{name: "When the flag is absent, it should report not found", args: []string{"run", "--namespace=x"}},
		{name: "When the flag uses '=', it should return its value", args: []string{"run", "--registry-overrides=a=b"}, want: "a=b", wantFound: true},
		{name: "When the flag is followed by its value, it should return the value", args: []string{"--registry-overrides", "a=b", "--x"}, want: "a=b", wantFound: true},
		{name: "When the flag has no value, it should fail", args: []string{"--registry-overrides"}, wantError: true},
		{name: "When the flag is set twice, it should fail", args: []string{"--registry-overrides=a=b", "--registry-overrides", "c=d"}, wantError: true},
		{name: "When a longer flag shares the prefix, it should not match", args: []string{"--registry-overrides-extra=a"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, found, err := registryOverridesArgValue(tt.args)
			if tt.wantError {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil || got != tt.want || found != tt.wantFound {
				t.Fatalf("got %q, %t, %v; want %q, %t", got, found, err, tt.want, tt.wantFound)
			}
		})
	}
}

func TestRegistryOverridesSetArg(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want []string
	}{
		{name: "When the flag is absent, it should append it", args: []string{"run", "--a=1"}, want: []string{"run", "--a=1", "--registry-overrides=x=y"}},
		{name: "When the flag uses '=', it should replace it", args: []string{"run", "--registry-overrides=old=v", "--a=1"}, want: []string{"run", "--a=1", "--registry-overrides=x=y"}},
		{name: "When the flag is followed by its value, it should replace both", args: []string{"run", "--registry-overrides", "old=v", "--a=1"}, want: []string{"run", "--a=1", "--registry-overrides=x=y"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := append([]string(nil), tt.args...)
			got := registryOverridesSetArg(tt.args, "x=y")
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			if !reflect.DeepEqual(tt.args, before) {
				t.Fatal("input args were modified")
			}
		})
	}
}

func TestRegistryOverridesApplied(t *testing.T) {
	for _, tt := range []struct {
		name    string
		current map[string]string
		host    string
		want    bool
	}{
		{name: "When every target is on the Route host, it should report applied", current: registryOverridesTestOverrides(), host: registryOverridesTestHost, want: true},
		{name: "When there are no overrides, it should report not applied", current: map[string]string{}, host: registryOverridesTestHost},
		{name: "When the Route host is unknown, it should report not applied", current: registryOverridesTestOverrides()},
		{name: "When a target is on another host, it should report not applied", current: map[string]string{"a.io/x": "other.example.com/x"}, host: registryOverridesTestHost},
		{name: "When a target only shares the host prefix, it should report not applied", current: map[string]string{"a.io/x": registryOverridesTestHost + ".evil/x"}, host: registryOverridesTestHost},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := registryOverridesApplied(tt.current, tt.host); got != tt.want {
				t.Fatalf("got %t, want %t", got, tt.want)
			}
		})
	}
}

func TestRegistryOverridesCheckDeploymentRolledOut(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*appsv1.Deployment)
		want   bool
		reason string
	}{
		{name: "When all replicas are updated and available, it should report done", want: true},
		{name: "When the spec is not observed yet, it should report the generation", change: func(d *appsv1.Deployment) { d.Status.ObservedGeneration = 1 }, reason: "observed generation"},
		{name: "When replicas are still updating, it should report the updated count", change: func(d *appsv1.Deployment) { d.Status.UpdatedReplicas = 1 }, reason: "updated"},
		{name: "When old replicas still exist, it should report the total", change: func(d *appsv1.Deployment) { d.Status.Replicas = 3 }, reason: "total"},
		{name: "When replicas are not available, it should report the available count", change: func(d *appsv1.Deployment) { d.Status.AvailableReplicas = 1 }, reason: "available"},
		{name: "When replicas is unset, it should default to one", change: func(d *appsv1.Deployment) {
			d.Spec.Replicas = nil
			d.Status.Replicas, d.Status.UpdatedReplicas, d.Status.AvailableReplicas = 1, 1, 1
		}, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := registryOverridesTestDeployment("kube-apiserver")
			if tt.change != nil {
				tt.change(&d)
			}
			got, reason := registryOverridesCheckDeploymentRolledOut(&d)
			if got != tt.want || !strings.Contains(reason, tt.reason) {
				t.Fatalf("got %t, %q; want %t, %q", got, reason, tt.want, tt.reason)
			}
		})
	}
	t.Run("When the Deployment is nil, it should report not done without panic", func(t *testing.T) {
		if got, _ := registryOverridesCheckDeploymentRolledOut(nil); got {
			t.Fatal("want not done")
		}
	})
}

func TestRegistryOverridesCheckStatefulSetRolledOut(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*appsv1.StatefulSet)
		want   bool
		reason string
	}{
		{name: "When all replicas run the current revision and are ready, it should report done", want: true},
		{name: "When the spec is not observed yet, it should report the generation", change: func(s *appsv1.StatefulSet) { s.Status.ObservedGeneration = 2 }, reason: "observed generation"},
		{name: "When the rollout has not reached the update revision, it should report the revision", change: func(s *appsv1.StatefulSet) { s.Status.CurrentRevision = "r1" }, reason: "revision"},
		{name: "When replicas are not ready, it should report the ready count", change: func(s *appsv1.StatefulSet) { s.Status.ReadyReplicas = 2 }, reason: "ready"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := registryOverridesTestStatefulSet("etcd")
			if tt.change != nil {
				tt.change(&s)
			}
			got, reason := registryOverridesCheckStatefulSetRolledOut(&s)
			if got != tt.want || !strings.Contains(reason, tt.reason) {
				t.Fatalf("got %t, %q; want %t, %q", got, reason, tt.want, tt.reason)
			}
		})
	}
	t.Run("When the StatefulSet is nil, it should report not done without panic", func(t *testing.T) {
		if got, _ := registryOverridesCheckStatefulSetRolledOut(nil); got {
			t.Fatal("want not done")
		}
	})
}

func TestRegistryOverridesCheckPropagated(t *testing.T) {
	expected := registryOverridesTestOverrides()
	formatted := registryOverridesFormatArg(expected)
	withArgs := func(args ...string) *appsv1.Deployment {
		d := registryOverridesTestDeployment("hosted-cluster-config-operator", "img")
		d.Spec.Template.Spec.Containers[0].Args = args
		return &d
	}
	// withCommand mirrors the HCCO Deployment, which sets every flag in
	// Command and none in Args.
	withCommand := func(command ...string) *appsv1.Deployment {
		d := registryOverridesTestDeployment("hosted-cluster-config-operator", "img")
		d.Spec.Template.Spec.Containers[0].Command = command
		return &d
	}
	onSidecar := func() *appsv1.Deployment {
		d := registryOverridesTestDeployment("hosted-cluster-config-operator", "img", "img")
		d.Spec.Template.Spec.Containers[1].Args = []string{"--registry-overrides=" + formatted}
		return &d
	}
	withoutPrimary := func() *appsv1.Deployment {
		d := registryOverridesTestDeployment("hosted-cluster-config-operator", "img")
		d.Spec.Template.Spec.Containers[0].Name = "other"
		d.Spec.Template.Spec.Containers[0].Args = []string{"--registry-overrides=" + formatted}
		return &d
	}
	for _, tt := range []struct {
		name       string
		deployment *appsv1.Deployment
		want       bool
		reason     string
		wantError  bool
	}{
		{name: "When the flag carries the expected mapping as one arg, it should report done", deployment: withArgs("--registry-overrides=" + formatted), want: true},
		{name: "When the flag carries the expected mapping as two args, it should report done", deployment: withArgs("run", "--registry-overrides", formatted), want: true},
		{name: "When the flag is in Command as two args, it should report done", deployment: withCommand("/usr/bin/control-plane-operator", "hosted-cluster-config-operator", "--registry-overrides", formatted), want: true},
		{name: "When the flag is set once in Command and once in Args, it should fail", deployment: func() *appsv1.Deployment {
			d := withCommand("run", "--registry-overrides", formatted)
			d.Spec.Template.Spec.Containers[0].Args = []string{"--registry-overrides=" + formatted}
			return d
		}(), wantError: true},
		{name: "When only a sidecar has the flag, it should report that the primary container lacks it", deployment: onSidecar(), reason: "container hosted-cluster-config-operator has no --registry-overrides"},
		{name: "When there is no primary container, it should report it", deployment: withoutPrimary(), reason: "has no container hosted-cluster-config-operator"},
		{name: "When the flag is the empty marker, it should report the mismatch", deployment: withArgs("--registry-overrides", "="), reason: "want"},
		{name: "When the flag has a different mapping, it should report the mismatch", deployment: withArgs("--registry-overrides=" + registryOverridesTestSource + "=other.example.com/x"), reason: "want"},
		{name: "When the primary container has no flag, it should report it", deployment: withArgs("run"), reason: "has no --registry-overrides"},
		{name: "When the flag value is malformed, it should fail", deployment: withArgs("--registry-overrides=broken"), wantError: true},
		{name: "When the Deployment is nil, it should fail without panic", wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, reason, err := registryOverridesCheckPropagated(tt.deployment, expected)
			if tt.wantError {
				if err == nil {
					t.Fatalf("want error, got %t, %q", got, reason)
				}
				return
			}
			if err != nil || got != tt.want || !strings.Contains(reason, tt.reason) {
				t.Fatalf("got %t, %q, %v; want %t, %q", got, reason, err, tt.want, tt.reason)
			}
		})
	}
}

func TestRegistryOverridesMatchesPrefix(t *testing.T) {
	for _, tt := range []struct {
		name, image, prefix string
		want                bool
	}{
		{name: "When the image is the prefix, it should match", image: "a.io/x", prefix: "a.io/x", want: true},
		{name: "When a digest follows the prefix, it should match", image: "a.io/x" + registryOverridesTestDigest, prefix: "a.io/x", want: true},
		{name: "When a path follows the prefix, it should match", image: "a.io/x/y:tag", prefix: "a.io/x", want: true},
		{name: "When a tag follows the prefix, it should match", image: "a.io/x:tag", prefix: "a.io/x", want: true},
		{name: "When a path follows a bare host prefix, it should match", image: "a.io/x@sha256:1", prefix: "a.io", want: true},
		{name: "When a port follows a bare host prefix, it should not match", image: "a.io:5000/x", prefix: "a.io"},
		{name: "When a port follows a host and port prefix, it should not match a longer port", image: "a.io:50001/x", prefix: "a.io:5000"},
		{name: "When the repository name is longer, it should not match", image: "a.io/xy@sha256:1", prefix: "a.io/x"},
		{name: "When the prefix is empty, it should not match", image: "a.io/x"},
		{name: "When the image is unrelated, it should not match", image: "b.io/x", prefix: "a.io/x"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := registryOverridesMatchesPrefix(tt.image, tt.prefix); got != tt.want {
				t.Fatalf("got %t, want %t", got, tt.want)
			}
		})
	}
}

func TestRegistryOverridesCheckImages(t *testing.T) {
	overrides := registryOverridesTestOverrides()
	mirrored := registryOverridesTestMirror + registryOverridesTestDigest
	source := registryOverridesTestSource + registryOverridesTestDigest
	mandatory := []string{"kube-apiserver", "kube-controller-manager"}
	healthy := func() ([]appsv1.Deployment, []appsv1.StatefulSet) {
		kas := registryOverridesTestDeployment("kube-apiserver", mirrored, registryOverridesTestHO)
		kas.Spec.Template.Spec.InitContainers = []corev1.Container{{Name: "init", Image: mirrored}}
		return []appsv1.Deployment{kas, registryOverridesTestDeployment("kube-controller-manager", mirrored)},
			[]appsv1.StatefulSet{registryOverridesTestStatefulSet("etcd", mirrored)}
	}
	for _, tt := range []struct {
		name      string
		change    func([]appsv1.Deployment, []appsv1.StatefulSet) ([]appsv1.Deployment, []appsv1.StatefulSet)
		overrides map[string]string
		want      bool
		reason    string
		wantError bool
	}{
		{name: "When every workload runs mirror images and is rolled out, it should report done", want: true},
		{name: "When a container still uses a source, it should name it", change: func(d []appsv1.Deployment, s []appsv1.StatefulSet) ([]appsv1.Deployment, []appsv1.StatefulSet) {
			d[1].Spec.Template.Spec.Containers[0].Image = source
			return d, s
		}, reason: "kube-controller-manager container kube-controller-manager still uses"},
		{name: "When a sidecar still uses a source, it should name it", change: func(d []appsv1.Deployment, s []appsv1.StatefulSet) ([]appsv1.Deployment, []appsv1.StatefulSet) {
			d[0].Spec.Template.Spec.Containers = append(d[0].Spec.Template.Spec.Containers, corev1.Container{Name: "audit-logs", Image: source})
			return d, s
		}, reason: "kube-apiserver container audit-logs still uses"},
		{name: "When only a sidecar of a mandatory workload runs a mirror image, it should report the primary container", change: func(d []appsv1.Deployment, s []appsv1.StatefulSet) ([]appsv1.Deployment, []appsv1.StatefulSet) {
			d[0].Spec.Template.Spec.Containers[0].Image = "registry.redhat.io/other@sha256:3"
			return d, s
		}, reason: "kube-apiserver has no container kube-apiserver with an image on a mirror"},
		{name: "When only the init container of a mandatory workload runs a mirror image, it should report the primary container", change: func(d []appsv1.Deployment, s []appsv1.StatefulSet) ([]appsv1.Deployment, []appsv1.StatefulSet) {
			d[0].Spec.Template.Spec.Containers = d[0].Spec.Template.Spec.Containers[1:]
			return d, s
		}, reason: "kube-apiserver has no container kube-apiserver with an image on a mirror"},
		{name: "When an init container still uses a source, it should name it", change: func(d []appsv1.Deployment, s []appsv1.StatefulSet) ([]appsv1.Deployment, []appsv1.StatefulSet) {
			d[0].Spec.Template.Spec.InitContainers[0].Image = source
			return d, s
		}, reason: "container init still uses"},
		{name: "When a StatefulSet still uses a source, it should name it", change: func(d []appsv1.Deployment, s []appsv1.StatefulSet) ([]appsv1.Deployment, []appsv1.StatefulSet) {
			s[0].Spec.Template.Spec.Containers[0].Image = source
			return d, s
		}, reason: "StatefulSet clusters-hc/etcd"},
		{name: "When a mandatory workload is missing, it should report it", change: func(d []appsv1.Deployment, s []appsv1.StatefulSet) ([]appsv1.Deployment, []appsv1.StatefulSet) {
			return d[:1], s
		}, reason: "kube-controller-manager has no container kube-controller-manager with an image on a mirror"},
		{name: "When a mandatory workload runs only images outside the overrides, it should report it", change: func(d []appsv1.Deployment, s []appsv1.StatefulSet) ([]appsv1.Deployment, []appsv1.StatefulSet) {
			d[1].Spec.Template.Spec.Containers[0].Image = "registry.redhat.io/other@sha256:3"
			return d, s
		}, reason: "kube-controller-manager has no container kube-controller-manager with an image on a mirror"},
		{name: "When a mandatory workload runs only the HO image, it should report it", change: func(d []appsv1.Deployment, s []appsv1.StatefulSet) ([]appsv1.Deployment, []appsv1.StatefulSet) {
			d[1].Spec.Template.Spec.Containers[0].Image = registryOverridesTestHO
			return d, s
		}, reason: "kube-controller-manager has no container kube-controller-manager with an image on a mirror"},
		{name: "When the HO image is under an overridden source, it should be ignored", overrides: map[string]string{
			registryOverridesTestSource:                            registryOverridesTestMirror,
			"registry.build01.ci.openshift.org/ci-op-abc/pipeline": registryOverridesTestHost + "/ci-op-abc/pipeline",
		}, want: true},
		{name: "When a Deployment is still rolling out, it should report it", change: func(d []appsv1.Deployment, s []appsv1.StatefulSet) ([]appsv1.Deployment, []appsv1.StatefulSet) {
			d[0].Status.UpdatedReplicas = 1
			return d, s
		}, reason: "1 updated of 2"},
		{name: "When a StatefulSet is still rolling out, it should report it", change: func(d []appsv1.Deployment, s []appsv1.StatefulSet) ([]appsv1.Deployment, []appsv1.StatefulSet) {
			s[0].Status.CurrentRevision = "r1"
			return d, s
		}, reason: "revision r1"},
		{name: "When there are no overrides, it should fail", overrides: map[string]string{}, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			deployments, statefulSets := healthy()
			if tt.change != nil {
				deployments, statefulSets = tt.change(deployments, statefulSets)
			}
			o := overrides
			if tt.overrides != nil {
				o = tt.overrides
			}
			got, reason, err := registryOverridesCheckImages(deployments, statefulSets, o, registryOverridesTestHO, mandatory)
			if tt.wantError {
				if err == nil {
					t.Fatalf("want error, got %t, %q", got, reason)
				}
				return
			}
			if err != nil || got != tt.want || !strings.Contains(reason, tt.reason) {
				t.Fatalf("got %t, %q, %v; want %t, %q", got, reason, err, tt.want, tt.reason)
			}
		})
	}
}

func TestRegistryOverridesCLIImage(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := imagev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	stream := func(tag, ref string) *imagev1.ImageStream {
		return &imagev1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Namespace: "openshift", Name: "cli"},
			Status:     imagev1.ImageStreamStatus{Tags: []imagev1.NamedTagEventList{{Tag: tag, Items: []imagev1.TagEvent{{DockerImageReference: ref}}}}},
		}
	}
	byDigest := "quay.io/openshift-release-dev/ocp-v4.0-art-dev" + registryOverridesTestDigest
	for _, tt := range []struct {
		name      string
		stream    *imagev1.ImageStream
		wantError bool
	}{
		{name: "When latest is imported by digest, it should return it", stream: stream("latest", byDigest)},
		{name: "When latest is only a tag, it should fail", stream: stream("latest", "quay.io/openshift/cli:latest"), wantError: true},
		{name: "When only another tag exists, it should fail", stream: stream("4.20", byDigest), wantError: true},
		{name: "When the ImageStream is missing, it should fail", wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.stream != nil {
				builder = builder.WithObjects(tt.stream)
			}
			got, err := registryOverridesCLIImage(t.Context(), builder.Build())
			if tt.wantError {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil || got != byDigest {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestRegistryOverridesPullSecret(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "hc"}, Spec: hyperv1.HostedClusterSpec{PullSecret: corev1.LocalObjectReference{Name: "hc-pull-secret"}}}
	secret := func(data map[string][]byte) *corev1.Secret {
		return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "hc-pull-secret"}, Data: data}
	}
	for _, tt := range []struct {
		name      string
		hc        *hyperv1.HostedCluster
		secret    *corev1.Secret
		wantError bool
	}{
		{name: "When the Secret has .dockerconfigjson, it should return it", hc: hc, secret: secret(map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)})},
		{name: "When the Secret lacks .dockerconfigjson, it should fail", hc: hc, secret: secret(map[string][]byte{"other": []byte("x")}), wantError: true},
		{name: "When the Secret is missing, it should fail", hc: hc, wantError: true},
		{name: "When the HC has no pull secret reference, it should fail", hc: &hyperv1.HostedCluster{}, wantError: true},
		{name: "When the HC is nil, it should fail without panic", wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.secret != nil {
				builder = builder.WithObjects(tt.secret)
			}
			got, err := registryOverridesPullSecret(t.Context(), builder.Build(), tt.hc)
			if tt.wantError {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil || string(got) != `{"auths":{}}` {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}
