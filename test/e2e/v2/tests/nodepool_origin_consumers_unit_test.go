//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/conditions"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/blang/semver"
	"github.com/onsi/gomega"
)

func TestExpectAWSNodePoolSteadyState(t *testing.T) {
	originalGomega := gomega.Default
	gomega.Default = gomega.NewGomega(nil)
	gomega.RegisterFailHandler(func(message string, _ ...int) { t.Fatal(message) })
	t.Cleanup(func() { gomega.Default = originalGomega })
	for _, test := range []struct {
		name, origin, observed, badField, wantError string
		resolverError                               bool
	}{
		{name: "When new origin contradicts older history, it should reject the actual incorrect aggregation reason", origin: "4.23.0", observed: "4.17.0", badField: "reason", wantError: "incorrect reason"},
		{name: "When new origin contradicts older history, it should reject the actual incorrect aggregation message", origin: "4.23.0", observed: "4.17.0", badField: "message", wantError: "incorrect message"},
		{name: "When old origin contradicts newer history, it should preserve the actual older aggregation contract", origin: "4.22.0", observed: "4.23.0", badField: "reason"},
		{name: "When new origin and aggregation fields are correct, it should accept actual pool conditions", origin: "4.23.0", observed: "4.17.0"},
		{name: "When origin resolution fails, it should fail before any pool observation", observed: "4.23.0", resolverError: true, wantError: "origin metadata unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tc := nodePoolOriginConsumerContext(t, test.origin, test.observed, test.resolverError)
			ctx, cancel := context.WithCancel(tc.Context)
			defer cancel()
			tc.Context = ctx
			pool := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "pool", Namespace: "clusters"}}
			for conditionType, status := range conditions.ExpectedNodePoolConditions(pool) {
				condition := hyperv1.NodePoolCondition{Type: conditionType, Status: status, Reason: hyperv1.AsExpectedReason, Message: hyperv1.AllIsWellMessage}
				if conditionType == hyperv1.NodePoolAllMachinesReadyConditionType {
					switch test.badField {
					case "reason":
						condition.Reason = "incorrect reason"
					case "message":
						condition.Message = "incorrect message"
					}
				}
				pool.Status.Conditions = append(pool.Status.Conditions, condition)
			}
			reads := 0
			tc.MgmtClient = fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(pool).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, options ...crclient.GetOption) error {
					if _, isPool := object.(*hyperv1.NodePool); isPool {
						reads++
						if test.wantError != "" {
							defer cancel()
						}
					}
					return client.Get(ctx, key, object, options...)
				},
			}).Build()
			err := gomega.InterceptGomegaFailure(func() { expectAWSNodePoolSteadyState(tc, pool) })
			if test.wantError == "" && err != nil || test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("actual aggregation consumer failure = %v, want %q", err, test.wantError)
			}
			if test.resolverError && reads != 0 || !test.resolverError && reads == 0 {
				t.Fatalf("unexpected pool observations: %d", reads)
			}
		})
	}
}

func TestExpectAWSNodePoolRuntime(t *testing.T) {
	originalGomega := gomega.Default
	gomega.Default = gomega.NewGomega(nil)
	gomega.RegisterFailHandler(func(message string, _ ...int) { t.Fatal(message) })
	t.Cleanup(func() { gomega.Default = originalGomega })
	for _, test := range []struct {
		name, origin, observed, stream, wantError string
		handlers                                  []corev1.NodeRuntimeHandler
		resolverError                             bool
	}{
		{name: "When new origin contradicts older history, it should reject actual missing runtime handlers", origin: "4.18.0", observed: "4.17.0", wantError: "missing runtime handlers"},
		{name: "When old origin contradicts newer history, it should retain old runtime compatibility", origin: "4.17.0", observed: "4.23.0"},
		{name: "When origin resolution fails, it should fail before inspecting runtime status", observed: "4.23.0", resolverError: true, wantError: "origin metadata unavailable"},
		{name: "When RHEL9 lacks runc, it should reject actual runtime status", origin: "4.18.0", observed: "4.17.0", handlers: []corev1.NodeRuntimeHandler{{Name: "crun"}}, wantError: "missing runtime handler runc"},
		{name: "When RHEL9 has both runtime handlers, it should accept actual runtime status", origin: "4.18.0", observed: "4.17.0", handlers: []corev1.NodeRuntimeHandler{{Name: "crun"}, {Name: "runc"}}},
		{name: "When status selects RHEL10, it should retain stream authority separate from origin", origin: "4.18.0", observed: "4.17.0", stream: hyperv1.OSImageStreamRHEL10, handlers: []corev1.NodeRuntimeHandler{{Name: "crun"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tc := nodePoolOriginConsumerContext(t, test.origin, test.observed, test.resolverError)
			pool := &hyperv1.NodePool{Status: hyperv1.NodePoolStatus{Version: "4.17.0"}}
			pool.Status.OSImageStream.Name = test.stream
			nodes := []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "worker"}, Status: corev1.NodeStatus{RuntimeHandlers: test.handlers}}}
			err := gomega.InterceptGomegaFailure(func() { expectAWSNodePoolRuntime(tc, pool, nodes) })
			if test.wantError == "" && err != nil || test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("actual runtime consumer failure = %v, want %q", err, test.wantError)
			}
		})
	}
}

func nodePoolOriginConsumerContext(t *testing.T, origin, observed string, resolverError bool) *internal.TestContext {
	t.Helper()
	for _, name := range []string{"E2E_PREVIOUS_RELEASE_IMAGE", "E2E_LATEST_RELEASE_IMAGE", "E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", "E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET"} {
		t.Setenv(name, "")
	}
	image := "quay.io/openshift-release-dev/ocp-release:4.23.0-x86_64"
	t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", image)
	cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}, Status: hyperv1.HostedClusterStatus{Version: &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{Version: observed}}}}}
	tc := &internal.TestContext{
		Context: t.Context(), MgmtClient: fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(cluster).Build(), ClusterName: cluster.Name, ClusterNamespace: cluster.Namespace,
		ConditionDiagnosticReleaseVersions: map[string]semver.Version{}, ConditionDiagnosticReleaseErrors: map[string]error{},
	}
	if resolverError {
		tc.ConditionDiagnosticReleaseErrors[image] = errors.New("origin metadata unavailable")
	} else {
		tc.ConditionDiagnosticReleaseVersions[image] = semver.MustParse(origin)
	}
	resetFailureConditionPhase(tc)
	return tc
}
