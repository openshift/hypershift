//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

type imageMirroringContextKey struct{}

// These expectations and payloads are authored independently of the producers.
var imageMirroringTestHC = &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{ImageContentSources: []hyperv1.ImageContentSource{
	{Source: "quay.io/libpod/registry", Mirrors: []string{"mirror.apps.ci.hypershift.dev/libpod/registry", "backup.apps.ci.hypershift.dev/libpod/registry"}},
	{Source: "mirror-source.hypershift-e2e.invalid/libpod/registry", Mirrors: []string{"mirror.apps.ci.hypershift.dev/libpod/registry"}},
}}}
var imageMirroringTestRoute = &routev1.Route{Spec: routev1.RouteSpec{Host: "mirror.apps.ci.hypershift.dev"}}
var imageMirroringTestIDMS = &configv1.ImageDigestMirrorSet{
	ObjectMeta: metav1.ObjectMeta{Name: "cluster", Labels: map[string]string{imageMirroringWorkerLabel: "worker", "unrelated": "allowed"}},
	Spec: configv1.ImageDigestMirrorSetSpec{ImageDigestMirrors: []configv1.ImageDigestMirrors{
		{Source: "quay.io/libpod/registry", Mirrors: []configv1.ImageMirror{"mirror.apps.ci.hypershift.dev/libpod/registry", "backup.apps.ci.hypershift.dev/libpod/registry"}},
		{Source: "mirror-source.hypershift-e2e.invalid/libpod/registry", Mirrors: []configv1.ImageMirror{"mirror.apps.ci.hypershift.dev/libpod/registry"}},
	}},
}

const imageMirroringTestYAML = `apiVersion: config.openshift.io/v1
kind: ImageDigestMirrorSet
metadata:
  name: cluster
  labels:
    machineconfiguration.openshift.io/role: worker
spec:
  imageDigestMirrors:
  - source: quay.io/libpod/registry
    mirrors:
    - mirror.apps.ci.hypershift.dev/libpod/registry
    - backup.apps.ci.hypershift.dev/libpod/registry
  - source: mirror-source.hypershift-e2e.invalid/libpod/registry
    mirrors:
    - mirror.apps.ci.hypershift.dev/libpod/registry
`

func TestImageMirroringExpected(t *testing.T) {
	tests := []struct {
		name   string
		change func(*hyperv1.HostedCluster, *routev1.Route)
		want   string
	}{
		{name: "When the fixture has two sources and multiple mirrors, it should snapshot the HC input"},
		{name: "When additional sources exist, it should retain all entries", change: func(h *hyperv1.HostedCluster, _ *routev1.Route) {
			h.Spec.ImageContentSources = append(h.Spec.ImageContentSources, hyperv1.ImageContentSource{Source: "quay.io/openshift-release-dev/ocp-release", Mirrors: []string{"backup.apps.ci.hypershift.dev/ocp-release"}})
		}},
		{name: "When ICS is nil, it should reject setup", change: func(h *hyperv1.HostedCluster, _ *routev1.Route) { h.Spec.ImageContentSources = nil }, want: "nonempty"},
		{name: "When ICS is empty, it should reject setup", change: func(h *hyperv1.HostedCluster, _ *routev1.Route) {
			h.Spec.ImageContentSources = []hyperv1.ImageContentSource{}
		}, want: "nonempty"},
		{name: "When a source is blank, it should reject setup", change: func(h *hyperv1.HostedCluster, _ *routev1.Route) { h.Spec.ImageContentSources[0].Source = " " }, want: "blank"},
		{name: "When mirrors are empty, it should reject setup", change: func(h *hyperv1.HostedCluster, _ *routev1.Route) { h.Spec.ImageContentSources[0].Mirrors = nil }, want: "no mirrors"},
		{name: "When a mirror is blank, it should reject setup", change: func(h *hyperv1.HostedCluster, _ *routev1.Route) { h.Spec.ImageContentSources[0].Mirrors[0] = " " }, want: "blank mirror"},
		{name: "When a source is duplicated, it should reject setup", change: func(h *hyperv1.HostedCluster, _ *routev1.Route) {
			h.Spec.ImageContentSources[1].Source = h.Spec.ImageContentSources[0].Source
		}, want: "duplicate"},
		{name: "When the real fixture source is missing, it should reject setup", change: func(h *hyperv1.HostedCluster, _ *routev1.Route) {
			h.Spec.ImageContentSources[0].Source = "quay.io/other/registry"
		}, want: "quay.io/libpod/registry"},
		{name: "When the virtual fixture source is missing, it should reject setup", change: func(h *hyperv1.HostedCluster, _ *routev1.Route) {
			h.Spec.ImageContentSources = h.Spec.ImageContentSources[:1]
		}, want: "mirror-source"},
		{name: "When Route host is empty, it should reject setup", change: func(_ *hyperv1.HostedCluster, r *routev1.Route) { r.Spec.Host = " " }, want: "empty host"},
		{name: "When Route host is wrong, it should reject setup", change: func(_ *hyperv1.HostedCluster, r *routev1.Route) { r.Spec.Host = "other.apps.ci.hypershift.dev" }, want: "exact Route host"},
		{name: "When a mirror only shares the host prefix, it should reject setup", change: func(h *hyperv1.HostedCluster, _ *routev1.Route) {
			h.Spec.ImageContentSources[1].Mirrors[0] = "mirror.apps.ci.hypershift.dev.evil/libpod/registry"
		}, want: "exact Route host"},
		{name: "When a mirror has no repository path, it should reject setup", change: func(h *hyperv1.HostedCluster, _ *routev1.Route) {
			h.Spec.ImageContentSources[1].Mirrors[0] = "mirror.apps.ci.hypershift.dev"
		}, want: "exact Route host"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc, route := imageMirroringTestHC.DeepCopy(), imageMirroringTestRoute.DeepCopy()
			if tt.change != nil {
				tt.change(hc, route)
			}
			before, routeBefore := hc.DeepCopy(), route.DeepCopy()
			got, err := imageMirroringExpected(hc, route)
			if tt.want != "" {
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("expected %q, got %v", tt.want, err)
				}
			} else {
				if err != nil || !reflect.DeepEqual(got, hc.Spec.ImageContentSources) {
					t.Fatalf("snapshot mismatch: %+v, %v", got, err)
				}
				got[0].Mirrors[0] = "changed"
			}
			if !reflect.DeepEqual(hc, before) || !reflect.DeepEqual(route, routeBefore) {
				t.Fatal("input mutated or snapshot aliases input")
			}
		})
	}
	t.Run("When HC is nil, it should reject setup without panic", func(t *testing.T) {
		if _, err := imageMirroringExpected(nil, imageMirroringTestRoute); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("When Route is nil, it should reject setup without panic", func(t *testing.T) {
		if _, err := imageMirroringExpected(imageMirroringTestHC, nil); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestImageMirroringConfigured(t *testing.T) {
	malformed := imageMirroringTestHC.DeepCopy()
	malformed.Spec.ImageContentSources = append(malformed.Spec.ImageContentSources, malformed.Spec.ImageContentSources[0])
	for _, tt := range []struct {
		name       string
		hc         *hyperv1.HostedCluster
		routeFound bool
		want       bool
	}{
		{name: "When HC is nil, it should report not configured", routeFound: true},
		{name: "When HC has no imageContentSources, it should report not configured", hc: &hyperv1.HostedCluster{}, routeFound: true},
		{name: "When the fixture Route is missing, it should report not configured", hc: imageMirroringTestHC},
		{name: "When imageContentSources and the fixture Route are present, it should report configured", hc: imageMirroringTestHC, routeFound: true, want: true},
		{name: "When imageContentSources are malformed, it should still report configured so strict checks fail", hc: malformed, routeFound: true, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := imageMirroringConfigured(tt.hc, tt.routeFound); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestImageMirroringCheckMappings(t *testing.T) {
	tests := []struct {
		name   string
		change func(*configv1.ImageDigestMirrorSet)
		want   bool
	}{
		{name: "When mappings match, it should accept ordered mirrors", want: true},
		{name: "When outer entries are reordered, it should accept them", want: true, change: func(o *configv1.ImageDigestMirrorSet) {
			o.Spec.ImageDigestMirrors[0], o.Spec.ImageDigestMirrors[1] = o.Spec.ImageDigestMirrors[1], o.Spec.ImageDigestMirrors[0]
		}},
		{name: "When an entry is missing, it should reject it", change: func(o *configv1.ImageDigestMirrorSet) { o.Spec.ImageDigestMirrors = o.Spec.ImageDigestMirrors[:1] }},
		{name: "When an entry is extra, it should reject it", change: func(o *configv1.ImageDigestMirrorSet) {
			o.Spec.ImageDigestMirrors = append(o.Spec.ImageDigestMirrors, configv1.ImageDigestMirrors{Source: "quay.io/extra/registry"})
		}},
		{name: "When sources are duplicated, it should reject them", change: func(o *configv1.ImageDigestMirrorSet) { o.Spec.ImageDigestMirrors[1] = o.Spec.ImageDigestMirrors[0] }},
		{name: "When sources are wrong, it should reject them", change: func(o *configv1.ImageDigestMirrorSet) { o.Spec.ImageDigestMirrors[1].Source = "quay.io/wrong/registry" }},
		{name: "When mirrors belong to the wrong source, it should reject them", change: func(o *configv1.ImageDigestMirrorSet) {
			o.Spec.ImageDigestMirrors[0].Mirrors, o.Spec.ImageDigestMirrors[1].Mirrors = o.Spec.ImageDigestMirrors[1].Mirrors, o.Spec.ImageDigestMirrors[0].Mirrors
		}},
		{name: "When mirror priority changes, it should reject it", change: func(o *configv1.ImageDigestMirrorSet) {
			m := o.Spec.ImageDigestMirrors[0].Mirrors
			m[0], m[1] = m[1], m[0]
		}},
		{name: "When a mirror is dropped, it should reject it", change: func(o *configv1.ImageDigestMirrorSet) {
			o.Spec.ImageDigestMirrors[0].Mirrors = o.Spec.ImageDigestMirrors[0].Mirrors[:1]
		}},
		{name: "When a mirror is duplicated, it should reject it", change: func(o *configv1.ImageDigestMirrorSet) {
			o.Spec.ImageDigestMirrors[0].Mirrors[1] = o.Spec.ImageDigestMirrors[0].Mirrors[0]
		}},
		{name: "When actual mappings are empty, it should reject them", change: func(o *configv1.ImageDigestMirrorSet) { o.Spec.ImageDigestMirrors = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := imageMirroringTestIDMS.DeepCopy()
			if tt.change != nil {
				tt.change(o)
			}
			expected := imageMirroringTestHC.DeepCopy().Spec.ImageContentSources
			before := o.DeepCopy()
			expectedBefore := imageMirroringTestHC.DeepCopy().Spec.ImageContentSources
			done, reason, err := imageMirroringCheckMappings(o.Spec.ImageDigestMirrors, expected)
			if err != nil || done != tt.want || (!done && !strings.Contains(reason, "expected HC mappings")) {
				t.Fatalf("done=%v reason=%q err=%v", done, reason, err)
			}
			if !reflect.DeepEqual(o, before) || !reflect.DeepEqual(expected, expectedBefore) {
				t.Fatal("comparison mutated inputs")
			}
		})
	}
	t.Run("When HC contains repeated mirrors, it should preserve their multiplicity", func(t *testing.T) {
		expected := imageMirroringTestHC.DeepCopy().Spec.ImageContentSources
		expected[1].Mirrors = append(expected[1].Mirrors, "mirror.apps.ci.hypershift.dev/libpod/registry")
		actual := imageMirroringTestIDMS.DeepCopy().Spec.ImageDigestMirrors
		actual[1].Mirrors = append(actual[1].Mirrors, "mirror.apps.ci.hypershift.dev/libpod/registry")
		if done, reason, err := imageMirroringCheckMappings(actual, expected); !done || err != nil {
			t.Fatalf("matching multiplicity rejected: %s %v", reason, err)
		}
		actual[1].Mirrors = actual[1].Mirrors[:1]
		if done, reason, err := imageMirroringCheckMappings(actual, expected); done || reason == "" || err != nil {
			t.Fatalf("deduplicated mirrors accepted: %v %q %v", done, reason, err)
		}
	})
	for _, tt := range []struct {
		name     string
		expected []hyperv1.ImageContentSource
	}{
		{name: "When expected mappings are nil, it should reject vacuous equality"},
		{name: "When expected mappings are empty, it should reject vacuous equality", expected: []hyperv1.ImageContentSource{}},
		{name: "When expected source is blank, it should reject it", expected: []hyperv1.ImageContentSource{{Mirrors: []string{"mirror/repo"}}, {Source: "other", Mirrors: []string{"mirror/repo"}}}},
		{name: "When expected mirrors are empty, it should reject them", expected: []hyperv1.ImageContentSource{{Source: "source"}, {Source: "other"}}},
		{name: "When expected sources are duplicated, it should reject them", expected: []hyperv1.ImageContentSource{{Source: "source", Mirrors: []string{"mirror/repo"}}, {Source: "source", Mirrors: []string{"mirror/repo"}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			done, reason, err := imageMirroringCheckMappings(imageMirroringTestIDMS.Spec.ImageDigestMirrors, tt.expected)
			if done || reason == "" || err != nil {
				t.Fatalf("%v %q %v", done, reason, err)
			}
		})
	}
}

func TestImageMirroringCheckIDMS(t *testing.T) {
	tests := []struct {
		name   string
		change func(*configv1.ImageDigestMirrorSet)
		want   string
	}{
		{name: "When the named IDMS matches with unrelated metadata, it should accept it"},
		{name: "When name is unrelated, it should reject it", change: func(o *configv1.ImageDigestMirrorSet) { o.Name = "unrelated" }, want: "cluster-scoped"},
		{name: "When namespace is nonempty, it should reject it", change: func(o *configv1.ImageDigestMirrorSet) { o.Namespace = "wrong" }, want: "cluster-scoped"},
		{name: "When worker label is missing, it should reject it", change: func(o *configv1.ImageDigestMirrorSet) { o.Labels = nil }, want: "worker role"},
		{name: "When worker label is wrong, it should reject it", change: func(o *configv1.ImageDigestMirrorSet) { o.Labels[imageMirroringWorkerLabel] = "master" }, want: "worker role"},
		{name: "When mappings are empty, it should reject them", change: func(o *configv1.ImageDigestMirrorSet) { o.Spec.ImageDigestMirrors = nil }, want: "mappings mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := imageMirroringTestIDMS.DeepCopy()
			if tt.change != nil {
				tt.change(o)
			}
			before := o.DeepCopy()
			done, reason, err := imageMirroringCheckIDMS([]*configv1.ImageDigestMirrorSet{o}, imageMirroringTestHC.Spec.ImageContentSources)
			if err != nil || done != (tt.want == "") || !strings.Contains(reason, tt.want) {
				t.Fatalf("%v %q %v", done, reason, err)
			}
			if !reflect.DeepEqual(o, before) {
				t.Fatal("mutated object")
			}
		})
	}
	for _, tt := range []struct {
		name    string
		objects []*configv1.ImageDigestMirrorSet
	}{
		{name: "When collection is nil, it should reject it"},
		{name: "When collection is empty, it should reject it", objects: []*configv1.ImageDigestMirrorSet{}},
		{name: "When target is nil, it should reject it", objects: []*configv1.ImageDigestMirrorSet{nil}},
		{name: "When multiple targets exist, it should reject them", objects: []*configv1.ImageDigestMirrorSet{imageMirroringTestIDMS, imageMirroringTestIDMS}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			done, reason, err := imageMirroringCheckIDMS(tt.objects, imageMirroringTestHC.Spec.ImageContentSources)
			if done || reason == "" || err != nil {
				t.Fatalf("%v %q %v", done, reason, err)
			}
		})
	}
}

func TestImageMirroringCheckConfigMap(t *testing.T) {
	tests := []struct {
		name   string
		change func(*corev1.ConfigMap)
		want   string
	}{
		{name: "When the payload matches, it should allow unrelated metadata and data"},
		{name: "When YAML key order and formatting differ, it should accept them", change: func(o *corev1.ConfigMap) {
			o.Data["config"] = strings.ReplaceAll(imageMirroringTestYAML, "apiVersion: config.openshift.io/v1\nkind: ImageDigestMirrorSet", "# harmless comment\nkind: ImageDigestMirrorSet\napiVersion: config.openshift.io/v1")
		}},
		{name: "When CM name is wrong, it should reject it", change: func(o *corev1.ConfigMap) { o.Name = "unrelated" }, want: "named HCP ignition target"},
		{name: "When CM namespace is wrong, it should reject it", change: func(o *corev1.ConfigMap) { o.Namespace = "wrong" }, want: "named HCP ignition target"},
		{name: "When core label is missing, it should reject it", change: func(o *corev1.ConfigMap) { o.Labels = nil }, want: "core-ignition-config"},
		{name: "When core label is wrong, it should reject it", change: func(o *corev1.ConfigMap) { o.Labels[imageMirroringCoreLabel] = "false" }, want: "core-ignition-config"},
		{name: "When config is missing, it should reject it", change: func(o *corev1.ConfigMap) { o.Data = nil }, want: "missing/empty"},
		{name: "When config is empty, it should reject it", change: func(o *corev1.ConfigMap) { o.Data["config"] = " \n" }, want: "missing/empty"},
		{name: "When YAML is malformed, it should retry with decode diagnostics", change: func(o *corev1.ConfigMap) { o.Data["config"] = "metadata: [" }, want: "YAML decode failed"},
		{name: "When kind is wrong, it should reject it", change: func(o *corev1.ConfigMap) {
			o.Data["config"] = strings.ReplaceAll(imageMirroringTestYAML, "ImageDigestMirrorSet", "MachineConfig")
		}, want: "expected config.openshift.io/v1"},
		{name: "When API version is wrong, it should reject it", change: func(o *corev1.ConfigMap) {
			o.Data["config"] = strings.ReplaceAll(imageMirroringTestYAML, "config.openshift.io/v1", "config.openshift.io/v2")
		}, want: "expected config.openshift.io/v1"},
		{name: "When GVK is absent, it should reject it", change: func(o *corev1.ConfigMap) { o.Data["config"] = "metadata: {name: cluster}" }, want: "expected config.openshift.io/v1"},
		{name: "When payload name is wrong, it should reject it", change: func(o *corev1.ConfigMap) {
			o.Data["config"] = strings.ReplaceAll(imageMirroringTestYAML, "name: cluster", "name: wrong")
		}, want: "cluster-scoped"},
		{name: "When payload namespace is nonempty, it should reject it", change: func(o *corev1.ConfigMap) {
			o.Data["config"] = strings.ReplaceAll(imageMirroringTestYAML, "name: cluster", "name: cluster\n  namespace: wrong")
		}, want: "cluster-scoped"},
		{name: "When payload worker label is wrong, it should reject it", change: func(o *corev1.ConfigMap) {
			o.Data["config"] = strings.ReplaceAll(imageMirroringTestYAML, "role: worker", "role: master")
		}, want: "worker role"},
		{name: "When payload worker label is missing, it should reject it", change: func(o *corev1.ConfigMap) {
			o.Data["config"] = strings.ReplaceAll(imageMirroringTestYAML, "machineconfiguration.openshift.io/role: worker", "unrelated: worker")
		}, want: "worker role"},
		{name: "When payload spec is empty, it should reject it", change: func(o *corev1.ConfigMap) {
			o.Data["config"] = strings.Split(imageMirroringTestYAML, "spec:")[0] + "spec: {}"
		}, want: "mappings mismatch"},
		{name: "When payload mappings are wrong, it should reject them", change: func(o *corev1.ConfigMap) {
			o.Data["config"] = strings.ReplaceAll(imageMirroringTestYAML, "backup.apps.ci.hypershift.dev", "wrong.apps.ci.hypershift.dev")
		}, want: "mappings mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-hc", Name: imageMirroringIgnitionName, Labels: map[string]string{imageMirroringCoreLabel: "true", "unrelated": "allowed"}}, Data: map[string]string{"config": imageMirroringTestYAML, "unrelated": "allowed"}}
			if tt.change != nil {
				tt.change(o)
			}
			before := o.DeepCopy()
			done, reason, err := imageMirroringCheckConfigMap([]*corev1.ConfigMap{o}, "clusters-hc", imageMirroringTestHC.Spec.ImageContentSources)
			if err != nil || done != (tt.want == "") || !strings.Contains(reason, tt.want) {
				t.Fatalf("%v %q %v", done, reason, err)
			}
			if !reflect.DeepEqual(o, before) {
				t.Fatal("mutated ConfigMap")
			}
		})
	}
	for _, tt := range []struct {
		name    string
		objects []*corev1.ConfigMap
		ns      string
	}{
		{name: "When collection is nil, it should reject it", ns: "clusters-hc"},
		{name: "When collection is empty, it should reject it", objects: []*corev1.ConfigMap{}, ns: "clusters-hc"},
		{name: "When target is nil, it should reject it", objects: []*corev1.ConfigMap{nil}, ns: "clusters-hc"},
		{name: "When multiple targets exist, it should reject them", objects: []*corev1.ConfigMap{{}, {}}, ns: "clusters-hc"},
		{name: "When namespace is empty, it should reject it", objects: []*corev1.ConfigMap{{ObjectMeta: metav1.ObjectMeta{Name: imageMirroringIgnitionName}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			done, reason, err := imageMirroringCheckConfigMap(tt.objects, tt.ns, imageMirroringTestHC.Spec.ImageContentSources)
			if done || reason == "" || err != nil {
				t.Fatalf("%v %q %v", done, reason, err)
			}
		})
	}
}

func TestImageMirroringGetIDMS(t *testing.T) {
	sentinel := errors.New("API unavailable")
	for _, tt := range []struct {
		name    string
		objects []crclient.Object
		failure error
		found   bool
	}{
		{name: "When the exact hosted IDMS exists, it should return the observed singleton", objects: []crclient.Object{imageMirroringTestIDMS}, found: true},
		{name: "When IDMS is absent, it should preserve NotFound"},
		{name: "When only an unrelated IDMS exists, it should preserve NotFound", objects: []crclient.Object{&configv1.ImageDigestMirrorSet{ObjectMeta: metav1.ObjectMeta{Name: "unrelated"}}}},
		{name: "When API fails, it should preserve the error", failure: sentinel},
	} {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := configv1.Install(scheme); err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(t.Context(), imageMirroringContextKey{}, "poll context")
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.objects...).WithInterceptorFuncs(interceptor.Funcs{Get: func(got context.Context, c crclient.WithWatch, key crclient.ObjectKey, o crclient.Object, opts ...crclient.GetOption) error {
				if got != ctx || key != (crclient.ObjectKey{Name: "cluster"}) {
					t.Fatalf("wrong context or key: %v", key)
				}
				if tt.failure != nil {
					return tt.failure
				}
				return c.Get(got, key, o, opts...)
			}}).Build()
			objects, err := imageMirroringGetIDMS(ctx, client)
			if tt.found {
				if err != nil || len(objects) != 1 || !reflect.DeepEqual(objects[0].Spec, imageMirroringTestIDMS.Spec) {
					t.Fatalf("got %+v %v", objects, err)
				}
			} else {
				if objects != nil {
					t.Fatal("synthetic objects returned")
				}
				if tt.failure != nil {
					if !errors.Is(err, sentinel) {
						t.Fatalf("lost API error: %v", err)
					}
				} else if !apierrors.IsNotFound(err) {
					t.Fatalf("lost NotFound: %v", err)
				}
			}
		})
	}
}

func TestImageMirroringGetConfigMap(t *testing.T) {
	sentinel := errors.New("API unavailable")
	target := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-hc", Name: imageMirroringIgnitionName}, Data: map[string]string{"config": imageMirroringTestYAML}}
	for _, tt := range []struct {
		name    string
		objects []crclient.Object
		failure error
		ns      string
		found   bool
	}{
		{name: "When the exact HCP ConfigMap exists, it should return its payload", objects: []crclient.Object{target}, ns: "clusters-hc", found: true},
		{name: "When ConfigMap is absent, it should preserve NotFound", ns: "clusters-hc"},
		{name: "When only a wrong namespace ConfigMap exists, it should preserve NotFound", objects: []crclient.Object{&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "wrong", Name: imageMirroringIgnitionName}}}, ns: "clusters-hc"},
		{name: "When only an unrelated ConfigMap exists, it should preserve NotFound", objects: []crclient.Object{&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-hc", Name: "unrelated"}}}, ns: "clusters-hc"},
		{name: "When API fails, it should preserve the error", failure: sentinel, ns: "clusters-hc"},
		{name: "When namespace is empty, it should fail without an API call"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(t.Context(), imageMirroringContextKey{}, "poll context")
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.objects...).WithInterceptorFuncs(interceptor.Funcs{Get: func(got context.Context, c crclient.WithWatch, key crclient.ObjectKey, o crclient.Object, opts ...crclient.GetOption) error {
				if tt.ns == "" {
					t.Fatal("unexpected API call")
				}
				if got != ctx || key != (crclient.ObjectKey{Namespace: "clusters-hc", Name: imageMirroringIgnitionName}) {
					t.Fatalf("wrong context or key: %v", key)
				}
				if tt.failure != nil {
					return tt.failure
				}
				return c.Get(got, key, o, opts...)
			}}).Build()
			objects, err := imageMirroringGetConfigMap(ctx, client, tt.ns)
			if tt.found {
				if err != nil || len(objects) != 1 || !reflect.DeepEqual(objects[0].Data, target.Data) {
					t.Fatalf("got %+v %v", objects, err)
				}
			} else {
				if objects != nil {
					t.Fatal("synthetic objects returned")
				}
				if tt.ns == "" {
					if err == nil || !strings.Contains(err.Error(), "namespace") {
						t.Fatalf("expected namespace error: %v", err)
					}
				} else if tt.failure != nil {
					if !errors.Is(err, sentinel) {
						t.Fatalf("lost API error: %v", err)
					}
				} else if !apierrors.IsNotFound(err) {
					t.Fatalf("lost NotFound: %v", err)
				}
			}
		})
	}
}
