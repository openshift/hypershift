package e2e

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var upgradeOSImagePattern = regexp.MustCompile(`^Red Hat Enterprise Linux CoreOS (9|10|4[0-9]{2}|5[0-9]{2})\.[0-9]`)

func nodePoolUpgradeReadinessTimeout(platform hyperv1.PlatformType) time.Duration {
	if platform == hyperv1.PowerVSPlatform {
		return 60 * time.Minute
	}
	return 45 * time.Minute
}

func waitForUpgradeOSImageStream(t testing.TB, ctx context.Context, mgmtClient, guestClient crclient.Client, key crclient.ObjectKey, desiredStream string, options ...e2eutil.EventuallyOption) (*hyperv1.NodePool, []corev1.Node) {
	t.Helper()
	pool := &hyperv1.NodePool{}
	guestNodes := &corev1.NodeList{}
	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("NodePool %s OS image stream to agree with all ready guest nodes (desired %q)", key, desiredStream),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			if err := mgmtClient.Get(ctx, key, pool); err != nil {
				return nil, err
			}
			if err := guestClient.List(ctx, guestNodes, crclient.MatchingLabels{hyperv1.NodePoolLabel: key.Name}); err != nil {
				return nil, err
			}
			return pool, nil
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			func(pool *hyperv1.NodePool) (bool, string, error) {
				validationErr := validateUpgradeOSImageStream(pool, guestNodes.Items, desiredStream)
				reason := "OS image stream matches all ready guest nodes"
				if validationErr != nil {
					reason = validationErr.Error()
				}
				return validationErr == nil, reason, nil
			},
		},
		options...,
	)
	return pool, guestNodes.Items
}

func validateUpgradeOSImageStream(pool *hyperv1.NodePool, nodes []corev1.Node, desiredStream string) error {
	if pool.Spec.Replicas == nil || *pool.Spec.Replicas <= 0 {
		return fmt.Errorf("expected a positive NodePool replica count")
	}
	if len(nodes) != int(*pool.Spec.Replicas) {
		return fmt.Errorf("expected %d guest nodes, got %d", *pool.Spec.Replicas, len(nodes))
	}
	observedStream := ""
	for _, node := range nodes {
		ready := false
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady {
				ready = condition.Status == corev1.ConditionTrue
			}
		}
		if !ready || !node.DeletionTimestamp.IsZero() {
			return fmt.Errorf("guest node %s is not ready or is terminating", node.Name)
		}
		matches := upgradeOSImagePattern.FindStringSubmatch(node.Status.NodeInfo.OSImage)
		if len(matches) != 2 {
			return fmt.Errorf("guest node %s has unrecognized OSImage %q", node.Name, node.Status.NodeInfo.OSImage)
		}
		stream := string(hyperv1.OSImageStreamRHEL9)
		if matches[1] == "10" || strings.HasPrefix(matches[1], "5") {
			stream = string(hyperv1.OSImageStreamRHEL10)
		}
		if observedStream != "" && stream != observedStream {
			return fmt.Errorf("guest nodes have mixed OS image streams: %s and %s", observedStream, stream)
		}
		observedStream = stream
	}
	if requested := pool.Spec.OSImageStream.Name; requested != "" && requested != observedStream {
		return fmt.Errorf("guest OS image stream is %s, requested %s", observedStream, requested)
	}
	if desiredStream != "" && desiredStream != observedStream {
		return fmt.Errorf("guest OS image stream is %s, want %s", observedStream, desiredStream)
	}
	if pool.Status.OSImageStream.Name != observedStream {
		return fmt.Errorf("status.osImageStream.name is %q, guest nodes report %s", pool.Status.OSImageStream.Name, observedStream)
	}
	return nil
}

func TestValidateUpgradeOSImageStream(t *testing.T) {
	rhel9 := "Red Hat Enterprise Linux CoreOS 9.8.20260808-0 (Plow)"
	rhel10 := "Red Hat Enterprise Linux CoreOS 10.2.20260924-0 (Coughlan)"
	for _, testCase := range []struct {
		name          string
		images        []string
		requested     string
		status        string
		desired       string
		replicas      *int32
		notReady      bool
		missingReady  bool
		terminating   bool
		expectedError string
	}{
		{name: "When historical nodes run RHEL9, it should validate their observed stream", images: []string{rhel9}, status: "rhel-9", replicas: ptr.To[int32](1)},
		{name: "When nodes run RHEL10, it should validate their observed stream", images: []string{rhel10}, status: "rhel-10", replicas: ptr.To[int32](1)},
		{name: "When legacy nodes run RHEL9, it should recognize the legacy format", images: []string{"Red Hat Enterprise Linux CoreOS 419.94.202503061027-0 (Plow)"}, status: "rhel-9", replicas: ptr.To[int32](1)},
		{name: "When legacy nodes run RHEL10, it should recognize the legacy format", images: []string{"Red Hat Enterprise Linux CoreOS 500.10.20260301-0 (Coughlan)"}, status: "rhel-10", replicas: ptr.To[int32](1)},
		{name: "When all upgraded nodes run the desired stream, it should succeed", images: []string{rhel10, rhel10}, status: "rhel-10", desired: "rhel-10", replicas: ptr.To[int32](2)},
		{name: "When an upgraded node remains on RHEL9, it should reject matching RHEL9 status", images: []string{rhel9}, status: "rhel-9", desired: "rhel-10", replicas: ptr.To[int32](1), expectedError: "want rhel-10"},
		{name: "When upgraded status claims RHEL10 but the node runs RHEL9, it should fail", images: []string{rhel9}, status: "rhel-10", desired: "rhel-10", replicas: ptr.To[int32](1), expectedError: "want rhel-10"},
		{name: "When a minority node runs RHEL9, it should reject majority RHEL10 status", images: []string{rhel10, rhel10, rhel9}, status: "rhel-10", desired: "rhel-10", replicas: ptr.To[int32](3), expectedError: "mixed OS image streams"},
		{name: "When a minority node is not ready, it should reject majority readiness", images: []string{rhel10, rhel10, rhel10}, status: "rhel-10", desired: "rhel-10", replicas: ptr.To[int32](3), notReady: true, expectedError: "guest node worker-2 is not ready"},
		{name: "When a minority node is terminating, it should wait for its replacement", images: []string{rhel10, rhel10, rhel10}, status: "rhel-10", desired: "rhel-10", replicas: ptr.To[int32](3), terminating: true, expectedError: "guest node worker-2 is not ready or is terminating"},
		{name: "When a minority node OS is unknown, it should reject majority RHEL10 status", images: []string{rhel10, rhel10, "Ubuntu 24.04"}, status: "rhel-10", desired: "rhel-10", replicas: ptr.To[int32](3), expectedError: "guest node worker-2 has unrecognized OSImage"},
		{name: "When RHEL9 is explicitly pinned, it should accept RHEL9 after upgrade", images: []string{rhel9}, requested: "rhel-9", status: "rhel-9", desired: "rhel-9", replicas: ptr.To[int32](1)},
		{name: "When an initial node violates an explicit RHEL10 pin, it should fail", images: []string{rhel9}, requested: "rhel-10", status: "rhel-9", replicas: ptr.To[int32](1), expectedError: "requested rhel-10"},
		{name: "When a node violates an explicit RHEL9 pin, it should fail", images: []string{rhel10}, requested: "rhel-9", status: "rhel-10", replicas: ptr.To[int32](1), expectedError: "requested rhel-9"},
		{name: "When stream status is stale, it should fail until status converges", images: []string{rhel10}, status: "rhel-9", replicas: ptr.To[int32](1), expectedError: "status.osImageStream.name"},
		{name: "When stream status is empty, it should fail until status is populated", images: []string{rhel9}, replicas: ptr.To[int32](1), expectedError: "status.osImageStream.name"},
		{name: "When a node OS is unknown, it should not trust stream status", images: []string{"Ubuntu 24.04"}, status: "rhel-10", replicas: ptr.To[int32](1), expectedError: "unrecognized OSImage"},
		{name: "When a node OS is empty, it should fail", images: []string{""}, status: "rhel-10", replicas: ptr.To[int32](1), expectedError: "unrecognized OSImage"},
		{name: "When a node OS generation is unsupported, it should fail", images: []string{"Red Hat Enterprise Linux CoreOS 11.0.20270101-0"}, status: "rhel-10", replicas: ptr.To[int32](1), expectedError: "unrecognized OSImage"},
		{name: "When no nodes exist, it should fail", replicas: ptr.To[int32](1), expectedError: "expected 1 guest nodes, got 0"},
		{name: "When too many nodes exist, it should wait for rollout completion", images: []string{rhel10, rhel10}, replicas: ptr.To[int32](1), expectedError: "expected 1 guest nodes, got 2"},
		{name: "When replicas are unset, it should fail", expectedError: "positive NodePool replica count"},
		{name: "When replicas are zero, it should fail", replicas: ptr.To[int32](0), expectedError: "positive NodePool replica count"},
		{name: "When a node is not ready, it should fail", images: []string{rhel9}, status: "rhel-9", replicas: ptr.To[int32](1), notReady: true, expectedError: "not ready"},
		{name: "When node readiness is unknown, it should fail", images: []string{rhel9}, status: "rhel-9", replicas: ptr.To[int32](1), missingReady: true, expectedError: "not ready"},
		{name: "When a ready node is terminating, it should fail", images: []string{rhel9}, status: "rhel-9", replicas: ptr.To[int32](1), terminating: true, expectedError: "terminating"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			pool := &hyperv1.NodePool{}
			pool.Spec.Replicas = testCase.replicas
			pool.Spec.OSImageStream.Name = testCase.requested
			pool.Status.OSImageStream.Name = testCase.status
			var nodes []corev1.Node
			for index, image := range testCase.images {
				node := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("worker-%d", index)}}
				node.Status.NodeInfo.OSImage = image
				if !testCase.missingReady {
					ready := corev1.ConditionTrue
					if testCase.notReady && index == len(testCase.images)-1 {
						ready = corev1.ConditionFalse
					}
					node.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}}
				}
				if testCase.terminating && index == len(testCase.images)-1 {
					node.DeletionTimestamp = ptr.To(metav1.Now())
				}
				nodes = append(nodes, node)
			}
			err := validateUpgradeOSImageStream(pool, nodes, testCase.desired)
			if testCase.expectedError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), testCase.expectedError) {
				t.Fatalf("expected error containing %q, got %v", testCase.expectedError, err)
			}
		})
	}
}

func TestNodePoolUpgradeReadinessTimeout(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		platform hyperv1.PlatformType
		want     time.Duration
	}{
		{name: "When the platform is unspecified, it should allow 45 minutes", want: 45 * time.Minute},
		{name: "When the platform is AWS, it should allow 45 minutes", platform: hyperv1.AWSPlatform, want: 45 * time.Minute},
		{name: "When the platform is Azure, it should allow 45 minutes", platform: hyperv1.AzurePlatform, want: 45 * time.Minute},
		{name: "When the platform is KubeVirt, it should allow 45 minutes", platform: hyperv1.KubevirtPlatform, want: 45 * time.Minute},
		{name: "When the platform is PowerVS, it should allow 60 minutes", platform: hyperv1.PowerVSPlatform, want: 60 * time.Minute},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := nodePoolUpgradeReadinessTimeout(testCase.platform); got != testCase.want {
				t.Fatalf("expected readiness timeout %s, got %s", testCase.want, got)
			}
		})
	}
}

func TestWaitForUpgradeOSImageStream(t *testing.T) {
	t.Run("When API observations converge, it should retry and return fresh pool and guest nodes", func(t *testing.T) {
		key := crclient.ObjectKey{Namespace: "clusters", Name: "workers"}
		getCalls, listCalls := 0, 0
		mgmtClient := interceptor.NewClient(nil, interceptor.Funcs{
			Get: func(ctx context.Context, client crclient.WithWatch, actualKey crclient.ObjectKey, obj crclient.Object, opts ...crclient.GetOption) error {
				if actualKey != key {
					t.Fatalf("expected NodePool key %s, got %s", key, actualKey)
				}
				getCalls++
				if getCalls == 1 {
					return fmt.Errorf("temporary NodePool get failure")
				}
				pool := obj.(*hyperv1.NodePool)
				*pool = hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name, ResourceVersion: fmt.Sprint(getCalls)}}
				pool.Spec.Replicas = ptr.To[int32](3)
				pool.Status.OSImageStream.Name = "rhel-10"
				if getCalls == 8 {
					pool.Status.OSImageStream.Name = "rhel-9"
				}
				return nil
			},
		})
		guestClient := interceptor.NewClient(nil, interceptor.Funcs{
			List: func(ctx context.Context, client crclient.WithWatch, list crclient.ObjectList, opts ...crclient.ListOption) error {
				listOptions := (&crclient.ListOptions{}).ApplyOptions(opts)
				if listOptions.LabelSelector == nil || listOptions.LabelSelector.String() != hyperv1.NodePoolLabel+"="+key.Name {
					t.Fatalf("expected NodePool label selector, got %v", listOptions.LabelSelector)
				}
				listCalls++
				if listCalls == 1 {
					return fmt.Errorf("temporary guest list failure")
				}
				nodes := list.(*corev1.NodeList)
				nodes.Items = nil
				if listCalls == 2 {
					return nil
				}
				for index := range 3 {
					nodes.Items = append(nodes.Items, corev1.Node{
						ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("worker-%d-observation-%d", index, listCalls)},
						Status: corev1.NodeStatus{
							NodeInfo:   corev1.NodeSystemInfo{OSImage: "Red Hat Enterprise Linux CoreOS 10.2.20260924-0 (Coughlan)"},
							Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
						},
					})
				}
				switch listCalls {
				case 3:
					nodes.Items[2].Status.Conditions[0].Status = corev1.ConditionFalse
				case 4:
					nodes.Items[2].DeletionTimestamp = ptr.To(metav1.Now())
				case 5:
					nodes.Items[2].Status.NodeInfo.OSImage = ""
				case 6:
					nodes.Items[2].Status.NodeInfo.OSImage = "Red Hat Enterprise Linux CoreOS 9.8.20260808-0 (Plow)"
				}
				return nil
			},
		})
		pool, nodes := waitForUpgradeOSImageStream(t, context.Background(), mgmtClient, guestClient, key, "rhel-10", e2eutil.WithInterval(time.Millisecond), e2eutil.WithTimeout(5*time.Second))
		if getCalls != 9 || listCalls != 8 {
			t.Fatalf("expected 9 NodePool gets and 8 guest lists, got %d and %d", getCalls, listCalls)
		}
		if pool.ResourceVersion != "9" || len(nodes) != 3 || nodes[2].Name != "worker-2-observation-8" {
			t.Fatalf("expected final pool and nodes, got pool %s and nodes %+v", pool.ResourceVersion, nodes)
		}
		if err := validateUpgradeOSImageStream(pool, nodes, "rhel-10"); err != nil {
			t.Fatal(err)
		}
	})
}
