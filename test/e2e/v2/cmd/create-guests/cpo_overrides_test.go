//go:build e2ev2

package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	imagev1 "github.com/openshift/api/image/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func cpoOverrideTestPlan(t *testing.T) *lifecycle.TestPlan {
	t.Helper()
	plan, err := lifecycle.ReadTestPlan(filepath.Join("..", "..", "..", "..", "..", "contrib", "aws-e2e-v2-cpo-overrides.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestValidateCPOOverrideSelection(t *testing.T) {
	platform := &lifecycle.AWSPlatformConfig{}
	plan := cpoOverrideTestPlan(t)
	specs := plan.FilterClusterSpecs(platform.ClusterSpecs("ordinary-latest", "ordinary-previous"))
	for _, test := range []struct {
		name      string
		platform  string
		legacy    bool
		mutate    func(*lifecycle.TestPlan, *[]lifecycle.ClusterSpec)
		wantError bool
	}{
		{name: "When the dedicated plan is explicit, it should work without a process mode flag", platform: "aws"},
		{name: "When the legacy flag selects the dedicated plan, it should preserve that request", platform: "aws", legacy: true},
		{name: "When the platform is unsupported, it should fail before provisioning", platform: "azure", wantError: true},
		{name: "When a release is overridden, it should reject the nonmapped fixture", platform: "aws", mutate: func(plan *lifecycle.TestPlan, specs *[]lifecycle.ClusterSpec) { (*specs)[0].ReleaseImage = "different" }, wantError: true},
		{name: "When a pair member is absent, it should reject the incomplete mode", platform: "aws", mutate: func(plan *lifecycle.TestPlan, specs *[]lifecycle.ClusterSpec) { *specs = (*specs)[:1] }, wantError: true},
		{name: "When both entries duplicate latest, it should reject the missing previous", platform: "aws", mutate: func(plan *lifecycle.TestPlan, specs *[]lifecycle.ClusterSpec) { (*specs)[1] = (*specs)[0] }, wantError: true},
		{name: "When optional coverage replaces the required label, it should reject a possible skip", platform: "aws", mutate: func(plan *lifecycle.TestPlan, specs *[]lifecycle.ClusterSpec) {
			plan.TestMatrix.Sequential[0].Steps[2].LabelFilter = "hosted-cluster-cpo"
		}, wantError: true},
		{name: "When a skip filter is supplied, it should reject the disabled assertion", platform: "aws", mutate: func(plan *lifecycle.TestPlan, specs *[]lifecycle.ClusterSpec) {
			plan.TestMatrix.Sequential[0].Steps[2].Skip = ".*"
		}, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			currentPlan := cpoOverrideTestPlan(t)
			currentSpecs := append([]lifecycle.ClusterSpec{}, specs...)
			if test.mutate != nil {
				test.mutate(currentPlan, &currentSpecs)
			}
			selected, err := validateCPOOverrideSelection(currentPlan, test.platform, currentSpecs, test.legacy)
			if (err != nil) != test.wantError || selected == test.wantError {
				t.Fatalf("unexpected selection: %v, %v", selected, err)
			}
		})
	}
	defaults := platform.DefaultTestPlan()
	defaultSpecs := defaults.FilterClusterSpecs(platform.ClusterSpecs("latest", "previous"))
	if selected, err := validateCPOOverrideSelection(&defaults, "aws", defaultSpecs, false); err != nil || selected {
		t.Fatalf("default coverage changed: %v, %v", selected, err)
	}
	if _, err := validateCPOOverrideSelection(&defaults, "aws", defaultSpecs, true); err == nil {
		t.Fatal("legacy flag must not run unrelated default suites instead of mapped-pair mode")
	}
	if selected, err := validateCPOOverrideSelection(&defaults, "azure", nil, true); err != nil || selected {
		t.Fatal("AWS-specific legacy mode handling must not alter nonAWS consumers")
	}
}

func TestValidateCPOOverrideGroups(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		mutate    func(*lifecycle.TestPlan)
		wantError bool
	}{
		{name: "When actual YAML composes both mapped fixture lanes, it should accept exact public hooks and original required children"},
		{name: "When the required assertion is missing, it should reject incomplete coverage", mutate: func(plan *lifecycle.TestPlan) {
			plan.TestMatrix.Sequential[0].Steps = plan.TestMatrix.Sequential[0].Steps[:2]
		}, wantError: true},
		{name: "When required assertion is duplicated, it should reject duplicate coverage", mutate: func(plan *lifecycle.TestPlan) {
			plan.TestMatrix.Sequential[0].Steps[3] = plan.TestMatrix.Sequential[0].Steps[2]
		}, wantError: true},
		{name: "When a hook selects arbitrary feature labels, it should reject unsafe expansion", mutate: func(plan *lifecycle.TestPlan) {
			plan.TestMatrix.Sequential[0].Steps[1].LabelFilter = "control-plane-upgrade"
		}, wantError: true},
		{name: "When common before includes ingress mutation, it should reject changed hook semantics", mutate: func(plan *lifecycle.TestPlan) {
			plan.TestMatrix.Sequential[0].Steps[1].LabelFilter = plan.TestMatrix.Sequential[0].Steps[3].LabelFilter
		}, wantError: true},
		{name: "When common after selects initial workers, it should reject stale creation intent", mutate: func(plan *lifecycle.TestPlan) {
			plan.TestMatrix.Sequential[0].Steps[3].LabelFilter = "hosted-cluster-health-configured-workers"
		}, wantError: true},
		{name: "When configured initial is omitted, it should reject incomplete root validation", mutate: func(plan *lifecycle.TestPlan) {
			plan.TestMatrix.Sequential[0].Steps[0].LabelFilter = "hosted-cluster-health"
		}, wantError: true},
		{name: "When a hook has a skip filter, it should reject hidden assertions", mutate: func(plan *lifecycle.TestPlan) { plan.TestMatrix.Sequential[0].Steps[1].Skip = ".*" }, wantError: true},
		{name: "When a child uses an unmapped variant, it should reject fixture escape", mutate: func(plan *lifecycle.TestPlan) { plan.TestMatrix.Sequential[0].Steps[3].Variant = "public" }, wantError: true},
		{name: "When both lanes use latest, it should reject missing previous", mutate: func(plan *lifecycle.TestPlan) { plan.TestMatrix.Sequential[1] = plan.TestMatrix.Sequential[0] }, wantError: true},
		{name: "When a lane is absent, it should reject an incomplete pair", mutate: func(plan *lifecycle.TestPlan) { plan.TestMatrix.Sequential = plan.TestMatrix.Sequential[:1] }, wantError: true},
		{name: "When an extra parallel child exists, it should reject expanded ownership", mutate: func(plan *lifecycle.TestPlan) {
			plan.TestMatrix.Parallel = []lifecycle.TestGroup{{Name: "extra", Variant: "public", LabelFilter: "hosted-cluster-cpo-required"}}
		}, wantError: true},
		{name: "When original required child is renamed, it should reject identity drift", mutate: func(plan *lifecycle.TestPlan) { plan.TestMatrix.Sequential[0].Steps[2].Name = "optional" }, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			plan := cpoOverrideTestPlan(t)
			if testCase.mutate != nil {
				testCase.mutate(plan)
			} else if err := plan.Validate((&lifecycle.AWSPlatformConfig{}).ClusterSpecs("unrelated-latest", "unrelated-previous")); err != nil {
				t.Fatal(err)
			}
			if err := validateCPOOverrideGroups(plan); (err != nil) != testCase.wantError {
				t.Fatalf("CPO hook validation error=%v", err)
			}
		})
	}
	if err := validateCPOOverrideGroups(nil); err == nil {
		t.Fatal("missing CPO plan was accepted")
	}
}

func TestPreflightSelectedCPOOverrides(t *testing.T) {
	sentinel := errors.New("injected preflight failure")
	for _, test := range []struct {
		name           string
		selected       bool
		preflightError error
	}{
		{name: "When CPO mode is disabled, it should not invoke the prerequisite", preflightError: sentinel},
		{name: "When CPO mode is enabled, it should invoke a successful prerequisite", selected: true},
		{name: "When a selected prerequisite fails, it should preserve its error", selected: true, preflightError: sentinel},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			err := preflightSelectedCPOOverrides(test.selected, func() error { called = true; return test.preflightError })
			if called != test.selected {
				t.Fatalf("preflight invoked=%v, selected=%v", called, test.selected)
			}
			if test.selected && !errors.Is(err, test.preflightError) || !test.selected && err != nil {
				t.Fatalf("unexpected preflight result: %v", err)
			}
		})
	}
	if err := preflightSelectedCPOOverrides(false, nil); err != nil {
		t.Fatal("unselected CPO mode must not require a callback")
	}
}

func readyCPOOverrideOperator(namespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "operator", Namespace: namespace, Generation: 2},
		Spec: appsv1.DeploymentSpec{Replicas: ptr.To[int32](1), Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{hyperv1.OperatorComponent: "operator"}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "operator", Command: []string{"/usr/bin/hypershift-operator"}, Env: []corev1.EnvVar{{Name: "ENABLE_CPO_OVERRIDES", Value: "1"}}}}},
		}},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 2, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1},
	}
}

func TestCPOOverrideOperatorReady(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*appsv1.Deployment)
	}{
		{name: "When the deployment is stale, it should fail", mutate: func(deployment *appsv1.Deployment) { deployment.Status.ObservedGeneration-- }},
		{name: "When replicas are absent, it should fail", mutate: func(deployment *appsv1.Deployment) { deployment.Spec.Replicas = nil }},
		{name: "When the operator is scaled to zero, it should fail", mutate: func(deployment *appsv1.Deployment) { deployment.Spec.Replicas = ptr.To[int32](0) }},
		{name: "When updated replicas are old, it should fail", mutate: func(deployment *appsv1.Deployment) { deployment.Status.UpdatedReplicas = 0 }},
		{name: "When ready replicas are missing, it should fail", mutate: func(deployment *appsv1.Deployment) { deployment.Status.ReadyReplicas = 0 }},
		{name: "When available replicas are missing, it should fail", mutate: func(deployment *appsv1.Deployment) { deployment.Status.AvailableReplicas = 0 }},
		{name: "When extra old replicas remain, it should fail", mutate: func(deployment *appsv1.Deployment) { deployment.Status.Replicas = 2 }},
		{name: "When unavailable replicas remain, it should fail", mutate: func(deployment *appsv1.Deployment) { deployment.Status.UnavailableReplicas = 1 }},
		{name: "When overrides are disabled, it should fail", mutate: func(deployment *appsv1.Deployment) { deployment.Spec.Template.Spec.Containers[0].Env[0].Value = "0" }},
		{name: "When enablement references an unresolved secret, it should fail", mutate: func(deployment *appsv1.Deployment) {
			deployment.Spec.Template.Spec.Containers[0].Env[0].ValueFrom = &corev1.EnvVarSource{}
		}},
		{name: "When another binary impersonates the name, it should fail", mutate: func(deployment *appsv1.Deployment) {
			deployment.Spec.Template.Spec.Containers[0].Command[0] = "/bin/other-operator"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			deployment := readyCPOOverrideOperator("custom-management")
			test.mutate(deployment)
			if err := cpoOverrideOperatorReady(deployment); err == nil {
				t.Fatal("expected unavailable or disabled operator prerequisite to fail")
			}
		})
	}
	if err := cpoOverrideOperatorReady(readyCPOOverrideOperator("custom-management")); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCPOOverrideOperator(t *testing.T) {
	operator := readyCPOOverrideOperator("custom-management")
	client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(operator, readyCPOOverrideOperator("another-management")).Build()
	if err := validateCPOOverrideOperator(t.Context(), client, ""); err == nil {
		t.Fatal("ambiguous installed operators must fail")
	}
	if err := validateCPOOverrideOperator(t.Context(), client, "custom-management"); err != nil {
		t.Fatal(err)
	}
	if err := validateCPOOverrideOperator(t.Context(), client, "missing"); err == nil {
		t.Fatal("missing installed operator must fail")
	}
	observed := &appsv1.Deployment{}
	if err := client.Get(t.Context(), crclient.ObjectKeyFromObject(operator), observed); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(observed.Spec, operator.Spec) {
		t.Fatal("read-only prerequisite mutated the installed deployment")
	}
	sentinel := errors.New("injected deployment listing failure")
	failingClient := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithInterceptorFuncs(interceptor.Funcs{List: func(context.Context, crclient.WithWatch, crclient.ObjectList, ...crclient.ListOption) error {
		return sentinel
	}}).Build()
	if err := validateCPOOverrideOperator(t.Context(), failingClient, ""); !errors.Is(err, sentinel) {
		t.Fatalf("listing failure was not preserved: %v", err)
	}
	if err := preflightCPOOverrides(t.Context(), failingClient, nil, "nonexistent-pull-secret", ""); !errors.Is(err, sentinel) {
		t.Fatalf("operator validation must precede release lookup: %v", err)
	}
}

type cpoReleaseProvider struct {
	versions map[string]string
	err      error
}

func (provider *cpoReleaseProvider) Lookup(ctx context.Context, image string, pullSecret []byte) (*releaseinfo.ReleaseImage, error) {
	if provider.err != nil {
		return nil, provider.err
	}
	return &releaseinfo.ReleaseImage{ImageStream: &imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: provider.versions[image]}}}, nil
}

func TestValidateCPOOverrideReleases(t *testing.T) {
	specs := []lifecycle.ClusterSpec{{Variant: "cpo-override-latest", ReleaseImage: "latest"}, {Variant: "cpo-override-previous", ReleaseImage: "previous"}}
	for _, test := range []struct {
		name, latest, previous string
		wantError              bool
	}{
		{name: "When the actual pair is mapped, it should accept both releases", latest: "4.17.43", previous: "4.17.20"},
		{name: "When actual content is unmapped, it should fail", latest: "0.0.0", previous: "4.17.20", wantError: true},
		{name: "When content metadata is absent, it should fail", previous: "4.17.20", wantError: true},
		{name: "When tags alias the same version, it should fail", latest: "4.17.20", previous: "4.17.20", wantError: true},
		{name: "When the pair is reversed, it should fail", latest: "4.17.20", previous: "4.17.43", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &cpoReleaseProvider{versions: map[string]string{"latest": test.latest, "previous": test.previous}}
			if err := validateCPOOverrideReleases(t.Context(), provider, specs, nil); (err != nil) != test.wantError {
				t.Fatalf("unexpected release validation: %v", err)
			}
		})
	}
	sentinel := errors.New("injected registry failure")
	if err := validateCPOOverrideReleases(t.Context(), &cpoReleaseProvider{err: sentinel}, specs, nil); !errors.Is(err, sentinel) {
		t.Fatalf("registry failure was not preserved: %v", err)
	}
}
