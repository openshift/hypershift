package etcdcli

import (
	"context"

	"go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	EtcdMemberStatusAvailable  = "EtcdMemberAvailable"
	EtcdMemberStatusNotStarted = "EtcdMemberNotStarted"
	EtcdMemberStatusUnhealthy  = "EtcdMemberUnhealthy"
	EtcdMemberStatusUnknown    = "EtcdMemberUnknown"
)

type EtcdClient interface {
	Defragment
	MemberAdder
	MemberPromoter
	MemberHealth
	IsMemberHealthy
	MemberLister
	MemberRemover
	HealthyMemberLister
	UnhealthyMemberLister
	MemberStatusChecker
	Status

	GetMember(ctx context.Context, name string) (*etcdserverpb.Member, error)
	MemberUpdatePeerURL(ctx context.Context, id uint64, peerURL []string) error
}

type Defragment interface {
	Defragment(ctx context.Context, member *etcdserverpb.Member) (*clientv3.DefragmentResponse, error)
}

type Status interface {
	Status(ctx context.Context, target string) (*clientv3.StatusResponse, error)
}

type MemberAdder interface {
	MemberAddAsLearner(ctx context.Context, peerURL string) error
}

type MemberPromoter interface {
	MemberPromote(ctx context.Context, member *etcdserverpb.Member) error
}

type MemberHealth interface {
	MemberHealth(ctx context.Context) (memberHealth, error)
}
type IsMemberHealthy interface {
	IsMemberHealthy(ctx context.Context, member *etcdserverpb.Member) (bool, error)
}
type MemberRemover interface {
	MemberRemove(ctx context.Context, memberID uint64) error
}

type MemberLister interface {
	// MemberList lists all members in a cluster
	MemberList(ctx context.Context) ([]*etcdserverpb.Member, error)
	// VotingMemberList lists all non learner members in a cluster
	VotingMemberList(ctx context.Context) ([]*etcdserverpb.Member, error)
}

type HealthyMemberLister interface {
	// HealthyMembers lists all healthy members in a cluster
	HealthyMembers(ctx context.Context) ([]*etcdserverpb.Member, error)
	// HealthyVotingMembers lists all non learner healthy members in a cluster
	HealthyVotingMembers(ctx context.Context) ([]*etcdserverpb.Member, error)
}

type UnhealthyMemberLister interface {
	// UnhealthyMembers lists all unhealthy members in a cluster
	UnhealthyMembers(ctx context.Context) ([]*etcdserverpb.Member, error)
	// UnhealthyVotingMembers lists all non learner unhealthy members in a cluster
	UnhealthyVotingMembers(ctx context.Context) ([]*etcdserverpb.Member, error)
}

type MemberStatusChecker interface {
	MemberStatus(ctx context.Context, member *etcdserverpb.Member) string
}
