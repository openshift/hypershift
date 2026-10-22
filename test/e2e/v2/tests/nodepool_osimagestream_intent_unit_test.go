//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
	"github.com/onsi/gomega"
)

func TestNodePoolOSImageStreamDefaultStatusBody(t *testing.T) {
	const childCaseEnv = "HYPERSHIFT_OSSTREAM_INTENT_UNIT_CASE"
	for index, testCase := range []struct {
		name      string
		intent    map[string]int32
		missing   bool
		wantSkip  bool
		wantError string
	}{
		{name: "When a fixture explicitly configures zero workers, it should skip before observing pools", intent: map[string]int32{"pool": 0}, wantSkip: true},
		{name: "When all configured pools are zero, it should skip", intent: map[string]int32{"pool": 0, "another": 0}, wantSkip: true},
		{name: "When an explicit fixture has no initial pools, it should skip", intent: map[string]int32{}, wantSkip: true},
		{name: "When positive intent has zero ready replicas, it should fail its readiness assertion", intent: map[string]int32{"pool": 1}, wantError: "nodesInfo"},
		{name: "When worker intent is unknown and replicas are zero, it should fail its readiness assertion", wantError: "nodesInfo"},
		{name: "When any configured pool needs workers, it should not skip", intent: map[string]int32{"pool": 0, "another": 1}, wantError: "nodesInfo"},
		{name: "When positive intent has a missing pool, it should fail", intent: map[string]int32{"pool": 1}, missing: true, wantError: "template NodePool"},
		{name: "When unknown intent has a missing pool, it should fail", missing: true, wantError: "template NodePool"},
		{name: "When configured intent is invalid, it should fail instead of skipping", intent: map[string]int32{"pool": -1}, wantError: "nonnegative"},
	} {
		if os.Getenv(childCaseEnv) == "" {
			t.Run(testCase.name, func(t *testing.T) {
				command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestNodePoolOSImageStreamDefaultStatusBody$", "-test.v")
				command.Env = append(os.Environ(), childCaseEnv+"="+strconv.Itoa(index))
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("actual status consumer error=%v:\n%s", err, output)
				}
			})
			continue
		}
		if os.Getenv(childCaseEnv) != strconv.Itoa(index) {
			continue
		}
		gomega.Default = gomega.NewWithT(t)
		scheme := runtime.NewScheme()
		if err := hyperv1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}, Status: hyperv1.HostedClusterStatus{Version: &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{Version: "5.0.0"}}}}}
		builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc)
		if !testCase.missing {
			builder.WithObjects(&hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "pool", Namespace: hc.Namespace}, Spec: hyperv1.NodePoolSpec{ClusterName: hc.Name, Replicas: ptr.To[int32](1)}})
		}
		lists := 0
		client := builder.WithInterceptorFuncs(interceptor.Funcs{List: func(ctx context.Context, client crclient.WithWatch, list crclient.ObjectList, options ...crclient.ListOption) error {
			lists++
			return client.List(ctx, list, options...)
		}}).Build()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		tc := &internal.TestContext{Context: ctx, MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace, ExpectedNodePools: testCase.intent}
		skipped := false
		failure := func() (failure error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					ginkgoError, ok := recovered.(types.GinkgoError)
					var expected types.GinkgoError
					if !ok || !errors.As(types.GinkgoErrors.UncaughtGinkgoPanic(types.CodeLocation{}), &expected) || ginkgoError.Heading != expected.Heading {
						t.Fatalf("unexpected consumer panic: %v", recovered)
					}
					skipped = true
				}
			}()
			return gomega.InterceptGomegaFailure(nodePoolOSImageStreamDefaultStatusBody(func() *internal.TestContext { return tc }))
		}()
		if skipped != testCase.wantSkip || testCase.wantSkip && lists != 0 || (failure != nil) != (testCase.wantError != "") || testCase.wantError != "" && !strings.Contains(failure.Error(), testCase.wantError) {
			t.Fatalf("skip=%v lists=%d failure=%v, want skip=%v error=%q", skipped, lists, failure, testCase.wantSkip, testCase.wantError)
		}
		return
	}
}

func TestNodePoolOSImageStreamDefaultStatusTest(t *testing.T) {
	selected := 0
	for _, spec := range ginkgo.PreviewSpecs("OSImageStream status registration regression").SpecReports {
		if spec.LeafNodeText == "When no osImageStream is set, it should report a recognized RHEL stream in status" {
			selected++
			selected, err := spec.MatchesLabelFilter("nodepool-osimagestream && !lifecycle")
			if err != nil || !selected {
				t.Fatalf("default status registration lost read-only selection: %v", spec.Labels())
			}
		}
	}
	if selected != 1 {
		t.Fatalf("registered %d status bodies, want exactly one", selected)
	}
}
