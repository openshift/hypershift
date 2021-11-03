package cluster

import (
	"context"

	"github.com/openshift/hypershift/api/v1alpha1"
)

type Cluster interface {
	Describe() interface{}
	CreateCluster(ctx context.Context, hc *v1alpha1.HostedCluster) error
	DumpCluster(ctx context.Context, hc *v1alpha1.HostedCluster, artifactsDir string)
	DestroyCluster(ctx context.Context, hc *v1alpha1.HostedCluster) error
}
