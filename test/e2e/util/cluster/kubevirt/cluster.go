package kubevirt

import (
	"context"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/kubevirt"
	"github.com/openshift/hypershift/cmd/cluster/none"
	"github.com/openshift/hypershift/test/e2e/util/cluster/aws"
)

type KubeVirt struct {
	t    *testing.T
	opts core.CreateOptions
}

func (k *KubeVirt) Describe() interface{} {
	return k.opts
}

func (k *KubeVirt) CreateCluster(ctx context.Context, hc *hyperv1.HostedCluster) error {
	k.opts.Namespace = hc.Namespace
	k.opts.Name = hc.Name
	return kubevirt.CreateCluster(ctx, &k.opts)
}

func (k *KubeVirt) DumpCluster(ctx context.Context, hc *hyperv1.HostedCluster, artifactsDir string) {
	aws.DumpHostedCluster(k.t, ctx, hc, artifactsDir)
}

func (k *KubeVirt) DestroyCluster(ctx context.Context, hc *hyperv1.HostedCluster) error {
	opts := &core.DestroyOptions{
		Namespace:          hc.Namespace,
		Name:               hc.Name,
		ClusterGracePeriod: 15 * time.Minute,
	}
	return none.DestroyCluster(ctx, opts)
}

func New(t *testing.T, opts core.CreateOptions) *KubeVirt {
	return &KubeVirt{opts: opts, t: t}
}
