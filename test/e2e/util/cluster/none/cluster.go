package none

import (
	"context"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/none"
	"github.com/openshift/hypershift/test/e2e/util/cluster/aws"
)

type None struct {
	t    *testing.T
	opts core.CreateOptions
}

func (n None) Describe() interface{} {
	return n.opts
}

func (n None) CreateCluster(ctx context.Context, hc *hyperv1.HostedCluster) error {
	n.opts.Namespace = hc.Namespace
	n.opts.Name = hc.Name
	return none.CreateCluster(ctx, &n.opts)
}

func (n None) DumpCluster(ctx context.Context, hc *hyperv1.HostedCluster, artifactsDir string) {
	aws.DumpHostedCluster(n.t, ctx, hc, artifactsDir)
}

func (n None) DestroyCluster(ctx context.Context, hc *hyperv1.HostedCluster) error {
	opts := &core.DestroyOptions{
		Namespace:          hc.Namespace,
		Name:               hc.Name,
		ClusterGracePeriod: 15 * time.Minute,
	}
	return none.DestroyCluster(ctx, opts)
}

func New(t *testing.T, opts core.CreateOptions) *None {
	return &None{opts: opts, t: t}
}
