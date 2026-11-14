package omnicpo

import (
	"errors"
	"testing"

	"github.com/openshift/hypershift/support/config"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/blang/semver"
)

// fakeResolveContext is a ResolveContext for unit tests.
type fakeResolveContext struct {
	version string
	images  map[string]string
}

func (f fakeResolveContext) ControlPlaneVersion() string { return f.version }
func (f fakeResolveContext) GetImage(name string) string { return f.images[name] }

func TestRangeContains(t *testing.T) {
	v423 := semver.MustParse("4.23.0")
	v510 := semver.MustParse("5.1.0")

	tests := []struct {
		name    string
		r       Range
		version string
		want    bool
	}{
		{name: "When the plane minor is below the bound, it should be in range", r: Below(v423), version: "4.22.9", want: true},
		{name: "When the plane minor equals the bound, it should be out of range", r: Below(v423), version: "4.23.0", want: false},
		{name: "When the plane patch is above the bound at the same minor, it should be out of range", r: Below(v423), version: "4.23.5", want: false},
		{name: "When the plane major is above the bound, it should be out of range", r: Below(v423), version: "5.0.0", want: false},
		// The prerelease fix: a 4.23 prerelease is a 4.23 plane, so it must NOT be below 4.23
		// (full-semver GTE would sort 4.23.0-ec.2 below 4.23.0 and wrongly strip its flags).
		{name: "When the plane is a prerelease of the bound minor, it should be out of range", r: Below(v423), version: "4.23.0-ec.2", want: false},
		{name: "When the plane is a nightly prerelease of the bound minor, it should be out of range", r: Below(v423), version: "4.23.0-0.nightly-2026", want: false},
		{name: "When the plane is a prerelease below the bound minor, it should be in range", r: Below(v423), version: "4.22.0-ec.1", want: true},
		{name: "When 4.23 is below the 5.1 bound, it should be in range", r: Below(v510), version: "4.23.0", want: true},
		{name: "When 5.0 is below the 5.1 bound, it should be in range", r: Below(v510), version: "5.0.0", want: true},
		{name: "When 5.1 equals the 5.1 bound, it should be out of range", r: Below(v510), version: "5.1.0", want: false},
		{name: "When the plane equals the lower bound, it should be in range", r: Between(v423, v510), version: "4.23.0", want: true},
		{name: "When the plane is between the bounds, it should be in range", r: Between(v423, v510), version: "5.0.0", want: true},
		{name: "When the plane equals the upper bound, it should be out of range", r: Between(v423, v510), version: "5.1.0", want: false},
		{name: "When the plane is below the lower bound, it should be out of range", r: Between(v423, v510), version: "4.22.0", want: false},
		{name: "When the version is unparsable, it should be out of range", r: Below(v423), version: "not-a-version", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.Contains(tc.version); got != tc.want {
				t.Errorf("Contains(%q) = %v, want %v", tc.version, got, tc.want)
			}
		})
	}
}

func deploymentWith(container corev1.Container) *appsv1.Deployment {
	return &appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{container}},
			},
		},
	}
}

func container(d *appsv1.Deployment, name string) *corev1.Container {
	for i := range d.Spec.Template.Spec.Containers {
		if d.Spec.Template.Spec.Containers[i].Name == name {
			return &d.Spec.Template.Spec.Containers[i]
		}
	}
	return nil
}

func TestStripArgs(t *testing.T) {
	d := deploymentWith(corev1.Container{
		Name: "op",
		Args: []string{"--serve", "--tls-min-version=VersionTLS12", "--config=/x", "--keep=1"},
	})
	if err := StripArgs("op", "--tls-min-version", "--config=")(nil, d); err != nil {
		t.Fatal(err)
	}
	got := container(d, "op").Args
	want := []string{"--serve", "--keep=1"}
	if !equalStrings(got, want) {
		t.Errorf("Args = %v, want %v", got, want)
	}
}

func TestStripCommandArgs(t *testing.T) {
	d := deploymentWith(corev1.Container{
		Name:    "webhook",
		Command: []string{"/bin/webhook", "--port=4443", "--tls-cipher-suites=A,B"},
	})
	if err := StripCommandArgs("webhook", "--tls-cipher-suites")(nil, d); err != nil {
		t.Fatal(err)
	}
	got := container(d, "webhook").Command
	want := []string{"/bin/webhook", "--port=4443"}
	if !equalStrings(got, want) {
		t.Errorf("Command = %v, want %v", got, want)
	}
}

func TestStripCommandFlags(t *testing.T) {
	d := deploymentWith(corev1.Container{
		Name:    "op",
		Command: []string{"/bin/op", "--haproxy-image", "img:1", "--default-haproxy-version", "3.2", "--serve"},
	})
	if err := StripCommandFlags("op", "--haproxy-image", "--default-haproxy-version")(nil, d); err != nil {
		t.Fatal(err)
	}
	got := container(d, "op").Command
	want := []string{"/bin/op", "--serve"}
	if !equalStrings(got, want) {
		t.Errorf("Command = %v, want %v", got, want)
	}
}

func TestStripShellScriptArgLines(t *testing.T) {
	d := deploymentWith(corev1.Container{
		Name: "op",
		Args: []string{"-c", "exec /bin/op \\\n--config=/etc/x \\\n--serve"},
	})
	if err := StripShellScriptArgLines("op", "--config=")(nil, d); err != nil {
		t.Fatal(err)
	}
	got := container(d, "op").Args[1]
	want := "exec /bin/op \\\n--serve"
	if got != want {
		t.Errorf("Args[1] = %q, want %q", got, want)
	}
}

func TestStripEnv(t *testing.T) {
	d := deploymentWith(corev1.Container{
		Name: "op",
		Env: []corev1.EnvVar{
			{Name: "KEEP", Value: "1"},
			{Name: "HAPROXY_32_IMAGE", Value: "img"},
			{Name: "DEFAULT_HAPROXY_VERSION", Value: "3.2"},
		},
	})
	if err := StripEnv("op", "HAPROXY_32_IMAGE", "DEFAULT_HAPROXY_VERSION")(nil, d); err != nil {
		t.Fatal(err)
	}
	got := container(d, "op").Env
	if len(got) != 1 || got[0].Name != "KEEP" {
		t.Errorf("Env = %v, want only KEEP", got)
	}
}

func TestSetImage(t *testing.T) {
	d := deploymentWith(corev1.Container{Name: "haproxy", Image: "haproxy-router-haproxy32"})
	rc := fakeResolveContext{images: map[string]string{"haproxy-router": "quay.io/x/haproxy-router:tag"}}
	if err := SetImage("haproxy", "haproxy-router")(rc, d); err != nil {
		t.Fatal(err)
	}
	if got := container(d, "haproxy").Image; got != "quay.io/x/haproxy-router:tag" {
		t.Errorf("Image = %q, want the resolved haproxy-router image", got)
	}
}

func TestApply(t *testing.T) {
	strip := Transform[*appsv1.Deployment]{
		Reason: "strip --config on old operands",
		When:   Below(semver.MustParse("4.23.0")),
		Do:     StripArgs("op", "--config="),
	}
	newDeploy := func() *appsv1.Deployment {
		return deploymentWith(corev1.Container{Name: "op", Args: []string{"--serve", "--config=/x"}})
	}

	t.Run("no-op when OMNI-CPO is off", func(t *testing.T) {
		t.Setenv(config.OmniCPOEnvVar, "0")
		d := newDeploy()
		if err := Apply(fakeResolveContext{version: "4.20.0"}, d, []Transform[*appsv1.Deployment]{strip}); err != nil {
			t.Fatal(err)
		}
		if len(container(d, "op").Args) != 2 {
			t.Errorf("expected args untouched when OMNI-CPO off, got %v", container(d, "op").Args)
		}
	})

	t.Run("applies in-range transform when on", func(t *testing.T) {
		t.Setenv(config.OmniCPOEnvVar, "1")
		d := newDeploy()
		if err := Apply(fakeResolveContext{version: "4.20.0"}, d, []Transform[*appsv1.Deployment]{strip}); err != nil {
			t.Fatal(err)
		}
		if got := container(d, "op").Args; !equalStrings(got, []string{"--serve"}) {
			t.Errorf("expected --config stripped, got %v", got)
		}
	})

	t.Run("skips out-of-range transform when on", func(t *testing.T) {
		t.Setenv(config.OmniCPOEnvVar, "1")
		d := newDeploy()
		if err := Apply(fakeResolveContext{version: "5.0.0"}, d, []Transform[*appsv1.Deployment]{strip}); err != nil {
			t.Fatal(err)
		}
		if len(container(d, "op").Args) != 2 {
			t.Errorf("expected args kept for a 5.0 plane, got %v", container(d, "op").Args)
		}
	})

	t.Run("propagates a mutation error with the reason", func(t *testing.T) {
		t.Setenv(config.OmniCPOEnvVar, "1")
		boom := Transform[*appsv1.Deployment]{
			Reason: "boom",
			When:   Below(semver.MustParse("4.23.0")),
			Do:     func(_ ResolveContext, _ *appsv1.Deployment) error { return errors.New("kaboom") },
		}
		err := Apply(fakeResolveContext{version: "4.20.0"}, newDeploy(), []Transform[*appsv1.Deployment]{boom})
		if err == nil {
			t.Fatal("expected an error")
		}
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
