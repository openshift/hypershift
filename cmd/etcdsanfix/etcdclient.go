package etcdsanfix

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	etcdclient "go.etcd.io/etcd/client/v3"
)

const (
	etcdClientTimeout = 5 * time.Second
	etcdDialTimeout   = 3 * time.Second
)

// etcdClientFromFiles builds a raw clientv3 from the in-pod cert files (in-cluster mode),
// cloned from the etcd-recovery command.
func (o *options) etcdClientFromFiles(endpoint string) (*etcdclient.Client, error) {
	caCert, err := os.ReadFile(o.etcdCAFile)
	if err != nil {
		return nil, fmt.Errorf("read etcd ca %q: %w", o.etcdCAFile, err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caCert)

	clientCert, err := tls.LoadX509KeyPair(o.etcdClientCertFile, o.etcdClientKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load etcd client cert: %w", err)
	}

	return etcdclient.New(etcdclient.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: etcdDialTimeout,
		TLS:         &tls.Config{Certificates: []tls.Certificate{clientCert}, RootCAs: pool},
	})
}

func (o *options) memberList(ctx context.Context) ([]*etcdserverpb.Member, error) {
	cli, _, cleanup, err := o.etcdDial(ctx, "")
	if err != nil {
		return nil, err
	}
	defer cleanup()
	rctx, cancel := context.WithTimeout(ctx, etcdClientTimeout)
	defer cancel()
	resp, err := cli.MemberList(rctx)
	if err != nil {
		return nil, err
	}
	return resp.Members, nil
}

func (o *options) memberAdd(ctx context.Context, member string) (uint64, error) {
	cli, _, cleanup, err := o.etcdDial(ctx, "")
	if err != nil {
		return 0, err
	}
	defer cleanup()
	rctx, cancel := context.WithTimeout(ctx, etcdClientTimeout)
	defer cancel()
	peerURL := fmt.Sprintf("https://%s.etcd-discovery.%s.svc:2380", member, o.namespace)
	resp, err := cli.MemberAdd(rctx, []string{peerURL})
	if err != nil {
		return 0, err
	}
	return resp.Member.ID, nil
}

// endpointHealthy mirrors `etcdctl endpoint health` for one member, named by pod.
func (o *options) endpointHealthy(ctx context.Context, member string) bool {
	cli, _, cleanup, err := o.etcdDial(ctx, member)
	if err != nil {
		return false
	}
	defer cleanup()
	return clientHealthy(ctx, cli)
}

// clientHealthy runs the health probe on an already-connected client: a Get plus an
// empty alarm list. Shared so health gathering can reuse one connection per member.
func clientHealthy(ctx context.Context, cli *etcdclient.Client) bool {
	hctx, cancel := context.WithTimeout(ctx, etcdClientTimeout)
	defer cancel()
	if _, err := cli.Get(hctx, "health"); err != nil && err != rpctypes.ErrPermissionDenied {
		return false
	}
	actx, cancel2 := context.WithTimeout(ctx, etcdClientTimeout)
	defer cancel2()
	resp, err := cli.AlarmList(actx)
	if err != nil {
		return false
	}
	return len(resp.Alarms) == 0
}

func (o *options) alarmList(ctx context.Context) ([]*etcdserverpb.AlarmMember, error) {
	cli, _, cleanup, err := o.etcdDial(ctx, "")
	if err != nil {
		return nil, err
	}
	defer cleanup()
	rctx, cancel := context.WithTimeout(ctx, etcdClientTimeout)
	defer cancel()
	resp, err := cli.AlarmList(rctx)
	if err != nil {
		return nil, err
	}
	return resp.Alarms, nil
}
