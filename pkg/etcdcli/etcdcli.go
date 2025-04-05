package etcdcli

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/openshift/library-go/pkg/operator/events"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"

	grpcprom "github.com/grpc-ecosystem/go-grpc-prometheus"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/client/pkg/v3/logutil"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/etcdserver"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
)

const (
	DefaultDialTimeout   = 15 * time.Second
	DefragDialTimeout    = 60 * time.Second
	DefaultClientTimeout = 30 * time.Second
)

type etcdClientGetter struct {
	eventRecorder events.Recorder

	clientPool *EtcdClientPool
}

func NewEtcdClient(endpointsFunc func() ([]string, error), eventRecorder events.Recorder) EtcdClient {
	g := &etcdClientGetter{
		eventRecorder: eventRecorder.WithComponentSuffix("etcd-client"),
	}
	newFunc := func() (*clientv3.Client, error) {
		endpoints, err := endpointsFunc()
		if err != nil {
			return nil, fmt.Errorf("error retrieving endpoints for new cached client: %w", err)
		}
		return newEtcdClientWithClientOpts(endpoints, true)
	}

	g.clientPool = NewDefaultEtcdClientPool(newFunc, endpointsFunc)
	return g
}

// newEtcdClientWithClientOpts allows customization of the etcd client using ClientOptions. All clients must be manually
// closed by the caller with Close().
func newEtcdClientWithClientOpts(endpoints []string, skipConnectionTest bool, opts ...ClientOption) (*clientv3.Client, error) {
	grpclog.SetLoggerV2(grpclog.NewLoggerV2(io.Discard, io.Discard, os.Stderr))
	clientOpts, err := newClientOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("error during clientOpts: %w", err)
	}

	dialOptions := []grpc.DialOption{
		grpc.WithChainUnaryInterceptor(grpcprom.UnaryClientInterceptor),
		grpc.WithChainStreamInterceptor(grpcprom.StreamClientInterceptor),
	}

	// IAN: these are hypershift specific locations.
	tlsInfo := transport.TLSInfo{
		CertFile:      "/etc/etcd/tls/client/etcd-client.crt",
		KeyFile:       "/etc/etcd/tls/client/etcd-client.key",
		TrustedCAFile: "/etc/etcd/tls/etcd-ca/ca.crt",
	}
	tlsConfig, err := tlsInfo.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("error during client TLSConfig: %w", err)
	}

	// Our logs are noisy
	lcfg := logutil.DefaultZapLoggerConfig
	lcfg.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	l, err := lcfg.Build()
	if err != nil {
		return nil, fmt.Errorf("failed building client logger: %w", err)
	}

	cfg := &clientv3.Config{
		DialOptions: dialOptions,
		Endpoints:   endpoints,
		DialTimeout: clientOpts.dialTimeout,
		TLS:         tlsConfig,
		Logger:      l,
	}

	cli, err := clientv3.New(*cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to make etcd client for endpoints %v: %w", endpoints, err)
	}

	// If the endpoint includes a learner member then we skip the test
	// as learner members don't support member list
	if skipConnectionTest {
		return cli, err
	}

	// Test client connection.
	ctx, cancel := context.WithTimeout(context.Background(), DefaultClientTimeout)
	defer cancel()
	_, err = cli.MemberList(ctx)
	if err != nil {
		if clientv3.IsConnCanceled(err) {
			return nil, fmt.Errorf("client connection was canceled: %w", err)
		}
		return nil, fmt.Errorf("error during client connection check: %w", err)
	}

	return cli, err
}

func (g *etcdClientGetter) MemberAddAsLearner(ctx context.Context, peerURL string) error {
	cli, err := g.clientPool.Get()
	if err != nil {
		return err
	}

	defer g.clientPool.Return(cli)

	ctx, cancel := context.WithTimeout(ctx, DefaultClientTimeout)
	defer cancel()
	membersResp, err := cli.MemberList(ctx)
	if err != nil {
		return err
	}

	for _, member := range membersResp.Members {
		for _, currPeerURL := range member.PeerURLs {
			if currPeerURL == peerURL {
				g.eventRecorder.Warningf("MemberAlreadyAdded", "member with peerURL %s already part of the cluster", peerURL)
				return nil
			}
		}
	}

	defer func() {
		if err != nil {
			g.eventRecorder.Warningf("MemberAddAsLearner", "failed to add new member %s: %v", peerURL, err)
		} else {
			g.eventRecorder.Eventf("MemberAddAsLearner", "successfully added new member %s", peerURL)
		}
	}()

	_, err = cli.MemberAddAsLearner(ctx, []string{peerURL})
	return err
}

func (g *etcdClientGetter) MemberPromote(ctx context.Context, member *etcdserverpb.Member) error {
	cli, err := g.clientPool.Get()
	if err != nil {
		return err
	}

	defer g.clientPool.Return(cli)

	defer func() {
		if err != nil {
			// Not being ready for promotion can be a common event until the learner's log
			// catches up with the leader, so we don't emit events for failing for that case
			if err.Error() == etcdserver.ErrLearnerNotReady.Error() {
				return
			}
			g.eventRecorder.Warningf("MemberPromote", "failed to promote learner member %s: %v", member.PeerURLs[0], err)
		} else {
			g.eventRecorder.Eventf("MemberPromote", "successfully promoted learner member %v", member.PeerURLs[0])
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, DefaultClientTimeout)
	defer cancel()
	_, err = cli.MemberPromote(ctx, member.ID)
	return err
}

func (g *etcdClientGetter) MemberUpdatePeerURL(ctx context.Context, id uint64, peerURLs []string) error {
	if members, err := g.MemberList(ctx); err != nil {
		g.eventRecorder.Eventf("MemberUpdate", "updating member %d with peers %v", id, strings.Join(peerURLs, ","))
	} else {
		memberName := fmt.Sprintf("%d", id)
		for _, member := range members {
			if member.ID == id {
				memberName = member.Name
				break
			}
		}
		g.eventRecorder.Eventf("MemberUpdate", "updating member %q with peers %v", memberName, strings.Join(peerURLs, ","))
	}

	cli, err := g.clientPool.Get()
	if err != nil {
		return err
	}

	defer g.clientPool.Return(cli)

	ctx, cancel := context.WithTimeout(ctx, DefaultClientTimeout)
	defer cancel()
	_, err = cli.MemberUpdate(ctx, id, peerURLs)
	if err != nil {
		return err
	}
	return err
}

func (g *etcdClientGetter) MemberRemove(ctx context.Context, memberID uint64) error {
	cli, err := g.clientPool.Get()
	if err != nil {
		return err
	}

	defer g.clientPool.Return(cli)

	ctx, cancel := context.WithTimeout(ctx, DefaultClientTimeout)
	defer cancel()
	_, err = cli.MemberRemove(ctx, memberID)
	if err == nil {
		g.eventRecorder.Eventf("MemberRemove", "removed member with ID: %v", memberID)
	}
	return err
}

func (g *etcdClientGetter) MemberList(ctx context.Context) ([]*etcdserverpb.Member, error) {
	cli, err := g.clientPool.Get()
	if err != nil {
		return nil, err
	}

	defer g.clientPool.Return(cli)

	ctx, cancel := context.WithTimeout(ctx, DefaultClientTimeout)
	defer cancel()
	membersResp, err := cli.MemberList(ctx)
	if err != nil {
		return nil, err
	}

	return membersResp.Members, nil
}

func (g *etcdClientGetter) VotingMemberList(ctx context.Context) ([]*etcdserverpb.Member, error) {
	members, err := g.MemberList(ctx)
	if err != nil {
		return nil, err
	}
	return filterVotingMembers(members), nil
}

// Status reports etcd endpoint status of client URL target. Example https://10.0.10.1:2379
func (g *etcdClientGetter) Status(ctx context.Context, clientURL string) (*clientv3.StatusResponse, error) {
	cli, err := g.clientPool.Get()
	if err != nil {
		return nil, err
	}

	defer g.clientPool.Return(cli)
	ctx, cancel := context.WithTimeout(ctx, DefaultClientTimeout)
	defer cancel()
	return cli.Status(ctx, clientURL)
}

func (g *etcdClientGetter) GetMember(ctx context.Context, name string) (*etcdserverpb.Member, error) {
	members, err := g.MemberList(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range members {
		if m.Name == name {
			return m, nil
		}
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{Group: "etcd.operator.openshift.io", Resource: "etcdmembers"}, name)
}

// GetMemberNameOrHost If the member's name is not set, extract ip/hostname from peerURL. Useful with unstarted members.
func GetMemberNameOrHost(member *etcdserverpb.Member) string {
	if len(member.Name) == 0 {
		u, err := url.Parse(member.PeerURLs[0])
		if err != nil {
			klog.Errorf("unstarted member has invalid peerURL: %#v", err)
			return "NAME-PENDING-BAD-PEER-URL"
		}
		return fmt.Sprintf("NAME-PENDING-%s", u.Hostname())
	}
	return member.Name
}

func (g *etcdClientGetter) UnhealthyMembers(ctx context.Context) ([]*etcdserverpb.Member, error) {
	cli, err := g.clientPool.Get()
	if err != nil {
		return nil, err
	}

	defer g.clientPool.Return(cli)

	ctx, cancel := context.WithTimeout(ctx, DefaultClientTimeout)
	defer cancel()
	etcdCluster, err := cli.MemberList(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not get member list %v", err)
	}

	memberHealth := getMemberHealth(ctx, etcdCluster.Members)

	unstartedMemberNames := GetUnstartedMemberNames(memberHealth)
	if len(unstartedMemberNames) > 0 {
		g.eventRecorder.Warningf("UnstartedEtcdMember", "unstarted members: %v", strings.Join(unstartedMemberNames, ","))
	}

	unhealthyMemberNames := GetUnhealthyMemberNames(memberHealth)
	if len(unhealthyMemberNames) > 0 {
		g.eventRecorder.Warningf("UnhealthyEtcdMember", "unhealthy members: %v", strings.Join(unhealthyMemberNames, ","))
	}

	return memberHealth.GetUnhealthyMembers(), nil
}

func (g *etcdClientGetter) UnhealthyVotingMembers(ctx context.Context) ([]*etcdserverpb.Member, error) {
	unhealthyMembers, err := g.UnhealthyMembers(ctx)
	if err != nil {
		return nil, err
	}
	return filterVotingMembers(unhealthyMembers), nil
}

// HealthyMembers performs health check of current members and returns a slice of healthy members and error
// if no healthy members found.
func (g *etcdClientGetter) HealthyMembers(ctx context.Context) ([]*etcdserverpb.Member, error) {
	cli, err := g.clientPool.Get()
	if err != nil {
		return nil, err
	}

	defer g.clientPool.Return(cli)

	ctx, cancel := context.WithTimeout(ctx, DefaultClientTimeout)
	defer cancel()
	etcdCluster, err := cli.MemberList(ctx)
	if err != nil {
		return nil, err
	}

	healthyMembers := getMemberHealth(ctx, etcdCluster.Members).GetHealthyMembers()
	if len(healthyMembers) == 0 {
		return nil, fmt.Errorf("no healthy etcd members found")
	}

	return healthyMembers, nil
}

func (g *etcdClientGetter) HealthyVotingMembers(ctx context.Context) ([]*etcdserverpb.Member, error) {
	healthyMembers, err := g.HealthyMembers(ctx)
	if err != nil {
		return nil, err
	}
	return filterVotingMembers(healthyMembers), nil
}

func (g *etcdClientGetter) MemberHealth(ctx context.Context) (memberHealth, error) {
	cli, err := g.clientPool.Get()
	if err != nil {
		return nil, err
	}

	defer g.clientPool.Return(cli)

	ctx, cancel := context.WithTimeout(ctx, DefaultClientTimeout)
	defer cancel()
	etcdCluster, err := cli.MemberList(ctx)
	if err != nil {
		return nil, err
	}
	return getMemberHealth(ctx, etcdCluster.Members), nil
}

func (g *etcdClientGetter) IsMemberHealthy(ctx context.Context, member *etcdserverpb.Member) (bool, error) {
	if member == nil {
		return false, fmt.Errorf("member can not be nil")
	}
	memberHealth := getMemberHealth(ctx, []*etcdserverpb.Member{member})
	if len(memberHealth) == 0 {
		return false, fmt.Errorf("member health check failed")
	}
	if memberHealth[0].Healthy {
		return true, nil
	}

	return false, nil
}

func (g *etcdClientGetter) MemberStatus(ctx context.Context, member *etcdserverpb.Member) string {
	cli, err := g.clientPool.Get()
	if err != nil {
		klog.Errorf("error getting etcd client: %#v", err)
		return EtcdMemberStatusUnknown
	}
	defer g.clientPool.Return(cli)

	if len(member.ClientURLs) == 0 && member.Name == "" {
		return EtcdMemberStatusNotStarted
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultClientTimeout)
	defer cancel()
	_, err = cli.Status(ctx, member.ClientURLs[0])
	if err != nil {
		klog.Errorf("error getting etcd member %s status: %#v", member.Name, err)
		return EtcdMemberStatusUnhealthy
	}

	return EtcdMemberStatusAvailable
}

// Defragment creates a new uncached clientv3 to the given member url and calls clientv3.Client.Defragment.
func (g *etcdClientGetter) Defragment(ctx context.Context, member *etcdserverpb.Member) (*clientv3.DefragmentResponse, error) {
	// no g.clientLock necessary, this always returns a new fresh client
	cli, err := newEtcdClientWithClientOpts([]string{member.ClientURLs[0]}, false, WithDialTimeout(DefragDialTimeout))
	if err != nil {
		return nil, fmt.Errorf("failed to get etcd client for defragment: %w", err)
	}
	defer func() {
		if cli == nil {
			return
		}
		if err := cli.Close(); err != nil {
			klog.Errorf("error closing etcd client for defrag: %v", err)
		}
	}()

	resp, err := cli.Defragment(ctx, member.ClientURLs[0])
	if err != nil {
		return nil, fmt.Errorf("error while running defragment: %w", err)
	}
	return resp, nil
}

// filterVotingMembers filters out learner members
func filterVotingMembers(members []*etcdserverpb.Member) []*etcdserverpb.Member {
	var votingMembers []*etcdserverpb.Member
	for _, member := range members {
		if member.IsLearner {
			continue
		}
		votingMembers = append(votingMembers, member)
	}
	return votingMembers
}
