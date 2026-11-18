package etcdsanfix

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	etcdv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/etcd"
	"github.com/openshift/hypershift/support/config"
	hyperutil "github.com/openshift/hypershift/support/util"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// reconcilePeerCertBridge mints the per-member-SAN peer cert using the in-tree pki
// reconciler (the #10161 logic) and patches etcd-peer-tls. It reuses the exact
// function the CPO uses, so the bridge cert matches the released fix. The replica
// count comes from etcdv2.Replicas(hcp), the same authority the StatefulSet uses.
func reconcilePeerCertBridge(ctx context.Context, c crclient.Client, namespace string) error {
	signer := manifests.EtcdSignerSecret(namespace)
	if err := c.Get(ctx, crclient.ObjectKeyFromObject(signer), signer); err != nil {
		return fmt.Errorf("get etcd-signer secret: %w", err)
	}

	hcp, err := getHCP(ctx, c, namespace)
	if err != nil {
		return err
	}
	replicas := etcdv2.Replicas(hcp)

	peer := manifests.EtcdPeerSecret(namespace)
	return hyperutil.UpdateObject(ctx, c, peer, func() error {
		return pki.ReconcileEtcdPeerSecret(peer, signer, config.OwnerRef{}, replicas)
	})
}
