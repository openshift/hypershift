//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	kasconst "github.com/openshift/hypershift/pkg/kas"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	utilexec "k8s.io/client-go/util/exec"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/blang/semver"
)

func TestSecurityAdmissionVersion(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		desired    string
		origin     string
		observed   string
		standalone bool
		nilStatus  bool
		want       string
		wantError  bool
	}{
		{name: "When desired version differs from origin and history, it should use the control plane target", desired: "5.1.0", origin: "4.17.0", observed: "4.16.0", want: "5.1.0"},
		{name: "When desired version is absent, it should use the independent origin not observed history", origin: "4.22.0", observed: "5.1.0", want: "4.22.0"},
		{name: "When status is absent, it should use the independent origin", nilStatus: true, origin: "4.18.0", want: "4.18.0"},
		{name: "When desired version is malformed, it should return the error", desired: "invalid", origin: "5.1.0", wantError: true},
		{name: "When desired exists but independent metadata is unresolved, it should retain the CPO specific gate", desired: "4.18.0", want: "4.18.0"},
		{name: "When fallback metadata is unresolved, it should not use observed history", observed: "5.1.0", wantError: true},
		{name: "When standalone has no independent inputs, it should retain observed fallback", standalone: true, observed: "4.22.3", want: "4.22.0"},
		{name: "When standalone history is absent, it should fail fallback", standalone: true, wantError: true},
		{name: "When standalone history is malformed, it should fail fallback", standalone: true, observed: "invalid", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			tc, hostedCluster := securityAdmissionFixture(t, testCase.origin, testCase.desired, testCase.observed)
			if testCase.standalone {
				t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", "")
				resetFailureConditionPhase(tc)
			}
			if testCase.nilStatus {
				hostedCluster.Status.Version = nil
			}
			actual, err := securityAdmissionVersion(tc, hostedCluster)
			if (err != nil) != testCase.wantError || err == nil && actual.String() != testCase.want {
				t.Fatalf("version = %s, error = %v", actual, err)
			}
		})
	}
}

func TestSecurityAdmissionAtLeast(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		origin    string
		desired   string
		minimum   string
		want      bool
		wantError bool
	}{
		{name: "When CPO is 4.18 and origin is older, it should enable existence and denied audit checks", origin: "4.17.0", desired: "4.18.0", minimum: "4.18.0", want: true},
		{name: "When origin is 5.1 but CPO is older, it should not enable protected deletion", origin: "5.1.0", desired: "4.22.0", minimum: "5.1.0"},
		{name: "When CPO is 5.1 but origin is older, it should enable protected deletion", origin: "4.22.0", desired: "5.1.0", minimum: "5.1.0", want: true},
		{name: "When CPO is below 4.18, it should not enable existence and denied audit checks", origin: "5.1.0", desired: "4.17.0", minimum: "4.18.0"},
		{name: "When desired is absent, it should gate on the independent fallback", origin: "4.18.0", minimum: "4.18.0", want: true},
		{name: "When desired is malformed, it should fail rather than skip", origin: "4.18.0", desired: "invalid", minimum: "4.18.0", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			tc, hc := securityAdmissionFixture(t, testCase.origin, testCase.desired, "5.1.0")
			actual, err := securityAdmissionAtLeast(tc, hc, semver.MustParse(testCase.minimum))
			if (err != nil) != testCase.wantError || err == nil && actual != testCase.want {
				t.Fatalf("eligible=%v error=%v, want %v error=%v", actual, err, testCase.want, testCase.wantError)
			}
		})
	}
}

func TestSecurityRequiredAdmissionPolicies(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		origin    string
		desired   string
		latest    string
		cpLatest  bool
		target    bool
		wantRBAC  bool
		wantError bool
	}{
		{name: "When origin is 5.1 and CPO is older, it should require the RBAC policy name", origin: "5.1.0", desired: "4.22.0", wantRBAC: true},
		{name: "When CPO is 5.1 and origin is older, it should require only the original five", origin: "4.22.0", desired: "5.1.0"},
		{name: "When proven CP phase selects latest, it should use the latest independent gate", origin: "4.22.0", latest: "5.1.0", cpLatest: true, wantRBAC: true},
		{name: "When AutoNode target uses latest, it should retain the previous independent gate", origin: "4.22.0", latest: "5.1.0", target: true},
		{name: "When origin metadata is unresolved, it should error instead of using CPO or observed version", desired: "5.1.0", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			tc, _ := securityAdmissionFixture(t, testCase.origin, testCase.desired, "5.1.0")
			if testCase.latest != "" {
				t.Setenv("E2E_LATEST_RELEASE_IMAGE", "latest")
				tc.ConditionDiagnosticReleaseVersions["latest"] = semver.MustParse(testCase.latest)
			}
			if testCase.cpLatest {
				t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", "latest")
			}
			if testCase.target {
				t.Setenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET", "latest")
			}
			resetFailureConditionPhase(tc)
			policies, err := securityRequiredAdmissionPolicies(tc)
			if (err != nil) != testCase.wantError {
				t.Fatalf("policies=%v error=%v", policies, err)
			}
			if testCase.wantError {
				return
			}
			want := []string{kasconst.AdmissionPolicyNameConfig, kasconst.AdmissionPolicyNameMirror, kasconst.AdmissionPolicyNameICSP, kasconst.AdmissionPolicyNameInfra, kasconst.AdmissionPolicyNameNTOMirroredConfigs}
			if testCase.wantRBAC {
				want = append(want, kasconst.AdmissionPolicyNameRBAC)
			}
			if !reflect.DeepEqual(policies, want) {
				t.Fatalf("policies=%v, want %v", policies, want)
			}
		})
	}
	t.Run("When actual AWS CPO mapped releases are configured, it should use their independent metadata keys", func(t *testing.T) {
		tc, hc := securityAdmissionFixture(t, "4.17.0", "5.1.0", "5.1.0")
		latest, previous := controlplaneoperatoroverrides.LatestOverrideTestReleases("aws")
		if latest == "" || previous == "" || latest == previous {
			t.Fatal("actual AWS CPO release pair must be distinct")
		}
		t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", previous)
		t.Setenv("E2E_LATEST_RELEASE_IMAGE", latest)
		tc.ConditionDiagnosticReleaseVersions = map[string]semver.Version{previous: semver.MustParse("4.17.0"), latest: semver.MustParse("4.17.0")}
		resetFailureConditionPhase(tc)
		policies, err := securityRequiredAdmissionPolicies(tc)
		if err != nil || len(policies) != 5 {
			t.Fatalf("policies=%v error=%v", policies, err)
		}
		eligible, err := securityAdmissionAtLeast(tc, hc, semver.MustParse("5.1.0"))
		if err != nil || !eligible {
			t.Fatalf("CPO eligibility=%v error=%v", eligible, err)
		}
	})
}

func securityAdmissionFixture(t *testing.T, origin, desired, observed string) (*internal.TestContext, *hyperv1.HostedCluster) {
	t.Helper()
	t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", "previous")
	t.Setenv("E2E_LATEST_RELEASE_IMAGE", "")
	t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", "")
	t.Setenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET", "")
	hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}, Status: hyperv1.HostedClusterStatus{Version: &hyperv1.ClusterVersionStatus{Desired: configv1.Release{Version: desired}, History: []configv1.UpdateHistory{{Version: observed}}}}}
	tc := &internal.TestContext{Context: t.Context(), MgmtClient: fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(hc).Build(), ClusterName: hc.Name, ClusterNamespace: hc.Namespace, ConditionDiagnosticReleaseVersions: map[string]semver.Version{}, ConditionDiagnosticReleaseErrors: map[string]error{"previous": errors.New("independent metadata unresolved")}}
	if origin != "" {
		tc.ConditionDiagnosticReleaseVersions["previous"] = semver.MustParse(origin)
	}
	resetFailureConditionPhase(tc)
	return tc, hc
}

func TestSecurityAdmissionImpersonation(t *testing.T) {
	t.Run("When constructing the denial client, it should authorize a nonwhitelisted identity without mutating the source config", func(t *testing.T) {
		original := &rest.Config{Host: "https://api.hypershift.example.com", Impersonate: rest.ImpersonationConfig{UserName: "system:admin"}}
		actual := securityAdmissionImpersonation(original)
		if actual.Host != original.Host || actual.Impersonate.UserName != "hypershift-e2e-rbac-vap-test" || !reflect.DeepEqual(actual.Impersonate.Groups, []string{"system:masters"}) || original.Impersonate.UserName != "system:admin" {
			t.Fatalf("actual=%+v original=%+v", actual.Impersonate, original.Impersonate)
		}
	})
}

func TestSecurityAdmissionRejection(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "When VAP rejects the request, it should accept the specific denial", err: errors.New("ValidatingAdmissionPolicy denied request"), want: true},
		{name: "When RBAC rejects the request, it should reject the unrelated error", err: errors.New("forbidden: user cannot delete resource")},
		{name: "When transport fails, it should reject the unrelated error", err: errors.New("connection refused")},
		{name: "When the request succeeds, it should fail the denial assertion"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := securityAdmissionRejection(testCase.err); (err == nil) != testCase.want {
				t.Fatalf("assertion error=%v", err)
			}
		})
	}
}

func TestSecurityDeniedAuditChange(t *testing.T) {
	for _, profile := range []configv1.AuditProfileType{configv1.DefaultAuditProfileType, configv1.AllRequestBodiesAuditProfileType} {
		t.Run(fmt.Sprintf("When audit profile is %s, it should toggle a dry run Update and retain the original", profile), func(t *testing.T) {
			original := &configv1.APIServer{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}, Spec: configv1.APIServerSpec{Audit: configv1.Audit{Profile: profile}}}
			updates := 0
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithInterceptorFuncs(interceptor.Funcs{Update: func(ctx context.Context, client crclient.WithWatch, object crclient.Object, opts ...crclient.UpdateOption) error {
				updates++
				options := (&crclient.UpdateOptions{}).ApplyOptions(opts)
				updated := object.(*configv1.APIServer)
				if ctx != t.Context() || !reflect.DeepEqual(options.DryRun, []string{metav1.DryRunAll}) || updated.Name != "cluster" || updated.Spec.Audit.Profile == profile {
					t.Fatalf("update=%+v options=%+v", updated, options)
				}
				return errors.New("ValidatingAdmissionPolicy denied audit mutation")
			}}).Build()
			if err := securityDeniedAuditChange(t.Context(), client, original); err != nil || updates != 1 || original.Spec.Audit.Profile != profile {
				t.Fatalf("error=%v updates=%d original=%s", err, updates, original.Spec.Audit.Profile)
			}
		})
	}
}

func TestSecurityDeniedBindingDeletions(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		deleteErr  error
		missing    string
		wantError  bool
		wantDelete int
	}{
		{name: "When VAP protects both bindings, it should assert both dry run deletions", deleteErr: errors.New("ValidatingAdmissionPolicy denied deletion"), wantDelete: 2},
		{name: "When authorization fails, it should not mistake it for admission rejection", deleteErr: errors.New("forbidden: RBAC"), wantError: true, wantDelete: 1},
		{name: "When admission allows deletion, it should fail without deleting a real binding", wantError: true, wantDelete: 1},
		{name: "When the first protected binding is absent, it should fail instead of skipping", missing: "hcco-cluster-admin", wantError: true},
		{name: "When the second protected binding is absent, it should fail after checking the first", missing: "kas-bootstrap-container-cluster-admin", deleteErr: errors.New("ValidatingAdmissionPolicy denied deletion"), wantError: true, wantDelete: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(hyperapi.Scheme)
			names := []string{"hcco-cluster-admin", "kas-bootstrap-container-cluster-admin"}
			for _, name := range names {
				if name != testCase.missing {
					builder.WithObjects(&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name}})
				}
			}
			var deleted []string
			client := builder.WithInterceptorFuncs(interceptor.Funcs{Delete: func(ctx context.Context, client crclient.WithWatch, object crclient.Object, opts ...crclient.DeleteOption) error {
				options := (&crclient.DeleteOptions{}).ApplyOptions(opts)
				if ctx != t.Context() || !reflect.DeepEqual(options.DryRun, []string{metav1.DryRunAll}) {
					t.Fatalf("delete options=%+v", options)
				}
				deleted = append(deleted, object.GetName())
				return testCase.deleteErr
			}}).Build()
			err := securityDeniedBindingDeletions(t.Context(), client)
			if (err != nil) != testCase.wantError || len(deleted) != testCase.wantDelete {
				t.Fatalf("error=%v deletes=%v", err, deleted)
			}
			for index, name := range deleted {
				if name != names[index] {
					t.Fatalf("unexpected binding %s", name)
				}
				if err := client.Get(t.Context(), crclient.ObjectKey{Name: name}, &rbacv1.ClusterRoleBinding{}); err != nil {
					t.Fatalf("dry run lost binding: %v", err)
				}
			}
		})
	}
}

func TestSecuritySetNetworkMTU(t *testing.T) {
	t.Run("When an allowed Network request changes MTU, it should use ordinary Update and restore with the same native path", func(t *testing.T) {
		var values []int
		client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(&configv1.Network{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}, Status: configv1.NetworkStatus{ClusterNetworkMTU: 9180}}).WithInterceptorFuncs(interceptor.Funcs{Update: func(ctx context.Context, client crclient.WithWatch, object crclient.Object, opts ...crclient.UpdateOption) error {
			if ctx != t.Context() || len(opts) != 0 {
				t.Fatal("expected ordinary non-dry-run Update")
			}
			values = append(values, object.(*configv1.Network).Status.ClusterNetworkMTU)
			return nil
		}}).Build()
		if err := securitySetNetworkMTU(t.Context(), client, 9181); err != nil {
			t.Fatal(err)
		}
		if err := securitySetNetworkMTU(t.Context(), client, 9180); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(values, []int{9181, 9180}) {
			t.Fatalf("updates=%v", values)
		}
	})
	t.Run("When Network is absent, it should return the read error", func(t *testing.T) {
		if err := securitySetNetworkMTU(t.Context(), fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build(), 9180); err == nil {
			t.Fatal("expected missing Network error")
		}
	})
}

func TestSecuritySetOperatorHubSources(t *testing.T) {
	for _, original := range []bool{true, false} {
		t.Run(fmt.Sprintf("When OperatorHub disabled is %v, it should toggle ordinary Update and restore", original), func(t *testing.T) {
			var values []bool
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(&configv1.OperatorHub{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}, Spec: configv1.OperatorHubSpec{DisableAllDefaultSources: original}}).WithInterceptorFuncs(interceptor.Funcs{Update: func(ctx context.Context, client crclient.WithWatch, object crclient.Object, opts ...crclient.UpdateOption) error {
				if ctx != t.Context() || len(opts) != 0 {
					t.Fatal("expected ordinary non-dry-run Update")
				}
				values = append(values, object.(*configv1.OperatorHub).Spec.DisableAllDefaultSources)
				return client.Update(ctx, object, opts...)
			}}).Build()
			for _, disabled := range []bool{!original, original} {
				if err := securitySetOperatorHubSources(t.Context(), client, disabled); err != nil {
					t.Fatal(err)
				}
			}
			actual := &configv1.OperatorHub{}
			if err := client.Get(t.Context(), crclient.ObjectKey{Name: "cluster"}, actual); err != nil || actual.Spec.DisableAllDefaultSources != original || !reflect.DeepEqual(values, []bool{!original, original}) {
				t.Fatalf("restored=%+v updates=%v error=%v", actual.Spec, values, err)
			}
		})
	}
	t.Run("When OperatorHub is absent, it should return the read error", func(t *testing.T) {
		if err := securitySetOperatorHubSources(t.Context(), fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build(), true); err == nil {
			t.Fatal("expected missing OperatorHub error")
		}
	})
}

func TestSecurityCurlConnectionDenied(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		err    error
		denied bool
	}{
		{name: "When curl times out, it should prove denied connectivity", err: utilexec.CodeExitError{Err: errors.New("timeout"), Code: 28}, denied: true},
		{name: "When curl cannot connect, it should prove denied connectivity", err: fmt.Errorf("wrapped: %w", utilexec.CodeExitError{Err: errors.New("refused"), Code: 7}), denied: true},
		{name: "When curl is missing, it should not prove denied connectivity", err: utilexec.CodeExitError{Err: errors.New("not found"), Code: 127}},
		{name: "When exec transport fails, it should not prove denied connectivity", err: errors.New("unable to upgrade connection")},
		{name: "When TLS fails, it should not prove denied connectivity", err: utilexec.CodeExitError{Err: errors.New("TLS failure"), Code: 35}},
		{name: "When curl succeeds, it should not prove denied connectivity"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if actual := securityCurlConnectionDenied(testCase.err); actual != testCase.denied {
				t.Fatalf("securityCurlConnectionDenied() = %v, want %v", actual, testCase.denied)
			}
		})
	}
}

func TestSecurityPodComponent(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		labels   map[string]string
		expected string
	}{
		{name: "When app and name exist, it should prefer app", labels: map[string]string{"app": "etcd", "name": "other"}, expected: "etcd"},
		{name: "When only name exists, it should return name", labels: map[string]string{"name": "cluster-api"}, expected: "cluster-api"},
		{name: "When profiling job exists, it should normalize the suffix", labels: map[string]string{"job-name": "olm-collect-profiles-123"}, expected: "olm-collect-profiles"},
		{name: "When featuregate job exists, it should recognize it", labels: map[string]string{"job-name": "featuregate-generator"}, expected: "featuregate-generator"},
		{name: "When no known labels exist, it should return empty"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if actual := securityPodComponent(corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: testCase.labels}}); actual != testCase.expected {
				t.Fatalf("securityPodComponent() = %q, want %q", actual, testCase.expected)
			}
		})
	}
}

func TestSecurityManagementKASComponents(t *testing.T) {
	t.Run("When checking privileged components, it should retain the baseline allowlist without request serving components", func(t *testing.T) {
		allowed := securityManagementKASComponents()
		if allowed.Len() != 21 || !allowed.HasAll("endpoint-resolver", "etcd", "featuregate-generator", "karpenter", "control-plane-operator") || allowed.HasAny("kube-apiserver", "private-router", "cluster-version-operator") {
			t.Fatalf("unexpected management KAS allowlist: %v", allowed.UnsortedList())
		}
	})
}
