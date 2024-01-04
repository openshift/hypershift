package etcdproxy

import (
	"bytes"
	"context"
	"sync"

	supportapi "github.com/openshift/hypershift/support/api"
	pb "go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/proxy/grpcproxy"
	"go.uber.org/zap"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
)

type kvProxyWrapper struct {
	pb.KVServer
	logger                *zap.Logger
	allowAPIServiceWrites bool
	m                     sync.Mutex
}

func NewKvProxyWrapper(lg *zap.Logger, c *clientv3.Client, allowAPIServiceWrites chan struct{}) pb.KVServer {
	inner, _ := grpcproxy.NewKvProxy(c)
	wrapper := &kvProxyWrapper{
		KVServer: inner,
		logger:   lg,
	}
	go func() {
		<-allowAPIServiceWrites
		wrapper.setAllowAPIServiceWrites()
	}()
	return wrapper
}

func (p *kvProxyWrapper) setAllowAPIServiceWrites() {
	p.m.Lock()
	defer p.m.Unlock()
	p.allowAPIServiceWrites = true
}

func (p *kvProxyWrapper) shouldAllowAPIServiceWrites() bool {
	p.m.Lock()
	defer p.m.Unlock()
	return p.allowAPIServiceWrites
}

func (p *kvProxyWrapper) Txn(ctx context.Context, r *pb.TxnRequest) (*pb.TxnResponse, error) {
	for i, op := range r.Success {
		if putRequest := op.GetRequestPut(); putRequest != nil {
			if bytes.HasPrefix(putRequest.Key, []byte("/kubernetes.io/apiregistration.k8s.io/apiservices/")) {
				if !p.shouldAllowAPIServiceWrites() {
					apiService := &apiregistrationv1.APIService{}
					gvk := apiregistrationv1.SchemeGroupVersion.WithKind("APIService")
					if _, _, err := supportapi.JsonSerializer.Decode(putRequest.Value, &gvk, apiService); err != nil {
						p.logger.Error("failed to unmarshal api service", zap.Error(err))
					}
					if apiService.Spec.Service != nil {
						p.logger.Info("ignoring non-local apiservice put request", zap.ByteString("key", putRequest.Key))
						r.Success[i].GetRequestPut().IgnoreValue = true
					}
				}
			}
		}
	}
	return p.KVServer.Txn(ctx, r)
}
