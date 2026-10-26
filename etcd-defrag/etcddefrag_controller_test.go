package etcddefrag

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openshift/hypershift/pkg/etcdcli"

	"github.com/openshift/library-go/pkg/operator/events"

	"k8s.io/utils/clock"

	"github.com/go-logr/logr"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type defragControllerClient struct {
	etcdcli.EtcdClient
	members     []*etcdserverpb.Member
	statuses    map[string]*clientv3.StatusResponse
	defragment  func(context.Context, *etcdserverpb.Member) (*clientv3.DefragmentResponse, error)
	listError   error
	statusError error
}

func (client *defragControllerClient) MemberList(context.Context) ([]*etcdserverpb.Member, error) {
	return client.members, client.listError
}

func (client *defragControllerClient) Status(_ context.Context, endpoint string) (*clientv3.StatusResponse, error) {
	return client.statuses[endpoint], client.statusError
}

func (client *defragControllerClient) Defragment(ctx context.Context, member *etcdserverpb.Member) (*clientv3.DefragmentResponse, error) {
	return client.defragment(ctx, member)
}

type defragCycleTestCase struct {
	name             string
	failMember       uint64
	failure          error
	cancelParent     bool
	empty            bool
	learner          bool
	unhealthy        bool
	healthError      bool
	listError        bool
	statusError      bool
	belowThreshold   bool
	recoveryError    bool
	recoveryRetry    bool
	priorError       bool
	learnersOnly     bool
	scaledBudget     bool
	wantOrder        []uint64
	wantHealthChecks int
	wantFailureCount int
	wantError        bool
}

func TestRunDefrag(t *testing.T) {
	t.Parallel()
	ordinaryError := errors.New("ordinary failure")
	for _, testCase := range []defragCycleTestCase{
		{name: "When healthy members need defrag, it should run sequentially with the leader last and recover after each", wantOrder: []uint64{2, 3, 1}, wantHealthChecks: 4},
		{name: "When the cycle exceeds one RPC budget, it should finish with each request independently bounded", scaledBudget: true, wantOrder: []uint64{2, 3, 1}, wantHealthChecks: 4},
		{name: "When a member is a learner, it should skip its defrag", learner: true, wantOrder: []uint64{2, 3, 1}, wantHealthChecks: 4},
		{name: "When only learners exist, it should complete without defrag", learnersOnly: true, wantHealthChecks: 1},
		{name: "When no members exist, it should complete without defrag", empty: true, wantHealthChecks: 1},
		{name: "When members are below threshold, it should count them as successful without recovery waits", belowThreshold: true, wantHealthChecks: 1},
		{name: "When the first RPC times out, it should stop all remaining members and account for failure", failMember: 2, failure: context.DeadlineExceeded, wantOrder: []uint64{2}, wantHealthChecks: 1, wantFailureCount: 1, wantError: true},
		{name: "When a later RPC times out, it should retain partial success and stop remaining members", failMember: 3, failure: fmt.Errorf("wrapped: %w", context.DeadlineExceeded), wantOrder: []uint64{2, 3}, wantHealthChecks: 2, wantFailureCount: 1, wantError: true},
		{name: "When timeout follows another failure, it should retain both errors and stop remaining members", failMember: 3, failure: context.DeadlineExceeded, priorError: true, wantOrder: []uint64{2, 3}, wantHealthChecks: 1, wantFailureCount: 1, wantError: true},
		{name: "When an RPC is canceled, it should stop all remaining members", failMember: 2, failure: context.Canceled, wantOrder: []uint64{2}, wantHealthChecks: 1, wantFailureCount: 1, wantError: true},
		{name: "When gRPC reports deadline exceeded, it should stop remaining members", failMember: 2, failure: fmt.Errorf("wrapped: %w", status.Error(codes.DeadlineExceeded, "deadline")), wantOrder: []uint64{2}, wantHealthChecks: 1, wantFailureCount: 1, wantError: true},
		{name: "When gRPC reports cancellation, it should stop remaining members", failMember: 2, failure: status.Error(codes.Canceled, "canceled"), wantOrder: []uint64{2}, wantHealthChecks: 1, wantFailureCount: 1, wantError: true},
		{name: "When parent cancellation accompanies another RPC error, it should stop remaining members", failMember: 2, failure: ordinaryError, cancelParent: true, wantOrder: []uint64{2}, wantHealthChecks: 1, wantFailureCount: 1, wantError: true},
		{name: "When an ordinary RPC fails, it should continue and retain failure accounting", failMember: 2, failure: ordinaryError, wantOrder: []uint64{2, 3, 1}, wantHealthChecks: 3, wantFailureCount: 1, wantError: true},
		{name: "When the cluster is unhealthy, it should not start any defrag", unhealthy: true, wantHealthChecks: 1, wantError: true},
		{name: "When health retrieval fails, it should return the error before defrag", healthError: true, wantHealthChecks: 1, wantError: true},
		{name: "When membership retrieval fails, it should return without health checks", listError: true, wantError: true},
		{name: "When status retrieval fails, it should return without defrag", statusError: true, wantHealthChecks: 1, wantError: true},
		{name: "When recovery health fails and its context ends, it should preserve successful defrag accounting", recoveryError: true, wantOrder: []uint64{1}, wantHealthChecks: 2},
		{name: "When recovery health temporarily fails, it should wait for health before the next defrag", recoveryRetry: true, wantOrder: []uint64{2, 3, 1}, wantHealthChecks: 5},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			client := &defragControllerClient{statuses: map[string]*clientv3.StatusResponse{}}
			for memberID := uint64(1); memberID <= 3; memberID++ {
				endpoint := fmt.Sprintf("https://etcd-%d:2379", memberID)
				client.members = append(client.members, &etcdserverpb.Member{ID: memberID, Name: fmt.Sprintf("etcd-%d", memberID), ClientURLs: []string{endpoint}})
				client.statuses[endpoint] = &clientv3.StatusResponse{Header: &etcdserverpb.ResponseHeader{MemberId: memberID}, Leader: 1, DbSize: minDefragBytes, DbSizeInUse: minDefragBytes / 2}
				if testCase.belowThreshold {
					client.statuses[endpoint].DbSizeInUse = minDefragBytes
				}
			}
			if testCase.empty {
				client.members = nil
			}
			if testCase.learnersOnly {
				for _, member := range client.members {
					member.IsLearner = true
				}
			}
			if testCase.recoveryError {
				client.members = client.members[:1]
			}
			if testCase.learner {
				client.members = append(client.members, &etcdserverpb.Member{ID: 4, Name: "learner", IsLearner: true, ClientURLs: []string{"https://learner:2379"}})
				client.statuses["https://learner:2379"] = &clientv3.StatusResponse{Header: &etcdserverpb.ResponseHeader{MemberId: 4}, Leader: 1, DbSize: minDefragBytes, DbSizeInUse: 0}
			}
			if testCase.listError {
				client.listError = ordinaryError
			}
			if testCase.statusError {
				client.statusError = ordinaryError
			}
			var order []uint64
			healthChecks := 0
			recoveryWait := 5 * time.Millisecond
			const requestBudget = 100 * time.Millisecond
			if testCase.scaledBudget {
				recoveryWait = 50 * time.Millisecond
			}
			lastDefrag := time.Time{}
			client.defragment = func(callCtx context.Context, member *etcdserverpb.Member) (*clientv3.DefragmentResponse, error) {
				if callCtx != ctx {
					t.Error("controller replaced the caller context with an aggregate budget")
				}
				order = append(order, member.ID)
				if testCase.scaledBudget {
					rpcCtx, rpcCancel := context.WithTimeout(callCtx, requestBudget)
					defer rpcCancel()
					select {
					case <-time.After(time.Millisecond):
					case <-rpcCtx.Done():
						return nil, rpcCtx.Err()
					}
					if err := rpcCtx.Err(); err != nil {
						return nil, err
					}
				}
				lastDefrag = time.Now()
				if testCase.priorError && member.ID == 2 {
					return nil, ordinaryError
				}
				if member.ID == testCase.failMember {
					if testCase.cancelParent {
						cancel()
					}
					return nil, testCase.failure
				}
				return &clientv3.DefragmentResponse{}, nil
			}
			controller := &DefragController{etcdClient: client, log: logr.Discard(), defragWaitDuration: recoveryWait, clusterHealth: func(context.Context) (bool, string, error) {
				healthChecks++
				return checkDefragTestHealth(t, testCase, healthChecks, lastDefrag, recoveryWait, cancel, ordinaryError)
			}}
			started := time.Now()
			err := controller.runDefrag(ctx)
			assertDefragCycleResult(t, testCase, err, ordinaryError, order, healthChecks, controller.numDefragFailures)
			if !testCase.wantError && len(order) == 3 && time.Since(started) < 3*recoveryWait {
				t.Error("cycle omitted recovery waits")
			}
			if testCase.scaledBudget && time.Since(started) <= requestBudget {
				t.Error("cycle did not exceed a single request budget")
			}
		})
	}
}

func checkDefragTestHealth(t *testing.T, testCase defragCycleTestCase, healthChecks int, lastDefrag time.Time, recoveryWait time.Duration, cancel context.CancelFunc, ordinaryError error) (bool, string, error) {
	t.Helper()
	if healthChecks > 1 && time.Since(lastDefrag) < recoveryWait {
		t.Error("health check skipped recovery wait")
	}
	if testCase.healthError {
		return false, "", ordinaryError
	}
	if testCase.recoveryRetry && healthChecks == 2 {
		return false, "recovering", nil
	}
	if testCase.recoveryError && healthChecks > 1 {
		cancel()
		return false, "", ordinaryError
	}
	return !testCase.unhealthy, "test health", nil
}

func assertDefragCycleResult(t *testing.T, testCase defragCycleTestCase, err, ordinaryError error, order []uint64, healthChecks, failureCount int) {
	t.Helper()
	if (err != nil) != testCase.wantError {
		t.Fatalf("unexpected error: %v", err)
	}
	if testCase.failure != nil && !errors.Is(err, testCase.failure) {
		t.Errorf("lost RPC error: %v", err)
	}
	if testCase.priorError && !errors.Is(err, ordinaryError) {
		t.Error("lost earlier ordinary error")
	}
	if !reflect.DeepEqual(order, testCase.wantOrder) {
		t.Errorf("defrag order %v, want %v", order, testCase.wantOrder)
	}
	if healthChecks != testCase.wantHealthChecks {
		t.Errorf("health checks %d, want %d", healthChecks, testCase.wantHealthChecks)
	}
	if failureCount != testCase.wantFailureCount {
		t.Errorf("failure count %d, want %d", failureCount, testCase.wantFailureCount)
	}
	if testCase.failure != nil && !strings.Contains(err.Error(), fmt.Sprintf("memberID: %x", testCase.failMember)) {
		t.Error("error lost member identity")
	}
}

func TestIsEndpointBackendFragmented(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name    string
		size    int64
		inUse   int64
		missing bool
		want    bool
	}{
		{name: "When status is missing, it should not defrag", missing: true},
		{name: "When size is below minimum, it should not defrag", size: minDefragBytes - 1},
		{name: "When size equals minimum and fragmentation exceeds threshold, it should defrag", size: minDefragBytes, inUse: minDefragBytes / 2, want: true},
		{name: "When fragmentation equals threshold, it should defrag", size: 200_000_000, inUse: 110_000_000, want: true},
		{name: "When fragmentation is below threshold, it should not defrag", size: 200_000_000, inUse: 110_020_000},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			controller := &DefragController{log: logr.Discard()}
			var endpointStatus *clientv3.StatusResponse
			if !testCase.missing {
				endpointStatus = &clientv3.StatusResponse{DbSize: testCase.size, DbSizeInUse: testCase.inUse}
			}
			if actual := controller.isEndpointBackendFragmented(&etcdserverpb.Member{ClientURLs: []string{"https://etcd-0:2379"}}, endpointStatus); actual != testCase.want {
				t.Errorf("fragmented=%v, want %v", actual, testCase.want)
			}
		})
	}
}

func TestCheckClusterHealth(t *testing.T) {
	t.Parallel()
	t.Run("When the default client cannot retrieve endpoints, it should preserve the health error", func(t *testing.T) {
		t.Parallel()
		endpointError := errors.New("endpoints unavailable")
		controller := &DefragController{etcdClient: etcdcli.NewEtcdClient(func() ([]string, error) { return nil, endpointError }, events.NewLoggingEventRecorder("test", clock.RealClock{}))}
		healthy, healthStatus, err := controller.checkClusterHealth(context.Background())
		if healthy || healthStatus != "" || !errors.Is(err, endpointError) {
			t.Fatalf("unexpected default health: %v %q %v", healthy, healthStatus, err)
		}
	})
	for _, testCase := range []struct {
		name    string
		healthy bool
		err     error
	}{
		{name: "When the cluster is healthy, it should preserve its health status", healthy: true},
		{name: "When the cluster is unhealthy, it should preserve its health status"},
		{name: "When health retrieval fails, it should return the error", err: errors.New("health error")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			controller := &DefragController{clusterHealth: func(context.Context) (bool, string, error) { return testCase.healthy, "cluster status", testCase.err }}
			healthy, healthStatus, err := controller.checkClusterHealth(context.Background())
			if healthy != testCase.healthy || healthStatus != "cluster status" || !errors.Is(err, testCase.err) {
				t.Fatalf("unexpected health: %v %q %v", healthy, healthStatus, err)
			}
		})
	}
}
