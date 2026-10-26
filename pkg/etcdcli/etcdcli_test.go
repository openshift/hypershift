package etcdcli

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type defragTestServer struct {
	etcdserverpb.UnimplementedMaintenanceServer
	etcdserverpb.UnimplementedClusterServer
	defragment func(context.Context) (*etcdserverpb.DefragmentResponse, error)
}

func (server *defragTestServer) MemberList(context.Context, *etcdserverpb.MemberListRequest) (*etcdserverpb.MemberListResponse, error) {
	return &etcdserverpb.MemberListResponse{}, nil
}

func (server *defragTestServer) Defragment(ctx context.Context, _ *etcdserverpb.DefragmentRequest) (*etcdserverpb.DefragmentResponse, error) {
	return server.defragment(ctx)
}

func TestDefragment(t *testing.T) {
	t.Parallel()
	ordinaryError := status.Error(codes.Unavailable, "defrag failed")
	for _, testCase := range []struct {
		name              string
		timeout           time.Duration
		parentTimeout     time.Duration
		cancelOnEntry     bool
		preCanceled       bool
		stall             bool
		serverError       error
		constructionError bool
		wantError         error
	}{
		{name: "When server responds immediately, it should use the 60 second default and succeed"},
		{name: "When a connected server stalls, it should enforce the request timeout", timeout: 100 * time.Millisecond, stall: true, wantError: context.DeadlineExceeded},
		{name: "When caller has an earlier deadline, it should preserve that deadline", timeout: time.Second, parentTimeout: 500 * time.Millisecond, stall: true, wantError: context.DeadlineExceeded},
		{name: "When caller cancels during the RPC, it should release the request", cancelOnEntry: true, stall: true, wantError: context.Canceled},
		{name: "When caller is already canceled, it should not reach the server", preCanceled: true, wantError: context.Canceled},
		{name: "When server returns an ordinary error, it should preserve the error", serverError: ordinaryError, wantError: ordinaryError},
		{name: "When client construction fails, it should return the construction error", constructionError: true, wantError: ordinaryError},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer func() { cancel() }()
			if testCase.preCanceled {
				cancel()
			}
			entered := make(chan time.Duration, 1)
			serverDone := make(chan struct{}, 1)
			server := &defragTestServer{defragment: func(serverCtx context.Context) (*etcdserverpb.DefragmentResponse, error) {
				defer func() { serverDone <- struct{}{} }()
				deadline, bounded := serverCtx.Deadline()
				if !bounded {
					return nil, status.Error(codes.Internal, "request has no deadline")
				}
				entered <- time.Until(deadline)
				if testCase.cancelOnEntry {
					cancel()
				}
				if testCase.stall {
					<-serverCtx.Done()
					return nil, serverCtx.Err()
				}
				return &etcdserverpb.DefragmentResponse{}, testCase.serverError
			}}
			listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			grpcServer := grpc.NewServer()
			etcdserverpb.RegisterMaintenanceServer(grpcServer, server)
			etcdserverpb.RegisterClusterServer(grpcServer, server)
			serveDone := make(chan struct{})
			go func() { defer close(serveDone); _ = grpcServer.Serve(listener) }()
			t.Cleanup(func() {
				grpcServer.Stop()
				select {
				case <-serveDone:
				case <-time.After(3 * time.Second):
					t.Error("server did not stop")
				}
			})
			var createdClient *clientv3.Client
			getter := &etcdClientGetter{defragTimeout: testCase.timeout, newDefragClient: func(endpoints []string, skipCheck bool, opts ...ClientOption) (*clientv3.Client, error) {
				if testCase.constructionError {
					return nil, ordinaryError
				}
				clientOpts, err := newClientOpts(opts...)
				if err != nil {
					return nil, err
				}
				if skipCheck || clientOpts.dialTimeout != DefragDialTimeout {
					t.Error("defrag client preparation changed")
				}
				client, err := clientv3.New(clientv3.Config{Endpoints: endpoints, DialTimeout: 10 * time.Millisecond})
				if err != nil {
					return nil, err
				}
				createdClient = client
				t.Cleanup(func() { _ = client.Close() })
				checkCtx, checkCancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer checkCancel()
				if _, err := client.MemberList(checkCtx); err != nil {
					return nil, err
				}
				return client, nil
			}}
			endpoint := "http://" + listener.Addr().String()
			if testCase.parentTimeout > 0 {
				preparedClient, err := getter.newDefragClient([]string{endpoint}, false, WithDialTimeout(DefragDialTimeout))
				if err != nil {
					t.Fatal(err)
				}
				getter.newDefragClient = func([]string, bool, ...ClientOption) (*clientv3.Client, error) { return preparedClient, nil }
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), testCase.parentTimeout)
			}
			watchdog := time.AfterFunc(3*time.Second, cancel)
			defer watchdog.Stop()
			started := time.Now()
			_, err = getter.Defragment(ctx, &etcdserverpb.Member{ClientURLs: []string{endpoint}})
			if errors.Is(testCase.wantError, ordinaryError) {
				if status.Code(err) != codes.Unavailable && !errors.Is(err, ordinaryError) {
					t.Fatalf("expected unavailable error, got %v", err)
				}
			} else if !errors.Is(err, testCase.wantError) {
				t.Fatalf("expected %v, got %v", testCase.wantError, err)
			}
			if createdClient != nil {
				select {
				case <-createdClient.Ctx().Done():
				default:
					t.Error("uncached client was not closed")
				}
			}
			if testCase.preCanceled || testCase.constructionError {
				select {
				case <-entered:
					t.Error("unexpected defrag request")
				default:
				}
				return
			}
			assertDefragDeadline(t, entered, testCase.timeout, testCase.parentTimeout)
			select {
			case <-serverDone:
			case <-time.After(3 * time.Second):
				t.Fatal("handler did not finish")
			}
			if time.Since(started) > 3*time.Second {
				t.Error("request exceeded test safety bound")
			}
		})
	}
}

func assertDefragDeadline(t *testing.T, entered <-chan time.Duration, timeout, parentTimeout time.Duration) {
	t.Helper()
	select {
	case remaining := <-entered:
		budget := timeout
		if budget == 0 {
			budget = 60 * time.Second
		}
		if parentTimeout > 0 {
			budget = parentTimeout
		}
		if remaining <= 0 || remaining > budget {
			t.Errorf("server deadline remaining %v exceeds budget %v", remaining, budget)
		}
		if timeout == 0 && parentTimeout == 0 && remaining < 55*time.Second {
			t.Errorf("default request budget too short: %v", remaining)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server never received defrag")
	}
}
