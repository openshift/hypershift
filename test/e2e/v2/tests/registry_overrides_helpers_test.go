//go:build e2ev2

package tests

import (
	"reflect"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
)

// Test inputs are written by hand, independently of the HyperShift override
// code.
const (
	registryOverridesTestHost   = "mirror-registry.apps.mgmt.example.com"
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

func registryOverridesTestCronJob(name, image string) batchv1.CronJob {
	c := batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-hc", Name: name}}
	c.Spec.JobTemplate.Spec.Template.Spec.Containers = []corev1.Container{{Name: name, Image: image}}
	return c
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
		cronJobs  []batchv1.CronJob
		want      bool
		reason    string
		wantError bool
	}{
		{name: "When every workload runs mirror images and is rolled out, it should report done", want: true},
		{name: "When a CronJob template runs mirror images, it should report done", cronJobs: []batchv1.CronJob{registryOverridesTestCronJob("olm-collect-profiles", mirrored)}, want: true},
		{name: "When a CronJob template still uses a source, it should name it", cronJobs: []batchv1.CronJob{registryOverridesTestCronJob("olm-collect-profiles", source)}, reason: "CronJob clusters-hc/olm-collect-profiles container olm-collect-profiles still uses"},
		{name: "When a CronJob template runs the HO image, it should be ignored", cronJobs: []batchv1.CronJob{registryOverridesTestCronJob("olm-collect-profiles", registryOverridesTestHO)}, want: true},
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
			got, reason, err := registryOverridesCheckImages(deployments, statefulSets, tt.cronJobs, o, registryOverridesTestHO, mandatory)
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

func TestRegistryOverridesPrimaryContainer(t *testing.T) {
	t.Run("When a container is named after the Deployment, it should return it", func(t *testing.T) {
		d := registryOverridesTestDeployment("control-plane-operator", "sidecar-first", "img")
		d.Spec.Template.Spec.Containers[0].Name, d.Spec.Template.Spec.Containers[1].Name = "sidecar", "control-plane-operator"
		c, err := registryOverridesPrimaryContainer(&d)
		if err != nil || c.Image != "img" {
			t.Fatalf("got %v, %v; want image img", c, err)
		}
	})
	t.Run("When no container is named after the Deployment, it should fail", func(t *testing.T) {
		d := registryOverridesTestDeployment("control-plane-operator", "img")
		d.Spec.Template.Spec.Containers[0].Name = "other"
		if _, err := registryOverridesPrimaryContainer(&d); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestRegistryOverridesCurrentPods(t *testing.T) {
	expected := registryOverridesTestOverrides()
	formatted := registryOverridesFormatArg(expected)
	pod := func(name string, args ...string) corev1.Pod {
		return corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-hc", Name: name, Labels: map[string]string{"pod-template-hash": "current"}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "ignition-server", Args: args}}},
			Status:     corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
		}
	}
	previous := pod("previous", "--registry-overrides="+formatted)
	previous.Labels["pod-template-hash"] = "previous"
	terminating := pod("terminating", "--registry-overrides="+formatted)
	terminating.DeletionTimestamp = &metav1.Time{}
	notReady := pod("not-ready", "--registry-overrides="+formatted)
	notReady.Status.Conditions[0].Status = corev1.ConditionFalse
	inCommand := pod("in-command")
	inCommand.Spec.Containers[0].Command = []string{"/usr/bin/control-plane-operator", "ignition-server", "--registry-overrides", formatted}
	onSidecar := pod("on-sidecar")
	onSidecar.Spec.Containers = append(onSidecar.Spec.Containers, corev1.Container{Name: "sidecar", Args: []string{"--registry-overrides=" + formatted}})
	for _, tt := range []struct {
		name      string
		pods      []corev1.Pod
		want      []string
		wantError bool
	}{
		{name: "When pods carry the expected flag in Args or Command, it should return them sorted", pods: []corev1.Pod{pod("b", "--registry-overrides="+formatted), inCommand, pod("a", "--registry-overrides", formatted)}, want: []string{"a", "b", "in-command"}},
		{name: "When a pod is terminating, it should skip it", pods: []corev1.Pod{terminating}},
		{name: "When a pod is not Ready, it should skip it", pods: []corev1.Pod{notReady}},
		{name: "When a pod runs the old spec without the flag, it should skip it", pods: []corev1.Pod{pod("old", "start")}},
		{name: "When a pod has a different mapping, it should skip it", pods: []corev1.Pod{pod("other", "--registry-overrides="+registryOverridesTestSource+"=other.example.com/x")}},
		{name: "When only a sidecar carries the flag, it should skip the pod", pods: []corev1.Pod{onSidecar}},
		{name: "When a pod belongs to a previous ReplicaSet, it should skip it", pods: []corev1.Pod{previous, pod("a", "--registry-overrides="+formatted)}, want: []string{"a"}},
		{name: "When the flag value is malformed, it should fail", pods: []corev1.Pod{pod("bad", "--registry-overrides=broken")}, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := registryOverridesCurrentPods(tt.pods, "current", "ignition-server", expected)
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
	t.Run("When there is no template hash, it should fail", func(t *testing.T) {
		if _, err := registryOverridesCurrentPods([]corev1.Pod{pod("a", "--registry-overrides="+formatted)}, "", "ignition-server", expected); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestRegistryOverridesLogContainsText(t *testing.T) {
	for _, tt := range []struct {
		name, log string
		want      bool
	}{
		{name: "When a line contains the text, it should report it", log: "a\nI1109 tokensecret_controller.go:331] \"IgnitionProvider generated payload\"\nb\n", want: true},
		{name: "When the text is on the last line without newline, it should report it", log: "a\nIgnitionProvider generated payload", want: true},
		{name: "When no line contains the text, it should report not found", log: "Reconciling\ngot ignition payload\n"},
		{name: "When the log is empty, it should report not found"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := registryOverridesLogContainsText(strings.NewReader(tt.log), "IgnitionProvider generated payload")
			if err != nil || got != tt.want {
				t.Fatalf("got %t, %v; want %t", got, err, tt.want)
			}
		})
	}
	t.Run("When a line exceeds the scanner buffer, it should fail", func(t *testing.T) {
		if _, err := registryOverridesLogContainsText(strings.NewReader(strings.Repeat("x", 2*1024*1024)), "y"); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestRegistryOverridesCheckPayloadsGenerated(t *testing.T) {
	nodePool := func(name, cluster string, conditions ...hyperv1.NodePoolCondition) hyperv1.NodePool {
		return hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: name},
			Spec:       hyperv1.NodePoolSpec{ClusterName: cluster},
			Status:     hyperv1.NodePoolStatus{Conditions: conditions},
		}
	}
	valid := hyperv1.NodePoolCondition{Type: hyperv1.NodePoolValidGeneratedPayloadConditionType, Status: corev1.ConditionTrue, Reason: hyperv1.AsExpectedReason}
	invalid := hyperv1.NodePoolCondition{Type: hyperv1.NodePoolValidGeneratedPayloadConditionType, Status: corev1.ConditionFalse, Reason: "InvalidConfig", Message: "failed to generate payload"}
	for _, tt := range []struct {
		name      string
		nodePools []hyperv1.NodePool
		want      bool
		reason    string
	}{
		{name: "When every NodePool of the cluster has a valid payload, it should report done", nodePools: []hyperv1.NodePool{nodePool("a", "hc", valid), nodePool("b", "hc", valid), nodePool("other", "other", invalid)}, want: true},
		{name: "When a NodePool has an invalid payload, it should report its reason", nodePools: []hyperv1.NodePool{nodePool("a", "hc", valid), nodePool("b", "hc", invalid)}, reason: "NodePool clusters/b ValidGeneratedPayload is False: InvalidConfig: failed to generate payload"},
		{name: "When a NodePool has no condition, it should report it", nodePools: []hyperv1.NodePool{nodePool("a", "hc")}, reason: "has no ValidGeneratedPayload condition"},
		{name: "When the cluster has no NodePool, it should report it", nodePools: []hyperv1.NodePool{nodePool("other", "other", valid)}, reason: "no NodePool for cluster hc"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := registryOverridesCheckPayloadsGenerated(tt.nodePools, "hc")
			if got != tt.want || !strings.Contains(reason, tt.reason) {
				t.Fatalf("got %t, %q; want %t, %q", got, reason, tt.want, tt.reason)
			}
		})
	}
}

func TestRegistryOverridesCheckDaemonSet(t *testing.T) {
	overrides := registryOverridesTestOverrides()
	mirrored := registryOverridesTestMirror + registryOverridesTestDigest
	healthy := func() *appsv1.DaemonSet {
		return &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "global-pull-secret-syncer", Generation: 2},
			Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "global-pull-secret-syncer", Image: mirrored}},
			}}},
			Status: appsv1.DaemonSetStatus{ObservedGeneration: 2, DesiredNumberScheduled: 2, UpdatedNumberScheduled: 2, NumberAvailable: 2},
		}
	}
	for _, tt := range []struct {
		name      string
		change    func(*appsv1.DaemonSet)
		image     string
		want      bool
		reason    string
		wantError bool
	}{
		{name: "When the DaemonSet runs the mirror image on every scheduled node, it should report done", want: true},
		{name: "When the container runs another image, it should report it", change: func(d *appsv1.DaemonSet) {
			d.Spec.Template.Spec.Containers[0].Image = registryOverridesTestSource + registryOverridesTestDigest
		}, reason: "runs " + registryOverridesTestSource},
		{name: "When the container is missing, it should report it", change: func(d *appsv1.DaemonSet) { d.Spec.Template.Spec.Containers[0].Name = "other" }, reason: "has no container global-pull-secret-syncer"},
		{name: "When the spec is not observed yet, it should report the generation", change: func(d *appsv1.DaemonSet) { d.Status.ObservedGeneration = 1 }, reason: "observed generation"},
		{name: "When no node is eligible, it should report it", change: func(d *appsv1.DaemonSet) {
			d.Status.DesiredNumberScheduled, d.Status.UpdatedNumberScheduled, d.Status.NumberAvailable = 0, 0, 0
		}, reason: "scheduled on no node"},
		{name: "When pods are still updating, it should report the updated count", change: func(d *appsv1.DaemonSet) { d.Status.UpdatedNumberScheduled = 1 }, reason: "1 updated of 2"},
		{name: "When an updated pod cannot pull its image, it should report it unavailable", change: func(d *appsv1.DaemonSet) {
			d.Status.NumberAvailable, d.Status.NumberUnavailable = 1, 1
		}, reason: "1 available and 1 unavailable of 2"},
		{name: "When the expected image is not on a mirror, it should fail", image: registryOverridesTestSource + registryOverridesTestDigest, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := healthy()
			if tt.change != nil {
				tt.change(d)
			}
			image := mirrored
			if tt.image != "" {
				image = tt.image
			}
			got, reason, err := registryOverridesCheckDaemonSet(d, image, overrides)
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
	t.Run("When the DaemonSet is nil, it should fail without panic", func(t *testing.T) {
		if _, _, err := registryOverridesCheckDaemonSet(nil, mirrored, overrides); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestRegistryOverridesInput(t *testing.T) {
	for _, tt := range []struct {
		name           string
		value          string
		want           map[string]string
		wantConfigured bool
		wantError      bool
	}{
		{name: "When the input is unset or empty, it should report not configured", value: ""},
		{name: "When the input holds overrides, it should parse them", value: "a.io/x=m.io/x,b.io/y=m.io/y", want: map[string]string{"a.io/x": "m.io/x", "b.io/y": "m.io/y"}, wantConfigured: true},
		{name: "When the input is malformed, it should fail", value: "a.io/x", wantConfigured: true, wantError: true},
		{name: "When the input holds no override, it should fail", value: "=", wantConfigured: true, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, configured, err := registryOverridesInput(tt.value)
			if configured != tt.wantConfigured || (err != nil) != tt.wantError {
				t.Fatalf("got configured %t, error %v; want configured %t, error %t", configured, err, tt.wantConfigured, tt.wantError)
			}
			if !tt.wantError && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRegistryOverridesOperatorState(t *testing.T) {
	operator := func(command, args []string) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Namespace: "hypershift", Name: "operator"},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{
				{Name: "operator", Image: registryOverridesTestHO, Command: command, Args: args},
			}}}},
		}
	}
	for _, tt := range []struct {
		name       string
		deployment *appsv1.Deployment
		want       map[string]string
		wantFound  bool
		wantError  bool
	}{
		{name: "When the operator has the flag in Args, it should return the overrides and the image", deployment: operator([]string{"/usr/bin/hypershift-operator"}, []string{"run", "--registry-overrides=a.io/x=m.io/x"}), want: map[string]string{"a.io/x": "m.io/x"}, wantFound: true},
		{name: "When the operator has the flag in Command, it should return the overrides", deployment: operator([]string{"/usr/bin/hypershift-operator", "run", "--registry-overrides", "a.io/x=m.io/x"}, nil), want: map[string]string{"a.io/x": "m.io/x"}, wantFound: true},
		{name: "When the operator has no flag, it should report not found with the image", deployment: operator(nil, []string{"run"})},
		{name: "When the flag value is malformed, it should fail", deployment: operator(nil, []string{"--registry-overrides=a.io/x"}), wantError: true},
		{name: "When the operator container is missing, it should fail", deployment: &appsv1.Deployment{}, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, found, image, err := registryOverridesOperatorState(tt.deployment)
			if tt.wantError {
				if err == nil {
					t.Fatalf("want error, got %v", got)
				}
				return
			}
			if err != nil || found != tt.wantFound || image != registryOverridesTestHO || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, %t, %q, %v; want %v, %t, %q", got, found, image, err, tt.want, tt.wantFound, registryOverridesTestHO)
			}
		})
	}
}

func TestRegistryOverridesSyncerEligibleNodes(t *testing.T) {
	node := func(name string, labels map[string]string) corev1.Node {
		return corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
	}
	for _, tt := range []struct {
		name  string
		nodes []corev1.Node
		want  []string
	}{
		{name: "When no node has an eligibility label, it should return none", nodes: []corev1.Node{node("a", nil), node("b", map[string]string{"other": "true"})}},
		{name: "When the NodePool label is not true, it should not count the node", nodes: []corev1.Node{node("a", map[string]string{"hypershift.openshift.io/nodepool-globalps-enabled": "false"})}},
		{name: "When nodes have either label, it should return them sorted", nodes: []corev1.Node{
			node("c", map[string]string{"karpenter.sh/nodepool": "default"}),
			node("a", map[string]string{"hypershift.openshift.io/nodepool-globalps-enabled": "true"}),
			node("b", nil),
		}, want: []string{"a", "c"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := registryOverridesSyncerEligibleNodes(tt.nodes); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRegistryOverridesCurrentTemplateHash(t *testing.T) {
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Namespace: "clusters-hc", Name: "ignition-server", UID: "deploy-uid",
		Annotations: map[string]string{"deployment.kubernetes.io/revision": "3"},
	}}
	rs := func(name, ownerUID, revision, hash string) appsv1.ReplicaSet {
		return appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
			Namespace: "clusters-hc", Name: name,
			OwnerReferences: []metav1.OwnerReference{{UID: types.UID(ownerUID)}},
			Annotations:     map[string]string{"deployment.kubernetes.io/revision": revision},
			Labels:          map[string]string{"pod-template-hash": hash},
		}}
	}
	for _, tt := range []struct {
		name        string
		deployment  *appsv1.Deployment
		replicaSets []appsv1.ReplicaSet
		want        string
		wantError   bool
	}{
		{name: "When the Deployment owns a ReplicaSet at its revision, it should return its hash", deployment: deployment, replicaSets: []appsv1.ReplicaSet{rs("old", "deploy-uid", "2", "aaa"), rs("new", "deploy-uid", "3", "bbb")}, want: "bbb"},
		{name: "When the ReplicaSet at the revision belongs to another Deployment, it should fail", deployment: deployment, replicaSets: []appsv1.ReplicaSet{rs("other", "other-uid", "3", "ccc")}, wantError: true},
		{name: "When no ReplicaSet is at the revision, it should fail", deployment: deployment, replicaSets: []appsv1.ReplicaSet{rs("old", "deploy-uid", "2", "aaa")}, wantError: true},
		{name: "When the current ReplicaSet has no hash label, it should fail", deployment: deployment, replicaSets: []appsv1.ReplicaSet{rs("new", "deploy-uid", "3", "")}, wantError: true},
		{name: "When the Deployment has no revision, it should fail", deployment: &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "x"}}, wantError: true},
		{name: "When the Deployment is nil, it should fail without panic", wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := registryOverridesCurrentTemplateHash(tt.deployment, tt.replicaSets)
			if (err != nil) != tt.wantError || got != tt.want {
				t.Fatalf("got %q, %v; want %q, error %t", got, err, tt.want, tt.wantError)
			}
		})
	}
}

func TestRegistryOverridesCheckReleaseCovered(t *testing.T) {
	overrides := registryOverridesTestOverrides()
	covered := registryOverridesTestSource + registryOverridesTestDigest
	hc := func(release string, controlPlaneRelease string, annotations map[string]string) *hyperv1.HostedCluster {
		h := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "hc", Annotations: annotations}}
		h.Spec.Release.Image = release
		if controlPlaneRelease != "" {
			h.Spec.ControlPlaneRelease = &hyperv1.Release{Image: controlPlaneRelease}
		}
		return h
	}
	for _, tt := range []struct {
		name   string
		hc     *hyperv1.HostedCluster
		want   bool
		reason string
	}{
		{name: "When the release starts with an overridden source, it should report done", hc: hc(covered, "", nil), want: true},
		{name: "When both releases start with overridden sources, it should report done", hc: hc(covered, "registry.build01.ci.openshift.org/ci-op-abc/release"+registryOverridesTestDigest, nil), want: true},
		{name: "When the release starts with no overridden source, it should name it", hc: hc("quay.io/openshift-release-dev/ocp-release"+registryOverridesTestDigest, "", nil), reason: "starts with no overridden source"},
		{name: "When only the control plane release is not covered, it should name it", hc: hc(covered, "quay.io/other/release"+registryOverridesTestDigest, nil), reason: "quay.io/other/release"},
		{name: "When the release only shares a prefix with a source repository, it should not count as covered", hc: hc(registryOverridesTestSource+"-extra"+registryOverridesTestDigest, "", nil), reason: "starts with no overridden source"},
		{name: "When the CPO image is pinned by annotation, it should report it", hc: hc(covered, "", map[string]string{hyperv1.ControlPlaneOperatorImageAnnotation: "quay.io/x/cpo:latest"}), reason: "bypasses the release"},
		{name: "When the HostedCluster is nil, it should report it without panic", reason: "HostedCluster is nil"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := registryOverridesCheckReleaseCovered(tt.hc, overrides)
			if got != tt.want || !strings.Contains(reason, tt.reason) {
				t.Fatalf("got %t, %q; want %t, %q", got, reason, tt.want, tt.reason)
			}
		})
	}
}

func TestRegistryOverridesCheckPodImages(t *testing.T) {
	overrides := registryOverridesTestOverrides()
	mirrored := registryOverridesTestMirror + registryOverridesTestDigest
	source := registryOverridesTestSource + registryOverridesTestDigest
	pod := func(name string, images ...string) corev1.Pod {
		p := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-hc", Name: name}}
		for i, image := range images {
			p.Spec.Containers = append(p.Spec.Containers, corev1.Container{Name: "c" + string(rune('0'+i)), Image: image})
		}
		return p
	}
	withSourceInit := pod("init", mirrored)
	withSourceInit.Spec.InitContainers = []corev1.Container{{Name: "init", Image: source}}
	terminating := pod("terminating", source)
	terminating.DeletionTimestamp = &metav1.Time{}
	for _, tt := range []struct {
		name      string
		pods      []corev1.Pod
		overrides map[string]string
		want      bool
		reason    string
		wantError bool
	}{
		{name: "When every container runs a mirror image, it should report done", pods: []corev1.Pod{pod("a", mirrored, mirrored)}, want: true},
		{name: "When a container runs an image outside the sources, it should accept it", pods: []corev1.Pod{pod("a", mirrored, "registry.redhat.io/other@sha256:3")}, want: true},
		{name: "When a container runs the HO image, it should ignore it", pods: []corev1.Pod{pod("a", mirrored, registryOverridesTestHO)}, want: true},
		{name: "When a container still uses a source, it should name it", pods: []corev1.Pod{pod("a", mirrored, source)}, reason: "pod clusters-hc/a container c1 still uses"},
		{name: "When an init container still uses a source, it should name it", pods: []corev1.Pod{withSourceInit}, reason: "pod clusters-hc/init container init still uses"},
		{name: "When only a terminating pod uses a source, it should ignore it", pods: []corev1.Pod{terminating, pod("a", mirrored)}, want: true},
		{name: "When there are no overrides, it should fail", pods: []corev1.Pod{pod("a", mirrored)}, overrides: map[string]string{}, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			o := overrides
			if tt.overrides != nil {
				o = tt.overrides
			}
			got, reason, err := registryOverridesCheckPodImages(tt.pods, o, registryOverridesTestHO)
			if tt.wantError {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			if err != nil || got != tt.want || !strings.Contains(reason, tt.reason) {
				t.Fatalf("got %t, %q, %v; want %t, %q", got, reason, err, tt.want, tt.reason)
			}
		})
	}
}

func TestRegistryOverridesCheckNoPolicyMirror(t *testing.T) {
	release := registryOverridesTestSource + registryOverridesTestDigest
	hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "hc"}}
	hc.Spec.Release.Image = release
	withCPRelease := hc.DeepCopy()
	withCPRelease.Spec.ControlPlaneRelease = &hyperv1.Release{Image: "quay.io/other/release" + registryOverridesTestDigest}
	cpo := func(env ...corev1.EnvVar) *appsv1.Deployment {
		d := registryOverridesTestDeployment("control-plane-operator", registryOverridesTestMirror+registryOverridesTestDigest)
		d.Spec.Template.Spec.Containers[0].Env = env
		return &d
	}
	policy := func(value string) corev1.EnvVar { return corev1.EnvVar{Name: "OPENSHIFT_IMG_OVERRIDES", Value: value} }
	for _, tt := range []struct {
		name      string
		hc        *hyperv1.HostedCluster
		cpo       *appsv1.Deployment
		want      bool
		reason    string
		wantError bool
	}{
		{name: "When there are no management cluster mirrors, it should report done", hc: hc, cpo: cpo(policy("=")), want: true},
		{name: "When the variable is empty, it should report done", hc: hc, cpo: cpo(policy("")), want: true},
		{name: "When no management cluster mirror applies to the release, it should report done", hc: hc, cpo: cpo(policy("quay.io/openshift-release-dev=mirror.example.com/ocp")), want: true},
		{name: "When a management cluster mirror applies to the release, it should name it", hc: hc, cpo: cpo(policy("quay.io/a=m.example.com/a," + registryOverridesTestSource + "=other.example.com/ci")), reason: "management cluster mirror " + registryOverridesTestSource + "=other.example.com/ci applies"},
		{name: "When a registry-wide mirror applies to the release, it should name it", hc: hc, cpo: cpo(policy("quay-proxy.ci.openshift.org=other.example.com")), reason: "applies to HC clusters/hc release image"},
		{name: "When a mirror applies only to the control plane release, it should name it", hc: withCPRelease, cpo: cpo(policy("quay.io/other=m.example.com/other")), reason: "quay.io/other/release"},
		{name: "When the CPO has no mirrors variable, it should fail", hc: hc, cpo: cpo(), wantError: true},
		{name: "When the CPO has no primary container, it should fail", hc: hc, cpo: &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "control-plane-operator"}}, wantError: true},
		{name: "When the HostedCluster is nil, it should fail without panic", cpo: cpo(policy("=")), wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, reason, err := registryOverridesCheckNoPolicyMirror(tt.hc, tt.cpo)
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
